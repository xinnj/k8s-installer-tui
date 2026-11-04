package main

// Tests for the installation config file. They run without root, podman or a
// terminal.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testAppConfig() AppConfig {
	return AppConfig{
		Default_vars: map[string]any{
			"kube_network_plugin": "flannel",
		},
		Configurable_vars: []map[string]any{
			{"description": "NTP", "var": map[string]any{"ntp_enabled": true}},
			{"description": "Mirrors", "var": map[string]any{"containerd_registries_mirrors": []any{}}},
			{"description": "Count", "var": map[string]any{"worker_count": 3}},
			{"description": "Name", "var": map[string]any{"cluster_name": "default"}},
			{"description": "Map", "var": map[string]any{"extra_map": map[string]any{"k": "v"}}},
		},
		Default_mirrors: []map[string]string{
			{"gcr_image_repo": "gcr.example"},
			{"files_repo": "https://files.example"},
		},
	}
}

func validAutoConfig() map[string]any {
	return map[string]any{
		"version": 1,
		"ssh": map[string]any{
			"user":     "root",
			"port":     22,
			"password": "secret",
		},
		"hosts": []any{"10.0.0.11", "10.0.0.12", "10.0.0.13"},
	}
}

func validDetailedConfig() map[string]any {
	return map[string]any{
		"version": 1,
		"ssh": map[string]any{
			"user":     "root",
			"port":     22,
			"password": "secret",
		},
		"hosts": []any{
			map[string]any{"name": "node1", "ip": "10.0.0.11", "roles": []any{"control-plane", "etcd"}},
			map[string]any{"name": "node2", "ip": "10.0.0.12", "roles": []any{"control-plane", "etcd"}},
			map[string]any{"name": "node3", "ip": "10.0.0.13", "roles": []any{"node", "etcd"}},
		},
	}
}

func yamlBytes(t *testing.T, v any) []byte {
	t.Helper()
	data, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal test config: %v", err)
	}
	return data
}

func loadTestConfig(t *testing.T, v any) (*installConfig, error) {
	t.Helper()
	cfg, err := parseInstallConfig(yamlBytes(t, v))
	if err != nil {
		return nil, err
	}
	if err := cfg.applyDefaults(testAppConfig()); err != nil {
		return nil, err
	}
	return cfg, cfg.validate(testAppConfig())
}

func TestParseInstallConfigHostShapes(t *testing.T) {
	t.Run("bare entries", func(t *testing.T) {
		cfg, err := parseInstallConfig(yamlBytes(t, validAutoConfig()))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(cfg.Hosts.Auto) != 3 {
			t.Fatalf("auto entries = %d, want 3", len(cfg.Hosts.Auto))
		}
		if len(cfg.Hosts.Detailed) != 0 {
			t.Fatalf("detailed entries = %d, want 0", len(cfg.Hosts.Detailed))
		}
	})

	t.Run("detailed entries", func(t *testing.T) {
		cfg, err := parseInstallConfig(yamlBytes(t, validDetailedConfig()))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(cfg.Hosts.Detailed) != 3 {
			t.Fatalf("detailed entries = %d, want 3", len(cfg.Hosts.Detailed))
		}
		if got := cfg.Hosts.Detailed[0].Roles; strings.Join(got, ",") != "control-plane,etcd" {
			t.Fatalf("roles = %v, want [control-plane etcd]", got)
		}
	})
}

func TestParseInstallConfigRejectsMixedHostShapes(t *testing.T) {
	m := validAutoConfig()
	m["hosts"] = []any{
		"10.0.0.11",
		map[string]any{"ip": "10.0.0.12", "roles": []any{"node"}},
	}

	if _, err := parseInstallConfig(yamlBytes(t, m)); err == nil {
		t.Fatal("mixed host shapes were accepted, want an error")
	}
}

func TestParseInstallConfigRejectsUnknownFields(t *testing.T) {
	t.Run("top level", func(t *testing.T) {
		m := validAutoConfig()
		m["mistyped"] = true

		if _, err := parseInstallConfig(yamlBytes(t, m)); err == nil {
			t.Fatal("unknown top-level field was accepted, want an error")
		}
	})

	t.Run("detailed host", func(t *testing.T) {
		m := validDetailedConfig()
		host := m["hosts"].([]any)[0].(map[string]any)
		host["rolez"] = []any{"etcd"}

		if _, err := parseInstallConfig(yamlBytes(t, m)); err == nil {
			t.Fatal("unknown detailed host field was accepted, want an error")
		}
	})
}

