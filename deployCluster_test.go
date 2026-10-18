package main

// Tests for the SSH key diagnosis on the Deploy Cluster page: the parser that
// names why a node refuses the deployment key, and the helpers around it.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// healthyDiagnosis is what diagnoseKeyAuth() prints for a node that accepts
// the key, cut down to the lines the parser looks at.
const healthyDiagnosis = `== sshd effective config ==
pubkeyauthentication yes
authorizedkeysfile .ssh/authorized_keys
permitrootlogin prohibit-password
strictmodes yes
allowusers root
== home and key file ==
drwx------ 3 root root 4096 Oct 13 10:00 /root
drwx------ 2 root root 4096 Oct 13 10:00 /root/.ssh
-rw------- 1 root root 389 Oct 13 10:00 /root/.ssh/authorized_keys
== selinux ==
Enforcing
-rw-------. 1 root root system_u:object_r:ssh_home_t:s0 389 Oct 13 10:00 /root/.ssh/authorized_keys
`

func TestVerdictFromDiagnosisNamesDisabledPubkeyAuthentication(t *testing.T) {
	output := strings.Replace(healthyDiagnosis, "pubkeyauthentication yes", "pubkeyauthentication no", 1)

	verdict := verdictFromDiagnosis(output)

	if !strings.Contains(verdict, "PubkeyAuthentication is disabled") {
		t.Errorf("verdict = %q, want it to name PubkeyAuthentication as disabled", verdict)
	}
}

func TestVerdictFromDiagnosisNamesNonDefaultAuthorizedKeysFile(t *testing.T) {
	output := strings.Replace(healthyDiagnosis, "authorizedkeysfile .ssh/authorized_keys", "authorizedkeysfile /etc/ssh/keys/%u", 1)

	verdict := verdictFromDiagnosis(output)

	if !strings.Contains(verdict, "AuthorizedKeysFile") {
		t.Errorf("verdict = %q, want it to name AuthorizedKeysFile", verdict)
	}
}

func TestVerdictFromDiagnosisNamesMissingAuthorizedKeysFile(t *testing.T) {
	output := strings.Replace(healthyDiagnosis,
		"-rw------- 1 root root 389 Oct 13 10:00 /root/.ssh/authorized_keys",
		"ls: cannot access '/root/.ssh/authorized_keys': No such file or directory", 1)

	verdict := verdictFromDiagnosis(output)

	if !strings.Contains(verdict, "does not exist") {
		t.Errorf("verdict = %q, want it to report the missing authorized_keys file", verdict)
	}
}

func TestVerdictFromDiagnosisNamesGroupWritableSshDir(t *testing.T) {
	output := strings.Replace(healthyDiagnosis, "drwx------ 2 root root 4096 Oct 13 10:00 /root/.ssh", "drwxrwxr-x 2 root root 4096 Oct 13 10:00 /root/.ssh", 1)

	verdict := verdictFromDiagnosis(output)

	if !strings.Contains(verdict, "writable by group or others") {
		t.Errorf("verdict = %q, want it to report the permissions StrictModes rejects", verdict)
	}
}

func TestVerdictFromDiagnosisIgnoresPermissionsWhenStrictModesIsOff(t *testing.T) {
	output := strings.Replace(healthyDiagnosis, "strictmodes yes", "strictmodes no", 1)
	output = strings.Replace(output, "drwx------ 2 root root", "drwxrwxr-x 2 root root", 1)

	verdict := verdictFromDiagnosis(output)

	if verdict != "" {
		t.Errorf("verdict = %q, want no verdict: StrictModes is off, so the permissions are not the cause", verdict)
	}
}

func TestVerdictFromDiagnosisNamesBadSelinuxLabel(t *testing.T) {
	output := strings.Replace(healthyDiagnosis, "system_u:object_r:ssh_home_t:s0", "unconfined_u:object_r:user_home_t:s0", 1)

	verdict := verdictFromDiagnosis(output)

	if !strings.Contains(verdict, "restorecon") {
		t.Errorf("verdict = %q, want it to point at restorecon for the SELinux label", verdict)
	}
}

