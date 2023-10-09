package main

import (
	"fmt"
	"github.com/rivo/tview"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
	"net/netip"
	"os"
	"path/filepath"
)

func initFlexNetwork() {
	formNetwork := tview.NewForm()
	formNetwork.SetTitle("Network").SetBorder(true)

	clusterVars := make(map[string]any)
	data, err := os.ReadFile(filepath.Join(projectPath, "group_vars/k8s_cluster/k8s-cluster.yml"))
	check(err)
	err = yaml.Unmarshal(data, &clusterVars)
	check(err)

	var serviceCidr, podCidr string
	if extraVars["kube_service_addresses"] == nil {
		serviceCidr = clusterVars["kube_service_addresses"].(string)
	} else {
		serviceCidr = extraVars["kube_service_addresses"].(string)
	}
	if extraVars["kube_pods_subnet"] == nil {
		podCidr = clusterVars["kube_pods_subnet"].(string)
	} else {
		podCidr = extraVars["kube_pods_subnet"].(string)
	}

	formNetwork.AddInputField("Service CIDR: ", serviceCidr, 0, nil, func(text string) {
		serviceCidr = text
	})
	formNetwork.AddInputField("Pod CIDR: ", podCidr, 0, nil, func(text string) {
		podCidr = text
	})

	checkConflicts := true
	formNetwork.AddCheckbox("Check IP Conflicts: ", checkConflicts, func(checked bool) {
		checkConflicts = checked
	})

	formDown := tview.NewForm()

	formDown.AddButton("Save & Next", func() {
		prefixService, err := netip.ParsePrefix(serviceCidr)
		if err != nil {
			showErrorModal("Service CIDR is wrong.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Network")
				})
			return
		}

		prefixPod, err := netip.ParsePrefix(podCidr)
		if err != nil {
			showErrorModal("Pod CIDR is wrong.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Network")
				})
			return
		}

		if prefixService.Overlaps(prefixPod) {
			showErrorModal("Service CIDR and Pod CIDR are overlapped.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Network")
				})
			return
		}

		if checkConflicts {
			ch := make(chan bool)
			go func() {
				modalCheckIp := tview.NewModal().SetText("Checking IP conflicts...")
				pages.AddPage("Check IP", modalCheckIp, true, true)
				app.ForceDraw()
				ch <- true
			}()

			serviceIps, err := Hosts(serviceCidr)
			check(err)
			podIps, err := Hosts(podCidr)
			check(err)
			ips := []string{serviceIps[0], serviceIps[len(serviceIps)/4], serviceIps[len(serviceIps)/2], serviceIps[len(serviceIps)*3/4], serviceIps[len(serviceIps)-1],
				podIps[0], podIps[len(podIps)/4], podIps[len(podIps)/2], podIps[len(podIps)*3/4], podIps[len(podIps)-1]}
			slices.Sort(ips)
			ips = slices.Compact(ips)
			reachableIPs, _ := groupPing(ips)

			// Wait until modal draw finish
			<-ch

			if len(reachableIPs) != 0 {
				slices.Sort(reachableIPs)
				showErrorModal(fmt.Sprintf("Potential IP conflicts detected.\n%v", reachableIPs),
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Network")
					})
				return
			}
		}

		extraVars["kube_service_addresses"] = serviceCidr
		extraVars["kube_pods_subnet"] = podCidr
		saveInventory()

		flexMirror.Clear()
		initFlexMirror()
		pages.SwitchToPage("Mirror")
	})

	formDown.AddButton("Back", func() {
		pages.SwitchToPage("HA Mode")
	})

	formDown.AddButton("Quit", func() {
		showQuitModal()
	})

	flexNetwork.SetDirection(tview.FlexRow).
		AddItem(formNetwork, 0, 1, true).
		AddItem(formDown, 3, 1, false)
}
