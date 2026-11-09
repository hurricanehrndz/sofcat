package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"github.com/hurricanehrndz/sofcat/pkg/service"
	"github.com/hurricanehrndz/sofcat/pkg/sofcatlog"
)

func resetMainHooks() {
	adminCheckFunc = adminCheck
	mkdirAllFunc = os.MkdirAll
	newReportFunc = report.New
	managedRunFunc = managedRun
	runServiceFunc = runService
	sendServiceCommandFunc = service.SendCommand
	runServiceActionFunc = service.RunAction
	serviceStatusFunc = service.ServiceStatus
	protectAppDataFunc = func(string) error { return nil }
	protectConfigFunc = func(string) error { return nil }
	// Never register the brand's event source on a test machine.
	startManagementLogFunc = func(string, bool, io.Writer) func() { return func() {} }
	newZipInstaller = installer.NewMachineZip
}

func TestRunAdminCheckError(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	cfg := config.Configuration{CheckOnly: false}
	adminCheckFunc = func() (bool, error) { return false, errors.New("boom") }
	mkdirAllFunc = func(path string, mode os.FileMode) error {
		t.Fatalf("mkdirAllFunc should not be called when admin check errors")
		return nil
	}

	_, err := managedRun(cfg, nil, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "unable to check if running as admin: boom") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRequiresAdmin(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	cfg := config.Configuration{CheckOnly: false}
	adminCheckFunc = func() (bool, error) { return false, nil }
	mkdirAllFunc = func(path string, mode os.FileMode) error {
		t.Fatalf("mkdirAllFunc should not be called when admin check fails")
		return nil
	}

	_, err := managedRun(cfg, nil, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "requires admnisistrative access") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCheckOnlySkipsAdminCheck(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	cfg := config.Configuration{CheckOnly: true, CachePath: "some/cache/path"}
	adminCalled := false
	adminCheckFunc = func() (bool, error) {
		adminCalled = true
		return false, nil
	}
	mkdirAllFunc = func(path string, mode os.FileMode) error { return errors.New("mkdir failed") }

	_, err := managedRun(cfg, nil, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if adminCalled {
		t.Fatalf("adminCheckFunc should not be called in check-only mode")
	}
	if !strings.Contains(err.Error(), "unable to create cache directory: mkdir failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCreateCacheError(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	cfg := config.Configuration{CheckOnly: false, CachePath: "some/cache/path"}
	adminCheckFunc = func() (bool, error) { return true, nil }
	mkdirAllFunc = func(path string, mode os.FileMode) error { return errors.New("mkdir failed") }

	_, err := managedRun(cfg, nil, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "unable to create cache directory: mkdir failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManagedRunFinalizesReportOnManifestError(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	cfg := config.Configuration{
		CheckOnly:   false,
		CachePath:   t.TempDir(),
		AppDataPath: t.TempDir(),
		URL:         "http://127.0.0.1:1/",
		Manifest:    "missing-manifest",
	}

	var captured *report.Report
	newReportFunc = func() *report.Report {
		captured = report.New()
		return captured
	}
	t.Cleanup(sofcatlog.Close)

	adminCheckFunc = func() (bool, error) { return true, nil }
	mkdirAllFunc = func(path string, mode os.FileMode) error { return nil }

	_, err := managedRun(cfg, nil, nil)
	if err == nil {
		t.Fatalf("expected error from manifest retrieval")
	}

	if captured == nil {
		t.Fatalf("expected managedRun to build a run report")
	}
	// A failed run must still leave an inventory naming the failure, or
	// osquery keeps reporting the previous run as current.
	inv := readInventory(t, cfg.AppDataPath)
	if inv.EndTime == "" {
		t.Errorf("expected inventory EndTime to be set on manifest retrieval failure")
	}
	if len(inv.Errors) != 1 || !strings.Contains(inv.Errors[0], "unable to retrieve manifest") {
		t.Errorf("expected the manifest error in inventory Errors, got %#v", inv.Errors)
	}
}

// readInventory decodes the inventory.json a real run wrote under appData.
func readInventory(t *testing.T, appData string) report.Inventory {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(appData, inventoryFile))
	if err != nil {
		t.Fatalf("read inventory: %v", err)
	}
	var inv report.Inventory
	if err := json.Unmarshal(data, &inv); err != nil {
		t.Fatalf("decode inventory: %v", err)
	}
	return inv
}

// TestManagedRunStateIsRunScoped verifies that each managedRun builds a fresh
// report, so a second run in the same process (service pipe `run` actions)
// does not contain the previous run's items (K7)
func TestManagedRunStateIsRunScoped(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	cfg := config.Configuration{
		CheckOnly:   false,
		CachePath:   t.TempDir(),
		AppDataPath: t.TempDir(),
		URL:         "http://127.0.0.1:1/",
		Manifest:    "missing-manifest",
	}

	var reports []*report.Report
	newReportFunc = func() *report.Report {
		r := report.New()
		reports = append(reports, r)
		return r
	}
	t.Cleanup(sofcatlog.Close)

	adminCheckFunc = func() (bool, error) { return true, nil }
	mkdirAllFunc = func(path string, mode os.FileMode) error { return nil }

	// Run 1 fails at manifest retrieval; simulate items recorded during it
	if _, err := managedRun(cfg, nil, nil); err == nil {
		t.Fatalf("expected error from manifest retrieval")
	}
	reports[0].InstalledItems = append(reports[0].InstalledItems, catalog.Item{DisplayName: "run1-item"})
	reports[0].FailedItems = append(reports[0].FailedItems, report.FailedItem{Name: "run1-failure"})

	// Run 2 must build fresh state that shares nothing with run 1
	if _, err := managedRun(cfg, nil, nil); err == nil {
		t.Fatalf("expected error from manifest retrieval")
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 run reports, got %d", len(reports))
	}
	if reports[1] == reports[0] {
		t.Fatalf("managedRun reused the previous run's report (K7)")
	}
	if len(reports[1].InstalledItems) != 0 || len(reports[1].FailedItems) != 0 {
		t.Errorf("run 2 report contains run 1 items (K7): %#v", reports[1])
	}
}

func TestExecuteServiceModesSkipRun(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	serviceAction := ""
	serviceCommand := ""
	serviceMode := false
	serviceStatusCalled := false
	runCalled := false

	managedRunFunc = func(cfg config.Configuration, progress installer.ProgressFn, _ *installer.Cancels) (*report.Report, error) {
		if progress != nil {
			t.Fatal("ordinary route unexpectedly supplied progress callback")
		}
		runCalled = true
		return nil, nil
	}
	runServiceActionFunc = func(cfg config.Configuration, action string) error {
		serviceAction = action
		return nil
	}
	sendServiceCommandFunc = func(cfg config.Configuration, spec string) (service.CommandResponse, error) {
		serviceCommand = spec
		return service.CommandResponse{Status: "ok"}, nil
	}
	runServiceFunc = func(cfg config.Configuration) error {
		serviceMode = true
		return nil
	}
	serviceStatusFunc = func(cfg config.Configuration) (string, error) {
		serviceStatusCalled = true
		return "running", nil
	}

	tests := []struct {
		name          string
		cfg           config.Configuration
		wantAction    string
		wantCommand   string
		wantSvcMode   bool
		wantSvcStatus bool
		expectRunCall bool
	}{
		{
			name: "service install",
			cfg: config.Configuration{
				ServiceInstall: true,
			},
			wantAction:    "install",
			expectRunCall: false,
		},
		{
			name: "service remove",
			cfg: config.Configuration{
				ServiceRemove: true,
			},
			wantAction:    "remove",
			expectRunCall: false,
		},
		{
			name: "service start",
			cfg: config.Configuration{
				ServiceStart: true,
			},
			wantAction:    "start",
			expectRunCall: false,
		},
		{
			name: "service stop",
			cfg: config.Configuration{
				ServiceStop: true,
			},
			wantAction:    "stop",
			expectRunCall: false,
		},
		{
			name: "service status",
			cfg: config.Configuration{
				ServiceStatus: true,
			},
			wantSvcStatus: true,
			expectRunCall: false,
		},
		{
			name: "service command",
			cfg: config.Configuration{
				ServiceCommand: "ListOptionalInstalls",
			},
			wantCommand:   "ListOptionalInstalls",
			expectRunCall: false,
		},
		{
			name: "service mode",
			cfg: config.Configuration{
				ServiceMode: true,
			},
			wantSvcMode:   true,
			expectRunCall: false,
		},
		{
			name:          "normal mode",
			cfg:           config.Configuration{},
			expectRunCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceAction = ""
			serviceCommand = ""
			serviceMode = false
			serviceStatusCalled = false
			runCalled = false

			err := route(tt.cfg)
			if err != nil {
				t.Fatalf("execute returned unexpected error: %v", err)
			}

			if runCalled != tt.expectRunCall {
				t.Fatalf("managedRun called = %v, expected %v", runCalled, tt.expectRunCall)
			}
			if serviceAction != tt.wantAction {
				t.Fatalf("service action = %q, expected %q", serviceAction, tt.wantAction)
			}
			if serviceCommand != tt.wantCommand {
				t.Fatalf("service command = %q, expected %q", serviceCommand, tt.wantCommand)
			}
			if serviceMode != tt.wantSvcMode {
				t.Fatalf("service mode call = %v, expected %v", serviceMode, tt.wantSvcMode)
			}
			if serviceStatusCalled != tt.wantSvcStatus {
				t.Fatalf("service status call = %v, expected %v", serviceStatusCalled, tt.wantSvcStatus)
			}
		})
	}
}

func TestRoutePrecedenceServiceInstallWins(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	serviceAction := ""
	serviceCommandCalled := false
	serviceModeCalled := false
	serviceStatusCalled := false
	runCalled := false

	runServiceActionFunc = func(cfg config.Configuration, action string) error {
		serviceAction = action
		return nil
	}
	sendServiceCommandFunc = func(cfg config.Configuration, spec string) (service.CommandResponse, error) {
		serviceCommandCalled = true
		return service.CommandResponse{Status: "ok"}, nil
	}
	runServiceFunc = func(cfg config.Configuration) error {
		serviceModeCalled = true
		return nil
	}
	serviceStatusFunc = func(cfg config.Configuration) (string, error) {
		serviceStatusCalled = true
		return "running", nil
	}
	managedRunFunc = func(cfg config.Configuration, _ installer.ProgressFn, _ *installer.Cancels) (*report.Report, error) {
		runCalled = true
		return nil, nil
	}

	cfg := config.Configuration{
		ServiceInstall: true,
		ServiceRemove:  true,
		ServiceStart:   true,
		ServiceStop:    true,
		ServiceStatus:  true,
		ServiceCommand: "ListOptionalInstalls",
		ServiceMode:    true,
	}
	if err := route(cfg); err != nil {
		t.Fatalf("unexpected route error: %v", err)
	}

	if serviceAction != "install" {
		t.Fatalf("expected install action, got %q", serviceAction)
	}
	if serviceCommandCalled {
		t.Fatalf("service command branch should not run when service install is set")
	}
	if serviceModeCalled {
		t.Fatalf("service mode branch should not run when service install is set")
	}
	if serviceStatusCalled {
		t.Fatalf("service status branch should not run when service install is set")
	}
	if runCalled {
		t.Fatalf("managedRun should not run when service install is set")
	}
}

func TestRouteServiceCommandPrintsItems(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	sendServiceCommandFunc = func(cfg config.Configuration, spec string) (service.CommandResponse, error) {
		return service.CommandResponse{
			Status: "ok",
			Items:  []string{"GoogleChrome", "VSCode"},
		}, nil
	}

	stdout := captureStdout(t, func() {
		err := route(config.Configuration{ServiceCommand: "ListOptionalInstalls"})
		if err != nil {
			t.Fatalf("unexpected route error: %v", err)
		}
	})

	if !strings.Contains(stdout, "GoogleChrome") || !strings.Contains(stdout, "VSCode") {
		t.Fatalf("expected stdout to include response items, got %q", stdout)
	}
}

func TestRouteServiceStatusPrintsValue(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	serviceStatusFunc = func(cfg config.Configuration) (string, error) {
		return "state: running\nstart_type: automatic", nil
	}

	stdout := captureStdout(t, func() {
		err := route(config.Configuration{ServiceStatus: true})
		if err != nil {
			t.Fatalf("unexpected route error: %v", err)
		}
	})

	if !strings.Contains(stdout, "Service status:") {
		t.Fatalf("expected stdout to include service status header, got %q", stdout)
	}
	if !strings.Contains(stdout, "state: running") {
		t.Fatalf("expected stdout to include service status, got %q", stdout)
	}
}

func TestRouteServiceActionPrintsSuccess(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	runServiceActionFunc = func(cfg config.Configuration, action string) error {
		return nil
	}

	tests := []struct {
		name       string
		cfg        config.Configuration
		wantOutput string
	}{
		{
			name: "service install",
			cfg: config.Configuration{
				ServiceInstall: true,
			},
			wantOutput: "Service installed successfully",
		},
		{
			name: "service remove",
			cfg: config.Configuration{
				ServiceRemove: true,
			},
			wantOutput: "Service removed successfully",
		},
		{
			name: "service start",
			cfg: config.Configuration{
				ServiceStart: true,
			},
			wantOutput: "Service started successfully",
		},
		{
			name: "service stop",
			cfg: config.Configuration{
				ServiceStop: true,
			},
			wantOutput: "Service stopped successfully",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := captureStdout(t, func() {
				err := route(tt.cfg)
				if err != nil {
					t.Fatalf("unexpected route error: %v", err)
				}
			})

			if !strings.Contains(stdout, tt.wantOutput) {
				t.Fatalf("expected stdout to include %q, got %q", tt.wantOutput, stdout)
			}
		})
	}
}

// A management command logs its steps and outcome through the event-log tee
// under the brand's source, so a silent uninstall leaves a record: here, a
// data directory it could not protect and the failure itself.
func TestRouteServiceActionLogsToManagementLog(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	var logged bytes.Buffer
	var source string
	startManagementLogFunc = func(src string, _ bool, _ io.Writer) func() {
		source = src
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
		return func() { slog.SetDefault(prev) }
	}
	protectAppDataFunc = func(string) error { return errors.New("access denied") }
	runServiceActionFunc = func(config.Configuration, string) error { return errors.New("scm unavailable") }

	if err := route(config.Configuration{ServiceInstall: true}); err == nil {
		t.Fatal("route succeeded, want the action's error")
	}
	if source != identity.Slug {
		t.Errorf("event source = %q, want %q", source, identity.Slug)
	}
	out := logged.String()
	for _, s := range []string{
		`level=WARN msg="unable to protect the data directory"`,
		`level=ERROR msg="-serviceinstall failed" err="scm unavailable"`,
	} {
		if !strings.Contains(out, s) {
			t.Errorf("log lacks %q:\n%s", s, out)
		}
	}
}

func TestRouteServiceCommandPrintsSuccessWhenNoItems(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	sendServiceCommandFunc = func(cfg config.Configuration, spec string) (service.CommandResponse, error) {
		return service.CommandResponse{Status: "ok"}, nil
	}

	stdout := captureStdout(t, func() {
		err := route(config.Configuration{ServiceCommand: "InstallItem:GoogleChrome"})
		if err != nil {
			t.Fatalf("unexpected route error: %v", err)
		}
	})

	if !strings.Contains(stdout, "InstallItem command completed successfully") {
		t.Fatalf("expected stdout to include success message, got %q", stdout)
	}
}

func TestRouteServiceCommandPrintsNoneForEmptyListOptionalInstalls(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	sendServiceCommandFunc = func(cfg config.Configuration, spec string) (service.CommandResponse, error) {
		return service.CommandResponse{Status: "ok"}, nil
	}

	stdout := captureStdout(t, func() {
		err := route(config.Configuration{ServiceCommand: "ListOptionalInstalls"})
		if err != nil {
			t.Fatalf("unexpected route error: %v", err)
		}
	})

	if strings.TrimSpace(stdout) != "none" {
		t.Fatalf("expected stdout to be none, got %q", stdout)
	}
}

func TestRouteServiceCommandErrorDoesNotPrintItems(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	sendServiceCommandFunc = func(cfg config.Configuration, spec string) (service.CommandResponse, error) {
		return service.CommandResponse{}, errors.New("boom")
	}

	stdout := captureStdout(t, func() {
		err := route(config.Configuration{ServiceCommand: "ListOptionalInstalls"})
		if err == nil {
			t.Fatalf("expected route error")
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("expected no stdout output on command error, got %q", stdout)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe creation failed: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("stdout pipe close failed: %v", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("stdout pipe read failed: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("stdout pipe close failed: %v", err)
	}

	return fmt.Sprint(buf.String())
}

// TestRouteServiceInstallProtectsAppData: -serviceinstall must protect the
// data directory and the config file, and a failure to do so must not stop
// the install. The installer runs it before any config.yaml exists, so the
// paths are protected as paths: neither needs to be there.
func TestRouteServiceInstallProtectsAppData(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()

	protected := ""
	installed := false
	protectAppDataFunc = func(path string) error {
		protected = path
		return errors.New("access denied")
	}
	protectedConfig := ""
	protectConfigFunc = func(path string) error {
		protectedConfig = path
		return errors.New("access denied")
	}
	runServiceActionFunc = func(cfg config.Configuration, action string) error {
		installed = action == "install"
		return nil
	}

	cfg := config.Configuration{ServiceInstall: true, AppDataPath: `C:\ProgramData\SofCat\Data`, ConfigPath: `C:\ProgramData\SofCat\config.yaml`}
	if err := route(cfg); err != nil {
		t.Fatalf("route: %v", err)
	}
	if protected != cfg.AppDataPath {
		t.Errorf("protected %q, want %q", protected, cfg.AppDataPath)
	}
	if protectedConfig != cfg.ConfigPath {
		t.Errorf("protected config %q, want %q", protectedConfig, cfg.ConfigPath)
	}
	if !installed {
		t.Error("install did not run after the protect failure")
	}
}

// TestRouteManagedRunPrintsWaitingItems: after a run from the command line,
// an admin sees which items wait and why, without reading the log.
func TestRouteManagedRunPrintsWaitingItems(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	managedRunFunc = func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		return &report.Report{
			DeferredItems: []report.DeferredItem{{
				Name: "tool", Action: "install", Code: report.CodeMissingSoftware,
				Reason: "Needs Demo Tool. Install it first, then try again.",
			}},
			HeldConditions: []report.HeldCondition{{
				Condition: `lab == "yes"`, Missing: []string{"lab"}, Items: []string{"a", "b"}, Reason: "facts unavailable",
			}},
		}, nil
	}
	stdout := captureStdout(t, func() {
		if err := route(config.Configuration{}); err != nil {
			t.Fatalf("route: %v", err)
		}
	})
	want := "deferred: tool (install): Needs Demo Tool. Install it first, then try again.\n" +
		"held: conditional_items \"lab == \\\"yes\\\"\": facts unavailable (lab); items: a, b\n"
	if stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
	}
}

// TestPrintLaunchdPlist: `just pkg` ships whatever -print-launchd-plist
// prints, so it must be exactly the installed-layout plist, and no other
// argument list may trigger it.
func TestPrintLaunchdPlist(t *testing.T) {
	var buf bytes.Buffer
	if !printLaunchdPlist([]string{"sofcat", "-print-launchd-plist"}, &buf) {
		t.Fatal("-print-launchd-plist not handled")
	}
	want := service.LaunchdPlist(identity.CLIPath(), identity.ConfigPathFor("darwin"), identity.DataDirFor("darwin"))
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("printed:\n%s\nwant:\n%s", buf.Bytes(), want)
	}

	buf.Reset()
	for _, args := range [][]string{{"sofcat"}, {"sofcat", "-c", "x"}, {"sofcat", "-print-launchd-plist", "-c", "x"}} {
		if printLaunchdPlist(args, &buf) || buf.Len() != 0 {
			t.Fatalf("%q handled as -print-launchd-plist", args)
		}
	}
}