func TestVerdictFromDiagnosisIgnoresSelinuxLabelWhenNotEnforcing(t *testing.T) {
	output := strings.Replace(healthyDiagnosis, "system_u:object_r:ssh_home_t:s0", "unconfined_u:object_r:user_home_t:s0", 1)
	output = strings.Replace(output, "Enforcing", "Permissive", 1)

	verdict := verdictFromDiagnosis(output)

	if verdict != "" {
		t.Errorf("verdict = %q, want no verdict: SELinux is not enforcing", verdict)
	}
}

func TestVerdictFromDiagnosisStaysQuietOnHealthyOutput(t *testing.T) {
	verdict := verdictFromDiagnosis(healthyDiagnosis)

	if verdict != "" {
		t.Errorf("verdict = %q, want no verdict when nothing in the output explains the refusal", verdict)
	}
}

func TestVerdictFromSshErrorNamesRejectedLogin(t *testing.T) {
	verdict := verdictFromSshError("exit status 255\nPermission denied (publickey).")

	if !strings.Contains(verdict, "rejected the login") {
		t.Errorf("verdict = %q, want it to name the rejected login", verdict)
	}
}

func TestVerdictFromSshErrorNamesClosedPort(t *testing.T) {
	verdict := verdictFromSshError("ssh: connect to host 10.0.0.1 port 22: Connection refused")

	if !strings.Contains(verdict, "nothing is listening") {
		t.Errorf("verdict = %q, want it to name the closed SSH port", verdict)
	}
}

func TestVerdictFromSshErrorNamesSilentPort(t *testing.T) {
	verdict := verdictFromSshError("ssh: connect to host 10.0.0.1 port 22: Connection timed out")

	if !strings.Contains(verdict, "did not answer") {
		t.Errorf("verdict = %q, want it to name the unresponsive SSH port", verdict)
	}
}

func TestVerdictFromSshErrorStaysQuietOnBareExitStatus(t *testing.T) {
	verdict := verdictFromSshError("exit status 255")

	if verdict != "" {
		t.Errorf("verdict = %q, want no verdict when ssh said nothing useful", verdict)
	}
}

func TestKeyLoginCommandOffersOnlyTheKey(t *testing.T) {
	inventory.All.Vars.Ansible_port = "22"
	inventory.All.Vars.Ansible_user = "root"

	cmd := keyLoginCommand(Host{Ansible_host: "10.0.0.1"}, "/data/idocluster/ansible-key")

	for _, want := range []string{
		"-i \"/data/idocluster/ansible-key\"",
		"-o IdentitiesOnly=yes",
		"-o BatchMode=yes",
		"-o PreferredAuthentications=publickey",
		"-p 22",
		"root@10.0.0.1",
		// The probe reads the node's answer, and a Kylin node translates "id"
		// into "用户id=0(root) 组id=0(root) 组=0(root)", which carries none of
		// the marker that says the node really answered.
		"LC_ALL=C id",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q does not contain %q", cmd, want)
		}
	}
}

func TestDiagnoseCommandSurvivesTheNodesShell(t *testing.T) {
	inventory.All.Vars.Ansible_port = "22"
	inventory.All.Vars.Ansible_user = "root"

	cmd := diagnoseCommand(Host{Ansible_host: "10.0.0.1"})

	// The remote script is quoted for the node's shell, which is the one that
	// interprets it: what ssh sends is expanded there, not in the container.
	// Anything that shell would expand is not part of the facts the diagnosis
	// reads.
	if strings.ContainsAny(cmd, "$`") {
		t.Errorf("command %q contains characters the node's shell would expand", cmd)
	}
	for _, want := range []string{"sshd -T", "getenforce", "ls -Z", "authorized_keys", "-o PreferredAuthentications=password", "root@10.0.0.1"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q does not contain %q", cmd, want)
		}
	}
}

