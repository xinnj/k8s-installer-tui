package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/AEROGU/tvchooser"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
)

type AccessMethodType struct {
	new   string
	exist string
}

var accessMethodsType = AccessMethodType{
	new:   "Create a new SSH key and copy to each node",
	exist: "Use existing SSH key",
}
var accessMethod = ""
var sshKeyFile = ""
var containerSshKeyFile = "/data/idocluster/ansible-key"
var containerSshPasswordFile = "/data/idocluster/ansible-password"

// sshKeyPath and sshPasswordPath live in the project the wizard is working on,
// which the user chooses on the Project page: they are worked out where they are
// used, not when the app starts, so that the project chosen later is the one the
// container reads - it has that project mounted at /data/idocluster.
func sshKeyPath() string {
	return filepath.Join(projectPath, "ansible-key")
}

func sshPasswordPath() string {
	return filepath.Join(projectPath, "ansible-password")
}

// hostName is how one node is named in the run log, in the answer file of a
// check, and in the error modal.
func hostName(host Host) string {
	return inventory.All.Vars.Ansible_user + "@" + host.Ansible_host + ":" + inventory.All.Vars.Ansible_port
}

// The stages of the key setup: one answer file each, per node.
const (
	stageCopyKey  = "copy-key"
	stageKeyLogin = "key-login"
	stageDiagnose = "diagnose"
)

// answerFileName turns a node's name into a file name: letters, digits, dots
// and dashes only.
func answerFileName(host string) string {
	return strings.NewReplacer("@", "-", ":", "-", "/", "-").Replace(host)
}

// checkAnswerFile is where one stage of one node's check leaves its answer, in
// the project directory. The container writes it through its mount, so the
// answer survives the command that wrote it - including a command whose
// container went away while it was running.
func checkAnswerFile(host, stage string) string {
	return filepath.Join(projectPath, "check-"+answerFileName(host)+"-"+stage+".out")
}

// containerProjectFile is where the container sees a file of the project
// directory. One place knows the mount, so the host and the container cannot
// disagree about where a file is.
func containerProjectFile(name string) string {
	return "/data/idocluster/" + name
}

// redirectToFile sends what a command prints, its errors included, to one file:
// ssh reports a refusal on its error stream, so both streams go there.
func redirectToFile(cmdString, containerPath string) string {
	return "{ " + cmdString + "; } > \"" + containerPath + "\" 2>&1"
}

// runCheckStage runs one stage of an SSH check and answers with what the node
// said, read from the file the stage writes rather than from the exit status of
// the command that wrote it: with ten checks running at once a command can come
// back with nothing at all, and the exit status of a command whose container was
// destroyed under it says nothing about the node. A stage that wrote nothing is
// run a second time, and what is known about it goes to the log either way.
func runCheckStage(host, stage, cmdString string) commandResult {
	answerPath := checkAnswerFile(host, stage)
	answered := false

	result := runUntilAnswered(func() (answer, output []byte, err error) {
		os.Remove(answerPath)

		output, err = inContainerExec(redirectToFile(cmdString, containerProjectFile(filepath.Base(answerPath))), 0, nil)
		if data, readErr := os.ReadFile(answerPath); readErr == nil && len(bytes.TrimSpace(data)) > 0 {
			answer = data
			answered = true
		} else {
			recordSilentStage(host, stage, answerPath, output, err)
		}

		return answer, output, err
	})

	// A stage that answered leaves nothing behind; one that wrote nothing leaves
	// its answer file where it is, as the evidence of what did not happen.
	if answered {
		os.Remove(answerPath)
	}

	return result
}

// recordSilentStage writes everything that is known about a stage that came back
// without an answer: whether the container runtime said anything at all, how the
// command ended, and what the answer file looks like from this side of the
// mount. The modal can only say that the node answered nothing - this is what
// says whether the command ever reached it.
func recordSilentStage(host, stage, answerPath string, output []byte, err error) {
	status := "the command exited 0"
	if err != nil {
		status = "the command failed: " + err.Error()
	}

	answerFile := "no answer file on the host"
	if info, statErr := os.Stat(answerPath); statErr == nil {
		answerFile = fmt.Sprintf("answer file of %d bytes on the host", info.Size())
	}

	appendToLog(fmt.Sprintf("\n===== SSH check: %s - %s: the stage wrote no answer =====\n%s, %s, the command's own output (%d bytes):\n%s\n",
		host, stage, status, answerFile, len(output), strings.TrimRight(string(output), "\n")))
}

// writeSshPassword puts the password where sshpass reads it with -f, so that
// it never reaches a command line: a password in an argument is visible in ps.
// The caller removes the file.
func writeSshPassword(password string) error {
	return os.WriteFile(sshPasswordPath(), []byte(password+"\n"), 0600)
}

