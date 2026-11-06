//go:build !zip_only

package installer

import (
	"archive/zip"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/go-version"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/download"
)

// nupkgInstaller runs a package's own PowerShell scripts, without Chocolatey.
// It is several steps rather than one command line, so actionItem drives it
// through prepareNupkg instead of a command.
type nupkgInstaller struct{}

func (nupkgInstaller) uninstallNeedsFile() bool { return true }

func (nupkgInstaller) successCodes() []int { return msiRestartCodes }

// Package script names under tools/, matched case-insensitively. embala's
// plain style writes the first set, Chocolatey-style packages the second;
// the plain set wins when a package has both, since embala's plain style
// also ships a chocolateyInstall.ps1 that refuses to run.
var (
	installScripts      = []string{"install.ps1", "chocolateyInstall.ps1"}
	uninstallScripts    = []string{"uninstall.ps1", "chocolateyUninstall.ps1"}
	beforeModifyScripts = []string{"before-modify.ps1", "chocolateyBeforeModify.ps1"}
)

// nupkgAction is a nupkg install or uninstall made ready to run: the package
// is checked and, where its scripts come from the new file, extracted to
// staging. Nothing has run yet.
type nupkgAction struct {
	name    string // the item's display name, for errors
	install bool
	version string // the nuspec version of the package being acted on

	kept, staging, old string // kept copy, staging and swap-out directories
	// beforeModify is the kept copy's before-modify, or "".
	beforeModify string
	// script is the install or uninstall script to run.
	script string
}

// prepareNupkg checks the verified archive and prepares the action. An
// install extracts the package into staging; an uninstall uses the kept copy
// when there is one and extracts the uninstaller file otherwise.
func (r *Runner) prepareNupkg(item catalog.Item, installerItem catalog.InstallerItem, archive, action string) (*nupkgAction, error) {
	if r.NupkgPath == "" {
		return nil, fmt.Errorf("%s: no directory is configured for kept nupkg copies", item.DisplayName)
	}
	zr, entries, spec, err := openNupkg(archive, installerItem.Hash)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", item.DisplayName, err)
	}
	defer func() { _ = zr.Close() }()

	install := action != "uninstall"
	wantID := installerItem.PackageID
	if !install && wantID == "" {
		wantID = item.Installer.PackageID
	}
	if wantID != "" && !strings.EqualFold(wantID, spec.ID) {
		return nil, fmt.Errorf("%s: the package id is %s, but the catalog's package_id is %s", item.DisplayName, spec.ID, wantID)
	}
	if install && item.Version != "" && !sameVersion(item.Version, spec.Version) {
		return nil, fmt.Errorf("%s: the package version is %s, but the catalog's version is %s", item.DisplayName, spec.Version, item.Version)
	}

	id := strings.ToLower(spec.ID)
	a := &nupkgAction{
		name:    item.DisplayName,
		install: install,
		version: spec.Version,
		kept:    keptCopyDir(r.NupkgPath, id),
		staging: filepath.Join(r.NupkgPath, ".staging-"+id),
		old:     filepath.Join(r.NupkgPath, ".old-"+id),
	}
	if err := a.settleOld(); err != nil {
		return nil, fmt.Errorf("%s: %w", item.DisplayName, err)
	}
	keptExists := isDir(a.kept)
	if keptExists {
		a.beforeModify = findScript(a.kept, beforeModifyScripts)
	}

	if !install && keptExists {
		if a.script = findScript(a.kept, uninstallScripts); a.script == "" {
			return nil, fmt.Errorf("%s: package has no uninstall script", item.DisplayName)
		}
		return a, nil
	}
	// Otherwise the script comes from the new file: the package being
	// installed, or the uninstaller file of a package with no kept copy,
	// which then runs no before-modify. A staging directory left by an
	// interrupted run is stale.
	if err := os.MkdirAll(r.NupkgPath, 0o755); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(a.staging); err != nil {
		return nil, err
	}
	if err := os.Mkdir(a.staging, 0o755); err != nil {
		return nil, err
	}
	names := installScripts
	if !install {
		names = uninstallScripts
	}
	if err := extractEntries(entries, a.staging); err != nil {
		a.discard()
		return nil, fmt.Errorf("%s: %w", item.DisplayName, err)
	}
	if a.script = findScript(a.staging, names); a.script == "" {
		a.discard()
		return nil, fmt.Errorf("%s: package has no %s script", item.DisplayName, action)
	}
	return a, nil
}

