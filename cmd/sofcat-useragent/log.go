package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// logName is the agent's own log file, so it never rotates the storefront's
// ui-client.log.
const logName = "useragent.log"

// setupLogger discards everything unless SOFCAT_DEBUG=1 or SOFCAT_UI_DEBUG=1,
// the switches the storefront honours and the helper did when it was part of
// it. Then it logs at debug level to
// %LOCALAPPDATA%\<Name>\useragent.log, beside the receipts. A failure leaves
// a usable, discarding logger. It is ui/log.go's setup with its own file;
// both are a few lines in package main, so neither is shared.
func setupLogger() (*slog.Logger, *lumberjack.Logger, error) {
	if os.Getenv("SOFCAT_DEBUG") != "1" && os.Getenv("SOFCAT_UI_DEBUG") != "1" {
		return slog.New(slog.DiscardHandler), nil, nil
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return slog.New(slog.DiscardHandler), nil, errors.New("LOCALAPPDATA is not set")
	}
	directory := filepath.Join(localAppData, identity.Name)
	if err := os.MkdirAll(directory, 0o700); err != nil { //nolint:gosec // G703: LOCALAPPDATA comes from the user's own session, which the agent runs as
		return slog.New(slog.DiscardHandler), nil, err
	}
	writer := &lumberjack.Logger{
		Filename:   filepath.Join(directory, logName),
		MaxSize:    10,
		MaxBackups: 1,
	}
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug})), writer, nil
}
