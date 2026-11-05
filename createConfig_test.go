package main

// Tests for turning the live wizard state into an install config file. They run
// without root, podman or a terminal.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testExportInventory() Inventory {
	var inv Inventory
	inv.All.Hosts = map[string]Host{
		"node1": {Ip: "10.0.0.11", Access_ip: "10.0.0.11", Ansible_host: "10.0.0.11", Node_labels: map[string]string{"zone": "zone1"}},
		"node2": {Ip: "10.0.0.12"},
		"node3": {Ip: "10.0.0.13", Access_ip: "10.0.0.23", Node_taints: []string{"logging:NoSchedule"}},
	}
	inv.All.Children.Kube_control_plane.Hosts = map[string]map[any]any{"node1": {}, "node2": {}}
	inv.All.Children.Kube_node.Hosts = map[string]map[any]any{"node1": {}, "node3": {}}
	inv.All.Children.Etcd.Hosts = map[string]map[any]any{"node1": {}, "node2": {}, "node3": {}}
	inv.All.Children.Calico_rr.Hosts = map[string]map[any]any{}
	inv.All.Vars.Ansible_user = "root"
	inv.All.Vars.Ansible_port = "22"

	return inv
}

func validExportInput(t *testing.T) createConfigInput {
	t.Helper()

	return createConfigInput{
		ProjectPath:    filepath.Join(t.TempDir(), "project"),
		HostnamePrefix: "node",
		Inventory:      testExportInventory(),
		ExtraVars: map[string]any{
			"kube_network_plugin":    "flannel",
			"kube_service_addresses": "10.233.0.0/18",
			"kube_pods_subnet":       "10.233.64.0/18",
		},
		App:            testAppConfig(),
		CheckConflicts: true,
		SSHUser:        "root",
		SSHPort:        22,
		SSHPassword:    "secret",
	}
}

func TestBuildInstallConfigHosts(t *testing.T) {
	in := validExportInput(t)

	cfg, err := buildInstallConfig(in)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	if len(cfg.Hosts.Auto) != 0 {
		t.Fatalf("auto hosts = %v, want none", cfg.Hosts.Auto)
	}

	var names []string
	for _, host := range cfg.Hosts.Detailed {
		names = append(names, host.Name)
	}
	if strings.Join(names, ",") != "node1,node2,node3" {
		t.Fatalf("host order = %v, want sorted names", names)
	}

	byName := map[string]installHost{}
	for _, host := range cfg.Hosts.Detailed {
		byName[host.Name] = host
	}
	if got := strings.Join(byName["node1"].Roles, ","); got != "kube_control_plane,kube_node,etcd" {
		t.Errorf("node1 roles = %q, want control-plane, node, etcd", got)
	}
	if got := strings.Join(byName["node3"].Roles, ","); got != "kube_node,etcd" {
		t.Errorf("node3 roles = %q, want node, etcd", got)
	}
	if byName["node2"].AccessIP != "10.0.0.12" || byName["node2"].AnsibleHost != "10.0.0.12" {
		t.Errorf("node2 fallbacks = %q / %q, want the IP", byName["node2"].AccessIP, byName["node2"].AnsibleHost)
	}
	if byName["node3"].AccessIP != "10.0.0.23" || byName["node3"].AnsibleHost != "10.0.0.23" {
		t.Errorf("node3 addresses = %q / %q, want the access IP", byName["node3"].AccessIP, byName["node3"].AnsibleHost)
	}
	if !reflect.DeepEqual(byName["node3"].Taints, []string{"logging:NoSchedule"}) {
		t.Errorf("node3 taints = %v, want the inventory taint", byName["node3"].Taints)
	}
	if !reflect.DeepEqual(byName["node1"].Labels, map[string]string{"zone": "zone1"}) {
		t.Errorf("node1 labels = %v, want the inventory label", byName["node1"].Labels)
	}
}

func TestBuildInstallConfigHostWithoutRole(t *testing.T) {
	in := validExportInput(t)
	in.Inventory.All.Hosts["node4"] = Host{Ip: "10.0.0.14"}

	if _, err := buildInstallConfig(in); err == nil || !strings.Contains(err.Error(), "node4") || !strings.Contains(err.Error(), "role") {
		t.Fatalf("host without a role = %v, want an error naming node4 and its role", err)
	}
}