// run runs the before-modify, if any, then the install or uninstall script,
// and on success updates the kept copy. A failed install leaves the previous
// kept copy as it was; a failed uninstall keeps it.
func (a *nupkgAction) run(successCodes []int) (string, error) {
	if a.beforeModify != "" {
		// Chocolatey downgrades a failing before-modify to a warning too.
		if _, err := runPackageScript(a.beforeModify, successCodes); err != nil {
			slog.Warn("Package before-modify script failed, continuing", "item", a.name, "err", err)
		}
	}
	out, err := runPackageScript(a.script, successCodes)
	if err != nil {
		return out, err
	}
	if !a.install {
		if err := os.RemoveAll(a.kept); err != nil {
			return out, fmt.Errorf("%s: uninstalled, but unable to remove the kept copy: %w", a.name, err)
		}
		return out, nil
	}
	if err := a.keep(); err != nil {
		return out, fmt.Errorf("%s: installed, but unable to keep the package: %w", a.name, err)
	}
	return out, nil
}

// keep moves staging into place as the kept copy. os.Rename cannot replace
// a directory on Windows, so the old copy moves aside first and is removed
// after; failing to remove it only warns, and the next install clears it.
func (a *nupkgAction) keep() error {
	if err := os.Rename(a.kept, a.old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := renameWithRetry(a.staging, a.kept); err != nil {
		_ = os.Rename(a.old, a.kept)
		return err
	}
	if err := os.RemoveAll(a.old); err != nil {
		slog.Warn("Unable to remove the previous kept nupkg copy", "path", a.old, "err", err)
	}
	return nil
}

// discard removes staging, which after a successful install is already gone.
func (a *nupkgAction) discard() {
	if err := os.RemoveAll(a.staging); err != nil {
		slog.Warn("Unable to remove the nupkg staging directory", "path", a.staging, "err", err)
	}
}

// openNupkg verifies archive against hash, refuses it if unsafe, and reads
// its root nuspec. The caller closes the reader.
func openNupkg(archive, hash string) (*zip.ReadCloser, []archiveEntry, nuspec, error) {
	if !download.Verify(archive, hash) {
		return nil, nil, nuspec{}, fmt.Errorf("%s does not match its catalog hash", filepath.Base(archive))
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return nil, nil, nuspec{}, fmt.Errorf("%w: %w", errUnsafeArchive, err)
	}
	entries, err := nupkgEntries(zr.File)
	if err == nil {
		err = checkEntries(entries)
	}
	var spec nuspec
	if err == nil {
		spec, err = entriesNuspec(entries)
	}
	if err != nil {
		_ = zr.Close()
		return nil, nil, nuspec{}, err
	}
	return zr, entries, spec, nil
}

// nupkgEntries names each file entry by its %XX-decoded name, which is what
// NuGet and choco pack write and what the zip checks must see, so %2e%2e is
// refused like "..". Directory entries are skipped: only files extract, and
// NuGet writes none, but other zip tools do.
func nupkgEntries(files []*zip.File) ([]archiveEntry, error) {
	entries := make([]archiveEntry, 0, len(files))
	for _, f := range files {
		if f.Mode().IsDir() {
			continue
		}
		name, err := url.PathUnescape(f.Name)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %w", errUnsafeArchive, f.Name, err)
		}
		entries = append(entries, archiveEntry{f: f, name: name})
	}
	return entries, nil
}

