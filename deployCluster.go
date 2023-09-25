package main

import (
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
)

func copySshKeyToNode(rootPassword string) error {
	keyFile = filepath.Join(projectPath, "ansible-key")

	_, err := os.Stat(keyFile)
	if err == nil {
		writeLog("Use existing key file: " + keyFile)
	} else {
		execCommand("mkdir -p /root/.ssh", 0)
		execCommand("ssh-keygen -q -N '' -f \""+keyFile+"\"", 0)
		writeLog("Generated key file: " + keyFile)
	}

	for _, host := range inventory.All.Hosts {
		cmdString := fmt.Sprintf("echo \"%s\" | sshpass ssh-copy-id -i \"%s\" -o StrictHostKeyChecking=no -o ConnectTimeout=5 -p 22 root@%s",
			rootPassword, keyFile, host.Ansible_host)
		cmd := exec.Command("/bin/sh", "-c", cmdString)

		_, err := cmd.CombinedOutput()
		if err != nil {
			showErrorModal("Can't copy SSH key to host: "+host.Ansible_host+".\nPlease make sure port 22 of the host is accessible, and root password is correct.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Deploy Cluster")
				})
			return err
		}
		writeLog("Copied key to host: " + host.Ansible_host)
	}
	return nil
}

func initFlexDeployCluster() {
	flexUp := tview.NewFlex()
	flexUp.SetTitle("Ready To Go").SetBorder(true)

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

		initLog("deploy-cluster-")

		err = copySshKeyToNode(rootPassword)
		if err != nil {
			return
		}

		initFlexSetupCluster(true)
		pages.SwitchToPage("Setup Cluster")
	})

	formDown.AddButton("Back", func() {
		pages.SwitchToPage("Mirror")
	})

	formDown.AddButton("Quit", func() {
		showQuitModal("Deploy Cluster")
	})

	flexDeployCluster.SetDirection(tview.FlexRow).
		AddItem(flexUp, 0, 1, true).
		AddItem(formDown, 3, 1, false)

}
