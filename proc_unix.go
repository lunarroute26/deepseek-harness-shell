//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// configureBackgroundProcess 让子进程自成进程组，便于整树回收。
func configureBackgroundProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree 先 SIGTERM 后 SIGKILL，回收整个进程组。
func killProcessTree(cmd *exec.Cmd) {
	pid := cmd.Process.Pid
	// 负 PID 表示进程组
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(-pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
