package main

import (
	"errors"
	"fmt"
	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
)

const pythonRequirements = "requirements-2.12.txt"

type AppConfig struct {
	Predefined_node_labels map[string]string
	Default_vars           map[string]any
	Configuable_vars       []map[string]any
}

var appPath string
var kubesprayPath string
var appConfig AppConfig
var app = tview.NewApplication()
var pages = tview.NewPages()
var formProject = tview.NewForm()
var formNewInventory = tview.NewForm()
var projectPath string
var inventoryFile string
var nodeIps []string
var inventory = Inventory{}
var modalError = tview.NewModal()
var nodeHostnamePrefix string = "node"
var flexEditInventory = tview.NewFlex()
var formHostDetails = tview.NewForm()
var hostDetails HostDetails
var formEditGroups = tview.NewForm()
var flexEditNodeLabels = tview.NewFlex()
var formAddHost = tview.NewForm()
var flexFeatures = tview.NewFlex()
var flexHaMode = tview.NewFlex()

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func checkRoot() {
	cmd := exec.Command("id", "-u")
	output, err := cmd.Output()
	check(err)
	if string(output[:len(output)-1]) != "0" {
		fmt.Println("Application must run as root or as sudoer.")
		os.Exit(1)
	}
}

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
	_, err := os.Stat("/root/.kubespray-dependencies-installed")
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
	output, err := exec.Command("tar", "xvf", matches[0]).CombinedOutput()
	if err != nil {
		fmt.Printf("%s\n", output)
		check(err)
	}

	findKubesprayPath()

	cmds := [][]string{{"/bin/sh", "-c", "yum install -y python3-pip podman podman-docker sshpass"},
		{"touch", "/etc/containers/nodocker"},
		{"/bin/sh", "-c", "pip3 install -U -r " + filepath.Join(kubesprayPath, pythonRequirements) + " -i https://pypi.tuna.tsinghua.edu.cn/simple"},
		{"/bin/sh", "-c", "pip3 install -U -r " + filepath.Join(kubesprayPath, "contrib/inventory_builder/requirements.txt") + " -i https://pypi.tuna.tsinghua.edu.cn/simple"}}
	for _, cmd := range cmds {
		fmt.Println(cmd)
		output, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			fmt.Printf("%s\n", output)
			check(err)
		}
	}

	file, err := os.Create("/root/.kubespray-dependencies-installed")
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

func showErrorModal(text string, handler func(buttonIndex int, buttonLabel string)) {
	modalError.ClearButtons()
	modalError.SetText(text).AddButtons([]string{"OK"}).SetDoneFunc(handler)
	pages.SwitchToPage("Error")
}

func initFormProject() {
	formProject.SetTitle("Project")
	formProject.SetBorder(true)
	formProject.AddInputField("Project path:", "", 0, nil, func(path string) {
		projectPath = path
	})
	formProject.AddButton("New", func() {
		if projectPath == "" {
			showErrorModal("Please provide project path.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Project")
				})
		} else {
			err := os.MkdirAll(projectPath, 0755)
			if err != nil {
				showErrorModal("Can't create path: "+projectPath,
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Project")
					})
			}

			nodeHostnamePrefix = "node"
			formNewInventory.Clear(true)
			initFormNewInventory()
			pages.SwitchToPage("New Inventory")
		}
	})
	formProject.AddButton("Load", func() {
		if projectPath == "" {
			showErrorModal("Please provide project path.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Project")
				})
		} else {
			loadInventory()
			flexEditInventory.Clear()
			initFlexEditInventory("")
			pages.SwitchToPage("Edit Inventory")
		}
	})
	formProject.AddButton("Quit", func() {
		app.Stop()
	})
}

func main() {
	checkRoot()

	ex, err := os.Executable()
	check(err)
	appPath = filepath.Dir(ex)

	installDependencies()

	readConfig()

	initFormProject()

	pages.AddPage("Error", modalError, true, false)
	pages.AddPage("Project", formProject, true, true)
	pages.AddPage("New Inventory", formNewInventory, true, false)
	pages.AddPage("Edit Inventory", flexEditInventory, true, false)
	pages.AddPage("Edit Groups", formEditGroups, true, false)
	pages.AddPage("Edit Node Labels", flexEditNodeLabels, true, false)
	pages.AddPage("Add Host", formAddHost, true, false)
	pages.AddPage("Features", flexFeatures, true, false)
	pages.AddPage("Features", flexHaMode, true, false)

	if err := app.SetRoot(pages, true).EnableMouse(true).Run(); err != nil {
		panic(err)
	}
}
