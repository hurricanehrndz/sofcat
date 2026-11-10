// Catalog scripts: an item's pre/post install and uninstall scripts and its
// depends_check scripts. Every build runs them, a zip-only one included.

package installer

import (
	"bytes"
	"cmp"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	commandPs1 = filepath.Join(os.Getenv("WINDIR"), "system32/", "WindowsPowershell", "v1.0", "powershell.exe")

	// This abstraction allows us to override when testing
	execCommand = exec.Command
)

// maxStderrTail bounds how much of a failed command's stderr goes into its
// error: enough for a PowerShell error record, not a whole installer log.
const maxStderrTail = 2048

// withStderr adds the end of a failed command's stderr to its error, so the
// log and the report say why it failed, not only its exit status.
func withStderr(err error, stderr []byte) error {
	msg := bytes.TrimSpace(stderr)
	if err == nil || len(msg) == 0 {
		return err
	}
	if len(msg) > maxStderrTail {
		msg = append([]byte("..."), msg[len(msg)-maxStderrTail:]...)
	}
	return fmt.Errorf("%w: %s", err, strings.ToValidUTF8(string(msg), ""))
}

// runScript writes the script to a unique temporary .ps1 under cachePath,
// executes it, and removes it. kind distinguishes the temp file names.
func runScript(script, kind, cachePath string) error {
	psArgs := []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"}
	return runTempScript(script, "sofcat-"+kind+"-*.ps1", cachePath, func(tmpScript string) *exec.Cmd {
		return execCommand(commandPs1, append(psArgs, tmpScript)...)
	})
}

// runDependsScript runs a depends_check script for this OS: with Windows
// PowerShell on Windows, like runScript, and elsewhere as an executable, so
// the script needs a #! line. It has no time limit, like the other catalog
// scripts.
func runDependsScript(script, cachePath string) error {
	if runtime.GOOS == "windows" {
		return runScript(script, "dependscheck", cachePath)
	}
	return runTempScript(script, "sofcat-dependscheck-*", cachePath, func(tmpScript string) *exec.Cmd {
		return execCommand(tmpScript)
	})
}

// runTempScript writes script to a unique temporary file under cachePath,
// which only its owner can read, write or execute, runs the command that
// command builds for it, and removes the file.
func runTempScript(script, pattern, cachePath string, command func(tmpScript string) *exec.Cmd) error {
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		return err
	}

	// Write the script to disk
	tmpFile, err := os.CreateTemp(cachePath, pattern)
	if err != nil {
		return err
	}
	tmpScript := tmpFile.Name()
	defer func() {
		if removeErr := os.Remove(tmpScript); removeErr != nil && !os.IsNotExist(removeErr) {
			slog.Warn("Unable to remove temporary script", "path", tmpScript, "err", removeErr)
		}
	}()
	_, writeErr := tmpFile.WriteString(script)
	chmodErr := tmpFile.Chmod(0o700)
	closeErr := tmpFile.Close()
	if err = cmp.Or(writeErr, chmodErr, closeErr); err != nil {
		return err
	}

	// Execute the script
	cmd := command(tmpScript)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	// Log results
	slog.Debug("Script results", "err", err, "stdout", stdout.String(), "stderr", stderr.String())

	return withStderr(err, stderr.Bytes())
}

var (
	// By putting the functions in a variable, we can override later in tests.
	itemScriptFunc    = runScript
	dependsScriptFunc = runDependsScript
)
