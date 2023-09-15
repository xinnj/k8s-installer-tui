package main

import (
	"github.com/rivo/tview"
	"os"
	"path/filepath"
)

const defaultEnableMirror = false

var initialValueLoaded = false
var enableMirror bool
var mirrors map[string]string

func initFlexMirror() {
	formMirror := tview.NewForm()
	formMirror.SetTitle("Public Download Mirror").SetBorder(true)

	mirrorFile := filepath.Join(projectPath, "group_vars/all/mirror.yml")

	if !initialValueLoaded {
		enableMirror = defaultEnableMirror
		_, err := os.Stat(mirrorFile)
		if err == nil {
			enableMirror = true
		}
		initialValueLoaded = true
	}

	formMirror.AddCheckbox("Enable public download mirror: ", enableMirror, func(checked bool) {
		enableMirror = checked
		flexMirror.Clear()
		initFlexMirror()
	})

	if enableMirror {
		if mirrors == nil {
			mirrors = make(map[string]string)

			// Load default values
			for _, item := range appConfig.Default_mirrors {
				for k, v := range item {
					mirrors[k] = v
				}
			}

			// Load values from inventory file
			for k, v := range mirrors {
				if inventory.All.Vars[k] != nil {
					mirrors[k] = v
				}
			}
		}

		// To order the display
		for _, item := range appConfig.Default_mirrors {
			for k := range item {
				formMirror.AddInputField(k+": ", mirrors[k], 0, nil, func(text string) {
					mirrors[k] = text
				})
			}
		}
	}

	formDown := tview.NewForm()

	formDown.AddButton("Save & Next", func() {
		if enableMirror {
			execCommand("cp -f "+filepath.Join(projectPath, "group_vars/all/offline.yml")+" "+mirrorFile, 0)

			execCommand("sed -i -E '/# .*\\{\\{ files_repo/s/^# //g' "+mirrorFile, 0)

			for k, v := range mirrors {
				inventory.All.Vars[k] = v
			}
		} else {
			err := os.Remove(mirrorFile)
			check(err)

			for k := range mirrors {
				delete(inventory.All.Vars, k)
			}
		}

		saveInventory()
		flexDeployCluster.Clear()
		initFlexDeployCluster()
		pages.SwitchToPage("Deploy Cluster")
	})

	formDown.AddButton("Cancel", func() {
		initialValueLoaded = false
		mirrors = nil
		pages.SwitchToPage("HA Mode")
	})

	flexMirror.SetDirection(tview.FlexRow).
		AddItem(formMirror, 0, 1, true).
		AddItem(formDown, 3, 1, false)
}
