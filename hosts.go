package main

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/exp/slices"
)

var flexHostDetails = tview.NewFlex()
var removeButton *tview.Button
var formHostsLeft = tview.NewForm()
var formHostsDown = tview.NewForm()

func initFlexEditHosts(selectedHostname string) {
	formHostsLeft.Clear(true)
	formHostsLeft.
		AddButton("Add", func() {
			formAddHost.Clear(true)
			initFormAddHost()
			pages.SwitchToPage("Add Host")
		}).
		AddButton("Remove", nil).
		AddButton("Save", func() {
			saveInventory()
			flexEditHosts.Clear()
			initFlexEditHosts("")
		})

	removeButton = formHostsLeft.GetButton(formHostsLeft.GetButtonIndex("Remove"))

	listHosts := tview.NewList().ShowSecondaryText(false)
	for index, host := range getHostsList() {
		listHosts.AddItem(host, "", rune(97+index), nil)
	}
	listHosts.SetChangedFunc(func(index int, host string, secondaryText string, shortcut rune) {
		hostname := strings.Split(host, ":")[0]
		if _, ok := originalInventory.All.Hosts[hostname]; ok {
			removeButton.SetDisabled(true)
			flexHostDetails.Clear()
			initFlexHostDetails(hostname, true)
		} else {
			removeButton.SetDisabled(false)
			flexHostDetails.Clear()
			initFlexHostDetails(hostname, false)
		}
	})

	removeButton.SetSelectedFunc(func() {
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
	})

	if len(inventory.All.Hosts) <= 1 {
		removeButton.SetDisabled(true)
	} else {
		removeButton.SetDisabled(false)
	}

	var hostname string
	if selectedHostname == "" {
		host, _ := listHosts.GetItemText(0)
		hostname = strings.Split(host, ":")[0]
	} else {
		selectedIndex := listHosts.FindItems(selectedHostname, "", false, false)
		listHosts.SetCurrentItem(selectedIndex[0])
		hostname = selectedHostname
	}
	if _, ok := originalInventory.All.Hosts[hostname]; ok {
		removeButton.SetDisabled(true)
		flexHostDetails.Clear()
		initFlexHostDetails(hostname, true)
	} else {
		flexHostDetails.Clear()
		initFlexHostDetails(hostname, false)
	}

	flexLeft := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(listHosts, 0, 1, true).
		AddItem(formHostsLeft, 3, 1, false)
	flexLeft.SetBorder(true)

	flexUp := tview.NewFlex().
		AddItem(flexLeft, 0, 1, true).
		AddItem(flexHostDetails, 0, 2, false)
	flexUp.SetTitle("Edit Hosts").SetBorder(true)

	formHostsDown.Clear(true)
	formHostsDown.AddButton("Save & Next", func() {
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

		if setupNewCluster {
			flexFeatures.Clear()
			initFlexFeatures()
			pages.SwitchToPage("Features")
		} else {
			flexDeployCluster.Clear()
			initFlexDeployCluster()
			pages.SwitchToPage("Deploy Cluster")
		}
	})
	formHostsDown.AddButton("Back", func() {
		pages.SwitchToPage("Project")
	})
	formHostsDown.AddButton("Quit", func() {
		showQuitModal()
	})

	flexEditHosts.SetDirection(tview.FlexRow).
		AddItem(flexUp, 0, 1, true).
		AddItem(formHostsDown, 3, 1, false)

	app.SetFocus(listHosts)

	listHosts.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlN {
			app.SetFocus(formHostsLeft)
		}
		if event.Key() == tcell.KeyCtrlP {
			app.SetFocus(formHostsDown)
		}
		return event
	})

	formHostsLeft.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlN {
			app.SetFocus(flexHostDetails)
		}
		if event.Key() == tcell.KeyCtrlP {
			app.SetFocus(listHosts)
		}
		return event
	})

	formHostsDown.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlN {
			app.SetFocus(listHosts)
		}
		if event.Key() == tcell.KeyCtrlP {
			app.SetFocus(flexHostDetails)
		}
		return event
	})
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
		newHostDetails.Ansible_port = "22"
		newHostDetails.Ip = text
		newHostDetails.Access_ip = text
	})

	formAddHost.AddButton("OK", func() {
		if newHostDetails.Hostname == "" {
			showErrorModal("Please provide hostname.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Add Host")
				})
			return
		} else if newHostDetails.Ip == "" {
			showErrorModal("Please provide IP address.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Add Host")
				})
			return
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

	//slices.Sort(hostsList)
	slices.SortFunc(hostsList, func(a, b string) int {
		re := regexp.MustCompile("^(.+?)(\\d+)$")

		aHostname := strings.Split(a, ":")[0]
		bHostname := strings.Split(b, ":")[0]

		aShortHostname := strings.Split(aHostname, ".")[0]
		aMatches := re.FindStringSubmatch(aShortHostname)
		aHostId, err := strconv.Atoi(aMatches[2])
		if err != nil {
			return strings.Compare(aHostname, bHostname)
		}

		bShortHostname := strings.Split(bHostname, ".")[0]
		bMatches := re.FindStringSubmatch(bShortHostname)
		bHostId, err := strconv.Atoi(bMatches[2])
		if err != nil {
			return strings.Compare(aHostname, bHostname)
		}

		if aHostId < bHostId {
			return -1
		} else if aHostId > bHostId {
			return 1
		}
		return 0
	})

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
