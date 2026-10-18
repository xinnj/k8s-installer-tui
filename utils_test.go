package main

// Tests for the container wrapper: one container carries every in-container
// command of a session, and it is reused, replaced or made on the terms its
// mounts set.

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestRunUntilAnsweredLooksAgainWhenTheStageDidNotAnswer(t *testing.T) {
	calls := 0
	result := runUntilAnswered(func() (answer, output []byte, err error) {
		calls++
		if calls == 1 {
			// A stage whose container the runtime lost comes back without the
			// answer it writes into its file, and without a word from it.
			return nil, nil, nil
		}
		return []byte("uid=0(root)\n"), nil, nil
	})

	if calls != 2 {
		t.Errorf("runUntilAnswered ran a stage that did not answer %d times, want it looked at twice", calls)
	}
	if string(result.output) != "uid=0(root)\n" || result.err != nil {
		t.Errorf("runUntilAnswered = (%q, %v), want the second attempt's answer", result.output, result.err)
	}
}

func TestRunUntilAnsweredKeepsWhatAnswered(t *testing.T) {
	calls := 0
	refused := runUntilAnswered(func() (answer, output []byte, err error) {
		calls++
		// A refusal is an answer, however the command ended.
		return []byte("Permission denied (publickey).\n"), nil, errors.New("exit status 255")
	})

	if calls != 1 {
		t.Errorf("runUntilAnswered ran a stage that answered %d times, want 1", calls)
	}
	if string(refused.output) != "Permission denied (publickey).\n" {
		t.Errorf("runUntilAnswered = %q, want the answer the stage wrote", refused.output)
	}
	if refused.err == nil {
		t.Error("runUntilAnswered dropped the failure of a stage that did answer")
	}
}

func TestRunUntilAnsweredReportsTheLastAttemptWhenNothingAnswers(t *testing.T) {
	calls := 0
	silent := runUntilAnswered(func() (answer, output []byte, err error) {
		calls++
		// The runtime's own output is all a stage that never ran leaves behind.
		return nil, []byte("Error: unable to determine if ... is mounted\n"), nil
	})

	if calls != 2 {
		t.Errorf("runUntilAnswered ran a stage that never answered %d times, want 2", calls)
	}
	if !strings.Contains(string(silent.output), "unable to determine if") {
		t.Errorf("runUntilAnswered = %q, want the last attempt's output when no attempt answered", silent.output)
	}
}

func TestContainerNameIsTheSameForEveryCommand(t *testing.T) {
	// podman exec addresses the very container podman run created, so every
	// command of one process has to name that same container - a name that
	// changed per command would have nothing to run in.
	if first, second := containerNameFor(1234), containerNameFor(1234); first != second {
		t.Errorf("containerNameFor(1234) = %q then %q, want the same name", first, second)
	}

	// Two instances of the app, and a container a crashed run left behind, must
	// not be able to take each other's name.
	if other := containerNameFor(1235); other == containerNameFor(1234) {
		t.Errorf("two processes share the name %q", other)
	}

	if !regexp.MustCompile(`^kubespray-\d+$`).MatchString(containerName) {
		t.Errorf("containerName = %q, want kubespray-<pid>", containerName)
	}
}

func TestContainerNameIsAShellSafeName(t *testing.T) {
	name := containerNameFor(1234)
	if name != "kubespray-1234" {
		t.Errorf("containerNameFor(1234) = %q, want %q", name, "kubespray-1234")
	}

	// The name is handed to the container tool on a command line, so anything a
	// shell or the runtime would read differently must not appear in it.
	if strings.ContainsAny(name, "$`\\\"' ") {
		t.Errorf("container name %q contains a character the shell would treat specially", name)
	}
}

