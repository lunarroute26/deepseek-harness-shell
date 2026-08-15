package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DSHStatus 表示 dsh 子进程的生命周期状态。
type DSHStatus int

const (
	DSHStarting DSHStatus = iota
	DSHReady
	DSHFailed
)

// 就绪信号示例（dsh web 启动完成后打印）：
//
//	dsh web: http://127.0.0.1:62341 (LAN: ...)
var readyURLRe = regexp.MustCompile(`(http://127\.0\.0\.1:\d+)`)

// readyTimeout 是等待 dsh 就绪的最长时间（首次启动要加载 Cordis 插件树，可能较慢）。
const readyTimeout = 90 * time.Second

// DSHRunner 负责拉起并管理 dsh 子进程。
type DSHRunner struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	started  bool
	OnStatus func(DSHStatus, string) // 状态回调，在独立 goroutine 中调用
}

func NewDSHRunner() *DSHRunner {
	return &DSHRunner{}
}

// Start 启动 dsh 子进程并异步等待就绪。失败时立即回调 DSHFailed。
func (r *DSHRunner) Start() error {
	cmd, err := buildDSHCommand()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.cmd = cmd
	r.started = true
	r.mu.Unlock()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("无法读取 dsh stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("无法读取 dsh stderr: %w", err)
	}

	// Unix 下让 dsh 自成进程组，便于整树回收
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 dsh 失败: %w", err)
	}

	// stderr 旁路到日志文件，避免管道阻塞
	logPath := dshLogPath()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		go io.Copy(logFile, stderr)
	} else {
		go io.Copy(io.Discard, stderr)
	}

	go r.watch(cmd, stdout)
	return nil
}

// watch 读取 stdout，解析就绪地址；进程意外退出时回调失败。
func (r *DSHRunner) watch(cmd *exec.Cmd, stdout io.Reader) {
	ctx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()

	urlCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if m := readyURLRe.FindStringSubmatch(line); len(m) > 1 {
				urlCh <- m[1]
				return
			}
		}
	}()

	select {
	case url := <-urlCh:
		r.notify(DSHReady, url)
	case <-ctx.Done():
		r.notify(DSHFailed, "等待 dsh 服务就绪超时（90s）。请查看日志: "+dshLogPath())
		_ = cmd.Process.Kill()
	case err := <-waitCh(cmd):
		r.notify(DSHFailed, fmt.Sprintf("dsh 进程意外退出: %v（日志: %s）", err, dshLogPath()))
	}
}

func waitCh(cmd *exec.Cmd) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- cmd.Wait() }()
	return ch
}

func (r *DSHRunner) notify(status DSHStatus, msg string) {
	if r.OnStatus != nil {
		r.OnStatus(status, msg)
	}
}

// Stop 停止 dsh 子进程及其进程树，幂等。
func (r *DSHRunner) Stop() {
	r.mu.Lock()
	cmd := r.cmd
	cancel := r.cancel
	r.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	killProcessTree(cmd)
	if cancel != nil {
		cancel()
	}
}

// ---------- 命令构造 ----------

// buildDSHCommand 构造 dsh 启动命令。
// 优先级：
//  1. DSH_LAUNCH：完整命令行覆盖（支持双引号路径）
//  2. DSH_REPO / DSH_HOME：仓库根 → node <repo>/apps/cli/lib/bin.js --profile web --port 0
//  3. 二进制旁的 deepseek-harness/（分发目录）
//  4. 当前目录或其父目录下的兄弟 deepseek-harness/（开发便利：壳子与仓库同目录布局）
//  5. PATH 中的 dsh 命令（npm 全局安装）
func buildDSHCommand() (*exec.Cmd, error) {
	if v := strings.TrimSpace(os.Getenv("DSH_LAUNCH")); v != "" {
		parts := splitLaunch(v)
		if len(parts) == 0 {
			return nil, fmt.Errorf("DSH_LAUNCH 为空")
		}
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Env = os.Environ()
		return cmd, nil
	}

	nodeBin, err := resolveNode()
	if err != nil {
		return nil, err
	}

	if entry, repoRoot, ok := resolveRepoEntry(); ok {
		args := []string{
			entry,
			"--profile", "web",
			"--host", "127.0.0.1",
			"--port", "0", // 让 OS 分配空闲端口，避免冲突
		}
		cmd := exec.Command(nodeBin, args...)
		cmd.Dir = repoRoot
		cmd.Env = os.Environ()
		return cmd, nil
	}

	if p, err := exec.LookPath("dsh"); err == nil {
		cmd := exec.Command(p, "web", "--host", "127.0.0.1", "--port", "0")
		cmd.Env = os.Environ()
		return cmd, nil
	}

	return nil, fmt.Errorf(
		"找不到 dsh 入口。请设置 DSH_REPO=<deepseek-harness 仓库根目录>，或 DSH_LAUNCH=<完整启动命令>，或安装 dsh 到 PATH")
}

// resolveNode 依次尝试：DSH_NODE → 二进制旁 runtime/bin/node → PATH
func resolveNode() (string, error) {
	if p := strings.TrimSpace(os.Getenv("DSH_NODE")); p != "" {
		if fileExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("DSH_NODE 指向的文件不存在: %s", p)
	}
	if exe, err := os.Executable(); err == nil {
		bin := "node"
		if runtime.GOOS == "windows" {
			bin = "node.exe"
		}
		cand := filepath.Join(filepath.Dir(exe), "runtime", "bin", bin)
		if fileExists(cand) {
			return cand, nil
		}
	}
	if p, err := exec.LookPath("node"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("未找到 Node.js。请安装 Node >= 22.19 并加入 PATH，或设置 DSH_NODE 指向 node 可执行文件")
}

// resolveRepoEntry 查找仓库入口（优先发布产物 lib/bin.js，回退源码 src/bin.ts）。
// 返回 (入口文件, 仓库根, 是否找到)。
func resolveRepoEntry() (string, string, bool) {
	candidates := []string{}
	if v := os.Getenv("DSH_REPO"); v != "" {
		candidates = append(candidates, v)
	}
	if v := os.Getenv("DSH_HOME"); v != "" {
		candidates = append(candidates, v)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "deepseek-harness"))
	}
	// 开发便利：壳子目录或其父目录下的兄弟仓库
	//（典型布局：.../desktop/deepseek-harness-shell 与 .../desktop/deepseek-harness 并列）
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "deepseek-harness"))
		candidates = append(candidates, filepath.Join(filepath.Dir(wd), "deepseek-harness"))
	}
	for _, root := range candidates {
		// 发布产物：lib/bin.js（tsdown 打包，规避 tsx 直跑 TS 的 const enum 兼容问题）
		built := filepath.Join(root, "apps", "cli", "lib", "bin.js")
		if fileExists(built) {
			return built, root, true
		}
		// 源码模式：src/bin.ts（需 tsx，且仓库里相关包已构建）
		src := filepath.Join(root, "apps", "cli", "src", "bin.ts")
		if fileExists(src) {
			return src, root, true
		}
	}
	return "", "", false
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// splitLaunch 简单拆分 DSH_LAUNCH（支持双引号包裹的路径）。
func splitLaunch(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case (r == ' ' || r == '\t') && !inQuote:
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// ---------- 日志路径 ----------

func dshLogPath() string {
	dir := os.TempDir()
	if runtime.GOOS == "windows" {
		dir = os.Getenv("TEMP")
		if dir == "" {
			dir = os.TempDir()
		}
	}
	return filepath.Join(dir, "deepseek-harness-dsh.log")
}