func TestInstallConfigDefaults(t *testing.T) {
	old := homePath
	homePath = t.TempDir()
	t.Cleanup(func() { homePath = old })

	m := validAutoConfig()
	delete(m, "version")

	cfg, err := loadTestConfig(t, m)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("version = %d, want 1", cfg.Version)
	}
	if want := filepath.Join(homePath, "idocluster"); cfg.ProjectPath != want {
		t.Errorf("project path = %q, want %q", cfg.ProjectPath, want)
	}
	if cfg.SSH.Port != 22 {
		t.Errorf("ssh port = %d, want 22", cfg.SSH.Port)
	}
	if cfg.SSH.User == "" {
		t.Error("ssh user is empty, want the current user")
	}
	if cfg.HostnamePrefix != "node" {
		t.Errorf("hostname prefix = %q, want node", cfg.HostnamePrefix)
	}
	if cfg.HA.Mode != "localhost loadbalancing" {
		t.Errorf("ha mode = %q, want localhost loadbalancing", cfg.HA.Mode)
	}
	if cfg.Network.Plugin != "flannel" {
		t.Errorf("network plugin = %q, want flannel", cfg.Network.Plugin)
	}
	if cfg.Network.CheckConflicts == nil || !*cfg.Network.CheckConflicts {
		t.Error("check_conflicts should default to true")
	}
	if cfg.Mirror.Enabled {
		t.Error("mirror should default to disabled")
	}
}

func TestInstallConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "unsupported version", mutate: func(m map[string]any) { m["version"] = 2 }, want: "version"},
		{name: "no hosts", mutate: func(m map[string]any) { m["hosts"] = []any{} }, want: "hosts"},
		{name: "invalid bare IP", mutate: func(m map[string]any) { m["hosts"] = []any{"10.0.0.999"} }, want: "invalid"},
		{name: "reversed range", mutate: func(m map[string]any) { m["hosts"] = []any{"10.0.0.20-10.0.0.10"} }, want: "range"},
		{name: "overlapping bare entries", mutate: func(m map[string]any) { m["hosts"] = []any{"10.0.0.11-10.0.0.15", "10.0.0.14"} }, want: "overlap"},
		{name: "no credential", mutate: func(m map[string]any) {
			m["ssh"] = map[string]any{"user": "root", "port": 22}
		}, want: "password"},
		{name: "sudo password without key", mutate: func(m map[string]any) {
			ssh := m["ssh"].(map[string]any)
			ssh["sudo_password"] = "other"
		}, want: "sudo_password"},
		{name: "bad port", mutate: func(m map[string]any) {
			m["ssh"].(map[string]any)["port"] = 70000
		}, want: "port"},
		{name: "bad user", mutate: func(m map[string]any) {
			m["ssh"].(map[string]any)["user"] = "bad user"
		}, want: "ssh.user"},
		{name: "kube-vip without VIP", mutate: func(m map[string]any) {
			m["ha"] = map[string]any{"mode": "kube-vip"}
		}, want: "vip"},
		{name: "invalid VIP", mutate: func(m map[string]any) {
			m["ha"] = map[string]any{"mode": "kube-vip", "vip": "not-an-ip"}
		}, want: "vip"},
		{name: "bad service CIDR", mutate: func(m map[string]any) {
			m["network"] = map[string]any{"service_cidr": "nope"}
		}, want: "service_cidr"},
		{name: "overlapping CIDRs", mutate: func(m map[string]any) {
			m["network"] = map[string]any{"service_cidr": "10.233.0.0/16", "pod_cidr": "10.233.64.0/18"}
		}, want: "overlap"},
		{name: "node inside pod CIDR", mutate: func(m map[string]any) {
			m["network"] = map[string]any{"pod_cidr": "10.0.0.0/16"}
		}, want: "inside"},
		{name: "IPv6 CIDR", mutate: func(m map[string]any) {
			m["network"] = map[string]any{"service_cidr": "fd00::/64"}
		}, want: "IPv4"},
		{name: "unknown feature", mutate: func(m map[string]any) {
			m["features"] = map[string]any{"nope": true}
		}, want: "features"},
		{name: "wrong feature type", mutate: func(m map[string]any) {
			m["features"] = map[string]any{"ntp_enabled": "yes"}
		}, want: "type"},
		{name: "mirror URLs without enabled", mutate: func(m map[string]any) {
			m["mirror"] = map[string]any{"urls": map[string]any{"gcr_image_repo": "x"}}
		}, want: "enabled"},
		{name: "unknown mirror key", mutate: func(m map[string]any) {
			m["mirror"] = map[string]any{"enabled": true, "urls": map[string]any{"nope": "x"}}
		}, want: "mirror"},
		{name: "bad files_repo", mutate: func(m map[string]any) {
			m["mirror"] = map[string]any{"enabled": true, "urls": map[string]any{"files_repo": "ftp://x"}}
		}, want: "files_repo"},
		{name: "calico vxlan with flannel", mutate: func(m map[string]any) {
			m["network"] = map[string]any{"plugin": "flannel", "calico_vxlan_mode": "Always"}
		}, want: "calico"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validAutoConfig()
			tt.mutate(m)

			_, err := loadTestConfig(t, m)
			if err == nil {
				t.Fatalf("config was accepted, want an error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestInstallConfigDetailedHostValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "missing roles", mutate: func(m map[string]any) {
			delete(m["hosts"].([]any)[0].(map[string]any), "roles")
		}, want: "roles"},
		{name: "unknown role", mutate: func(m map[string]any) {
			m["hosts"].([]any)[0].(map[string]any)["roles"] = []any{"control-plane", "etcd", "database"}
		}, want: "role"},
		{name: "no control plane", mutate: func(m map[string]any) {
			m["hosts"].([]any)[0].(map[string]any)["roles"] = []any{"etcd"}
			m["hosts"].([]any)[1].(map[string]any)["roles"] = []any{"etcd"}
		}, want: "control-plane"},
		{name: "no etcd", mutate: func(m map[string]any) {
			for _, h := range m["hosts"].([]any) {
				host := h.(map[string]any)
				roles := []any{}
				for _, r := range host["roles"].([]any) {
					if r != "etcd" {
						roles = append(roles, r)
					}
				}
				host["roles"] = roles
			}
		}, want: "etcd"},
		{name: "even etcd", mutate: func(m map[string]any) {
			m["hosts"] = append(m["hosts"].([]any), map[string]any{
				"name": "node4", "ip": "10.0.0.14", "roles": []any{"node", "etcd"},
			})
		}, want: "odd"},
		{name: "calico_rr with node", mutate: func(m map[string]any) {
			m["hosts"].([]any)[2].(map[string]any)["roles"] = []any{"node", "calico_rr", "etcd"}
		}, want: "calico_rr"},
		{name: "duplicate name", mutate: func(m map[string]any) {
			m["hosts"].([]any)[1].(map[string]any)["name"] = "node1"
		}, want: "unique"},
		{name: "duplicate IP", mutate: func(m map[string]any) {
			m["hosts"].([]any)[1].(map[string]any)["ip"] = "10.0.0.11"
		}, want: "unique"},
		{name: "invalid name", mutate: func(m map[string]any) {
			m["hosts"].([]any)[0].(map[string]any)["name"] = "Node_1"
		}, want: "name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validDetailedConfig()
			tt.mutate(m)

			_, err := loadTestConfig(t, m)
			if err == nil {
				t.Fatalf("config was accepted, want an error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestInstallConfigAcceptsAKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("key"), 0600); err != nil {
		t.Fatal(err)
	}

	m := validAutoConfig()
	m["ssh"] = map[string]any{
		"user":          "ops",
		"port":          2222,
		"key_file":      keyPath,
		"sudo_password": "sudo",
	}

	cfg, err := loadTestConfig(t, m)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.SSH.KeyFile != keyPath {
		t.Errorf("key file = %q, want %q", cfg.SSH.KeyFile, keyPath)
	}
}

func TestAutoHostEntriesAreNormalised(t *testing.T) {
	m := validAutoConfig()
	m["hosts"] = []any{
		"10.0.0.15, 192.168.1.15",
		"10.0.0.12 - 10.0.0.14",
		" 10.0.0.11 ",
	}

	cfg, err := loadTestConfig(t, m)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []string{"10.0.0.15,192.168.1.15", "10.0.0.12-10.0.0.14", "10.0.0.11"}
	if strings.Join(cfg.Hosts.Auto, "|") != strings.Join(want, "|") {
		t.Fatalf("auto hosts = %v, want %v", cfg.Hosts.Auto, want)
	}
}

func TestDetailedHostAddressesAreNormalised(t *testing.T) {
	m := validDetailedConfig()
	host := m["hosts"].([]any)[0].(map[string]any)
	host["ip"] = " 10.0.0.11 "
	host["access_ip"] = " 10.0.0.11 "
	host["ansible_host"] = " 10.0.0.11 "

	cfg, err := loadTestConfig(t, m)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := cfg.Hosts.Detailed[0]
	if got.IP != "10.0.0.11" || got.AccessIP != "10.0.0.11" || got.AnsibleHost != "10.0.0.11" {
		t.Fatalf("addresses = %q / %q / %q, want them normalised", got.IP, got.AccessIP, got.AnsibleHost)
	}
}

func TestInstallConfigRejectsDuplicateAccessIPs(t *testing.T) {
	m := validAutoConfig()
	m["hosts"] = []any{"10.0.0.11,192.168.1.11", "10.0.0.12,192.168.1.11"}

	if _, err := loadTestConfig(t, m); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("duplicate access IP = %v, want an overlap error", err)
	}
}

func TestInstallExampleConfigIsValid(t *testing.T) {
	old := homePath
	homePath = t.TempDir()
	t.Cleanup(func() { homePath = old })

	if _, err := loadInstallConfig("install.example.yaml", testAppConfig()); err != nil {
		t.Fatalf("install.example.yaml is not valid: %v", err)
	}
}

func TestLoadInstallConfigRejectsMultipleDocuments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := "version: 1\nhosts:\n  - 10.0.0.11\n---\nversion: 1\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadInstallConfig(path, testAppConfig()); err == nil {
		t.Fatal("multiple YAML documents were accepted, want an error")
	}
}

func TestConfigPermissionWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if configPermissionWarning(path) == "" {
		t.Fatal("0644 config produced no warning")
	}

	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if warning := configPermissionWarning(path); warning != "" {
		t.Fatalf("0600 config produced a warning: %s", warning)
	}
}
