package main

// Tests for the Mirror page's create-mode destination and its seeding of a
// loaded project's mirror overrides.

import (
	"path/filepath"
	"testing"

	"github.com/rivo/tview"
)

func TestMirrorNextPage(t *testing.T) {
	old := createMode
	t.Cleanup(func() { createMode = old })

	createMode = false
	if got := mirrorNextPage(); got != "Deploy Cluster" {
		t.Errorf("normal next page = %q, want Deploy Cluster", got)
	}

	createMode = true
	if got := mirrorNextPage(); got != "Save Config" {
		t.Errorf("create next page = %q, want Save Config", got)
	}
}

func TestMirrorPageUsesOverridesFromExtraVars(t *testing.T) {
	snapshotInstallGlobals(t)
	t.Cleanup(func() { flexMirror.Clear() })

	projectPath = t.TempDir()
	writeTestFile(t, filepath.Join(projectPath, "group_vars/all/mirror.yml"), "")
	appConfig = testAppConfig()
	extraVars = map[string]any{"gcr_image_repo": "gcr.custom"}
	initialValueLoaded = false
	mirrors = nil
	enableMirror = false

	flexMirror.Clear()
	initFlexMirror()

	form, ok := flexMirror.GetItem(0).(*tview.Form)
	if !ok {
		t.Fatalf("first item = %T, want *tview.Form", flexMirror.GetItem(0))
	}
	item := form.GetFormItemByLabel("gcr_image_repo: ")
	input, ok := item.(*tview.InputField)
	if !ok {
		t.Fatalf("gcr field = %#v, want *tview.InputField", item)
	}
	if got := input.GetText(); got != "gcr.custom" {
		t.Fatalf("gcr field = %q, want the loaded project's override", got)
	}
}