func TestBuildInstallConfigHostWithoutIP(t *testing.T) {
	in := validExportInput(t)
	in.Inventory.All.Hosts["node1"] = Host{}

	if _, err := buildInstallConfig(in); err == nil || !strings.Contains(strings.ToLower(err.Error()), "ip") {
		t.Fatalf("host without an IP = %v, want an IP error", err)
	}
}

func TestBuildInstallConfigClassifiesVars(t *testing.T) {
	in := validExportInput(t)
	in.App.Default_vars["keep_default"] = "same"
	in.App.Default_vars["override_default"] = "original"
	in.ExtraVars = map[string]any{
		"ntp_enabled":                        false,
		"containerd_registries_mirrors":      []any{},
		"worker_count":                       5,
		"extra_map":                          map[string]any{"k": "v"},
		"kube_network_plugin":                "calico",
		"kube_service_addresses":             "10.233.0.0/18",
		"kube_pods_subnet":                   "10.233.64.0/18",
		"calico_vxlan_mode":                  "CrossSubnet",
		"loadbalancer_apiserver_localhost":   false,
		"kube_vip_enabled":                   true,
		"kube_vip_controlplane_enabled":      true,
		"kube_vip_arp_enabled":               true,
		"kube_proxy_strict_arp":              true,
		"kube_vip_lb_enable":                 true,
		"kube_vip_address":                   "10.0.0.100",
		"loadbalancer_apiserver":             map[string]any{"address": "10.0.0.100", "port": "6443"},
		"apiserver_loadbalancer_domain_name": "10.0.0.100",
		"gcr_image_repo":                     "gcr.override",
		"files_repo":                         "https://files.override",
		"kube_proxy_mode":                    "ipvs",
		"keep_default":                       "same",
		"override_default":                   "changed",
	}
	in.HAMode = "kube-vip"
	in.VIP = "10.0.0.100"
	in.CheckConflicts = false
	in.MirrorEnabled = true
	in.Mirrors = map[string]string{
		"gcr_image_repo": "gcr.override",
		"files_repo":     "https://files.override",
	}

	cfg, err := buildInstallConfig(in)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}

	if cfg.Features["ntp_enabled"] != false {
		t.Errorf("feature ntp_enabled = %v, want false", cfg.Features["ntp_enabled"])
	}
	if cfg.Features["worker_count"] != 5 {
		t.Errorf("feature worker_count = %v, want 5", cfg.Features["worker_count"])
	}
	if cfg.Features["cluster_name"] != "default" {
		t.Errorf("feature cluster_name = %v, want the configured default", cfg.Features["cluster_name"])
	}

	wantExtra := map[string]any{
		"kube_proxy_mode":  "ipvs",
		"override_default": "changed",
	}
	if !reflect.DeepEqual(cfg.ExtraVars, wantExtra) {
		t.Errorf("extra_vars = %#v, want %#v", cfg.ExtraVars, wantExtra)
	}

	if cfg.Network.Plugin != "calico" || cfg.Network.CalicoVxlanMode != "CrossSubnet" {
		t.Errorf("network = %+v, want calico/CrossSubnet", cfg.Network)
	}
	if cfg.Network.CheckConflicts == nil || *cfg.Network.CheckConflicts {
		t.Errorf("check_conflicts = %v, want an explicit false", cfg.Network.CheckConflicts)
	}
	if cfg.HA.Mode != "kube-vip" || cfg.HA.VIP != "10.0.0.100" {
		t.Errorf("ha = %+v, want kube-vip at 10.0.0.100", cfg.HA)
	}
	if !cfg.Mirror.Enabled || cfg.Mirror.URLs["gcr_image_repo"] != "gcr.override" ||
		cfg.Mirror.URLs["files_repo"] != "https://files.override" {
		t.Errorf("mirror = %+v, want the overrides", cfg.Mirror)
	}
}