// sshCopyIdCommand installs the deployment key on a node over the password in
// the file written by writeSshPassword.
func sshCopyIdCommand(host Host) string {
	return fmt.Sprintf("mkdir -p ~/.ssh && chmod 700 ~/.ssh && sshpass -f \"%s\" ssh-copy-id -i \"%s\" -o StrictHostKeyChecking=no -o ConnectTimeout=5 -p %s %s@%s",
		containerSshPasswordFile, containerSshKeyFile, inventory.All.Vars.Ansible_port, inventory.All.Vars.Ansible_user, host.Ansible_host)
}

func copyFile(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	return os.Chmod(dst, mode)
}

// hostResult is the outcome of an SSH operation against one node.
type hostResult struct {
	host          string
	successful    bool
	errMsg        string // the line ssh reported about the failure
	passwordLogin bool   // the failed command logged in with a password
	diagnosis     string // the node's sshd facts, when the key was refused
}

// keyLoginCommand is the probe that asks a node to log in with the deployment
// key alone: no other identity is offered, nothing is prompted for, and no
// password is accepted, so a success means key login really works. The locale
// is pinned because the probe reads the answer: a Kylin node translates "id"
// into "用户id=0(root) 组id=0(root) 组=0(root)", which no longer carries the
// marker that says the node really answered.
func keyLoginCommand(host Host, keyFile string) string {
	return fmt.Sprintf("mkdir -p ~/.ssh && ssh -i \"%s\" -o IdentitiesOnly=yes -o BatchMode=yes -o PreferredAuthentications=publickey -o StrictHostKeyChecking=no -o ConnectTimeout=5 -p %s %s@%s LC_ALL=C id",
		keyFile, inventory.All.Vars.Ansible_port, inventory.All.Vars.Ansible_user, host.Ansible_host)
}

// identityMarker is what the probe's "id" prints, under the locale it pins,
// when the node really answered.
const identityMarker = "uid="

// probeFailure judges the outcome of the key-only login probe: "" when the
// node took the deployment key, otherwise what to tell the operator. A command
// that exits successfully without the node reporting an identity never reached
// the node - the exit status of a command whose container was destroyed under
// it says nothing - so it counts as a failed check, never as a pass. This is
// what keeps a node that cannot take the key from being waved through to the
// playbook.
func probeFailure(err error, output []byte) string {
	if err != nil {
		return errorText(err, output)
	}
	if !strings.Contains(string(output), identityMarker) {
		return "The key login check did not run: the node answered nothing - see the run log."
	}
	return ""
}

// keyLoginFailure returns "" when the node accepts the deployment key for
// key-only login, or ssh's error output when it does not, together with the
// command's whole output for the run log. Only the key is offered - BatchMode
// and PreferredAuthentications keep ssh from falling back to a password or a
// prompt - so a success proves key login works. It runs in the container,
// where ssh and the deployment key are the ones Ansible uses.
func keyLoginFailure(host Host, keyFile string) (errMsg, rawOutput string) {
	result := runCheckStage(hostName(host), stageKeyLogin, keyLoginCommand(host, keyFile))
	return probeFailure(result.err, result.output), string(result.output)
}

// diagnoseCommand is the read-only script that diagnoseKeyAuth runs on a node
// over a password login. The node's shell is left to interpret it, and the
// script ends in "true" so that a check finding nothing cannot look like a
// failed login. It uses no shell variables or backticks: the container
// command passes through a shell on the host first, which would expand them.
// The password comes from the file written by writeSshPassword.
func diagnoseCommand(host Host) string {
	remoteCommand := `echo "== sshd effective config =="; ` +
		`sudo -n sshd -T 2>/dev/null || sshd -T 2>/dev/null || grep -Ehis "^(pubkeyauthentication|authorizedkeysfile|permitrootlogin|strictmodes|allowusers|denyusers|allowgroups)" /etc/ssh/sshd_config /etc/ssh/sshd_config.d/*.conf 2>/dev/null; ` +
		`echo "== home and key file =="; ls -ld ~ ~/.ssh ~/.ssh/authorized_keys 2>&1; ` +
		`echo "== selinux =="; getenforce 2>/dev/null || echo "selinux tools absent"; ls -Z ~/.ssh/authorized_keys 2>&1; ` +
		`true`
	return fmt.Sprintf("sshpass -f \"%s\" ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 -o PreferredAuthentications=password -o PubkeyAuthentication=no -o NumberOfPasswordPrompts=1 -p %s %s@%s '%s'",
		containerSshPasswordFile, inventory.All.Vars.Ansible_port, inventory.All.Vars.Ansible_user, host.Ansible_host, remoteCommand)
}

