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
		if extraVars["loadbalancer_apiserver_localhost"] == nil || extraVars["loadbalancer_apiserver_localhost"].(bool) {
			haMode = "localhost loadbalancing"
		} else {
			haMode = "kube-vip"
			vip = extraVars["kube_vip_address"].(string)
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
			extraVars["loadbalancer_apiserver_localhost"] = true
			delete(extraVars, "kube_vip_enabled")
			delete(extraVars, "kube_vip_controlplane_enabled")
			delete(extraVars, "kube_vip_arp_enabled")
			delete(extraVars, "kube_proxy_strict_arp")
			delete(extraVars, "kube_vip_lb_enable")
			delete(extraVars, "loadbalancer_apiserver")
			delete(extraVars, "kube_vip_address")
		} else {
			if vip == "" {
				showErrorModal("Please provide VIP.",
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("HA Mode")
					})
				return
			}

			extraVars["loadbalancer_apiserver_localhost"] = false
			extraVars["kube_vip_enabled"] = true
			extraVars["kube_vip_controlplane_enabled"] = true
			extraVars["kube_vip_arp_enabled"] = true
			extraVars["kube_proxy_strict_arp"] = true
			extraVars["kube_vip_lb_enable"] = true
			extraVars["kube_vip_address"] = vip
			apiServer := map[string]string{"address": vip, "port": "6443"}
			extraVars["loadbalancer_apiserver"] = apiServer
		}

		saveInventory()

		flexNetwork.Clear()
		initFlexNetwork()
		pages.SwitchToPage("Network")
	})

	formDown.AddButton("Back", func() {
		haMode = ""
		vip = ""
		pages.SwitchToPage("Features")
	})

	formDown.AddButton("Quit", func() {
		showQuitModal()
	})

	flexHaMode.SetDirection(tview.FlexRow).
		AddItem(formHaMode, 0, 1, true).
		AddItem(formDown, 3, 1, false)
}
