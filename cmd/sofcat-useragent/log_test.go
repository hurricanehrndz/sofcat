package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The service starts the agent on every plan change, so without the debug
// switch it must leave nothing behind in the user's profile.
func TestLogOffByDefault(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("SOFCAT_DEBUG", "")
	t.Setenv("SOFCAT_UI_DEBUG", "")

	logger, writer, err := setupLogger()
	if err != nil || writer != nil {
		t.Fatalf("writer=%v err=%v", writer, err)
	}
	logger.Info("must be discarded")
	if _, err := os.Stat(filepath.Join(root, identity.Name)); !os.IsNotExist(err) {
		t.Fatalf("a path was created: %v", err)
	}
}

// With the switch, the agent writes its own file, not the storefront's
// ui-client.log, whose rotation it would otherwise race.
func TestLogOwnFileWithDebug(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("SOFCAT_DEBUG", "1")

	logger, writer, err := setupLogger()
	if err != nil || writer == nil {
		t.Fatalf("writer=%v err=%v", writer, err)
	}
	logger.Debug("user sync: placement done")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, identity.Name, "useragent.log")); err != nil {
		t.Fatal(err)
	}
}
