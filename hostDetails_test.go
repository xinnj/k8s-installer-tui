package main

// Tests for the Edit Groups / Edit Node Labels / Edit Node Taints buttons on
// the Edit Hosts page. They run without root, podman or a terminal.

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// startEditHosts builds the Edit Hosts page for a new project in a simulation
// screen. The caller gets a running application and the simulation screen.
func startEditHosts(t *testing.T) (*tview.Application, tcell.SimulationScreen) {
	return startEditHostsSized(t, Inventory{}, 172, 42)
}

func startEditHostsWith(t *testing.T, original Inventory) (*tview.Application, tcell.SimulationScreen) {
	return startEditHostsSized(t, original, 172, 42)
}

func startEditHostsSized(t *testing.T, original Inventory, width, height int) (*tview.Application, tcell.SimulationScreen) {
	t.Helper()
	snapshotInstallGlobals(t)

	oldPages, oldApp := pages, app
	oldFlexSetup, oldFlexEdit, oldFlexDetails := flexSetupMode, flexEditHosts, flexHostDetails
	oldActions := formHostDetailsActions
	t.Cleanup(func() {
		pages, app = oldPages, oldApp
		flexSetupMode, flexEditHosts, flexHostDetails = oldFlexSetup, oldFlexEdit, oldFlexDetails
		formHostDetailsActions = oldActions
		flexSetupMode.Clear()
		flexEditHosts.Clear()
		flexHostDetails.Clear()
	})

	inventory = testExportInventory()
	originalInventory = original
	extraVars = map[string]any{}
	setupNewCluster = true
	appConfig = testAppConfig()

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(width, height)

	testApp := tview.NewApplication().SetScreen(sim)
	app = testApp
	pages = tview.NewPages()

	flexSetupMode.Clear()
	initPages()
	flexEditHosts.Clear()
	initFlexEditHosts("")
	pages.SwitchToPage("Edit Hosts")

	testApp.SetMouseCapture(normalizeButtonDoubleClicks(pages))
	testApp.SetRoot(pages, true)
	go func() {
		if err := testApp.Run(); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(testApp.Stop)

	// A SimulationScreen ignores SetSize before Run; apply the requested size
	// once the event loop is running and reset the root rectangle to match.
	testApp.QueueUpdateDraw(func() {
		sim.SetSize(width, height)
		testApp.SetRoot(pages, true)
	})

	waitFor(t, "the edit hosts page", func() bool {
		for _, row := range screenRows(t, testApp, sim) {
			if strings.Contains(row, "Edit Groups") {
				return true
			}
		}
		return false
	})

	return testApp, sim
}

func frontPage(testApp *tview.Application) string {
	var front string
	testApp.QueueUpdateDraw(func() {
		front, _ = pages.GetFrontPage()
	})
	return front
}

func TestEditHostDetailsButtonsKeyboardPath(t *testing.T) {
	testApp, sim := startEditHosts(t)

	// list -> host actions -> host details -> host details actions
	sim.InjectKey(tcell.KeyCtrlN, 0, tcell.ModNone)
	waitFor(t, "the host actions to focus", func() bool {
		return primitiveHasFocus(testApp, formHostsActions)
	})
	sim.InjectKey(tcell.KeyCtrlN, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyCtrlN, 0, tcell.ModNone)
	waitFor(t, "the host details actions to focus", func() bool {
		return primitiveHasFocus(testApp, formHostDetailsActions)
	})

	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, "the edit groups page", func() bool {
		return frontPage(testApp) == "Edit Groups"
	})
}

// Selecting a host and then clicking one of its action buttons happens within
// tview's double-click window. Button ignores MouseLeftDoubleClick, so the
// second click used to do nothing. The page must still switch.
func TestEditHostDetailsButtonsAfterHostSelection(t *testing.T) {
	testApp, sim := startEditHosts(t)

	// Select the first host with a click.
	sim.InjectMouse(5, 2, tcell.Button1, tcell.ModNone)
	sim.InjectMouse(5, 2, tcell.ButtonNone, tcell.ModNone)

	var x, y int
	testApp.QueueUpdateDraw(func() {
		index := formHostDetailsActions.GetButtonIndex("Edit Groups")
		if index < 0 {
			t.Fatal("Edit Groups button is missing")
		}
		bx, by, bw, bh := formHostDetailsActions.GetButton(index).GetRect()
		x, y = bx+bw/2, by+bh/2
	})

	// A click right after the host click is classified as a double-click.
	sim.InjectMouse(x, y, tcell.Button1, tcell.ModNone)
	sim.InjectMouse(x, y, tcell.ButtonNone, tcell.ModNone)

	waitFor(t, "the edit groups page", func() bool {
		return frontPage(testApp) == "Edit Groups"
	})
}

func TestEditHostDetailsButtonsWideTerminal(t *testing.T) {
	testApp, sim := startEditHostsSized(t, Inventory{}, 172, 42)

	var x, y int
	testApp.QueueUpdateDraw(func() {
		index := formHostDetailsActions.GetButtonIndex("Edit Node Labels")
		if index < 0 {
			t.Fatal("Edit Node Labels button is missing")
		}
		bx, by, bw, bh := formHostDetailsActions.GetButton(index).GetRect()
		x, y = bx+bw/2, by+bh/2
	})

	sim.InjectMouse(x, y, tcell.Button1, tcell.ModNone)
	sim.InjectMouse(x, y, tcell.ButtonNone, tcell.ModNone)

	waitFor(t, "the edit node labels page", func() bool {
		return frontPage(testApp) == "Edit Node Labels"
	})
}

