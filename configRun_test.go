package main

// Tests for the config-driven run preparation and the prefilled Deploy page.
// They run without root, podman or a terminal.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
)

func snapshotInstallGlobals(t *testing.T) {
	t.Helper()

	oldHome, oldApp, oldKubespray := homePath, appPath, kubesprayPath
	oldProject, oldSetup := projectPath, setupNewCluster
	oldInventory, oldOriginal, oldVars := inventory, originalInventory, extraVars
	oldInventoryFile := inventoryFile
	oldNodeIps, oldUser, oldPort, oldPrefix := nodeIps, nodeSshUser, nodeSshPort, nodeHostnamePrefix
	oldSSHKey, oldAccess := sshKeyFile, accessMethod
	oldHAMode, oldVIP := haMode, vip
	oldPlugin, oldCheck, oldNetLoaded := networkPlugin, checkConflicts, initialNetworkLoaded
	oldMirror, oldMirrors, oldMirrorLoaded := enableMirror, mirrors, initialValueLoaded
	oldSSHPass, oldSudoPass := prefillSSHPassword, prefillSudoPassword
	oldLog := logFileName
	oldDry, oldConfigRun, oldAuto := dryRun, configRun, autoRun
	oldExitCode := autoRunExitCode.Load()
	oldAppConfig := appConfig

	t.Cleanup(func() {
		homePath, appPath, kubesprayPath = oldHome, oldApp, oldKubespray
		projectPath, setupNewCluster = oldProject, oldSetup
		inventory, originalInventory, extraVars = oldInventory, oldOriginal, oldVars
		inventoryFile = oldInventoryFile
		nodeIps, nodeSshUser, nodeSshPort, nodeHostnamePrefix = oldNodeIps, oldUser, oldPort, oldPrefix
		sshKeyFile, accessMethod = oldSSHKey, oldAccess
		haMode, vip = oldHAMode, oldVIP
		networkPlugin, checkConflicts, initialNetworkLoaded = oldPlugin, oldCheck, oldNetLoaded
		enableMirror, mirrors, initialValueLoaded = oldMirror, oldMirrors, oldMirrorLoaded
		prefillSSHPassword, prefillSudoPassword = oldSSHPass, oldSudoPass
		logFileName = oldLog
		dryRun, configRun, autoRun = oldDry, oldConfigRun, oldAuto
		autoRunExitCode.Store(oldExitCode)
		appConfig = oldAppConfig
	})
}

func prepareFakeKubespray(t *testing.T, dir string) string {
	t.Helper()

	kubesprayDir := filepath.Join(dir, "kubespray")
	writeTestFile(t, filepath.Join(kubesprayDir, "inventory/sample/group_vars/k8s_cluster/k8s-cluster.yml"),
		"kube_service_addresses: 10.233.0.0/18\nkube_pods_subnet: 10.233.64.0/18\n")
	writeTestFile(t, filepath.Join(kubesprayDir, "inventory/sample/group_vars/all/offline.yml"),
		"# files_repo: \"http://myprivatehttpd\"\n# kubeadm_download_url: \"{{ files_repo }}/kubeadm\"\n")

	return kubesprayDir
}

func configForPrepare(t *testing.T, projectDir string) *installConfig {
	t.Helper()

	m := validDetailedConfig()
	m["project_path"] = projectDir
	m["network"] = map[string]any{"plugin": "calico", "check_conflicts": false}
	m["mirror"] = map[string]any{"enabled": true, "urls": map[string]any{"gcr_image_repo": "gcr.override"}}
	m["features"] = map[string]any{"ntp_enabled": false}

	return mustInstallConfig(t, m)
}

