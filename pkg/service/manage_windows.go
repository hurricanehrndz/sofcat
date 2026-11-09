//go:build windows

package service

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"github.com/hurricanehrndz/sofcat/pkg/sofcatlog"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// The SCM and registry calls -serviceinstall and -serviceremove make, as
// variables so the Windows tests can replace them.
var (
	serviceExistsFunc  = serviceExists
	createServiceFunc  = createService
	startServiceFunc   = startWindowsService
	stopServiceFunc    = stopWindowsService
	deleteServiceFunc  = deleteWindowsService
	setRunValueFunc    = setRunValue
	deleteRunValueFunc = deleteRunValue
	endAgentsFunc      = endAgents
)

// The key whose values Windows starts at every logon, in the 64-bit view.
// Variables so the Windows test can point the writer at a scratch key.
var (
	runRoot = registry.LOCAL_MACHINE
	runKey  = `Software\Microsoft\Windows\CurrentVersion\Run`
)

func serviceVerboseEnabled(cfg config.Configuration) bool {
	return cfg.Verbose || cfg.Debug
}

func serviceVerbose(cfg config.Configuration, format string, args ...interface{}) {
	if !serviceVerboseEnabled(cfg) {
		return
	}
	fmt.Printf("[service] "+format+"\n", args...)
}

func RunAction(cfg config.Configuration, action string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()
	serviceVerbose(cfg, "action=%s service=%q", action, cfg.ServiceName)

	switch action {
	case "install":
		return installWindowsService(m, cfg)
	case "remove":
		return removeWindowsService(m, cfg)
	case "start":
		return startWindowsService(m, cfg)
	case "stop":
		return stopWindowsService(m, cfg)
	default:
		return fmt.Errorf("unsupported service action %q", action)
	}
}

