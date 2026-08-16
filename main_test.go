package main

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

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
