package main

import (
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/exp/slices"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var process *os.Process
var processState *os.ProcessState
var flexSetupClusterUp = tview.NewFlex()
var startTime time.Time
var stopTimer = make(chan bool)
var abortButton *tview.Button
var backButton *tview.Button
var quitButton *tview.Button
var logContent = tview.NewTextView()

func initFlexSetupCluster(clean bool) {
	if clean {
		flexSetupCluster.Clear()
	}

	textLog := tview.NewInputField()
	textLog.SetLabel("Log File: ")
	textLog.SetText(logFilePath)
	textLog.SetDisabled(true)

	logContent = tview.NewTextView()
	logContent.SetBackgroundColor(tcell.ColorDarkBlue)
	logContent.SetMaxLines(500).
		SetWrap(true).
		SetWordWrap(true).
		SetChangedFunc(func() {
			logContent.ScrollToEnd()
			app.Draw()
		})

	flexSetupClusterUp = tview.NewFlex().SetDirection(tview.FlexRow)
	flexSetupClusterUp.SetTitle("Setup Cluster").SetBorder(true)
	flexSetupClusterUp.AddItem(textLog, 2, 1, false).
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
						backButton.SetDisabled(false)
						quitButton.SetDisabled(false)

						pages.SwitchToPage("Setup Cluster")
					}
				})
			pages.AddPage("Confirm Abort", confirmAbort, true, true)
		}
	})
	abortButton = formDown.GetButton(formDown.GetButtonIndex("Abort"))
	abortButton.SetDisabled(true)

	formDown.AddButton("Back", func() {
		pages.SwitchToPage("Deploy Cluster")
	})
	backButton = formDown.GetButton(formDown.GetButtonIndex("Back"))
	backButton.SetDisabled(false)

	formDown.AddButton("Quit", func() {
		showQuitModal()
	})
	quitButton = formDown.GetButton(formDown.GetButtonIndex("Quit"))
	quitButton.SetDisabled(false)

	flexSetupCluster.SetDirection(tview.FlexRow).
		AddItem(flexSetupClusterUp, 0, 1, true).
		AddItem(formDown, 3, 1, false)

	go execCmd(logContent)
	go updateTimer(stopTimer)
}

