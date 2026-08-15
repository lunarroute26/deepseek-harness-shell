package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed updater.key.pub
var updaterPubKey []byte

// version 由构建时 ldflags 注入：-ldflags "-X main.version=0.1.0"（不带 v）
var version = "0.1.0-dev"

func main() {
	app := application.New(application.Options{
		Name:        "DeepSeek Harness",
		Description: "DeepSeek Harness 桌面壳（Wails v3 + dsh web）",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{
			ProgramName: "deepseek-harness",
		},
	})

	// 三平台统一使用标准系统标题栏（标题栏独立于内容区显示）：
	//   - macOS:  MacTitleBarDefault —— 标准可见标题栏 + 交通灯，不透明、不隐藏、内容不延伸
	//   - Windows: 默认标准标题栏（不设 Frameless / 隐藏标题栏选项）
	//   - Linux:   默认标准标题栏（GTK 客户端装饰由桌面环境提供）
	// 这样三平台 title bar 行为一致：标题、窗口按钮都由系统原生绘制。
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "DeepSeek Harness",
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

	initUpdater(app)

	runner := NewDSHRunner()
	runner.OnStatus = func(status DSHStatus, msg string) {
		switch status {
		case DSHReady:
			// 服务就绪：把 WebView 导航到 dsh 实际监听地址。
			// 三重保险：
			//  1. SetURL —— 生产模式（wails3 build）下直接生效；
			//  2. EmitEvent dsh:ready —— splash 页 JS 监听后 location 跳转；
			//  3. ExecJS —— 直接注入 location.href，绕开 dev 模式 AssetServer 对导航的介入。
			app.Logger.Info("dsh ready", "url", msg)
			window.SetURL(msg)
			window.EmitEvent("dsh:ready", msg)
			window.ExecJS(fmt.Sprintf("window.location.href = %q;", msg))
		case DSHFailed:
			app.Logger.Error("dsh failed", "detail", msg)
			window.EmitEvent("dsh:error", msg)
		}
	}

	if err := runner.Start(); err != nil {
		app.Logger.Error("failed to start dsh", "error", err.Error())
		window.EmitEvent("dsh:error", err.Error())
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}

	// 窗口全部关闭、应用退出后，回收 dsh 子进程
	runner.Stop()
}

// initUpdater 配置自动更新：
//   - provider: GitHub Releases（lunarroute26/deepseek-harness-shell）
//   - 校验:     发行版附带 SHA256SUMS，下载后按 sha256 摘要校验
//   - 公钥:     updater.key.pub 已嵌入（GitHub provider 为 digest-only 校验；
//              公钥为将来切换到带签名的 manifest/endpoint provider 预留）
//   - 时机:     启动 5 秒后静默检查一次（有新版才弹窗）；之后每 6h 后台轮询
func initUpdater(app *application.App) {
	gh, err := github.New(github.Config{
		Repository:    "lunarroute26/deepseek-harness-shell",
		ChecksumAsset: "SHA256SUMS",
	})
	if err != nil {
		app.Logger.Error("updater: github provider init failed", "error", err.Error())
		return
	}

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
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rel, err := app.Updater.Check(ctx)
		if err != nil {
			app.Logger.Info("updater: startup check failed (将按 CheckInterval 重试)", "error", err.Error())
			return
		}
		if rel != nil {
			app.Logger.Info("updater: update available", "version", rel.Version)
			if err := app.Updater.CheckAndInstall(ctx); err != nil {
				app.Logger.Error("updater: install failed", "error", err.Error())
			}
		}
	}()
}
