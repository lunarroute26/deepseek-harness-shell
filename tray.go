package main

import (
	"runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const applicationName = "deepseek harness shell"

var singleInstanceEncryptionKey = [32]byte{
	0x75, 0x0e, 0x91, 0xa2, 0x89, 0xd9, 0x31, 0xe1,
	0x42, 0xf7, 0x5b, 0x85, 0x34, 0x56, 0x18, 0x56,
	0xd1, 0x29, 0x4e, 0xac, 0x7e, 0xb0, 0xb7, 0x11,
	0xe9, 0xd5, 0xb2, 0x7a, 0x1f, 0x6c, 0xbc, 0x10,
}

type trayController struct {
	app           *application.App
	window        *application.WebviewWindow
	systemTray    *application.SystemTray
	exitRequested atomic.Bool
	quit          func()
	beforeQuit    func()
}

type trayActions struct {
	beforeQuit func()
}

func newTrayController(
	app *application.App,
	window *application.WebviewWindow,
	icon []byte,
	actions trayActions,
) *trayController {
	controller := &trayController{
		app:        app,
		window:     window,
		quit:       app.Quit,
		beforeQuit: actions.beforeQuit,
	}

	window.RegisterHook(events.Common.WindowClosing, controller.handleWindowClosing)

	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(icon)
	} else {
		tray.SetIcon(icon)
	}
	tray.SetTooltip(applicationName)

	menu := application.NewMenu()
	menu.Add("打开主界面").OnClick(func(_ *application.Context) {
		controller.showMainWindow()
	})
	menu.AddSeparator()
	menu.Add("退出 " + applicationName).OnClick(func(_ *application.Context) {
		controller.requestExit()
	})
	tray.SetMenu(menu)
	tray.OnClick(controller.showMainWindow)
	tray.OnRightClick(tray.OpenMenu)
	controller.systemTray = tray

	if runtime.GOOS == "darwin" {
		app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(_ *application.ApplicationEvent) {
			controller.showMainWindow()
		})
	}

	return controller
}

func (controller *trayController) handleWindowClosing(event *application.WindowEvent) {
	if controller.exitRequested.Load() {
		return
	}
	controller.window.Hide()
	event.Cancel()
}

func (controller *trayController) showMainWindow() {
	showMainWindow(controller.app, controller.window)
}

func showMainWindow(app *application.App, window *application.WebviewWindow) {
	if app == nil || window == nil || window.NativeWindow() == nil {
		return
	}
	application.InvokeSync(func() {
		app.Show()
		window.Restore()
		window.Show()
		window.Focus()
	})
}

func (controller *trayController) requestExit() bool {
	if !controller.exitRequested.CompareAndSwap(false, true) {
		return false
	}
	if controller.beforeQuit != nil {
		controller.beforeQuit()
	}
	controller.quit()
	return true
}

func (controller *trayController) markExiting() {
	controller.exitRequested.Store(true)
}
