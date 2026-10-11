package main

// Tests for the extra vars the HA Mode page writes.

import "testing"

func TestApplyHaModeKubeVipPointsEndpointAtVip(t *testing.T) {
	const vip = "10.184.101.69"
	extraVars := map[string]any{}

	applyHaMode("kube-vip", vip, extraVars)

	if extraVars["loadbalancer_apiserver_localhost"] != false {
		t.Errorf("loadbalancer_apiserver_localhost = %v, want false", extraVars["loadbalancer_apiserver_localhost"])
	}
	for _, key := range []string{
		"kube_vip_enabled",
		"kube_vip_controlplane_enabled",
		"kube_vip_arp_enabled",
		"kube_proxy_strict_arp",
		"kube_vip_lb_enable",
	} {
		if extraVars[key] != true {
			t.Errorf("%s = %v, want true", key, extraVars[key])
		}
	}
	if extraVars["kube_vip_address"] != vip {
		t.Errorf("kube_vip_address = %v, want %q", extraVars["kube_vip_address"], vip)
	}

	apiServer, ok := extraVars["loadbalancer_apiserver"].(map[string]string)
	if !ok {
		t.Fatalf("loadbalancer_apiserver = %v, want a map", extraVars["loadbalancer_apiserver"])
	}
	if apiServer["address"] != vip || apiServer["port"] != "6443" {
		t.Errorf("loadbalancer_apiserver = %v, want address %q and port 6443", apiServer, vip)
	}

	// Kubespray >= 2.29 no longer writes lb-apiserver.kubernetes.local into
	// /etc/hosts, so the apiserver endpoint has to be the VIP itself.
	if extraVars["apiserver_loadbalancer_domain_name"] != vip {
		t.Errorf("apiserver_loadbalancer_domain_name = %v, want %q",
			extraVars["apiserver_loadbalancer_domain_name"], vip)
	}
}

func TestApplyHaModeLocalhostClearsKubeVipSettings(t *testing.T) {
	extraVars := map[string]any{
		"loadbalancer_apiserver_localhost":   false,
		"kube_vip_enabled":                   true,
		"kube_vip_controlplane_enabled":      true,
		"kube_vip_arp_enabled":               true,
		"kube_proxy_strict_arp":              true,
		"kube_vip_lb_enable":                 true,
		"kube_vip_address":                   "10.184.101.69",
		"loadbalancer_apiserver":             map[string]string{"address": "10.184.101.69", "port": "6443"},
		"apiserver_loadbalancer_domain_name": "10.184.101.69",
		"kube_network_plugin":                "flannel",
	}

	applyHaMode("localhost loadbalancing", "", extraVars)

	if extraVars["loadbalancer_apiserver_localhost"] != true {
		t.Errorf("loadbalancer_apiserver_localhost = %v, want true", extraVars["loadbalancer_apiserver_localhost"])
	}
	for _, key := range []string{
		"kube_vip_enabled",
		"kube_vip_controlplane_enabled",
		"kube_vip_arp_enabled",
		"kube_proxy_strict_arp",
		"kube_vip_lb_enable",
		"kube_vip_address",
		"loadbalancer_apiserver",
		"apiserver_loadbalancer_domain_name",
	} {
		if value, ok := extraVars[key]; ok {
			t.Errorf("%s = %v, want it deleted", key, value)
		}
	}
	if extraVars["kube_network_plugin"] != "flannel" {
		t.Errorf("kube_network_plugin = %v, want it left alone", extraVars["kube_network_plugin"])
	}
}