func TestContainerRunArgvIsDetachedAndCarriesTheMounts(t *testing.T) {
	argv := containerRunArgv("/tool/podman", "kubespray-1234", "spec", "image:tag", "/app", "/project", "/offline")
	joined := strings.Join(argv, " ")

	for _, want := range []string{"-d", "--privileged", "--network=host", "image:tag"} {
		if !containsArg(argv, want) {
			t.Errorf("argv %q does not carry %q", joined, want)
		}
	}
	if !containsArgs(argv, "--name", "kubespray-1234") {
		t.Errorf("argv %q does not name the container it makes", joined)
	}
	if !containsArgs(argv, "--label", containerSpecLabel+"=spec") {
		t.Errorf("argv %q does not record what the container was made with", joined)
	}
	for _, mount := range []string{"/app:/data/k8s-installer-tui", "/project:/data/idocluster", "/offline:/data/k8s-installer-offline"} {
		if !containsArgs(argv, "-v", mount) {
			t.Errorf("argv %q does not mount %s", joined, mount)
		}
	}

	// The container has to outlive every command run in it, and it must never
	// delete another container to take a name.
	for _, unwanted := range []string{"--rm", "--replace"} {
		if containsArg(argv, unwanted) {
			t.Errorf("argv %q contains %q", joined, unwanted)
		}
	}
}

func TestContainerExecArgvRunsInTheNamedContainerWithoutStartingANewOne(t *testing.T) {
	const cmd = `echo "$HOME" | tee /tmp/x`

	argv := containerExecArgv("/tool/podman", "kubespray-1234", cmd, []string{"CONFIG_FILE=/tmp/hosts.yaml"})
	joined := strings.Join(argv, " ")

	if !containsArg(argv, "exec") {
		t.Errorf("argv %q does not run a command in the container that is there", joined)
	}
	if containsArg(argv, "run") {
		t.Errorf("argv %q starts a container instead of using the one that is there", joined)
	}
	for _, unwanted := range []string{"--replace", "--rm", "-v"} {
		if containsArg(argv, unwanted) {
			t.Errorf("argv %q contains %q", joined, unwanted)
		}
	}
	if !containsArg(argv, "kubespray-1234") {
		t.Errorf("argv %q does not name the container", joined)
	}
	if !containsArgs(argv, "-e", "CONFIG_FILE=/tmp/hosts.yaml") {
		t.Errorf("argv %q does not pass the environment on", joined)
	}

	// The command is one argument, handed to the container's own bash: no shell
	// on the host parses it, so nothing in it can be rewritten on the way in.
	if got := argv[len(argv)-1]; got != cmd {
		t.Errorf("argv ends with %q, want the command verbatim: %q", got, cmd)
	}
	if bash, dashC := argv[len(argv)-3], argv[len(argv)-2]; bash != "/bin/bash" || dashC != "-c" {
		t.Errorf("argv %q does not run the command with the container's bash", joined)
	}
}

func TestPodmanRunsWithTheCgroupsV1WarningSilenced(t *testing.T) {
	// The host is on cgroups v1 and podman warns about it on every call. The
	// variable has to reach podman itself, and sudo would strip it out of the
	// environment, so it goes through env on sudo's command line.
	for name, argv := range map[string][]string{
		"run":  containerRunArgv("/tool/podman", "kubespray-1234", "spec", "image:tag", "/app", "/project", "/offline"),
		"exec": containerExecArgv("/tool/podman", "kubespray-1234", "true", nil),
	} {
		if !containsArgs(argv, "env", podmanEnv) {
			t.Errorf("%s argv %q does not silence podman's cgroups v1 warning", name, strings.Join(argv, " "))
		}
	}

	if got := sudoPodman("images"); !strings.Contains(got, "sudo env "+podmanEnv+" ") {
		t.Errorf("sudoPodman = %q, want the warning silenced on sudo's command line", got)
	}
}

func TestParseContainerInspectReadsTheRunningStateAndTheSpec(t *testing.T) {
	output := []byte(`[{"State":{"Running":true},"Config":{"Labels":{"io.idocluster.spec":"image|/app|/project|/offline"}}}]`)

	running, spec, err := parseContainerInspect(output)
	if err != nil {
		t.Fatalf("parseContainerInspect: %v", err)
	}
	if !running {
		t.Error("parseContainerInspect reported a running container as stopped")
	}
	if spec != "image|/app|/project|/offline" {
		t.Errorf("spec = %q, want what the container was made with", spec)
	}

	if _, _, err := parseContainerInspect([]byte("[]")); err == nil {
		t.Error("parseContainerInspect accepted a report of no container")
	}
	if _, _, err := parseContainerInspect([]byte("not json")); err == nil {
		t.Error("parseContainerInspect accepted output that is not JSON")
	}
}

