package main

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

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

	textView := tview.NewTextView().
		SetTextAlign(tview.AlignLeft).
		SetTextColor(tcell.ColorDarkRed).
		SetText("\nKeyboard Usage:\nTab / Shift-Tab: move focus inside a block\nCtrl-N / Ctrl-P: move focus among blocks")
	textView.SetBorder(false)

	flexSetupMode.SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(formSetupMode, 5, 1, true).
		AddItem(textView, 0, 1, false).
		AddItem(nil, 0, 1, false)
}