func ServiceStatus(cfg config.Configuration) (string, error) {
	m, err := mgr.Connect()
	if err != nil {
		return "", fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()

	s, err := m.OpenService(cfg.ServiceName)
	if err != nil {
		return "", fmt.Errorf("failed to open service %q: %w", cfg.ServiceName, err)
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return "", fmt.Errorf("failed to query service %q: %w", cfg.ServiceName, err)
	}
	serviceConfig, err := s.Config()
	if err != nil {
		return "", fmt.Errorf("failed to read service config for %q: %w", cfg.ServiceName, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\n", cfg.ServiceName)
	fmt.Fprintf(&b, "display_name: %s\n", serviceConfig.DisplayName)
	fmt.Fprintf(&b, "state: %s\n", serviceStateString(status.State))
	fmt.Fprintf(&b, "start_type: %s\n", serviceStartTypeString(serviceConfig.StartType))
	fmt.Fprintf(&b, "start_account: %s\n", serviceConfig.ServiceStartName)
	fmt.Fprintf(&b, "binary_path: %s\n", serviceConfig.BinaryPathName)
	fmt.Fprintf(&b, "description: %s\n", serviceConfig.Description)
	fmt.Fprintf(&b, "accepts: %s\n", serviceAcceptsString(status.Accepts))

	return strings.TrimSpace(b.String()), nil
}

func serviceStateString(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start_pending"
	case svc.StopPending:
		return "stop_pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue_pending"
	case svc.PausePending:
		return "pause_pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}

func serviceStartTypeString(startType uint32) string {
	switch startType {
	case mgr.StartAutomatic:
		return "automatic"
	case mgr.StartManual:
		return "manual"
	case mgr.StartDisabled:
		return "disabled"
	default:
		return fmt.Sprintf("unknown(%d)", startType)
	}
}

func serviceAcceptsString(accepts svc.Accepted) string {
	parts := make([]string, 0, 5)
	if accepts&svc.AcceptStop != 0 {
		parts = append(parts, "stop")
	}
	if accepts&svc.AcceptShutdown != 0 {
		parts = append(parts, "shutdown")
	}
	if accepts&svc.AcceptPauseAndContinue != 0 {
		parts = append(parts, "pause_continue")
	}
	if accepts&svc.AcceptParamChange != 0 {
		parts = append(parts, "param_change")
	}
	if accepts&svc.AcceptNetBindChange != 0 {
		parts = append(parts, "net_bind_change")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}

func installWindowsService(m *mgr.Mgr, cfg config.Configuration) error {
	if cfg.ConfigPath == "" {
		return errors.New("config path is required to install service")
	}
	configPath, err := filepath.Abs(cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to resolve config path: %w", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}
	exePath = filepath.Clean(exePath)
	startArgs := serviceInstallArgs(configPath)
	serviceVerbose(cfg, "install config: exe=%q config=%q args=%q start_type=automatic account=%q", exePath, configPath, startArgs, "LocalSystem")

	if exists, existsErr := serviceExistsFunc(m, cfg.ServiceName); existsErr != nil {
		return fmt.Errorf("failed to query existing service %q: %w", cfg.ServiceName, existsErr)
	} else if exists {
		return fmt.Errorf("service %q already exists", cfg.ServiceName)
	}

	serviceConfig := mgr.Config{
		DisplayName:      identity.Name,
		Description:      identity.Name + " application management service",
		StartType:        mgr.StartAutomatic,
		ServiceStartName: "LocalSystem",
	}
	if err := createServiceFunc(m, cfg.ServiceName, exePath, serviceConfig, startArgs...); err != nil {
		return fmt.Errorf("failed to create service %q: %w", cfg.ServiceName, err)
	}
	slog.Info("registered the service", "service", cfg.ServiceName, "exe", exePath, "event", sofcatlog.EventServiceInstalled)

	// The helper beside this executable runs at every logon. A failure from
	// here on undoes the registration, so a retried install does not meet a
	// half-finished service and refuse.
	name, data := userSyncRunValue(filepath.Join(filepath.Dir(exePath), userAgentExe()))
	if err := setRunValueFunc(name, data); err != nil {
		undoServiceInstall(m, cfg, "")
		return fmt.Errorf("failed to write Run value %q: %w", name, err)
	}
	slog.Info("wrote the Run value", "name", name, "data", data, "event", sofcatlog.EventRunValueWritten)
	if err := startServiceFunc(m, cfg); err != nil {
		undoServiceInstall(m, cfg, name)
		return err
	}

	return nil
}

// undoServiceInstall removes the Run value runValue, unless it is empty, and
// the service registration of an install that failed part way.
func undoServiceInstall(m *mgr.Mgr, cfg config.Configuration, runValue string) {
	if runValue != "" {
		if err := deleteRunValueFunc(runValue); err != nil {
			slog.Error("unable to undo the Run value", "name", runValue, "err", err, "event", sofcatlog.EventRunValueRemoved)
		} else {
			slog.Info("undid the Run value", "name", runValue, "event", sofcatlog.EventRunValueRemoved)
		}
	}
	if err := deleteServiceFunc(m, cfg); err != nil {
		slog.Error("unable to undo the service registration", "service", cfg.ServiceName, "err", err, "event", sofcatlog.EventServiceInstalled)
	} else {
		slog.Info("undid the service registration", "service", cfg.ServiceName, "event", sofcatlog.EventServiceInstalled)
	}
}

func serviceExists(m *mgr.Mgr, name string) (bool, error) {
	s, err := m.OpenService(name)
	if err == nil {
		_ = s.Close()
		return true, nil
	}
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return false, nil
	}
	return false, err
}

func createService(m *mgr.Mgr, name, exePath string, c mgr.Config, args ...string) error {
	s, err := m.CreateService(name, exePath, c, args...)
	if err != nil {
		return err
	}
	return s.Close()
}

func setRunValue(name, data string) error {
	k, err := registry.OpenKey(runRoot, runKey, registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	return k.SetStringValue(name, data)
}

// deleteRunValue removes the value; one that is already gone is not an error.
func deleteRunValue(name string) error {
	k, err := registry.OpenKey(runRoot, runKey, registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

func startWindowsService(m *mgr.Mgr, cfg config.Configuration) error {
	serviceVerbose(cfg, "start requested for service=%q", cfg.ServiceName)
	s, err := m.OpenService(cfg.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to open service %q: %w", cfg.ServiceName, err)
	}
	defer s.Close()

	status, err := s.Query()
	if err == nil && (status.State == svc.Running || status.State == svc.StartPending) {
		slog.Info("the service is already running", "service", cfg.ServiceName, "event", sofcatlog.EventServiceStarted)
		return nil
	}

	// -serviceinstall starts the service, so a -servicestart right after it
	// may race its startup.
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("failed to start service %q: %w", cfg.ServiceName, err)
	}
	slog.Info("started the service", "service", cfg.ServiceName, "event", sofcatlog.EventServiceStarted)
	return nil
}

func stopWindowsService(m *mgr.Mgr, cfg config.Configuration) error {
	serviceVerbose(cfg, "stop requested for service=%q", cfg.ServiceName)
	s, err := m.OpenService(cfg.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to open service %q: %w", cfg.ServiceName, err)
	}
	defer s.Close()

	status, err := s.Control(svc.Stop)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			slog.Info("the service is already stopped", "service", cfg.ServiceName, "event", sofcatlog.EventServiceStopped)
			return nil
		}
		if !errors.Is(err, windows.ERROR_SERVICE_CANNOT_ACCEPT_CTRL) {
			return fmt.Errorf("failed to stop service %q: %w", cfg.ServiceName, err)
		}
		// The control is refused while a stop is already in flight, so the
		// wait below finishes the job instead of failing the removal.
		serviceVerbose(cfg, "service=%q already stopping", cfg.ServiceName)
		status.State = svc.StopPending
	}

	deadline := time.Now().Add(15 * time.Second)
	for status.State != svc.Stopped {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for service %q to stop", cfg.ServiceName)
		}
		time.Sleep(300 * time.Millisecond)
		status, err = s.Query()
		if err != nil {
			return fmt.Errorf("failed querying service %q: %w", cfg.ServiceName, err)
		}
	}
	slog.Info("stopped the service", "service", cfg.ServiceName, "event", sofcatlog.EventServiceStopped)

	return nil
}

// removeWindowsService stops the service and waits for it, so the uninstaller
// that runs next can delete its executable, deletes it, removes the Run value
// and then ends the running agents beside this executable (endAgents), last,
// so no logon starts a fresh one after the sweep. A service that is already
// gone (a half-cleaned machine) only has the Run value and the agents left.
// Ending the agents never fails the remove.
func removeWindowsService(m *mgr.Mgr, cfg config.Configuration) error {
	serviceVerbose(cfg, "remove requested for service=%q", cfg.ServiceName)
	exists, err := serviceExistsFunc(m, cfg.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to query existing service %q: %w", cfg.ServiceName, err)
	}
	if exists {
		if err := stopServiceFunc(m, cfg); err != nil {
			return err
		}
		if err := deleteServiceFunc(m, cfg); err != nil {
			return err
		}
	} else {
		slog.Info("the service is not installed; removing the Run value and ending agents only", "service", cfg.ServiceName, "event", sofcatlog.EventServiceDeleted)
	}
	name, _ := userSyncRunValue("")
	if err := deleteRunValueFunc(name); err != nil {
		return fmt.Errorf("failed to remove Run value %q: %w", name, err)
	}
	slog.Info("removed the Run value", "name", name, "event", sofcatlog.EventRunValueRemoved)
	// A known and accepted race (decision 17): at a logon, Explorer can read
	// the Run value just before the delete above and start an agent after
	// the sweep below. That agent retries for a minute holding its exe, so
	// the uninstaller can leave the exe behind until the next uninstall or
	// reboot. If that ever matters, the fix is in the agent: stop retrying
	// once the Run value is gone.
	endAgentsFunc()
	return nil
}

func deleteWindowsService(m *mgr.Mgr, cfg config.Configuration) error {
	s, err := m.OpenService(cfg.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to open service %q: %w", cfg.ServiceName, err)
	}

	if err := s.Delete(); err != nil {
		_ = s.Close()
		return fmt.Errorf("failed to remove service %q: %w", cfg.ServiceName, err)
	}
	serviceVerbose(cfg, "delete issued for service=%q", cfg.ServiceName)

	// Delete marks a service for deletion; it may remain visible while other
	// processes (for example Services MMC) still hold an open handle.
	if err := s.Close(); err != nil {
		return fmt.Errorf("failed closing service handle for %q: %w", cfg.ServiceName, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		existing, err := m.OpenService(cfg.ServiceName)
		if err != nil {
			if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
				slog.Info("deleted the service", "service", cfg.ServiceName, "event", sofcatlog.EventServiceDeleted)
				return nil
			}
			return fmt.Errorf("failed to verify removal of service %q: %w", cfg.ServiceName, err)
		}
		_ = existing.Close()

		if time.Now().After(deadline) {
			return fmt.Errorf("service %q is marked for deletion but still present; close Services and try again", cfg.ServiceName)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