func TestBuildInstallConfigCredentials(t *testing.T) {
	t.Run("password", func(t *testing.T) {
		in := validExportInput(t)
		cfg, err := buildInstallConfig(in)
		if err != nil {
			t.Fatalf("build config: %v", err)
		}
		if cfg.SSH.Password != "secret" || cfg.SSH.KeyFile != "" || cfg.SSH.SudoPassword != "" {
			t.Fatalf("ssh = %+v, want password mode only", cfg.SSH)
		}
	})

	t.Run("existing key", func(t *testing.T) {
		keyPath := filepath.Join(t.TempDir(), "id_ed25519")
		if err := os.WriteFile(keyPath, []byte("key"), 0600); err != nil {
			t.Fatal(err)
		}

		in := validExportInput(t)
		in.SSHUser = "ops"
		in.SSHPort = 2222
		in.SSHPassword = ""
		in.SSHKeyFile = keyPath
		in.SudoPassword = "sudo"

		cfg, err := buildInstallConfig(in)
		if err != nil {
			t.Fatalf("build config: %v", err)
		}
		if cfg.SSH.Password != "" || cfg.SSH.KeyFile != keyPath || cfg.SSH.SudoPassword != "sudo" {
			t.Fatalf("ssh = %+v, want key mode with a sudo password", cfg.SSH)
		}
	})
}

func TestBuildInstallConfigRejectsEvenEtcd(t *testing.T) {
	in := validExportInput(t)
	delete(in.Inventory.All.Children.Etcd.Hosts, "node3")

	if _, err := buildInstallConfig(in); err == nil || !strings.Contains(err.Error(), "odd") {
		t.Fatalf("even etcd = %v, want an odd-count error", err)
	}
}

func TestBuildInstallConfigFallsBackToHostnamePrefix(t *testing.T) {
	in := validExportInput(t)
	in.HostnamePrefix = "bad_prefix!"
	in.Inventory = exportInventoryNamed("alpha1", "beta2", "gamma3")

	cfg, err := buildInstallConfig(in)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	if cfg.HostnamePrefix != "node" {
		t.Fatalf("hostname prefix = %q, want the node fallback", cfg.HostnamePrefix)
	}
}

// exportInventoryNamed builds a valid three-host inventory with the given
// names, so a test can control what prefix the names imply.
func exportInventoryNamed(names ...string) Inventory {
	var inv Inventory
	inv.All.Hosts = map[string]Host{}
	inv.All.Children.Kube_control_plane.Hosts = map[string]map[any]any{}
	inv.All.Children.Kube_node.Hosts = map[string]map[any]any{}
	inv.All.Children.Etcd.Hosts = map[string]map[any]any{}
	inv.All.Children.Calico_rr.Hosts = map[string]map[any]any{}

	for index, name := range names {
		ip := fmt.Sprintf("10.0.0.%d", 11+index)
		inv.All.Hosts[name] = Host{Ip: ip, Access_ip: ip, Ansible_host: ip}
		inv.All.Children.Kube_control_plane.Hosts[name] = map[any]any{}
		inv.All.Children.Kube_node.Hosts[name] = map[any]any{}
		inv.All.Children.Etcd.Hosts[name] = map[any]any{}
	}
	inv.All.Vars.Ansible_user = "root"
	inv.All.Vars.Ansible_port = "22"

	return inv
}

func TestBuildInstallConfigDerivesHostnamePrefix(t *testing.T) {
	in := validExportInput(t)
	in.HostnamePrefix = "node"
	in.Inventory = exportInventoryNamed("onpremisis-1", "onpremisis-2", "onpremisis-3")

	cfg, err := buildInstallConfig(in)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	if cfg.HostnamePrefix != "onpremisis-" {
		t.Fatalf("hostname prefix = %q, want onpremisis-", cfg.HostnamePrefix)
	}
}

func TestBuildInstallConfigKeepsPrefixForMixedHostnames(t *testing.T) {
	in := validExportInput(t)
	in.HostnamePrefix = "farm"
	in.Inventory = exportInventoryNamed("alpha1", "beta2", "gamma3")

	cfg, err := buildInstallConfig(in)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	if cfg.HostnamePrefix != "farm" {
		t.Fatalf("hostname prefix = %q, want the supplied farm", cfg.HostnamePrefix)
	}
}

func TestBuildInstallConfigRejectsBothCredentials(t *testing.T) {
	in := validExportInput(t)
	in.SSHKeyFile = filepath.Join(t.TempDir(), "id_ed25519")

	if _, err := buildInstallConfig(in); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("password plus key = %v, want an exclusivity error", err)
	}
}

