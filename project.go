package main

import (
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const inventoryBuilder = "contrib/inventory_builder/inventory.py"

type Host struct {
	Ansible_host string
	Ip           string
	Access_ip    string
	Node_labels  map[string]string
}

type Inventory struct {
	All struct {
		Hosts    map[string]Host
		Children struct {
			Kube_control_plane struct {
				Hosts map[string]map[any]any
			}
			Kube_node struct {
				Hosts map[string]map[any]any
			}
			Etcd struct {
				Hosts map[string]map[any]any
			}
			K8s_cluster struct {
				Children struct {
					Kube_control_plane map[string]any
					Kube_node          map[string]any
				}
			}
			Calico_rr struct {
				Hosts map[string]map[any]any
			}
		}
		Vars map[string]any
	}
}

func initFormProject() {
	formProject.SetTitle("Project")
	formProject.SetBorder(true)

	if projectPath == "" {
		projectPath = "/root/idocluster"
	}
	formProject.AddInputField("Project path:", projectPath, 0, nil, func(path string) {
		projectPath = path
	})

	formProject.AddButton("New", func() {
		if projectPath == "" {
			showErrorModal("Please provide project path.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Project")
				})
			return
		}

		_, err := os.Stat(projectPath)
		// Path exists
		if err == nil {
			// Remove existing backup folder first
			backupPath := strings.TrimSuffix(projectPath, "/") + ".bak"
			err = os.RemoveAll(backupPath)
			if err != nil {
				showErrorModal("Can't remove backup path: "+backupPath,
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Project")
					})
				return
			}

			// Backup existing project path
			err = os.Rename(projectPath, backupPath)
			if err != nil {
				showErrorModal("Can't backup path: "+projectPath,
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Project")
					})
				return
			}
		}

		err = os.MkdirAll(projectPath, 0755)
		if err != nil {
			showErrorModal("Can't create path: "+projectPath,
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Project")
				})
			return
		}

		execCommand(exec.Command("/bin/sh", "-c", "cp -a "+filepath.Join(appPath, "inventory/sample/*")+" "+projectPath))

		nodeHostnamePrefix = "node"
		formNewProject.Clear(true)
		initFormNewProject()
		pages.SwitchToPage("New Inventory")
	})
	formProject.AddButton("Load", func() {
		if projectPath == "" {
			showErrorModal("Please provide project path.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Project")
				})
		} else {
			inventoryFile = filepath.Join(projectPath, "hosts.yaml")

			data, err := os.ReadFile(inventoryFile)
			if err != nil {
				showErrorModal("Can't find file: "+inventoryFile,
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Project")
					})
				return
			}

			err = yaml.Unmarshal(data, &inventory)
			if err != nil {
				showErrorModal("Can't parse file: "+inventoryFile,
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Project")
					})
				return
			}

			flexEditInventory.Clear()
			initFlexEditHosts("")
			pages.SwitchToPage("Edit Inventory")
		}
	})
	formProject.AddButton("Quit", func() {
		app.Stop()
	})
}

func initFormNewProject() {
	formNewProject.SetTitle("New Project")
	formNewProject.SetBorder(true)

	formNewProject.AddInputField("IP address of each node (separated by space): ", "", 0, nil, func(ips string) {
		re := regexp.MustCompile(` +`)
		var r []string
		r = re.Split(ips, -1)
		nodeIps = nil
		for _, str := range r {
			str = strings.Trim(str, " ")
			if str != "" {
				nodeIps = append(nodeIps, str)
			}
		}
	})

	formNewProject.AddInputField("Hostname prefix: ", "node", 20, nil, func(text string) {
		nodeHostnamePrefix = text
	})

	formNewProject.AddButton("OK", func() {
		nodeHostnamePrefix = strings.Trim(nodeHostnamePrefix, " ")
		if len(nodeIps) == 0 {
			showErrorModal("Please provide IP address of each node.",
				func(buttonIndex int, buttonLabel string) {
					formNewProject.Clear(true)
					initFormNewProject()
					pages.SwitchToPage("New Inventory")
				})
		} else if nodeHostnamePrefix == "" {
			showErrorModal("Please provide hostname prefix.",
				func(buttonIndex int, buttonLabel string) {
					formNewProject.Clear(true)
					initFormNewProject()
					pages.SwitchToPage("New Inventory")
				})
		} else {
			populateInventory()
			initFlexEditHosts("")
			pages.SwitchToPage("Edit Inventory")
		}
	})
	formNewProject.AddButton("Cancel", func() {
		pages.SwitchToPage("Project")
	})
}

func populateInventory() {
	inventoryFile = filepath.Join(projectPath, "hosts.yaml")
	ips := strings.Join(nodeIps, " ")
	cmd := exec.Command("/bin/sh", "-c", "python3 "+filepath.Join(kubesprayPath, inventoryBuilder)+" "+ips)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "CONFIG_FILE="+inventoryFile)
	cmd.Env = append(cmd.Env, "HOST_PREFIX="+nodeHostnamePrefix)

	execCommand(cmd)

	data, err := os.ReadFile(inventoryFile)
	check(err)

	err = yaml.Unmarshal(data, &inventory)
	check(err)

	inventory.All.Vars = make(map[string]any)
}

func saveInventory() {
	inventoryFile = filepath.Join(projectPath, "hosts.yaml")

	data, err := yaml.Marshal(&inventory)
	check(err)

	err = os.WriteFile(inventoryFile, data, 0644)
	check(err)
}
