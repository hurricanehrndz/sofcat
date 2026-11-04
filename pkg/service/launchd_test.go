package service

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The plist `just pkg` ships and the one -serviceinstall writes come from
// LaunchdPlist. launchd keys on the label, and the daemon must find its config
// and log into the root-only Data directory, so a rebrand moves every path.
func TestInstalledLaunchdPlistGolden(t *testing.T) {
	origSlug, origName, origID := identity.Slug, identity.Name, identity.Identifier
	t.Cleanup(func() { identity.Slug, identity.Name, identity.Identifier = origSlug, origName, origID })
	identity.Slug, identity.Name, identity.Identifier = "tapas-test", "Tapas & Test", "ca.hrndz.tapas-test"

	const want = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>ca.hrndz.tapas-test</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Library/Application Support/Tapas &amp; Test/bin/tapas-test</string>
		<string>-c</string>
		<string>/Library/Application Support/Tapas &amp; Test/config.yaml</string>
		<string>-service</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>/Library/Application Support/Tapas &amp; Test/Data/launchd.log</string>
	<key>StandardErrorPath</key>
	<string>/Library/Application Support/Tapas &amp; Test/Data/launchd.log</string>
</dict>
</plist>
`
	got := InstalledLaunchdPlist()
	if string(got) != want {
		t.Fatalf("plist:\n%s\nwant:\n%s", got, want)
	}
}

func TestLaunchdStatus(t *testing.T) {
	printed := "system/ca.hrndz.sofcat = {\n" +
		"\tactive count = 1\n" +
		"\tpath = /Library/LaunchDaemons/ca.hrndz.sofcat.plist\n" +
		"\ttype = LaunchDaemon\n" +
		"\tstate = running\n" +
		"\n" +
		"\tprogram = /Library/Application Support/SofCat/bin/sofcat\n" +
		"\targuments = {\n" +
		"\t\tstate = nested\n" +
		"\t}\n" +
		"\tpid = 4242\n" +
		"\tlast exit code = (never exited)\n" +
		"}\n"
	want := "name: ca.hrndz.sofcat\n" +
		"plist: /Library/LaunchDaemons/ca.hrndz.sofcat.plist\n" +
		"state: running\n" +
		"pid: 4242\n" +
		"program: /Library/Application Support/SofCat/bin/sofcat\n" +
		"last_exit_code: (never exited)"
	if got := launchdStatus("ca.hrndz.sofcat", "/Library/LaunchDaemons/ca.hrndz.sofcat.plist", []byte(printed)); got != want {
		t.Fatalf("status:\n%s\nwant:\n%s", got, want)
	}
}

// fakeLaunchctl replaces launchctl for one test: `print` succeeds only while
// loaded is true (bootstrap sets it, bootout clears it, as launchd does), and
// every call is recorded.
func fakeLaunchctl(t *testing.T, loaded bool) *[]string {
	t.Helper()
	orig := launchctl
	t.Cleanup(func() { launchctl = orig })
	var calls []string
	launchctl = func(args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch args[0] {
		case "print":
			if !loaded {
				return []byte("Could not find service"), errors.New("exit status 113")
			}
			return []byte("system/x = {\n\tstate = running\n\tpid = 7\n}\n"), nil
		case "bootstrap":
			loaded = true
		case "bootout":
			loaded = false
		}
		return nil, nil
	}
	return &calls
}

// testLabel is the label launchdAction drives: the stock brand's.
var testLabel = identity.LaunchdLabel()

func assertCalls(t *testing.T, got *[]string, want ...string) {
	t.Helper()
	if !slices.Equal(*got, want) {
		t.Fatalf("launchctl calls = %q, want %q", *got, want)
	}
}

// install writes the plist -serviceinstall promises (this executable, the
// absolute config path, the data dir), 0644 so launchd accepts it, and
// bootstraps it into the system domain.
func TestLaunchdInstallWritesPlistAndBootstraps(t *testing.T) {
	calls := fakeLaunchctl(t, false)
	dir := t.TempDir()
	plist := filepath.Join(dir, testLabel+".plist")
	cfg := config.Configuration{ConfigPath: filepath.Join(dir, "config.yaml"), AppDataPath: filepath.Join(dir, "Data")}

	if err := launchdAction(cfg, "install", plist); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	if want := LaunchdPlist(filepath.Clean(exe), cfg.ConfigPath, cfg.AppDataPath); !bytes.Equal(got, want) {
		t.Fatalf("plist:\n%s\nwant:\n%s", got, want)
	}
	// Windows reports only the read-only bit; CI runs these tests there too.
	if fi, err := os.Stat(plist); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o644) {
		t.Fatalf("plist mode = %v, %v; want 0644", fi.Mode().Perm(), err)
	}
	assertCalls(t, calls, "print system/"+testLabel, "bootstrap system "+plist)
}

// A second install must not overwrite the plist of a loaded daemon.
func TestLaunchdInstallRefusesLoadedDaemon(t *testing.T) {
	calls := fakeLaunchctl(t, true)
	plist := filepath.Join(t.TempDir(), testLabel+".plist")
	err := launchdAction(config.Configuration{ConfigPath: "config.yaml"}, "install", plist)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want already exists", err)
	}
	if _, err := os.Stat(plist); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plist written for a loaded daemon: %v", err)
	}
	assertCalls(t, calls, "print system/"+testLabel)
}

// start restarts a loaded daemon, and bootstraps one a stop booted out.
func TestLaunchdStart(t *testing.T) {
	plist := identity.LaunchdPlistPath()
	calls := fakeLaunchctl(t, true)
	if err := launchdAction(config.Configuration{}, "start", plist); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, calls, "print system/"+testLabel, "kickstart -k system/"+testLabel)

	calls = fakeLaunchctl(t, false)
	if err := launchdAction(config.Configuration{}, "start", plist); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, calls, "print system/"+testLabel, "bootstrap system "+plist)
}

// stop boots the daemon out but keeps the plist, so start can bootstrap it
// again; remove also deletes the plist.
func TestLaunchdStopKeepsPlistRemoveDeletesIt(t *testing.T) {
	plist := filepath.Join(t.TempDir(), testLabel+".plist")
	if err := os.WriteFile(plist, []byte("plist"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := fakeLaunchctl(t, true)
	if err := launchdAction(config.Configuration{}, "stop", plist); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, calls, "print system/"+testLabel, "bootout system/"+testLabel)
	if _, err := os.Stat(plist); err != nil {
		t.Fatalf("stop removed the plist: %v", err)
	}

	calls = fakeLaunchctl(t, true)
	if err := launchdAction(config.Configuration{}, "remove", plist); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, calls, "print system/"+testLabel, "bootout system/"+testLabel)
	if _, err := os.Stat(plist); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove kept the plist: %v", err)
	}
}

func TestLaunchdServiceStatus(t *testing.T) {
	fakeLaunchctl(t, true)
	got, err := launchdServiceStatus()
	if err != nil {
		t.Fatal(err)
	}
	if want := "name: " + testLabel + "\nplist: " + identity.LaunchdPlistPath() + "\nstate: running\npid: 7"; got != want {
		t.Fatalf("status:\n%s\nwant:\n%s", got, want)
	}

	fakeLaunchctl(t, false)
	if _, err := launchdServiceStatus(); err == nil || !strings.Contains(err.Error(), "Could not find service") {
		t.Fatalf("err = %v, want launchctl's output", err)
	}
}
