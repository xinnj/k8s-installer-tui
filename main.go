package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
)

const podmanDownloadUrl = "https://github.com/xinnj/podman-launcher/releases/download/1.0.0/podman-launcher-amd64"
const kubesprayRuntimeTag = "docker.io/xinnj/kubespray-runtime:2.28.0"
const kubesprayRuntimeFile = "docker.io_xinnj_kubespray-runtime-2.28.0.tar"
const inContainer = true

var homePath, _ = os.UserHomeDir()
var offlinePath = filepath.Join(homePath, "/k8s-installer-offline")
var containerTool = "podman"
var appPath string
var kubesprayPath string

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
var flexDeployCluster = tview.NewFlex()
var logFileName string
var flexSetupCluster = tview.NewFlex()
var defaultSshKeyfile = filepath.Join(projectPath, "ansible-key")
var extraVars map[string]any
var flexSetupMode = tview.NewFlex()
var setupNewCluster bool

func prepareKubespray() {
	kubesprayPath = filepath.Join(appPath, "kubespray")
	_, err := os.Stat(kubesprayPath)
	// Path not exist, extract the package
	if err != nil {
		matches, err := filepath.Glob(filepath.Join(appPath, "kubespray-*.tar.gz"))
		check(err)
		if matches == nil {
			panic("Can't find kubespray archive file.")
		}
		cmd := fmt.Sprintf("mkdir -p %s;"+
			"tar xvf %s -C %s --strip-components=1;"+
			"cp -af %s/patches/* %s",
			kubesprayPath, matches[0], kubesprayPath, appPath, kubesprayPath)
		execCommandAndCheck(cmd, 0, false)
	}
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

	_, err1 = execCommand("sudo podman --version", 0, false)
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

	output, err := execCommand(fmt.Sprintf("sudo %s images -nq %s", containerTool, kubesprayRuntimeTag), 0, false)
	if err != nil || len(output) == 0 {
		fmt.Println("Pulling kubespray runtime image")
		var cmds []string
		_, err = os.Stat(filepath.Join(offlinePath, kubesprayRuntimeFile))
		if err != nil {
			cmds = append(cmds, fmt.Sprintf("sudo %s pull %s", containerTool, kubesprayRuntimeTag))
		} else {
			cmds = append(cmds, fmt.Sprintf("sudo %s load -i %s", containerTool, filepath.Join(offlinePath, kubesprayRuntimeFile)))
		}

		cmdsLen := len(cmds)
		for index, cmd := range cmds {
			fmt.Println(strconv.Itoa(index+1) + " of " + strconv.Itoa(cmdsLen))
			execCommandAndCheck(cmd, 0, false)
		}
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

func readConfig() {
	var configPath string
	if len(os.Args) > 1 {
		var err error
		configPath, err = filepath.Abs(os.Args[1])
		check(err)
	} else {
		configPath = filepath.Join(appPath, "config.yaml")
	}

	data, err := os.ReadFile(configPath)
	check(err)

	err = yaml.Unmarshal(data, &appConfig)
	check(err)
}

func main() {
	checkPrivilege()

	ex, err := os.Executable()
	check(err)
	appPath = filepath.Dir(ex)

	readConfig()

	prepareKubespray()

	output, err := execCommand("sudo podman --version", 0, false)
	if err != nil || len(output) == 0 {
		// podman not found in system, use the one in offline path
		containerTool = filepath.Join(offlinePath, "podman")
	}

	installDependencies()

	output, err = execCommand("sudo "+containerTool+" --version", 0, false)
	if err != nil || len(output) == 0 {
		fmt.Println("Can't execute " + containerTool + ". Please check the installation.")
		os.Exit(1)
	}

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
	pages.AddPage("Deploy Cluster", flexDeployCluster, true, false)
	pages.AddPage("Setup Cluster", flexSetupCluster, true, false)

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
