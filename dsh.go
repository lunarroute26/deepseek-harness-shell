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
	mu         sync.Mutex
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	generation uint64
	command    func() (*exec.Cmd, error)
	timeout    time.Duration
	OnStatus   func(DSHStatus, string) // 状态回调，在独立 goroutine 中调用
}

func NewDSHRunner() *DSHRunner {
	return &DSHRunner{
		command: buildDSHCommand,
		timeout: readyTimeout,
	}
}

// Start 启动 dsh 子进程并异步等待就绪。失败时立即回调 DSHFailed。
func (r *DSHRunner) Start() error {
	r.mu.Lock()
	if r.cmd != nil {
		r.mu.Unlock()
		return fmt.Errorf("dsh 服务已经在运行")
	}
	r.generation++
	generation := r.generation
	r.mu.Unlock()

	command := r.command
	if command == nil {
		command = buildDSHCommand
	}
	cmd, err := command()
	if err != nil {
		appendDSHLog("启动前检查失败: %v", err)
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("无法读取 dsh stdout: %w", err)
	}
	logFile, err := os.OpenFile(dshLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("无法打开 dsh 日志: %w", err)
	}
	fmt.Fprintf(logFile, "\n[%s] 启动: %s\n", time.Now().Format(time.RFC3339), cmd.String())
	cmd.Stderr = logFile

	// 后台启动 dsh；Unix 下创建独立进程组，Windows 下禁止弹出控制台窗口。
	configureBackgroundProcess(cmd)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(logFile, "启动进程失败: %v\n", err)
		_ = logFile.Close()
		return fmt.Errorf("启动 dsh 失败: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	if generation != r.generation {
		r.mu.Unlock()
		cancel()
		killProcessTree(cmd)
		_ = logFile.Close()
		return fmt.Errorf("dsh 启动已取消")
	}
	r.cmd = cmd
	r.cancel = cancel
	r.mu.Unlock()

	go r.watch(ctx, generation, cmd, io.TeeReader(stdout, logFile), logFile)
	return nil
}