func TestGeneratedConfigReproducesExtraVarsPrecedence(t *testing.T) {
	in := validExportInput(t)
	in.HAMode = "localhost loadbalancing"
	in.VIP = ""
	in.ExtraVars = map[string]any{
		"ntp_enabled":            false,
		"kube_network_plugin":    "calico",
		"calico_vxlan_mode":      "CrossSubnet",
		"kube_service_addresses": "10.233.0.0/18",
		"kube_pods_subnet":       "10.233.64.0/18",
		"gcr_image_repo":         "gcr.override",
		"kube_proxy_mode":        "ipvs",
		// A stale HA key must not end up in extra_vars, which is merged after
		// applyHaMode and would switch the next run to kube-vip.
		"kube_vip_address": "10.0.0.100",
	}
	in.MirrorEnabled = true
	in.Mirrors = map[string]string{"gcr_image_repo": "gcr.override"}

	cfg, err := buildInstallConfig(in)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}

	path := filepath.Join(t.TempDir(), "install.yaml")
	if _, err := writeInstallConfigFile(path, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := loadInstallConfig(path, in.App)
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}

	vars, err := buildExtraVars(loaded, in.App, map[string]any{})
	if err != nil {
		t.Fatalf("build extra vars: %v", err)
	}

	want := map[string]any{
		"kube_network_plugin":              "calico",
		"calico_vxlan_mode":                "CrossSubnet",
		"gcr_image_repo":                   "gcr.override",
		"ntp_enabled":                      false,
		"kube_proxy_mode":                  "ipvs",
		"loadbalancer_apiserver_localhost": true,
	}
	for key, expected := range want {
		if !reflect.DeepEqual(vars[key], expected) {
			t.Errorf("%s = %#v, want %#v", key, vars[key], expected)
		}
	}
	if _, ok := vars["kube_vip_address"]; ok {
		t.Error("the stale kube_vip_address leaked into extra_vars")
	}
	if _, ok := vars["kube_vip_enabled"]; ok {
		t.Error("the stale kube_vip_enabled leaked into extra_vars")
	}
}

func TestWriteInstallConfigFile(t *testing.T) {
	in := validExportInput(t)
	cfg, err := buildInstallConfig(in)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "install.yaml")

	abs, err := writeInstallConfigFile(path, cfg)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}
	if abs != path {
		t.Errorf("absolute path = %q, want %q", abs, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), installConfigHeader) {
		t.Errorf("config does not start with the warning header:\n%s", data)
	}
	if !strings.Contains(string(data), "hosts:\n  - name: node1") {
		t.Errorf("config does not use two-space indentation:\n%s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".install-config-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}

	if _, err := writeInstallConfigFile(path, cfg); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(second) {
		t.Errorf("writing the same state twice is not deterministic")
	}

	loaded, err := loadInstallConfig(path, in.App)
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if loaded.Network.CheckConflicts == nil || !*loaded.Network.CheckConflicts {
		t.Errorf("loaded check_conflicts = %v, want true", loaded.Network.CheckConflicts)
	}
	if len(loaded.Hosts.Detailed) != 3 {
		t.Errorf("loaded hosts = %d, want 3", len(loaded.Hosts.Detailed))
	}

	body := strings.TrimPrefix(string(second), installConfigHeader)
	again, err := marshalInstallConfig(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if body != string(again) {
		t.Errorf("generated config is not idempotent:\nfirst:\n%s\nsecond:\n%s", body, again)
	}
}

func TestWriteInstallConfigFileOverwritesAndTightensMode(t *testing.T) {
	in := validExportInput(t)
	cfg, err := buildInstallConfig(in)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}

	path := filepath.Join(t.TempDir(), "install.yaml")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := writeInstallConfigFile(path, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), installConfigHeader) {
		t.Fatalf("the old content was not replaced:\n%s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestWriteInstallConfigFileMissingDirectory(t *testing.T) {
	in := validExportInput(t)
	cfg, err := buildInstallConfig(in)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}

	path := filepath.Join(t.TempDir(), "missing", "install.yaml")
	if _, err := writeInstallConfigFile(path, cfg); err == nil {
		t.Fatal("writing into a missing directory succeeded, want an error")
	}
}
