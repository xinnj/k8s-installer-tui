package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
)

// configRun is true while a --config run is in progress. autoRun starts the
// playbook without operator input; dryRun stops on the Deploy page instead.
var (
	configRun           bool
	autoRun             bool
	dryRun              bool
	autoRunExitCode     atomic.Int32
	stagedAuth          atomic.Bool
	cleanupOnce         sync.Once
	prefillSSHPassword  string
	prefillSudoPassword string
)

// runConfigDriven fills the wizard from cfg and, unless dry is set, runs the
// create-cluster pipeline. It does not return: the process exits with the run's
// code so callers can use it from automation.
func runConfigDriven(cfg *installConfig, dry bool) {
	dryRun = dry
	configRun = true
	autoRun = !dry
	autoRunExitCode.Store(1)
	if dry {
		autoRunExitCode.Store(0)
	}
	// Set before preflight so cleanup on a preflight panic targets the config's
	// project rather than the default one.
	projectPath = cfg.ProjectPath

	defer func() {
		if recovered := recover(); recovered != nil {
			message := panicMessage(recovered)
			appendToLog("\n===== installer error: " + message + " =====\n")
			fmt.Fprintln(os.Stderr, "k8s-installer-tui:", message)
			cleanupAutoRun()
			os.Exit(1)
		}
	}()

	installSignalHandler()

	preflightHost()

	if err := prepareInstall(cfg); err != nil {
		appendToLog("\n===== installer error: " + err.Error() + " =====\n")
		fmt.Fprintln(os.Stderr, "k8s-installer-tui:", err)
		cleanupAutoRun()
		os.Exit(1)
	}

	initPages()
	prefillPages(cfg)

	if autoRun {
		// The log view's changed callback calls app.Draw, which blocks until
		// the event loop is running. Queue the setup page after Run starts.
		go func() {
			app.QueueUpdateDraw(func() {
				initFlexSetupCluster(true)
				pages.SwitchToPage("Setup Cluster")
			})
		}()
	}

	runApp()

	cleanupAutoRun()
	code := int(autoRunExitCode.Load())
	logPath := ""
	if logFileName != "" {
		logPath = filepath.Join(projectPath, logFileName)
	}
	report := autoRunReport{
		Code:      code,
		LogPath:   logPath,
		Project:   projectPath,
		HostCount: len(inventory.All.Hosts),
		Dry:       dryRun,
	}
	if !startTime.IsZero() {
		report.Duration = time.Since(startTime)
	}
	fmt.Fprint(os.Stderr, autoRunSummary(report))
	os.Exit(code)
}

// prepareInstall writes the project files and, for a real run, stages the SSH
// access. A dry-run stops after the files and the conflict check.
func prepareInstall(cfg *installConfig) error {
	setupNewCluster = true
	projectPath = cfg.ProjectPath
	sshKeyFile = cfg.SSH.KeyFile
	nodeSshUser = cfg.SSH.User
	nodeSshPort = strconv.Itoa(cfg.SSH.Port)
	nodeHostnamePrefix = cfg.HostnamePrefix
	if cfg.SSH.KeyFile != "" {
		accessMethod = accessMethodsType.exist
	} else {
		accessMethod = accessMethodsType.new
	}

	if err := prepareProjectDirectory(); err != nil {
		return err
	}
	if err := prepareInventory(cfg); err != nil {
		return err
	}
	if err := validateBuiltInventory(); err != nil {
		return err
	}

	vars, err := buildExtraVars(cfg, appConfig, readClusterVars(projectPath))
	if err != nil {
		return fmt.Errorf("extra vars: %w", err)
	}
	if err := validateResolvedNetwork(vars, inventory, cfg.HA.VIP); err != nil {
		return err
	}
	extraVars = vars

	if err := applyMirrorConfig(cfg); err != nil {
		return err
	}
	if err := saveInventoryErr(); err != nil {
		return fmt.Errorf("save project: %w", err)
	}

	if !dryRun {
		initLog("create-cluster-")
		appendToLog(runHeader(cfg))
	}

	if err := checkNetworkConflicts(cfg, extraVars); err != nil {
		return err
	}
	if dryRun {
		return nil
	}

	stagedAuth.Store(true)
	return setupNodeAccess(cfg)
}