// diagnoseKeyAuth logs in with the password and collects the read-only facts
// that explain a refused key: the effective sshd settings, the ownership and
// permissions of the path to authorized_keys, and the SELinux label. It
// changes nothing on the node, and ends in "true" so that a check finding
// nothing is not mistaken for a failed login.
func diagnoseKeyAuth(host Host) string {
	result := runCheckStage(hostName(host), stageDiagnose, diagnoseCommand(host))
	if result.err != nil {
		return "could not log in with the password either: " + errorText(result.err, result.output)
	}
	// The script ends in "true", so a silent run is a login that never
	// happened, not a node with nothing to report: say so rather than let the
	// modal conclude the node's settings are fine.
	if len(bytes.TrimSpace(result.output)) == 0 {
		return "the node's sshd facts could not be read: the command printed nothing - see the run log."
	}
	return string(result.output)
}

// checkSshKey copies the chosen key to where the deployment reads it and
// checks that every node takes it for key-only login. An error means the check
// could not be made at all, and the caller must not carry on as if it had
// passed.
func checkSshKey() (errorNodes []string, errorMsgs []string, err error) {
	if sshKeyFile != sshKeyPath() {
		err := copyFile(sshKeyFile, sshKeyPath(), 0600)
		if err != nil {
			return nil, nil, errors.New("Can't access SSH key file: " + sshKeyFile)
		}
	}

	const maxConcurrency = 10

	numHosts := len(inventory.All.Hosts)
	if numHosts == 0 {
		return nil, nil, errors.New("Can't check the SSH key: the inventory lists no nodes.")
	}

	// One container carries the whole run, made before the checks fan out: a
	// container that cannot be started is one message here, not the same
	// failure repeated for every node.
	if err := ensureContainer(); err != nil {
		return nil, nil, errors.New("Can't check the SSH key: " + err.Error())
	}

	resultCh := make(chan hostResult)

	groups := int(math.Ceil(float64(numHosts) / maxConcurrency))

	var hosts []Host
	for _, host := range inventory.All.Hosts {
		hosts = append(hosts, host)
	}

	for i := 0; i < groups; i++ {
		start := i * maxConcurrency
		end := int(math.Min(float64(numHosts-1), float64((i+1)*maxConcurrency-1)))

		for j := start; j <= end; j++ {
			go func(host Host, resultCh chan hostResult) {
				hostname := hostName(host)
				result := hostResult{host: hostname, successful: true}
				errMsg, rawOutput := keyLoginFailure(host, containerSshKeyFile)
				recordCheck(hostname, stageKeyLogin, rawOutput, errMsg == "")
				if errMsg != "" {
					result.successful = false
					result.errMsg = errMsg
				}
				resultCh <- result
			}(hosts[j], resultCh)
		}

		nodes, msgs := collectFailures(resultCh, end-start+1)
		errorNodes = append(errorNodes, nodes...)
		errorMsgs = append(errorMsgs, msgs...)
	}

	return errorNodes, errorMsgs, nil
}

// verdictFromDiagnosis turns the raw output of diagnoseKeyAuth into a
// one-line explanation of the most likely reason sshd refuses the key, or ""
// when nothing in the output explains the refusal. sshd -T prints its
// directives in lower case, and the configuration files are read without
// regard to case either, so the whole output is folded before it is matched.
func verdictFromDiagnosis(output string) string {
	lower := strings.ToLower(output)

	if value, ok := directiveValue(lower, "pubkeyauthentication"); ok && value == "no" {
		return "PubkeyAuthentication is disabled on the node: set 'PubkeyAuthentication yes' in /etc/ssh/sshd_config or a drop-in under /etc/ssh/sshd_config.d/, then reload sshd"
	}

	if value, ok := directiveValue(lower, "authorizedkeysfile"); ok && !strings.Contains(value, ".ssh/authorized_keys") {
		return fmt.Sprintf("the key was copied to ~/.ssh/authorized_keys, but the node's sshd reads AuthorizedKeysFile %s instead - install the key there or restore the default", value)
	}

	if strings.Contains(lower, "no such file or directory") && strings.Contains(lower, "authorized_keys") {
		return "the key was copied, but ~/.ssh/authorized_keys does not exist on the node"
	}

	// StrictModes is what makes sshd reject a key file whose path is writable
	// by group or others, so when it is off the permissions explain nothing.
	if value, ok := directiveValue(lower, "strictmodes"); !ok || value != "no" {
		for _, entry := range lsEntries(lower) {
			if entry.mode[5] == 'w' || entry.mode[8] == 'w' {
				return fmt.Sprintf("StrictModes rejects %s because it is writable by group or others - run 'chmod go-w' on it", entry.path)
			}
		}
	}

	if strings.Contains(lower, "enforcing") {
		if label, ok := selinuxLabelLine(lower); ok && !strings.Contains(label, "ssh_home_t") {
			return "SELinux is enforcing and ~/.ssh/authorized_keys has a label sshd cannot use - run 'restorecon -R ~/.ssh' on the node"
		}
	}

	return ""
}

