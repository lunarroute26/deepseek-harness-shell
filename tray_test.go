package main

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestTrayExitIsRequestedOnce(t *testing.T) {
	var quitCalls atomic.Int32
	var beforeQuitCalls atomic.Int32
	controller := &trayController{
		beforeQuit: func() {
			beforeQuitCalls.Add(1)
		},
		quit: func() {
			quitCalls.Add(1)
		},
	}

	var waitGroup sync.WaitGroup
	for index := 0; index < 16; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			controller.requestExit()
		}()
	}
	waitGroup.Wait()

	if got := quitCalls.Load(); got != 1 {
		t.Fatalf("quit called %d times, want 1", got)
	}
	if got := beforeQuitCalls.Load(); got != 1 {
		t.Fatalf("before quit called %d times, want 1", got)
	}
	if !controller.exitRequested.Load() {
		t.Fatal("exit request was not recorded")
	}
}

func TestWindowCloseIsCancelledUntilShutdown(t *testing.T) {
	controller := &trayController{window: &application.WebviewWindow{}}
	closeEvent := application.NewWindowEvent()
	controller.handleWindowClosing(closeEvent)
	if !closeEvent.IsCancelled() {
		t.Fatal("normal window close was not cancelled")
	}

	controller.markExiting()
	shutdownCloseEvent := application.NewWindowEvent()
	controller.handleWindowClosing(shutdownCloseEvent)
	if shutdownCloseEvent.IsCancelled() {
		t.Fatal("shutdown window close was cancelled")
	}
}
