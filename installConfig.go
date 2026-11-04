package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const installConfigMaxBytes = 1 << 20

// installConfig is the installation file passed with --config. It is separate
// from the UI's own config.yaml (configurable_vars, default_vars, mirrors).
type installConfig struct {
	Version        int            `yaml:"version"`
	ProjectPath    string         `yaml:"project_path"`
	HostnamePrefix string         `yaml:"hostname_prefix"`
	SSH            installSSH     `yaml:"ssh"`
	Hosts          installHosts   `yaml:"hosts"`
	HA             installHA      `yaml:"ha"`
	Network        installNetwork `yaml:"network"`
	Mirror         installMirror  `yaml:"mirror"`
	Features       map[string]any `yaml:"features,omitempty"`
	ExtraVars      map[string]any `yaml:"extra_vars,omitempty"`
}

type installSSH struct {
	User         string `yaml:"user"`
	Port         int    `yaml:"port"`
	Password     string `yaml:"password,omitempty"`
	KeyFile      string `yaml:"key_file,omitempty"`
	SudoPassword string `yaml:"sudo_password,omitempty"`
}

// installHosts holds either the bare IP/range/access-pair form or the detailed
// host objects, never both. The YAML shape decides which.
type installHosts struct {
	Auto     []string
	Detailed []installHost
}

