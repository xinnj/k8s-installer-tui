package main

// Tests for the pure installation planners. They run without root, podman or a
// terminal.

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func mustInstallConfig(t *testing.T, v any) *installConfig {
	t.Helper()
	cfg, err := loadTestConfig(t, v)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func TestBuildInventoryFromDetailedHosts(t *testing.T) {
	m := validDetailedConfig()
	hosts := m["hosts"].([]any)
	node1 := hosts[0].(map[string]any)
	node1["labels"] = map[string]any{"zone": "zone1"}
	node1["taints"] = []any{"logging:NoSchedule"}

	cfg := mustInstallConfig(t, m)
	inv, err := buildInventory(cfg)
	if err != nil {
		t.Fatalf("build inventory: %v", err)
	}

	if inv.All.Vars.Ansible_user != "root" {
		t.Errorf("ansible_user = %q, want root", inv.All.Vars.Ansible_user)
	}
	if inv.All.Vars.Ansible_port != "22" {
		t.Errorf("ansible_port = %q, want 22", inv.All.Vars.Ansible_port)
	}

	host := inv.All.Hosts["node1"]
	if host.Ansible_host != "10.0.0.11" || host.Ip != "10.0.0.11" || host.Access_ip != "10.0.0.11" {
		t.Errorf("node1 = %+v, want all addresses 10.0.0.11", host)
	}
	if host.Node_labels["zone"] != "zone1" {
		t.Errorf("node1 labels = %v, want zone=zone1", host.Node_labels)
	}
	if len(host.Node_taints) != 1 || host.Node_taints[0] != "logging:NoSchedule" {
		t.Errorf("node1 taints = %v, want logging:NoSchedule", host.Node_taints)
	}

	if _, ok := inv.All.Children.Kube_control_plane.Hosts["node1"]; !ok {
		t.Error("node1 is not in kube_control_plane")
	}
	if _, ok := inv.All.Children.Kube_control_plane.Hosts["node3"]; ok {
		t.Error("node3 is in kube_control_plane, but its roles are node+etcd")
	}
	if _, ok := inv.All.Children.Kube_node.Hosts["node3"]; !ok {
		t.Error("node3 is not in kube_node")
	}
	if _, ok := inv.All.Children.Kube_node.Hosts["node1"]; ok {
		t.Error("node1 is in kube_node, but detailed roles are exact")
	}
	if got := len(inv.All.Children.Etcd.Hosts); got != 3 {
		t.Errorf("etcd hosts = %d, want 3", got)
	}
	if inv.All.Children.K8s_cluster.Children.Kube_control_plane == nil {
		t.Error("k8s_cluster.children.kube_control_plane is nil")
	}
	if inv.All.Children.K8s_cluster.Children.Kube_node == nil {
		t.Error("k8s_cluster.children.kube_node is nil")
	}

	data, err := yaml.Marshal(&inv)
	if err != nil {
		t.Fatalf("marshal inventory: %v", err)
	}
	for _, want := range []string{"k8s_cluster:", "kube_control_plane:", "node_labels:"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("inventory YAML does not contain %q:\n%s", want, data)
		}
	}
}

func TestBuildInventoryRequiresDetailedHosts(t *testing.T) {
	if _, err := buildInventory(mustInstallConfig(t, validAutoConfig())); err == nil {
		t.Fatal("buildInventory accepted a bare-entry config, want an error")
	}
}

