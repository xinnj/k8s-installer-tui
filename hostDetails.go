package main

import (
	"github.com/rivo/tview"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
	"strings"
)

type HostDetails struct {
	Hostname     string
	Ansible_host string
	Ip           string
	Access_ip    string
	Groups       []string
	Node_labels  map[string]string
}

var hostDetails HostDetails
var tmpNodeLabels string

func initFlexHostDetails(hostname string, readonly bool) {
	getHostDetails(hostname)

	formHostDetails := tview.NewForm()
	formHostDetails.AddTextView("Hostname: ", hostname, 0, 3, false, false)

	formHostDetails.AddInputField("Ansible Host:", hostDetails.Ansible_host, 0, nil, func(text string) {
		hostDetails.Ansible_host = text
		writeBackHostDetails()
	})
	formHostDetails.AddInputField("IP:", hostDetails.Ip, 0, nil, func(text string) {
		hostDetails.Ip = text
		writeBackHostDetails()
	})
	formHostDetails.AddInputField("Access IP:", hostDetails.Access_ip, 0, nil, func(text string) {
		hostDetails.Access_ip = text
		writeBackHostDetails()
	})

	formHostDetails.AddTextView("Groups: ", strings.Join(hostDetails.Groups, "\n"), 0, 0, false, true)

	var labelsString string
	if len(hostDetails.Node_labels) != 0 {
		labels, err := yaml.Marshal(&hostDetails.Node_labels)
		check(err)
		labelsString = string(labels)
	}
	formHostDetails.AddTextView("Node Labels: ", labelsString, 0, 0, false, true)

	formDown := tview.NewForm()

	if readonly {
		for i := 0; i < formHostDetails.GetFormItemCount(); i++ {
			formHostDetails.GetFormItem(i).SetDisabled(true)
		}
	} else {
		formDown.
			AddButton("Edit Groups", func() {
				initFormEditGroups()
				pages.SwitchToPage("Edit Groups")
			}).
			AddButton("Edit Node Labels", func() {
				flexEditNodeLabels.Clear()
				initFlexEditNodeLabels()
				pages.SwitchToPage("Edit Node Labels")
			})
	}

	flexHostDetails.
		SetDirection(tview.FlexRow).
		AddItem(formHostDetails, 0, 1, true).
		AddItem(formDown, 3, 1, false)
	flexHostDetails.SetBorder(true)
}

func initFormEditGroups() {
	formEditGroups.Clear(true)

	var newGroups []string
	checkedMap := map[string]string{}
	for _, group := range hostDetails.Groups {
		checkedMap[group] = "checked"
	}

	formEditGroups.SetTitle("Edit Group").SetBorder(true)
	formEditGroups.AddTextView("Hostname: ", hostDetails.Hostname, 0, 3, false, false)

	formEditGroups.AddCheckbox("kube_control_plane", slices.Contains(hostDetails.Groups, "kube_control_plane"), func(checked bool) {
		if checked {
			checkedMap["kube_control_plane"] = "checked"
		} else {
			delete(checkedMap, "kube_control_plane")
		}
	})
	formEditGroups.AddCheckbox("kube_node", slices.Contains(hostDetails.Groups, "kube_node"), func(checked bool) {
		if checked {
			checkedMap["kube_node"] = "checked"
		} else {
			delete(checkedMap, "kube_node")
		}
	})
	formEditGroups.AddCheckbox("etcd", slices.Contains(hostDetails.Groups, "etcd"), func(checked bool) {
		if checked {
			checkedMap["etcd"] = "checked"
		} else {
			delete(checkedMap, "etcd")
		}
	})
	formEditGroups.AddCheckbox("calico_rr", slices.Contains(hostDetails.Groups, "calico_rr"), func(checked bool) {
		if checked {
			checkedMap["calico_rr"] = "checked"
		} else {
			delete(checkedMap, "calico_rr")
		}
	})

	formEditGroups.AddButton("OK", func() {
		for key := range checkedMap {
			newGroups = append(newGroups, key)
		}
		hostDetails.Groups = newGroups
		writeBackHostDetails()

		flexHostDetails.Clear()
		initFlexHostDetails(hostDetails.Hostname, false)
		pages.SwitchToPage("Edit Hosts")
	})

	formEditGroups.AddButton("Cancel", func() {
		pages.SwitchToPage("Edit Hosts")
	})
}

