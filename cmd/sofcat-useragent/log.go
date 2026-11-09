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

// logMaxMiB caps useragent.log; at the cap it rotates to one backup, so the
// agent, which the service starts on every plan change, keeps at most twice
// this in the user's profile. lumberjack counts whole MiB, so 1 is its floor.
const logMaxMiB = 1

// setupLogger always logs to %LOCALAPPDATA%\<Name>\useragent.log, beside the
// receipts: at info level, or debug with SOFCAT_DEBUG=1 or
// SOFCAT_UI_DEBUG=1, the switches the storefront honours. It rotates with
// lumberjack, as the service's own log does, but at logMaxMiB with one
// backup. A failure leaves a usable, discarding logger, so the sync still
// runs. It is ui/log.go's setup with its own file; both are a few lines in
// package main, so neither is shared.
func setupLogger() (*slog.Logger, *lumberjack.Logger, error) {
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
		MaxSize:    logMaxMiB,
		MaxBackups: 1,
	}
	level := slog.LevelInfo
	if os.Getenv("SOFCAT_DEBUG") == "1" || os.Getenv("SOFCAT_UI_DEBUG") == "1" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})), writer, nil
}