// verdictFromSshError interprets a bare ssh failure for the cases where there
// is no password to look at the node with: the copy never went through, or
// the node was never reached at all.
func verdictFromSshError(errText string) string {
	lower := strings.ToLower(errText)
	switch {
	case strings.Contains(lower, "permission denied"):
		return "the node rejected the login - check the password if one was used, and whether the node accepts the key (PubkeyAuthentication and authorized_keys)"
	case strings.Contains(lower, "connection refused"):
		return "nothing is listening on the SSH port - check the SSH port number and that sshd is running"
	case strings.Contains(lower, "timed out"), strings.Contains(lower, "no route to host"):
		return "the SSH port did not answer - check connectivity and the firewall on the node"
	}
	return ""
}

// capLines keeps the first n lines of s, reporting whether it had to cut.
func capLines(s string, n int) (string, bool) {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n"), false
	}
	return strings.Join(lines[:n], "\n"), true
}

// directiveValue returns the value of an sshd directive, as "sshd -T" prints
// it, or as the diagnostics found it in a configuration file.
func directiveValue(lowerOutput, directive string) (string, bool) {
	for _, line := range strings.Split(lowerOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == directive {
			return fields[1], true
		}
	}
	return "", false
}

// lsEntry is one "ls -ld" listing of the diagnosis output.
type lsEntry struct {
	mode string
	path string
}

// lsEntries collects the ls -ld style lines of the diagnosis output. GNU ls
// appends "." or "+" to the mode when SELinux or ACLs are in play, so the
// mode is trimmed of those markers before it is checked.
func lsEntries(lowerOutput string) []lsEntry {
	var entries []lsEntry
	for _, line := range strings.Split(lowerOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mode := strings.TrimRight(fields[0], ".+")
		if len(mode) != 10 || (mode[0] != 'd' && mode[0] != '-') {
			continue
		}
		entries = append(entries, lsEntry{mode: mode, path: fields[len(fields)-1]})
	}
	return entries
}

// selinuxLabelLine returns the "ls -Z" line for authorized_keys; its SELinux
// context shows up as a user:role:type:level field.
func selinuxLabelLine(lowerOutput string) (string, bool) {
	for _, line := range strings.Split(lowerOutput, "\n") {
		if strings.Contains(line, "authorized_keys") && strings.Contains(line, ":object_r:") {
			return line, true
		}
	}
	return "", false
}

// collectFailures drains n results and turns the failures into the node list
// and the messages for the error modal, writing the diagnosis of a refused key
// to the log.
func collectFailures(resultCh chan hostResult, n int) (errorNodes []string, errorMsgs []string) {
	for i := 0; i < n; i++ {
		result := <-resultCh
		if result.successful {
			continue
		}
		errorNodes = append(errorNodes, result.host)
		errorMsgs = append(errorMsgs, failureMessage(result))
		if result.diagnosis != "" {
			appendToLog("\n===== SSH check: " + result.host + " - the node's sshd facts =====\n" +
				strings.TrimRight(result.diagnosis, "\n") + "\n")
		}
	}
	return errorNodes, errorMsgs
}

// containerGoneVerdict is what a node's block says when the command never
// reached the node because the container it runs in was gone. It is not the
// node's fault, and the container tool's own words say nothing to the operator:
// they are in the run log, where recordCheck wrote them.
const containerGoneVerdict = "The check did not run: the container it needed was gone. See the run log for what the container runtime reported."

// containerReportPatterns are the container tool's own vocabulary. A line that
// carries one of them is the runtime talking about itself - the container it
// could not make, the storage it could not reach - and never a node's answer.
var containerReportPatterns = []string{
	"container storage",
	"no such container",
	"container state improper",
	"is not running",
	"overlay-containers",
	"is mounted",
	"container name",
}

// isContainerReport reports whether a failure line is the container tool's own
// report rather than a command's.
func isContainerReport(errText string) bool {
	lower := strings.ToLower(errText)
	for _, pattern := range containerReportPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}

	return false
}

// failureVerdict names the plain-language cause of one node's failure. A
// failure the container runtime produced is named first: it says the check
// never ran, and no reading of the node's sshd can explain it. Only a command
// that authenticated with the password can be blamed on the password: the
// key-only probe prints the very same "Permission denied (publickey,password)."
// when a node offers a password method the probe may not use, so the caller has
// to say which kind of command failed.
func failureVerdict(errText string, passwordLogin bool) string {
	if isContainerReport(errText) {
		return containerGoneVerdict
	}
	if passwordLogin && isPasswordRefusal(errText) {
		return passwordRejectedVerdict()
	}
	return verdictFromSshError(errText)
}