func TestBuildExtraVarsPrecedence(t *testing.T) {
	app := testAppConfig()
	app.Default_vars["nested"] = map[string]any{"key": "value"}

	m := validAutoConfig()
	m["features"] = map[string]any{"ntp_enabled": false}
	m["network"] = map[string]any{"plugin": "calico"}
	m["mirror"] = map[string]any{
		"enabled": true,
		"urls":    map[string]any{"gcr_image_repo": "gcr.override"},
	}
	m["extra_vars"] = map[string]any{"kube_network_plugin": "cilium", "custom": "value"}

	vars, err := buildExtraVars(mustInstallConfig(t, m), app, map[string]any{
		"kube_service_addresses": "10.0.0.0/16",
		"kube_pods_subnet":       "10.1.0.0/16",
	})
	if err != nil {
		t.Fatalf("build extra vars: %v", err)
	}

	if got := vars["ntp_enabled"]; got != false {
		t.Errorf("ntp_enabled = %v, want false", got)
	}
	if _, ok := vars["containerd_registries_mirrors"]; !ok {
		t.Error("configurable_vars defaults were not seeded")
	}
	if vars["worker_count"] != 3 || vars["cluster_name"] != "default" {
		t.Errorf("configurable defaults = worker_count %v, cluster_name %v", vars["worker_count"], vars["cluster_name"])
	}
	if got := vars["kube_network_plugin"]; got != "cilium" {
		t.Errorf("kube_network_plugin = %v, want cilium (extra_vars wins)", got)
	}
	if got := vars["calico_vxlan_mode"]; got != "Always" {
		t.Errorf("calico_vxlan_mode = %v, want Always", got)
	}
	if got := vars["kube_service_addresses"]; got != "10.0.0.0/16" {
		t.Errorf("kube_service_addresses = %v, want 10.0.0.0/16", got)
	}
	if got := vars["kube_pods_subnet"]; got != "10.1.0.0/16" {
		t.Errorf("kube_pods_subnet = %v, want 10.1.0.0/16", got)
	}
	if got := vars["gcr_image_repo"]; got != "gcr.override" {
		t.Errorf("gcr_image_repo = %v, want the config override", got)
	}
	if got := vars["files_repo"]; got != "https://files.example" {
		t.Errorf("files_repo = %v, want the default mirror", got)
	}
	if got := vars["custom"]; got != "value" {
		t.Errorf("custom = %v, want value", got)
	}

	nested := vars["nested"].(map[string]any)
	nested["key"] = "changed"
	if got := app.Default_vars["nested"].(map[string]any)["key"]; got != "value" {
		t.Errorf("app Default_vars was mutated through the result: key = %v", got)
	}
}

func TestBuildExtraVarsHA(t *testing.T) {
	t.Run("localhost", func(t *testing.T) {
		vars, err := buildExtraVars(mustInstallConfig(t, validAutoConfig()), testAppConfig(), nil)
		if err != nil {
			t.Fatalf("build extra vars: %v", err)
		}
		if got := vars["loadbalancer_apiserver_localhost"]; got != true {
			t.Errorf("loadbalancer_apiserver_localhost = %v, want true", got)
		}
		if _, ok := vars["kube_vip_address"]; ok {
			t.Error("kube_vip_address is set in localhost mode")
		}
	})

	t.Run("kube-vip", func(t *testing.T) {
		m := validAutoConfig()
		m["ha"] = map[string]any{"mode": "kube-vip", "vip": "10.0.0.100"}

		vars, err := buildExtraVars(mustInstallConfig(t, m), testAppConfig(), nil)
		if err != nil {
			t.Fatalf("build extra vars: %v", err)
		}
		if got := vars["kube_vip_address"]; got != "10.0.0.100" {
			t.Errorf("kube_vip_address = %v, want 10.0.0.100", got)
		}
		if got := vars["apiserver_loadbalancer_domain_name"]; got != "10.0.0.100" {
			t.Errorf("apiserver_loadbalancer_domain_name = %v, want the VIP", got)
		}
		lb, ok := vars["loadbalancer_apiserver"].(map[string]string)
		if !ok || lb["address"] != "10.0.0.100" || lb["port"] != "6443" {
			t.Errorf("loadbalancer_apiserver = %#v, want address=10.0.0.100 port=6443", vars["loadbalancer_apiserver"])
		}
	})
}

func TestBuildExtraVarsNetworkDefaults(t *testing.T) {
	t.Run("project defaults", func(t *testing.T) {
		vars, err := buildExtraVars(mustInstallConfig(t, validAutoConfig()), testAppConfig(), map[string]any{
			"kube_service_addresses": "192.168.0.0/16",
			"kube_pods_subnet":       "192.169.0.0/16",
		})
		if err != nil {
			t.Fatalf("build extra vars: %v", err)
		}
		if vars["kube_service_addresses"] != "192.168.0.0/16" || vars["kube_pods_subnet"] != "192.169.0.0/16" {
			t.Errorf("CIDRs = %v / %v, want the project defaults", vars["kube_service_addresses"], vars["kube_pods_subnet"])
		}
	})

	t.Run("Kubespray defaults", func(t *testing.T) {
		vars, err := buildExtraVars(mustInstallConfig(t, validAutoConfig()), testAppConfig(), nil)
		if err != nil {
			t.Fatalf("build extra vars: %v", err)
		}
		if vars["kube_service_addresses"] != "10.233.0.0/18" || vars["kube_pods_subnet"] != "10.233.64.0/18" {
			t.Errorf("CIDRs = %v / %v, want the Kubespray defaults", vars["kube_service_addresses"], vars["kube_pods_subnet"])
		}
	})
}

