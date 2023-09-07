package main

import (
	"github.com/rivo/tview"
	"golang.org/x/exp/slices"
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
				Hosts map[string]map[interface{}]interface{}
			}
			Kube_node struct {
				Hosts map[string]map[interface{}]interface{}
			}
			Etcd struct {
				Hosts map[string]map[interface{}]interface{}
			}
			K8s_cluster struct {
				Children struct {
					Kube_control_plane map[string]interface{}
					Kube_node          map[string]interface{}
				}
			}
			Calico_rr struct {
				Hosts map[string]map[interface{}]interface{}
			}
		}
		Vars map[string]interface{}
	}
}

func initFormNewInventory() {
	formNewInventory.SetTitle("New Project")
	formNewInventory.SetBorder(true)

	formNewInventory.AddInputField("IP address of each node (separated by space): ", "", 0, nil, func(ips string) {
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

	formNewInventory.AddCheckbox("Modify node hostname with prefix", modifyNodeHostname, func(checked bool) {
		modifyNodeHostname = true
		formNewInventory.Clear(true)
		initFormNewInventory()
	})

	formNewInventory.AddInputField("Hostname prefix: ", "node", 20, nil, func(text string) {
		nodeHostnamePrefix = text
	})
	inputFieldPrefix := formNewInventory.GetFormItemByLabel("Hostname prefix: ")
	inputFieldPrefix.SetDisabled(!modifyNodeHostname)

	formNewInventory.AddCheckbox("Use current node hostname", !modifyNodeHostname, func(checked bool) {
		modifyNodeHostname = false
		formNewInventory.Clear(true)
		initFormNewInventory()
	})

	formNewInventory.AddButton("OK", func() {
		nodeHostnamePrefix = strings.Trim(nodeHostnamePrefix, " ")
		if len(nodeIps) == 0 {
			showErrorModal("Please provide IP address of each node.",
				func(buttonIndex int, buttonLabel string) {
					formNewInventory.Clear(true)
					initFormNewInventory()
					pages.SwitchToPage("New Inventory")
				})
		} else if modifyNodeHostname && nodeHostnamePrefix == "" {
			showErrorModal("Please provide hostname prefix.",
				func(buttonIndex int, buttonLabel string) {
					formNewInventory.Clear(true)
					initFormNewInventory()
					pages.SwitchToPage("New Inventory")
				})
		} else {
			populateInventory()
			initFlexEditInventory()
			pages.SwitchToPage("Edit Inventory")
		}
	})
	formNewInventory.AddButton("Cancel", func() {
		pages.SwitchToPage("Project")
	})
}

func initFlexEditInventory() {
	formHostDetails.SetBorder(true)

	listHosts := tview.NewList().ShowSecondaryText(false)
	for index, host := range getHostsList() {
		listHosts.AddItem(host, "", rune(49+index), nil)
	}
	listHosts.SetChangedFunc(func(index int, host string, secondaryText string, shortcut rune) {
		hostname := strings.Split(host, ":")[0]
		formHostDetails.Clear(true)
		initFormHostDetails(hostname)
	})
	host, _ := listHosts.GetItemText(0)
	hostname := strings.Split(host, ":")[0]
	formHostDetails.Clear(true)
	initFormHostDetails(hostname)

	formLeft := tview.NewForm().
		AddButton("Add Node", func() {
			// todo:
		}).
		AddButton("Save", func() {
			// todo:
		})
	flexLeft := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(listHosts, 0, 1, true).
		AddItem(formLeft, 3, 1, false)
	flexLeft.SetBorder(true)

	flexUp := tview.NewFlex().
		AddItem(flexLeft, 0, 1, true).
		AddItem(formHostDetails, 0, 2, false)

	formDown := tview.NewForm()
	formDown.AddButton("Next", nil)
	formDown.AddButton("Cancel", nil)

	flexEditInventory.SetTitle("Edit Inventory")
	flexEditInventory.SetBorder(true)

	flexEditInventory.SetDirection(tview.FlexRow).
		AddItem(flexUp, 0, 1, true).
		AddItem(formDown, 3, 1, false)
}

func populateInventory() {
	inventoryFile = filepath.Join(projectPath, "hosts.yaml")
	ips := strings.Join(nodeIps, " ")
	cmd := exec.Command("/bin/sh", "-c", "python3 "+filepath.Join(kubesprayPath, inventoryBuilder)+" "+ips)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "CONFIG_FILE="+inventoryFile)
	if modifyNodeHostname {
		cmd.Env = append(cmd.Env, "HOST_PREFIX="+nodeHostnamePrefix, "USE_REAL_HOSTNAME=False")
	} else {
		cmd.Env = append(cmd.Env, "USE_REAL_HOSTNAME=True")
	}
	err := cmd.Run()
	check(err)

	data, err := os.ReadFile(inventoryFile)
	check(err)

	err = yaml.Unmarshal(data, &inventory)
	check(err)
}

func saveInventory() {
	inventoryFile = filepath.Join(projectPath, "hosts.yaml")

	data, err := yaml.Marshal(&inventory)
	check(err)

	err = os.WriteFile(inventoryFile, data, 0644)
	check(err)
}

func loadInventory() {
	inventoryFile = filepath.Join(projectPath, "hosts.yaml")

	data, err := os.ReadFile(inventoryFile)
	check(err)

	err = yaml.Unmarshal(data, &inventory)
	check(err)
}

func getHostsList() []string {
	var hostsList []string
	for name, host := range inventory.All.Hosts {
		hostsList = append(hostsList, name+": "+host.Ansible_host)
	}

	slices.Sort(hostsList)

	return hostsList
}
