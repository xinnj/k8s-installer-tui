package main

import (
	"fmt"
	"github.com/rivo/tview"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
	"io"
	"math"
	"os"
	"strings"
)

type AccessMethodType struct {
	new   string
	exist string
}

var accessMethodsType = AccessMethodType{
	new:   "Create a new SSH key and copy to each node",
	exist: "Use existing SSH key",
}
var accessMethod = ""
var sshKeyFile = ""

func copyFile(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	return os.Chmod(dst, mode)
}

func checkSshKey() (errorNodes []string) {
	if sshKeyFile != defaultSshKeyfile {
		err := copyFile(sshKeyFile, defaultSshKeyfile, 0600)
		if err != nil {
			showErrorModal("Can't access SSH key file: "+sshKeyFile,
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Deploy Cluster")
				})
			return
		}
	}

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

	var hostIps []string
	for _, host := range inventory.All.Hosts {
		hostIps = append(hostIps, host.Ansible_host)
	}

	for i := 0; i < groups; i++ {
		start := i * maxConcurrency
		end := int(math.Min(float64(numHosts-1), float64((i+1)*maxConcurrency-1)))

		for j := start; j <= end; j++ {
			go func(ip string, resultCh chan copyResult) {
				cmdString := fmt.Sprintf("ssh -i \"%s\" -o StrictHostKeyChecking=no -o ConnectTimeout=5 -o PasswordAuthentication=no -p 22 root@%s id",
					defaultSshKeyfile, ip)
				_, err := execCommand(cmdString, 0, false)
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

	_, err := os.Stat(defaultSshKeyfile)
	if err != nil {
		execCommandAndCheck("rm -f \""+defaultSshKeyfile+"\"; ssh-keygen -q -N '' -f \""+defaultSshKeyfile+"\"", 0, inContainer)
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
					rootPassword, defaultSshKeyfile, ip)
				_, err := execCommand(cmdString, 0, inContainer)
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
	formUp := tview.NewForm()
	if setupNewCluster {
		formUp.SetTitle("Create New Cluster").SetBorder(true)
	} else {
		formUp.SetTitle("Modify Cluster").SetBorder(true)
		accessMethod = accessMethodsType.new
	}

	if accessMethod == "" {
		_, err := os.Stat(defaultSshKeyfile)
		if err != nil {
			// not found
			accessMethod = accessMethodsType.new
		} else {
			accessMethod = accessMethodsType.exist
		}
	}

	accessMethods := []string{
		accessMethodsType.new,
		accessMethodsType.exist,
	}
	initialOption := slices.Index(accessMethods, accessMethod)
	formUp.AddDropDown("How to access cluster nodes: ", accessMethods, initialOption, func(option string, optionIndex int) {
		if accessMethod != option {
			accessMethod = option
			flexDeployCluster.Clear()
			initFlexDeployCluster()
		}
	})

	var rootPassword string
	if accessMethod == accessMethodsType.new {
		formUp.
			AddPasswordField("Root password: ", "", 0, '*', func(text string) {
				rootPassword = text
			})
	} else {
		if sshKeyFile == "" {
			sshKeyFile = defaultSshKeyfile
		}
		formUp.
			AddInputField("SSH key file: ", sshKeyFile, 0, nil, func(text string) {
				sshKeyFile = strings.Trim(text, " ")
			})
	}

	inventoryContentByte, err := yaml.Marshal(&inventory)
	check(err)
	inventoryContentString := string(inventoryContentByte)
	formUp.AddTextArea("Inventory file: ", inventoryContentString, 0, 10, 0, func(text string) {
		inventoryContentString = text
	})

	for k, v := range appConfig.Default_vars {
		extraVars[k] = v
	}
	extraVarsContentByte, err := yaml.Marshal(&extraVars)
	check(err)
	extraVarsContentString := string(extraVarsContentByte)
	formUp.AddTextArea("Extra vars: ", extraVarsContentString, 0, 10, 0, func(text string) {
		extraVarsContentString = text
	})

	formDown := tview.NewForm()

	formDown.AddButton("Start", func() {
		if accessMethod == accessMethodsType.new {
			if rootPassword == "" {
				showErrorModal("Please provide root password of each node.",
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Deploy Cluster")
					})
				return
			}
		} else {
			if sshKeyFile == "" {
				showErrorModal("Please provide SSH key file.",
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Deploy Cluster")
					})
				return
			} else {
				_, err := os.Stat(sshKeyFile)
				if err != nil {
					// not found
					showErrorModal("Can't find the SSH key file: "+sshKeyFile,
						func(buttonIndex int, buttonLabel string) {
							pages.SwitchToPage("Deploy Cluster")
						})
					return
				}
			}
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

		var extraVarsContent map[string]any
		err = yaml.Unmarshal([]byte(extraVarsContentString), &extraVarsContent)
		if err != nil {
			showErrorModal("Format of extra vars is wrong.",
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Deploy Cluster")
				})
			return
		}
		extraVars = extraVarsContent

		saveInventory()

		if setupNewCluster {
			initLog("create-cluster-")
		} else {
			initLog("resize-cluster-")
		}

		if accessMethod == accessMethodsType.exist {
			// Check SSH key
			ch := make(chan bool)
			go func() {
				modalCheckSshKey := tview.NewModal().SetText("Check connection to each node using existing SSH key...")
				pages.AddPage("Check SSH Key", modalCheckSshKey, true, true)
				app.ForceDraw()
				ch <- true
			}()

			errorNodes := checkSshKey()

			// Wait until modal draw finish
			<-ch

			if len(errorNodes) > 0 {
				showErrorModal(fmt.Sprintf("Can't connect these nodes %v \n"+
					"Please make sure port 22 of the host is accessible, and SSH key is correct.", errorNodes),
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Deploy Cluster")
					})
				return
			}
		} else {
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
				showErrorModal(fmt.Sprintf("Can't copy SSH key to these nodes %v \n"+
					"Please make sure port 22 of the host is accessible, and root password is correct.", errorNodes),
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Deploy Cluster")
					})
				return
			}
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
		showQuitModal()
	})

	flexDeployCluster.SetDirection(tview.FlexRow).
		AddItem(formUp, 0, 1, true).
		AddItem(formDown, 3, 1, false)

}
