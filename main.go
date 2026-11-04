package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
)

const podmanDownloadUrl = "https://github.com/xinnj/podman-launcher/releases/download/1.0.0/podman-launcher-amd64"
const kubesprayRuntimeTag = "docker.io/xinnj/kubespray-runtime:2.29.1"
const kubesprayRuntimeFile = "docker.io_xinnj_kubespray-runtime-2.29.1.tar"
const inContainer = true

// homePath is the home directory of the user who invoked the app. When run
// under sudo, $HOME is reset to the target user's home (/root), so the
// invoking user's home is taken from SUDO_USER instead.
var homePath = resolveHomePath()
var offlinePath = filepath.Join(homePath, "/k8s-installer-offline")
var containerTool = filepath.Join(offlinePath, "podman")
var appPath string
var kubesprayPath string

func resolveHomePath() string {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		if u, err := user.Lookup(sudoUser); err == nil && u.HomeDir != "" {
			return u.HomeDir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Can't determine the home directory: "+err.Error())
		os.Exit(1)
	}
	return home
}

type AppConfig struct {
	Predefined_node_labels map[string]string
	Predefined_node_taints []string
	Default_vars           map[string]any
	Configurable_vars      []map[string]any
	Default_mirrors        []map[string]string
}

var appConfig AppConfig
var app = tview.NewApplication()
var pages = tview.NewPages()
var modalError = tview.NewModal()
var modalQuit = tview.NewModal()
var flexProject = tview.NewFlex()
var formNewProject = tview.NewForm()
var flexEditHosts = tview.NewFlex()
var formEditGroups = tview.NewForm()
var flexEditNodeLabels = tview.NewFlex()
var flexEditNodeTaints = tview.NewFlex()
var formAddHost = tview.NewForm()
var flexFeatures = tview.NewFlex()
var flexHaMode = tview.NewFlex()
var flexNetwork = tview.NewFlex()
var flexMirror = tview.NewFlex()
var flexSaveConfig = tview.NewFlex()
var createMode bool
var createConfigPath string
var flexDeployCluster = tview.NewFlex()
var logFileName string
var flexSetupCluster = tview.NewFlex()
var extraVars map[string]any
var flexSetupMode = tview.NewFlex()
var setupNewCluster bool

// patchStampFileName is the file in the kubespray tree that records which
// archive and patch set built it. A tree with another build's stamp is rebuilt
// rather than patched in place: the patches are full-file replacements, so an
// old one has to give way to the archive's own file, and a new archive has to
// replace the old tree.
const patchStampFileName = ".idocluster-patches"

func prepareKubespray() {
	kubesprayPath = filepath.Join(appPath, "kubespray")

	patches, err := patchFingerprint(filepath.Join(appPath, "patches"))
	check(err)

	matches, err := filepath.Glob(filepath.Join(appPath, "kubespray-*.tar.gz"))
	check(err)
	archive := ""
	if len(matches) > 0 {
		info, err := os.Stat(matches[0])
		check(err)
		archive = fmt.Sprintf("%s|%d", filepath.Base(matches[0]), info.Size())
	}

	// A host that keeps only the extracted tree has no archive to compare, so
	// matching patches are enough there.
	if stamp, err := os.ReadFile(filepath.Join(kubesprayPath, patchStampFileName)); err == nil {
		stampedPatches, stampedArchive, _ := strings.Cut(string(stamp), "\n")
		if stampedPatches == patches && (archive == "" || stampedArchive == archive) {
			chownToRunner(kubesprayPath)
			return
		}
	}

	if archive == "" {
		panic("Can't find kubespray archive file. Put kubespray-*.tar.gz next to the installer so " + kubesprayPath + " can be rebuilt.")
	}

	cmd := fmt.Sprintf("mkdir -p %s;"+
		"tar xf %s -C %s --strip-components=1;"+
		"cp -af %s/patches/* %s",
		kubesprayPath, matches[0], kubesprayPath, appPath, kubesprayPath)
	execCommandAndCheck(cmd, 0, false)

	check(os.WriteFile(filepath.Join(kubesprayPath, patchStampFileName), []byte(patches+"\n"+archive), 0644))

	chownToRunner(kubesprayPath)
}

