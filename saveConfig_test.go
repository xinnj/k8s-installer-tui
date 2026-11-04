package main

// Tests for the Save Config page. They build the page directly and need no
// root, podman or terminal.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rivo/tview"
)

func TestInitSaveConfigStatePrefills(t *testing.T) {
	snapshotInstallGlobals(t)

	t.Run("inventory values and the configured output", func(t *testing.T) {
		resetSaveConfigState()
		createConfigPath = "out.yaml"
		projectPath = t.TempDir()
		inventory = testExportInventory()

		initSaveConfigDefaults()

		if saveConfigPath != "out.yaml" {
			t.Errorf("output path = %q, want out.yaml", saveConfigPath)
		}
		if saveSSHUser != "root" {
			t.Errorf("SSH user = %q, want root from the inventory", saveSSHUser)
		}
		if saveSSHPort != "22" {
			t.Errorf("SSH port = %q, want 22 from the inventory", saveSSHPort)
		}
		if saveAccessMethod != saveAccessPassword {
			t.Errorf("access method = %q, want password without a project key", saveAccessMethod)
		}
	})

	t.Run("an existing project key selects key mode", func(t *testing.T) {
		resetSaveConfigState()
		createConfigPath = ""
		projectPath = t.TempDir()
		inventory = testExportInventory()
		writeTestFile(t, sshKeyPath(), "key")

		initSaveConfigDefaults()

		if saveConfigPath != defaultCreateConfigPath {
			t.Errorf("output path = %q, want %q", saveConfigPath, defaultCreateConfigPath)
		}
		if saveAccessMethod != saveAccessExistingKey {
			t.Errorf("access method = %q, want the existing key", saveAccessMethod)
		}
		if saveSSHKeyFile != sshKeyPath() {
			t.Errorf("key file = %q, want %q", saveSSHKeyFile, sshKeyPath())
		}
	})

	t.Run("falls back to the new project fields", func(t *testing.T) {
		resetSaveConfigState()
		createConfigPath = ""
		projectPath = t.TempDir()
		inventory = Inventory{}
		nodeSshUser = "ops"
		nodeSshPort = "2222"

		initSaveConfigDefaults()

		if saveSSHUser != "ops" {
			t.Errorf("SSH user = %q, want the new project user", saveSSHUser)
		}
		if saveSSHPort != "2222" {
			t.Errorf("SSH port = %q, want the new project port", saveSSHPort)
		}
	})
}

func TestSaveConfigPagePasswordMode(t *testing.T) {
	snapshotInstallGlobals(t)
	resetSaveConfigState()
	createConfigPath = "install.yaml"
	projectPath = t.TempDir()
	inventory = testExportInventory()
	saveAccessMethod = saveAccessPassword
	saveSSHPassword = "secret"

	flexSaveConfig.Clear()
	initFlexSaveConfig()

	formUp, ok := flexSaveConfig.GetItem(0).(*tview.Form)
	if !ok {
		t.Fatalf("first item = %T, want *tview.Form", flexSaveConfig.GetItem(0))
	}
	output, ok := formUp.GetFormItemByLabel("Output file: ").(*tview.InputField)
	if !ok || output.GetText() != "install.yaml" {
		t.Fatalf("output field = %#v, want install.yaml", formUp.GetFormItemByLabel("Output file: "))
	}
	password, ok := formUp.GetFormItemByLabel("SSH password: ").(*tview.InputField)
	if !ok || password.GetText() != "secret" {
		t.Fatalf("password field = %#v, want the prefilled secret", formUp.GetFormItemByLabel("SSH password: "))
	}
	if item := formUp.GetFormItemByLabel("SSH key file: "); item != nil {
		t.Errorf("key field is present in password mode: %T", item)
	}

	formDown, ok := flexSaveConfig.GetItem(2).(*tview.Form)
	if !ok {
		t.Fatalf("third item = %T, want *tview.Form", flexSaveConfig.GetItem(2))
	}
	for _, label := range []string{"Create Config", "Back", "Quit"} {
		if formDown.GetButtonIndex(label) < 0 {
			t.Errorf("button %q is missing", label)
		}
	}
}

func TestSaveConfigPageKeyMode(t *testing.T) {
	snapshotInstallGlobals(t)
	resetSaveConfigState()
	createConfigPath = ""
	projectPath = t.TempDir()
	inventory = testExportInventory()
	inventory.All.Vars.Ansible_user = "ops"
	saveAccessMethod = saveAccessExistingKey
	saveSSHKeyFile = filepath.Join(t.TempDir(), "id_ed25519")
	saveSudoPassword = "sudo"

	flexSaveConfig.Clear()
	initFlexSaveConfig()

	formUp, ok := flexSaveConfig.GetItem(0).(*tview.Form)
	if !ok {
		t.Fatalf("first item = %T, want *tview.Form", flexSaveConfig.GetItem(0))
	}
	key, ok := formUp.GetFormItemByLabel("SSH key file: ").(*sshKeyField)
	if !ok {
		t.Fatalf("key field = %#v, want *sshKeyField", formUp.GetFormItemByLabel("SSH key file: "))
	}
	if key.input.GetText() != saveSSHKeyFile {
		t.Errorf("key text = %q, want %q", key.input.GetText(), saveSSHKeyFile)
	}
	sudo, ok := formUp.GetFormItemByLabel("Sudo password (non-root user, optional): ").(*tview.InputField)
	if !ok || sudo.GetText() != "sudo" {
		t.Fatalf("sudo field = %#v, want the prefilled sudo password", formUp.GetFormItemByLabel("Sudo password (non-root user, optional): "))
	}
	if item := formUp.GetFormItemByLabel("SSH password: "); item != nil {
		t.Errorf("password field is present in key mode: %T", item)
	}
}

