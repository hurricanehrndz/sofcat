//go:build windows

package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
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
	deleteRunValueFunc = func(name string) (bool, error) { return true, step("unset " + name) }
	endAgentsFunc = func() { _ = step("end agents") }
	t.Cleanup(func() {
		serviceExistsFunc = serviceExists
		createServiceFunc = createService
		startServiceFunc = startWindowsService
		stopServiceFunc = stopWindowsService
		deleteServiceFunc = deleteWindowsService
		setRunValueFunc = setRunValue
		deleteRunValueFunc = deleteRunValue
		endAgentsFunc = endAgents
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
// rolls its files back, and the next attempt would otherwise be refused. The
// stop comes before the delete: a start that timed out may still be running
// the executable the installer is about to roll back.
func TestInstallWindowsServiceUndoesOnStartFailure(t *testing.T) {
	f := installFakeSCM(t)
	f.fail["start"] = errors.New("boom")
	// A failing stop must not keep the registration.
	f.fail["stop"] = errors.New("still starting")
	cfg := config.Configuration{ServiceName: "sofcat", ConfigPath: `C:\ProgramData\SofCat\config.yaml`}

	if err := installWindowsService(nil, cfg); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("install with a failing start: %v", err)
	}
	if n := len(f.calls); n != 7 || f.calls[4] != "unset "+identity.Slug+"-user-sync" || f.calls[5] != "stop" || f.calls[6] != "delete" {
		t.Errorf("calls after the start failure: %q", f.calls)
	}
}

// A Run value write failure undoes the registration the same way; the
// service has not been started yet, so the stop finds it stopped and only
// the service goes.
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
	expectCalls(t, f.calls, []string{"exists", "create", "set " + name + "=" + data, "stop", "delete"})
}

// -serviceremove runs from the uninstaller before it deletes the executable,
// so the stop (which waits for Stopped) comes first. The Run value goes next
// and the running agents last, so no logon starts one after the sweep.
func TestRemoveWindowsServiceStopsDeletesAndUnregistersHelper(t *testing.T) {
	f := installFakeSCM(t)
	f.exists = true
	cfg := config.Configuration{ServiceName: "sofcat"}

	if err := removeWindowsService(nil, cfg); err != nil {
		t.Fatalf("remove: %v", err)
	}
	expectCalls(t, f.calls, []string{"exists", "stop", "delete", "unset " + identity.Slug + "-user-sync", "end agents"})

	f.calls = nil
	f.fail["stop"] = errors.New("timed out")
	if err := removeWindowsService(nil, cfg); err == nil {
		t.Fatal("remove with a service that will not stop succeeded")
	}
	expectCalls(t, f.calls, []string{"exists", "stop"})
}

// On a half-cleaned machine (service deleted by hand, Run value left behind)
// the uninstaller must still succeed, take the value with it and end the
// agents.
func TestRemoveWindowsServiceWithoutServiceRemovesRunValue(t *testing.T) {
	f := installFakeSCM(t)
	cfg := config.Configuration{ServiceName: "sofcat"}

	if err := removeWindowsService(nil, cfg); err != nil {
		t.Fatalf("remove without a service: %v", err)
	}
	expectCalls(t, f.calls, []string{"exists", "unset " + identity.Slug + "-user-sync", "end agents"})

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

	// The uninstaller runs -serviceremove twice; the second run must say the
	// value was already gone, not that it removed it.
	removed, err := deleteRunValueFunc(name)
	if err != nil || !removed {
		t.Fatalf("delete = %v, %v; want true, nil", removed, err)
	}
	if removed, err = deleteRunValueFunc(name); err != nil || removed {
		t.Fatalf("delete of a missing value = %v, %v; want false, nil", removed, err)
	}
}

