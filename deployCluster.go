package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/AEROGU/tvchooser"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
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
var containerSshKeyFile = "/data/idocluster/ansible-key"

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

func checkSshKey() (errorNodes []string, err error) {
	if sshKeyFile != defaultSshKeyfile {
		err := copyFile(sshKeyFile, defaultSshKeyfile, 0600)
		if err != nil {
			showErrorModal("Can't access SSH key file: "+sshKeyFile,
				func(buttonIndex int, buttonLabel string) {
					pages.SwitchToPage("Deploy Cluster")
				})
			return nil, err
		}
	}

	const maxConcurrency = 10

	type copyResult struct {
		host       string
		successful bool
	}

	numHosts := len(inventory.All.Hosts)
	if numHosts == 0 {
		return
	}

	resultCh := make(chan copyResult)

	groups := int(math.Ceil(float64(numHosts) / maxConcurrency))

	var hosts []Host
	for _, host := range inventory.All.Hosts {
		hosts = append(hosts, host)
	}

	for i := 0; i < groups; i++ {
		start := i * maxConcurrency
		end := int(math.Min(float64(numHosts-1), float64((i+1)*maxConcurrency-1)))

		for j := start; j <= end; j++ {
			go func(host Host, resultCh chan copyResult) {
				hostname := inventory.All.Vars.Ansible_user + "@" + host.Ansible_host + ":" + inventory.All.Vars.Ansible_port
				cmdString := fmt.Sprintf("ssh -i \"%s\" -o StrictHostKeyChecking=no -o ConnectTimeout=5 -o PasswordAuthentication=no -p %s %s@%s id",
					defaultSshKeyfile, inventory.All.Vars.Ansible_port, inventory.All.Vars.Ansible_user, host.Ansible_host)
				_, err := execCommand(cmdString, 0, false)
				if err != nil {
					resultCh <- copyResult{host: hostname, successful: false}
				} else {
					resultCh <- copyResult{host: hostname, successful: true}
				}
			}(hosts[j], resultCh)
		}

		var result copyResult
		for j := start; j <= end; j++ {
			result = <-resultCh
			if !result.successful {
				errorNodes = append(errorNodes, result.host)
			}
		}
	}

	return errorNodes, nil
}

func errorText(err error, output []byte) string {
	msg := err.Error()
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) > 0 && lines[len(lines)-1] != "" {
		msg += "\n" + lines[len(lines)-1]
	}
	return msg
}

func copySshKeyToNode(sshPassword string) (errorNodes []string, errorMsgs []string) {
	const maxConcurrency = 10

	type copyResult struct {
		host       string
		successful bool
		errMsg     string
	}

	numHosts := len(inventory.All.Hosts)
	if numHosts == 0 {
		return
	}

	resultCh := make(chan copyResult)

	groups := int(math.Ceil(float64(numHosts) / maxConcurrency))

	_, err := os.Stat(defaultSshKeyfile)
	if err != nil {
		// Generate SSH key and change ownership to current user
		execCommandAndCheck(fmt.Sprintf("rm -f \"%s\"; ssh-keygen -q -N '' -f \"%s\"; chown %d:%d \"%s\" \"%s.pub\"", containerSshKeyFile, containerSshKeyFile, os.Getuid(), os.Getgid(), containerSshKeyFile, containerSshKeyFile), 0, inContainer)
	}

	var hosts []Host
	for _, host := range inventory.All.Hosts {
		hosts = append(hosts, host)
	}

	for i := 0; i < groups; i++ {
		start := i * maxConcurrency
		end := int(math.Min(float64(numHosts-1), float64((i+1)*maxConcurrency-1)))

		for j := start; j <= end; j++ {
			go func(host Host, resultCh chan copyResult) {
				hostname := inventory.All.Vars.Ansible_user + "@" + host.Ansible_host + ":" + inventory.All.Vars.Ansible_port
				cmdString := fmt.Sprintf("mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo \"%s\" | sshpass ssh-copy-id -i \"%s\" -o StrictHostKeyChecking=no -o ConnectTimeout=5 -p %s %s@%s",
					sshPassword, containerSshKeyFile, inventory.All.Vars.Ansible_port, inventory.All.Vars.Ansible_user, host.Ansible_host)
				output, err := execCommand(cmdString, 0, inContainer)
				if err != nil {
					resultCh <- copyResult{host: hostname, successful: false, errMsg: errorText(err, output)}
				} else {
					resultCh <- copyResult{host: hostname, successful: true}
				}
			}(hosts[j], resultCh)
		}

		var result copyResult
		for j := start; j <= end; j++ {
			result = <-resultCh
			if !result.successful {
				errorNodes = append(errorNodes, result.host)
				errorMsgs = append(errorMsgs, result.host+"\n"+result.errMsg)
			}
		}
	}

	return errorNodes, errorMsgs
}

// sshKeyField is a form item showing an editable SSH key path and a "Browse"
// button on the same row. tview puts every button of a form into the form's
// own button row at the bottom, so the two controls are combined into a single
// form item here to keep the button next to the field.
type sshKeyField struct {
	*tview.Flex

	input  *tview.InputField
	button *tview.Button
	label  string
}

