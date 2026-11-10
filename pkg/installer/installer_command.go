//go:build !zip_only

// The command installers (msi, exe, ps1, msix, nupkg). A zip-only build (the
// zip_only tag) leaves this file out, so its binary cannot run them;
// installer_zip_only.go stands in for what Install calls. Catalog scripts are
// in script.go, which every build compiles.

package installer

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/download"
)

var (
	// Base command for each installer type
	commandMsi = filepath.Join(os.Getenv("WINDIR"), "system32/", "msiexec.exe")

	// These abstractions allows us to override when testing
	runCommand   = runCMD
	runCommandIn = runCMDIn
)

// runCommand executes a command and it's argurments in the CMD environment
func runCMD(command string, arguments []string) (string, error) {
	return runCMDIn("", command, arguments)
}

// runCMDIn is runCMD with dir as the working directory ("" keeps sofcat's).
func runCMDIn(dir, command string, arguments []string) (string, error) {
	cmd := execCommand(command, arguments...)
	cmd.Dir = dir
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

// typeInstaller is what every installer type tells actionItem.
type typeInstaller interface {
	uninstallNeedsFile() bool
	// successCodes are the non-zero exit codes this type treats as success
	// unless the catalog item sets success_codes.
	successCodes() []int
}

// commandInstaller is a type that installs and uninstalls with one command
// line, translated from the catalog item. nupkg is the type that does not.
type commandInstaller interface {
	typeInstaller
	installCommand(item catalog.Item, absFile string) (cmd string, args []string, err error)
	uninstallCommand(item catalog.Item, absFile string) (cmd string, args []string, err error)
}

// msiRestartCodes are MSI's "success, restart required/initiated" exits.
// A nupkg script that runs an MSI passes them through, so nupkg shares them.
var msiRestartCodes = []int{1641, 3010}

// typeInstallers maps each supported installer type to its implementation.
var typeInstallers = map[string]typeInstaller{
	"msi":   msiInstaller{},
	"exe":   exeInstaller{},
	"ps1":   ps1Installer{},
	"msix":  msixInstaller{},
	"nupkg": nupkgInstaller{},
}

// The command-line types. prepareRun reaches their command methods through
// a type switch, so these keep a type that loses one from compiling.
var (
	_ commandInstaller = msiInstaller{}
	_ commandInstaller = exeInstaller{}
	_ commandInstaller = ps1Installer{}
	_ commandInstaller = msixInstaller{}
)

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

// actionItem is the shared execution path for installs and uninstalls:
// download if needed, prepare the type-specific command (or a nupkg's
// package), run it, and record the honest result in the report.
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

	// A listed success_codes replaces the type's defaults; an explicit empty
	// list turns them off.
	successCodes := installerItem.SuccessCodes
	if successCodes == nil {
		successCodes = impl.successCodes()
	}

	run, cleanup, err := r.prepareRun(impl, &item, installerItem, absFile, action, verb, successCodes)
	if err != nil {
		slog.Warn("Unable to prepare the "+action, "item", item.DisplayName, "installerType", installerItem.Type, "err", err)
		r.emit(item, "failed", NoPercent, err.Error())
		return "", r.recordFailure(item, action, err)
	}
	defer cleanup()

	// The last moment the user can withdraw the item; past this a cancel is refused.
	if !r.Cancels.act(item.Name) {
		return "", r.canceled(item)
	}

	// Run the command
	r.emit(item, state, NoPercent, item.DisplayName)
	slog.Info(verb+"ing", "item", item.DisplayName, "version", item.Version, "installerType", installerItem.Type)
	out, err := run()
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

// prepareRun readies the action via the type implementation: one command
// line, or a nupkg's checked and extracted package. Nothing runs until run
// is called; cleanup removes what preparing left behind. A nupkg item with
// no version takes the package's.
func (r *Runner) prepareRun(impl typeInstaller, item *catalog.Item, installerItem catalog.InstallerItem, absFile, action, verb string, successCodes []int) (func() (string, error), func(), error) {
	switch impl := impl.(type) {
	case nupkgInstaller:
		pkg, err := r.prepareNupkg(*item, installerItem, absFile, action)
		if err != nil {
			return nil, nil, err
		}
		if item.Version == "" && action != "uninstall" {
			item.Version = pkg.version
		}
		return func() (string, error) { return pkg.run(successCodes) }, pkg.discard, nil
	case commandInstaller:
		var cmd string
		var args []string
		var err error
		if action == "uninstall" {
			cmd, args, err = impl.uninstallCommand(*item, absFile)
		} else {
			cmd, args, err = impl.installCommand(*item, absFile)
		}
		if err != nil {
			return nil, nil, err
		}
		name := item.DisplayName
		return func() (string, error) {
			out, err := runCommand(cmd, args)
			if code, ok := acceptedExitCode(err, successCodes); ok {
				slog.Info(verb+"er exited with an accepted non-zero code", "item", name, "code", code)
				err = nil
			}
			return out, err
		}, func() {}, nil
	}
	return nil, nil, fmt.Errorf("installer type %q has no way to run for %s", installerItem.Type, item.DisplayName)
}

func (r *Runner) installItem(item catalog.Item, itemURL string) (string, error) {
	return r.actionItem(item, itemURL, "install")
}

func (r *Runner) uninstallItem(item catalog.Item, itemURL string) (string, error) {
	return r.actionItem(item, itemURL, "uninstall")
}

var (
	// By putting the functions in a variable, we can override later in tests.
	// installer_zip_only.go defines the zip-only stand-ins.
	installItemFunc   = (*Runner).installItem
	uninstallItemFunc = (*Runner).uninstallItem
)