func TestBuildExtraVarsMirror(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		m := validAutoConfig()
		m["mirror"] = map[string]any{"enabled": true}

		vars, err := buildExtraVars(mustInstallConfig(t, m), testAppConfig(), nil)
		if err != nil {
			t.Fatalf("build extra vars: %v", err)
		}
		if vars["gcr_image_repo"] != "gcr.example" || vars["files_repo"] != "https://files.example" {
			t.Errorf("mirror keys = %v / %v, want the defaults", vars["gcr_image_repo"], vars["files_repo"])
		}
	})

	t.Run("disabled", func(t *testing.T) {
		vars, err := buildExtraVars(mustInstallConfig(t, validAutoConfig()), testAppConfig(), nil)
		if err != nil {
			t.Fatalf("build extra vars: %v", err)
		}
		if _, ok := vars["gcr_image_repo"]; ok {
			t.Error("gcr_image_repo is set while the mirror is disabled")
		}
		if _, ok := vars["files_repo"]; ok {
			t.Error("files_repo is set while the mirror is disabled")
		}
	})
}

func TestValidateResolvedNetwork(t *testing.T) {
	extra := map[string]any{
		"kube_service_addresses": "10.233.0.0/18",
		"kube_pods_subnet":       "10.233.64.0/18",
	}
	valid := Inventory{}
	valid.All.Hosts = map[string]Host{
		"node1": {Ip: "10.0.0.11", Ansible_host: "10.0.0.11", Access_ip: "10.0.0.11"},
	}
	if err := validateResolvedNetwork(extra, valid, ""); err != nil {
		t.Fatalf("valid network rejected: %v", err)
	}

	inside := Inventory{}
	inside.All.Hosts = map[string]Host{
		"node1": {Ip: "10.233.64.5", Ansible_host: "10.233.64.5", Access_ip: "10.233.64.5"},
	}
	if err := validateResolvedNetwork(extra, inside, ""); err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("node inside the pod CIDR = %v, want an inside error", err)
	}

	if err := validateResolvedNetwork(extra, valid, "10.233.0.5"); err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("VIP inside the service CIDR = %v, want an inside error", err)
	}
}

func TestBuildExtraVarsRejectsWrongTypesForPageSettings(t *testing.T) {
	m := validAutoConfig()
	m["extra_vars"] = map[string]any{"kube_service_addresses": 123}

	if _, err := buildExtraVars(mustInstallConfig(t, m), testAppConfig(), nil); err == nil {
		t.Fatal("a numeric kube_service_addresses was accepted, want a type error")
	}
}

func TestRenderMirrorFile(t *testing.T) {
	source := `# files_repo: "http://myprivatehttpd"
# kubeadm_download_url: "{{ files_repo }}/kubeadm"
# kubectl_download_url: "{{ files_repo }}/kubectl"
foo: bar
`
	want := `# files_repo: "http://myprivatehttpd"
kubeadm_download_url: "{{ files_repo }}/kubeadm"
kubectl_download_url: "{{ files_repo }}/kubectl"
foo: bar
`

	if got := string(renderMirrorFile([]byte(source))); got != want {
		t.Fatalf("renderMirrorFile() =\n%s\nwant:\n%s", got, want)
	}
}

func TestSampledPrefixIPs(t *testing.T) {
	ips, err := sampledPrefixIPs("10.0.0.0/24")
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	want := []string{"10.0.0.1", "10.0.0.64", "10.0.0.128", "10.0.0.191", "10.0.0.254"}
	if strings.Join(ips, ",") != strings.Join(want, ",") {
		t.Fatalf("sampled = %v, want %v", ips, want)
	}

	if _, err := sampledPrefixIPs("fd00::/64"); err == nil {
		t.Fatal("IPv6 CIDR was accepted, want an error")
	}
	if _, err := sampledPrefixIPs("10.0.0.0/31"); err == nil {
		t.Fatal("/31 was accepted, want an error")
	}
}

func TestExitCodeForRun(t *testing.T) {
	if got := exitCodeForRun(nil); got != 0 {
		t.Errorf("exitCodeForRun(nil) = %d, want 0", got)
	}
	if got := exitCodeForRun(errors.New("failed")); got != 1 {
		t.Errorf("exitCodeForRun(error) = %d, want 1", got)
	}
}