func TestSaveConfigRefreshesUneditedSSH(t *testing.T) {
	snapshotInstallGlobals(t)
	resetSaveConfigState()
	createConfigPath = ""
	projectPath = t.TempDir()
	inventory = testExportInventory()

	flexSaveConfig.Clear()
	initFlexSaveConfig()

	form := flexSaveConfig.GetItem(0).(*tview.Form)
	if got := form.GetFormItemByLabel("SSH user: ").(*tview.InputField).GetText(); got != "root" {
		t.Fatalf("SSH user = %q, want the inventory user", got)
	}

	// The hosts page changes the inventory; returning to Save Config must show
	// the new values, not the ones captured on the first visit.
	inventory.All.Vars.Ansible_user = "ops"
	inventory.All.Vars.Ansible_port = "2222"
	flexSaveConfig.Clear()
	initFlexSaveConfig()

	form = flexSaveConfig.GetItem(0).(*tview.Form)
	if got := form.GetFormItemByLabel("SSH user: ").(*tview.InputField).GetText(); got != "ops" {
		t.Fatalf("SSH user = %q, want the updated inventory user", got)
	}
	if got := form.GetFormItemByLabel("SSH port: ").(*tview.InputField).GetText(); got != "2222" {
		t.Fatalf("SSH port = %q, want the updated inventory port", got)
	}

	// An operator edit outranks the inventory on the next rebuild.
	saveSSHUser = "custom"
	flexSaveConfig.Clear()
	initFlexSaveConfig()

	form = flexSaveConfig.GetItem(0).(*tview.Form)
	if got := form.GetFormItemByLabel("SSH user: ").(*tview.InputField).GetText(); got != "custom" {
		t.Fatalf("SSH user = %q, want the operator's edit", got)
	}
}

func TestOutputFileState(t *testing.T) {
	dir := t.TempDir()

	if exists, err := outputFileState(filepath.Join(dir, "missing.yaml")); err != nil || exists {
		t.Fatalf("missing path = %v / %v, want absent without an error", exists, err)
	}

	file := filepath.Join(dir, "install.yaml")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if exists, err := outputFileState(file); err != nil || !exists {
		t.Fatalf("existing file = %v / %v, want present without an error", exists, err)
	}

	if _, err := outputFileState(dir); err == nil {
		t.Fatal("directory target was accepted, want an error")
	}
}

func TestSaveConfigInput(t *testing.T) {
	snapshotInstallGlobals(t)

	prepare := func() {
		projectPath = "/tmp/project"
		nodeHostnamePrefix = "node"
		inventory = testExportInventory()
		extraVars = map[string]any{"kube_network_plugin": "flannel"}
		appConfig = testAppConfig()
		haMode = "localhost loadbalancing"
		vip = ""
		checkConflicts = true
		enableMirror = false
		mirrors = nil
		saveSSHUser = "root"
		saveSSHPort = "22"
		saveSSHKeyFile = ""
		saveSSHPassword = "secret"
		saveSudoPassword = ""
	}

	t.Run("password", func(t *testing.T) {
		prepare()
		saveAccessMethod = saveAccessPassword

		in, err := saveConfigInput()
		if err != nil {
			t.Fatalf("input: %v", err)
		}
		if in.SSHPassword != "secret" || in.SSHKeyFile != "" || in.SSHPort != 22 {
			t.Fatalf("input = %+v, want password mode on port 22", in)
		}
		if !in.CheckConflicts || in.MirrorEnabled {
			t.Errorf("check/mirror = %v/%v, want true/false", in.CheckConflicts, in.MirrorEnabled)
		}
	})

	t.Run("missing password", func(t *testing.T) {
		prepare()
		saveAccessMethod = saveAccessPassword
		saveSSHPassword = ""

		if _, err := saveConfigInput(); err == nil {
			t.Fatal("empty password was accepted")
		}
	})

	t.Run("key", func(t *testing.T) {
		prepare()
		saveAccessMethod = saveAccessExistingKey
		saveSSHKeyFile = "/tmp/key"
		saveSudoPassword = "sudo"

		in, err := saveConfigInput()
		if err != nil {
			t.Fatalf("input: %v", err)
		}
		if in.SSHKeyFile != "/tmp/key" || in.SudoPassword != "sudo" || in.SSHPassword != "" {
			t.Fatalf("input = %+v, want key mode with a sudo password", in)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		prepare()
		saveAccessMethod = saveAccessExistingKey
		saveSSHKeyFile = ""

		if _, err := saveConfigInput(); err == nil {
			t.Fatal("empty key path was accepted")
		}
	})

	t.Run("bad port", func(t *testing.T) {
		prepare()
		saveAccessMethod = saveAccessPassword
		saveSSHPort = "nope"

		if _, err := saveConfigInput(); err == nil {
			t.Fatal("non-numeric port was accepted")
		}
	})

	t.Run("empty port uses 22", func(t *testing.T) {
		prepare()
		saveAccessMethod = saveAccessPassword
		saveSSHPort = ""

		in, err := saveConfigInput()
		if err != nil {
			t.Fatalf("input: %v", err)
		}
		if in.SSHPort != 22 {
			t.Fatalf("port = %d, want 22", in.SSHPort)
		}
	})
}