func TestHostDetailsItemsDoNotCoverActions(t *testing.T) {
	snapshotInstallGlobals(t)
	inventory = testExportInventory()
	originalInventory = Inventory{}
	setupNewCluster = true
	appConfig = testAppConfig()

	flexEditHosts.Clear()
	initFlexEditHosts("")

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(172, 42)

	flexEditHosts.SetRect(0, 0, 172, 42)
	flexEditHosts.Draw(screen)

	upper, ok := flexHostDetails.GetItem(0).(*tview.Form)
	if !ok {
		t.Fatalf("host details item = %T, want *tview.Form", flexHostDetails.GetItem(0))
	}

	type recter interface {
		GetRect() (int, int, int, int)
	}
	for i := 0; i < formHostDetailsActions.GetButtonCount(); i++ {
		button := formHostDetailsActions.GetButton(i)
		bx, by, bw, bh := button.GetRect()
		for j := 0; j < upper.GetFormItemCount(); j++ {
			item := upper.GetFormItem(j)
			rectangle, ok := item.(recter)
			if !ok {
				continue
			}
			ix, iy, iw, ih := rectangle.GetRect()
			if ix < bx+bw && bx < ix+iw && iy < by+bh && by < iy+ih {
				t.Errorf("item %q rect=%d,%d %dx%d overlaps button %q rect=%d,%d %dx%d",
					item.GetLabel(), ix, iy, iw, ih, button.GetLabel(), bx, by, bw, bh)
			}
		}
	}
}

func TestEditHostDetailsButtonsForExistingHost(t *testing.T) {
	testApp, sim := startEditHostsWith(t, testExportInventory())

	if count := formHostDetailsActions.GetButtonCount(); count != 3 {
		t.Fatalf("action buttons = %d for an existing host, want 3", count)
	}
	if !hostDetailsReadonly {
		t.Fatal("an existing host is not in read-only mode")
	}

	testApp.QueueUpdateDraw(func() {
		testApp.SetFocus(formHostDetailsActions.GetButton(0)) // Edit Groups
	})
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, "the edit groups page", func() bool {
		return frontPage(testApp) == "Edit Groups"
	})

	testApp.QueueUpdateDraw(func() {
		testApp.SetFocus(formEditGroups.GetButton(0)) // OK
	})
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, "the edit hosts page", func() bool {
		return frontPage(testApp) == "Edit Hosts"
	})

	if !hostDetailsReadonly {
		t.Error("the details page lost its read-only state after editing groups")
	}
	if count := formHostDetailsActions.GetButtonCount(); count != 3 {
		t.Errorf("action buttons = %d after returning, want 3", count)
	}
}

func TestEditGroupsOkAfterCheckboxClick(t *testing.T) {
	testApp, sim := startEditHosts(t)

	testApp.QueueUpdateDraw(func() {
		index := formHostDetailsActions.GetButtonIndex("Edit Groups")
		testApp.SetFocus(formHostDetailsActions.GetButton(index))
	})
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, "the edit groups page", func() bool {
		return frontPage(testApp) == "Edit Groups"
	})

	var checkboxX, checkboxY, okX, okY int
	testApp.QueueUpdateDraw(func() {
		etcd := formEditGroups.GetFormItem(3).(*tview.Checkbox)
		x, y, width, height := etcd.GetRect()
		checkboxX, checkboxY = x+width/2, y+height/2

		ok := formEditGroups.GetButton(0)
		x, y, width, height = ok.GetRect()
		okX, okY = x+width/2, y+height/2
	})

	// The checkbox click is a single click; the OK click right after it is
	// classified as a double-click by tview and used to be ignored.
	sim.InjectMouse(checkboxX, checkboxY, tcell.Button1, tcell.ModNone)
	sim.InjectMouse(checkboxX, checkboxY, tcell.ButtonNone, tcell.ModNone)
	sim.InjectMouse(okX, okY, tcell.Button1, tcell.ModNone)
	sim.InjectMouse(okX, okY, tcell.ButtonNone, tcell.ModNone)

	waitFor(t, "the edit hosts page", func() bool {
		return frontPage(testApp) == "Edit Hosts"
	})
	if _, ok := inventory.All.Children.Etcd.Hosts["node1"]; ok {
		t.Error("unchecking etcd was not saved")
	}
}

func TestEditGroupsRoundTrip(t *testing.T) {
	testApp, sim := startEditHosts(t)

	testApp.QueueUpdateDraw(func() {
		index := formHostDetailsActions.GetButtonIndex("Edit Groups")
		testApp.SetFocus(formHostDetailsActions.GetButton(index))
	})
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, "the edit groups page", func() bool {
		return frontPage(testApp) == "Edit Groups"
	})

	testApp.QueueUpdateDraw(func() {
		testApp.SetFocus(formEditGroups.GetFormItem(3).(*tview.Checkbox)) // etcd
	})
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, "the etcd checkbox to clear", func() bool {
		var checked bool
		testApp.QueueUpdateDraw(func() {
			checked = formEditGroups.GetFormItem(3).(*tview.Checkbox).IsChecked()
		})
		return !checked
	})

	testApp.QueueUpdateDraw(func() {
		testApp.SetFocus(formEditGroups.GetButton(0)) // OK
	})
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)

	waitFor(t, "the edit hosts page", func() bool {
		return frontPage(testApp) == "Edit Hosts"
	})
	if _, ok := inventory.All.Children.Etcd.Hosts["node1"]; ok {
		t.Error("unchecking etcd was not saved")
	}
}