func execCmd(view *tview.TextView) {
	startTime = time.Now()

	cmdString := fmt.Sprintf(`
cp -a %s %s
cp -a %s %s
chmod -R 700 %s
`, filepath.Join(appPath, "ansible-roles/*"), filepath.Join(kubesprayPath, "roles/"),
		filepath.Join(appPath, "ansible-playbooks/*"), filepath.Join(kubesprayPath, "playbooks/"),
		kubesprayPath)
	execCommand(cmdString, 0)

	if setupNewCluster {
		// Create a new cluster
		cmdString = fmt.Sprintf(`
set -euao pipefail

export inventory=%s
export key=%s
export vars=%s
export log=%s

echo "====================Setup a new cluster====================" | tee -a "$log"

echo "====================playbooks/extra_setup.yml====================" | tee -a "$log"
/usr/local/bin/ansible-playbook -i "$inventory" -u root --private-key="$key" -e @"$vars" \
  "playbooks/extra_setup.yml" 2>&1 | tee -a "$log"

echo "====================playbooks/cluster.yml====================" | tee -a "$log"
/usr/local/bin/ansible-playbook -i "$inventory" -u root --private-key="$key" -e @"$vars" \
  "playbooks/cluster.yml" 2>&1 | tee -a "$log"

echo | tee -a "$log"
echo "====================Setup Finished====================" | tee -a "$log"
echo | tee -a "$log"

/usr/local/bin/ansible -i "$inventory" -u root --private-key="$key" kube_control_plane[0] \
  -m shell -a "kubectl get node" 2>&1 | tee -a "$log"
`, inventoryFile, keyFile, filepath.Join(projectPath, "extra-vars.yaml"), logFilePath)
	} else {
		// Modify existing cluster
		var addedControlAndEtcdNodes, addedWorkNodes []string

		for k, _ := range inventory.All.Children.Kube_control_plane.Hosts {
			if originalInventory.All.Children.Kube_control_plane.Hosts[k] == nil {
				addedControlAndEtcdNodes = append(addedControlAndEtcdNodes, k)
			}
		}
		for k, _ := range inventory.All.Children.Etcd.Hosts {
			if originalInventory.All.Children.Etcd.Hosts[k] == nil {
				addedControlAndEtcdNodes = append(addedControlAndEtcdNodes, k)
			}
		}
		slices.Sort(addedControlAndEtcdNodes)
		addedControlAndEtcdNodes = slices.Compact(addedControlAndEtcdNodes)

		for k, _ := range inventory.All.Children.Kube_node.Hosts {
			if inventory.All.Children.Kube_control_plane.Hosts[k] == nil &&
				inventory.All.Children.Etcd.Hosts[k] == nil &&
				originalInventory.All.Children.Kube_node.Hosts[k] == nil {
				// The node is pure work node and not in the original inventory
				addedWorkNodes = append(addedWorkNodes, k)
			}
		}

		cmdAddControlNode := ""
		if len(addedControlAndEtcdNodes) > 0 {
			cmdAddControlNode = fmt.Sprintf(`
export inventory=%s
export key=%s
export vars=%s
export log=%s

echo "====================playbooks/cluster.yml====================" | tee -a "$log"
/usr/local/bin/ansible-playbook -i "$inventory" -u root --private-key="$key" -e @"$vars" \
  --skip-tags=multus \
  --limit=etcd,kube_control_plane -e ignore_assert_errors=yes -e etcd_retries=10 \
  "playbooks/cluster.yml" 2>&1 | tee -a "$log"

echo "====================playbooks/upgrade_cluster.yml====================" | tee -a "$log"
/usr/local/bin/ansible-playbook -i "$inventory" -u root --private-key="$key" -e @"$vars" \
  --skip-tags=multus \
  --limit=etcd,kube_control_plane -e ignore_assert_errors=yes -e etcd_retries=10 \
  "playbooks/upgrade_cluster.yml" 2>&1 | tee -a "$log"
`, inventoryFile, keyFile, filepath.Join(projectPath, "extra-vars.yaml"), logFilePath)
		}

		cmdAddWorkNode := ""
		if len(addedWorkNodes) > 0 {
			cmdAddWorkNode = fmt.Sprintf(`
export inventory=%s
export key=%s
export vars=%s
export log=%s

echo "====================playbooks/facts.yml====================" | tee -a "$log"
/usr/local/bin/ansible-playbook -i "$inventory" -u root --private-key="$key" -e @"$vars" \
  "playbooks/facts.yml" 2>&1 | tee -a "$log"

echo "====================playbooks/scale.yml====================" | tee -a "$log"
/usr/local/bin/ansible-playbook -i "$inventory" -u root --private-key="$key" -e @"$vars" \
  --limit="%s" \
  "playbooks/scale.yml" 2>&1 | tee -a "$log"
`, inventoryFile, keyFile, filepath.Join(projectPath, "extra-vars.yaml"), logFilePath,
				strings.Join(addedWorkNodes, ","))
		}

		cmdString = fmt.Sprintf(`
set -euao pipefail

export inventory=%s
export key=%s
export vars=%s
export log=%s

echo "====================Modify cluster====================" | tee -a "$log"

echo "====================playbooks/extra_setup.yml====================" | tee -a "$log"
/usr/local/bin/ansible-playbook -i "$inventory" -u root --private-key="$key" -e @"$vars" \
  "playbooks/extra_setup.yml" 2>&1 | tee -a "$log"

%s
%s

echo "====================Restart all nginx-proxy====================" | tee -a "$log"
/usr/local/bin/ansible -i "$inventory" -u root --private-key="$key" kube_control_plane[0] \
  -m shell -a "kubectl get pod -n kube-system | grep nginx-proxy | awk '{print \$1}' | xargs -r kubectl delete pod -n kube-system"

echo "====================Restart all nginx ingress controller====================" | tee -a "$log"
/usr/local/bin/ansible -i "$inventory" -u root --private-key="$key" kube_control_plane[0] \
  -m shell -a "kubectl delete pod --all -n ingress-nginx"

echo | tee -a "$log"
echo "====================Setup Finished====================" | tee -a "$log"
echo | tee -a "$log"

/usr/local/bin/ansible -i "$inventory" -u root --private-key="$key" kube_control_plane[0] \
  -m shell -a "kubectl get node" 2>&1 | tee -a "$log"
`, inventoryFile, keyFile, filepath.Join(projectPath, "extra-vars.yaml"), logFilePath,
			cmdAddControlNode,
			cmdAddWorkNode)
	}

	cmd := exec.Command("/bin/bash", "-c", cmdString)
	cmd.Dir = kubesprayPath
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	check(err)

	err = cmd.Start()
	check(err)
	process = cmd.Process

	abortButton.SetDisabled(false)
	backButton.SetDisabled(true)
	quitButton.SetDisabled(true)

	_, err = io.Copy(view, stdout)
	check(err)

	var resultColor tcell.Color
	err = cmd.Wait()
	if err != nil {
		resultColor = tcell.ColorDarkRed
	} else {
		resultColor = tcell.ColorDarkGreen

		if !setupNewCluster {
			now := time.Now()
			suffix := fmt.Sprintf("%d%02d%02dT%02d%02d%02d",
				now.Year(), now.Month(), now.Day(),
				now.Hour(), now.Minute(), now.Second())

			originalInventoryFile := filepath.Join(projectPath, "hosts.yaml")
			bakFile := originalInventoryFile + "." + suffix

			err := os.Rename(originalInventoryFile, bakFile)
			check(err)

			err = os.Rename(inventoryFile, originalInventoryFile)
			check(err)
		}
	}

	processState = cmd.ProcessState

	stopTimer <- true

	app.QueueUpdateDraw(func() {
		logContent.SetBackgroundColor(resultColor)
		abortButton.SetDisabled(true)
		backButton.SetDisabled(false)
		quitButton.SetDisabled(false)
	})
}

func updateTimer(stop chan bool) {
	for {
		select {
		case <-stop:
			return
		default:
			app.QueueUpdateDraw(func() {
				flexSetupClusterUp.SetTitle("Setup Cluster - Time Elapsed: " + time.Since(startTime).Round(time.Second).String())
			})
			time.Sleep(time.Second)
		}
	}
}
