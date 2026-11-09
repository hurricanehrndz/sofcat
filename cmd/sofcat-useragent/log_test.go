package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The agent runs with no window, so its log is the only record of a sync a
// support case can ask for: it must be written without the debug switch, at
// info level, and without the debug detail.
func TestLogWrittenWithoutDebug(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SOFCAT_DEBUG", "")
	t.Setenv("SOFCAT_UI_DEBUG", "")

	logger, writer, err := setupLogger(filepath.Join(root, identity.Name))
	if err != nil || writer == nil {
		t.Fatalf("writer=%v err=%v", writer, err)
	}
	logger.Info("user sync: placement done")
	logger.Debug("user sync: debug detail")
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, identity.Name, logName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "placement done") || strings.Contains(string(data), "debug detail") {
		t.Fatalf("log = %q, want the info record only", data)
	}
}

// With the switch, debug records are written too, to the agent's own file,
// not the storefront's ui-client.log, whose rotation it would otherwise race.
func TestLogDebugWithSwitch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SOFCAT_DEBUG", "1")

	logger, writer, err := setupLogger(filepath.Join(root, identity.Name))
	if err != nil || writer == nil {
		t.Fatalf("writer=%v err=%v", writer, err)
	}
	logger.Debug("user sync: debug detail")
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, identity.Name, logName))
	if err != nil || !strings.Contains(string(data), "debug detail") {
		t.Fatalf("log = %q, err = %v", data, err)
	}
}

// The service starts the agent on every plan change and the log is always on,
// so it must stay small: past the cap it rotates to a single backup.
func TestLogSizeCapped(t *testing.T) {
	root := t.TempDir()

	logger, writer, err := setupLogger(filepath.Join(root, identity.Name))
	if err != nil || writer == nil {
		t.Fatalf("writer=%v err=%v", writer, err)
	}
	const limit = logMaxMiB << 20
	padding := strings.Repeat("x", 1024)
	for range 3 * limit / 1024 {
		logger.Info("user sync: placement done", "padding", padding)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, identity.Name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil {
			t.Fatal(ierr)
		}
		total += info.Size()
	}
	if total > 2*limit {
		t.Errorf("%d files hold %d bytes; want at most %d (the log and one backup)", len(entries), total, 2*limit)
	}
	info, err := os.Stat(filepath.Join(dir, logName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > limit {
		t.Errorf("%s holds %d bytes; want at most %d", logName, info.Size(), limit)
	}
}

// A profile the agent cannot write to must not stop the sync: the logger
// still works and discards.
func TestLogFallsBackWhenUnwritable(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	logger, writer, err := setupLogger(filepath.Join(blocker, identity.Name))
	if err == nil || writer != nil || logger == nil {
		t.Fatalf("logger=%v writer=%v err=%v; want a discarding logger and an error", logger, writer, err)
	}
	logger.Info("must be discarded")
}
