package main

// saveConfig.go is the last page of --create mode. It collects the output path
// and the SSH credentials to record, then writes an install config. No SSH
// connection, key copy, or playbook run happens here.

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/exp/slices"
)

const (
	saveAccessPassword    = "SSH password"
	saveAccessExistingKey = "Use an existing SSH key"
)

var (
	saveConfigInitialized bool
	saveConfigPath        string
	saveSSHUser           string
	saveSSHPort           string
	saveSSHUserPrefill    string
	saveSSHPortPrefill    string
	saveAccessMethod      string
	saveSSHPassword       string
	saveSSHKeyFile        string
	saveSudoPassword      string
)

func resetSaveConfigState() {
	saveConfigInitialized = false
	saveConfigPath = ""
	saveSSHUser = ""
	saveSSHPort = ""
	saveSSHUserPrefill = ""
	saveSSHPortPrefill = ""
	saveAccessMethod = ""
	saveSSHPassword = ""
	saveSSHKeyFile = ""
	saveSudoPassword = ""
}

// initSaveConfigState fills the page once, so rebuilding it for the credential
// dropdown does not discard what the operator has typed.
func initSaveConfigState() {
	if saveConfigInitialized {
		return
	}
	saveConfigInitialized = true

	if saveConfigPath == "" {
		saveConfigPath = createConfigPath
	}
	if saveConfigPath == "" {
		saveConfigPath = defaultCreateConfigPath
	}

	if saveAccessMethod == "" {
		if saveSSHKeyFile == "" {
			if _, err := os.Stat(sshKeyPath()); err == nil {
				saveSSHKeyFile = sshKeyPath()
			}
		}
		if saveSSHKeyFile != "" {
			saveAccessMethod = saveAccessExistingKey
		} else {
			saveAccessMethod = saveAccessPassword
		}
	}
}

// refreshSaveSSHDefaults follows the live inventory unless the operator changed
// the field on this page. Re-entering after editing the hosts page must not
// export the previous inventory's user or port.
func refreshSaveSSHDefaults() {
	inventoryUser := strings.TrimSpace(inventory.All.Vars.Ansible_user)
	if inventoryUser == "" {
		inventoryUser = strings.TrimSpace(nodeSshUser)
	}
	if inventoryUser == "" {
		if current, err := user.Current(); err == nil {
			inventoryUser = current.Username
		}
	}
	if saveSSHUser == "" || saveSSHUser == saveSSHUserPrefill {
		saveSSHUser = inventoryUser
	}
	saveSSHUserPrefill = inventoryUser

	inventoryPort := strings.TrimSpace(inventory.All.Vars.Ansible_port)
	if inventoryPort == "" {
		inventoryPort = strings.TrimSpace(nodeSshPort)
	}
	if inventoryPort == "" {
		inventoryPort = "22"
	}
	if saveSSHPort == "" || saveSSHPort == saveSSHPortPrefill {
		saveSSHPort = inventoryPort
	}
	saveSSHPortPrefill = inventoryPort
}

func initSaveConfigDefaults() {
	initSaveConfigState()
	refreshSaveSSHDefaults()
}

