//go:build windows

package main

import (
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureBackgroundProcess prevents long-running Node processes from opening
// a console window when they are launched by the GUI application.
func configureBackgroundProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}

// killProcessTree 使用 taskkill /T /F 回收进程树。
func killProcessTree(cmd *exec.Cmd) {
	pid := cmd.Process.Pid
	taskkill := exec.Command("taskkill", "/pid", strconv.Itoa(pid), "/T", "/F")
	configureBackgroundProcess(taskkill)
	_ = taskkill.Run()
}
