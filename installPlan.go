package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

const (
	defaultServiceCIDR = "10.233.0.0/18"
	defaultPodCIDR     = "10.233.64.0/18"
)

// buildInventory turns the detailed hosts form into the inventory Kubespray
// reads. Bare IP/range entries do not come through here: they are handed to
// inventory.py, which also assigns their roles.
func buildInventory(cfg *installConfig) (Inventory, error) {
	var inv Inventory
	if len(cfg.Hosts.Detailed) == 0 {
		return inv, errors.New("detailed hosts are required to build the inventory")
	}

	inv.All.Vars.Ansible_user = cfg.SSH.User
	inv.All.Vars.Ansible_port = strconv.Itoa(cfg.SSH.Port)
	inv.All.Hosts = map[string]Host{}
	inv.All.Children.Kube_control_plane.Hosts = map[string]map[any]any{}
	inv.All.Children.Kube_node.Hosts = map[string]map[any]any{}
	inv.All.Children.Etcd.Hosts = map[string]map[any]any{}
	inv.All.Children.Calico_rr.Hosts = map[string]map[any]any{}
	inv.All.Children.K8s_cluster.Children.Kube_control_plane = map[string]any{}
	inv.All.Children.K8s_cluster.Children.Kube_node = map[string]any{}

	for _, host := range cfg.Hosts.Detailed {
		inv.All.Hosts[host.Name] = Host{
			Ansible_host: host.AnsibleHost,
			Ip:           host.IP,
			Access_ip:    host.AccessIP,
			Node_labels:  host.Labels,
			Node_taints:  host.Taints,
		}
		for _, role := range host.Roles {
			switch role {
			case roleControlPlane:
				inv.All.Children.Kube_control_plane.Hosts[host.Name] = map[any]any{}
			case roleNode:
				inv.All.Children.Kube_node.Hosts[host.Name] = map[any]any{}
			case roleEtcd:
				inv.All.Children.Etcd.Hosts[host.Name] = map[any]any{}
			case roleCalicoRR:
				inv.All.Children.Calico_rr.Hosts[host.Name] = map[any]any{}
			}
		}
	}

	return inv, nil
}

// buildExtraVars assembles the Ansible variables in the documented order:
// default_vars, then the feature defaults and overrides, HA, network, mirror,
// and finally the user's extra_vars escape hatch.
func buildExtraVars(cfg *installConfig, app AppConfig, clusterVars map[string]any) (map[string]any, error) {
	extra := make(map[string]any)
	for key, value := range app.Default_vars {
		extra[key] = deepCopyValue(value)
	}

	defaults, err := configurableVarTypes(app)
	if err != nil {
		return nil, fmt.Errorf("configurable vars: %w", err)
	}
	for key, value := range defaults {
		extra[key] = deepCopyValue(value)
	}
	for key, value := range cfg.Features {
		extra[key] = deepCopyValue(value)
	}

	applyHaMode(cfg.HA.Mode, cfg.HA.VIP, extra)

	serviceCIDR := cfg.Network.ServiceCIDR
	if serviceCIDR == "" {
		if value, ok := clusterVars["kube_service_addresses"].(string); ok && value != "" {
			serviceCIDR = value
		} else {
			serviceCIDR = defaultServiceCIDR
		}
	}
	podCIDR := cfg.Network.PodCIDR
	if podCIDR == "" {
		if value, ok := clusterVars["kube_pods_subnet"].(string); ok && value != "" {
			podCIDR = value
		} else {
			podCIDR = defaultPodCIDR
		}
	}
	if _, err := parseIPv4Prefix(serviceCIDR); err != nil {
		return nil, fmt.Errorf("service CIDR: %w", err)
	}
	if _, err := parseIPv4Prefix(podCIDR); err != nil {
		return nil, fmt.Errorf("pod CIDR: %w", err)
	}

	extra["kube_network_plugin"] = cfg.Network.Plugin
	extra["kube_service_addresses"] = serviceCIDR
	extra["kube_pods_subnet"] = podCIDR
	if cfg.Network.Plugin == "calico" {
		vxlanMode := cfg.Network.CalicoVxlanMode
		if vxlanMode == "" {
			vxlanMode = "Always"
		}
		extra["calico_vxlan_mode"] = vxlanMode
	} else {
		delete(extra, "calico_vxlan_mode")
	}

	if cfg.Mirror.Enabled {
		for _, item := range app.Default_mirrors {
			for key, value := range item {
				extra[key] = value
			}
		}
		for key, value := range cfg.Mirror.URLs {
			extra[key] = value
		}
	} else {
		for _, item := range app.Default_mirrors {
			for key := range item {
				delete(extra, key)
			}
		}
	}

	for key, value := range cfg.ExtraVars {
		extra[key] = deepCopyValue(value)
	}

	if err := validateExtraVarTypes(extra); err != nil {
		return nil, err
	}

	return extra, nil
}

