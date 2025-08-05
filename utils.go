package main

import (
	"context"
	"fmt"
	"math"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func check(e error) {
	if e != nil {
		app.Stop()
		panic(e)
	}
}

func createCommandFile(cmdString string) {
	file, err := os.OpenFile(projectPath+"/._commands", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	check(err)
	defer file.Close()

	_, err = file.WriteString(cmdString)
	check(err)
}

func execCommand(cmdString string, timeout int, inContainer bool, envs ...string) ([]byte, error) {
	var cmd *exec.Cmd

	cmdArg := ""
	paramEnvs := ""
	if inContainer {
		for _, env := range envs {
			paramEnvs = paramEnvs + "-e " + env + " "
		}

		cmdArg = fmt.Sprintf("%s/podman-launcher-amd64 run --network=host --rm "+
			"-v '%s':'%s' -v '%s':'%s' -v '%s':'%s' -v '/root/.ssh:/root/.ssh' %s %s /bin/bash -c \"%s\"",
			offlinePath, appPath, appPath, projectPath, projectPath, offlinePath, offlinePath, paramEnvs,
			kubesprayRuntimeTag, strings.ReplaceAll(cmdString, `"`, `\"`))
	} else {
		cmdArg = cmdString
	}

	if timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
		defer cancel()

		cmd = exec.CommandContext(ctx, "/bin/bash", "-c", cmdArg)
	} else {
		cmd = exec.Command("/bin/bash", "-c", cmdArg)
	}

	if !inContainer {
		for _, env := range envs {
			cmd.Env = append(cmd.Env, env)
		}
	}

	return cmd.CombinedOutput()
}

func execCommandAndCheck(cmdString string, timeout int, inContainer bool, envs ...string) {
	output, err := execCommand(cmdString, timeout, inContainer, envs...)
	if err != nil {
		app.Stop()
		panic(string(output))
	}
}

func checkRoot() {
	cmd := exec.Command("id", "-u")
	output, err := cmd.Output()
	check(err)
	if string(output[:len(output)-1]) != "0" {
		fmt.Println("Application must run as root or as sudoer.")
		os.Exit(1)
	}
}

func showErrorModal(text string, handler func(buttonIndex int, buttonLabel string)) {
	modalError.ClearButtons()
	modalError.SetText(text).AddButtons([]string{"OK"}).SetDoneFunc(handler)
	pages.SwitchToPage("Error")
}

func showQuitModal() {
	currentPage, _ := pages.GetFrontPage()

	modalQuit.ClearButtons()
	modalQuit.SetText("Do you want to quit the application?").
		AddButtons([]string{"Quit", "Cancel"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			if buttonLabel == "Cancel" {
				pages.SwitchToPage(currentPage)
			}
			if buttonLabel == "Quit" {
				app.Stop()
			}
		})
	pages.SwitchToPage("Quit")
}

func initLog(prefix string) {
	now := time.Now()
	suffix := fmt.Sprintf("%d%02d%02dT%02d%02d%02d",
		now.Year(), now.Month(), now.Day(),
		now.Hour(), now.Minute(), now.Second())
	logFilePath = filepath.Join(projectPath, prefix+suffix+".log")
}

func Hosts(cidr string) (ips []string, err error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil, err
	}

	for addr := prefix.Addr(); prefix.Contains(addr); addr = addr.Next() {
		ips = append(ips, addr.String())
	}

	if len(ips) < 2 {
		return ips, nil
	}

	return ips[1 : len(ips)-1], nil
}

func groupPing(ips []string) (reachableIps []string, unreachableIps []string) {
	const maxConcurrency = 10

	type pingResult struct {
		ip        string
		reachable bool
	}

	if ips == nil || len(ips) == 0 {
		return
	}

	resultCh := make(chan pingResult)

	groups := int(math.Ceil(float64(len(ips)) / maxConcurrency))

	for i := 0; i < groups; i++ {
		start := i * maxConcurrency
		end := int(math.Min(float64(len(ips)-1), float64((i+1)*maxConcurrency-1)))

		for j := start; j <= end; j++ {
			go func(ip string, resultCh chan pingResult) {
				err := exec.Command("ping", ip, "-c", "2").Run()
				if err != nil {
					resultCh <- pingResult{ip: ip, reachable: false}
				} else {
					resultCh <- pingResult{ip: ip, reachable: true}
				}
			}(ips[j], resultCh)
		}

		var result pingResult
		for j := start; j <= end; j++ {
			result = <-resultCh
			if result.reachable {
				reachableIps = append(reachableIps, result.ip)
			} else {
				unreachableIps = append(unreachableIps, result.ip)
			}
		}
	}

	return reachableIps, unreachableIps
}
