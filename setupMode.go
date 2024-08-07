package main

import "github.com/rivo/tview"

func initFlexSetupMode() {
	formSetupMode := tview.NewForm()
	formSetupMode.SetTitle("Setup Mode")
	formSetupMode.SetBorder(true)

	formSetupMode.AddButton("Create / Update Cluster", func() {
		setupNewCluster = true
		flexProject.Clear()
		initFlexProject()
		pages.SwitchToPage("Project")
	})

	formSetupMode.AddButton("Add Nodes To Cluster", func() {
		setupNewCluster = false
		flexProject.Clear()
		initFlexProject()
		pages.SwitchToPage("Project")
	})

	formSetupMode.AddButton("Quit", func() {
		showQuitModal()
	})

	formSetupMode.SetButtonsAlign(tview.AlignCenter)

	flexSetupMode.SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(formSetupMode, 5, 1, true).
		AddItem(nil, 0, 1, false)
}
