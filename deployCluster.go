package main

import (
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
	"math"
	"os"
	"os/exec"
	"path/filepath"
)

func copySshKeyToNode(rootPassword string) (errorNodes []string) {
	const maxConcurrency = 10

	type copyResult struct {
		ip         string
		successful bool
	}

	numHosts := len(inventory.All.Hosts)
	if numHosts == 0 {
		return
	}

	resultCh := make(chan copyResult)

	groups := int(math.Ceil(float64(numHosts) / maxConcurrency))

	keyFile = filepath.Join(projectPath, "ansible-key")

	_, err := os.Stat(keyFile)
	if err != nil {
		execCommand("mkdir -p /root/.ssh", 0)
		execCommand("ssh-keygen -q -N '' -f \""+keyFile+"\"", 0)
	}

	var hostIps []string
	for _, host := range inventory.All.Hosts {
		hostIps = append(hostIps, host.Ansible_host)
	}

	for i := 0; i < groups; i++ {
		start := i * maxConcurrency
		end := int(math.Min(float64(numHosts-1), float64((i+1)*maxConcurrency-1)))

		for j := start; j <= end; j++ {
			go func(ip string, resultCh chan copyResult) {
				cmdString := fmt.Sprintf("echo \"%s\" | sshpass ssh-copy-id -i \"%s\" -o StrictHostKeyChecking=no -o ConnectTimeout=5 -p 22 root@%s",
					rootPassword, keyFile, ip)
				cmd := exec.Command("/bin/sh", "-c", cmdString)

				_, err := cmd.CombinedOutput()
				if err != nil {
					resultCh <- copyResult{ip: ip, successful: false}
				} else {
					resultCh <- copyResult{ip: ip, successful: true}
				}
			}(hostIps[j], resultCh)
		}

		var result copyResult
		for j := start; j <= end; j++ {
			result = <-resultCh
			if !result.successful {
				errorNodes = append(errorNodes, result.ip)
			}
		}
	}

	return errorNodes
}

func initFlexDeployCluster() {
	flexUp := tview.NewFlex()
	if setupNewCluster {
		flexUp.SetTitle("Create New Cluster").SetBorder(true)
	} else {
		flexUp.SetTitle("Modify Cluster").SetBorder(true)
	}

	var rootPassword string
	formPassword := tview.NewForm().
		AddPasswordField("Root password of each node: ", "", 0, '*', func(text string) {
			rootPassword = text
		})

	inventoryContentByte, err := yaml.Marshal(&inventory)
	check(err)
	inventoryContentString := string(inventoryContentByte)
	textInventory := tview.NewTextArea()
	style := tcell.Style{}
	style = style.Background(tcell.ColorBlue)
	textInventory.SetTextStyle(style)
	textInventory.
		SetLabel("Inventory file: ").
		SetText(inventoryContentString, false).
		SetChangedFunc(func() {
			inventoryContentString = textInventory.GetText()
		})

	flexUp.SetDirection(tview.FlexRow).
		AddItem(formPassword, 4, 1, true).
		AddItem(textInventory, 0, 1, false)

	formDown := tview.NewForm()

	formDown.AddButton("Start", func() {
		if rootPassword == "" {
			showErrorModal("Please provide root password of each node.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Deploy Cluster")
				})
			return
		}

		var inventoryContent Inventory
		err := yaml.Unmarshal([]byte(inventoryContentString), &inventoryContent)
		if err != nil {
			showErrorModal("Format of inventory file is wrong.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Deploy Cluster")
				})
			return
		}
		inventory = inventoryContent

		for k, v := range appConfig.Default_vars {
			extraVars[k] = v
		}

		saveInventory()

		if setupNewCluster {
			initLog("create-cluster-")
		} else {
			initLog("modify-cluster-")
		}

		// Copy SSH key
		ch := make(chan bool)
		go func() {
			modalCopySshKey := tview.NewModal().SetText("Copy SSH key to each node...")
			pages.AddPage("Copy SSH Key", modalCopySshKey, true, true)
			app.ForceDraw()
			ch <- true
		}()

		errorNodes := copySshKeyToNode(rootPassword)

		// Wait until modal draw finish
		<-ch

		if len(errorNodes) > 0 {
			showErrorModal(fmt.Sprintf("Can't copy SSH key to these hosts %v \n"+
				"Please make sure port 22 of the host is accessible, and root password is correct.", errorNodes),
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Deploy Cluster")
				})
			return
		}

		initFlexSetupCluster(true)
		pages.SwitchToPage("Setup Cluster")
	})

	formDown.AddButton("Back", func() {
		if setupNewCluster {
			pages.SwitchToPage("Mirror")
		} else {
			pages.SwitchToPage("Edit Hosts")
		}
	})

	formDown.AddButton("Quit", func() {
		showQuitModal("Deploy Cluster")
	})

	flexDeployCluster.SetDirection(tview.FlexRow).
		AddItem(flexUp, 0, 1, true).
		AddItem(formDown, 3, 1, false)

}