// watch 读取 stdout，解析就绪地址；进程意外退出时回调失败。
func (r *DSHRunner) watch(ctx context.Context, generation uint64, cmd *exec.Cmd, stdout io.Reader, logFile *os.File) {
	defer logFile.Close()
	urlCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		sent := false
		for scanner.Scan() {
			line := scanner.Text()
			if m := readyURLRe.FindStringSubmatch(line); !sent && len(m) > 1 {
				urlCh <- m[1]
				sent = true
			}
		}
	}()

	exitCh := waitCh(cmd)
	timeout := r.timeout
	if timeout <= 0 {
		timeout = readyTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ready := false

	for {
		select {
		case url := <-urlCh:
			if !ready {
				ready = true
				timer.Stop()
				r.notifyFor(generation, DSHReady, url)
			}
		case <-ctx.Done():
			return
		case <-timer.C:
			r.notifyFor(generation, DSHFailed, fmt.Sprintf("等待 dsh 服务就绪超时（%s）。请查看日志: %s", timeout, dshLogPath()))
			killProcessTree(cmd)
			r.clearCommand(generation)
			return
		case err := <-exitCh:
			r.clearCommand(generation)
			if ctx.Err() == nil {
				r.notifyFor(generation, DSHFailed, fmt.Sprintf("dsh 进程意外退出: %v（日志: %s）", err, dshLogPath()))
			}
			return
		}
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

func (r *DSHRunner) notifyFor(generation uint64, status DSHStatus, msg string) {
	r.mu.Lock()
	current := generation == r.generation
	r.mu.Unlock()
	if current {
		r.notify(status, msg)
	}
}

func (r *DSHRunner) clearCommand(generation uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if generation == r.generation {
		r.cmd = nil
		r.cancel = nil
	}
}

// Stop 停止 dsh 子进程及其进程树，幂等。
func (r *DSHRunner) Stop() {
	r.mu.Lock()
	cmd := r.cmd
	cancel := r.cancel
	r.generation++
	r.cmd = nil
	r.cancel = nil
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
//  2. DSH_REPO：仓库根 → node <repo>/apps/cli/lib/bin.js --profile web --port 0
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

	var cmd *exec.Cmd
	if entry, repoRoot, ok := resolveRepoEntry(); ok {
		args := []string{
			entry,
			"--profile", "web",
			"--host", "127.0.0.1",
			"--port", "0", // 让 OS 分配空闲端口，避免冲突
		}
		cmd = exec.Command(nodeBin, args...)
		cmd.Dir = repoRoot
		cmd.Env = os.Environ()
	} else if p, err := exec.LookPath("dsh"); err == nil {
		cmd = exec.Command(p, "web", "--host", "127.0.0.1", "--port", "0")
		cmd.Env = os.Environ()
	} else {
		return nil, fmt.Errorf(
			"找不到 dsh 入口。请设置 DSH_REPO=<deepseek-harness 仓库根目录>，或 DSH_LAUNCH=<完整启动命令>，或安装 dsh 到 PATH")
	}

	backup, err := migrateLegacyProfileFallback()
	if err != nil {
		return nil, fmt.Errorf("无法迁移旧版 dsh profile 模块缓存: %w", err)
	}
	if backup != "" {
		appendDSHLog("检测到旧版 profile 模块缓存，已备份到: %s", backup)
	}
	return cmd, nil
}

// migrateLegacyProfileFallback preserves and moves aside old physical package
// trees from profiles/node_modules. Current dsh owns this directory and
// rebuilds it from installation junctions on every launch; profile manifests,
// patches, sessions, and databases live elsewhere and are not touched.
func migrateLegacyProfileFallback() (string, error) {
	home, err := dshDataHome()
	if err != nil {
		return "", err
	}
	modulesDir := filepath.Join(home, "profiles", "node_modules")
	legacy, err := hasPhysicalFallbackPackages(modulesDir)
	if err != nil || !legacy {
		return "", err
	}

	backupRoot := filepath.Join(home, "backups")
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		return "", fmt.Errorf("无法创建备份目录 %s: %w", backupRoot, err)
	}
	backup := filepath.Join(
		backupRoot,
		fmt.Sprintf("profile-node_modules-%s-%d", time.Now().Format("20060102-150405"), os.Getpid()),
	)
	if err := os.Rename(modulesDir, backup); err != nil {
		// A concurrent shell launch may have completed the same migration.
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("无法将 %s 备份到 %s: %w", modulesDir, backup, err)
	}
	return backup, nil
}

func dshDataHome() (string, error) {
	configured := strings.TrimSpace(os.Getenv("DSH_HOME"))
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户目录: %w", err)
	}
	if configured == "" {
		return filepath.Join(userHome, ".dsh"), nil
	}
	if configured == "~" {
		return userHome, nil
	}
	if strings.HasPrefix(configured, "~/") || strings.HasPrefix(configured, `~\`) {
		configured = filepath.Join(userHome, configured[2:])
	}
	absolute, err := filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("无法解析 DSH_HOME %q: %w", configured, err)
	}
	return absolute, nil
}

func hasPhysicalFallbackPackages(modulesDir string) (bool, error) {
	entries, err := os.ReadDir(modulesDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("无法读取 %s: %w", modulesDir, err)
	}
	for _, entry := range entries {
		path := filepath.Join(modulesDir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return false, fmt.Errorf("无法检查 %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		// Scoped package containers are real directories; the package entries
		// immediately below them must be links managed by dsh.
		if info.IsDir() && strings.HasPrefix(entry.Name(), "@") {
			packages, err := os.ReadDir(path)
			if err != nil {
				return false, fmt.Errorf("无法读取 %s: %w", path, err)
			}
			for _, pkg := range packages {
				packagePath := filepath.Join(path, pkg.Name())
				packageInfo, err := os.Lstat(packagePath)
				if err != nil {
					return false, fmt.Errorf("无法检查 %s: %w", packagePath, err)
				}
				if packageInfo.Mode()&os.ModeSymlink == 0 {
					return true, nil
				}
			}
			continue
		}
		return true, nil
	}
	return false, nil
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
		for _, cand := range []string{
			filepath.Join(resourceRootForExecutable(exe, runtime.GOOS), "payload", "runtime", "bin", bin),
			filepath.Join(filepath.Dir(exe), "runtime", "bin", bin),
		} {
			if fileExists(cand) {
				return cand, nil
			}
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
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(resourceRootForExecutable(exe, runtime.GOOS), "payload", "dsh"))
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "deepseek-harness"))
	}
	// 开发便利：壳子目录或其父目录下的兄弟仓库
	//（典型布局：.../desktop/deepseek-harness-shell 与 .../desktop/deepseek-harness 并列）
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "deepseek-harness"))
		candidates = append(candidates, filepath.Join(filepath.Dir(wd), "deepseek-harness"))
	}
	for _, root := range candidates {
		// 随包生产闭包：pnpm deploy 的包根目录。
		deployed := filepath.Join(root, "lib", "bin.js")
		if fileExists(deployed) {
			return deployed, root, true
		}
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

// resourceRootForExecutable 返回平台资源根。macOS 正式包把 payload 放在
// Contents/Resources；其他平台以及裸二进制都使用可执行文件所在目录。
func resourceRootForExecutable(executable, goos string) string {
	dir := filepath.Dir(filepath.Clean(executable))
	if goos == "darwin" && filepath.Base(dir) == "MacOS" {
		contents := filepath.Dir(dir)
		if filepath.Base(contents) == "Contents" {
			return filepath.Join(contents, "Resources")
		}
	}
	return dir
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

func appendDSHLog(format string, args ...any) {
	file, err := os.OpenFile(dshLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "[%s] ", time.Now().Format(time.RFC3339))
	fmt.Fprintf(file, format, args...)
	fmt.Fprintln(file)
}
