package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResourceRootForExecutable(t *testing.T) {
	tests := []struct {
		name       string
		executable string
		goos       string
		want       string
	}{
		{"macOS app", "/Applications/deepseek-harness-shell.app/Contents/MacOS/deepseek-harness-shell", "darwin", "/Applications/deepseek-harness-shell.app/Contents/Resources"},
		{"macOS bare binary", "/opt/deepseek-harness-shell", "darwin", "/opt"},
		{"Windows executable", "/Program Files/deepseek harness shell/deepseek-harness-shell.exe", "windows", "/Program Files/deepseek harness shell"},
		{"Linux executable", "/usr/local/bin/deepseek-harness-shell", "linux", "/usr/local/bin"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resourceRootForExecutable(test.executable, test.goos); got != test.want {
				t.Fatalf("resource root = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveRepoEntry(t *testing.T) {
	t.Setenv("DSH_HOME", "")

	t.Run("deploy root", func(t *testing.T) {
		root := t.TempDir()
		entry := filepath.Join(root, "lib", "bin.js")
		writeTestFile(t, entry)
		t.Setenv("DSH_REPO", root)

		gotEntry, gotRoot, ok := resolveRepoEntry()
		if !ok || gotEntry != entry || gotRoot != root {
			t.Fatalf("resolveRepoEntry() = (%q, %q, %v)", gotEntry, gotRoot, ok)
		}
	})

	t.Run("source root", func(t *testing.T) {
		root := t.TempDir()
		entry := filepath.Join(root, "apps", "cli", "src", "bin.ts")
		writeTestFile(t, entry)
		t.Setenv("DSH_REPO", root)

		gotEntry, gotRoot, ok := resolveRepoEntry()
		if !ok || gotEntry != entry || gotRoot != root {
			t.Fatalf("resolveRepoEntry() = (%q, %q, %v)", gotEntry, gotRoot, ok)
		}
	})
}

func TestMigrateLegacyProfileFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	legacyPackage := filepath.Join(
		home,
		"profiles",
		"node_modules",
		"@deepseek-ai",
		"dsh-session-query-sqlite",
	)
	marker := filepath.Join(legacyPackage, "migration-marker.txt")
	writeTestFile(t, marker)

	backup, err := migrateLegacyProfileFallback()
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("legacy fallback was not migrated")
	}
	if _, err := os.Stat(filepath.Join(
		backup,
		"@deepseek-ai",
		"dsh-session-query-sqlite",
		"migration-marker.txt",
	)); err != nil {
		t.Fatalf("legacy package was not preserved in backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "profiles", "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("legacy fallback still exists: %v", err)
	}

	backup, err = migrateLegacyProfileFallback()
	if err != nil || backup != "" {
		t.Fatalf("second migration = (%q, %v), want no-op", backup, err)
	}
}

func TestPhysicalFallbackDetectionAllowsScopedContainer(t *testing.T) {
	modulesDir := filepath.Join(t.TempDir(), "node_modules")
	if err := os.MkdirAll(filepath.Join(modulesDir, "@deepseek-ai"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy, err := hasPhysicalFallbackPackages(modulesDir)
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		t.Fatal("empty scoped package container was treated as a legacy package")
	}
}

func TestDSHRunnerReady(t *testing.T) {
	runner := testRunner("ready")
	events := make(chan runnerEvent, 2)
	runner.OnStatus = func(status DSHStatus, message string) {
		events <- runnerEvent{status, message}
	}

	if err := runner.Start(); err != nil {
		t.Fatal(err)
	}
	defer runner.Stop()

	event := waitRunnerEvent(t, events)
	if event.status != DSHReady || event.message != "http://127.0.0.1:45678" {
		t.Fatalf("event = %#v", event)
	}
}

func TestDSHRunnerUnexpectedExit(t *testing.T) {
	runner := testRunner("error")
	events := make(chan runnerEvent, 2)
	runner.OnStatus = func(status DSHStatus, message string) {
		events <- runnerEvent{status, message}
	}

	if err := runner.Start(); err != nil {
		t.Fatal(err)
	}
	event := waitRunnerEvent(t, events)
	if event.status != DSHFailed || !strings.Contains(event.message, "意外退出") {
		t.Fatalf("event = %#v", event)
	}
}

func TestDSHRunnerStopSuppressesStaleGeneration(t *testing.T) {
	runner := testRunner("slow-ready")
	events := make(chan runnerEvent, 4)
	runner.OnStatus = func(status DSHStatus, message string) {
		events <- runnerEvent{status, message}
	}

	if err := runner.Start(); err != nil {
		t.Fatal(err)
	}
	runner.Stop()
	runner.command = helperCommand("ready")
	if err := runner.Start(); err != nil {
		t.Fatal(err)
	}
	defer runner.Stop()

	event := waitRunnerEvent(t, events)
	if event.status != DSHReady || event.message != "http://127.0.0.1:45678" {
		t.Fatalf("event = %#v", event)
	}
	select {
	case extra := <-events:
		t.Fatalf("stale generation emitted %#v", extra)
	case <-time.After(400 * time.Millisecond):
	}
}

func TestDSHRunnerBuildError(t *testing.T) {
	runner := NewDSHRunner()
	runner.command = func() (*exec.Cmd, error) { return nil, errors.New("payload missing") }
	if err := runner.Start(); err == nil || !strings.Contains(err.Error(), "payload missing") {
		t.Fatalf("Start() error = %v", err)
	}
}

type runnerEvent struct {
	status  DSHStatus
	message string
}

func testRunner(mode string) *DSHRunner {
	runner := NewDSHRunner()
	runner.command = helperCommand(mode)
	runner.timeout = 2 * time.Second
	return runner
}

func helperCommand(mode string) func() (*exec.Cmd, error) {
	return func() (*exec.Cmd, error) {
		cmd := exec.Command(os.Args[0], "-test.run=TestDSHRunnerHelperProcess", "--", mode)
		cmd.Env = append(os.Environ(), "GO_WANT_DSH_HELPER=1", "DSH_HELPER_MODE="+mode)
		return cmd, nil
	}
}

func waitRunnerEvent(t *testing.T, events <-chan runnerEvent) runnerEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for runner event")
		return runnerEvent{}
	}
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDSHRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DSH_HELPER") != "1" {
		return
	}
	switch os.Getenv("DSH_HELPER_MODE") {
	case "ready":
		fmt.Println("dsh web: http://127.0.0.1:45678 (LAN: disabled)")
		time.Sleep(10 * time.Second)
	case "slow-ready":
		time.Sleep(250 * time.Millisecond)
		fmt.Println("dsh web: http://127.0.0.1:49999")
		time.Sleep(10 * time.Second)
	case "error":
		fmt.Fprintln(os.Stderr, "intentional helper failure")
		os.Exit(7)
	default:
		os.Exit(2)
	}
}