// isPasswordRefusal reports whether ssh refused a login while it was offering
// the password, which is what a wrong password looks like.
func isPasswordRefusal(errText string) bool {
	lower := strings.ToLower(errText)
	return strings.Contains(lower, "permission denied") && strings.Contains(lower, "password")
}

// passwordRejectedVerdict says in plain words that the node refused the
// password. It opens the node's block, so it is a sentence, and it names the
// user whose password was refused.
func passwordRejectedVerdict() string {
	if user := inventory.All.Vars.Ansible_user; user != "" {
		return "The node rejected the password for " + user + "."
	}
	return "The node rejected the password."
}

// failureMessage renders one node's failure for the error modal: what happened
// first, then the evidence. A node whose key was copied but refused gets its
// sshd diagnosis; any other failure gets the line ssh reported.
func failureMessage(result hostResult) string {
	if result.diagnosis != "" {
		return diagnosisBlock(result.host, result.errMsg, result.diagnosis)
	}
	block := result.host
	if verdict := failureVerdict(result.errMsg, result.passwordLogin); verdict != "" {
		block += "\n" + verdict
	}
	// The container runtime's own words explain nothing to the operator; the
	// run log has them, where recordCheck put them.
	if result.errMsg != "" && !isContainerReport(result.errMsg) {
		block += "\n" + result.errMsg
	}
	return block
}

// diagnosisBlock renders one node's diagnosis for the error modal: what ssh
// said, then the likely cause, then the raw facts, capped so that one node
// cannot fill the screen. The whole diagnosis goes to the run log instead.
func diagnosisBlock(host, errMsg, diagnosis string) string {
	block := host + "\nThe key was copied, but the node still refuses key login."
	if errMsg != "" {
		block += "\n" + errMsg
	}
	if verdict := verdictFromDiagnosis(diagnosis); verdict != "" {
		block += "\nLikely cause: " + verdict + "."
	} else {
		block += "\nNo cause found in the node's sshd settings; the details are below."
	}
	facts, truncated := capLines(diagnosis, 12)
	block += "\n" + facts
	if truncated {
		block += fmt.Sprintf("\n... (full diagnosis in %s)", filepath.Join(projectPath, logFileName))
	}
	return block
}

// recordCheck writes one stage of the key setup for one node to the log file of
// the current run, whether it passed or failed. A pass has to leave evidence
// too: when a node passes the check and the deployment then fails on it, this
// is what shows which stages really ran and what the node answered.
func recordCheck(host, stage, output string, ok bool) {
	result := "ok"
	if !ok {
		result = "failed"
	}

	block := "\n===== SSH check: " + host + " - " + stage + ": " + result + " =====\n"
	if text := strings.TrimRight(output, "\n"); text != "" {
		block += text + "\n"
	}
	appendToLog(block)
}

// appendToLog appends to the log file of the current run. That is the file
// the Ansible output is teed into later, and the SSH checks may create it
// first.
func appendToLog(text string) {
	if logFileName == "" {
		return
	}
	file, err := os.OpenFile(filepath.Join(projectPath, logFileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer file.Close()
	file.WriteString(text)
}

// sshErrorPatterns are the fragments that mark a line as saying why an SSH
// operation failed, tried in this order when a single line carries several.
// They are ssh-specific on purpose: the container runtime's own error line
// mentions neither, so it can never be picked as the reason.
var sshErrorPatterns = []string{
	"permission denied",                // a wrong password, or a key the node will not take
	"too many authentication failures", // more keys were offered than the node accepts
	"host key verification failed",     // the node is not the one it was before
	"no route to host",                 // nothing answers at that address
	"connection refused",               // nothing is listening on the SSH port
	"connection timed out",
	"timed out", // ConnectTimeout gave up
	"sshpass:",  // sshpass never got to run ssh
	"ssh:",      // ssh's own message, already labelled as such
}

// meaningfulLine returns the line of a failed command's output that explains
// the failure, or "" when none of them does. The lines are read from the bottom
// up, because ssh reports its verdict last - but the container runtime adds its
// own teardown error after everything the command printed, and a line that
// starts with "Error:" is that runtime, never ssh.
func meaningfulLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "Error: ") {
			continue
		}
		lower := strings.ToLower(line)
		for _, pattern := range sshErrorPatterns {
			if strings.Contains(lower, pattern) {
				return line
			}
		}
	}
	return ""
}

