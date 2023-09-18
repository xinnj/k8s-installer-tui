package main

import (
	"fmt"
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
	formDeployCluster := tview.NewForm()
	formDeployCluster.SetTitle("Deploy Cluster").SetBorder(true)

	var rootPassword string
	formDeployCluster.AddPasswordField("Root password of each node: ", "", 0, '*', func(text string) {
		rootPassword = text
	})

	inventoryContentByte, err := yaml.Marshal(&inventory)
	check(err)
	inventoryContentString := string(inventoryContentByte)
	formDeployCluster.AddTextArea("Inventory file: ", inventoryContentString, 0, 20, 0, func(text string) {
		inventoryContentString = text
	})

	formDown := tview.NewForm()

	formDown.AddButton("Deploy Cluster", func() {
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

		saveInventory()

		err = copySshKeyToNode(rootPassword)
		if err != nil {
			return
		}

		defaultVarsFile := filepath.Join(projectPath, "default-vars.yaml")
		data, err := yaml.Marshal(&appConfig.Default_vars)
		check(err)
		err = os.WriteFile(defaultVarsFile, data, 0644)
		check(err)

		initFlexSetupCluster(true)
		pages.SwitchToPage("Setup Cluster")
	})

	formDown.AddButton("Cancel", func() {
		pages.SwitchToPage("Mirror")
	})

	formDown.AddButton("Quit", func() {
		showQuitModal("Deploy Cluster")
	})

	flexDeployCluster.SetDirection(tview.FlexRow).
		AddItem(formDeployCluster, 0, 1, true).
		AddItem(formDown, 3, 1, false)

}
