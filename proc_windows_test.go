//go:build windows

package main

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureBackgroundProcessHidesConsoleWindow(t *testing.T) {
	cmd := exec.Command("node.exe")
	configureBackgroundProcess(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr was not configured")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("background process window is not hidden")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("CREATE_NO_WINDOW is not set")
	}
}
