//go:build !zip_only

package installer

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// The nupkg engine runs a package's own scripts, so these tests build small
// packages, stub the script runner, and judge the engine by which script
// bodies ran, in which order, and what is left under the kept-copy root.

const testNuspecNS = "http://schemas.microsoft.com/packaging/2015/06/nuspec.xsd"

// testNuspec is a root nuspec for id and version in namespace ns ("" for
// none).
func testNuspec(ns, id, version string) string {
	xmlns := ""
	if ns != "" {
		xmlns = fmt.Sprintf(` xmlns="%s"`, ns)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<package%s><metadata><id>%s</id><version>%s</version><authors>t</authors></metadata></package>`, xmlns, id, version)
}

// writeNupkg writes entries as a zip at path and returns its sha256.
func writeNupkg(t *testing.T, path string, entries ...zipEntry) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// scripts are a package's tools/ files, name to body. Each body names its
// package and role, such as "1.0.0 install", so a run shows which package's
// script ran.
type scripts map[string]string

// plainScripts is a plain-set package of version v with every script.
func plainScripts(v string) scripts {
	return scripts{"install.ps1": v + " install", "uninstall.ps1": v + " uninstall", "before-modify.ps1": v + " before-modify"}
}

// testPackage writes a package of id and version with the given tools files
// into the runner's cache and returns the catalog item that installs it, its
// uninstaller being the same file.
func testPackage(t *testing.T, r *Runner, id, version string, tools scripts, extra ...zipEntry) catalog.Item {
	t.Helper()
	entries := []zipEntry{{name: id + ".nuspec", body: testNuspec(testNuspecNS, id, version)}}
	for name, body := range tools {
		entries = append(entries, zipEntry{name: "tools/" + name, body: body})
	}
	entries = append(entries, extra...)
	loc := fmt.Sprintf("packages/%s.%s.nupkg", id, version)
	hash := writeNupkg(t, filepath.Join(r.CachePath, filepath.FromSlash(loc)), entries...)
	file := catalog.InstallerItem{Type: "nupkg", Location: loc, Hash: hash}
	return catalog.Item{Name: "TestPackage", DisplayName: "Test Package", Version: version, Installer: file, Uninstaller: file}
}

func nupkgRunner(t *testing.T) *Runner {
	t.Helper()
	r := newTestRunner()
	r.CachePath = t.TempDir()
	r.NupkgPath = filepath.Join(t.TempDir(), "nupkg")
	return r
}

// scriptRuns stubs the package script runner. It records the body of each
// script run and answers with exits[body], nil when absent. It also checks
// each run is the PowerShell wrapper started from the script's directory.
func scriptRuns(t *testing.T, exits map[string]error) *[]string {
	t.Helper()
	var ran []string
	runCommandIn = func(dir, command string, args []string) (string, error) {
		wrapper := args[len(args)-1]
		_, quoted, _ := strings.Cut(wrapper, "& '")
		quoted, _, _ = strings.Cut(quoted, "'; if")
		script := strings.ReplaceAll(quoted, "''", "'")
		if command != commandPs1 || dir != filepath.Dir(script) {
			t.Errorf("ran %s in %s for %s, want powershell in the script's directory", command, dir, script)
		}
		body, err := os.ReadFile(script)
		if err != nil {
			t.Fatalf("script %s is not on disk: %v", script, err)
		}
		ran = append(ran, string(body))
		return "", exits[string(body)]
	}
	runCommand = func(command string, args []string) (string, error) {
		t.Fatalf("a nupkg ran a plain command: %s %v", command, args)
		return "", nil
	}
	t.Cleanup(func() {
		runCommandIn = runCMDIn
		runCommand = origRunCommand
	})
	return &ran
}

func wantRuns(t *testing.T, ran *[]string, want ...string) {
	t.Helper()
	if len(*ran) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(*ran, want) {
		t.Fatalf("scripts ran %q, want %q", *ran, want)
	}
}

func wantKept(t *testing.T, r *Runner, id, version string) {
	t.Helper()
	got, ok := KeptNupkgVersion(r.NupkgPath, id)
	if version == "" {
		if ok {
			t.Fatalf("kept copy of %s at %q exists, want none", id, got)
		}
		return
	}
	if !ok || got != version {
		t.Fatalf("kept copy of %s = %q, %v; want %q", id, got, ok, version)
	}
}

// wantOnlyKept checks nothing but kept copies is left under the root: no
// staging or swap-out directory.
func wantOnlyKept(t *testing.T, r *Runner) {
	t.Helper()
	children, _ := os.ReadDir(r.NupkgPath)
	for _, c := range children {
		if strings.HasPrefix(c.Name(), ".") {
			t.Errorf("%s is left under the kept-copy root", c.Name())
		}
	}
}

func install(t *testing.T, r *Runner, item catalog.Item) error {
	t.Helper()
	_, err := r.installItem(item, "https://example.com/unused")
	return err
}

func uninstall(t *testing.T, r *Runner, item catalog.Item) error {
	t.Helper()
	_, err := r.uninstallItem(item, "https://example.com/unused")
	return err
}

func TestNupkgInstallRunsTheScriptAndKeepsThePackage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools scripts
		want  string
	}{
		{"plain set", scripts{"install.ps1": "plain"}, "plain"},
		{"chocolatey set", scripts{"chocolateyInstall.ps1": "choco"}, "choco"},
		// embala's plain style ships a refusing chocolateyInstall.ps1 too.
		{"plain over chocolatey", scripts{"install.ps1": "plain", "chocolateyInstall.ps1": "choco"}, "plain"},
		{"mixed case", scripts{"INSTALL.Ps1": "plain", "ChocolateyInstall.PS1": "choco"}, "plain"},
		{"mixed case chocolatey", scripts{"CHOCOLATEYINSTALL.ps1": "choco"}, "choco"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := nupkgRunner(t)
			ran := scriptRuns(t, nil)
			var events []string
			r.Emit = func(_ catalog.Item, _ []string, state string, _ int, _ string) { events = append(events, state) }
			item := testPackage(t, r, "My.Package", "1.0.0", tc.tools)

			if err := install(t, r, item); err != nil {
				t.Fatalf("install: %v", err)
			}
			wantRuns(t, ran, tc.want)
			// The kept copy is keyed by the lowercased nuspec id.
			if !isDir(filepath.Join(r.NupkgPath, "my.package")) {
				t.Fatal("no kept copy at my.package")
			}
			wantKept(t, r, "MY.PACKAGE", "1.0.0")
			wantOnlyKept(t, r)
			if len(r.Report.InstalledItems) != 1 || len(r.Report.FailedItems) != 0 {
				t.Fatalf("report = %+v, want one installed item", r.Report)
			}
			if want := []string{"downloading", "installing", "done"}; !reflect.DeepEqual(events, want) {
				t.Fatalf("events = %v, want %v", events, want)
			}
		})
	}
}

// A Tools directory spelled in another case still holds the scripts.
func TestNupkgToolsDirectoryCase(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, nil)
	item := testPackage(t, r, "pkg", "1.0.0", nil, zipEntry{name: "Tools/Install.ps1", body: "install"})

	if err := install(t, r, item); err != nil {
		t.Fatalf("install: %v", err)
	}
	wantRuns(t, ran, "install")
}

// An upgrade and a forced reinstall both run the kept copy's before-modify,
// then the new package's install, and the new package becomes the kept copy.
func TestNupkgUpgradeRunsTheKeptBeforeModifyFirst(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, nil)
	v1 := testPackage(t, r, "pkg", "1.0.0", plainScripts("1"))
	v2 := testPackage(t, r, "pkg", "2.0.0", plainScripts("2"))

	if err := install(t, r, v1); err != nil {
		t.Fatalf("install 1.0.0: %v", err)
	}
	wantRuns(t, ran, "1 install")

	*ran = nil
	if err := install(t, r, v1); err != nil {
		t.Fatalf("reinstall 1.0.0: %v", err)
	}
	wantRuns(t, ran, "1 before-modify", "1 install")
	wantKept(t, r, "pkg", "1.0.0")

	*ran = nil
	if err := install(t, r, v2); err != nil {
		t.Fatalf("upgrade to 2.0.0: %v", err)
	}
	wantRuns(t, ran, "1 before-modify", "2 install")
	wantKept(t, r, "pkg", "2.0.0")
	wantOnlyKept(t, r)
}

func TestNupkgChocolateyBeforeModify(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, nil)
	choco := scripts{"chocolateyInstall.ps1": "install", "chocolateyBeforeModify.ps1": "choco bm"}
	if err := install(t, r, testPackage(t, r, "pkg", "1.0.0", choco)); err != nil {
		t.Fatal(err)
	}
	*ran = nil
	if err := install(t, r, testPackage(t, r, "pkg", "2.0.0", choco)); err != nil {
		t.Fatal(err)
	}
	wantRuns(t, ran, "choco bm", "install")
}

// A failing before-modify warns, and the upgrade or uninstall goes on.
func TestNupkgFailingBeforeModifyContinues(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, map[string]error{"1 before-modify": fakeExit(1), "2 before-modify": fakeExit(1)})
	if err := install(t, r, testPackage(t, r, "pkg", "1.0.0", plainScripts("1"))); err != nil {
		t.Fatal(err)
	}
	v2 := testPackage(t, r, "pkg", "2.0.0", plainScripts("2"))

	*ran = nil
	if err := install(t, r, v2); err != nil {
		t.Fatalf("upgrade with a failing before-modify: %v", err)
	}
	wantRuns(t, ran, "1 before-modify", "2 install")
	wantKept(t, r, "pkg", "2.0.0")

	*ran = nil
	if err := uninstall(t, r, v2); err != nil {
		t.Fatalf("uninstall with a failing before-modify: %v", err)
	}
	wantRuns(t, ran, "2 before-modify", "2 uninstall")
	wantKept(t, r, "pkg", "")
}

// Uninstall runs the kept copy's scripts, not the uninstaller file's, and
// removes the kept copy.
func TestNupkgUninstallUsesTheKeptCopy(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, nil)
	if err := install(t, r, testPackage(t, r, "pkg", "1.0.0", plainScripts("1"))); err != nil {
		t.Fatal(err)
	}
	newer := testPackage(t, r, "pkg", "2.0.0", plainScripts("2"))

	*ran = nil
	if err := uninstall(t, r, newer); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	wantRuns(t, ran, "1 before-modify", "1 uninstall")
	wantKept(t, r, "pkg", "")
	wantOnlyKept(t, r)
	if len(r.Report.UninstalledItems) != 1 {
		t.Fatalf("report = %+v, want one uninstalled item", r.Report)
	}
}

// With no kept copy (installed by Chocolatey, or removed by hand), uninstall
// runs the uninstaller file's script and no before-modify.
func TestNupkgUninstallWithoutAKeptCopyUsesTheFile(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, nil)
	item := testPackage(t, r, "pkg", "1.0.0", plainScripts("1"))

	if err := uninstall(t, r, item); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	wantRuns(t, ran, "1 uninstall")
	wantKept(t, r, "pkg", "")
	wantOnlyKept(t, r)
}

func TestNupkgNoInstallScript(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, nil)
	// A package embala built before 0.10.0 carries a payload and no script.
	item := testPackage(t, r, "pkg", "1.0.0", scripts{"pkg.exe": "payload"})

	err := install(t, r, item)
	if err == nil || err.Error() != "Test Package: package has no install script" {
		t.Fatalf("install err = %v, want the no-install-script error", err)
	}
	wantRuns(t, ran)
	wantKept(t, r, "pkg", "")
	wantOnlyKept(t, r)
	if len(r.Report.FailedItems) != 1 {
		t.Fatalf("report = %+v, want one failure", r.Report)
	}
}

// A package with no uninstall script fails the uninstall and keeps the kept
// copy, so the next run can try again.
func TestNupkgNoUninstallScript(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, nil)
	item := testPackage(t, r, "pkg", "1.0.0", scripts{"install.ps1": "install", "before-modify.ps1": "bm"})
	if err := install(t, r, item); err != nil {
		t.Fatal(err)
	}

	*ran = nil
	err := uninstall(t, r, item)
	if err == nil || !strings.Contains(err.Error(), "package has no uninstall script") {
		t.Fatalf("uninstall err = %v, want the no-uninstall-script error", err)
	}
	wantRuns(t, ran)
	wantKept(t, r, "pkg", "1.0.0")

	// The same holds for an uninstaller file with no kept copy.
	other := testPackage(t, r, "other", "1.0.0", scripts{"install.ps1": "install"})
	if err := uninstall(t, r, other); err == nil || !strings.Contains(err.Error(), "package has no uninstall script") {
		t.Fatalf("uninstall err = %v, want the no-uninstall-script error", err)
	}
	wantOnlyKept(t, r)
}

func TestNupkgIDAndVersionChecks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		edit    func(*catalog.Item)
		wantErr string // "" for success
	}{
		{"package_id differs", func(i *catalog.Item) { i.Installer.PackageID = "other" }, "the package id is Pkg, but the catalog's package_id is other"},
		{"package_id matches in another case", func(i *catalog.Item) { i.Installer.PackageID = "PKG" }, ""},
		{"version differs", func(i *catalog.Item) { i.Version = "2.0.0" }, "the package version is 1.0.0, but the catalog's version is 2.0.0"},
		{"version equal as versions", func(i *catalog.Item) { i.Version = "1.0" }, ""},
		{"no version", func(i *catalog.Item) { i.Version = "" }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := nupkgRunner(t)
			ran := scriptRuns(t, nil)
			item := testPackage(t, r, "Pkg", "1.0.0", scripts{"install.ps1": "install"})
			tc.edit(&item)

			err := install(t, r, item)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("install: %v", err)
				}
				wantKept(t, r, "pkg", "1.0.0")
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("install err = %v, want %q", err, tc.wantErr)
			}
			wantRuns(t, ran)
			wantKept(t, r, "pkg", "")
			wantOnlyKept(t, r)
		})
	}
}

func TestNupkgNoVersionReportsTheNuspecVersion(t *testing.T) {
	r := nupkgRunner(t)
	scriptRuns(t, nil)
	item := testPackage(t, r, "pkg", "1.2.0", scripts{"install.ps1": "install"})
	item.Version = ""

	if err := install(t, r, item); err != nil {
		t.Fatal(err)
	}
	if got := r.Report.InstalledItems[0].Version; got != "1.2.0" {
		t.Fatalf("reported version %q, want the nuspec's 1.2.0", got)
	}
}

// NuGet and choco pack %-encode entry names, so the zip checks run on the
// decoded names: %2e%2e is refused like "..", and an ordinary encoded name
// extracts under its decoded one. Directory entries are skipped.
func TestNupkgEntryNames(t *testing.T) {
	for _, name := range []string{"tools/%2e%2e/%2e%2e/evil.ps1", "%2E%2E/evil.ps1", "tools/%2fetc", "tools/a%5cb", "tools/bad%zz"} {
		t.Run(name, func(t *testing.T) {
			r := nupkgRunner(t)
			ran := scriptRuns(t, nil)
			item := testPackage(t, r, "pkg", "1.0.0", scripts{"install.ps1": "install"}, zipEntry{name: name, body: "x"})

			if err := install(t, r, item); !errors.Is(err, errUnsafeArchive) {
				t.Fatalf("install err = %v, want an unsafe-archive refusal", err)
			}
			wantRuns(t, ran)
			if children, _ := os.ReadDir(r.NupkgPath); len(children) != 0 {
				t.Fatalf("a refused package left %v", children)
			}
		})
	}

	r := nupkgRunner(t)
	scriptRuns(t, nil)
	item := testPackage(t, r, "pkg", "1.0.0", scripts{"install.ps1": "install"},
		zipEntry{name: "tools/", mode: fs.ModeDir | 0o755},
		zipEntry{name: "tools/my%20file.txt", body: "x"},
		zipEntry{name: "%5BContent_Types%5D.xml", body: "<Types/>"})
	if err := install(t, r, item); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, name := range []string{"tools/my file.txt", "[Content_Types].xml"} {
		if _, err := os.Stat(filepath.Join(r.NupkgPath, "pkg", filepath.FromSlash(name))); err != nil {
			t.Errorf("%s was not extracted under its decoded name: %v", name, err)
		}
	}
}

func TestNupkgStaleStagingDoesNotBlock(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, nil)
	stale := filepath.Join(r.NupkgPath, ".staging-pkg", "tools")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "leftover.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := install(t, r, testPackage(t, r, "pkg", "1.0.0", scripts{"install.ps1": "install"})); err != nil {
		t.Fatalf("install: %v", err)
	}
	wantRuns(t, ran, "install")
	if _, err := os.Stat(filepath.Join(r.NupkgPath, "pkg", "tools", "leftover.txt")); !os.IsNotExist(err) {
		t.Fatalf("the stale staging directory's file reached the kept copy: %v", err)
	}
	wantOnlyKept(t, r)
}

// A failed install script leaves the previous kept copy as it was and
// removes the partly extracted new package.
func TestNupkgFailedInstallKeepsTheOldCopy(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, map[string]error{"2 install": fakeExit(1)})
	if err := install(t, r, testPackage(t, r, "pkg", "1.0.0", plainScripts("1"))); err != nil {
		t.Fatal(err)
	}

	*ran = nil
	if err := install(t, r, testPackage(t, r, "pkg", "2.0.0", plainScripts("2"))); err == nil {
		t.Fatal("a failing install script succeeded")
	}
	wantRuns(t, ran, "1 before-modify", "2 install")
	wantKept(t, r, "pkg", "1.0.0")
	body, err := os.ReadFile(filepath.Join(r.NupkgPath, "pkg", "tools", "uninstall.ps1"))
	if err != nil || string(body) != "1 uninstall" {
		t.Fatalf("kept uninstall script = %q, %v; want 1.0.0's", body, err)
	}
	wantOnlyKept(t, r)
	if len(r.Report.FailedItems) != 1 || len(r.Report.InstalledItems) != 1 {
		t.Fatalf("report = %+v, want the first install and one failure", r.Report)
	}
}

// A failed uninstall script keeps the kept copy.
func TestNupkgFailedUninstallKeepsTheCopy(t *testing.T) {
	r := nupkgRunner(t)
	scriptRuns(t, map[string]error{"1 uninstall": fakeExit(1)})
	item := testPackage(t, r, "pkg", "1.0.0", plainScripts("1"))
	if err := install(t, r, item); err != nil {
		t.Fatal(err)
	}
	if err := uninstall(t, r, item); err == nil {
		t.Fatal("a failing uninstall script succeeded")
	}
	wantKept(t, r, "pkg", "1.0.0")
}

// The restart codes count as success by default; an item's success_codes
// replaces them.
func TestNupkgSuccessCodes(t *testing.T) {
	r := nupkgRunner(t)
	scriptRuns(t, map[string]error{"install": fakeExit(3010)})
	item := testPackage(t, r, "pkg", "1.0.0", scripts{"install.ps1": "install"})
	if err := install(t, r, item); err != nil {
		t.Fatalf("exit 3010: %v", err)
	}

	r = nupkgRunner(t)
	item = testPackage(t, r, "pkg", "1.0.0", scripts{"install.ps1": "install"})
	item.Installer.SuccessCodes = []int{}
	if err := install(t, r, item); err == nil {
		t.Fatal("exit 3010 succeeded with success_codes turned off")
	}
	wantKept(t, r, "pkg", "")
}

// Package scripts run with errors forced to stop and their exit code passed
// through, and a quote in the path cannot end the string.
func TestPackageScriptArgs(t *testing.T) {
	args := packageScriptArgs(`C:\Data\it's\tools\install.ps1`)
	want := []string{
		"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		`$ErrorActionPreference = 'Stop'; & 'C:\Data\it''s\tools\install.ps1'; if ($LASTEXITCODE) { exit $LASTEXITCODE }; exit 0`,
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %q\nwant %q", args, want)
	}
	// PowerShell also ends a single-quoted string on the typographic quotes.
	if got := packageScriptArgs("C:\\a\u2019b.ps1")[6]; !strings.Contains(got, "& 'C:\\a\u2019\u2019b.ps1'") {
		t.Fatalf("wrapper = %q, want the typographic quote doubled", got)
	}
}

func TestParseNuspecIgnoresTheNamespace(t *testing.T) {
	for _, ns := range []string{"", testNuspecNS, "http://schemas.microsoft.com/packaging/2010/07/nuspec.xsd", "urn:anything"} {
		spec, err := parseNuspec(strings.NewReader(testNuspec(ns, "Pkg", "1.0.0")))
		if err != nil || spec != (nuspec{ID: "Pkg", Version: "1.0.0"}) {
			t.Errorf("namespace %q: %+v, %v", ns, spec, err)
		}
	}
	for _, bad := range []string{testNuspec("", "..", "1.0.0"), testNuspec("", `a\b`, "1.0.0"), testNuspec("", "pkg", ""), "not xml"} {
		if _, err := parseNuspec(strings.NewReader(bad)); err == nil {
			t.Errorf("parseNuspec(%q) succeeded", bad)
		}
	}
}

func TestKeptNupkgVersion(t *testing.T) {
	root := t.TempDir()
	if _, ok := KeptNupkgVersion(root, "pkg"); ok {
		t.Fatal("a missing kept copy exists")
	}
	if _, ok := KeptNupkgVersion("", "pkg"); ok {
		t.Fatal("no root, yet a kept copy exists")
	}
	if _, ok := KeptNupkgVersion(root, "../pkg"); ok {
		t.Fatal("an invalid id reached the file system")
	}
	dir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if v, ok := KeptNupkgVersion(root, "pkg"); !ok || v != "" {
		t.Fatalf("unreadable kept copy = %q, %v; want it present at no version", v, ok)
	}
	if err := os.WriteFile(filepath.Join(dir, "Pkg.nuspec"), []byte(testNuspec(testNuspecNS, "Pkg", "3.1.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, ok := KeptNupkgVersion(root, "PKG"); !ok || v != "3.1.0" {
		t.Fatalf("kept copy = %q, %v; want 3.1.0", v, ok)
	}
}

// A crash between keep's two renames leaves only .old-<id>; the next install
// must bring it back, so before-modify sees the kept copy.
func TestNupkgRecoversTheKeptCopyFromAnInterruptedSwap(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, nil)
	oldTools := filepath.Join(r.NupkgPath, ".old-pkg", "tools")
	if err := os.MkdirAll(oldTools, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldTools, "before-modify.ps1"), []byte("old before"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := install(t, r, testPackage(t, r, "pkg", "2.0.0", scripts{"install.ps1": "install"})); err != nil {
		t.Fatalf("install: %v", err)
	}
	wantRuns(t, ran, "old before", "install")
	wantOnlyKept(t, r)
}

// A leftover .old-<id> beside a kept copy is stale: it goes before the
// script runs, and one that cannot go fails the action with no script run.
func TestNupkgStaleOldIsRemovedBeforeTheScript(t *testing.T) {
	r := nupkgRunner(t)
	scriptRuns(t, nil)
	if err := install(t, r, testPackage(t, r, "pkg", "1.0.0", plainScripts("1"))); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(r.NupkgPath, ".old-pkg")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := install(t, r, testPackage(t, r, "pkg", "2.0.0", plainScripts("2"))); err != nil {
		t.Fatalf("install: %v", err)
	}
	wantKept(t, r, "pkg", "2.0.0")
	wantOnlyKept(t, r)
}

func TestNupkgUnremovableOldFailsBeforeAnyScript(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("needs a directory the test user cannot empty")
	}
	r := nupkgRunner(t)
	ran := scriptRuns(t, nil)
	if err := install(t, r, testPackage(t, r, "pkg", "1.0.0", plainScripts("1"))); err != nil {
		t.Fatal(err)
	}
	*ran = nil
	// A read-only directory stands in for a locked file.
	stale := filepath.Join(r.NupkgPath, ".old-pkg", "sub")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stale, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stale, 0o755) })

	if err := install(t, r, testPackage(t, r, "pkg", "2.0.0", plainScripts("2"))); err == nil {
		t.Fatal("install succeeded with an unremovable .old directory")
	}
	wantRuns(t, ran)
	wantKept(t, r, "pkg", "1.0.0")
}

// A cancel at the last window, after prepare and before any script, runs
// nothing and leaves no staging directory behind.
func TestNupkgCancelAfterPrepareRunsNoScript(t *testing.T) {
	r := nupkgRunner(t)
	ran := scriptRuns(t, nil)
	item := testPackage(t, r, "pkg", "1.0.0", plainScripts("1"))
	r.Cancels = NewCancels()
	// The "downloading" event comes before prepare and the cached file needs
	// no download, so the withdrawal is seen only at the act check.
	r.Emit = func(it catalog.Item, _ []string, state string, _ int, _ string) {
		if state == "downloading" {
			r.Cancels.mu.Lock()
			r.Cancels.canceled[it.Name] = true
			r.Cancels.mu.Unlock()
		}
	}

	if err := install(t, r, item); !errors.Is(err, ErrCanceled) {
		t.Fatalf("install error = %v, want ErrCanceled", err)
	}
	wantRuns(t, ran)
	wantKept(t, r, "pkg", "")
	wantOnlyKept(t, r)
}