// sshReport labels a line as ssh's own message. ssh prefixes its verdict with
// "user@host: ", which the node's line above the message already says, and
// ssh-copy-id prints what ssh said behind its own "/usr/bin/ssh-copy-id:
// ERROR: ".
func sshReport(line string) string {
	if strings.HasPrefix(line, "/") {
		if _, rest, ok := strings.Cut(line, "ERROR: "); ok {
			line = strings.TrimSpace(rest)
		}
	}
	if strings.HasPrefix(line, "ssh:") {
		return line
	}
	if head, rest, ok := strings.Cut(line, ": "); ok && strings.Contains(head, "@") && !strings.Contains(head, " ") {
		return "ssh: " + rest
	}
	return "ssh: " + line
}

// errorText renders a failed in-container command for the person reading the
// error modal: the line that says what went wrong, not the container runtime
// tearing the container down after the command is over. The exit status is left
// out - it is the host shell's, not ssh's, and says nothing about the cause -
// while the command's whole output goes to the run log.
func errorText(err error, output []byte) string {
	if line := meaningfulLine(string(output)); line != "" {
		return sshReport(line)
	}
	if text := strings.TrimSpace(string(output)); text != "" {
		lines := strings.Split(text, "\n")
		return lines[len(lines)-1]
	}
	return err.Error()
}

// copySshKeyToNode installs the deployment key on every node with ssh-copy-id
// over the password, then logs in with the key to make sure the copy actually
// gave the deployment what it needs.
func copySshKeyToNode(sshPassword string) (errorNodes []string, errorMsgs []string, err error) {
	const maxConcurrency = 10

	numHosts := len(inventory.All.Hosts)
	if numHosts == 0 {
		return nil, nil, errors.New("Can't set up key login: the inventory lists no nodes.")
	}

	resultCh := make(chan hostResult)

	groups := int(math.Ceil(float64(numHosts) / maxConcurrency))

	_, err = os.Stat(sshKeyPath())
	if err != nil {
		// Generate SSH key and change ownership to current user
		execCommandAndCheck(fmt.Sprintf("rm -f \"%s\"; ssh-keygen -q -N '' -f \"%s\"; chown %d:%d \"%s\" \"%s.pub\"", containerSshKeyFile, containerSshKeyFile, os.Getuid(), os.Getgid(), containerSshKeyFile, containerSshKeyFile), 0, inContainer)
	}

	if err := writeSshPassword(sshPassword); err != nil {
		// Without the password file nothing can be copied, so the caller has
		// to stop rather than let the wizard move on unchecked.
		return nil, nil, errors.New("Can't set up key login: can't write the SSH password file: " + err.Error())
	}
	defer os.Remove(sshPasswordPath())

	// One container carries the whole run, made before the checks fan out: a
	// container that cannot be started is one message here, not the same
	// failure repeated for every node.
	if err := ensureContainer(); err != nil {
		return nil, nil, errors.New("Can't set up key login: " + err.Error())
	}

	var hosts []Host
	for _, host := range inventory.All.Hosts {
		hosts = append(hosts, host)
	}

	for i := 0; i < groups; i++ {
		start := i * maxConcurrency
		end := int(math.Min(float64(numHosts-1), float64((i+1)*maxConcurrency-1)))

		for j := start; j <= end; j++ {
			go func(host Host, resultCh chan hostResult) {
				hostname := hostName(host)
				result := runCheckStage(hostname, stageCopyKey, sshCopyIdCommand(host))
				recordCheck(hostname, stageCopyKey, string(result.output), result.err == nil)
				if result.err != nil {
					resultCh <- hostResult{host: hostname, errMsg: errorText(result.err, result.output), passwordLogin: true}
					return
				}

				// The copy reports success as long as the key landed in the
				// file, even when the node is set up to refuse it. Password
				// login still works at this point, so the node can be asked
				// what it does not like about the key.
				errMsg, rawOutput := keyLoginFailure(host, containerSshKeyFile)
				recordCheck(hostname, stageKeyLogin, rawOutput, errMsg == "")
				if errMsg != "" {
					resultCh <- hostResult{host: hostname, errMsg: errMsg, diagnosis: diagnoseKeyAuth(host)}
					return
				}

				resultCh <- hostResult{host: hostname, successful: true}
			}(hosts[j], resultCh)
		}

		nodes, msgs := collectFailures(resultCh, end-start+1)
		errorNodes = append(errorNodes, nodes...)
		errorMsgs = append(errorMsgs, msgs...)
	}

	return errorNodes, errorMsgs, nil
}