func TestPrepareInstallWritesProjectAndPrefills(t *testing.T) {
	snapshotInstallGlobals(t)

	dir := t.TempDir()
	appPath = filepath.Join(dir, "app")
	kubesprayPath = prepareFakeKubespray(t, appPath)
	appConfig = testAppConfig()
	dryRun = true

	projectDir := filepath.Join(dir, "project")
	cfg := configForPrepare(t, projectDir)

	if err := prepareInstall(cfg); err != nil {
		t.Fatalf("prepare install: %v", err)
	}

	hostsData, err := os.ReadFile(filepath.Join(projectDir, "hosts.yaml"))
	if err != nil {
		t.Fatalf("read hosts.yaml: %v", err)
	}
	var inv Inventory
	if err := yaml.Unmarshal(hostsData, &inv); err != nil {
		t.Fatalf("parse hosts.yaml: %v", err)
	}
	if len(inv.All.Hosts) != 3 {
		t.Errorf("hosts = %d, want 3", len(inv.All.Hosts))
	}
	if _, ok := inv.All.Children.Kube_node.Hosts["node3"]; !ok {
		t.Error("node3 is not in kube_node")
	}
	if inv.All.Vars.Ansible_user != "root" || inv.All.Vars.Ansible_port != "22" {
		t.Errorf("all.vars = %+v, want root/22", inv.All.Vars)
	}

	extraData, err := os.ReadFile(filepath.Join(projectDir, "extra-vars.yaml"))
	if err != nil {
		t.Fatalf("read extra-vars.yaml: %v", err)
	}
	var vars map[string]any
	if err := yaml.Unmarshal(extraData, &vars); err != nil {
		t.Fatalf("parse extra-vars.yaml: %v", err)
	}
	if vars["ntp_enabled"] != false {
		t.Errorf("ntp_enabled = %v, want false", vars["ntp_enabled"])
	}
	if vars["kube_network_plugin"] != "calico" {
		t.Errorf("kube_network_plugin = %v, want calico", vars["kube_network_plugin"])
	}
	if vars["gcr_image_repo"] != "gcr.override" {
		t.Errorf("gcr_image_repo = %v, want the config override", vars["gcr_image_repo"])
	}

	mirrorData, err := os.ReadFile(filepath.Join(projectDir, "group_vars/all/mirror.yml"))
	if err != nil {
		t.Fatalf("read mirror.yml: %v", err)
	}
	if !strings.Contains(string(mirrorData), "kubeadm_download_url: \"{{ files_repo }}/kubeadm\"") {
		t.Errorf("mirror.yml did not uncomment files_repo usage:\n%s", mirrorData)
	}

	for name, data := range map[string][]byte{"hosts.yaml": hostsData, "extra-vars.yaml": extraData} {
		if strings.Contains(string(data), "secret") {
			t.Errorf("%s contains the SSH password", name)
		}
	}

	for _, name := range []string{"ansible-password", "ansible-become-password"} {
		if _, err := os.Stat(filepath.Join(projectDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after a dry-run preparation", name)
		}
	}
	if logFileName != "" {
		t.Errorf("log file name = %q, want none in dry-run", logFileName)
	}

	if projectPath != projectDir {
		t.Errorf("projectPath = %q, want %q", projectPath, projectDir)
	}
	if !setupNewCluster {
		t.Error("setupNewCluster = false, want true")
	}
	if accessMethod != accessMethodsType.new {
		t.Errorf("accessMethod = %q, want the new-key method", accessMethod)
	}

	prefillTUI(cfg)
	if prefillSSHPassword != "secret" {
		t.Errorf("prefilled SSH password = %q, want secret", prefillSSHPassword)
	}
	if haMode != "localhost loadbalancing" {
		t.Errorf("haMode = %q, want localhost loadbalancing", haMode)
	}
	if networkPlugin != "calico" {
		t.Errorf("networkPlugin = %q, want calico", networkPlugin)
	}
	if checkConflicts {
		t.Error("checkConflicts = true, want false from the config")
	}
	if !enableMirror || mirrors["gcr_image_repo"] != "gcr.override" {
		t.Errorf("mirror prefill = %v / %v, want enabled with the override", enableMirror, mirrors["gcr_image_repo"])
	}
}

func TestPrefillPagesHandlesNonNumericHostNames(t *testing.T) {
	snapshotInstallGlobals(t)

	dir := t.TempDir()
	appPath = filepath.Join(dir, "app")
	kubesprayPath = prepareFakeKubespray(t, appPath)
	appConfig = testAppConfig()
	dryRun = true
	configRun = true

	m := validDetailedConfig()
	hosts := m["hosts"].([]any)
	hosts[0].(map[string]any)["name"] = "cp-a"
	hosts[1].(map[string]any)["name"] = "cp-b"
	hosts[2].(map[string]any)["name"] = "worker-a"
	m["project_path"] = filepath.Join(dir, "project")
	m["network"] = map[string]any{"plugin": "flannel", "check_conflicts": false}
	cfg := mustInstallConfig(t, m)

	if err := prepareInstall(cfg); err != nil {
		t.Fatalf("prepare install: %v", err)
	}

	// Building the Edit Hosts page calls getHostsList; names that do not end in
	// a digit used to panic its sort comparator.
	prefillPages(cfg)
}

func TestPrepareInstallReusesProjectAndKeepsTheKey(t *testing.T) {
	snapshotInstallGlobals(t)

	dir := t.TempDir()
	appPath = filepath.Join(dir, "app")
	kubesprayPath = prepareFakeKubespray(t, appPath)
	appConfig = testAppConfig()
	dryRun = true

	projectDir := filepath.Join(dir, "project")
	keyPath := filepath.Join(projectDir, "ansible-key")
	writeTestFile(t, keyPath, "existing key")

	cfg := configForPrepare(t, projectDir)
	if err := prepareInstall(cfg); err != nil {
		t.Fatalf("prepare install: %v", err)
	}

	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read preserved key: %v", err)
	}
	if string(data) != "existing key" {
		t.Errorf("ansible-key = %q, want the existing key", data)
	}
}

