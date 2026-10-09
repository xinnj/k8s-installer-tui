package main

// Tests for the composite form item used by the Deploy Cluster page. They run
// without root, podman or a terminal by rendering into a tcell simulation
// screen.

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func simRows(sim tcell.SimulationScreen) []string {
	cells, width, height := sim.GetContents()
	rows := make([]string, height)
	for y := 0; y < height; y++ {
		var b strings.Builder
		for x := 0; x < width; x++ {
			cell := cells[y*width+x]
			if len(cell.Runes) > 0 {
				b.WriteRune(cell.Runes[0])
			} else {
				b.WriteRune(' ')
			}
		}
		rows[y] = strings.TrimRight(b.String(), " ")
	}
	return rows
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestSshKeyFieldInDeployCluster renders the whole Deploy Cluster page in
// "Use existing SSH key" mode and checks that the Browse button shares the row
// of the SSH key path without disturbing the form layout, and that Tab walks
// from the path to the button and on to the next form item.
func TestSshKeyFieldInDeployCluster(t *testing.T) {
	accessMethod = accessMethodsType.exist
	sshKeyFile = "/root/.ssh/id_rsa"

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(100, 45)

	app = tview.NewApplication().SetScreen(sim)
	initFlexDeployCluster()
	app.SetRoot(flexDeployCluster, true)

	go func() {
		if err := app.Run(); err != nil {
			t.Error(err)
		}
	}()
	defer app.Stop()

	var rows []string
	waitFor(t, "the deploy page to be drawn", func() bool {
		rows = simRows(sim)
		for _, row := range rows {
			if strings.Contains(row, "SSH key file:") {
				return true
			}
		}
		return false
	})

	form, ok := flexDeployCluster.GetItem(0).(*tview.Form)
	if !ok {
		t.Fatal("the page does not start with a form")
	}
	field, ok := form.GetFormItem(1).(*sshKeyField)
	if !ok {
		t.Fatalf("form item 1 is %T, want *sshKeyField", form.GetFormItem(1))
	}

	keyRow, dropdownRow, inventoryRow := -1, -1, -1
	for i, row := range rows {
		switch {
		case strings.Contains(row, "SSH key file:"):
			keyRow = i
		case strings.Contains(row, "How to access cluster nodes:"):
			dropdownRow = i
		case strings.Contains(row, "Inventory file:"):
			inventoryRow = i
		}
	}
	if keyRow < 0 || dropdownRow < 0 || inventoryRow < 0 {
		t.Fatalf("rows not found: key=%d dropdown=%d inventory=%d\n%s",
			keyRow, dropdownRow, inventoryRow, strings.Join(rows, "\n"))
	}

	if !strings.Contains(rows[keyRow], sshKeyFile) {
		t.Errorf("SSH key row does not show the path: %q", rows[keyRow])
	}
	if !strings.Contains(rows[keyRow], "Browse") {
		t.Errorf("Browse button is not on the SSH key row: %q", rows[keyRow])
	}

	// A form separates its items with one blank row, so the composite must
	// occupy exactly one item slot: dropdown, blank, key field, blank, inventory.
	if keyRow != dropdownRow+2 || inventoryRow != keyRow+2 {
		t.Errorf("unexpected item spacing: dropdown=%d key=%d inventory=%d, want a stride of 2",
			dropdownRow, keyRow, inventoryRow)
	}

	// The path field has to start where the other fields start.
	pathCol := strings.Index(rows[keyRow], sshKeyFile)
	dropdownCol := strings.Index(rows[dropdownRow], "Use existing")
	if pathCol != dropdownCol {
		t.Errorf("field column misaligned: dropdown field starts at %d, key path at %d", dropdownCol, pathCol)
	}

	// Tab: path -> Browse -> next item, and Backtab: Browse -> path.
	app.SetFocus(field)
	waitFor(t, "focus on the path field", func() bool { return field.input.HasFocus() })

	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	waitFor(t, "Tab to move into the Browse button", func() bool { return field.button.HasFocus() })

	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	next := form.GetFormItem(2)
	waitFor(t, "Tab to leave the row for the next form item", func() bool { return next.HasFocus() })

	app.SetFocus(field)
	waitFor(t, "focus back on the path field", func() bool { return field.input.HasFocus() })
	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	waitFor(t, "Tab into the Browse button again", func() bool { return field.button.HasFocus() })
	sim.InjectKey(tcell.KeyBacktab, 0, tcell.ModNone)
	waitFor(t, "Backtab from Browse back to the path field", func() bool { return field.input.HasFocus() })
}
