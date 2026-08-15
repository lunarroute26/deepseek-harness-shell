//go:build windows

package main

import (
	"os/exec"
	"strconv"
)

// setProcessGroup Windows 下无需特殊处理，taskkill /T 即可回收进程树。
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessTree 使用 taskkill /T /F 回收进程树。
func killProcessTree(cmd *exec.Cmd) {
	pid := cmd.Process.Pid
	_ = exec.Command("taskkill", "/pid", strconv.Itoa(pid), "/T", "/F").Run()
}