func TestContainerCommandsParseAsBash(t *testing.T) {
	inventory.All.Vars.Ansible_port = "22"
	inventory.All.Vars.Ansible_user = "root"
	host := Host{Ansible_host: "10.0.0.1"}

	commands := map[string]string{
		"key login": keyLoginCommand(host, "/data/idocluster/ansible-key"),
		"copy key":  sshCopyIdCommand(host),
		"diagnose":  diagnoseCommand(host),
	}
	for name, cmd := range commands {
		// The container's bash parses the command as it is written...
		if out, err := exec.Command("/bin/bash", "-n", "-c", cmd).CombinedOutput(); err != nil {
			t.Errorf("%s command does not parse as bash in the container: %v\n%s", name, err, out)
		}

		// ...and it arrives there as one argument of an argument vector. No
		// shell on the host parses the command, so nothing in it - a quote, a
		// dollar sign, a backslash - can be rewritten on the way in.
		argv := containerExecArgv("/tool/podman", "kubespray-1234", cmd, nil)
		if argv[len(argv)-1] != cmd {
			t.Errorf("%s command does not arrive as one argument: %q", name, argv[len(argv)-1])
		}
	}
}

func TestSshCommandsTakeThePasswordFromAFile(t *testing.T) {
	inventory.All.Vars.Ansible_port = "22"
	inventory.All.Vars.Ansible_user = "root"
	host := Host{Ansible_host: "10.0.0.1"}

	commands := map[string]string{
		"copy key": sshCopyIdCommand(host),
		"diagnose": diagnoseCommand(host),
	}
	for name, cmd := range commands {
		if !strings.Contains(cmd, `sshpass -f "`+containerSshPasswordFile+`"`) {
			t.Errorf("%s command %q does not read the password from %s", name, cmd, containerSshPasswordFile)
		}
		if strings.Contains(cmd, "| sshpass") || strings.Contains(cmd, "sshpass -p") {
			t.Errorf("%s command %q still puts the password on the command line", name, cmd)
		}
	}
}

func TestWriteSshPasswordKeepsAwkwardPasswords(t *testing.T) {
	originalPath := projectPath
	projectPath = t.TempDir()
	defer func() { projectPath = originalPath }()

	// Everything a shell would treat specially if the password were
	// interpolated into a command instead of read from the file.
	password := "$HOME `id` \"quoted\" 'single' \\backslash\\"

	if err := writeSshPassword(password); err != nil {
		t.Fatalf("writeSshPassword: %v", err)
	}

	passwordPath := sshPasswordPath()
	data, err := os.ReadFile(passwordPath)
	if err != nil {
		t.Fatalf("reading back the password file: %v", err)
	}
	if string(data) != password+"\n" {
		t.Errorf("password file = %q, want %q", data, password+"\n")
	}

	info, err := os.Stat(passwordPath)
	if err != nil {
		t.Fatalf("stat of the password file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("password file mode = %v, want 0600", perm)
	}
}

// wrongPasswordOutput is what copying the key with a wrong password prints:
// sshpass feeds ssh the same password until ssh gives up, and the container
// runtime then writes its own teardown error after all of it.
const wrongPasswordOutput = `/usr/bin/ssh-copy-id: INFO: Source of key(s) to be installed: "/data/idocluster/ansible-key.pub"
Permission denied, please try again.
root@10.184.101.66: Permission denied (publickey,password).
Error: unable to determine if "/root/.local/share/podman-static/share/containers/storage/overlay-containers/f1a9c3/userdata/shm" is mounted: lstat /root/.local/share/podman-static/share/containers/storage/overlay-containers/f1a9c3: no such file or directory
`

func TestProbeFailureNeedsTheNodesOwnAnswer(t *testing.T) {
	if errMsg := probeFailure(nil, []byte("uid=0(root) gid=0(root) groups=0(root)\n")); errMsg != "" {
		t.Errorf("probeFailure = %q, want no failure when the node reported its identity", errMsg)
	}

	// A successful exit status with no answer from the node means the command
	// never reached it, and must not pass as a working key login: that is how a
	// node which cannot take the key got through to the playbook.
	if errMsg := probeFailure(nil, nil); errMsg == "" {
		t.Error("probeFailure = \"\", want a failure when the node answered nothing")
	}

	errMsg := probeFailure(errors.New("exit status 255"), []byte("Permission denied (publickey).\n"))
	if !strings.Contains(errMsg, "Permission denied") {
		t.Errorf("probeFailure = %q, want ssh's own refusal", errMsg)
	}

	// A node that answers in its own language carries no marker, so the probe
	// pins LC_ALL=C (see keyLoginCommand) to be able to read the answer at
	// all. The localized answer must not pass as verified.
	localised := probeFailure(nil, []byte("用户id=0(root) 组id=0(root) 组=0(root)\n"))
	if localised == "" {
		t.Error("probeFailure = \"\", want an answer the check cannot read to fail the check")
	}
}

func TestDiagnosisBlockShowsWhatSshSaid(t *testing.T) {
	diagnosis := strings.Replace(healthyDiagnosis, "pubkeyauthentication yes", "pubkeyauthentication no", 1)

	block := diagnosisBlock("root@10.0.0.1:22", "ssh: Permission denied (publickey,password).", diagnosis)

	if !strings.Contains(block, "ssh: Permission denied (publickey,password).") {
		t.Errorf("block %q does not say what ssh reported", block)
	}
	if !strings.Contains(block, "PubkeyAuthentication is disabled") {
		t.Errorf("block %q does not name the cause found in the node's settings", block)
	}
}

func TestFailureMessageShowsTheSshRefusalNotThePodmanTeardown(t *testing.T) {
	inventory.All.Vars.Ansible_user = "root"

	result := hostResult{
		host:          "root@10.184.101.66:22",
		errMsg:        errorText(errors.New("exit status 127"), []byte(wrongPasswordOutput)),
		passwordLogin: true,
	}

	msg := failureMessage(result)

	want := "root@10.184.101.66:22\nThe node rejected the password for root.\nssh: Permission denied (publickey,password)."
	if msg != want {
		t.Errorf("failureMessage =\n%q\nwant\n%q", msg, want)
	}
	for _, unwanted := range []string{"podman-static", "overlay-containers", "exit status", "Error:", "INFO:"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("failureMessage %q still shows %q", msg, unwanted)
		}
	}
}