func prepareProjectDirectory() error {
	if _, err := os.Stat(projectPath); err == nil {
		fmt.Fprintf(os.Stderr, "Reusing existing project directory %s\n", projectPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("project: %w", err)
	}

	if err := os.MkdirAll(projectPath, 0700); err != nil {
		return fmt.Errorf("project: %w", err)
	}
	if err := os.Chmod(projectPath, 0700); err != nil {
		return fmt.Errorf("project: %w", err)
	}

	return copySampleInventory()
}

func copySampleInventory() error {
	// Keep the trailing "/." intact: filepath.Join would clean it away and cp
	// would copy the sample directory itself instead of its contents.
	source := filepath.Join(kubesprayPath, "inventory", "sample") + string(filepath.Separator) + "."
	output, err := runArgv([]string{"cp", "-a", source, projectPath}, 0)
	if err != nil {
		return fmt.Errorf("project: copy the inventory sample: %s", strings.TrimSpace(string(output)))
	}

	return nil
}

func prepareInventory(cfg *installConfig) error {
	if len(cfg.Hosts.Detailed) > 0 {
		inv, err := buildInventory(cfg)
		if err != nil {
			return fmt.Errorf("inventory: %w", err)
		}
		inventory = inv
		originalInventory = Inventory{}
		extraVars = map[string]any{}
		nodeIps = nil
		inventoryFile = filepath.Join(projectPath, "hosts.yaml")
		return nil
	}

	nodeIps = append([]string(nil), cfg.Hosts.Auto...)
	inventory = Inventory{}
	originalInventory = Inventory{}
	extraVars = map[string]any{}
	populateInventory()

	return nil
}

func validateBuiltInventory() error {
	if len(inventory.All.Hosts) == 0 {
		return errors.New("inventory: no hosts were generated")
	}
	if len(inventory.All.Children.Kube_control_plane.Hosts) == 0 {
		return errors.New("inventory: no control-plane host")
	}
	etcdHosts := len(inventory.All.Children.Etcd.Hosts)
	if etcdHosts == 0 {
		return errors.New("inventory: no etcd host")
	}
	if etcdHosts%2 == 0 {
		return fmt.Errorf("inventory: etcd host count must be odd, got %d", etcdHosts)
	}

	return nil
}

func readClusterVars(projectDir string) map[string]any {
	data, err := os.ReadFile(filepath.Join(projectDir, "group_vars/k8s_cluster/k8s-cluster.yml"))
	if err != nil {
		return nil
	}

	vars := map[string]any{}
	if err := yaml.Unmarshal(data, &vars); err != nil {
		return nil
	}

	return vars
}

func applyMirrorConfig(cfg *installConfig) error {
	mirrorFile := filepath.Join(projectPath, "group_vars/all/mirror.yml")
	if !cfg.Mirror.Enabled {
		if err := os.Remove(mirrorFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("mirror: %w", err)
		}
		return nil
	}

	source, err := os.ReadFile(filepath.Join(projectPath, "group_vars/all/offline.yml"))
	if err != nil {
		return fmt.Errorf("mirror: %w", err)
	}
	if err := os.WriteFile(mirrorFile, renderMirrorFile(source), 0644); err != nil {
		return fmt.Errorf("mirror: %w", err)
	}

	return nil
}

func checkNetworkConflicts(cfg *installConfig, extra map[string]any) error {
	if cfg.Network.CheckConflicts == nil || !*cfg.Network.CheckConflicts {
		return nil
	}

	serviceCIDR, _ := extra["kube_service_addresses"].(string)
	podCIDR, _ := extra["kube_pods_subnet"].(string)

	var ips []string
	for _, cidr := range []string{serviceCIDR, podCIDR} {
		sampled, err := sampledPrefixIPs(cidr)
		if err != nil {
			return fmt.Errorf("network conflict check: %w", err)
		}
		ips = append(ips, sampled...)
	}

	fmt.Fprintf(os.Stderr, "Checking %d sampled addresses for IP conflicts...\n", len(ips))
	reachable, _ := groupPing(ips)
	if len(reachable) == 0 {
		return nil
	}

	sort.Strings(reachable)
	message := fmt.Sprintf("Potential IP conflicts detected (sampled ping): %s", strings.Join(reachable, ", "))
	appendToLog(message + "\n")

	return errors.New(message)
}

func setupNodeAccess(cfg *installConfig) error {
	var errorNodes, errorMsgs []string
	var err error
	if cfg.SSH.KeyFile != "" {
		errorNodes, errorMsgs, err = checkSshKey(cfg.SSH.SudoPassword)
	} else {
		errorNodes, errorMsgs, err = copySshKeyToNode(cfg.SSH.Password)
	}
	if err != nil {
		return fmt.Errorf("SSH setup: %w", err)
	}
	if len(errorNodes) > 0 {
		return fmt.Errorf("SSH setup failed on %s:\n%s", strings.Join(errorNodes, ", "), strings.Join(errorMsgs, "\n\n"))
	}

	return nil
}

// prefillTUI sets the page-level globals so the wizard displays the config
// values rather than the form defaults.
func prefillTUI(cfg *installConfig) {
	prefillSSHPassword = cfg.SSH.Password
	prefillSudoPassword = cfg.SSH.SudoPassword
	sshKeyFile = cfg.SSH.KeyFile
	if cfg.SSH.KeyFile != "" {
		accessMethod = accessMethodsType.exist
	} else {
		accessMethod = accessMethodsType.new
	}

	haMode = cfg.HA.Mode
	vip = cfg.HA.VIP

	networkPlugin = cfg.Network.Plugin
	checkConflicts = cfg.Network.CheckConflicts != nil && *cfg.Network.CheckConflicts
	initialNetworkLoaded = true

	enableMirror = cfg.Mirror.Enabled
	initialValueLoaded = true
	// Leave the map nil while disabled so enabling the checkbox on the page
	// loads the default_mirrors values, exactly as the TUI does.
	mirrors = nil
	if cfg.Mirror.Enabled {
		mirrors = map[string]string{}
		for _, item := range appConfig.Default_mirrors {
			for key, value := range item {
				mirrors[key] = value
			}
		}
		for key, value := range cfg.Mirror.URLs {
			mirrors[key] = value
		}
	}
}

func prefillPages(cfg *installConfig) {
	prefillTUI(cfg)

	flexProject.Clear()
	initFlexProject()

	flexEditHosts.Clear()
	initFlexEditHosts("")

	flexFeatures.Clear()
	initFlexFeatures()

	flexHaMode.Clear()
	initFlexHaMode()

	flexNetwork.Clear()
	initFlexNetwork()

	flexMirror.Clear()
	initFlexMirror()

	flexDeployCluster.Clear()
	initFlexDeployCluster()

	if dryRun {
		pages.SwitchToPage("Deploy Cluster")
		return
	}

	flexSetupCluster.Clear()
	flexSetupCluster.AddItem(tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText("Starting the unattended run..."), 0, 1, true)
	pages.SwitchToPage("Setup Cluster")
}

func runHeader(cfg *installConfig) string {
	return fmt.Sprintf("===== Unattended installation started at %s; project=%s; hosts=%d =====\n",
		time.Now().Format(time.RFC3339), cfg.ProjectPath, len(inventory.All.Hosts))
}

func installSignalHandler() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-signals
		fmt.Fprintln(os.Stderr, "\nInterrupted; stopping the run and cleaning up...")
		// Restore the terminal before exiting, and record the signal's code in
		// case Run returns and runConfigDriven exits first.
		autoRunExitCode.Store(130)
		app.Stop()
		cleanupAutoRun()
		os.Exit(130)
	}()
}

