package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	downloadStatusDownloading = "downloading"
	downloadStatusComplete    = "complete"
	downloadStatusError       = "error"
	downloadStatusCancelled   = "cancelled"

	downloadProgressInterval = 200 * time.Millisecond
	maxDownloadHistory       = 20
)

type downloadTaskState struct {
	ID             string `json:"id"`
	Filename       string `json:"filename"`
	Destination    string `json:"destination"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	BytesWritten   int64  `json:"bytesWritten"`
	TotalBytes     int64  `json:"totalBytes"`
	BytesPerSecond int64  `json:"bytesPerSecond"`
}

type downloadSnapshot struct {
	Tasks []downloadTaskState `json:"tasks"`
}

type downloadTask struct {
	state  downloadTaskState
	cancel context.CancelFunc
}

type downloadManager struct {
	app        *application.App
	mainWindow *application.WebviewWindow
	window     *application.WebviewWindow
	client     *http.Client

	mu          sync.Mutex
	baseURL     *url.URL
	tasks       map[string]*downloadTask
	order       []string
	windowReady bool
	dialogMu    sync.Mutex
	downloads   sync.WaitGroup
	sequence    atomic.Uint64
	shutdown    atomic.Bool
	rootContext context.Context
	rootCancel  context.CancelFunc
}

func newDownloadManager(app *application.App, mainWindow *application.WebviewWindow) *downloadManager {
	rootContext, rootCancel := context.WithCancel(app.Context())
	manager := &downloadManager{
		app:         app,
		mainWindow:  mainWindow,
		client:      newDownloadHTTPClient(),
		tasks:       make(map[string]*downloadTask),
		rootContext: rootContext,
		rootCancel:  rootCancel,
	}
	manager.window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             downloadWindowName,
		Title:            "下载任务 - " + applicationName,
		Width:            560,
		Height:           480,
		MinWidth:         420,
		MinHeight:        320,
		URL:              "/download.html",
		Hidden:           true,
		InitialPosition:  application.WindowCentered,
		BackgroundColour: application.NewRGB(245, 246, 248),
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarDefault,
		},
	})
	manager.window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if manager.shutdown.Load() {
			return
		}
		manager.window.Hide()
		event.Cancel()
	})
	manager.registerBridgeHooks()
	return manager
}

func newDownloadHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 2 * time.Minute
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("download redirects are not allowed")
		},
	}
}

func parseDSHBaseURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid dsh base URL: %w", err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
		return nil, fmt.Errorf("dsh base URL must be an HTTP 127.0.0.1 address with an explicit port")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("dsh base URL contains unsupported components")
	}
	parsed.Path = ""
	parsed.RawPath = ""
	return parsed, nil
}

func validateDownloadRequest(baseURL *url.URL, rawURL, suggestedFilename string) (*url.URL, string, error) {
	if baseURL == nil {
		return nil, "", errors.New("dsh service URL is not ready")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("invalid download URL: %w", err)
	}
	if parsed.Scheme != baseURL.Scheme || parsed.Host != baseURL.Host || parsed.User != nil || parsed.Fragment != "" {
		return nil, "", errors.New("download URL is not from the active dsh service")
	}
	if parsed.Path != "/api/session.export" {
		return nil, "", errors.New("download path is not allowed")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return nil, "", errors.New("download query is malformed")
	}
	if len(query) != 2 || len(query["sessionId"]) != 1 || strings.TrimSpace(query["sessionId"][0]) == "" ||
		len(query["includeDescendants"]) != 1 || query["includeDescendants"][0] != "true" {
		return nil, "", errors.New("download query does not match the session export contract")
	}
	filename := sanitiseZipFilename(suggestedFilename)
	if filename == "" {
		sessionID := strings.NewReplacer("/", "_", "\\", "_").Replace(query["sessionId"][0])
		filename = sanitiseZipFilename("dsh-session-" + sessionID + ".zip")
	}
	return parsed, filename, nil
}

func sanitiseZipFilename(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = filepath.Base(value)
	var result strings.Builder
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
			result.WriteRune(character)
		case character >= 'A' && character <= 'Z':
			result.WriteRune(character)
		case character >= '0' && character <= '9':
			result.WriteRune(character)
		case strings.ContainsRune("._- ", character):
			result.WriteRune(character)
		case unicode.IsSpace(character):
			result.WriteByte(' ')
		default:
			result.WriteByte('_')
		}
	}
	filename := strings.Trim(result.String(), " .")
	if filename == "" || filename == "." || filename == ".." {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".zip") {
		filename += ".zip"
	}
	const maxBaseLength = 176
	if len(filename) > maxBaseLength+4 {
		filename = strings.TrimRight(filename[:maxBaseLength], " .") + ".zip"
	}
	return filename
}

func ensureZipDestination(destination string) string {
	if strings.EqualFold(filepath.Ext(destination), ".zip") {
		return destination
	}
	return destination + ".zip"
}

func (manager *downloadManager) promptAndStart(sourceURL *url.URL, filename string) {
	if manager.shutdown.Load() {
		return
	}
	manager.dialogMu.Lock()
	destination, err := manager.app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		CanCreateDirectories: true,
		Title:                "保存 Session log",
		Filename:             filename,
		ButtonText:           "保存",
		Filters: []application.FileFilter{
			{DisplayName: "ZIP 压缩文件", Pattern: "*.zip"},
		},
		Window: manager.mainWindow,
	}).PromptForSingleSelection()
	manager.dialogMu.Unlock()
	if err != nil {
		manager.app.Logger.Error("download save dialog failed", "error", err.Error())
		manager.app.Dialog.Error().
			SetTitle("无法保存文件").
			SetMessage(err.Error()).
			AttachToWindow(manager.mainWindow).
			Show()
		return
	}
	if destination == "" || manager.shutdown.Load() {
		return
	}
	destination = ensureZipDestination(destination)

	manager.mu.Lock()
	if manager.shutdown.Load() {
		manager.mu.Unlock()
		return
	}
	manager.downloads.Add(1)
	manager.mu.Unlock()

	ctx, cancel := context.WithCancel(manager.rootContext)
	id := fmt.Sprintf("download-%d-%d", time.Now().UnixMilli(), manager.sequence.Add(1))
	task := &downloadTask{
		state: downloadTaskState{
			ID:          id,
			Filename:    filepath.Base(destination),
			Destination: destination,
			Status:      downloadStatusDownloading,
			TotalBytes:  -1,
		},
		cancel: cancel,
	}
	manager.addTask(task)
	manager.showWindow()
	go func() {
		defer manager.downloads.Done()
		manager.runTask(ctx, task, cloneURL(sourceURL))
	}()
}

func (manager *downloadManager) addTask(task *downloadTask) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.tasks[task.state.ID] = task
	manager.order = append([]string{task.state.ID}, manager.order...)
	manager.pruneHistoryLocked()
}

func (manager *downloadManager) pruneHistoryLocked() {
	if len(manager.order) <= maxDownloadHistory {
		return
	}
	kept := make([]string, 0, len(manager.order))
	for _, id := range manager.order {
		task := manager.tasks[id]
		if task == nil {
			continue
		}
		if len(kept) < maxDownloadHistory || task.state.Status == downloadStatusDownloading {
			kept = append(kept, id)
			continue
		}
		delete(manager.tasks, id)
	}
	manager.order = kept
}

func (manager *downloadManager) runTask(ctx context.Context, task *downloadTask, sourceURL *url.URL) {
	started := time.Now()
	err := streamDownload(ctx, manager.client, sourceURL, task.state.Destination, func(written, total int64) {
		manager.updateTask(task.state.ID, func(state *downloadTaskState) {
			state.BytesWritten = written
			state.TotalBytes = total
			elapsed := time.Since(started)
			if elapsed > 0 {
				state.BytesPerSecond = int64(float64(written) / elapsed.Seconds())
			}
		})
	})

	manager.updateTask(task.state.ID, func(state *downloadTaskState) {
		state.BytesPerSecond = 0
		switch {
		case err == nil:
			state.Status = downloadStatusComplete
			if state.TotalBytes < 0 {
				state.TotalBytes = state.BytesWritten
			}
		case errors.Is(err, context.Canceled):
			state.Status = downloadStatusCancelled
			state.Error = "下载已取消"
		default:
			state.Status = downloadStatusError
			state.Error = err.Error()
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		manager.app.Logger.Error("session export download failed", "url", sourceURL.String(), "error", err.Error())
	}
}

func (manager *downloadManager) updateTask(id string, update func(*downloadTaskState)) {
	manager.mu.Lock()
	if task := manager.tasks[id]; task != nil {
		update(&task.state)
	}
	manager.mu.Unlock()
	manager.publish()
}

func (manager *downloadManager) snapshot() downloadSnapshot {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	tasks := make([]downloadTaskState, 0, len(manager.order))
	for _, id := range manager.order {
		if task := manager.tasks[id]; task != nil {
			tasks = append(tasks, task.state)
		}
	}
	return downloadSnapshot{Tasks: tasks}
}

func (manager *downloadManager) publish() {
	manager.mu.Lock()
	ready := manager.windowReady
	manager.mu.Unlock()
	if !ready || manager.window == nil || manager.shutdown.Load() {
		return
	}
	payload, err := marshalForJavaScript(manager.snapshot())
	if err != nil {
		manager.app.Logger.Error("download state encoding failed", "error", err.Error())
		return
	}
	manager.window.ExecJS("window.__dshShellDownloadUpdate && window.__dshShellDownloadUpdate(" + payload + ");")
}

func marshalForJavaScript(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (manager *downloadManager) showWindow() {
	if manager.window == nil || manager.shutdown.Load() {
		return
	}
	application.InvokeSync(func() {
		manager.window.Restore()
		manager.window.Show()
		manager.window.Focus()
	})
	manager.publish()
}

func (manager *downloadManager) handleAction(action, taskID string) {
	switch action {
	case "cancel":
		manager.mu.Lock()
		task := manager.tasks[taskID]
		if task != nil && task.state.Status == downloadStatusDownloading {
			task.cancel()
		}
		manager.mu.Unlock()
	case "open", "reveal":
		manager.mu.Lock()
		task := manager.tasks[taskID]
		var destination string
		if task != nil && task.state.Status == downloadStatusComplete {
			destination = task.state.Destination
		}
		manager.mu.Unlock()
		if destination == "" {
			return
		}
		var err error
		if action == "open" {
			err = manager.app.Browser.OpenFile(destination)
		} else {
			err = manager.app.Env.OpenFileManager(destination, true)
		}
		if err != nil {
			manager.app.Logger.Error("download action failed", "action", action, "error", err.Error())
		}
	case "clear":
		manager.mu.Lock()
		kept := manager.order[:0]
		for _, id := range manager.order {
			task := manager.tasks[id]
			if task != nil && task.state.Status == downloadStatusDownloading {
				kept = append(kept, id)
			} else {
				delete(manager.tasks, id)
			}
		}
		manager.order = kept
		manager.mu.Unlock()
		manager.publish()
	}
}

func (manager *downloadManager) Shutdown() {
	if !manager.shutdown.CompareAndSwap(false, true) {
		return
	}
	manager.rootCancel()
	manager.mu.Lock()
	for _, task := range manager.tasks {
		task.cancel()
	}
	manager.mu.Unlock()
	done := make(chan struct{})
	go func() {
		manager.downloads.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		manager.app.Logger.Warn("timed out waiting for downloads to stop")
	}
}

type progressWriter struct {
	destination io.Writer
	total       int64
	written     int64
	lastUpdate  time.Time
	progress    func(written, total int64)
}

func (writer *progressWriter) Write(data []byte) (int, error) {
	written, err := writer.destination.Write(data)
	writer.written += int64(written)
	now := time.Now()
	if writer.progress != nil && (writer.lastUpdate.IsZero() || now.Sub(writer.lastUpdate) >= downloadProgressInterval) {
		writer.lastUpdate = now
		writer.progress(writer.written, writer.total)
	}
	return written, err
}

func streamDownload(
	ctx context.Context,
	client *http.Client,
	sourceURL *url.URL,
	destination string,
	progress func(written, total int64),
) (resultError error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL.String(), nil)
	if err != nil {
		return fmt.Errorf("无法创建下载请求: %w", err)
	}
	request.Header.Set("Accept", "application/zip")
	request.Header.Set("User-Agent", applicationName+"/"+version)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("下载请求失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		message := strings.TrimSpace(string(detail))
		if message == "" {
			message = response.Status
		}
		return fmt.Errorf("服务返回 HTTP %d: %s", response.StatusCode, message)
	}

	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(destination)+"-*.part")
	if err != nil {
		return fmt.Errorf("无法创建临时文件: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		if closeErr := temporary.Close(); resultError == nil && closeErr != nil && !committed {
			resultError = fmt.Errorf("无法关闭临时文件: %w", closeErr)
		}
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	writer := &progressWriter{
		destination: temporary,
		total:       response.ContentLength,
		progress:    progress,
	}
	if progress != nil {
		progress(0, response.ContentLength)
	}
	if _, err := io.CopyBuffer(writer, response.Body, make([]byte, 128*1024)); err != nil {
		return fmt.Errorf("读取下载内容失败: %w", err)
	}
	if progress != nil {
		progress(writer.written, response.ContentLength)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("无法同步下载文件: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("无法关闭下载文件: %w", err)
	}
	if err := replaceDownloadedFile(temporaryPath, destination); err != nil {
		return fmt.Errorf("无法保存下载文件: %w", err)
	}
	committed = true
	return nil
}
