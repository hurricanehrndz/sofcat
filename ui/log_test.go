package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticsDisabledCreatesNoFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("SOFCAT_UI_DEBUG", "0")
	t.Setenv("SOFCAT_DEBUG", "0")

	logger, writer, err := setupLogger()
	if err != nil || writer != nil {
		t.Fatalf("disabled setup: writer=%v err=%v", writer, err)
	}
	logger.Debug("must be discarded")
	if _, err := os.Stat(filepath.Join(root, "sofcat")); !os.IsNotExist(err) {
		t.Fatalf("disabled diagnostics created a path: %v", err)
	}
}

func TestDiagnosticsEnabledWritesBoundedFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("SOFCAT_UI_DEBUG", "1")
	t.Setenv("SOFCAT_DEBUG", "0")

	logger, writer, err := setupLogger()
	if err != nil || writer == nil {
		t.Fatalf("enabled setup: writer=%v err=%v", writer, err)
	}
	logger.Debug("binding call completed", "operation", "ListOptionalInstalls", "result", "ok")
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	contents, err := os.ReadFile(filepath.Join(root, "sofcat", "ui-client.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `"operation":"ListOptionalInstalls"`) {
		t.Fatalf("structured diagnostic missing correlation fields: %s", contents)
	}
	if writer.MaxSize != 10 || writer.MaxBackups != 1 {
		t.Fatalf("unexpected rotation policy: %#v", writer)
	}
}

// R9 accepts either debug variable, so the shared one alone must enable the file.
func TestDiagnosticsEnabledBySofCatDebug(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("SOFCAT_UI_DEBUG", "0")
	t.Setenv("SOFCAT_DEBUG", "1")

	logger, writer, err := setupLogger()
	if err != nil || writer == nil {
		t.Fatalf("enabled setup: writer=%v err=%v", writer, err)
	}
	logger.Debug("binding call completed", "operation", "ListOptionalInstalls", "result", "ok")
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if _, err := os.Stat(filepath.Join(root, "sofcat", "ui-client.log")); err != nil {
		t.Fatalf("SOFCAT_DEBUG did not enable the log file: %v", err)
	}
}

// Logging setup failure must never break the app: a usable logger, no file.
func TestDiagnosticsSetupFailureStillReturnsLogger(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("SOFCAT_UI_DEBUG", "1")

	logger, writer, err := setupLogger()
	if err == nil || writer != nil || logger == nil {
		t.Fatalf("expected best-effort failure: logger=%v writer=%v err=%v", logger, writer, err)
	}
	logger.Debug("must not panic")
	if _, err := os.Stat(filepath.Join(root, "sofcat")); !os.IsNotExist(err) {
		t.Fatalf("failed setup created a path: %v", err)
	}
}
