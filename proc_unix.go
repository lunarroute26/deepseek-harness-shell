//go:build !windows

package main

import (
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup 让子进程自成进程组，便于整树回收。
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree 先 SIGTERM 后 SIGKILL，回收整个进程组。
func killProcessTree(cmd *exec.Cmd) {
	pid := cmd.Process.Pid
	// 负 PID 表示进程组
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}
