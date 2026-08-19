package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

//go:embed build/trayicon.png
var trayIcon []byte

//go:embed updater.key.pub
var updaterPubKey []byte

// version 由构建时 ldflags 注入：-ldflags "-X main.version=0.1.3"（不带 v）
var version = "0.1.3-dev"

const (
	updateCheckTimeout        = 120 * time.Second
	updateDownloadIdleTimeout = 3 * time.Hour
)

func main() {
	appLogger, appLogFile, err := newAppLogger()
	if err != nil {
		log.Printf("application logger unavailable: %v", err)
		appLogger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	secondInstanceLaunches := make(chan application.SecondInstanceData, 1)
	var downloads *downloadManager
	app := application.New(application.Options{
		Name:                        applicationName,
		Description:                 "deepseek harness shell desktop application",
		Icon:                        appIcon,
		Logger:                      appLogger,
		DisableDefaultSignalHandler: true,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:      "com.deepseek.harness",
			EncryptionKey: singleInstanceEncryptionKey,
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				select {
				case secondInstanceLaunches <- data:
				default:
				}
			},
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		RawMessageHandler: func(window application.Window, message string, originInfo *application.OriginInfo) {
			if downloads != nil {
				downloads.handleRawMessage(window, message, originInfo)
			}
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{
			DisableQuitOnLastWindowClosed: true,
			ProgramName:                   "deepseek-harness",
		},
	})

	// 三平台统一使用标准系统标题栏（标题栏独立于内容区显示）：
	//   - macOS:  MacTitleBarDefault —— 标准可见标题栏 + 交通灯，不透明、不隐藏、内容不延伸
	//   - Windows: 默认标准标题栏（不设 Frameless / 隐藏标题栏选项）
	//   - Linux:   默认标准标题栏（GTK 客户端装饰由桌面环境提供）
	// 这样三平台 title bar 行为一致：标题、窗口按钮都由系统原生绘制。
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            applicationName,
		Width:            1280,
		Height:           860,
		MinWidth:         900,
		MinHeight:        600,
		URL:              "/", // 先加载内嵌启动页，dsh 就绪后导航到实际地址
		BackgroundColour: application.NewRGB(18, 18, 22),
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarDefault,
		},
	})
	downloads = newDownloadManager(app, window)
	app.Menu.Set(newApplicationMenu(app))
	tray := newTrayController(app, window, trayIcon, trayActions{
		beforeQuit: downloads.Shutdown,
	})
	app.OnShutdown(tray.markExiting)
	app.OnShutdown(downloads.Shutdown)
	go func() {
		for {
			select {
			case data := <-secondInstanceLaunches:
				app.Logger.Info("second instance launch", "args", data.Args, "working_dir", data.WorkingDir)
				showMainWindow(app, window)
			case <-app.Context().Done():
				return
			}
		}
	}()

	initUpdater(app)

	runner := NewDSHRunner()
	lifecycle := newDSHLifecycle(runner, func(status DSHStatus, msg string) {
		switch status {
		case DSHStarting:
			app.Logger.Info("dsh starting", "detail", msg)
			window.EmitEvent("dsh:status", msg)
		case DSHReady:
			// 服务就绪：把 WebView 导航到 dsh 实际监听地址。
			// 三重保险：
			//  1. SetURL —— 生产模式（wails3 build）下直接生效；
			//  2. EmitEvent dsh:ready —— splash 页 JS 监听后 location 跳转；
			//  3. ExecJS —— 直接注入 location.href，绕开 dev 模式 AssetServer 对导航的介入。
			app.Logger.Info("dsh ready", "url", msg)
			if err := downloads.setDSHBaseURL(msg); err != nil {
				app.Logger.Error("download bridge disabled: invalid dsh URL", "error", err.Error())
			}
			window.SetURL(msg)
			window.EmitEvent("dsh:ready", msg)
			window.ExecJS(fmt.Sprintf("window.location.href = %q;", msg))
		case DSHFailed:
			app.Logger.Error("dsh failed", "detail", msg)
			window.EmitEvent("dsh:error", msg)
		}
	})
	runner.OnStatus = lifecycle.HandleStatus

	// Start the sidecar as soon as the native WebView runtime is ready. The
	// splash event below remains as a state-replay handshake, but is not the
	// only way to start dsh: WebView2 can otherwise leave packaged Windows
	// builds stuck on the splash page if that frontend event is missed.
	var activateOnce sync.Once
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(_ *application.WindowEvent) {
		activateOnce.Do(lifecycle.SplashReady)
	})
	app.Event.On("dsh:splash-ready", func(_ *application.CustomEvent) {
		lifecycle.SplashReady()
	})
	app.Event.On("dsh:retry", func(_ *application.CustomEvent) {
		lifecycle.Retry()
	})
	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt)
	go func() {
		<-shutdownSignals
		tray.markExiting()
		downloads.Shutdown()
		lifecycle.Shutdown()
		app.Quit()
	}()
	app.OnShutdown(lifecycle.Shutdown)
	app.OnShutdown(func() { signal.Stop(shutdownSignals) })
	if appLogFile != nil {
		app.OnShutdown(func() { _ = appLogFile.Close() })
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func newApplicationMenu(app *application.App) *application.Menu {
	menu := application.NewMenu()
	if runtime.GOOS == "darwin" {
		appMenu := menu.AddSubmenu("deepseek harness shell")
		appMenu.AddRole(application.About)
		appMenu.Add("Check for Updates...").OnClick(func(_ *application.Context) {
			checkForUpdates(app)
		})
		appMenu.AddSeparator()
		appMenu.AddRole(application.ServicesMenu)
		appMenu.AddSeparator()
		appMenu.AddRole(application.Hide)
		appMenu.AddRole(application.HideOthers)
		appMenu.AddRole(application.UnHide)
		menu.AddRole(application.FileMenu)
	} else {
		fileMenu := menu.AddSubmenu("File")
		fileMenu.AddRole(application.CloseWindow)
	}
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.ViewMenu)
	menu.AddRole(application.WindowMenu)
	return menu
}

