//go:build !zip_only

// The command installers (msi, exe, ps1, msix, nupkg) and catalog scripts.
// A zip-only build (the zip_only tag) leaves this file out, so its binary
// cannot run them; installer_zip_only.go stands in for what Install calls.

package installer

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/download"
)

var (
	// Base command for each installer type
	commandNupkg = filepath.Join(os.Getenv("ProgramData"), "chocolatey/bin/choco.exe")
	commandMsi   = filepath.Join(os.Getenv("WINDIR"), "system32/", "msiexec.exe")
	commandPs1   = filepath.Join(os.Getenv("WINDIR"), "system32/", "WindowsPowershell", "v1.0", "powershell.exe")

	// These abstractions allows us to override when testing
	execCommand = exec.Command
	runCommand  = runCMD
)

// runCommand executes a command and it's argurments in the CMD environment
func runCMD(command string, arguments []string) (string, error) {
	cmd := execCommand(command, arguments...)
	var cmdOutput []string
	cmdReader, err := cmd.StdoutPipe()
	if err != nil {
		slog.Warn("Error creating pipe to stdout", "command", command, "args", arguments, "err", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	scanner := bufio.NewScanner(cmdReader)
	slog.Debug("Running command", "command", command, "args", arguments)
	go func() {
		for scanner.Scan() {
			cmdOutput = append(cmdOutput, scanner.Text())
		}
		wg.Done()
	}()

	err = cmd.Start()
	if err != nil {
		slog.Warn("Error running command", "command", command, "args", arguments, "err", err)
	}

	wg.Wait()
	err = withStderr(cmd.Wait(), stderr.Bytes())
	output := strings.Join(cmdOutput, "\n")
	slog.Debug("Command output", "output", output)
	if err != nil {
		slog.Warn("Command error", "command", command, "args", arguments, "err", err)
	}

	return output, err
}

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

// exitCoder is the part of *exec.ExitError acceptedExitCode needs; tests use
// it to stand in codes a Unix helper process cannot exit with, such as 3010.
type exitCoder interface {
	error
	ExitCode() int
}

// acceptedExitCode reports whether err is a process exit whose code is in
// successCodes, e.g. MSI 3010 (restart required).
func acceptedExitCode(err error, successCodes []int) (int, bool) {
	var exitErr exitCoder
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	for _, code := range successCodes {
		if exitErr.ExitCode() == code {
			return code, true
		}
	}
	return 0, false
}

// Get a Nupkg's id using `choco list`
func getNupkgIDs(nupkgDir, versionArg string) ([]string, error) {
	// Compile the arguments needed to get the id
	command := commandNupkg
	arguments := []string{"list", versionArg, "--id-only", "-r", "-s", nupkgDir}

	// Run the command and parse each non-empty line as a candidate id
	cmdOut, cmdErr := runCommand(command, arguments)
	outputLines := strings.Split(cmdOut, "\n")
	ids := make([]string, 0, len(outputLines))
	for _, line := range outputLines {
		candidate := strings.TrimSpace(line)
		if candidate == "" {
			continue
		}
		ids = append(ids, candidate)
	}

	return ids, cmdErr
}

func resolveNupkgID(itemName, nupkgDir, versionArg, packageID string) (string, error) {
	explicitID := strings.TrimSpace(packageID)
	if explicitID != "" {
		return explicitID, nil
	}

	if versionArg == "" {
		return "", nil
	}

	ids, cmdErr := getNupkgIDs(nupkgDir, versionArg)
	if cmdErr != nil {
		return "", cmdErr
	}

	if len(ids) == 0 {
		return "", fmt.Errorf("no package id was found for %s; set installer/uninstaller.package_id", itemName)
	}

	if len(ids) > 1 {
		return "", fmt.Errorf("multiple package ids were found for %s: %s; set installer/uninstaller.package_id", itemName, strings.Join(ids, ", "))
	}

	return ids[0], nil
}

// typeInstaller translates a catalog item into the command and arguments for
// one installer type.
type typeInstaller interface {
	installCommand(item catalog.Item, absFile string) (cmd string, args []string, err error)
	uninstallCommand(item catalog.Item, absFile string) (cmd string, args []string, err error)
	uninstallNeedsFile() bool
	// successCodes are the non-zero exit codes this type treats as success
	// unless the catalog item sets success_codes.
	successCodes() []int
}

// msiRestartCodes are MSI's "success, restart required/initiated" exits.
// Chocolatey passes them through, so nupkg shares them.
var msiRestartCodes = []int{1641, 3010}

// typeInstallers maps each supported installer type to its implementation.
var typeInstallers = map[string]typeInstaller{
	"msi":   msiInstaller{},
	"exe":   exeInstaller{},
	"ps1":   ps1Installer{},
	"msix":  msixInstaller{},
	"nupkg": nupkgInstaller{},
}

type msiInstaller struct{}

func (msiInstaller) installCommand(item catalog.Item, absFile string) (string, []string, error) {
	args := []string{"/i", absFile, "/qn", "/norestart"}
	args = append(args, item.Installer.Arguments...)
	return commandMsi, args, nil
}

func (msiInstaller) uninstallCommand(item catalog.Item, absFile string) (string, []string, error) {
	return commandMsi, []string{"/x", absFile, "/qn", "/norestart"}, nil
}

func (msiInstaller) uninstallNeedsFile() bool { return true }

func (msiInstaller) successCodes() []int { return msiRestartCodes }

type exeInstaller struct{}

func (exeInstaller) installCommand(item catalog.Item, absFile string) (string, []string, error) {
	return absFile, item.Installer.Arguments, nil
}

func (exeInstaller) uninstallCommand(item catalog.Item, absFile string) (string, []string, error) {
	return absFile, item.Uninstaller.Arguments, nil
}

func (exeInstaller) uninstallNeedsFile() bool { return true }

// Setup.exe wrappers disagree on restart codes, so exe items list their own.
func (exeInstaller) successCodes() []int { return nil }

type ps1Installer struct{}

func (ps1Installer) installCommand(item catalog.Item, absFile string) (string, []string, error) {
	return commandPs1, []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", absFile}, nil
}

func (ps1Installer) uninstallCommand(item catalog.Item, absFile string) (string, []string, error) {
	return commandPs1, []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", absFile}, nil
}

func (ps1Installer) uninstallNeedsFile() bool { return true }

func (ps1Installer) successCodes() []int { return nil }

type msixInstaller struct{}

func (msixInstaller) installCommand(item catalog.Item, absFile string) (string, []string, error) {
	psCommand := fmt.Sprintf("Add-AppxProvisionedPackage -Online -PackagePath '%s' -SkipLicense", absFile)
	if len(item.Installer.Arguments) > 0 {
		psCommand += " " + strings.Join(item.Installer.Arguments, " ")
	}
	return commandPs1, []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psCommand}, nil
}

func (msixInstaller) uninstallCommand(item catalog.Item, absFile string) (string, []string, error) {
	if item.Check.Appx.Name == "" {
		return "", nil, fmt.Errorf("Check.Appx.Name is required for msix uninstall of %s", item.DisplayName)
	}
	removeCmd := fmt.Sprintf(
		"$pkg = Get-AppxProvisionedPackage -Online | Where-Object { $_.DisplayName -eq '%s' }; if ($pkg) { Remove-AppxProvisionedPackage -Online -PackageName $pkg.PackageName }; Get-AppxPackage -Name '%s' -AllUsers | Remove-AppxPackage -AllUsers -ErrorAction SilentlyContinue",
		item.Check.Appx.Name, item.Check.Appx.Name,
	)
	return commandPs1, []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", removeCmd}, nil
}

func (msixInstaller) uninstallNeedsFile() bool { return false }

func (msixInstaller) successCodes() []int { return nil }

type nupkgInstaller struct{}

// nupkgCommand builds the choco command shared by install and uninstall.
func nupkgCommand(verb string, item catalog.Item, absFile, packageID string) (string, []string, error) {
	// choco wants the "id" and parent dir, so we need to determine both
	slog.Info("Determining nupkg id", "item", item.DisplayName)
	nupkgDir := filepath.Dir(absFile)

	// Since choco recommends the source is a directory,
	// we need to pass a version to filter unexpected nupkgs (if we have a version)
	var versionArg string
	if item.Version != "" {
		versionArg = fmt.Sprintf("--version=%s", item.Version)
	}

	nupkgID, err := resolveNupkgID(item.DisplayName, nupkgDir, versionArg, packageID)
	if err != nil {
		return "", nil, fmt.Errorf("unable to determine nupkg id for %s: %w", item.DisplayName, err)
	}

	// Now pass the id along with the parent directory
	var args []string
	if nupkgID != "" && versionArg != "" {
		// Only use this form if we have an ID and version number
		args = []string{verb, nupkgID, "-s", nupkgDir, versionArg, "-f", "-y", "-r"}
	} else {
		// If we dont have an id and version, fallback to the method choco doesn't recommend (but works)
		args = []string{verb, absFile, "-f", "-y", "-r"}
	}
	return commandNupkg, args, nil
}

func (nupkgInstaller) installCommand(item catalog.Item, absFile string) (string, []string, error) {
	return nupkgCommand("install", item, absFile, item.Installer.PackageID)
}

func (nupkgInstaller) uninstallCommand(item catalog.Item, absFile string) (string, []string, error) {
	return nupkgCommand("uninstall", item, absFile, item.Uninstaller.PackageID)
}

func (nupkgInstaller) uninstallNeedsFile() bool { return true }

func (nupkgInstaller) successCodes() []int { return msiRestartCodes }

// actionItem is the shared execution path for installs and uninstalls:
// download if needed, build the type-specific command, run it, and record the
// honest result in the report.
func (r *Runner) actionItem(item catalog.Item, itemURL, action string) (string, error) {
	var installerItem catalog.InstallerItem
	var state, verb string
	if action == "uninstall" {
		installerItem = item.Uninstaller
		// msix items may omit the uninstaller block; infer from the installer
		if installerItem.Type == "" && item.Installer.Type == "msix" {
			installerItem.Type = "msix"
		}
		state, verb = "removing", "Uninstall"
	} else {
		installerItem = item.Installer
		state, verb = "installing", "Install"
	}

	impl, ok := typeInstallers[installerItem.Type]
	if !ok {
		err := fmt.Errorf("unsupported %ser type %q for %s", action, installerItem.Type, item.DisplayName)
		r.emit(item, "failed", NoPercent, err.Error())
		return "", r.recordFailure(item, action, err)
	}

	// Download the item if this action needs the file on disk
	var absFile string
	if action == "install" || impl.uninstallNeedsFile() {
		relPath, fileName := path.Split(installerItem.Location)
		absFile = filepath.Join(r.CachePath, relPath, fileName)
		ctx, done, ok := r.Cancels.download(item.Name)
		if !ok {
			return "", r.canceled(item)
		}
		// Progress reaches every local user, so it names the item; the URL,
		// which can carry signed-query tokens, stays in the debug log.
		slog.Debug("Downloading", "item", item.DisplayName, "url", itemURL)
		r.emit(item, "downloading", NoPercent, item.DisplayName)
		valid := download.IfNeeded(ctx, absFile, itemURL, installerItem.Hash, func(percent int) {
			r.emit(item, "downloading", percent, item.DisplayName)
		})
		withdrawn := ctx.Err() != nil // read before done(), which cancels ctx
		done()
		if withdrawn {
			return "", r.canceled(item)
		}
		if !valid {
			err := fmt.Errorf("unable to download a valid installer for %s", item.DisplayName)
			slog.Warn("Unable to download valid file", "item", item.DisplayName)
			r.emit(item, "failed", NoPercent, err.Error())
			return "", r.recordFailure(item, action, err)
		}
	}

	// Build the command via the type implementation
	var cmd string
	var args []string
	var err error
	if action == "uninstall" {
		cmd, args, err = impl.uninstallCommand(item, absFile)
	} else {
		cmd, args, err = impl.installCommand(item, absFile)
	}
	if err != nil {
		slog.Warn("Unable to build command", "item", item.DisplayName, "installerType", installerItem.Type, "err", err)
		r.emit(item, "failed", NoPercent, err.Error())
		return "", r.recordFailure(item, action, err)
	}

	// The last moment the user can withdraw the item; past this a cancel is refused.
	if !r.Cancels.act(item.Name) {
		return "", r.canceled(item)
	}

	// Run the command
	r.emit(item, state, NoPercent, item.DisplayName)
	slog.Info(verb+"ing", "item", item.DisplayName, "version", item.Version, "installerType", installerItem.Type)
	out, err := runCommand(cmd, args)
	// A listed success_codes replaces the type's defaults; an explicit empty
	// list turns them off.
	successCodes := installerItem.SuccessCodes
	if successCodes == nil {
		successCodes = impl.successCodes()
	}
	if code, ok := acceptedExitCode(err, successCodes); ok {
		slog.Info(verb+"er exited with an accepted non-zero code", "item", item.DisplayName, "code", code)
		err = nil
	}
	if err != nil {
		slog.Warn(verb+"ation FAILED", "item", item.DisplayName, "version", item.Version, "result", "error", "err", err)
		r.emit(item, "failed", NoPercent, err.Error())
		return out, r.recordFailure(item, action, err)
	}
	slog.Info(verb+"ation SUCCESSFUL", "item", item.DisplayName, "version", item.Version, "result", "success")
	// The command changed the registry, so later checks in this run (the
	// self-serve prune, updates, the inventory) must not read the old snapshot.
	r.Checker.Invalidate()

	// Add the item to the report only after the command succeeded
	if action == "uninstall" {
		r.Report.UninstalledItems = append(r.Report.UninstalledItems, item)
	} else {
		r.Report.InstalledItems = append(r.Report.InstalledItems, item)
	}
	r.emit(item, "done", 100, "")

	return out, nil
}

func (r *Runner) installItem(item catalog.Item, itemURL string) (string, error) {
	return r.actionItem(item, itemURL, "install")
}

func (r *Runner) uninstallItem(item catalog.Item, itemURL string) (string, error) {
	return r.actionItem(item, itemURL, "uninstall")
}

// runScript writes the script to a unique temporary .ps1 under cachePath,
// executes it, and removes it. kind distinguishes the temp file names.
func runScript(script, kind, cachePath string) error {
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		return err
	}

	// Write the script to disk as a Powershell file
	tmpFile, err := os.CreateTemp(cachePath, "sofcat-"+kind+"-*.ps1")
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
	closeErr := tmpFile.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}

	// Execute the script
	psArgs := []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", tmpScript}
	cmd := execCommand(commandPs1, psArgs...)
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
	// installer_zip_only.go defines the zip-only stand-ins.
	installItemFunc   = (*Runner).installItem
	uninstallItemFunc = (*Runner).uninstallItem
	itemScriptFunc    = runScript
)
