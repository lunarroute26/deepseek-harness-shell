package main

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLifecycleWaitsForSplashAndReplaysState(t *testing.T) {
	runner := &fakeDSHProcess{}
	events := make(chan runnerEvent, 8)
	lifecycle := newDSHLifecycle(runner, func(status DSHStatus, message string) {
		events <- runnerEvent{status, message}
	})

	time.Sleep(20 * time.Millisecond)
	if starts, _ := runner.counts(); starts != 0 {
		t.Fatalf("runner started before splash handshake")
	}

	lifecycle.SplashReady()
	waitFor(t, func() bool {
		starts, _ := runner.counts()
		return starts == 1
	})
	if event := waitRunnerEvent(t, events); event.status != DSHStarting {
		t.Fatalf("initial event = %#v", event)
	}

	lifecycle.HandleStatus(DSHFailed, "failed")
	if event := waitRunnerEvent(t, events); event.status != DSHFailed {
		t.Fatalf("failure event = %#v", event)
	}
	lifecycle.SplashReady()
	if event := waitRunnerEvent(t, events); event.status != DSHFailed || event.message != "failed" {
		t.Fatalf("replayed event = %#v", event)
	}
	if starts, _ := runner.counts(); starts != 1 {
		t.Fatalf("replay restarted runner: starts=%d", starts)
	}
}

func TestLifecycleRetryIsSerialised(t *testing.T) {
	runner := &fakeDSHProcess{startErr: errors.New("first start failed")}
	events := make(chan runnerEvent, 8)
	lifecycle := newDSHLifecycle(runner, func(status DSHStatus, message string) {
		events <- runnerEvent{status, message}
	})
	lifecycle.SplashReady()

	waitFor(t, func() bool {
		starts, _ := runner.counts()
		return starts == 1
	})
	waitForStatus(t, events, DSHFailed)
	runner.setStartError(nil)

	for index := 0; index < 10; index++ {
		go lifecycle.Retry()
	}
	waitFor(t, func() bool {
		starts, stops := runner.counts()
		return starts == 2 && stops == 1
	})
	time.Sleep(50 * time.Millisecond)
	starts, stops := runner.counts()
	if starts != 2 || stops != 1 {
		t.Fatalf("retry counts = starts:%d stops:%d", starts, stops)
	}
	lifecycle.Shutdown()
	lifecycle.Shutdown()
	_, stops = runner.counts()
	if stops != 2 {
		// One stop belongs to Retry and one to the first Shutdown call.
		t.Fatalf("Shutdown is not idempotent: stops=%d", stops)
	}
}

type fakeDSHProcess struct {
	mu       sync.Mutex
	starts   int
	stops    int
	startErr error
}

func (f *fakeDSHProcess) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	return f.startErr
}

func (f *fakeDSHProcess) Stop() {
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()
}

func (f *fakeDSHProcess) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops
}

func (f *fakeDSHProcess) setStartError(err error) {
	f.mu.Lock()
	f.startErr = err
	f.mu.Unlock()
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met")
}

func waitForStatus(t *testing.T, events <-chan runnerEvent, status DSHStatus) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.status == status {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for status %d", status)
		}
	}
}
