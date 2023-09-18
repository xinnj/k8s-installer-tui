package main

import (
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

var process *os.Process
var processState *os.ProcessState
var flexUp = tview.NewFlex()
var startTime time.Time
var stopTimer = make(chan bool)
var abortButton *tview.Button
var quitButton *tview.Button

func initFlexSetupCluster(clean bool) {
	if clean {
		flexSetupCluster.Clear()
	}

	textLog := tview.NewInputField()
	textLog.SetLabel("Log File: ")
	textLog.SetText(logFilePath)
	textLog.SetDisabled(true)

	logContent := tview.NewTextView()
	logContent.SetBackgroundColor(tcell.ColorDarkGreen)
	logContent.SetMaxLines(500).
		SetWrap(true).
		SetWordWrap(true).
		SetChangedFunc(func() {
			logContent.ScrollToEnd()
			app.Draw()
		})

	flexUp = tview.NewFlex().SetDirection(tview.FlexRow)
	flexUp.SetTitle("Setup Cluster").SetBorder(true)
	flexUp.AddItem(textLog, 2, 1, false).
		AddItem(logContent, 0, 1, true)

	formDown := tview.NewForm()
	formDown.AddButton("Abort", func() {
		if processState == nil {
			confirmAbort := tview.NewModal().
				SetText("Do you want to abort the execution?").
				AddButtons([]string{"Abort", "Cancel"}).
				SetDoneFunc(func(buttonIndex int, buttonLabel string) {
					if buttonLabel == "Cancel" {
						pages.SwitchToPage("Setup Cluster")
					}
					if buttonLabel == "Abort" {
						pgid, err := syscall.Getpgid(process.Pid)
						check(err)
						syscall.Kill(-pgid, 15)
						stopTimer <- true
						abortButton.SetDisabled(true)
						quitButton.SetDisabled(false)

						pages.SwitchToPage("Setup Cluster")
					}
				})
			pages.AddPage("Confirm Abort", confirmAbort, true, true)
		}
	})
	abortButton = formDown.GetButton(formDown.GetButtonIndex("Abort"))
	abortButton.SetDisabled(true)
	formDown.AddButton("Quit", func() {
		showQuitModal("Setup Cluster")
	})
	quitButton = formDown.GetButton(formDown.GetButtonIndex("Quit"))
	quitButton.SetDisabled(false)

	flexSetupCluster.SetDirection(tview.FlexRow).
		AddItem(flexUp, 0, 1, true).
		AddItem(formDown, 3, 1, false)

	go execCmd(logContent)
	go updateTimer(stopTimer)
}

func execCmd(view *tview.TextView) {
	startTime = time.Now()
	writeLog("====================Start to setup the cluster====================\n")
	err := logFile.Close()
	check(err)

	cmdString := fmt.Sprintf(`
cp -a %s %s
cp -a %s %s
`, filepath.Join(appPath, "ansible-roles/*"), filepath.Join(kubesprayPath, "roles/"),
		filepath.Join(appPath, "ansible-playbooks/*"), filepath.Join(kubesprayPath, "playbooks/"))
	execCommand(cmdString, 0)

	cmdString = fmt.Sprintf(`
set -euao pipefail

export inventory=%s
export key=%s
export vars=%s
export log=%s
/usr/local/bin/ansible-playbook -i "$inventory" -u root --private-key="$key" -e @"$vars" "%s" 2>&1 | tee -a "$log"
#/usr/local/bin/ansible-playbook -i "$inventory" -u root --private-key="$key" -e @"$vars" "%s" 2>&1 | tee -a "$log"
echo | tee -a "$log"
echo "====================Setup Finished====================" | tee -a "$log"
echo | tee -a "$log"
/usr/local/bin/ansible -i "$inventory" -u root --private-key="$key" kube_control_plane[0] -m shell -a "kubectl get node" 2>&1 | tee -a "$log"
`, inventoryFile, keyFile, filepath.Join(projectPath, "default-vars.yaml"), logFilePath,
		"playbooks/extra_setup.yml",
		"playbooks/cluster.yml")

	cmd := exec.Command("/bin/bash", "-c", cmdString)
	cmd.Dir = kubesprayPath
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	check(err)

	err = cmd.Start()
	check(err)
	process = cmd.Process

	abortButton.SetDisabled(false)
	quitButton.SetDisabled(true)

	_, err = io.Copy(view, stdout)
	check(err)

	err = cmd.Wait()
	processState = cmd.ProcessState

	stopTimer <- true
	abortButton.SetDisabled(true)
	quitButton.SetDisabled(false)
}

func updateTimer(stop chan bool) {
	for {
		select {
		case <-stop:
			return
		default:
			app.QueueUpdateDraw(func() {
				flexUp.SetTitle("Setup Cluster - Time Elapsed:" + time.Since(startTime).Round(time.Second).String())
			})
			time.Sleep(time.Second)
		}
	}
}