func TestMeaningfulLineTakesTheBottomMostSshLine(t *testing.T) {
	output := "Permission denied, please try again.\n" +
		"root@10.0.0.1: Permission denied (publickey,password).\n" +
		"exit status 255\n"

	line := meaningfulLine(output)

	if line != "root@10.0.0.1: Permission denied (publickey,password)." {
		t.Errorf("meaningfulLine = %q, want ssh's final verdict", line)
	}
}

func TestMeaningfulLineIgnoresTheRuntimesOwnErrors(t *testing.T) {
	output := "Permission denied (publickey,password).\n" +
		"Error: lstat /var/lib/containers/storage: permission denied\n"

	line := meaningfulLine(output)

	if strings.HasPrefix(line, "Error:") {
		t.Errorf("meaningfulLine = %q, want the command's own line rather than the runtime's", line)
	}
}

func TestErrorTextAlwaysReportsSomething(t *testing.T) {
	if text := errorText(errors.New("exit status 1"), []byte("first line\nlast line\n")); text != "last line" {
		t.Errorf("errorText = %q, want the last line of the output", text)
	}

	// Nothing to show is still better said than shown as a bare exit status.
	if text := errorText(errors.New("exit status 127"), nil); text != "exit status 127" {
		t.Errorf("errorText = %q, want the error itself when the command said nothing", text)
	}
}

func TestSshReportLabelsSshsOwnLine(t *testing.T) {
	if got := sshReport("root@10.0.0.1: Permission denied (publickey,password)."); got != "ssh: Permission denied (publickey,password)." {
		t.Errorf("sshReport = %q, want ssh's line, labelled, with the host prefix dropped", got)
	}

	// A line ssh already labelled keeps its own wording.
	const labelled = "ssh: connect to host 10.0.0.1 port 22: Connection refused"
	if got := sshReport(labelled); got != labelled {
		t.Errorf("sshReport = %q, want %q unchanged", got, labelled)
	}

	// ssh-copy-id wraps ssh's message in its own path and "ERROR: ".
	const wrapped = "/usr/bin/ssh-copy-id: ERROR: ssh: connect to host 10.0.0.1 port 22: Connection refused"
	if got := sshReport(wrapped); got != "ssh: connect to host 10.0.0.1 port 22: Connection refused" {
		t.Errorf("sshReport = %q, want ssh's message without ssh-copy-id's wrapper", got)
	}
}