func initFlexSaveConfig() {
	initSaveConfigDefaults()

	formUp := tview.NewForm()
	formUp.SetTitle("Save Installation Config").SetBorder(true)

	formUp.AddInputField("Output file: ", saveConfigPath, 0, nil, func(text string) {
		saveConfigPath = text
	})
	formUp.AddInputField("SSH user: ", saveSSHUser, 0, nil, func(text string) {
		saveSSHUser = text
	})
	formUp.AddInputField("SSH port: ", saveSSHPort, 0, nil, func(text string) {
		saveSSHPort = text
	})

	methods := []string{saveAccessPassword, saveAccessExistingKey}
	initial := slices.Index(methods, saveAccessMethod)
	if initial < 0 {
		initial = 0
	}
	formUp.AddDropDown("SSH credentials: ", methods, initial, func(option string, optionIndex int) {
		if saveAccessMethod != option {
			saveAccessMethod = option
			flexSaveConfig.Clear()
			initFlexSaveConfig()
		}
	})

	if saveAccessMethod == saveAccessExistingKey {
		if saveSSHKeyFile == "" {
			saveSSHKeyFile = sshKeyPath()
		}
		keyField := newSshKeyField("SSH key file: ", saveSSHKeyFile, func(text string) {
			saveSSHKeyFile = strings.TrimSpace(text)
		})
		keyField.SetBrowseFunc(func() {
			if path := browseSshKeyFile(); path != "" {
				saveSSHKeyFile = path
				keyField.SetText(path)
			}
		})
		formUp.AddFormItem(keyField)
		formUp.AddPasswordField("Sudo password (non-root user, optional): ", saveSudoPassword, 0, '*', func(text string) {
			saveSudoPassword = text
		})
	} else {
		label := "SSH password: "
		if strings.TrimSpace(saveSSHUser) != "root" {
			label = "SSH / sudo Password: "
		}
		formUp.AddPasswordField(label, saveSSHPassword, 0, '*', func(text string) {
			saveSSHPassword = text
		})
	}

	note := tview.NewTextView().
		SetTextAlign(tview.AlignLeft).
		SetText("No SSH connection, key copy, or installation runs here.\n" +
			"The config is written with mode 0600 and can be run later with --config.")
	note.SetBorder(false)
	note.SetBackgroundColor(tview.Styles.PrimitiveBackgroundColor)

	formDown := tview.NewForm()
	formDown.AddButton("Create Config", func() {
		createConfigFromSavePage()
	})
	formDown.AddButton("Back", func() {
		pages.SwitchToPage("Mirror")
	})
	formDown.AddButton("Quit", func() {
		showQuitModal()
	})

	flexSaveConfig.SetDirection(tview.FlexRow).
		AddItem(formUp, 0, 1, true).
		AddItem(note, 2, 0, false).
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

func saveConfigInput() (createConfigInput, error) {
	sshUser := strings.TrimSpace(saveSSHUser)
	if sshUser == "" {
		return createConfigInput{}, errors.New("Please provide the SSH user.")
	}

	port := 22
	if text := strings.TrimSpace(saveSSHPort); text != "" {
		parsed, err := strconv.Atoi(text)
		if err != nil || parsed < 1 || parsed > 65535 {
			return createConfigInput{}, fmt.Errorf("SSH port %q is not a number between 1 and 65535.", text)
		}
		port = parsed
	}

	in := createConfigInput{
		ProjectPath:    projectPath,
		HostnamePrefix: nodeHostnamePrefix,
		Inventory:      inventory,
		ExtraVars:      extraVars,
		App:            appConfig,
		HAMode:         haMode,
		VIP:            vip,
		CheckConflicts: checkConflicts,
		MirrorEnabled:  enableMirror,
		Mirrors:        mirrors,
		SSHUser:        sshUser,
		SSHPort:        port,
	}

	switch saveAccessMethod {
	case saveAccessExistingKey:
		keyFile := strings.TrimSpace(saveSSHKeyFile)
		if keyFile == "" {
			return createConfigInput{}, errors.New("Please provide the SSH key file.")
		}
		in.SSHKeyFile = keyFile
		in.SudoPassword = saveSudoPassword
	default:
		if saveSSHPassword == "" {
			return createConfigInput{}, errors.New("Please provide the SSH password.")
		}
		in.SSHPassword = saveSSHPassword
	}

	return in, nil
}

func createConfigFromSavePage() {
	path := strings.TrimSpace(saveConfigPath)
	if path == "" {
		showSaveConfigError("Please provide the output file path.")
		return
	}
	abs, err := expandHomePath(path)
	if err != nil {
		showSaveConfigError("Output file: " + err.Error())
		return
	}

	in, err := saveConfigInput()
	if err != nil {
		showSaveConfigError(err.Error())
		return
	}
	cfg, err := buildInstallConfig(in)
	if err != nil {
		showSaveConfigError(err.Error())
		return
	}

	if exists, err := outputFileState(abs); err != nil {
		showSaveConfigError("Output file: " + err.Error())
		return
	} else if exists {
		confirmOverwriteConfig(abs, cfg)
		return
	}
	writeConfigAndFinish(abs, cfg)
}

// outputFileState tells the page whether to ask before replacing the target.
// Anything other than "missing" is an error: a directory or an unreadable path
// must not turn into a rename failure after the operator confirms an overwrite.
func outputFileState(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, fmt.Errorf("%s is a directory, not a file", path)
	}

	return true, nil
}

func showSaveConfigError(text string) {
	showErrorModal(text, func(buttonIndex int, buttonLabel string) {
		pages.SwitchToPage("Save Config")
	})
}

func confirmOverwriteConfig(abs string, cfg *installConfig) {
	modal := tview.NewModal().
		SetText("Overwrite the existing config file?\n" + abs).
		AddButtons([]string{"Overwrite", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			if buttonLabel != "Overwrite" {
				pages.SwitchToPage("Save Config")
				return
			}
			writeConfigAndFinish(abs, cfg)
		})
	pages.AddPage("Overwrite Config", modal, true, true)
	pages.SwitchToPage("Overwrite Config")
}

func writeConfigAndFinish(abs string, cfg *installConfig) {
	resolved, err := writeInstallConfigFile(abs, cfg)
	if err != nil {
		showSaveConfigError("Could not write the config file:\n" + err.Error())
		return
	}

	warning := ""
	if err := chownToRunner(resolved); err != nil {
		warning = "\n\nWarning: could not change the file owner: " + err.Error()
	}
	showConfigSavedModal(resolved, warning)
}

func showConfigSavedModal(path, warning string) {
	modal := tview.NewModal().
		SetText("Config file created:\n" + path + "\n\n" +
			"It contains plaintext SSH credentials and is mode 0600." + warning).
		AddButtons([]string{"Quit"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			app.Stop()
		})
	pages.AddPage("Config Saved", modal, true, true)
	pages.SwitchToPage("Config Saved")
}
