package main

import (
	_ "embed"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed frontend/download-bridge.js
var downloadBridgeSource string

const (
	downloadRequestMessageType = "dsh-shell:download-request"
)

type downloadRawMessage struct {
	Type     string `json:"type"`
	Version  int    `json:"version"`
	URL      string `json:"url,omitempty"`
	Filename string `json:"filename,omitempty"`
}

func bridgeScriptForOrigin(origin string) string {
	encoded, _ := json.Marshal(origin)
	return downloadBridgeSource + "(" + string(encoded) + ");"
}

func (manager *downloadManager) registerBridgeHooks() {
	inject := func(_ *application.WindowEvent) {
		manager.mu.Lock()
		baseURL := manager.baseURL
		manager.mu.Unlock()
		if baseURL == nil {
			return
		}
		manager.mainWindow.ExecJS(bridgeScriptForOrigin(baseURL.Scheme + "://" + baseURL.Host))
	}

	manager.mainWindow.OnWindowEvent(events.Mac.WebViewDidFinishNavigation, inject)
	manager.mainWindow.OnWindowEvent(events.Windows.WebViewNavigationCompleted, inject)
	manager.mainWindow.OnWindowEvent(events.Linux.WindowLoadFinished, inject)
}

func (manager *downloadManager) setDSHBaseURL(rawURL string) error {
	baseURL, err := parseDSHBaseURL(rawURL)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	manager.baseURL = baseURL
	manager.mu.Unlock()
	return nil
}

func (manager *downloadManager) handleRawMessage(
	window application.Window,
	message string,
	originInfo *application.OriginInfo,
) {
	var envelope downloadRawMessage
	if err := json.Unmarshal([]byte(message), &envelope); err != nil || envelope.Version != 1 {
		return
	}

	if envelope.Type != downloadRequestMessageType || window == nil || window.ID() != manager.mainWindow.ID() {
		return
	}
	manager.mu.Lock()
	baseURL := cloneURL(manager.baseURL)
	manager.mu.Unlock()
	if baseURL == nil || !downloadMessageOriginAllowed(originInfo, baseURL) {
		manager.app.Logger.Warn("download request rejected: origin mismatch")
		return
	}
	sourceURL, filename, err := validateDownloadRequest(baseURL, envelope.URL, envelope.Filename)
	if err != nil {
		manager.app.Logger.Warn("download request rejected", "error", err.Error())
		return
	}
	go manager.promptAndStart(sourceURL, filename)
}

func downloadMessageOriginAllowed(info *application.OriginInfo, baseURL *url.URL) bool {
	if info == nil || baseURL == nil {
		return false
	}
	want := baseURL.Scheme + "://" + baseURL.Host
	candidates := []string{info.Origin, info.TopOrigin}
	seen := false
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		seen = true
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Scheme+"://"+parsed.Host != want {
			return false
		}
	}
	return seen
}

func cloneURL(value *url.URL) *url.URL {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