func TestFailureVerdictOnlyBlamesThePasswordForPasswordLogins(t *testing.T) {
	inventory.All.Vars.Ansible_user = "root"
	const refused = "ssh: Permission denied (publickey,password)."

	if verdict := failureVerdict(refused, true); !strings.Contains(verdict, "rejected the password for root") {
		t.Errorf("verdict = %q, want it to name the rejected password", verdict)
	}

	// The key-only probe sees the same line when a node offers a password it is
	// not allowed to use, and then the password is not the problem.
	if verdict := failureVerdict(refused, false); strings.Contains(verdict, "rejected the password") {
		t.Errorf("verdict = %q, want a key login not to be blamed on the password", verdict)
	}
}

// podmanTeardown is what the container runtime writes after a command it lost
// the container under: the failure the modal must never present as the node's
// answer.
const podmanTeardown = `exit status 125
Error: creating container storage: the container name "kubespray" is already in use by 8279eb8ecd5e8e6230de7f3470afd5efa07efda4090d8352b6358d91a`

func TestFailureVerdictNamesAContainerFailureBeforeSsh(t *testing.T) {
	verdict := failureVerdict(podmanTeardown, true)

	if verdict != containerGoneVerdict {
		t.Errorf("failureVerdict = %q, want the container verdict", verdict)
	}
	if strings.Contains(verdict, "password") {
		t.Errorf("failureVerdict = %q, want a container failure not blamed on the password", verdict)
	}
}

func TestFailureMessageHidesTheContainerRuntimesOwnWords(t *testing.T) {
	msg := failureMessage(hostResult{host: "root@10.184.101.67:22", errMsg: podmanTeardown, passwordLogin: true})

	if !strings.Contains(msg, containerGoneVerdict) {
		t.Errorf("failureMessage = %q, want the container verdict", msg)
	}
	for _, unwanted := range []string{"Error:", "creating container storage", "exit status"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("failureMessage %q still shows %q", msg, unwanted)
		}
	}
}

func TestAnswerFileNameDropsWhatAFilenameCannotHold(t *testing.T) {
	if got := answerFileName("root@10.0.0.1:22"); got != "root-10.0.0.1-22" {
		t.Errorf("answerFileName = %q, want %q", got, "root-10.0.0.1-22")
	}
}

func TestCheckAnswerFileNamesTheStageAndTheNode(t *testing.T) {
	originalPath := projectPath
	projectPath = "/project"
	defer func() { projectPath = originalPath }()

	got := checkAnswerFile("root@10.0.0.1:22", stageCopyKey)

	if want := "/project/check-root-10.0.0.1-22-copy-key.out"; got != want {
		t.Errorf("checkAnswerFile = %q, want %q", got, want)
	}
}

func TestRecordSilentStageKeepsWhatIsKnown(t *testing.T) {
	logPath := swapLog(t)

	recordSilentStage("root@10.0.0.1:22", stageKeyLogin, filepath.Join(projectPath, "check.out"), []byte("WARN[0000] Using cgroups-v1\n"), nil)

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading the run log: %v", err)
	}
	// The modal can only say the node answered nothing. This is what says
	// whether the command reached it at all.
	for _, want := range []string{"root@10.0.0.1:22", stageKeyLogin, "the command exited 0", "no answer file", "cgroups-v1"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log = %q, want it to contain %q", log, want)
		}
	}
}

func TestRecordSilentStageNamesAFailedCommand(t *testing.T) {
	logPath := swapLog(t)

	recordSilentStage("root@10.0.0.1:22", stageDiagnose, filepath.Join(projectPath, "check.out"), nil, errors.New("exit status 125"))

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading the run log: %v", err)
	}
	if !strings.Contains(string(log), "exit status 125") {
		t.Errorf("log = %q, want it to say how the command ended", log)
	}
}