// entriesNuspec parses the package's one root .nuspec.
func entriesNuspec(entries []archiveEntry) (nuspec, error) {
	var found *zip.File
	for _, e := range entries {
		if isRootNuspec(e.name) {
			if found != nil {
				return nuspec{}, fmt.Errorf("package has more than one .nuspec")
			}
			found = e.f
		}
	}
	if found == nil {
		return nuspec{}, fmt.Errorf("package has no .nuspec")
	}
	rc, err := found.Open()
	if err != nil {
		return nuspec{}, err
	}
	defer func() { _ = rc.Close() }()
	return parseNuspec(rc)
}

// sameVersion compares two versions as versions, so 1.0 equals 1.0.0, and
// as strings when either does not parse.
func sameVersion(a, b string) bool {
	va, errA := version.NewVersion(a)
	vb, errB := version.NewVersion(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return va.Equal(vb)
}

// findScript returns the path of the first of names under pkgDir's tools
// directory, matching names in ASCII case only, or "" if none is there.
func findScript(pkgDir string, names []string) string {
	tools := findEntry(pkgDir, "tools", true)
	if tools == "" {
		return ""
	}
	for _, name := range names {
		if script := findEntry(tools, name, false); script != "" {
			return script
		}
	}
	return ""
}

// findEntry returns the path of dir's child whose lowercased name is
// lower(name) and whose kind matches wantDir, or "".
func findEntry(dir, name string, wantDir bool) string {
	children, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	want := strings.ToLower(name)
	for _, c := range children {
		if strings.ToLower(c.Name()) != want {
			continue
		}
		if wantDir && c.IsDir() || !wantDir && c.Type().IsRegular() {
			return filepath.Join(dir, c.Name())
		}
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// psSingleQuotes are the characters PowerShell ends a single-quoted string
// on: the ASCII quote and its typographic forms. Doubling each escapes it.
var psSingleQuotes = strings.NewReplacer("'", "''", "‘", "‘‘", "’", "’’", "‚", "‚‚", "‛", "‛‛")

// packageScriptArgs are the powershell.exe arguments that run script with
// errors forced to stop, as Chocolatey's own runner does: under plain -File
// an unknown command or a non-terminating error would exit 0. The script's
// exit code passes through.
func packageScriptArgs(script string) []string {
	wrapper := "$ErrorActionPreference = 'Stop'; & '" + psSingleQuotes.Replace(script) + "'; if ($LASTEXITCODE) { exit $LASTEXITCODE }; exit 0"
	return []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", wrapper}
}

// runPackageScript runs one package script from its tools directory and
// treats an exit code in successCodes as success.
func runPackageScript(script string, successCodes []int) (string, error) {
	slog.Info("Running package script", "script", script)
	out, err := runCommandIn(filepath.Dir(script), commandPs1, packageScriptArgs(script))
	if code, ok := acceptedExitCode(err, successCodes); ok {
		slog.Info("Package script exited with an accepted non-zero code", "script", script, "code", code)
		err = nil
	}
	return out, err
}

// settleOld deals with a swap-out directory left by an earlier install. If
// the kept copy is missing, a crash between keep's two renames left the old
// copy as the only one, so it comes back. Otherwise it is stale and goes now,
// before any script runs: failing here is cheaper than after the install.
func (a *nupkgAction) settleOld() error {
	if !isDir(a.old) {
		return nil
	}
	if !isDir(a.kept) {
		slog.Info("Restoring the kept nupkg copy from an interrupted swap", "path", a.kept)
		return os.Rename(a.old, a.kept)
	}
	return os.RemoveAll(a.old)
}

// renameRetries and renameDelay bound the retry of keep's staging rename. On
// Windows a lingering child of the install script or a virus scan can hold
// staging for a moment. A process that holds it longer still fails the install.
var (
	renameRetries = 5
	renameDelay   = 500 * time.Millisecond
)

func renameWithRetry(from, to string) error {
	err := os.Rename(from, to)
	for i := 1; err != nil && i < renameRetries; i++ {
		time.Sleep(renameDelay)
		err = os.Rename(from, to)
	}
	return err
}
