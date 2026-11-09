//go:build windows

package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/mgr"
)

// fakeSCM replaces every SCM and registry call with a recorder, so the install
// and remove orchestration runs without a service manager. Each call appends a
// line to calls; the fail map makes a step return an error.
type fakeSCM struct {
	calls  []string
	fail   map[string]error
	config mgr.Config
	args   []string
	exists bool
}

func installFakeSCM(t *testing.T) *fakeSCM {
	t.Helper()
	f := &fakeSCM{fail: map[string]error{}}
	step := func(name string) error {
		f.calls = append(f.calls, name)
		return f.fail[name]
	}
	serviceExistsFunc = func(*mgr.Mgr, string) (bool, error) { return f.exists, step("exists") }
	createServiceFunc = func(_ *mgr.Mgr, _, _ string, c mgr.Config, args ...string) error {
		f.config, f.args = c, args
		return step("create")
	}
	startServiceFunc = func(*mgr.Mgr, config.Configuration) error { return step("start") }
	stopServiceFunc = func(*mgr.Mgr, config.Configuration) error { return step("stop") }
	deleteServiceFunc = func(*mgr.Mgr, config.Configuration) error { return step("delete") }
	setRunValueFunc = func(name, data string) error { return step("set " + name + "=" + data) }
	deleteRunValueFunc = func(name string) error { return step("unset " + name) }
	t.Cleanup(func() {
		serviceExistsFunc = serviceExists
		createServiceFunc = createService
		startServiceFunc = startWindowsService
		stopServiceFunc = stopWindowsService
		deleteServiceFunc = deleteWindowsService
		setRunValueFunc = setRunValue
		deleteRunValueFunc = deleteRunValue
	})
	return f
}

func expectCalls(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// -serviceinstall is what the installer runs: it must leave a service named
// after the brand, the helper's logon entry beside the executable, and the
// service running, in that order.
func TestInstallWindowsServiceRegistersHelperAndStarts(t *testing.T) {
	f := installFakeSCM(t)
	cfg := config.Configuration{ServiceName: "sofcat", ConfigPath: `C:\ProgramData\SofCat\config.yaml`}

	if err := installWindowsService(nil, cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agentExe := filepath.Join(filepath.Dir(exe), userAgentExe())
	expectCalls(t, f.calls, []string{
		"exists",
		"create",
		fmt.Sprintf(`set %s-user-sync="%s"`, identity.Slug, agentExe),
		"start",
	})
	if f.config.DisplayName != identity.Name || f.config.Description != identity.Name+" application management service" {
		t.Errorf("service config = %+v, want display name %q", f.config, identity.Name)
	}
	if f.config.StartType != mgr.StartAutomatic || f.config.ServiceStartName != "LocalSystem" {
		t.Errorf("service config = %+v, want automatic start as LocalSystem", f.config)
	}
	if strings.Join(f.args, " ") != `-c C:\ProgramData\SofCat\config.yaml -service` {
		t.Errorf("start args = %q", f.args)
	}
}

func TestInstallWindowsServiceRefusesExisting(t *testing.T) {
	f := installFakeSCM(t)
	f.exists = true
	cfg := config.Configuration{ServiceName: "sofcat", ConfigPath: `C:\ProgramData\SofCat\config.yaml`}

	err := installWindowsService(nil, cfg)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("install over an existing service: %v", err)
	}
	expectCalls(t, f.calls, []string{"exists"})
}

// A start failure must not leave a registered service behind: the installer
// rolls its files back, and the next attempt would otherwise be refused.
func TestInstallWindowsServiceUndoesOnStartFailure(t *testing.T) {
	f := installFakeSCM(t)
	f.fail["start"] = errors.New("boom")
	cfg := config.Configuration{ServiceName: "sofcat", ConfigPath: `C:\ProgramData\SofCat\config.yaml`}

	if err := installWindowsService(nil, cfg); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("install with a failing start: %v", err)
	}
	if n := len(f.calls); n != 6 || f.calls[4] != "unset "+identity.Slug+"-user-sync" || f.calls[5] != "delete" {
		t.Errorf("calls after the start failure: %q", f.calls)
	}
}

// A Run value write failure undoes the registration the same way; the
// service has not been started yet, so only the service goes.
func TestInstallWindowsServiceUndoesOnRunValueFailure(t *testing.T) {
	f := installFakeSCM(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name, data := userSyncRunValue(filepath.Join(filepath.Dir(exe), userAgentExe()))
	f.fail["set "+name+"="+data] = errors.New("boom")
	cfg := config.Configuration{ServiceName: "sofcat", ConfigPath: `C:\ProgramData\SofCat\config.yaml`}

	err = installWindowsService(nil, cfg)
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), name) {
		t.Fatalf("install with a failing Run value write: %v", err)
	}
	expectCalls(t, f.calls, []string{"exists", "create", "set " + name + "=" + data, "delete"})
}

// -serviceremove runs from the uninstaller before it deletes the executable,
// so the stop (which waits for Stopped) comes first; the Run value goes last.
func TestRemoveWindowsServiceStopsDeletesAndUnregistersHelper(t *testing.T) {
	f := installFakeSCM(t)
	f.exists = true
	cfg := config.Configuration{ServiceName: "sofcat"}

	if err := removeWindowsService(nil, cfg); err != nil {
		t.Fatalf("remove: %v", err)
	}
	expectCalls(t, f.calls, []string{"exists", "stop", "delete", "unset " + identity.Slug + "-user-sync"})

	f.calls = nil
	f.fail["stop"] = errors.New("timed out")
	if err := removeWindowsService(nil, cfg); err == nil {
		t.Fatal("remove with a service that will not stop succeeded")
	}
	expectCalls(t, f.calls, []string{"exists", "stop"})
}

// On a half-cleaned machine (service deleted by hand, Run value left behind)
// the uninstaller must still succeed and take the value with it.
func TestRemoveWindowsServiceWithoutServiceRemovesRunValue(t *testing.T) {
	f := installFakeSCM(t)
	cfg := config.Configuration{ServiceName: "sofcat"}

	if err := removeWindowsService(nil, cfg); err != nil {
		t.Fatalf("remove without a service: %v", err)
	}
	expectCalls(t, f.calls, []string{"exists", "unset " + identity.Slug + "-user-sync"})

	f.calls = nil
	f.fail["exists"] = errors.New("access denied")
	if err := removeWindowsService(nil, cfg); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("remove with an unanswerable service query: %v", err)
	}
	expectCalls(t, f.calls, []string{"exists"})
}

// The real registry writer and remover, against a scratch key under HKCU.
// Removing a value that is already gone is not an error, so a second
// uninstall or a hand-cleaned machine does not fail.
func TestRunValueWriteAndRemove(t *testing.T) {
	origRoot, origKey := runRoot, runKey
	runRoot, runKey = registry.CURRENT_USER, fmt.Sprintf(`Software\sofcat-test\run-%d`, os.Getpid())
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, runKey)
		runRoot, runKey = origRoot, origKey
	})
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	_ = k.Close()

	name, data := userSyncRunValue(`C:\Program Files\SofCat\sofcat-useragent.exe`)
	if err = setRunValueFunc(name, data); err != nil {
		t.Fatalf("set: %v", err)
	}
	k, err = registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := k.GetStringValue(name)
	_ = k.Close()
	if err != nil || got != data {
		t.Fatalf("value %q = %q, %v; want %q", name, got, err, data)
	}

	if err = deleteRunValueFunc(name); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err = deleteRunValueFunc(name); err != nil {
		t.Fatalf("delete of a missing value: %v", err)
	}
}
