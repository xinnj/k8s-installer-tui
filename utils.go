package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func check(e error) {
	if e != nil {
		app.Stop()
		panic(e)
	}
}

func execCommand(cmdString string, timeout int, envs ...string) {
	var cmd *exec.Cmd

	if timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
		defer cancel()

		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", cmdString)
	} else {
		cmd = exec.Command("/bin/sh", "-c", cmdString)
	}

	cmd.Env = os.Environ()
	for _, env := range envs {
		cmd.Env = append(cmd.Env, env)
	}

	output, err := cmd.CombinedOutput()
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

func initLog(prefix string) {
	if logFile != nil {
		logFile.Close()
	}

	now := time.Now()
	suffix := fmt.Sprintf("%d%02d%02dT%02d%02d%02d",
		now.Year(), now.Month(), now.Day(),
		now.Hour(), now.Minute(), now.Second())
	logFilePath := filepath.Join(projectPath, prefix+suffix+".log")

	f, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	check(err)

	logFile = f
}

func writeLog(content string) {
	_, err := logFile.WriteString(content + "\n")
	check(err)
	err = logFile.Sync()
	check(err)
}
