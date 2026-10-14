package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func check(e error) {
	if e != nil {
		app.Stop()
		panic(e)
	}
}

func createCommandFile(cmdString string) {
	file, err := os.OpenFile(projectPath+"/._commands", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	check(err)
	defer file.Close()

	_, err = file.WriteString(cmdString)
	check(err)
}

// containerName is the name of the container this session runs every command
// in. It has to stay the same for as long as the container lives: podman exec
// addresses the very container podman run created, so a name that changed per
// command would have nothing to run in. The process id keeps two instances of
// the app - and a container a crashed run left behind - from taking each
// other's name.
var containerName = containerNameFor(os.Getpid())

// containerNameFor builds that name. Letters, digits and dashes only: the name
// is handed to the container tool on a command line, and a name that could be
// read as anything else is a container no command could reach.
func containerNameFor(pid int) string {
	return fmt.Sprintf("kubespray-%d", pid)
}

// containerSpecLabel marks the containers this app makes, and carries what they
// were made with.
const containerSpecLabel = "io.idocluster.spec"

// podmanEnv is the environment every podman invocation needs. A host on cgroups
// v1 gets podman's deprecation warning on stderr with every call: noise in the
// output a check reads, and - during the playbook run, whose stderr is the
// terminal - lines written over the TUI. Podman 5 has no configuration for it,
// only this variable, and it has to be set for the podman process itself. It
// goes through `env` because sudo resets the environment, and `sudo VAR=value`
// depends on the sudoers policy.
const podmanEnv = "PODMAN_IGNORE_CGROUPSV1_WARNING=true"

// sudoPodman builds the command line that runs the container tool as root with
// that environment.
func sudoPodman(args string) string {
	return "sudo env " + podmanEnv + " " + containerTool + " " + args
}

// containerSpec is what a container has to have been made with to carry this
// session's commands: the image it runs, and the three directories it mounts.
// The mounts are fixed when a container is created, so a container made for
// another project - or from an older image - mounts the wrong directories and
// has to be replaced rather than reused.
func containerSpec(image, appDir, projectDir, offlineDir string) string {
	return image + "|" + appDir + "|" + projectDir + "|" + offlineDir
}

// currentContainerSpec is the spec of the container this session needs now.
func currentContainerSpec() string {
	return containerSpec(kubesprayRuntimeTag, appPath, projectPath, offlinePath)
}

// containerRunArgv builds the command that creates the container every command
// runs in. It is detached (-d) and keeps no --rm: the container has to outlive
// each command run in it, including one another command is still running in.
// Nothing uses --replace, which deletes a container of the same name - one
// shared name with --replace is what let one SSH check destroy the container
// another check was working in.
//
// The command that keeps it alive is a bash loop rather than "sleep infinity":
// the runtime image is Alpine, whose sleep is busybox's, and older busybox
// builds reject "infinity". It is an argument vector, not a shell string, so no
// shell on the host parses the paths.
func containerRunArgv(tool, name, spec, image, appDir, projectDir, offlineDir string) []string {
	return []string{
		"sudo", "env", podmanEnv, tool, "run", "-d",
		"--privileged", "--network=host",
		"--name", name,
		"--label", containerSpecLabel + "=" + spec,
		"-v", appDir + ":/data/k8s-installer-tui",
		"-v", projectDir + ":/data/idocluster",
		"-v", offlineDir + ":/data/k8s-installer-offline",
		image,
		"/bin/bash", "-c", "while true; do sleep 3600; done",
	}
}

// containerExecArgv builds the command that runs cmdString in the container that
// is already there. The command string is one element of the vector, handed to
// the container's own bash: no shell on the host parses it, so a command may
// contain anything - a password, a quote, a dollar sign - and arrives as it was
// written.
func containerExecArgv(tool, name, cmdString string, envs []string) []string {
	argv := []string{"sudo", "env", podmanEnv, tool, "exec"}
	for _, env := range envs {
		argv = append(argv, "-e", env)
	}

	return append(argv, name, "/bin/bash", "-c", cmdString)
}

// containerState is the part of "podman inspect" a reused container is judged
// by: whether it is running, and what it was made with.
type containerState struct {
	State  struct{ Running bool }
	Config struct{ Labels map[string]string }
}

// parseContainerInspect reads those two facts from what "podman inspect"
// printed. A container without the label was not made by this app, so it reads
// as one this session has to replace.
func parseContainerInspect(output []byte) (running bool, spec string, err error) {
	var states []containerState
	if err := json.Unmarshal(output, &states); err != nil {
		return false, "", err
	}
	if len(states) == 0 {
		return false, "", errors.New("the container tool reported no container")
	}

	return states[0].State.Running, states[0].Config.Labels[containerSpecLabel], nil
}

// containerIsCurrent decides whether the container that is already there can
// carry this session's commands. A stopped container runs nothing, and one made
// with other mounts mounts the wrong directories.
func containerIsCurrent(running bool, madeWith, want string) bool {
	return running && madeWith == want
}

// containerAbsentLines are what the container tool says about a container that
// is not there at all. That is the normal state before the first command, so it
// is an answer rather than a failure of the tool.
var containerAbsentLines = []string{
	"no such container",
	"no container with name or id",
	"unable to find container",
	"no such object",
	"does not exist",
}

// containerGoneLines are what it says about a container a command needed and no
// longer has. That is the one failure that says nothing about the command
// itself, so the command is worth running once more against a fresh container.
var containerGoneLines = []string{
	"is not running",
	"container state improper",
}

// outputNames reports whether output holds one of the given lines, ignoring
// case: the container tool's wording is not part of any contract.
func outputNames(output []byte, lines []string) bool {
	lower := strings.ToLower(string(output))
	for _, line := range lines {
		if strings.Contains(lower, line) {
			return true
		}
	}

	return false
}

// containerWasAbsent reports whether the container tool said there is no such
// container.
func containerWasAbsent(output []byte) bool { return outputNames(output, containerAbsentLines) }

// containerWasGone reports whether a failed command failed because the container
// it ran in was gone - removed by the Abort button, or replaced when the project
// changed.
func containerWasGone(output []byte) bool {
	return containerWasAbsent(output) || outputNames(output, containerGoneLines)
}

// removeLeftoverContainers deletes the containers an earlier run of this app
// left behind. A container that outlived its run is worse than stale when it is
// an Ansible run: it keeps working on a cluster nobody is watching. Only the
// containers this app made carry the label, so nothing else can be hit, and a
// failure here is not fatal - the next start tries again.
func removeLeftoverContainers() {
	output, err := execCommand(sudoPodman("ps -a --filter label="+containerSpecLabel+" --format '{{.Names}}'"), 0, false)
	if err != nil {
		// A build that does not take a label filter gets what the Abort button
		// already does: this container tool's storage is its own, so removing
		// all of its containers touches nothing outside this app.
		execCommand(sudoPodman("rm -a -f"), 0, false)
		return
	}

	for _, name := range containerNamesFromPs(output) {
		execCommand(sudoPodman("rm -f "+name), 0, false)
	}
}

// containerNamesFromPs keeps the names of a "podman ps" listing, and only the
// names this app gives its containers: the sweep must not be able to touch
// anything else.
func containerNamesFromPs(output []byte) []string {
	var names []string
	for _, field := range strings.Fields(string(output)) {
		if strings.HasPrefix(field, "kubespray-") {
			names = append(names, field)
		}
	}

	return names
}

var containerMu sync.Mutex

// containerUpWith is the spec of the container this process is known to have
// running, empty when none is. It is read before the container tool is asked -
// the SSH checks run ten commands at a time, and each of them would otherwise
// start a container of its own.
var containerUpWith atomic.Pointer[string]

// forgetContainer records that no container is known to be up, so the next
// command looks for one again and makes a new one when the old was removed.
func forgetContainer() {
	containerUpWith.Store(nil)
}

// ensureContainer makes sure a running container made with the current mounts is
// there, and returns the failure of that when it cannot be.
func ensureContainer() error {
	want := currentContainerSpec()
	if up := containerUpWith.Load(); up != nil && *up == want {
		return nil
	}

	containerMu.Lock()
	defer containerMu.Unlock()

	if up := containerUpWith.Load(); up != nil && *up == want {
		return nil
	}

	if err := ensureContainerWith(want, inspectContainer, removeContainer, createContainer); err != nil {
		return err
	}

	containerUpWith.Store(&want)

	return nil
}

// errNoContainer says the container is not there at all. That is the normal
// state before the first command of a session, and it is not a failure of the
// container tool.
var errNoContainer = errors.New("no container")

// ensureContainerWith is ensureContainer without a container tool: the three
// things that touch it are passed in, so that the decision to reuse, replace or
// create the container can be checked without one.
func ensureContainerWith(want string, inspect func() (bool, string, error), remove, create func() error) error {
	running, madeWith, err := inspect()
	if err == nil && containerIsCurrent(running, madeWith, want) {
		return nil
	}

	// A container that is there but wrong - stopped, or made with other mounts
	// - holds the name every command reaches the container by, so it has to go
	// before a new one can take that name. A container that is not there needs
	// no removing, and a tool that failed for any other reason is reported
	// rather than papered over with a new container.
	if err == nil {
		if err := remove(); err != nil {
			return fmt.Errorf("can't remove the container the commands run in: %w", err)
		}
	} else if !errors.Is(err, errNoContainer) {
		return err
	}

	if err := create(); err != nil {
		return fmt.Errorf("can't start the container the commands run in: %w", err)
	}

	return nil
}

// inspectContainer reports whether this session's container is running and what
// it was made with.
func inspectContainer() (bool, string, error) {
	output, err := execCommand(sudoPodman("inspect "+containerName), 0, false)
	if err != nil {
		if containerWasAbsent(output) {
			return false, "", errNoContainer
		}

		return false, "", fmt.Errorf("%s: %s", err, lastLine(output))
	}

	return parseContainerInspect(output)
}

// removeContainer deletes this session's container, and everything running in
// it.
func removeContainer() error {
	output, err := execCommand(sudoPodman("rm -f "+containerName), 0, false)
	if err != nil {
		return fmt.Errorf("%s: %s", err, lastLine(output))
	}

	return nil
}

// createContainer starts the container this session runs its commands in.
func createContainer() error {
	argv := containerRunArgv(containerTool, containerName, currentContainerSpec(), kubesprayRuntimeTag, appPath, projectPath, offlinePath)

	output, err := runArgv(argv, 0)
	if err != nil {
		return fmt.Errorf("%s: %s", err, lastLine(output))
	}

	return nil
}

// lastLine is the line of output worth showing: a tool that failed usually says
// why on the last line it wrote.
func lastLine(output []byte) string {
	text := strings.TrimSpace(string(output))
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return ""
	}

	return strings.TrimSpace(lines[len(lines)-1])
}