// sshKeyField is a form item showing an editable SSH key path and a "Browse"
// button on the same row. tview puts every button of a form into the form's
// own button row at the bottom, so the two controls are combined into a single
// form item here to keep the button next to the field.
type sshKeyField struct {
	*tview.Flex

	input  *tview.InputField
	button *tview.Button
	label  string
}

func newSshKeyField(label, text string, changed func(text string)) *sshKeyField {
	input := tview.NewInputField().
		SetLabel(label).
		SetText(text).
		SetChangedFunc(changed)
	button := tview.NewButton("Browse")

	return &sshKeyField{
		Flex: tview.NewFlex().
			AddItem(input, 0, 1, true).
			AddItem(tview.NewBox(), 1, 0, false).
			AddItem(button, tview.TaggedStringWidth(button.GetLabel())+4, 0, false),
		input:  input,
		button: button,
		label:  label,
	}
}

func (f *sshKeyField) GetLabel() string {
	return f.label
}

func (f *sshKeyField) SetFormAttributes(labelWidth int, labelColor, bgColor, fieldTextColor, fieldBgColor tcell.Color) tview.FormItem {
	f.input.SetFormAttributes(labelWidth, labelColor, bgColor, fieldTextColor, fieldBgColor)
	f.SetBackgroundColor(bgColor)
	return f
}

func (f *sshKeyField) GetFieldWidth() int {
	return 0
}

func (f *sshKeyField) GetFieldHeight() int {
	return 1
}

func (f *sshKeyField) SetDisabled(disabled bool) tview.FormItem {
	f.input.SetDisabled(disabled)
	f.button.SetDisabled(disabled)
	return f
}

// SetFinishedFunc stores the form's handler for leaving this item. Tab moves
// from the key path to the Browse button and from there on to the next form
// item, so the button keeps tview's keyboard navigation.
func (f *sshKeyField) SetFinishedFunc(handler func(key tcell.Key)) tview.FormItem {
	f.input.SetFinishedFunc(func(key tcell.Key) {
		if key == tcell.KeyTab {
			app.SetFocus(f.button)
			return
		}
		handler(key)
	})
	f.button.SetExitFunc(func(key tcell.Key) {
		if key == tcell.KeyBacktab {
			app.SetFocus(f.input)
			return
		}
		handler(key)
	})
	return f
}

func (f *sshKeyField) SetBrowseFunc(handler func()) {
	f.button.SetSelectedFunc(handler)
}

func (f *sshKeyField) SetText(text string) {
	f.input.SetText(text)
}

