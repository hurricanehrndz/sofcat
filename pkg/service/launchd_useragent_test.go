package service

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The LaunchAgent `just pkg` ships and -serviceinstall writes: launchd keys
// on the label, starts the helper once per GUI login (RunAtLoad, Aqua only,
// never kept alive) and the helper sits beside the CLI.
func TestInstalledUserAgentPlistGolden(t *testing.T) {
	origSlug, origName, origID := identity.Slug, identity.Name, identity.Identifier
	t.Cleanup(func() { identity.Slug, identity.Name, identity.Identifier = origSlug, origName, origID })
	identity.Slug, identity.Name, identity.Identifier = "tapas-test", "Tapas & Test", "ca.hrndz.tapas-test"

	const want = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>ca.hrndz.tapas-test.useragent</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Library/Application Support/Tapas &amp; Test/bin/tapas-test-useragent</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
</dict>
</plist>
`
	if got := InstalledUserAgentPlist(); string(got) != want {
		t.Fatalf("plist:\n%s\nwant:\n%s", got, want)
	}
}

// Each GUI session, fast-user-switched ones too, has a loginwindow owned by
// its user. The sign-in screen's (root) and Setup Assistant's (_mbsetupuser,
// 248) are not users to sync, a user with two sessions counts once, and only
// a process named loginwindow counts.
func TestLoginwindowUIDs(t *testing.T) {
	ps := "    0 /sbin/launchd\n" +
		"    0 /System/Library/CoreServices/loginwindow.app/Contents/MacOS/loginwindow\n" +
		"  248 /System/Library/CoreServices/loginwindow.app/Contents/MacOS/loginwindow\n" +
		"  501 /System/Library/CoreServices/loginwindow.app/Contents/MacOS/loginwindow\n" +
		"  501 /Applications/Some App.app/Contents/MacOS/Some App\n" +
		"  502 /System/Library/CoreServices/loginwindow.app/Contents/MacOS/loginwindow\n" +
		"  501 /System/Library/CoreServices/loginwindow.app/Contents/MacOS/loginwindow\n" +
		"  503 /usr/libexec/loginwindowhelper\n" +
		"\n"
	if got, want := loginwindowUIDs([]byte(ps)), []int{501, 502}; !slices.Equal(got, want) {
		t.Fatalf("uids = %v, want %v", got, want)
	}
}

// fakeGUIUsers replaces guiUsers for one test with a fixed list of uids.
func fakeGUIUsers(t *testing.T, uids ...int) {
	t.Helper()
	orig := guiUsers
	t.Cleanup(func() { guiUsers = orig })
	guiUsers = func() ([]int, error) { return uids, nil }
}

// fakeGUILaunchctl replaces launchctl for one test with per-target state:
// `print` succeeds for the targets in loaded, every call in fail fails, and
// bootstrap and bootout load and unload as launchd does. Calls are recorded.
func fakeGUILaunchctl(t *testing.T, loaded map[string]bool, fail ...string) *[]string {
	t.Helper()
	orig := launchctl
	t.Cleanup(func() { launchctl = orig })
	var calls []string
	launchctl = func(args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if slices.Contains(fail, call) {
			return []byte("Bootstrap failed: 5: Input/output error"), errors.New("exit status 5")
		}
		switch args[0] {
		case "print":
			if !loaded[args[1]] {
				return []byte("Could not find service"), errors.New("exit status 113")
			}
		case "bootstrap":
			loaded[args[1]+"/"+identity.UserAgentLabel()] = true
		case "bootout":
			delete(loaded, args[1])
		}
		return nil, nil
	}
	return &calls
}

// userSyncFixture stores a plan in a temporary data directory and creates
// the helper and its plist, so startUserSyncLaunchd has something to start.
func userSyncFixture(t *testing.T) (appData, helper, plist string) {
	t.Helper()
	dir := t.TempDir()
	appData = filepath.Join(dir, "Data")
	helper = filepath.Join(dir, identity.Slug+"-useragent")
	plist = filepath.Join(dir, identity.UserAgentLabel()+".plist")
	if err := os.MkdirAll(appData, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{UserPlanPath(appData): `{"entries":[],"facts":{}}`, helper: "helper", plist: "plist"} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return appData, helper, plist
}

// A new plan reaches every signed-in user: a kickstart where launchd loaded
// the agent at logon, a bootstrap (RunAtLoad runs it) for a user who signed
// in before the install. One user's failure leaves the plan unrecorded, so
// the next run starts the helpers again; once all start, an unchanged plan
// starts nothing.
func TestStartUserSyncLaunchd(t *testing.T) {
	appData, helper, plist := userSyncFixture(t)
	label := identity.UserAgentLabel()
	fakeGUIUsers(t, 501, 502)
	loaded := map[string]bool{"gui/501/" + label: true}
	calls := fakeGUILaunchctl(t, loaded, "bootstrap gui/502 "+plist)

	startUserSyncLaunchd(appData, helper, label, plist)
	assertCalls(t, calls,
		"print gui/501/"+label, "kickstart gui/501/"+label,
		"print gui/502/"+label, "bootstrap gui/502 "+plist)
	if _, due := userSyncDue(appData); !due {
		t.Fatal("plan recorded although user 502's helper did not start")
	}

	calls = fakeGUILaunchctl(t, loaded)
	startUserSyncLaunchd(appData, helper, label, plist)
	assertCalls(t, calls,
		"print gui/501/"+label, "kickstart gui/501/"+label,
		"print gui/502/"+label, "bootstrap gui/502 "+plist)
	if _, due := userSyncDue(appData); due {
		t.Fatal("plan not recorded after every helper started")
	}

	calls = fakeGUILaunchctl(t, loaded)
	startUserSyncLaunchd(appData, helper, label, plist)
	assertCalls(t, calls)
}

// A kickstart does nothing to a helper that is still running, which may have
// fetched its last plan before this one was saved, so a running helper must
// leave the plan due rather than be counted as started: otherwise the new
// plan waits for the user's next logon.
func TestStartUserSyncLaunchdHelperRunning(t *testing.T) {
	appData, helper, plist := userSyncFixture(t)
	label := identity.UserAgentLabel()
	fakeGUIUsers(t, 501)
	running := true
	orig := launchctl
	t.Cleanup(func() { launchctl = orig })
	var calls []string
	launchctl = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "print" && running {
			return []byte("gui/501/" + label + " = {\n\tstate = running\n\tpid = 7\n}\n"), nil
		}
		return []byte("gui/501/" + label + " = {\n\tstate = not running\n}\n"), nil
	}

	startUserSyncLaunchd(appData, helper, label, plist)
	assertCalls(t, &calls, "print gui/501/"+label)
	if _, due := userSyncDue(appData); !due {
		t.Fatal("plan recorded although the running helper was not started")
	}

	running, calls = false, nil
	startUserSyncLaunchd(appData, helper, label, plist)
	assertCalls(t, &calls, "print gui/501/"+label, "kickstart gui/501/"+label)
	if _, due := userSyncDue(appData); due {
		t.Fatal("plan not recorded once the helper started")
	}
}

// Without the helper or its plist (a CLI run from a build directory, a
// half-removed install) nothing starts and the plan stays due.
func TestStartUserSyncLaunchdNeedsHelperAndPlist(t *testing.T) {
	for _, missing := range []string{"helper", "plist"} {
		appData, helper, plist := userSyncFixture(t)
		gone := map[string]string{"helper": helper, "plist": plist}[missing]
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
		fakeGUIUsers(t, 501)
		calls := fakeGUILaunchctl(t, map[string]bool{})
		startUserSyncLaunchd(appData, helper, identity.UserAgentLabel(), plist)
		assertCalls(t, calls)
		if _, due := userSyncDue(appData); !due {
			t.Fatalf("plan recorded without the %s", missing)
		}
	}
}

// -serviceremove deletes the LaunchAgent plist and boots the agent out of
// every signed-in user's domain where it is loaded, which ends a running
// helper. A failed bootout does not fail the remove.
func TestRemoveUserAgents(t *testing.T) {
	label := identity.UserAgentLabel()
	plist := filepath.Join(t.TempDir(), label+".plist")
	if err := os.WriteFile(plist, []byte("plist"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeGUIUsers(t, 501, 502, 503)
	loaded := map[string]bool{"gui/501/" + label: true, "gui/503/" + label: true}
	calls := fakeGUILaunchctl(t, loaded, "bootout gui/503/"+label)

	if err := removeUserAgents(label, plist); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, calls,
		"print gui/501/"+label, "bootout gui/501/"+label,
		"print gui/502/"+label,
		"print gui/503/"+label, "bootout gui/503/"+label)
	if _, err := os.Stat(plist); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove kept the plist: %v", err)
	}
	if loaded["gui/501/"+label] {
		t.Fatal("user 501's agent is still loaded")
	}
}

// fakeStorefront replaces storefrontPIDs and terminate for one test: the
// storefront runs as pids, and the pids asked to exit are recorded.
func fakeStorefront(t *testing.T, pids ...int) *[]int {
	t.Helper()
	origPIDs, origTerminate := storefrontPIDs, terminate
	t.Cleanup(func() { storefrontPIDs, terminate = origPIDs, origTerminate })
	storefrontPIDs = func(string) ([]int, error) { return pids, nil }
	var ended []int
	terminate = func(pid int) error {
		ended = append(ended, pid)
		return nil
	}
	return &ended
}

// -serviceremove must end only what runs from the storefront's bundle: a
// product name with a space, but not the daemon, the helper beside it, or
// another bundle whose path merely starts with the same name.
func TestBundlePIDs(t *testing.T) {
	ps := `    1 /sbin/launchd
  412 /Library/Application Support/Tapas Test/bin/tapas-test
  733 /Applications/Tapas Test.app/Contents/MacOS/Tapas Test
  734 /Applications/Tapas Test Two.app/Contents/MacOS/Tapas Test
  801 /Users/a/Applications/Tapas Test.app/Contents/MacOS/Tapas Test
 1002 /Applications/Tapas Test.app/Contents/MacOS/Tapas Test
`
	got := bundlePIDs([]byte(ps), "/Applications/Tapas Test.app")
	if want := []int{733, 1002}; !slices.Equal(got, want) {
		t.Fatalf("bundlePIDs = %v, want %v", got, want)
	}
}
