package main

// Tests for the version shown by the installer. They run without root, podman
// or a terminal.

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestVersionLabel(t *testing.T) {
	tests := []struct {
		version  string
		revision string
		want     string
	}{
		{version: "v2.4.0", revision: "", want: "v2.4.0"},
		{version: "", revision: "", want: "dev"},
		{version: "dev", revision: "", want: "dev"},
		{version: "dev", revision: "abcdef123456", want: "dev (abcdef1)"},
		{version: "dev", revision: "abc", want: "dev (abc)"},
		{version: "v2.4.0", revision: "abcdef123456", want: "v2.4.0"},
	}

	for _, tt := range tests {
		if got := versionLabel(tt.version, tt.revision); got != tt.want {
			t.Errorf("versionLabel(%q, %q) = %q, want %q", tt.version, tt.revision, got, tt.want)
		}
	}
}

func TestVersionStringReleaseOverride(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "v2.4.0"
	if got := versionString(); got != "K8s Installer TUI v2.4.0" {
		t.Fatalf("versionString() = %q, want %q", got, "K8s Installer TUI v2.4.0")
	}
}

func TestSetupModePageShowsVersion(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "v9.9.9"
	initFlexSetupMode()

	if got := flexSetupMode.GetItemCount(); got != 4 {
		t.Fatalf("setup mode page has %d items, want 4", got)
	}
	versionView, ok := flexSetupMode.GetItem(3).(*tview.TextView)
	if !ok {
		t.Fatalf("item 3 = %T, want *tview.TextView", flexSetupMode.GetItem(3))
	}
	if got := versionView.GetText(true); !strings.HasSuffix(got, "K8s Installer TUI v9.9.9") {
		t.Fatalf("version text = %q, want it to end with %q", got, "K8s Installer TUI v9.9.9")
	}

	// Draw the page and confirm the version is actually rendered on screen.
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("init simulation screen: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(80, 24)

	flexSetupMode.SetRect(0, 0, 80, 24)
	flexSetupMode.Draw(screen)
	screen.Show()

	rows := simRows(screen)
	for _, row := range rows {
		if strings.Contains(row, "K8s Installer TUI v9.9.9") {
			return
		}
	}
	t.Fatalf("rendered Setup Mode page does not contain the version; screen:\n%s", strings.Join(rows, "\n"))
}