func TestRedirectToFileSendsBothStreams(t *testing.T) {
	got := redirectToFile("ssh -i key root@10.0.0.1 id", "/data/idocluster/check.out")

	// ssh reports a refusal on its error stream, so the answer file has to hold
	// both streams.
	for _, want := range []string{"ssh -i key root@10.0.0.1 id", `> "/data/idocluster/check.out"`, "2>&1"} {
		if !strings.Contains(got, want) {
			t.Errorf("redirectToFile = %q, want it to contain %q", got, want)
		}
	}
}

// swapLog points the run log at a directory of the test's own.
func swapLog(t *testing.T) string {
	t.Helper()

	originalPath, originalLog := projectPath, logFileName
	projectPath = t.TempDir()
	logFileName = "run.log"
	t.Cleanup(func() { projectPath, logFileName = originalPath, originalLog })

	return filepath.Join(projectPath, logFileName)
}

func TestRecordCheckLogsPassesToo(t *testing.T) {
	logPath := swapLog(t)

	recordCheck("root@10.0.0.1:22", "key login", "uid=0(root) gid=0(root) groups=0(root)", true)

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading the run log: %v", err)
	}
	for _, want := range []string{"root@10.0.0.1:22", "key login", "ok", "uid=0(root)"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log = %q, want it to contain %q", log, want)
		}
	}
}

func TestRecordCheckKeepsTheWholeOutput(t *testing.T) {
	logPath := swapLog(t)

	recordCheck("root@10.184.101.66:22", "copy key", wrongPasswordOutput, false)

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading the run log: %v", err)
	}
	// The modal shows one line; the log has to keep what the node answered,
	// including the container runtime's own error.
	for _, want := range []string{"failed", "Permission denied (publickey,password).", "overlay-containers"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log = %q, want it to contain %q", log, want)
		}
	}
}

func TestCopySshKeyToNodeRefusesToSkipTheCheck(t *testing.T) {
	originalHosts := inventory.All.Hosts
	inventory.All.Hosts = nil
	defer func() { inventory.All.Hosts = originalHosts }()

	if _, _, err := copySshKeyToNode("s3cret"); err == nil {
		t.Error("copySshKeyToNode = nil error, want it to refuse an inventory with no nodes")
	}
}

func TestCopySshKeyToNodeStopsWhenThePasswordCannotBeWritten(t *testing.T) {
	originalHosts, originalPath := inventory.All.Hosts, projectPath
	inventory.All.Hosts = map[string]Host{"node1": {Ansible_host: "10.0.0.1"}}
	projectPath = t.TempDir()
	defer func() {
		inventory.All.Hosts, projectPath = originalHosts, originalPath
	}()

	// An existing deployment key keeps the copy from generating one, which
	// would need the container runtime.
	if err := os.WriteFile(sshKeyPath(), []byte("key"), 0600); err != nil {
		t.Fatalf("writing the key file: %v", err)
	}
	// A directory where the password file has to go: writing it cannot work,
	// and the copy has to stop before it reaches the container.
	if err := os.Mkdir(sshPasswordPath(), 0700); err != nil {
		t.Fatalf("making the password path unwritable: %v", err)
	}

	_, _, err := copySshKeyToNode("s3cret")

	if err == nil {
		t.Error("copySshKeyToNode = nil error, want it to stop when the password file cannot be written")
	}
}

func TestCapLinesKeepsShortOutputWhole(t *testing.T) {
	text, truncated := capLines("a\nb\nc", 5)

	if truncated || text != "a\nb\nc" {
		t.Errorf("capLines = (%q, %v), want the whole text and no truncation", text, truncated)
	}
}

func TestCapLinesCutsLongOutput(t *testing.T) {
	text, truncated := capLines("a\nb\nc", 2)

	if !truncated || text != "a\nb" {
		t.Errorf("capLines = (%q, %v), want the first two lines and truncation", text, truncated)
	}
}

func TestCapLinesIgnoresTrailingNewline(t *testing.T) {
	text, truncated := capLines("a\nb\n", 2)

	if truncated || text != "a\nb" {
		t.Errorf("capLines = (%q, %v), want the two lines and no truncation", text, truncated)
	}
}