func initFlexEditNodeLabels() {
	flexEditNodeLabels.SetTitle("Edit Node Labels").SetBorder(true)

	formEditLabels := tview.NewForm()
	formEditLabels.SetBorder(true)
	formEditLabels.AddTextView("Hostname: ", hostDetails.Hostname, 0, 3, false, false)

	if tmpNodeLabels == "" && len(hostDetails.Node_labels) != 0 {
		labels, err := yaml.Marshal(&hostDetails.Node_labels)
		check(err)
		tmpNodeLabels = string(labels)
	}

	formEditLabels.AddTextArea("Node Labels: ", tmpNodeLabels, 0, 0, 0, func(text string) {
		tmpNodeLabels = text
	})

	formEditLabels.AddButton("OK", func() {
		var newLabels map[string]string
		err := yaml.Unmarshal([]byte(tmpNodeLabels), &newLabels)
		if err != nil {
			showErrorModal("Node label format is wrong.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Edit Node Labels")
				})
		} else {
			hostDetails.Node_labels = newLabels
			writeBackHostDetails()
			tmpNodeLabels = ""
			flexHostDetails.Clear()
			initFlexHostDetails(hostDetails.Hostname, false)
			pages.SwitchToPage("Edit Hosts")
		}
	})

	formEditLabels.AddButton("Cancel", func() {
		tmpNodeLabels = ""
		pages.SwitchToPage("Edit Hosts")
	})

	formPredefinedLabels := tview.NewForm()
	formPredefinedLabels.SetBorder(true)
	var options []string
	for key, value := range appConfig.Predefined_node_labels {
		options = append(options, key+": "+value)
	}
	slices.Sort(options)
	var selectedLabel string
	formPredefinedLabels.AddDropDown("Predefined Labels", options, -1, func(option string, optionIndex int) {
		selectedLabel = option
	})

	formPredefinedLabels.AddButton("<< Add", func() {
		if selectedLabel != "" {
			tmpNodeLabels = tmpNodeLabels + selectedLabel + "\n"
			flexEditNodeLabels.Clear()
			initFlexEditNodeLabels()
		}
	})

	flexEditNodeLabels.
		AddItem(formEditLabels, 0, 2, true).
		AddItem(formPredefinedLabels, 0, 1, false)
}

func getHostDetails(hostname string) {
	hostDetails.Hostname = hostname
	hostDetails.Ansible_host = inventory.All.Hosts[hostname].Ansible_host
	hostDetails.Ip = inventory.All.Hosts[hostname].Ip
	hostDetails.Access_ip = inventory.All.Hosts[hostname].Access_ip
	hostDetails.Node_labels = inventory.All.Hosts[hostname].Node_labels

	var groups []string
	if _, ok := inventory.All.Children.Kube_control_plane.Hosts[hostname]; ok {
		groups = append(groups, "kube_control_plane")
	}
	if _, ok := inventory.All.Children.Kube_node.Hosts[hostname]; ok {
		groups = append(groups, "kube_node")
	}
	if _, ok := inventory.All.Children.Etcd.Hosts[hostname]; ok {
		groups = append(groups, "etcd")
	}
	if _, ok := inventory.All.Children.Calico_rr.Hosts[hostname]; ok {
		groups = append(groups, "calico_rr")
	}
	hostDetails.Groups = groups
}

func writeBackHostDetails() {
	var h Host
	h.Ansible_host = hostDetails.Ansible_host
	h.Ip = hostDetails.Ip
	h.Access_ip = hostDetails.Access_ip
	h.Node_labels = hostDetails.Node_labels

	if slices.Contains(hostDetails.Groups, "kube_control_plane") {
		inventory.All.Children.Kube_control_plane.Hosts[hostDetails.Hostname] = make(map[any]any)
	} else {
		delete(inventory.All.Children.Kube_control_plane.Hosts, hostDetails.Hostname)
	}
	if slices.Contains(hostDetails.Groups, "kube_node") {
		inventory.All.Children.Kube_node.Hosts[hostDetails.Hostname] = make(map[any]any)
	} else {
		delete(inventory.All.Children.Kube_node.Hosts, hostDetails.Hostname)
	}
	if slices.Contains(hostDetails.Groups, "etcd") {
		inventory.All.Children.Etcd.Hosts[hostDetails.Hostname] = make(map[any]any)
	} else {
		delete(inventory.All.Children.Etcd.Hosts, hostDetails.Hostname)
	}
	if slices.Contains(hostDetails.Groups, "calico_rr") {
		inventory.All.Children.Calico_rr.Hosts[hostDetails.Hostname] = make(map[any]any)
	} else {
		delete(inventory.All.Children.Calico_rr.Hosts, hostDetails.Hostname)
	}

	inventory.All.Hosts[hostDetails.Hostname] = h
}
