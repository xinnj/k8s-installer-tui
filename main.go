package main

import (
	"errors"
	"fmt"
	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
)

const pythonRequirements = "requirements-2.12.txt"

type AppConfig struct {
	Predefined_node_labels map[string]string
	Default_vars           map[string]any
	Configuable_vars       []map[string]any
	Default_mirrors        []map[string]string
}

var appPath string
var kubesprayPath string
var appConfig AppConfig
var app = tview.NewApplication()
var pages = tview.NewPages()
var formProject = tview.NewForm()
var formNewProject = tview.NewForm()
var modalError = tview.NewModal()
var flexEditHosts = tview.NewFlex()
var formHostDetails = tview.NewForm()
var formEditGroups = tview.NewForm()
var flexEditNodeLabels = tview.NewFlex()
var formAddHost = tview.NewForm()
var flexFeatures = tview.NewFlex()
var flexHaMode = tview.NewFlex()
var flexMirror = tview.NewFlex()
var flexDeployCluster = tview.NewFlex()
var logFile *os.File

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

// Todo: mirror configurable
func installDependencies() {
	_, err := os.Stat("/root/.idocluster-dependencies-installed")
	if err == nil {
		findKubesprayPath()
		return
	}
	if !errors.Is(err, os.ErrNotExist) {
		panic(err)
	}

	matches, err := filepath.Glob(filepath.Join(appPath, "kubespray-*.tar.gz"))
	check(err)
	if matches == nil {
		panic("Can't find kubespray archive file.")
	}
	execCommand("tar xvf "+matches[0], 0)

	findKubesprayPath()

	cmds := []string{"yum install -y python3-pip podman podman-docker sshpass",
		"touch /etc/containers/nodocker",
		"pip3 install -U -r " + filepath.Join(kubesprayPath, pythonRequirements) + " -i https://pypi.tuna.tsinghua.edu.cn/simple",
		"pip3 install -U -r " + filepath.Join(kubesprayPath, "contrib/inventory_builder/requirements.txt") + " -i https://pypi.tuna.tsinghua.edu.cn/simple"}
	for _, cmd := range cmds {
		fmt.Println(cmd)
		execCommand(cmd, 0)
	}

	file, err := os.Create("/root/.idocluster-dependencies-installed")
	check(err)
	err = file.Close()
	check(err)
}

func readConfig() {
	data, err := os.ReadFile(filepath.Join(appPath, "config.yaml"))
	check(err)

	err = yaml.Unmarshal(data, &appConfig)
	check(err)
}

func main() {
	checkRoot()

	initLog("deploy-cluster-")
	defer logFile.Close()

	ex, err := os.Executable()
	check(err)
	appPath = filepath.Dir(ex)

	installDependencies()

	readConfig()

	initFormProject()

	pages.AddPage("Error", modalError, true, false)
	pages.AddPage("Project", formProject, true, true)
	pages.AddPage("New Project", formNewProject, true, false)
	pages.AddPage("Edit Hosts", flexEditHosts, true, false)
	pages.AddPage("Edit Groups", formEditGroups, true, false)
	pages.AddPage("Edit Node Labels", flexEditNodeLabels, true, false)
	pages.AddPage("Add Host", formAddHost, true, false)
	pages.AddPage("Features", flexFeatures, true, false)
	pages.AddPage("HA Mode", flexHaMode, true, false)
	pages.AddPage("Mirror", flexMirror, true, false)
	pages.AddPage("Deploy Cluster", flexDeployCluster, true, false)

	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		panic(err)
	}
}