// runArgv runs an argument vector, with the whole run limited to timeout seconds
// when timeout is above zero.
func runArgv(argv []string, timeout int) ([]byte, error) {
	if timeout <= 0 {
		return exec.Command(argv[0], argv[1:]...).CombinedOutput()
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}

// inContainerExec runs one command in the container this session runs its
// commands in, making or remaking that container when it is missing, stopped, or
// was made with other mounts. A command that failed because the container went
// away under it is run once more against a fresh container: that failure says
// nothing about the command.
func inContainerExec(cmdString string, timeout int, envs []string) ([]byte, error) {
	if err := ensureContainer(); err != nil {
		return nil, err
	}

	output, err := runArgv(containerExecArgv(containerTool, containerName, cmdString, envs), timeout)
	if err != nil && containerWasGone(output) {
		forgetContainer()
		if ensureErr := ensureContainer(); ensureErr != nil {
			return output, ensureErr
		}

		return runArgv(containerExecArgv(containerTool, containerName, cmdString, envs), timeout)
	}

	return output, err
}

func execCommand(cmdString string, timeout int, inContainer bool, envs ...string) ([]byte, error) {
	if inContainer {
		return inContainerExec(cmdString, timeout, envs)
	}

	var cmd *exec.Cmd
	if timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
		defer cancel()

		cmd = exec.CommandContext(ctx, "/bin/bash", "-c", cmdString)
	} else {
		cmd = exec.Command("/bin/bash", "-c", cmdString)
	}

	for _, env := range envs {
		cmd.Env = append(cmd.Env, env)
	}

	return cmd.CombinedOutput()
}