func cleanupAutoRun() {
	cleanupOnce.Do(cleanupAutoRunOnce)
}

// autoRunReport carries what the console report needs. The TUI is gone by the
// time it is printed, so everything still worth reading has to come from here
// or from the log file.
type autoRunReport struct {
	Code      int
	LogPath   string
	Project   string
	HostCount int
	Duration  time.Duration
	Dry       bool
}

// autoRunTailLines is how much of a failed run's log is repeated on the
// console. The TUI has already shown it, but tcell wipes it on exit.
const autoRunTailLines = 20

// reportRule closes the result block, on success and on failure alike.
const reportRule = "====================================================="

// autoRunSummary reports how an unattended run ended, for the console the
// shell prompt returns to. tcell clears the screen when the application stops,
// so without this the operator saw no more than they had before the TUI
// started, and a failure's reason stayed only in the log file.
func autoRunSummary(report autoRunReport) string {
	if report.Dry {
		return "Dry run finished; no installation was started.\n"
	}

	var summary strings.Builder
	summary.WriteString("================ Installation result ================\n")
	result := "SUCCESS"
	if report.Code != 0 {
		result = "FAILED"
	}
	fmt.Fprintf(&summary, "Result     : %s\n", result)
	fmt.Fprintf(&summary, "Exit code  : %d\n", report.Code)
	if report.Duration > 0 {
		fmt.Fprintf(&summary, "Duration   : %s\n", formatRunDuration(report.Duration))
	}
	if report.Project != "" {
		fmt.Fprintf(&summary, "Project    : %s\n", report.Project)
	}
	if report.LogPath != "" {
		fmt.Fprintf(&summary, "Log file   : %s\n", report.LogPath)
	}
	if report.HostCount > 0 {
		fmt.Fprintf(&summary, "Hosts      : %d\n", report.HostCount)
	}

	if report.Code == 0 {
		summary.WriteString(reportRule)
		summary.WriteString("\n")
		return summary.String()
	}

	lines := readLogTail(report.LogPath)
	if problems := playRecapProblems(lines); len(problems) > 0 {
		summary.WriteString("\nHost problems:\n")
		for _, problem := range problems {
			fmt.Fprintf(&summary, "  %s  %s\n", problem.host, problemCounters(problem))
		}
	}
	if line := lastFailureLine(lines); line != "" {
		summary.WriteString("\nLast error:\n")
		// 78 plus the two-space indent keeps the block within 80 columns.
		for _, wrapped := range wrapWords(errorDetail(line), 78) {
			fmt.Fprintf(&summary, "  %s\n", wrapped)
		}
	}
	if tail := lastLines(lines, autoRunTailLines); len(tail) > 0 {
		fmt.Fprintf(&summary, "\nLast %d lines of the log:\n", autoRunTailLines)
		for _, line := range tail {
			fmt.Fprintf(&summary, "  %s\n", line)
		}
	}
	summary.WriteString(reportRule)
	summary.WriteString("\n")

	return summary.String()
}