// browseSshKeyFile opens the file picker and returns the selected path, or ""
// if the user cancelled. The picker has no start directory: the paths given to
// it become entries of its "Favorites" list, which is expanded on open. The
// home directory comes first so that it acts as the default location.
func browseSshKeyFile() string {
	favorites := []string{homePath + "|Home"}
	if sshDir := filepath.Join(homePath, ".ssh"); pathExists(sshDir) {
		favorites = append(favorites, sshDir+"|SSH keys")
	}
	if pathExists(projectPath) {
		favorites = append(favorites, projectPath+"|Cluster project")
	}

	// Hidden files are shown so that keys in hidden directories such as ~/.ssh can be picked.
	return tvchooser.FileChooser(app, true, favorites...)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func initFlexDeployCluster() {
	formUp := tview.NewForm()
	if setupNewCluster {
		formUp.SetTitle("Create New Cluster").SetBorder(true)
	} else {
		formUp.SetTitle("Modify Cluster").SetBorder(true)
		if accessMethod == "" {
			accessMethod = accessMethodsType.new
		}
	}

	if accessMethod == "" {
		_, err := os.Stat(sshKeyPath())
		if err != nil {
			// not found
			accessMethod = accessMethodsType.new
		} else {
			accessMethod = accessMethodsType.exist
		}
	}

	accessMethods := []string{
		accessMethodsType.new,
		accessMethodsType.exist,
	}
	initialOption := slices.Index(accessMethods, accessMethod)
	formUp.AddDropDown("How to access cluster nodes: ", accessMethods, initialOption, func(option string, optionIndex int) {
		if accessMethod != option {
			accessMethod = option
			flexDeployCluster.Clear()
			initFlexDeployCluster()
		}
	})

	var sshPassword string
	if accessMethod == accessMethodsType.new {
		formUp.
			AddPasswordField("SSH Password: ", "", 0, '*', func(text string) {
				sshPassword = text
			})
	} else {
		if sshKeyFile == "" {
			sshKeyFile = sshKeyPath()
		}
		sshKey := newSshKeyField("SSH key file: ", sshKeyFile, func(text string) {
			sshKeyFile = strings.Trim(text, " ")
		})
		sshKey.SetBrowseFunc(func() {
			if path := browseSshKeyFile(); path != "" {
				sshKeyFile = path
				sshKey.SetText(path)
			}
		})
		formUp.AddFormItem(sshKey)
	}

	inventoryContentByte, err := yaml.Marshal(&inventory)
	check(err)
	inventoryContentString := string(inventoryContentByte)
	formUp.AddTextArea("Inventory file: ", inventoryContentString, 0, 10, 0, func(text string) {
		inventoryContentString = text
	})

	for k, v := range appConfig.Default_vars {
		extraVars[k] = v
	}
	extraVarsContentByte, err := yaml.Marshal(&extraVars)
	check(err)
	extraVarsContentString := string(extraVarsContentByte)
	formUp.AddTextArea("Extra vars: ", extraVarsContentString, 0, 10, 0, func(text string) {
		extraVarsContentString = text
	})

	formDown := tview.NewForm()

	formDown.AddButton("Start", func() {
		if accessMethod == accessMethodsType.new {
			if sshPassword == "" {
				showErrorModal("Please provide the SSH password for each node.",
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Deploy Cluster")
					})
				return
			}
		} else {
			if sshKeyFile == "" {
				showErrorModal("Please provide the SSH key file.",
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Deploy Cluster")
					})
				return
			} else {
				_, err := os.Stat(sshKeyFile)
				if err != nil {
					// not found
					showErrorModal("Can't find the SSH key file: "+sshKeyFile,
						func(buttonIndex int, buttonLabel string) {
							pages.SwitchToPage("Deploy Cluster")
						})
					return
				}
			}
		}

		var inventoryContent Inventory
		err := yaml.Unmarshal([]byte(inventoryContentString), &inventoryContent)
		if err != nil {
			showErrorModal("Format of inventory file is wrong.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Deploy Cluster")
				})
			return
		}
		inventory = inventoryContent

		var extraVarsContent map[string]any
		err = yaml.Unmarshal([]byte(extraVarsContentString), &extraVarsContent)
		if err != nil {
			showErrorModal("Format of extra vars is wrong.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Deploy Cluster")
				})
			return
		}
		extraVars = extraVarsContent

		saveInventory()

		if setupNewCluster {
			initLog("create-cluster-")
		} else {
			initLog("resize-cluster-")
		}

		if accessMethod == accessMethodsType.exist {
			// Check SSH key
			ch := make(chan bool)
			go func() {
				modalCheckSshKey := tview.NewModal().SetText("Check connection to each node using existing SSH key...")
				pages.AddPage("Check SSH Key", modalCheckSshKey, true, true)
				app.ForceDraw()
				ch <- true
			}()

			errorNodes, errorMsgs, err := checkSshKey()

			// Wait until modal draw finish
			<-ch

			if err != nil {
				showErrorModal(err.Error(),
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Deploy Cluster")
					})
				return
			}

			if len(errorNodes) > 0 {
				showErrorModal(fmt.Sprintf("Can't log in to these nodes with the SSH key:\n\n%s", strings.Join(errorMsgs, "\n\n")),
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Deploy Cluster")
					})
				return
			}
		} else {
			// Copy SSH key
			ch := make(chan bool)
			go func() {
				modalCopySshKey := tview.NewModal().SetText("Copy SSH key to each node...")
				pages.AddPage("Copy SSH Key", modalCopySshKey, true, true)
				app.ForceDraw()
				ch <- true
			}()

			errorNodes, errorMsgs, err := copySshKeyToNode(sshPassword)

			// Wait until modal draw finish
			<-ch

			if err != nil {
				// Nothing was copied or checked, so the wizard must not move on
				// to the setup page as if it had been.
				showErrorModal(err.Error(),
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Deploy Cluster")
					})
				return
			}

			if len(errorNodes) > 0 {
				showErrorModal(fmt.Sprintf("Can't set up key login on these nodes:\n\n%s", strings.Join(errorMsgs, "\n\n")),
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Deploy Cluster")
					})
				return
			}
		}

		initFlexSetupCluster(true)
		pages.SwitchToPage("Setup Cluster")
	})

	formDown.AddButton("Back", func() {
		if setupNewCluster {
			pages.SwitchToPage("Mirror")
		} else {
			pages.SwitchToPage("Edit Hosts")
		}
	})

	formDown.AddButton("Quit", func() {
		showQuitModal()
	})

	flexDeployCluster.SetDirection(tview.FlexRow).
		AddItem(formUp, 0, 1, true).
		AddItem(formDown, 3, 1, false)

	app.SetFocus(formUp)

	formUp.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlN || event.Key() == tcell.KeyCtrlP {
			app.SetFocus(formDown)
		}
		return event
	})

	formDown.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlN || event.Key() == tcell.KeyCtrlP {
			app.SetFocus(formUp)
		}
		return event
	})
}
