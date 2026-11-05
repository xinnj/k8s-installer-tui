package main

// An unattended run ends by stopping the event loop, not by exiting the
// process from the playbook goroutine. tview's Run does not finalize the
// screen itself; only app.Stop calls tcell's Fini, which turns mouse reporting
// off and restores the terminal. Exiting directly left the shell receiving
// mouse escape sequences as command input.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// finiRecordingScreen counts how often the screen is finalized.
type finiRecordingScreen struct {
	tcell.SimulationScreen
	finiCalls atomic.Int32
}

func (s *finiRecordingScreen) Fini() {
	s.finiCalls.Add(1)
	s.SimulationScreen.Fini()
}

func TestFinishAutoRunStopsTheUI(t *testing.T) {
	snapshotInstallGlobals(t)

	oldApp, oldPages := app, pages
	t.Cleanup(func() {
		app, pages = oldApp, oldPages
	})

	screen := &finiRecordingScreen{SimulationScreen: tcell.NewSimulationScreen("UTF-8")}
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(80, 25)

	testApp := tview.NewApplication().SetScreen(screen)
	app = testApp
	pages = tview.NewPages()
	pages.AddPage("Running", tview.NewTextView().SetText("running"), true, true)
	testApp.SetRoot(pages, true)
	t.Cleanup(testApp.Stop)

	done := make(chan error, 1)
	go func() {
		done <- testApp.Run()
	}()

	// Wait until the event loop is drawing, so Stop does not race its start.
	waitFor(t, "the application to draw", func() bool {
		return len(screenRows(t, testApp, screen)) > 0
	})

	autoRun = true
	finishAutoRun(7)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned an error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the event loop did not stop after the run finished")
	}

	if screen.finiCalls.Load() == 0 {
		t.Error("the screen was not finalized; the terminal would be left in raw mouse mode")
	}
	if code := autoRunExitCode.Load(); code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
}

// The console must still say how an unattended run ended: tcell clears the
// screen when the application stops, so the TUI's result is gone by the time
// the shell prompt returns.
const failedRunLog = `PLAY [all] ****************************************
fatal: [onpremisis-1]: UNREACHABLE! => {"changed": false, "msg": "Failed to connect to the host via ssh: ssh: connect to host 10.0.0.11 port 22: Connection timed out", "unreachable": true}
PLAY RECAP ****************************************
onpremisis-1               : ok=620  changed=13   unreachable=1    failed=0    skipped=488  rescued=0    ignored=0
onpremisis-2               : ok=558  changed=13   unreachable=1    failed=0    skipped=420  rescued=0    ignored=0
onpremisis-3               : ok=471  changed=16   unreachable=0    failed=0    skipped=381  rescued=0    ignored=0
`

func writeTestRunLog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "create-cluster-20261106T094512.log")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAutoRunSummaryFailure(t *testing.T) {
	logPath := writeTestRunLog(t, failedRunLog)

	summary := autoRunSummary(autoRunReport{
		Code:      1,
		LogPath:   logPath,
		Project:   "/home/xinnj/idocluster",
		HostCount: 3,
		Duration:  6*time.Minute + 30*time.Second,
	})

	expected := "================ Installation result ================\n" +
		"Result     : FAILED\n" +
		"Exit code  : 1\n" +
		"Duration   : 6m 30s\n" +
		"Project    : /home/xinnj/idocluster\n" +
		"Log file   : " + logPath + "\n" +
		"Hosts      : 3\n" +
		"\n" +
		"Host problems:\n" +
		"  onpremisis-1  unreachable=1\n" +
		"  onpremisis-2  unreachable=1\n" +
		"\n" +
		"Last error:\n" +
		"  fatal: [onpremisis-1]: UNREACHABLE! => Failed to connect to the host via ssh:\n" +
		"  ssh: connect to host 10.0.0.11 port 22: Connection timed out\n" +
		"\n" +
		"Last 20 lines of the log:\n" +
		"  PLAY [all] ****************************************\n" +
		"  fatal: [onpremisis-1]: UNREACHABLE! => {\"changed\": false, \"msg\": \"Failed to connect to the host via ssh: ssh: connect to host 10.0.0.11 port 22: Connection timed out\", \"unreachable\": true}\n" +
		"  PLAY RECAP ****************************************\n" +
		"  onpremisis-1               : ok=620  changed=13   unreachable=1    failed=0    skipped=488  rescued=0    ignored=0\n" +
		"  onpremisis-2               : ok=558  changed=13   unreachable=1    failed=0    skipped=420  rescued=0    ignored=0\n" +
		"  onpremisis-3               : ok=471  changed=16   unreachable=0    failed=0    skipped=381  rescued=0    ignored=0\n" +
		"=====================================================\n"

	if summary != expected {
		t.Errorf("summary mismatch:\n--- got ---\n%s\n--- want ---\n%s", summary, expected)
	}
}

func TestAutoRunSummarySuccess(t *testing.T) {
	logPath := writeTestRunLog(t, "setup finished\n")

	summary := autoRunSummary(autoRunReport{
		Code:      0,
		LogPath:   logPath,
		Project:   "/home/xinnj/idocluster",
		HostCount: 3,
		Duration:  12*time.Minute + 4*time.Second,
	})

	expected := "================ Installation result ================\n" +
		"Result     : SUCCESS\n" +
		"Exit code  : 0\n" +
		"Duration   : 12m 4s\n" +
		"Project    : /home/xinnj/idocluster\n" +
		"Log file   : " + logPath + "\n" +
		"Hosts      : 3\n" +
		"=====================================================\n"

	if summary != expected {
		t.Errorf("summary mismatch:\n--- got ---\n%s\n--- want ---\n%s", summary, expected)
	}
}

func TestAutoRunSummaryDryRun(t *testing.T) {
	summary := autoRunSummary(autoRunReport{Dry: true})

	if !strings.Contains(summary, "Dry run finished") {
		t.Errorf("summary does not describe a dry run:\n%s", summary)
	}
	if strings.Contains(summary, "SUCCESS") {
		t.Errorf("dry run claims an installation:\n%s", summary)
	}
}

func TestAutoRunSummaryTailKeepsLast20Lines(t *testing.T) {
	var content strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	logPath := writeTestRunLog(t, content.String())

	summary := autoRunSummary(autoRunReport{Code: 1, LogPath: logPath})

	if strings.Contains(summary, "line 10\n") || !strings.Contains(summary, "line 11\n") {
		t.Errorf("summary does not keep the last 20 lines:\n%s", summary)
	}
	if !strings.Contains(summary, "line 30\n") {
		t.Errorf("summary does not end with the log tail:\n%s", summary)
	}
}

func TestFormatRunDuration(t *testing.T) {
	for _, test := range []struct {
		duration time.Duration
		want     string
	}{
		{5 * time.Second, "5s"},
		{73 * time.Second, "1m 13s"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1h 2m 3s"},
	} {
		if got := formatRunDuration(test.duration); got != test.want {
			t.Errorf("formatRunDuration(%s) = %q, want %q", test.duration, got, test.want)
		}
	}
}