// patchFingerprint identifies the patch set this build ships: the relative
// path and the content of every file under patchDir. A tree built with the
// same patches has the same fingerprint.
func patchFingerprint(patchDir string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(patchDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		relative, err := filepath.Rel(patchDir, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		fmt.Fprintf(hash, "%s\n", relative)
		hash.Write(content)

		return nil
	})
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func installDependencies() {
	_, err1 := os.Stat(filepath.Join(homePath, ".idocluster-dependencies-installed"))
	_, err2 := os.Stat(filepath.Join(appPath, ".idocluster-dependencies-installed"))
	if err1 == nil && err2 == nil {
		return
	}
	if err1 != nil && !errors.Is(err1, os.ErrNotExist) {
		panic(err1)
	}
	if err2 != nil && !errors.Is(err2, os.ErrNotExist) {
		panic(err2)
	}

	fmt.Println("Install dependencies. Please wait a while...")

	_, err1 = execCommand("sudo env "+podmanEnv+" podman --version", 0, false)
	_, err2 = os.Stat(filepath.Join(offlinePath, "podman"))
	if err1 != nil && err2 != nil {
		fmt.Println("Downloading podman")
		cmds := []string{
			fmt.Sprintf("mkdir -p %s; curl -o %s/podman -L %s", offlinePath, offlinePath, podmanDownloadUrl),
			fmt.Sprintf("chmod +x %s/podman", offlinePath),
		}
		cmdsLen := len(cmds)
		for index, cmd := range cmds {
			fmt.Println(strconv.Itoa(index+1) + " of " + strconv.Itoa(cmdsLen))
			execCommandAndCheck(cmd, 0, false)
		}
	}

	var cmds []string
	_, err := os.Stat(filepath.Join(offlinePath, kubesprayRuntimeFile))
	if err != nil {
		fmt.Println("Pulling kubespray runtime image from registry")
		cmds = append(cmds, sudoPodman("pull "+kubesprayRuntimeTag))
	} else {
		fmt.Println("Loading kubespray runtime image from offline file")
		cmds = append(cmds, sudoPodman("load -i "+filepath.Join(offlinePath, kubesprayRuntimeFile)))
	}

	cmdsLen := len(cmds)
	for index, cmd := range cmds {
		fmt.Println(strconv.Itoa(index+1) + " of " + strconv.Itoa(cmdsLen))
		execCommandAndCheck(cmd, 0, false)
	}

	file, err := os.Create(filepath.Join(homePath, ".idocluster-dependencies-installed"))
	check(err)
	err = file.Close()
	check(err)

	file, err = os.Create(filepath.Join(appPath, ".idocluster-dependencies-installed"))
	check(err)
	err = file.Close()
	check(err)
}

func readAppConfig(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(data, &appConfig)
}

func setAppPath() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	appPath = filepath.Dir(executable)

	return nil
}

func preflightHost() {
	checkPrivilege()

	check(os.MkdirAll(offlinePath, 0755))

	prepareKubespray()

	installDependencies()

	chownToRunner(offlinePath)
	chownToRunner(filepath.Join(homePath, ".idocluster-dependencies-installed"))
	chownToRunner(filepath.Join(appPath, ".idocluster-dependencies-installed"))

	output, err := execCommand(sudoPodman("--version"), 0, false)
	if err != nil || len(output) == 0 {
		fmt.Println("Can't execute " + containerTool + ". Please check the installation.")
		os.Exit(1)
	}

	// Nothing replaces a container any more, so a container an earlier run left
	// behind - one still running an Ansible playbook, say - would keep working
	// on a cluster nobody is watching.
	removeLeftoverContainers()
}

func initPages() {
	initFlexSetupMode()

	pages.AddPage("Error", modalError, true, false)
	pages.AddPage("Quit", modalQuit, true, false)

	pages.AddPage("Setup Mode", flexSetupMode, true, true)
	pages.AddPage("Project", flexProject, true, false)
	pages.AddPage("New Project", formNewProject, true, false)
	pages.AddPage("Edit Hosts", flexEditHosts, true, false)
	pages.AddPage("Edit Groups", formEditGroups, true, false)
	pages.AddPage("Edit Node Labels", flexEditNodeLabels, true, false)
	pages.AddPage("Edit Node Taints", flexEditNodeTaints, true, false)
	pages.AddPage("Add Host", formAddHost, true, false)
	pages.AddPage("Features", flexFeatures, true, false)
	pages.AddPage("HA Mode", flexHaMode, true, false)
	pages.AddPage("Network", flexNetwork, true, false)
	pages.AddPage("Mirror", flexMirror, true, false)
	pages.AddPage("Save Config", flexSaveConfig, true, false)
	pages.AddPage("Deploy Cluster", flexDeployCluster, true, false)
	pages.AddPage("Setup Cluster", flexSetupCluster, true, false)
}

func runApp() {
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			showQuitModal()
			return tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModNone)
		}

		return event
	})

	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		panic(err)
	}
}

func main() {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(os.Stdout)
			return
		}
		fmt.Fprintln(os.Stderr, "k8s-installer-tui:", err)
		fmt.Fprintln(os.Stderr, "Run 'k8s-installer-tui --help' for usage.")
		os.Exit(2)
	}
	if opts.showVersion {
		fmt.Println(versionString())
		return
	}

	if err := setAppPath(); err != nil {
		fmt.Fprintln(os.Stderr, "k8s-installer-tui:", err)
		os.Exit(1)
	}

	if opts.create {
		if err := readAppConfig(filepath.Join(appPath, "config.yaml")); err != nil {
			fmt.Fprintln(os.Stderr, "k8s-installer-tui: config:", err)
			os.Exit(2)
		}

		// Set the mode before initPages: the Setup Mode page is built from it.
		createMode = true
		createConfigPath = opts.configPath
		preflightHost()
		initPages()
		runApp()
		return
	}

	if opts.configPath != "" {
		if err := readAppConfig(filepath.Join(appPath, "config.yaml")); err != nil {
			fmt.Fprintln(os.Stderr, "k8s-installer-tui: config:", err)
			os.Exit(2)
		}

		cfg, err := loadInstallConfig(opts.configPath, appConfig)
		if err != nil {
			fmt.Fprintln(os.Stderr, "k8s-installer-tui:", err)
			os.Exit(2)
		}
		if warning := configPermissionWarning(opts.configPath); warning != "" {
			fmt.Fprintln(os.Stderr, "warning:", warning)
		}

		runConfigDriven(cfg, opts.dryRun)
		return
	}

	check(readAppConfig(filepath.Join(appPath, "config.yaml")))

	preflightHost()
	initPages()
	runApp()
}