func TestContainerIsCurrentRejectsAStoppedOrStaleContainer(t *testing.T) {
	if !containerIsCurrent(true, "spec", "spec") {
		t.Error("containerIsCurrent = false, want a running container made with the same mounts to be reused")
	}
	if containerIsCurrent(false, "spec", "spec") {
		t.Error("containerIsCurrent = true for a stopped container, want it replaced")
	}

	// The mounts are fixed when a container is created, so a container made for
	// another project cannot carry this session's commands.
	if containerIsCurrent(true, "spec", "other") {
		t.Error("containerIsCurrent = true for a container of another project, want it replaced")
	}
}

func TestEnsureContainerReusesTheContainerItMade(t *testing.T) {
	var calls []string

	err := ensureContainerWith("spec",
		func() (bool, string, error) { return true, "spec", nil },
		func() error { calls = append(calls, "remove"); return nil },
		func() error { calls = append(calls, "create"); return nil })

	if err != nil {
		t.Fatalf("ensureContainerWith: %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("calls = %v, want the container that is already right to be left alone", calls)
	}
}

func TestEnsureContainerCreatesTheContainerWhenThereIsNone(t *testing.T) {
	var calls []string

	err := ensureContainerWith("spec",
		func() (bool, string, error) { return false, "", errNoContainer },
		func() error { calls = append(calls, "remove"); return nil },
		func() error { calls = append(calls, "create"); return nil })

	if err != nil {
		t.Fatalf("ensureContainerWith: %v", err)
	}
	if strings.Join(calls, ",") != "create" {
		t.Errorf("calls = %v, want a container made without removing the one that is not there", calls)
	}
}

func TestEnsureContainerReplacesAStaleContainer(t *testing.T) {
	var calls []string

	err := ensureContainerWith("this project",
		func() (bool, string, error) { return true, "another project", nil },
		func() error { calls = append(calls, "remove"); return nil },
		func() error { calls = append(calls, "create"); return nil })

	if err != nil {
		t.Fatalf("ensureContainerWith: %v", err)
	}
	// The name is taken until the stale container is gone.
	if strings.Join(calls, ",") != "remove,create" {
		t.Errorf("calls = %v, want the stale container removed before the new one is made", calls)
	}
}

func TestEnsureContainerReportsAToolThatCannotBeAsked(t *testing.T) {
	var calls []string

	err := ensureContainerWith("spec",
		func() (bool, string, error) { return false, "", errors.New("cannot connect to the runtime") },
		func() error { calls = append(calls, "remove"); return nil },
		func() error { calls = append(calls, "create"); return nil })

	if err == nil {
		t.Error("ensureContainerWith = nil error, want a container tool that cannot be asked to be reported")
	}
	if len(calls) != 0 {
		t.Errorf("calls = %v, want nothing done when the container tool cannot be asked", calls)
	}
}

func TestContainerWasGoneRecognisesTheRuntimesReport(t *testing.T) {
	for _, output := range []string{
		"Error: no such container: kubespray-1234\n",
		"Error: container kubespray-1234 is not running\n",
	} {
		if !containerWasGone([]byte(output)) {
			t.Errorf("containerWasGone(%q) = false, want the command run again against a fresh container", output)
		}
	}

	// A node refusing the login is a result, not a lost container.
	if containerWasGone([]byte("Permission denied (publickey,password).\n")) {
		t.Error("containerWasGone = true for a node that refused the login")
	}
}

func TestContainerNamesFromPsKeepsOnlyThisAppsContainers(t *testing.T) {
	output := []byte("kubespray-1234\nkubespray-4321\nsomething-else\n\n")

	names := containerNamesFromPs(output)

	if strings.Join(names, ",") != "kubespray-1234,kubespray-4321" {
		t.Errorf("containerNamesFromPs = %v, want the containers this app made", names)
	}
}

// containsArg reports whether an argument vector holds exactly this argument.
func containsArg(argv []string, want string) bool {
	for _, arg := range argv {
		if arg == want {
			return true
		}
	}

	return false
}

// containsArgs reports whether an argument vector holds this flag followed by
// this value.
func containsArgs(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}

	return false
}