// validateExtraVarTypes protects the prefilled pages from unchecked type
// assertions: extra_vars is merged last and may override the settings the
// Network and HA pages read.
func validateExtraVarTypes(extra map[string]any) error {
	for _, key := range []string{
		"kube_network_plugin",
		"kube_service_addresses",
		"kube_pods_subnet",
		"calico_vxlan_mode",
		"kube_vip_address",
		"apiserver_loadbalancer_domain_name",
	} {
		value, ok := extra[key]
		if !ok {
			continue
		}
		if _, ok := value.(string); !ok {
			return fmt.Errorf("extra_vars.%s: want a string, got %T", key, value)
		}
	}

	if value, ok := extra["loadbalancer_apiserver_localhost"]; ok {
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("extra_vars.loadbalancer_apiserver_localhost: want a bool, got %T", value)
		}
	}

	return nil
}

// validateResolvedNetwork checks the CIDRs as they will actually be used,
// including the defaults that were not present in the config file.
func validateResolvedNetwork(extra map[string]any, inv Inventory, vip string) error {
	serviceCIDR, _ := extra["kube_service_addresses"].(string)
	podCIDR, _ := extra["kube_pods_subnet"].(string)

	servicePrefix, err := parseIPv4Prefix(serviceCIDR)
	if err != nil {
		return fmt.Errorf("service_cidr: %w", err)
	}
	podPrefix, err := parseIPv4Prefix(podCIDR)
	if err != nil {
		return fmt.Errorf("pod_cidr: %w", err)
	}
	if servicePrefix.Overlaps(podPrefix) {
		return errors.New("network: service_cidr and pod_cidr overlap")
	}

	for name, host := range inv.All.Hosts {
		for _, value := range []string{host.Ansible_host, host.Ip, host.Access_ip} {
			addr, err := parseIPv4(value)
			if err != nil {
				continue
			}
			if servicePrefix.Contains(addr) || podPrefix.Contains(addr) {
				return fmt.Errorf("network: node %s address %s is inside the service/pod CIDR", name, addr)
			}
		}
	}

	if vip != "" {
		addr, err := parseIPv4(vip)
		if err != nil {
			return fmt.Errorf("ha.vip: %w", err)
		}
		if servicePrefix.Contains(addr) || podPrefix.Contains(addr) {
			return fmt.Errorf("network: VIP %s is inside the service/pod CIDR", addr)
		}
	}

	return nil
}

func deepCopyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copied := make(map[string]any, len(typed))
		for key, item := range typed {
			copied[key] = deepCopyValue(item)
		}
		return copied
	case []any:
		copied := make([]any, len(typed))
		for i, item := range typed {
			copied[i] = deepCopyValue(item)
		}
		return copied
	default:
		return value
	}
}

// renderMirrorFile reproduces the TUI's sed on offline.yml: lines that use
// {{ files_repo }} are no longer commented out.
func renderMirrorFile(source []byte) []byte {
	lines := strings.Split(string(source), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "# ") && strings.Contains(line, "{{ files_repo") {
			lines[i] = strings.TrimPrefix(line, "# ")
		}
	}

	return []byte(strings.Join(lines, "\n"))
}

// sampledPrefixIPs picks five usable IPv4 addresses from a CIDR without
// enumerating it. It mirrors the positions the Network page samples.
func sampledPrefixIPs(cidr string) ([]string, error) {
	prefix, err := parseIPv4Prefix(cidr)
	if err != nil {
		return nil, err
	}

	prefixStart, prefixEnd := prefixInterval(prefix)
	if prefixEnd-prefixStart < 2 {
		return nil, fmt.Errorf("%s has no usable addresses", cidr)
	}

	usableStart := uint64(prefixStart) + 1
	usableEnd := uint64(prefixEnd) - 1
	count := usableEnd - usableStart + 1

	positions := []uint64{0, count / 4, count / 2, count * 3 / 4, count - 1}
	seen := map[uint64]bool{}
	ips := make([]string, 0, len(positions))
	for _, position := range positions {
		if seen[position] {
			continue
		}
		seen[position] = true
		ips = append(ips, uint32ToAddr(uint32(usableStart+position)).String())
	}

	return ips, nil
}

func uint32ToAddr(value uint32) netip.Addr {
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], value)
	return netip.AddrFrom4(bytes)
}

func exitCodeForRun(err error) int {
	if err == nil {
		return 0
	}
	return 1
}
