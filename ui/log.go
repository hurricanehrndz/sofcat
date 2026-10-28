package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

func setupLogger() (*slog.Logger, *lumberjack.Logger, error) {
	if os.Getenv("SOFCAT_UI_DEBUG") != "1" && os.Getenv("SOFCAT_DEBUG") != "1" {
		return slog.New(slog.DiscardHandler), nil, nil
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return slog.New(slog.DiscardHandler), nil, errors.New("LOCALAPPDATA is not set")
	}

	directory := filepath.Join(localAppData, identity.Slug)
	if err := os.MkdirAll(directory, 0o700); err != nil { //nolint:gosec // G703: LOCALAPPDATA comes from the user's own session, which the UI runs as
		return slog.New(slog.DiscardHandler), nil, err
	}

	writer := &lumberjack.Logger{
		Filename:   filepath.Join(directory, "ui-client.log"),
		MaxSize:    10,
		MaxBackups: 1,
	}
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug})), writer, nil
}
