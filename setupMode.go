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
		if createMode {
			// A new --create walk starts from a clean Save Config page.
			resetSaveConfigState()
		}
		setupNewCluster = true
		flexProject.Clear()
		initFlexProject()
		pages.SwitchToPage("Project")
	})

	formSetupMode.AddButton("Add Nodes To Cluster", func() {
		if createMode {
			return
		}
		setupNewCluster = false
		flexProject.Clear()
		initFlexProject()
		pages.SwitchToPage("Project")
	})

	formSetupMode.AddButton("Quit", func() {
		showQuitModal()
	})

	if createMode {
		if index := formSetupMode.GetButtonIndex("Add Nodes To Cluster"); index >= 0 {
			formSetupMode.GetButton(index).SetDisabled(true)
		}
	}

	formSetupMode.SetButtonsAlign(tview.AlignCenter)

	textView := tview.NewTextView().
		SetTextAlign(tview.AlignLeft).
		SetTextColor(tcell.ColorWhiteSmoke).
		SetText("\nKeyboard Usage:\nTab / Shift-Tab: move focus inside a block\nCtrl-N / Ctrl-P: move focus among blocks\n\n" + versionString())
	textView.SetBorder(false)
	textView.SetBackgroundColor(tview.Styles.PrimitiveBackgroundColor)
	textView.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		return action, nil
	})

	flexSetupMode.SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(formSetupMode, 5, 1, true).
		AddItem(nil, 0, 1, false).
		AddItem(textView, 6, 1, false)
}