type installHost struct {
	Name        string            `yaml:"name"`
	IP          string            `yaml:"ip"`
	AccessIP    string            `yaml:"access_ip,omitempty"`
	AnsibleHost string            `yaml:"ansible_host,omitempty"`
	Roles       []string          `yaml:"roles"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Taints      []string          `yaml:"taints,omitempty"`
}

type installHA struct {
	Mode string `yaml:"mode"`
	VIP  string `yaml:"vip,omitempty"`
}

type installNetwork struct {
	Plugin          string `yaml:"plugin"`
	CalicoVxlanMode string `yaml:"calico_vxlan_mode,omitempty"`
	ServiceCIDR     string `yaml:"service_cidr,omitempty"`
	PodCIDR         string `yaml:"pod_cidr,omitempty"`
	CheckConflicts  *bool  `yaml:"check_conflicts"`
}

type installMirror struct {
	Enabled bool              `yaml:"enabled"`
	URLs    map[string]string `yaml:"urls,omitempty"`
}

const (
	roleControlPlane = "kube_control_plane"
	roleNode         = "kube_node"
	roleEtcd         = "etcd"
	roleCalicoRR     = "calico_rr"
)

var (
	hostnamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	sshUserPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	installHostKeys = map[string]bool{
		"name": true, "ip": true, "access_ip": true, "ansible_host": true,
		"roles": true, "labels": true, "taints": true,
	}
)

func (h *installHosts) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return errors.New("hosts must be a list")
	}
	if len(node.Content) == 0 {
		return nil
	}

	kind := node.Content[0].Kind
	for _, item := range node.Content {
		if item.Kind != kind {
			return errors.New("hosts must be either a list of IP/range strings or a list of host objects, not both")
		}
	}

	switch kind {
	case yaml.ScalarNode:
		return node.Decode(&h.Auto)
	case yaml.MappingNode:
		for hostIndex, item := range node.Content {
			for i := 0; i+1 < len(item.Content); i += 2 {
				key := item.Content[i].Value
				if !installHostKeys[key] {
					return fmt.Errorf("hosts[%d]: unknown field %q", hostIndex, key)
				}
			}
		}
		return node.Decode(&h.Detailed)
	default:
		return errors.New("hosts entries must be IP/range strings or host objects")
	}
}

// MarshalYAML writes the same two shapes UnmarshalYAML accepts. Without it,
// yaml.Marshal would emit the struct's auto/detailed fields as a mapping, which
// the loader then rejects.
func (h installHosts) MarshalYAML() (any, error) {
	if len(h.Auto) > 0 {
		return h.Auto, nil
	}

	return h.Detailed, nil
}

func parseInstallConfig(data []byte) (*installConfig, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var cfg installConfig
	if err := decoder.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("config file is empty")
		}
		return nil, fmt.Errorf("parse config: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("config must contain exactly one YAML document")
		}
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &cfg, nil
}

func loadInstallConfig(path string, app AppConfig) (*installConfig, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if info.Size() > installConfigMaxBytes {
		return nil, fmt.Errorf("config: file is larger than %d bytes", installConfigMaxBytes)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	cfg, err := parseInstallConfig(data)
	if err != nil {
		return nil, err
	}
	if err := cfg.applyDefaults(app); err != nil {
		return nil, err
	}
	if err := cfg.validate(app); err != nil {
		return nil, err
	}

	return cfg, nil
}

func configPermissionWarning(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if info.Mode().Perm()&0o077 == 0 {
		return ""
	}

	return fmt.Sprintf("config file %s is readable by other users; chmod 600 it because it contains plaintext credentials", path)
}

func (c *installConfig) applyDefaults(app AppConfig) error {
	if c.Version == 0 {
		c.Version = 1
	}

	if c.ProjectPath == "" {
		c.ProjectPath = filepath.Join(homePath, "idocluster")
	}
	projectPath, err := expandHomePath(c.ProjectPath)
	if err != nil {
		return fmt.Errorf("project_path: %w", err)
	}
	c.ProjectPath = projectPath

	if c.HostnamePrefix == "" {
		c.HostnamePrefix = "node"
	}

	if c.SSH.User == "" {
		if current, err := user.Current(); err == nil {
			c.SSH.User = current.Username
		}
	}
	if c.SSH.Port == 0 {
		c.SSH.Port = 22
	}
	if c.SSH.KeyFile != "" {
		keyFile, err := expandHomePath(c.SSH.KeyFile)
		if err != nil {
			return fmt.Errorf("ssh.key_file: %w", err)
		}
		c.SSH.KeyFile = keyFile
	}

	if c.HA.Mode == "" {
		c.HA.Mode = "localhost loadbalancing"
	}
	c.HA.Mode = normalizeHAMode(c.HA.Mode)

	if c.Network.Plugin == "" {
		c.Network.Plugin = "flannel"
		if plugin, ok := app.Default_vars["kube_network_plugin"].(string); ok && plugin != "" {
			c.Network.Plugin = plugin
		}
	}
	c.Network.Plugin = strings.ToLower(strings.TrimSpace(c.Network.Plugin))

	if c.Network.CheckConflicts == nil {
		check := true
		c.Network.CheckConflicts = &check
	}

	if c.Mirror.URLs == nil {
		c.Mirror.URLs = map[string]string{}
	}
	if c.Features == nil {
		c.Features = map[string]any{}
	}
	if c.ExtraVars == nil {
		c.ExtraVars = map[string]any{}
	}

	// Normalise the bare entries once. They are joined with spaces and handed
	// to inventory.py, which splits on whitespace: a raw "ip, access_ip" would
	// become two different hosts there and diverge from what was validated.
	for i, raw := range c.Hosts.Auto {
		if entry, err := parseAutoHostEntry(raw); err == nil {
			c.Hosts.Auto[i] = entry.Raw
		}
	}

	for i := range c.Hosts.Detailed {
		host := &c.Hosts.Detailed[i]
		if host.Name == "" {
			host.Name = fmt.Sprintf("%s%d", c.HostnamePrefix, i+1)
		}
		if host.AccessIP == "" {
			host.AccessIP = host.IP
		}
		if host.AnsibleHost == "" {
			host.AnsibleHost = host.AccessIP
		}
		if addr, err := parseIPv4(host.IP); err == nil {
			host.IP = addr.String()
		}
		if addr, err := parseIPv4(host.AccessIP); err == nil {
			host.AccessIP = addr.String()
		}
		if addr, err := parseIPv4(host.AnsibleHost); err == nil {
			host.AnsibleHost = addr.String()
		}
		host.Roles = normalizeRoles(host.Roles)
	}

	return nil
}

func (c *installConfig) validate(app AppConfig) error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if c.Version != 1 {
		add("version: unsupported version %d", c.Version)
	}

	if filepath.Clean(c.ProjectPath) == "/" {
		add("project_path: must not be the root directory")
	}
	if filepath.Clean(c.ProjectPath) == filepath.Clean(homePath) {
		add("project_path: must not be the home directory")
	}
	if strings.IndexFunc(c.ProjectPath, isControlRune) >= 0 {
		add("project_path: contains control characters")
	}

	if !sshUserPattern.MatchString(c.SSH.User) {
		add("ssh.user: %q is not a valid user name", c.SSH.User)
	}
	if c.SSH.Port < 1 || c.SSH.Port > 65535 {
		add("ssh.port: %d is out of range", c.SSH.Port)
	}
	if c.SSH.KeyFile == "" {
		if c.SSH.Password == "" {
			add("ssh: one of ssh.password or ssh.key_file is required")
		}
		if c.SSH.SudoPassword != "" {
			add("ssh.sudo_password: only valid with ssh.key_file")
		}
	} else {
		info, err := os.Stat(c.SSH.KeyFile)
		if err != nil {
			add("ssh.key_file: %v", err)
		} else if !info.Mode().IsRegular() {
			add("ssh.key_file: %q is not a regular file", c.SSH.KeyFile)
		} else if file, err := os.Open(c.SSH.KeyFile); err != nil {
			add("ssh.key_file: %v", err)
		} else {
			file.Close()
		}
	}

	if !hostnamePattern.MatchString(c.HostnamePrefix) || len(c.HostnamePrefix) > 63 {
		add("hostname_prefix: %q is not a valid DNS label", c.HostnamePrefix)
	}

	hostCount := len(c.Hosts.Auto) + len(c.Hosts.Detailed)
	if hostCount == 0 {
		add("hosts: at least one host is required")
	}
	if len(c.Hosts.Auto) > 0 && len(c.Hosts.Detailed) > 0 {
		add("hosts: use either bare entries or detailed host objects, not both")
	}

	var autoEntries []autoHostEntry
	if len(c.Hosts.Auto) > 0 {
		entries, err := parseAutoHostEntries(c.Hosts.Auto)
		if err != nil {
			add("hosts: %v", err)
		} else {
			autoEntries = entries
			if err := checkAutoHostOverlaps(entries); err != nil {
				add("hosts: %v", err)
			}
		}
	}

	var detailedAddrs []netip.Addr
	if len(c.Hosts.Detailed) > 0 {
		addrs, hostErrs := c.validateDetailedHosts()
		detailedAddrs = addrs
		errs = append(errs, hostErrs...)
	}

	var vip netip.Addr
	switch c.HA.Mode {
	case "localhost loadbalancing":
	case "kube-vip":
		if c.HA.VIP == "" {
			add("ha.vip: required when ha.mode is kube-vip")
		} else {
			addr, err := parseIPv4(c.HA.VIP)
			if err != nil {
				add("ha.vip: %v", err)
			} else {
				vip = addr
			}
		}
	default:
		add("ha.mode: %q is not localhost or kube-vip", c.HA.Mode)
	}

	switch c.Network.Plugin {
	case "flannel", "calico":
	default:
		add("network.plugin: %q is not flannel or calico", c.Network.Plugin)
	}
	if c.Network.CalicoVxlanMode != "" {
		if c.Network.Plugin != "calico" {
			add("network.calico_vxlan_mode: only valid with network.plugin: calico")
		}
		if c.Network.CalicoVxlanMode != "Always" && c.Network.CalicoVxlanMode != "CrossSubnet" {
			add("network.calico_vxlan_mode: %q is not Always or CrossSubnet", c.Network.CalicoVxlanMode)
		}
	}

	var servicePrefix, podPrefix netip.Prefix
	hasService, hasPod := false, false
	if c.Network.ServiceCIDR != "" {
		prefix, err := parseIPv4Prefix(c.Network.ServiceCIDR)
		if err != nil {
			add("network.service_cidr: %v", err)
		} else {
			servicePrefix, hasService = prefix, true
		}
	}
	if c.Network.PodCIDR != "" {
		prefix, err := parseIPv4Prefix(c.Network.PodCIDR)
		if err != nil {
			add("network.pod_cidr: %v", err)
		} else {
			podPrefix, hasPod = prefix, true
		}
	}
	if hasService && hasPod && servicePrefix.Overlaps(podPrefix) {
		add("network: service_cidr and pod_cidr overlap")
	}

	checkInside := func(name string, prefix netip.Prefix) {
		for _, entry := range autoEntries {
			if intervalOverlapsPrefix(entry.Start, entry.End, prefix) {
				add("network.%s: host range %q is inside %s", name, entry.Raw, prefix)
			} else if entry.AccessIP.IsValid() && prefix.Contains(entry.AccessIP) {
				add("network.%s: access IP %s is inside %s", name, entry.AccessIP, prefix)
			}
		}
		for _, addr := range detailedAddrs {
			if prefix.Contains(addr) {
				add("network.%s: host address %s is inside %s", name, addr, prefix)
			}
		}
		if vip.IsValid() && prefix.Contains(vip) {
			add("network.%s: VIP %s is inside %s", name, vip, prefix)
		}
	}
	if hasService {
		checkInside("service_cidr", servicePrefix)
	}
	if hasPod {
		checkInside("pod_cidr", podPrefix)
	}

	if err := c.validateFeatures(app); err != nil {
		errs = append(errs, err)
	}
	if err := c.validateMirror(app); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (c *installConfig) validateDetailedHosts() ([]netip.Addr, []error) {
	var addrs []netip.Addr
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	names := map[string]bool{}
	ips := map[string]bool{}
	ansibleHosts := map[string]bool{}
	roleCount := map[string]int{}

	for i, host := range c.Hosts.Detailed {
		if !hostnamePattern.MatchString(host.Name) || len(host.Name) > 63 {
			add("hosts[%d].name: %q is not a valid hostname", i, host.Name)
		}
		if names[host.Name] {
			add("hosts[%d].name: %q must be unique", i, host.Name)
		}
		names[host.Name] = true

		if addr, err := parseIPv4(host.IP); err != nil {
			add("hosts[%d].ip: %v", i, err)
		} else {
			addrs = append(addrs, addr)
			if ips[addr.String()] {
				add("hosts[%d].ip: %s must be unique", i, addr)
			}
			ips[addr.String()] = true
		}
		if addr, err := parseIPv4(host.AccessIP); err != nil {
			add("hosts[%d].access_ip: %v", i, err)
		} else {
			addrs = append(addrs, addr)
		}
		if addr, err := parseIPv4(host.AnsibleHost); err != nil {
			add("hosts[%d].ansible_host: %v", i, err)
		} else {
			addrs = append(addrs, addr)
			if ansibleHosts[addr.String()] {
				add("hosts[%d].ansible_host: %s must be unique", i, addr)
			}
			ansibleHosts[addr.String()] = true
		}

		if len(host.Roles) == 0 {
			add("hosts[%d].roles: at least one role is required", i)
			continue
		}
		hasControlPlane, hasNode, hasCalicoRR := false, false, false
		for _, role := range host.Roles {
			switch role {
			case roleControlPlane:
				hasControlPlane = true
				roleCount[roleControlPlane]++
			case roleNode:
				hasNode = true
				roleCount[roleNode]++
			case roleEtcd:
				roleCount[roleEtcd]++
			case roleCalicoRR:
				hasCalicoRR = true
				roleCount[roleCalicoRR]++
			default:
				add("hosts[%d].roles: unknown role %q", i, role)
			}
		}
		if hasCalicoRR && (hasControlPlane || hasNode) {
			add("hosts[%d].roles: calico_rr cannot be combined with control-plane or node", i)
		}

		for key := range host.Labels {
			if strings.TrimSpace(key) == "" {
				add("hosts[%d].labels: label key is empty", i)
			}
		}
		for _, taint := range host.Taints {
			if strings.TrimSpace(taint) == "" {
				add("hosts[%d].taints: taint is empty", i)
			}
		}
	}

	if roleCount[roleControlPlane] == 0 {
		add("hosts.roles: at least one control-plane host is required")
	}
	if roleCount[roleEtcd] == 0 {
		add("hosts.roles: at least one etcd host is required")
	}
	if roleCount[roleEtcd]%2 == 0 {
		add("hosts.roles: etcd host count must be odd, got %d", roleCount[roleEtcd])
	}

	return addrs, errs
}

func (c *installConfig) validateFeatures(app AppConfig) error {
	expected, err := configurableVarTypes(app)
	if err != nil {
		return fmt.Errorf("features: %w", err)
	}

	var errs []error
	for key, value := range c.Features {
		want, ok := expected[key]
		if !ok {
			errs = append(errs, fmt.Errorf("features.%s: unknown key; put arbitrary Ansible variables in extra_vars", key))
			continue
		}
		if !featureValueMatches(want, value) {
			errs = append(errs, fmt.Errorf("features.%s: want %s, got %s (type mismatch)", key, valueTypeName(want), valueTypeName(value)))
		}
	}

	return errors.Join(errs...)
}

func (c *installConfig) validateMirror(app AppConfig) error {
	known := map[string]bool{}
	for _, item := range app.Default_mirrors {
		for key := range item {
			known[key] = true
		}
	}

	var errs []error
	if len(c.Mirror.URLs) > 0 && !c.Mirror.Enabled {
		errs = append(errs, errors.New("mirror.enabled: set true to use mirror.urls"))
	}
	for key, value := range c.Mirror.URLs {
		if !known[key] {
			errs = append(errs, fmt.Errorf("mirror.urls: unknown mirror key %q", key))
		}
		if key == "files_repo" && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			errs = append(errs, fmt.Errorf("mirror.urls.files_repo: %q must be an http(s) URL", value))
		}
	}

	return errors.Join(errs...)
}

func configurableVarTypes(app AppConfig) (map[string]any, error) {
	types := map[string]any{}
	for _, item := range app.Configurable_vars {
		vars, ok := item["var"].(map[string]any)
		if !ok {
			return nil, errors.New("configurable_vars entry has no var map")
		}
		for key, value := range vars {
			types[key] = value
		}
	}

	return types, nil
}

func featureValueMatches(want, got any) bool {
	switch want.(type) {
	case bool:
		_, ok := got.(bool)
		return ok
	case string:
		_, ok := got.(string)
		return ok
	case int:
		_, ok := got.(int)
		return ok
	case []any:
		_, ok := got.([]any)
		return ok
	case map[string]any:
		_, ok := got.(map[string]any)
		return ok
	default:
		return false
	}
}

func valueTypeName(value any) string {
	switch value.(type) {
	case bool:
		return "bool"
	case string:
		return "string"
	case int:
		return "int"
	case []any:
		return "list"
	case map[string]any:
		return "map"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func normalizeHAMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "localhost", "localhost loadbalancing", "localhost-loadbalancing", "local":
		return "localhost loadbalancing"
	case "kube-vip", "kubevip", "kube vip":
		return "kube-vip"
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func normalizeRoles(roles []string) []string {
	normalized := make([]string, 0, len(roles))
	seen := map[string]bool{}
	for _, role := range roles {
		role = normalizeRole(role)
		if role == "" || seen[role] {
			continue
		}
		seen[role] = true
		normalized = append(normalized, role)
	}

	return normalized
}

func normalizeRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	role = strings.ReplaceAll(role, "_", "-")

	switch role {
	case "control-plane", "controlplane", "kube-control-plane", "master":
		return roleControlPlane
	case "node", "worker", "kube-node":
		return roleNode
	case "etcd":
		return roleEtcd
	case "calico-rr":
		return roleCalicoRR
	default:
		return role
	}
}

func expandHomePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is empty")
	}
	if path == "~" {
		path = homePath
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(homePath, path[2:])
	}

	return filepath.Abs(path)
}

func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f
}

// autoHostEntry is one bare hosts entry. Start and End are the inclusive IPv4
// interval of its primary address, used for duplicate/overlap and CIDR checks.
type autoHostEntry struct {
	Raw      string
	IP       netip.Addr
	AccessIP netip.Addr
	Start    uint32
	End      uint32
}

func parseAutoHostEntries(entries []string) ([]autoHostEntry, error) {
	parsed := make([]autoHostEntry, 0, len(entries))
	for i, raw := range entries {
		entry, err := parseAutoHostEntry(raw)
		if err != nil {
			return nil, fmt.Errorf("hosts[%d]: %w", i, err)
		}
		parsed = append(parsed, entry)
	}

	return parsed, nil
}

func parseAutoHostEntry(raw string) (autoHostEntry, error) {
	entry := autoHostEntry{}
	text := strings.TrimSpace(raw)
	if text == "" {
		return entry, errors.New("empty host entry")
	}

	if strings.Contains(text, ",") {
		parts := strings.Split(text, ",")
		if len(parts) != 2 {
			return entry, fmt.Errorf("%q: expected ip,access_ip", raw)
		}
		ip, err := parseIPv4(parts[0])
		if err != nil {
			return entry, err
		}
		accessIP, err := parseIPv4(parts[1])
		if err != nil {
			return entry, fmt.Errorf("access_ip: %w", err)
		}
		entry.Raw = ip.String() + "," + accessIP.String()
		entry.IP = ip
		entry.AccessIP = accessIP
		entry.Start = addrToUint32(ip)
		entry.End = entry.Start
		return entry, nil
	}

	if strings.Contains(text, "-") {
		parts := strings.Split(text, "-")
		if len(parts) != 2 {
			return entry, fmt.Errorf("%q: invalid range", raw)
		}
		startAddr, err := parseIPv4(parts[0])
		if err != nil {
			return entry, err
		}
		endAddr, err := parseIPv4(parts[1])
		if err != nil {
			return entry, err
		}
		start := addrToUint32(startAddr)
		end := addrToUint32(endAddr)
		if start > end {
			return entry, fmt.Errorf("%q: range start is after end", raw)
		}
		entry.Raw = startAddr.String() + "-" + endAddr.String()
		entry.IP = startAddr
		entry.AccessIP = startAddr
		entry.Start = start
		entry.End = end
		return entry, nil
	}

	ip, err := parseIPv4(text)
	if err != nil {
		return entry, err
	}
	entry.Raw = ip.String()
	entry.IP = ip
	entry.AccessIP = ip
	entry.Start = addrToUint32(ip)
	entry.End = entry.Start

	return entry, nil
}

func checkAutoHostOverlaps(entries []autoHostEntry) error {
	sorted := append([]autoHostEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Start < sorted[j].Start
	})
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Start <= sorted[i-1].End {
			return fmt.Errorf("host entries %q and %q overlap", sorted[i-1].Raw, sorted[i].Raw)
		}
	}

	// An access IP is a connection address too. Two entries that share one, or
	// an access IP inside another entry's primary range, would send checks to
	// the same host twice.
	for i, entry := range entries {
		if !entry.AccessIP.IsValid() {
			continue
		}
		for j, other := range entries {
			if i == j {
				continue
			}
			sharesAccessIP := other.AccessIP.IsValid() && entry.AccessIP == other.AccessIP
			if sharesAccessIP || intervalContains(other.Start, other.End, entry.AccessIP) {
				return fmt.Errorf("host entries %q and %q overlap on %s", entry.Raw, other.Raw, entry.AccessIP)
			}
		}
	}

	return nil
}

func intervalContains(start, end uint32, addr netip.Addr) bool {
	value := addrToUint32(addr)
	return value >= start && value <= end
}

func parseIPv4(value string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid address %q", value)
	}
	if !addr.Is4() {
		return netip.Addr{}, fmt.Errorf("invalid address %q: IPv4 is required", value)
	}

	return addr, nil
}

func parseIPv4Prefix(value string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q", value)
	}
	if !prefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q: IPv4 is required", value)
	}
	if prefix.Bits() > 30 {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q: at least /30 is required", value)
	}

	return prefix, nil
}

func addrToUint32(addr netip.Addr) uint32 {
	bytes := addr.As4()
	return binary.BigEndian.Uint32(bytes[:])
}

func prefixInterval(prefix netip.Prefix) (uint32, uint32) {
	start := addrToUint32(prefix.Masked().Addr())
	hostBits := 32 - prefix.Bits()
	end := uint32(uint64(start) + (uint64(1) << hostBits) - 1)

	return start, end
}

func intervalOverlapsPrefix(start, end uint32, prefix netip.Prefix) bool {
	prefixStart, prefixEnd := prefixInterval(prefix)
	return start <= prefixEnd && end >= prefixStart
}
