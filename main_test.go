package main

import (
	"net/http"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

func TestUpdaterHTTPClientHasNoWholeBodyTimeout(t *testing.T) {
	client := newUpdaterHTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("HTTP client timeout = %s, want 0", client.Timeout)
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTP client transport = %T, want *http.Transport", client.Transport)
	}
	if transport.ResponseHeaderTimeout != updateCheckTimeout {
		t.Fatalf(
			"response header timeout = %s, want %s",
			transport.ResponseHeaderTimeout,
			updateCheckTimeout,
		)
	}
}

func TestMacOSUpdateAssetMatcherPrefersAppArchive(t *testing.T) {
	assets := []github.ReleaseAsset{
		{Name: "deepseek-harness-shell-darwin-arm64.dmg"},
		{Name: "deepseek-harness-shell-darwin-arm64.zip"},
		{Name: "deepseek-harness-shell-darwin-amd64.zip"},
	}

	got := macOSUpdateAssetMatcher(updater.CheckRequest{
		Platform: "darwin",
		Arch:     "arm64",
	}, assets)
	if got != 1 {
		t.Fatalf("macOSUpdateAssetMatcher() = %d, want 1", got)
	}
}

func TestMacOSUpdateAssetMatcherRejectsInstallerAndWrongArchitecture(t *testing.T) {
	assets := []github.ReleaseAsset{
		{Name: "deepseek-harness-shell-darwin-arm64.dmg"},
		{Name: "deepseek-harness-shell-darwin-amd64.zip"},
	}

	got := macOSUpdateAssetMatcher(updater.CheckRequest{
		Platform: "darwin",
		Arch:     "arm64",
	}, assets)
	if got != -1 {
		t.Fatalf("macOSUpdateAssetMatcher() = %d, want -1", got)
	}
}
