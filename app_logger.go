package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

func newAppLogger() (*slog.Logger, *os.File, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, nil, fmt.Errorf("无法确定日志目录: %w", err)
	}
	logDir := filepath.Join(configDir, "deepseek harness shell")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("无法创建日志目录: %w", err)
	}
	file, err := os.OpenFile(
		filepath.Join(logDir, "deepseek-harness.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0o644,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("无法打开应用日志: %w", err)
	}
	return slog.New(slog.NewTextHandler(file, nil)), file, nil
}
