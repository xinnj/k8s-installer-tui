package main

import (
	"errors"
	"github.com/rivo/tview"
	"golang.org/x/exp/slices"
	"regexp"
	"strconv"
	"strings"
)

var flexHostDetails = tview.NewFlex()
var removeButton *tview.Button

func initFlexEditHosts(selectedHostname string) {
	listHosts := tview.NewList().ShowSecondaryText(false)
	for index, host := range getHostsList() {
		listHosts.AddItem(host, "", rune(49+index), nil)
	}
	listHosts.SetChangedFunc(func(index int, host string, secondaryText string, shortcut rune) {
		hostname := strings.Split(host, ":")[0]
		flexHostDetails.Clear()
		initFlexHostDetails(hostname)
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
	flexHostDetails.Clear()
	initFlexHostDetails(hostname)

	formLeft := tview.NewForm().
		AddButton("Add", func() {
			formAddHost.Clear(true)
			initFormAddHost()
			pages.SwitchToPage("Add Host")
		}).
		AddButton("Remove", func() {
			currentItem, _ := listHosts.GetItemText(listHosts.GetCurrentItem())
			hostToBeRemoved := strings.Split(currentItem, ":")[0]
			modalConfirm := tview.NewModal().
				SetText("Are you want to remove node:\n" + hostToBeRemoved).
				AddButtons([]string{"Remove", "Cancel"}).
				SetDoneFunc(func(buttonIndex int, buttonLabel string) {
					if buttonLabel == "Cancel" {
						pages.SwitchToPage("Edit Hosts")
					}
					if buttonLabel == "Remove" {
						delete(inventory.All.Hosts, hostToBeRemoved)
						delete(inventory.All.Children.Kube_control_plane.Hosts, hostToBeRemoved)
						delete(inventory.All.Children.Kube_node.Hosts, hostToBeRemoved)
						delete(inventory.All.Children.Etcd.Hosts, hostToBeRemoved)
						delete(inventory.All.Children.Calico_rr.Hosts, hostToBeRemoved)

						flexEditHosts.Clear()
						initFlexEditHosts("")
						pages.SwitchToPage("Edit Hosts")
					}
				})
			pages.AddPage("Confirm Remove Node", modalConfirm, true, true)
		}).
		AddButton("Save", func() {
			saveInventory()
			flexEditHosts.Clear()
			initFlexEditHosts("")
		})

	removeButton = formLeft.GetButton(formLeft.GetButtonIndex("Remove"))
	if len(inventory.All.Hosts) <= 1 {
		removeButton.SetDisabled(true)
	} else {
		removeButton.SetDisabled(false)
	}

	flexLeft := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(listHosts, 0, 1, true).
		AddItem(formLeft, 3, 1, false)
	flexLeft.SetBorder(true)

	flexUp := tview.NewFlex().
		AddItem(flexLeft, 0, 1, true).
		AddItem(flexHostDetails, 0, 2, false)
	flexUp.SetTitle("Edit Hosts").SetBorder(true)

	formDown := tview.NewForm()
	formDown.AddButton("Save & Next", func() {
		if len(inventory.All.Children.Etcd.Hosts)%2 == 0 {
			showErrorModal("ETCD node number should be odd. "+
				"Current number is "+strconv.Itoa(len(inventory.All.Children.Etcd.Hosts))+".",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Edit Hosts")
				})
			return
		}

		saveInventory()
		flexEditHosts.Clear()
		initFlexEditHosts("")
		flexFeatures.Clear()
		initFlexFeatures()
		pages.SwitchToPage("Features")
	})
	formDown.AddButton("Back", func() {
		pages.SwitchToPage("Project")
	})
	formDown.AddButton("Quit", func() {
		showQuitModal("Edit Hosts")
	})

	flexEditHosts.SetDirection(tview.FlexRow).
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
				// etcd nodes number should be always odd
				for node := range inventory.All.Hosts {
					inventory.All.Children.Etcd.Hosts[node] = make(map[any]any)
				}

				newHostDetails.Groups = append(newHostDetails.Groups, "etcd")
				newHostDetails.Groups = append(newHostDetails.Groups, "kube_node")
			default:
				newHostDetails.Groups = append(newHostDetails.Groups, "kube_node")
			}

			hostDetails = newHostDetails
			writeBackHostDetails()
			flexEditHosts.Clear()
			initFlexEditHosts(newHostDetails.Hostname)
			pages.SwitchToPage("Edit Hosts")
		}
	})

	formAddHost.AddButton("Cancel", func() {
		pages.SwitchToPage("Edit Hosts")
	})
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