func newSshKeyField(label, text string, changed func(text string)) *sshKeyField {
	input := tview.NewInputField().
		SetLabel(label).
		SetText(text).
		SetChangedFunc(changed)
	button := tview.NewButton("Browse")

	return &sshKeyField{
		Flex: tview.NewFlex().
			AddItem(input, 0, 1, true).
			AddItem(tview.NewBox(), 1, 0, false).
			AddItem(button, tview.TaggedStringWidth(button.GetLabel())+4, 0, false),
		input:  input,
		button: button,
		label:  label,
	}
}

func (f *sshKeyField) GetLabel() string {
	return f.label
}

func (f *sshKeyField) SetFormAttributes(labelWidth int, labelColor, bgColor, fieldTextColor, fieldBgColor tcell.Color) tview.FormItem {
	f.input.SetFormAttributes(labelWidth, labelColor, bgColor, fieldTextColor, fieldBgColor)
	f.SetBackgroundColor(bgColor)
	return f
}

func (f *sshKeyField) GetFieldWidth() int {
	return 0
}

func (f *sshKeyField) GetFieldHeight() int {
	return 1
}

func (f *sshKeyField) SetDisabled(disabled bool) tview.FormItem {
	f.input.SetDisabled(disabled)
	f.button.SetDisabled(disabled)
	return f
}

// SetFinishedFunc stores the form's handler for leaving this item. Tab moves
// from the key path to the Browse button and from there on to the next form
// item, so the button keeps tview's keyboard navigation.
func (f *sshKeyField) SetFinishedFunc(handler func(key tcell.Key)) tview.FormItem {
	f.input.SetFinishedFunc(func(key tcell.Key) {
		if key == tcell.KeyTab {
			app.SetFocus(f.button)
			return
		}
		handler(key)
	})
	f.button.SetExitFunc(func(key tcell.Key) {
		if key == tcell.KeyBacktab {
			app.SetFocus(f.input)
			return
		}
		handler(key)
	})
	return f
}

func (f *sshKeyField) SetBrowseFunc(handler func()) {
	f.button.SetSelectedFunc(handler)
}

func (f *sshKeyField) SetText(text string) {
	f.input.SetText(text)
}

// browseSshKeyFile opens the file picker and returns the selected path, or ""
// if the user cancelled. The picker has no start directory: the paths given to
// it become entries of its "Favorites" list, which is expanded on open. The
// home directory comes first so that it acts as the default location.
func browseSshKeyFile() string {
	favorites := []string{homePath + "|Home"}
	if sshDir := filepath.Join(homePath, ".ssh"); pathExists(sshDir) {
		favorites = append(favorites, sshDir+"|SSH keys")
	}
	if pathExists(projectPath) {
		favorites = append(favorites, projectPath+"|Cluster project")
	}

	// Hidden files are shown so that keys in hidden directories such as ~/.ssh can be picked.
	return tvchooser.FileChooser(app, true, favorites...)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func initFlexDeployCluster() {
	formUp := tview.NewForm()
	if setupNewCluster {
		formUp.SetTitle("Create New Cluster").SetBorder(true)
	} else {
		formUp.SetTitle("Modify Cluster").SetBorder(true)
		if accessMethod == "" {
			accessMethod = accessMethodsType.new
		}
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

	var sshPassword string
	if accessMethod == accessMethodsType.new {
		formUp.
			AddPasswordField("SSH Password: ", "", 0, '*', func(text string) {
				sshPassword = text
			})
	} else {
		if sshKeyFile == "" {
			sshKeyFile = defaultSshKeyfile
		}
		sshKey := newSshKeyField("SSH key file: ", sshKeyFile, func(text string) {
			sshKeyFile = strings.Trim(text, " ")
		})
		sshKey.SetBrowseFunc(func() {
			if path := browseSshKeyFile(); path != "" {
				sshKeyFile = path
				sshKey.SetText(path)
			}
		})
		formUp.AddFormItem(sshKey)
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
			if sshPassword == "" {
				showErrorModal("Please provide the SSH password for each node.",
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Deploy Cluster")
					})
				return
			}
		} else {
			if sshKeyFile == "" {
				showErrorModal("Please provide the SSH key file.",
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

			errorNodes, err := checkSshKey()
			if err != nil {
				return
			}

			// Wait until modal draw finish
			<-ch

			if len(errorNodes) > 0 {
				showErrorModal(fmt.Sprintf("Can't connect these nodes %v \n"+
					"Please make sure SSH port of the host is accessible, and SSH key is correct.", errorNodes),
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

			errorNodes, errorMsgs := copySshKeyToNode(sshPassword)

			// Wait until modal draw finish
			<-ch

			if len(errorNodes) > 0 {
				showErrorModal(fmt.Sprintf("Can't copy SSH key to these nodes %v \n"+
					"Please make sure SSH port of the host is accessible, and SSH password is correct.\n\n%s",
					errorNodes, strings.Join(errorMsgs, "\n\n")),
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

	app.SetFocus(formUp)

	formUp.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlN || event.Key() == tcell.KeyCtrlP {
			app.SetFocus(formDown)
		}
		return event
	})

	formDown.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlN || event.Key() == tcell.KeyCtrlP {
			app.SetFocus(formUp)
		}
		return event
	})
}