func TestDeployPagePrefill(t *testing.T) {
	oldAccess, oldSSHKey := accessMethod, sshKeyFile
	oldSSHPass, oldSudoPass := prefillSSHPassword, prefillSudoPassword
	oldDry, oldConfigRun := dryRun, configRun
	oldInventory, oldVars, oldApp := inventory, extraVars, appConfig
	t.Cleanup(func() {
		accessMethod, sshKeyFile = oldAccess, oldSSHKey
		prefillSSHPassword, prefillSudoPassword = oldSSHPass, oldSudoPass
		dryRun, configRun = oldDry, oldConfigRun
		inventory, extraVars, appConfig = oldInventory, oldVars, oldApp
		flexDeployCluster.Clear()
	})

	inventory = Inventory{}
	inventory.All.Vars.Ansible_user = "root"
	inventory.All.Vars.Ansible_port = "22"
	inventory.All.Hosts = map[string]Host{
		"node1": {Ansible_host: "10.0.0.11", Ip: "10.0.0.11", Access_ip: "10.0.0.11"},
	}
	extraVars = map[string]any{}
	appConfig = AppConfig{}

	prefillSSHPassword = "secret"
	prefillSudoPassword = ""
	accessMethod = accessMethodsType.new

	t.Run("dry run", func(t *testing.T) {
		dryRun = true
		flexDeployCluster.Clear()
		initFlexDeployCluster()

		formUp, ok := flexDeployCluster.GetItem(0).(*tview.Form)
		if !ok {
			t.Fatalf("first item = %T, want *tview.Form", flexDeployCluster.GetItem(0))
		}
		field := formUp.GetFormItemByLabel("SSH Password: ")
		if field == nil {
			t.Fatal("SSH Password field not found")
		}
		input, ok := field.(*tview.InputField)
		if !ok || input.GetText() != "secret" {
			t.Fatalf("SSH Password field = %#v, want it prefilled with secret", field)
		}
		if !strings.Contains(formUp.GetTitle(), "dry run") {
			t.Errorf("page title = %q, want a dry-run notice", formUp.GetTitle())
		}

		formDown, ok := flexDeployCluster.GetItem(1).(*tview.Form)
		if !ok {
			t.Fatalf("second item = %T, want *tview.Form", flexDeployCluster.GetItem(1))
		}
		start := formDown.GetButton(formDown.GetButtonIndex("Start"))
		if start == nil || !start.IsDisabled() {
			t.Error("Start button is not disabled in dry-run mode")
		}
	})

	t.Run("real run", func(t *testing.T) {
		dryRun = false
		flexDeployCluster.Clear()
		initFlexDeployCluster()

		formUp := flexDeployCluster.GetItem(0).(*tview.Form)
		if strings.Contains(formUp.GetTitle(), "dry run") {
			t.Errorf("page title = %q, want no dry-run notice", formUp.GetTitle())
		}
		formDown := flexDeployCluster.GetItem(1).(*tview.Form)
		start := formDown.GetButton(formDown.GetButtonIndex("Start"))
		if start == nil || start.IsDisabled() {
			t.Error("Start button is disabled in a real run")
		}
	})

	t.Run("config run keeps the install file's values", func(t *testing.T) {
		configRun = true
		dryRun = false
		appConfig = testAppConfig() // Default_vars says flannel
		extraVars = map[string]any{"kube_network_plugin": "calico"}
		accessMethod = accessMethodsType.new
		prefillSSHPassword = ""
		flexDeployCluster.Clear()

		initFlexDeployCluster()

		if got := extraVars["kube_network_plugin"]; got != "calico" {
			t.Fatalf("extraVars network plugin = %v, want calico from the install file", got)
		}
		formUp := flexDeployCluster.GetItem(0).(*tview.Form)
		item := formUp.GetFormItemByLabel("Extra vars: ")
		area, ok := item.(*tview.TextArea)
		if !ok {
			t.Fatalf("Extra vars item = %T, want *tview.TextArea", item)
		}
		if !strings.Contains(area.GetText(), "kube_network_plugin: calico") {
			t.Fatalf("Extra vars text area does not show the install file's calico value:\n%s", area.GetText())
		}
	})
}
