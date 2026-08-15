package main

import "sync"

type dshProcess interface {
	Start() error
	Stop()
}

// dshLifecycle serialises start, retry, and shutdown operations and retains
// the latest state so a reloaded splash page can recover missed events.
type dshLifecycle struct {
	mu        sync.Mutex
	operation sync.Mutex
	runner    dshProcess
	publish   func(DSHStatus, string)

	activated bool
	busy      bool
	closed    bool
	status    DSHStatus
	message   string
}

func newDSHLifecycle(runner dshProcess, publish func(DSHStatus, string)) *dshLifecycle {
	return &dshLifecycle{
		runner:  runner,
		publish: publish,
		status:  DSHStarting,
		message: "正在启动本地服务...",
	}
}

// SplashReady is called only after the page has installed all event listeners.
// The first call starts dsh; subsequent calls replay the current state.
func (l *dshLifecycle) SplashReady() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	shouldStart := !l.activated
	l.activated = true
	if shouldStart {
		l.busy = true
		l.status = DSHStarting
		l.message = "正在启动本地服务..."
	}
	status, message := l.status, l.message
	l.mu.Unlock()

	l.publish(status, message)
	if shouldStart {
		go l.start(false)
	}
}

// Retry accepts one retry only while the retained state is failed.
func (l *dshLifecycle) Retry() {
	l.mu.Lock()
	if l.closed || !l.activated || l.busy || l.status != DSHFailed {
		l.mu.Unlock()
		return
	}
	l.busy = true
	l.status = DSHStarting
	l.message = "正在重试..."
	status, message := l.status, l.message
	l.mu.Unlock()

	l.publish(status, message)
	go l.start(true)
}

func (l *dshLifecycle) start(restart bool) {
	l.operation.Lock()
	defer l.operation.Unlock()

	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		return
	}

	if restart {
		l.runner.Stop()
	}
	if err := l.runner.Start(); err != nil {
		l.HandleStatus(DSHFailed, err.Error())
		return
	}

	l.mu.Lock()
	l.busy = false
	l.mu.Unlock()
}

func (l *dshLifecycle) HandleStatus(status DSHStatus, message string) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.status = status
	l.message = message
	if status == DSHReady || status == DSHFailed {
		l.busy = false
	}
	activated := l.activated
	l.mu.Unlock()

	if activated {
		l.publish(status, message)
	}
}

func (l *dshLifecycle) Shutdown() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	l.mu.Unlock()

	// If Start is in flight, wait for it and then stop the resulting process.
	l.operation.Lock()
	l.runner.Stop()
	l.operation.Unlock()
}
