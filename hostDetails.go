package main

import (
	"golang.org/x/exp/slices"
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

func initFormHostDetails(hostname string) {
	getHostDetails(hostname)

	formHostDetails.SetBorder(true)

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

	formHostDetails.AddTextView("Groups: ", strings.Join(hostDetails.Groups, "\n"), 0, 0, false, false)
	formHostDetails.AddButton("Edit Groups", func() {
		formEditGroups.Clear(true)
		initFormEditGroups()
		pages.SwitchToPage("Edit Groups")
	})

	var labels string
	for key, value := range hostDetails.Node_labels {
		if labels != "" {
			labels = labels + "\n"
		}
		labels = labels + key + ": " + value
	}
	formHostDetails.AddTextView("Node Labels: ", labels, 0, 0, false, false)
	formHostDetails.AddButton("Edit Node Labels", func() {
		initFormEditGroups()
		pages.SwitchToPage("Edit Groups")
	})
}

func initFormEditGroups() {
	var newGroups []string
	checkedMap := map[string]string{}
	for _, group := range hostDetails.Groups {
		checkedMap[group] = "checked"
	}

	formEditGroups.SetTitle("Edit Group").SetBorder(true)
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
		for key, _ := range checkedMap {
			newGroups = append(newGroups, key)
		}
		hostDetails.Groups = newGroups
		writeBackHostDetails()

		formHostDetails.Clear(true)
		initFormHostDetails(hostDetails.Hostname)
		pages.SwitchToPage("Edit Inventory")
	})

	formEditGroups.AddButton("Cancel", func() {
		pages.SwitchToPage("Edit Inventory")
	})
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
		inventory.All.Children.Kube_control_plane.Hosts[hostDetails.Hostname] = make(map[interface{}]interface{})
	} else {
		delete(inventory.All.Children.Kube_control_plane.Hosts, hostDetails.Hostname)
	}
	if slices.Contains(hostDetails.Groups, "kube_node") {
		inventory.All.Children.Kube_node.Hosts[hostDetails.Hostname] = make(map[interface{}]interface{})
	} else {
		delete(inventory.All.Children.Kube_node.Hosts, hostDetails.Hostname)
	}
	if slices.Contains(hostDetails.Groups, "etcd") {
		inventory.All.Children.Etcd.Hosts[hostDetails.Hostname] = make(map[interface{}]interface{})
	} else {
		delete(inventory.All.Children.Etcd.Hosts, hostDetails.Hostname)
	}
	if slices.Contains(hostDetails.Groups, "calico_rr") {
		inventory.All.Children.Calico_rr.Hosts[hostDetails.Hostname] = make(map[interface{}]interface{})
	} else {
		delete(inventory.All.Children.Calico_rr.Hosts, hostDetails.Hostname)
	}

	inventory.All.Hosts[hostDetails.Hostname] = h
}
