package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

// logName is the agent's own log file, so it never rotates the storefront's
// ui-client.log.
const logName = "useragent.log"

// logMaxMiB caps useragent.log; at the cap it rotates to one backup, so the
// agent, which the service starts on every plan change, keeps at most twice
// this in the user's profile. lumberjack counts whole MiB, so 1 is its floor.
// The cap is best effort: when two agents share the file (a logon start and a
// service spawn in one session), the one that hits the cap cannot rename the
// file the other holds open, so its later writes are dropped and the file can
// pass the cap until the other exits. The sync is unaffected.
const logMaxMiB = 1

// setupLogger always logs to useragent.log in directory, the user's state
// directory (userState) beside the receipts: at info level, or debug with
// SOFCAT_DEBUG=1 or SOFCAT_UI_DEBUG=1, the switches the storefront honours.
// It rotates with lumberjack, as the service's own log does, but at
// logMaxMiB with one backup. A failure, or no directory, leaves a usable,
// discarding logger, so the sync still runs. It is ui/log.go's setup with
// its own file; both are a few lines in package main, so neither is shared.
func setupLogger(directory string) (*slog.Logger, *lumberjack.Logger, error) {
	if directory == "" {
		return slog.New(slog.DiscardHandler), nil, errors.New("no user state directory")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
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
