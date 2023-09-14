package main

import (
	"github.com/rivo/tview"
)

var haMode string
var vip string

func initFlexHaMode() {
	formHaMode := tview.NewForm()
	formHaMode.SetTitle("Set HA Mode").SetBorder(true)

	if haMode == "" {
		if inventory.All.Vars["loadbalancer_apiserver_localhost"] == nil || inventory.All.Vars["loadbalancer_apiserver_localhost"].(bool) {
			haMode = "localhost loadbalancing"
		} else {
			haMode = "kube-vip"
			vip = inventory.All.Vars["kube_vip_address"].(string)
		}
	}

	formHaMode.AddCheckbox("localhost loadbalancing: ", haMode == "localhost loadbalancing", func(checked bool) {
		if checked {
			haMode = "localhost loadbalancing"
			flexHaMode.Clear()
			initFlexHaMode()
		} else {
			haMode = "kube-vip"
			flexHaMode.Clear()
			initFlexHaMode()
		}
	})

	formHaMode.AddCheckbox("kube-vip: ", haMode == "kube-vip", func(checked bool) {
		if checked {
			haMode = "kube-vip"
			flexHaMode.Clear()
			initFlexHaMode()
		} else {
			haMode = "localhost loadbalancing"
			flexHaMode.Clear()
			initFlexHaMode()
		}
	})

	if haMode == "kube-vip" {
		formHaMode.AddInputField("VIP: ", vip, 0, nil, func(text string) {
			vip = text
		})
	}

	formDown := tview.NewForm()

	formDown.AddButton("Save & Next", func() {
		if haMode == "localhost loadbalancing" {
			inventory.All.Vars["loadbalancer_apiserver_localhost"] = true
			delete(inventory.All.Vars, "kube_vip_enabled")
			delete(inventory.All.Vars, "kube_vip_controlplane_enabled")
			delete(inventory.All.Vars, "kube_vip_arp_enabled")
			delete(inventory.All.Vars, "kube_proxy_strict_arp")
			delete(inventory.All.Vars, "kube_vip_lb_enable")
			delete(inventory.All.Vars, "loadbalancer_apiserver")
			delete(inventory.All.Vars, "kube_vip_address")
		} else {
			if vip == "" {
				showErrorModal("Please provide VIP.",
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("HA Mode")
					})
				return
			} else {
				inventory.All.Vars["loadbalancer_apiserver_localhost"] = false
				inventory.All.Vars["kube_vip_enabled"] = true
				inventory.All.Vars["kube_vip_controlplane_enabled"] = true
				inventory.All.Vars["kube_vip_arp_enabled"] = true
				inventory.All.Vars["kube_proxy_strict_arp"] = true
				inventory.All.Vars["kube_vip_lb_enable"] = true
				inventory.All.Vars["kube_vip_address"] = vip
				apiServer := map[string]string{"address": vip, "port": "6443"}
				inventory.All.Vars["loadbalancer_apiserver"] = apiServer
			}
		}

		saveInventory()

		flexMirror.Clear()
		initFlexMirror()
		pages.SwitchToPage("Mirror")
	})

	formDown.AddButton("Cancel", func() {
		haMode = ""
		vip = ""
		pages.SwitchToPage("Features")
	})

	flexHaMode.SetDirection(tview.FlexRow).
		AddItem(formHaMode, 0, 1, true).
		AddItem(formDown, 3, 1, false)
}