// commandResult is one run of a command: what it printed, and how it failed.
type commandResult struct {
	output []byte
	err    error
}

// runUntilAnswered runs one stage of an SSH check, and looks at it a second time
// when it came back without an answer. A stage whose container the runtime lost
// leaves no answer behind, and the exit status of a command whose container was
// destroyed under it says nothing about the command. A stage that did answer is
// worked from, whatever its exit status was: a refusal is an answer.
func runUntilAnswered(run func() (answer, output []byte, err error)) commandResult {
	answer, output, err := run()
	if answer != nil {
		return commandResult{output: answer, err: err}
	}

	answer, output, err = run()
	if answer != nil {
		return commandResult{output: answer, err: err}
	}

	return commandResult{output: output, err: err}
}

func execCommandAndCheck(cmdString string, timeout int, inContainer bool, envs ...string) string {
	output, err := execCommand(cmdString, timeout, inContainer, envs...)
	if err != nil {
		app.Stop()
		panic(string(output))
	}
	return string(output)
}

func checkPrivilege() {
	_, err := execCommand("sudo -n true", 5, false)
	if err != nil {
		fmt.Println("Application must run as sudoer.")
		os.Exit(1)
	}
}

// chownToRunner gives path, and everything under it, to the user who invoked
// the app with sudo. The app runs as root, so everything it creates - the
// extracted kubespray tree, the offline materials, the cluster project - would
// otherwise be owned by root and be unusable to that user. It does nothing
// when the app is not started with sudo.
func chownToRunner(path string) error {
	uid, err := strconv.Atoi(os.Getenv("SUDO_UID"))
	if err != nil {
		return nil
	}
	gid, err := strconv.Atoi(os.Getenv("SUDO_GID"))
	if err != nil {
		return nil
	}

	return filepath.WalkDir(path, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

func showErrorModal(text string, handler func(buttonIndex int, buttonLabel string)) {
	modalError.ClearButtons()
	modalError.SetText(text).AddButtons([]string{"OK"}).SetDoneFunc(handler)
	pages.SwitchToPage("Error")
}

func showQuitModal() {
	currentPage, _ := pages.GetFrontPage()

	modalQuit.ClearButtons()
	modalQuit.SetText("Do you want to quit the application?").
		AddButtons([]string{"Quit", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			if buttonLabel == "Cancel" {
				pages.SwitchToPage(currentPage)
			}
			if buttonLabel == "Quit" {
				app.Stop()
			}
		})
	pages.SwitchToPage("Quit")
}

func initLog(prefix string) {
	now := time.Now()
	suffix := fmt.Sprintf("%d%02d%02dT%02d%02d%02d",
		now.Year(), now.Month(), now.Day(),
		now.Hour(), now.Minute(), now.Second())
	logFileName = prefix + suffix + ".log"
}

func Hosts(cidr string) (ips []string, err error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil, err
	}

	for addr := prefix.Addr(); prefix.Contains(addr); addr = addr.Next() {
		ips = append(ips, addr.String())
	}

	if len(ips) < 2 {
		return ips, nil
	}

	return ips[1 : len(ips)-1], nil
}

func groupPing(ips []string) (reachableIps []string, unreachableIps []string) {
	const maxConcurrency = 10

	type pingResult struct {
		ip        string
		reachable bool
	}

	if ips == nil || len(ips) == 0 {
		return
	}

	resultCh := make(chan pingResult)

	groups := int(math.Ceil(float64(len(ips)) / maxConcurrency))

	for i := 0; i < groups; i++ {
		start := i * maxConcurrency
		end := int(math.Min(float64(len(ips)-1), float64((i+1)*maxConcurrency-1)))

		for j := start; j <= end; j++ {
			go func(ip string, resultCh chan pingResult) {
				err := exec.Command("ping", ip, "-c", "2").Run()
				if err != nil {
					resultCh <- pingResult{ip: ip, reachable: false}
				} else {
					resultCh <- pingResult{ip: ip, reachable: true}
				}
			}(ips[j], resultCh)
		}

		var result pingResult
		for j := start; j <= end; j++ {
			result = <-resultCh
			if result.reachable {
				reachableIps = append(reachableIps, result.ip)
			} else {
				unreachableIps = append(unreachableIps, result.ip)
			}
		}
	}

	return reachableIps, unreachableIps
}