func formatRunDuration(d time.Duration) string {
	d = d.Round(time.Second)
	hours := int(d / time.Hour)
	minutes := int(d/time.Minute) % 60
	seconds := int(d/time.Second) % 60
	switch {
	case hours > 0:
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	case minutes > 0:
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

// hostRecap is one host's PLAY RECAP counters, kept only when something went
// wrong with it.
type hostRecap struct {
	host        string
	unreachable int
	failed      int
}

var playRecapPattern = regexp.MustCompile(`^\s*(\S+)\s*:\s*ok=\d+.*\bunreachable=(\d+).*\bfailed=(\d+)`)

// playRecapProblems returns the hosts the last PLAY RECAP in the log marks
// unreachable or failed, sorted by name. A log with several recaps keeps the
// last count for each host.
func playRecapProblems(lines []string) []hostRecap {
	problems := map[string]hostRecap{}
	for _, line := range lines {
		match := playRecapPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		unreachable, _ := strconv.Atoi(match[2])
		failed, _ := strconv.Atoi(match[3])
		if unreachable == 0 && failed == 0 {
			delete(problems, match[1])
			continue
		}
		problems[match[1]] = hostRecap{host: match[1], unreachable: unreachable, failed: failed}
	}

	result := make([]hostRecap, 0, len(problems))
	for _, problem := range problems {
		result = append(result, problem)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].host < result[j].host })

	return result
}

