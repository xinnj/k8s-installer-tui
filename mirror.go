package main

import (
	"github.com/gdamore/tcell/v2"
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
				if extraVars[k] != nil {
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
			execCommandAndCheck("cp -f "+filepath.Join(projectPath, "group_vars/all/offline.yml")+" "+mirrorFile, 0, false)

			execCommandAndCheck("sed -i -E '/# .*\\{\\{ files_repo/s/^# //g' "+mirrorFile, 0, false)

			for k, v := range mirrors {
				extraVars[k] = v
			}
		} else {
			os.Remove(mirrorFile)

			for k := range mirrors {
				delete(extraVars, k)
			}
		}

		saveInventory()
		flexDeployCluster.Clear()
		initFlexDeployCluster()
		pages.SwitchToPage("Deploy Cluster")
	})

	formDown.AddButton("Back", func() {
		initialValueLoaded = false
		mirrors = nil
		pages.SwitchToPage("Network")
	})

	formDown.AddButton("Quit", func() {
		showQuitModal()
	})

	flexMirror.SetDirection(tview.FlexRow).
		AddItem(formMirror, 0, 1, true).
		AddItem(formDown, 3, 1, false)

	app.SetFocus(formMirror)

	formMirror.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlN || event.Key() == tcell.KeyCtrlP {
			app.SetFocus(formDown)
		}
		return event
	})

	formDown.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlN || event.Key() == tcell.KeyCtrlP {
			app.SetFocus(formMirror)
		}
		return event
	})
}
