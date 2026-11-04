package main

// Tests for the Setup Mode restrictions in --create mode.

import (
	"testing"

	"github.com/rivo/tview"
)

func TestSetupModeDisablesAddNodesInCreateMode(t *testing.T) {
	oldCreate := createMode
	t.Cleanup(func() {
		createMode = oldCreate
		flexSetupMode.Clear()
	})

	tests := []struct {
		name     string
		create   bool
		disabled bool
	}{
		{name: "normal", create: false, disabled: false},
		{name: "create mode", create: true, disabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createMode = tt.create
			flexSetupMode.Clear()
			initFlexSetupMode()

			form, ok := flexSetupMode.GetItem(1).(*tview.Form)
			if !ok {
				t.Fatalf("item 1 = %T, want *tview.Form", flexSetupMode.GetItem(1))
			}
			index := form.GetButtonIndex("Add Nodes To Cluster")
			if index < 0 {
				t.Fatal("Add Nodes button is missing")
			}
			if got := form.GetButton(index).IsDisabled(); got != tt.disabled {
				t.Fatalf("Add Nodes disabled = %v, want %v", got, tt.disabled)
			}
		})
	}
}
