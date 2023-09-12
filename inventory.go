package main

import (
	"errors"
	"github.com/rivo/tview"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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

	formNewInventory.AddInputField("Hostname prefix: ", "node", 20, nil, func(text string) {
		nodeHostnamePrefix = text
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
		} else if nodeHostnamePrefix == "" {
			showErrorModal("Please provide hostname prefix.",
				func(buttonIndex int, buttonLabel string) {
					formNewInventory.Clear(true)
					initFormNewInventory()
					pages.SwitchToPage("New Inventory")
				})
		} else {
			populateInventory()
			initFlexEditInventory("")
			pages.SwitchToPage("Edit Inventory")
		}
	})
	formNewInventory.AddButton("Cancel", func() {
		pages.SwitchToPage("Project")
	})
}

func initFlexEditInventory(selectedHostname string) {
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

	var hostname string
	if selectedHostname == "" {
		host, _ := listHosts.GetItemText(0)
		hostname = strings.Split(host, ":")[0]
	} else {
		selectedIndex := listHosts.FindItems(selectedHostname, "", false, false)
		listHosts.SetCurrentItem(selectedIndex[0])
		hostname = selectedHostname
	}
	formHostDetails.Clear(true)
	initFormHostDetails(hostname)

	formLeft := tview.NewForm().
		AddButton("Add Node", func() {
			formAddHost.Clear(true)
			initFormAddHost()
			pages.SwitchToPage("Add Host")
		}).
		AddButton("Save", func() {
			saveInventory()
		})
	flexLeft := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(listHosts, 0, 1, true).
		AddItem(formLeft, 3, 1, false)
	flexLeft.SetBorder(true)

	flexUp := tview.NewFlex().
		AddItem(flexLeft, 0, 1, true).
		AddItem(formHostDetails, 0, 2, false)
	flexUp.SetTitle("Edit Hosts").SetBorder(true)

	formDown := tview.NewForm()
	formDown.AddButton("Save & Next", func() {
		saveInventory()
		flexFeatures.Clear()
		initFlexFeatures()
		pages.SwitchToPage("Features")
	})
	formDown.AddButton("Cancel", func() {
		pages.SwitchToPage("Project")
	})

	flexEditInventory.SetDirection(tview.FlexRow).
		AddItem(flexUp, 0, 1, true).
		AddItem(formDown, 3, 1, false)
}

func initFormAddHost() {
	var newHostDetails HostDetails
	formAddHost.SetTitle("Add Host").SetBorder(true)

	hostname, err := getNextHostname()
	newHostDetails.Hostname = hostname
	if err == nil {
		formAddHost.AddTextView("Hostname: ", newHostDetails.Hostname, 0, 3, false, false)
	} else {
		formAddHost.AddInputField("Hostname: ", "", 0, nil, func(text string) {
			newHostDetails.Hostname = text
		})
	}

	formAddHost.AddInputField("IP: ", "", 0, nil, func(text string) {
		newHostDetails.Ansible_host = text
		newHostDetails.Ip = text
		newHostDetails.Access_ip = text
	})

	formAddHost.AddButton("OK", func() {
		if newHostDetails.Hostname == "" {
			showErrorModal("Please provide hostname.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Add Host")
				})
		} else if newHostDetails.Ip == "" {
			showErrorModal("Please provide IP address.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Add Host")
				})
		} else {
			currentHostsNum := len(inventory.All.Hosts)
			switch currentHostsNum {
			case 1:
				newHostDetails.Groups = append(newHostDetails.Groups, "kube_control_plane")
				newHostDetails.Groups = append(newHostDetails.Groups, "kube_node")
			case 2:
				newHostDetails.Groups = append(newHostDetails.Groups, "etcd")
				newHostDetails.Groups = append(newHostDetails.Groups, "kube_node")
			default:
				newHostDetails.Groups = append(newHostDetails.Groups, "kube_node")
			}

			hostDetails = newHostDetails
			writeBackHostDetails()
			flexEditInventory.Clear()
			initFlexEditInventory(newHostDetails.Hostname)
			pages.SwitchToPage("Edit Inventory")
		}
	})

	formAddHost.AddButton("Cancel", func() {
		pages.SwitchToPage("Edit Inventory")
	})
}

func populateInventory() {
	inventoryFile = filepath.Join(projectPath, "hosts.yaml")
	ips := strings.Join(nodeIps, " ")
	cmd := exec.Command("/bin/sh", "-c", "python3 "+filepath.Join(kubesprayPath, inventoryBuilder)+" "+ips)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "CONFIG_FILE="+inventoryFile)
	cmd.Env = append(cmd.Env, "HOST_PREFIX="+nodeHostnamePrefix)

	err := cmd.Run()
	check(err)

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

func getNextHostname() (string, error) {
	hostnamePrefix := ""
	highestHostid := 0

	re := regexp.MustCompile("^(.+?)(\\d+)$")
	for name := range inventory.All.Hosts {
		shortHostname := strings.Split(name, ".")[0]
		matches := re.FindStringSubmatch(shortHostname)
		if matches == nil {
			return "", errors.New("Can't match shortHostname.")
		}

		hostnamePrefix = matches[1]
		hostId, err := strconv.Atoi(matches[2])
		if err != nil {
			return "", err
		}

		if hostId > highestHostid {
			highestHostid = hostId
		}
	}

	return hostnamePrefix + strconv.Itoa(highestHostid+1), nil
}