// -serviceremove ends the storefront as well as the helper: an open
// storefront keeps <slug>-ui.exe locked, and the uninstaller then cannot
// delete it. Both names carry the brand's slug.
func TestAgentExesIncludeStorefront(t *testing.T) {
	origSlug := identity.Slug
	defer func() { identity.Slug = origSlug }()
	identity.Slug = "tapas-test"

	got := strings.Join(agentExes(), " ")
	if want := "tapas-test-useragent.exe tapas-test-ui.exe"; got != want {
		t.Errorf("agentExes() = %q, want %q", got, want)
	}
}

// The real sweep, with no agent beside the test binary: it lists every
// process, opens none of them, and returns.
func TestEndAgentsWithNothingToEnd(t *testing.T) {
	endAgents()
}

// startAndWait starts only a service that is neither running nor starting,
// then waits for it to run. A service already starting (the automatic start
// at boot) is waited on, not started again; ERROR_SERVICE_ALREADY_RUNNING
// from a start that lost the race is not a failure; and a stopping service
// gets its own error instead of a misleading "did not stay running".
func TestStartAndWait(t *testing.T) {
	orig := startTimeout
	startTimeout = 50 * time.Millisecond
	t.Cleanup(func() { startTimeout = orig })

	tests := []struct {
		name       string
		states     []svc.State
		startErr   error
		wantStarts int
		wantErr    string
	}{
		{"already running", []svc.State{svc.Running}, nil, 0, ""},
		{"already starting", []svc.State{svc.StartPending, svc.StartPending, svc.Running}, nil, 0, ""},
		{"starts and runs", []svc.State{svc.Stopped, svc.StartPending, svc.Running}, nil, 1, ""},
		{"lost the race", []svc.State{svc.Stopped, svc.Running}, windows.ERROR_SERVICE_ALREADY_RUNNING, 1, ""},
		{"start refused", []svc.State{svc.Stopped}, windows.ERROR_SERVICE_DISABLED, 1, "failed to start"},
		{"stops at startup", []svc.State{svc.Stopped, svc.StartPending, svc.Stopped}, nil, 1, "did not stay running"},
		{"stays pending", []svc.State{svc.Stopped, svc.StartPending}, nil, 1, "timed out"},
		{"stopping", []svc.State{svc.StopPending}, nil, 0, "is stopping; run the command again once it has stopped"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queries, starts := 0, 0
			query := func() (svc.Status, error) {
				s := tt.states[min(queries, len(tt.states)-1)]
				queries++
				return svc.Status{State: s}, nil
			}
			start := func() error { starts++; return tt.startErr }
			err := startAndWait(config.Configuration{ServiceName: "sofcat"}, query, start)
			if starts != tt.wantStarts {
				t.Errorf("started %d times, want %d", starts, tt.wantStarts)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("startAndWait: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("startAndWait error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// The wait reads the service's status: running and start pending are the
// only states a starting service may be in, and anything else is a failed
// start whose exit code the error carries, the service's own when it set one.
func TestServiceStartProgress(t *testing.T) {
	tests := []struct {
		status    svc.Status
		want      startProgress
		wantState string
	}{
		{svc.Status{State: svc.Running}, startRunning, "running"},
		{svc.Status{State: svc.StartPending}, startPending, "start_pending"},
		{svc.Status{State: svc.Stopped, Win32ExitCode: 1}, startFailed, "state stopped, exit code 1"},
		{svc.Status{State: svc.Stopped, Win32ExitCode: uint32(windows.ERROR_SERVICE_SPECIFIC_ERROR), ServiceSpecificExitCode: 7}, startFailed, "state stopped, exit code 7"},
		{svc.Status{State: svc.StopPending}, startFailed, "state stop_pending, exit code 0"},
	}
	for _, tt := range tests {
		got, state := serviceStartProgress(tt.status)
		if got != tt.want || state != tt.wantState {
			t.Errorf("serviceStartProgress(%+v) = %v, %q; want %v, %q", tt.status, got, state, tt.want, tt.wantState)
		}
	}
}