func checkForUpdates(app *application.App) {
	if app.Updater.CurrentVersion() == "" {
		app.Dialog.Error().
			SetTitle("Unable to Check for Updates").
			SetMessage("The updater is not available.").
			SetIcon(appIcon).
			Show()
		return
	}

	if err := app.Updater.CheckAndInstall(context.Background()); err != nil {
		app.Logger.Error("updater: manual check failed", "error", err.Error())
	}
}

func macOSUpdateAssetMatcher(req updater.CheckRequest, assets []github.ReleaseAsset) int {
	for index, asset := range assets {
		if !strings.HasSuffix(strings.ToLower(asset.Name), ".zip") {
			continue
		}
		if github.DefaultAssetMatcher(req, []github.ReleaseAsset{asset}) == 0 {
			return index
		}
	}
	return -1
}

func newUpdaterHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Limit connection/header stalls, but do not apply http.Client.Timeout to
	// the response body. The resumable provider owns download idle handling.
	transport.ResponseHeaderTimeout = updateCheckTimeout
	return &http.Client{Transport: transport}
}

// initUpdater 配置自动更新：
//   - provider: GitHub Releases（lunarroute26/deepseek-harness-shell）
//   - 校验:     发行版附带 SHA256SUMS，下载后按 sha256 摘要校验
//   - 公钥:     updater.key.pub 已嵌入（GitHub provider 为 digest-only 校验；
//     公钥为将来切换到带签名的 manifest/endpoint provider 预留）
//   - 时机:     启动 5 秒后静默检查一次（有新版才弹窗）；之后每 6h 后台轮询
func initUpdater(app *application.App) {
	// Windows and Linux payloads live in a directory beside the executable,
	// while the current Wails updater atomically replaces only one file there.
	// macOS updates the whole .app bundle, including Contents/Resources/payload.
	if runtime.GOOS != "darwin" {
		app.Logger.Info("updater disabled: sidecar payload requires an installer update")
		return
	}

	httpClient := newUpdaterHTTPClient()
	githubProvider, err := github.New(github.Config{
		Repository:    "lunarroute26/deepseek-harness-shell",
		AssetMatcher:  macOSUpdateAssetMatcher,
		ChecksumAsset: "SHA256SUMS",
		HTTPClient:    httpClient,
	})
	if err != nil {
		app.Logger.Error("updater: github provider init failed", "error", err.Error())
		return
	}
	gh := newResumableGitHubProvider(
		githubProvider,
		httpClient,
		updateCheckTimeout,
		updateDownloadIdleTimeout,
	)

	if err := app.Updater.Init(updater.Config{
		CurrentVersion: version,
		Providers:      []updater.Provider{gh},
		PublicKey:      updaterPubKey,
		CheckInterval:  6 * time.Hour,
	}); err != nil {
		app.Logger.Error("updater: init failed", "error", err.Error())
		return
	}

	// 启动后延迟 5 秒做一次静默检查：只有发现新版本才打开更新窗口，
	// 避免每次启动都弹出"已是最新版本"打扰用户。
	go func() {
		time.Sleep(5 * time.Second)
		checkCtx, cancelCheck := context.WithTimeout(context.Background(), updateCheckTimeout)
		rel, err := app.Updater.Check(checkCtx)
		cancelCheck()
		if err != nil {
			app.Logger.Info("updater: startup check failed (将按 CheckInterval 重试)", "error", err.Error())
			return
		}
		if rel != nil {
			app.Logger.Info("updater: update available", "version", rel.Version)
			if err := app.Updater.CheckAndInstall(context.Background()); err != nil {
				app.Logger.Error("updater: install failed", "error", err.Error())
			}
		}
	}()
}
