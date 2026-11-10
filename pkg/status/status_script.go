package status

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// scriptCheckFunc runs a catalog item's check.script; tests replace it.
var scriptCheckFunc = checkScript

func checkScript(catalogItem catalog.Item, cachePath string, installType string) (actionNeeded bool, checkErr error) {
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		return false, err
	}

	// Write InstallCheckScript to disk as a Powershell file
	tmpFile, err := os.CreateTemp(cachePath, "sofcat-check-*.ps1")
	if err != nil {
		return false, err
	}
	tmpScript := tmpFile.Name()
	defer func() {
		if removeErr := os.Remove(tmpScript); removeErr != nil && !os.IsNotExist(removeErr) {
			slog.Warn("Unable to remove temporary check script", "path", tmpScript, "err", removeErr)
		}
	}()
	_, writeErr := tmpFile.WriteString(catalogItem.Check.Script)
	closeErr := tmpFile.Close()
	if writeErr != nil {
		return false, writeErr
	}
	if closeErr != nil {
		return false, closeErr
	}

	// Build the command to execute the script
	psCmd := filepath.Join(os.Getenv("WINDIR"), "system32/", "WindowsPowershell", "v1.0", "powershell.exe")
	psArgs := []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", tmpScript}

	// Execute the script
	cmd := execCommand(psCmd, psArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	cmdSuccess := cmd.ProcessState.Success()
	outStr, errStr := stdout.String(), stderr.String()

	// Log results
	slog.Debug("Command error", "err", err)
	slog.Debug("Command stdout", "stdout", outStr)
	slog.Debug("Command stderr", "stderr", errStr)

	actionNeeded = false
	// Application not installed if exit 0
	if installType == "uninstall" {
		actionNeeded = !cmdSuccess
	} else if installType == "install" || installType == "update" {
		actionNeeded = cmdSuccess
	}

	return actionNeeded, checkErr
}