func problemCounters(problem hostRecap) string {
	var counters []string
	if problem.unreachable > 0 {
		counters = append(counters, fmt.Sprintf("unreachable=%d", problem.unreachable))
	}
	if problem.failed > 0 {
		counters = append(counters, fmt.Sprintf("failed=%d", problem.failed))
	}
	return strings.Join(counters, " ")
}

// failureMarkers are the fragments that mark the line explaining why a run
// stopped. They are tried from the end of the log backwards.
var failureMarkers = []string{"UNREACHABLE!", "FAILED!", "ERROR!", "installer error:"}

func lastFailureLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		for _, marker := range failureMarkers {
			if strings.Contains(lines[i], marker) {
				return strings.TrimSpace(lines[i])
			}
		}
	}
	return ""
}

var ansibleMsgPattern = regexp.MustCompile(`"msg"\s*:\s*"((?:[^"\\]|\\.)*)"`)

// errorDetail keeps the useful part of an Ansible failure line: the task
// context before "=>" and the msg value, without the rest of the result map.
func errorDetail(line string) string {
	index := strings.Index(line, "=>")
	if index < 0 {
		return line
	}
	prefix := strings.TrimSpace(line[:index+2])
	match := ansibleMsgPattern.FindStringSubmatch(line[index+2:])
	if match == nil {
		return line
	}
	message := match[1]
	if unquoted, err := strconv.Unquote(`"` + match[1] + `"`); err == nil {
		message = unquoted
	}
	return prefix + " " + message
}

// wrapWords breaks text into lines of at most width runes, keeping whole words
// where it can and splitting a word that is longer than a line.
func wrapWords(text string, width int) []string {
	var lines []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			lines = append(lines, string(current))
			current = nil
		}
	}

	for _, word := range strings.Fields(text) {
		runes := []rune(word)
		if len(current) > 0 && len(current)+1+len(runes) > width {
			flush()
		}
		for len(runes) > width {
			if len(current) > 0 {
				flush()
			}
			lines = append(lines, string(runes[:width]))
			runes = runes[width:]
		}
		if len(current) > 0 {
			current = append(current, ' ')
		}
		current = append(current, runes...)
	}
	flush()

	return lines
}

// readLogTail returns the lines in the last maxLogTailBytes of path, since an
// Ansible log grows to megabytes. When the read starts in the middle of a
// line, that partial line is dropped.
func readLogTail(path string) []string {
	if path == "" {
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil
	}

	const maxLogTailBytes = 64 * 1024
	offset := info.Size() - maxLogTailBytes
	if offset < 0 {
		offset = 0
	}
	buffer := make([]byte, info.Size()-offset)
	if _, err := file.ReadAt(buffer, offset); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}

	text := strings.TrimRight(string(buffer), "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if offset > 0 && len(lines) > 1 {
		lines = lines[1:]
	}

	return lines
}

func lastLines(lines []string, count int) []string {
	if len(lines) > count {
		return lines[len(lines)-count:]
	}
	return lines
}

func cleanupAutoRunOnce() {
	if inContainer && containerUpWith.Load() != nil {
		removeContainer()
		forgetContainer()
	}
	// Only remove password files this run staged. A dry-run must not delete
	// files an earlier interactive run deliberately keeps for ad-hoc ansible.
	if stagedAuth.Load() {
		removeBecomePassword()
		os.Remove(sshPasswordPath())
	}
	chownToRunner(projectPath)
}

func panicMessage(value any) string {
	message := fmt.Sprint(value)
	if len(message) > 4096 {
		message = message[:4096] + "..."
	}

	return message
}
