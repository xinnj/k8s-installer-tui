package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
)

const inventoryBuilder = "inventory_builder/inventory.py"

var inventoryFile string
var nodeIps []string
var inventory = Inventory{}
var originalInventory = Inventory{}
var projectPath = "/root/idocluster"
var nodeHostnamePrefix = "node"

type Host struct {
	Ansible_host string
	Ip           string
	Access_ip    string
	Node_labels  map[string]string
	Node_taints  []string
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
	}
}

func initFlexProject() {
	formProject := tview.NewForm()
	formProject.SetTitle("Project")
	formProject.SetBorder(true)

	formProject.AddInputField("Project path:", projectPath, 0, nil, func(path string) {
		projectPath = path
	})

	if setupNewCluster {
		formProject.AddButton("New", func() {
			if projectPath == "" {
				showErrorModal("Please provide project path.",
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Project")
					})
				return
			}

			formNewProject.Clear(true)
			initFormNewProject()
			pages.SwitchToPage("New Project")
		})
	}

	formProject.AddButton("Load", func() {
		if projectPath == "" {
			showErrorModal("Please provide project path.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Project")
				})
			return
		}

		err := loadInventory()
		if err != nil {
			return
		}

		flexEditHosts.Clear()
		initFlexEditHosts("")
		pages.SwitchToPage("Edit Hosts")

	})

	formProject.AddButton("Back", func() {
		pages.SwitchToPage("Setup Mode")
	})

	formProject.AddButton("Quit", func() {
		showQuitModal()
	})

	flexProject.SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(formProject, 7, 1, true).
		AddItem(nil, 0, 1, false)
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
					pages.SwitchToPage("New Project")
				})
			return
		}
		if nodeHostnamePrefix == "" {
			showErrorModal("Please provide hostname prefix.",
				func(buttonIndex int, buttonLabel string) {
					formNewProject.Clear(true)
					initFormNewProject()
					pages.SwitchToPage("New Project")
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
						pages.SwitchToPage("New Project")
					})
				return
			}

			// Backup existing project path
			err = os.Rename(projectPath, backupPath)
			if err != nil {
				showErrorModal("Can't backup path: "+projectPath,
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("New Project")
					})
				return
			}
		}

		err = os.MkdirAll(projectPath, 0755)
		if err != nil {
			showErrorModal("Can't create path: "+projectPath,
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("New Project")
				})
			return
		}

		execCommandAndCheck("cp -af "+filepath.Join(kubesprayPath, "inventory/sample/*")+" "+projectPath, 0, false)
		populateInventory()
		flexEditHosts.Clear()
		initFlexEditHosts("")
		pages.SwitchToPage("Edit Hosts")
	})

	formNewProject.AddButton("Cancel", func() {
		pages.SwitchToPage("Project")
	})
}

func populateInventory() {
	inventory = Inventory{}
	originalInventory = Inventory{}
	extraVars = make(map[string]any)

	inventoryFile = filepath.Join(projectPath, "hosts.yaml")
	ips := strings.Join(nodeIps, " ")
	cmd := "python3 " + filepath.Join(appPath, inventoryBuilder) + " " + ips
	execCommandAndCheck(cmd, 0, inContainer, "CONFIG_FILE="+inventoryFile, "HOST_PREFIX="+nodeHostnamePrefix)

	data, err := os.ReadFile(inventoryFile)
	check(err)
	err = yaml.Unmarshal(data, &inventory)
	check(err)

	extraVarsFile := filepath.Join(projectPath, "extra-vars.yaml")
	_, err = os.Stat(extraVarsFile)
	if err == nil {
		data, err = os.ReadFile(extraVarsFile)
		check(err)
		err = yaml.Unmarshal(data, &extraVars)
		check(err)
	}
}

func loadInventory() error {
	inventory = Inventory{}
	originalInventory = Inventory{}
	extraVars = make(map[string]any)

	inventoryFile = filepath.Join(projectPath, "hosts.yaml")

	if !setupNewCluster {
		_, err := os.Stat(filepath.Join(projectPath, "modified-hosts.yaml"))
		if err == nil {
			inventoryFile = filepath.Join(projectPath, "modified-hosts.yaml")
		}

		originalInventoryFile := filepath.Join(projectPath, "hosts.yaml")
		data, err := os.ReadFile(originalInventoryFile)
		if err != nil {
			showErrorModal("Can't find file: "+originalInventoryFile,
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Project")
				})
			return err
		}

		err = yaml.Unmarshal(data, &originalInventory)
		if err != nil {
			showErrorModal("Can't parse file: "+originalInventoryFile,
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Project")
				})
			return err
		}
	} else {
		execCommandAndCheck("cp -af "+filepath.Join(kubesprayPath, "inventory/sample/*")+" "+projectPath, 0, false)
	}

	data, err := os.ReadFile(inventoryFile)
	if err != nil {
		showErrorModal("Can't find file: "+inventoryFile,
			func(buttonIndex int, buttonLabel string) {
				pages.SwitchToPage("Project")
			})
		return err
	}

	err = yaml.Unmarshal(data, &inventory)
	if err != nil {
		showErrorModal("Can't parse file: "+inventoryFile,
			func(buttonIndex int, buttonLabel string) {
				pages.SwitchToPage("Project")
			})
		return err
	}

	extraVarsFile := filepath.Join(projectPath, "extra-vars.yaml")
	_, err = os.Stat(extraVarsFile)
	if err == nil {
		data, err = os.ReadFile(extraVarsFile)

		err = yaml.Unmarshal(data, &extraVars)
		if err != nil {
			showErrorModal("Can't parse file: "+extraVarsFile,
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Project")
				})
			return err
		}
	}

	return nil
}

func saveInventory() {
	if setupNewCluster {
		inventoryFile = filepath.Join(projectPath, "hosts.yaml")
	} else {
		inventoryFile = filepath.Join(projectPath, "modified-hosts.yaml")
	}

	data, err := yaml.Marshal(&inventory)
	check(err)
	err = os.WriteFile(inventoryFile, data, 0644)
	check(err)

	// extra-vars.yaml can only be modified during setting up a new cluster
	if setupNewCluster {
		extraVarsFile := filepath.Join(projectPath, "extra-vars.yaml")
		data, err = yaml.Marshal(&extraVars)
		check(err)
		err = os.WriteFile(extraVarsFile, data, 0644)
		check(err)
	}
}
