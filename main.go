package main

import (
	"errors"
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
)

const pythonRequirements = "requirements.txt"

type AppConfig struct {
	Python_repo            string
	Predefined_node_labels map[string]string
	Default_vars           map[string]any
	Configurable_vars      []map[string]any
	Default_mirrors        []map[string]string
}

var appPath string
var kubesprayPath string
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
var formAddHost = tview.NewForm()
var flexFeatures = tview.NewFlex()
var flexHaMode = tview.NewFlex()
var flexNetwork = tview.NewFlex()
var flexMirror = tview.NewFlex()
var flexDeployCluster = tview.NewFlex()
var logFilePath string
var flexSetupCluster = tview.NewFlex()
var keyFile string
var extraVars map[string]any
var flexSetupMode = tview.NewFlex()
var setupNewCluster bool

func findKubesprayPath() {
	matches, err := filepath.Glob(filepath.Join(appPath, "kubespray*"))
	check(err)
	if matches == nil {
		fmt.Println("Can't find kubespray directory.")
		check(err)
	}
	for _, match := range matches {
		f, err := os.Stat(match)
		check(err)
		if f.IsDir() {
			kubesprayPath = match
		}
	}
	if kubesprayPath == "" {
		panic("Can't find kubespray directory.")
	}
}

func installDependencies() {
	_, err1 := os.Stat("/root/.idocluster-dependencies-installed")
	_, err2 := os.Stat(filepath.Join(appPath, ".idocluster-dependencies-installed"))
	if err1 == nil && err2 == nil {
		findKubesprayPath()
		return
	}
	if err1 != nil && !errors.Is(err1, os.ErrNotExist) {
		panic(err1)
	}
	if err2 != nil && !errors.Is(err2, os.ErrNotExist) {
		panic(err2)
	}

	fmt.Println("Install dependencies...")

	matches, err := filepath.Glob(filepath.Join(appPath, "kubespray-*.tar.gz"))
	check(err)
	if matches == nil {
		panic("Can't find kubespray archive file.")
	}
	execCommand("tar xvf "+matches[0], 0)

	findKubesprayPath()

	execCommand("cp -af "+appPath+"/patches/* "+kubesprayPath, 0)

	var pythonRepoParam string
	if appConfig.Python_repo != "" {
		pythonRepoParam = " -i " + appConfig.Python_repo
	}

	cmds := []string{
		"if [ -n \"$(which yum 2>/dev/null)\" ]; then pkg_mgr=yum; else pkg_mgr=apt; fi; $pkg_mgr install -y python3-pip podman podman-docker sshpass rsync",
		"touch /etc/containers/nodocker",
		"pip3 install -r " + filepath.Join(kubesprayPath, pythonRequirements) + pythonRepoParam,
		"pip3 install -r " + filepath.Join(kubesprayPath, "contrib/inventory_builder/requirements.txt") + pythonRepoParam,
	}
	for _, cmd := range cmds {
		// fmt.Println(cmd)
		execCommand(cmd, 0)
	}

	file, err := os.Create("/root/.idocluster-dependencies-installed")
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
	checkRoot()

	ex, err := os.Executable()
	check(err)
	appPath = filepath.Dir(ex)

	readConfig()

	installDependencies()

	initFlexSetupMode()

	pages.AddPage("Error", modalError, true, false)
	pages.AddPage("Quit", modalQuit, true, false)

	pages.AddPage("Setup Mode", flexSetupMode, true, true)
	pages.AddPage("Project", flexProject, true, false)
	pages.AddPage("New Project", formNewProject, true, false)
	pages.AddPage("Edit Hosts", flexEditHosts, true, false)
	pages.AddPage("Edit Groups", formEditGroups, true, false)
	pages.AddPage("Edit Node Labels", flexEditNodeLabels, true, false)
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
