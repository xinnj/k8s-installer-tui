package main

import (
	"errors"
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strconv"
)

const pythonRequirements = "requirements.txt"

type AppConfig struct {
	Python_repo            string
	Predefined_node_labels map[string]string
	Predefined_node_taints []string
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
var flexEditNodeTaints = tview.NewFlex()
var formAddHost = tview.NewForm()
var flexFeatures = tview.NewFlex()
var flexHaMode = tview.NewFlex()
var flexNetwork = tview.NewFlex()
var flexMirror = tview.NewFlex()
var flexDeployCluster = tview.NewFlex()
var logFilePath string
var flexSetupCluster = tview.NewFlex()
var defaultSshKeyfile = filepath.Join(projectPath, "ansible-key")
var extraVars map[string]any
var flexSetupMode = tview.NewFlex()
var setupNewCluster bool
var offlinePath = "/root/k8s-installer-offline"
var inContainer bool
var kubesprayRuntime = "docker.io/xinnj/kubespray-runtime"

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
	_, err1 := os.Stat("/root/.idocluster-dependencies-installed")
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

	var pythonRepoParam string
	if appConfig.Python_repo != "" {
		pythonRepoParam = " -i " + appConfig.Python_repo
	}

	var cmds []string
	if inContainer {
		cmds = []string{
			fmt.Sprintf("%s/podman-launcher-amd64 rmi -if %s", offlinePath, kubesprayRuntime),
			fmt.Sprintf("%s/podman-launcher-amd64 load -i %s/docker.io_xinnj_kubespray-runtime.tar", offlinePath, offlinePath),
		}
	} else {
		cmds = []string{
			"if [ -n \"$(which yum 2>/dev/null)\" ]; then pkg_mgr=yum; else pkg_mgr=apt; fi; $pkg_mgr install -y python3-pip podman podman-docker sshpass rsync",
			"touch /etc/containers/nodocker",
			"pip3 install -r " + filepath.Join(kubesprayPath, pythonRequirements) + pythonRepoParam,
			"pip3 install -r " + filepath.Join(kubesprayPath, "contrib/inventory_builder/requirements.txt") + pythonRepoParam,
		}
	}

	fmt.Println("Install dependencies. Please wait a while...")

	len := len(cmds)
	for index, cmd := range cmds {
		fmt.Println(strconv.Itoa(index+1) + " of " + strconv.Itoa(len))
		execCommandAndCheck(cmd, 0, false)
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

	execCommandAndCheck("mkdir -p /root/.ssh", 0, false)

	ex, err := os.Executable()
	check(err)
	appPath = filepath.Dir(ex)

	_, err1 := os.Stat(offlinePath + "/podman-launcher-amd64")
	_, err2 := os.Stat(offlinePath + "/docker.io_xinnj_kubespray-runtime.tar")
	if err1 == nil && err2 == nil {
		inContainer = true
		fmt.Println("Install offline.")
	} else {
		inContainer = false
		fmt.Println("Install online.")
	}

	readConfig()

	prepareKubespray()

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
