package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"github.com/hurricanehrndz/sofcat/pkg/service"
	"github.com/hurricanehrndz/sofcat/pkg/sofcatlog"
)

var (
	managedRunFunc         = managedRun
	runServiceFunc         = runService
	sendServiceCommandFunc = service.SendCommand
	runServiceActionFunc   = service.RunAction
	serviceStatusFunc      = service.ServiceStatus
	protectAppDataFunc     = protectAppData
	protectConfigFunc      = protectConfig
	startManagementLogFunc = sofcatlog.StartManagementLog
)

func main() {
	if printLaunchdPlist(os.Args, os.Stdout) {
		return
	}
	cfg := config.Get()
	if err := route(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// printLaunchdPlist handles the hidden -print-launchd-plist and
// -print-useragent-plist, for `just pkg`: each writes a launchd plist the
// package installs, the daemon's or the per-user helper's LaunchAgent, the
// same bytes -serviceinstall writes for the installed layout. They need no
// config, so main runs them before config.Get.
func printLaunchdPlist(args []string, w io.Writer) bool {
	if len(args) != 2 {
		return false
	}
	switch args[1] {
	case "-print-launchd-plist":
		_, _ = w.Write(service.InstalledLaunchdPlist())
	case "-print-useragent-plist":
		_, _ = w.Write(service.InstalledUserAgentPlist())
	default:
		return false
	}
	return true
}

func route(cfg config.Configuration) error {
	switch {
	case cfg.ServiceInstall:
		return manageService(cfg, "install", "installed")
	case cfg.ServiceRemove:
		return manageService(cfg, "remove", "removed")
	case cfg.ServiceStart:
		return manageService(cfg, "start", "started")
	case cfg.ServiceStop:
		return manageService(cfg, "stop", "stopped")
	}

	if cfg.ServiceStatus {
		status, err := serviceStatusFunc(cfg)
		if err != nil {
			return err
		}
		fmt.Println("Service status:")
		fmt.Println(status)
		return nil
	}

	if cfg.ServiceCommand != "" {
		resp, err := sendServiceCommandFunc(cfg, cfg.ServiceCommand)
		action := cfg.ServiceCommand
		if i := strings.Index(action, ":"); i >= 0 {
			action = action[:i]
		}
		action = strings.ToLower(strings.TrimSpace(action))
		if action == "run" {
			return printRun(resp, err)
		}
		if err != nil {
			return err
		}

		if len(resp.Items) > 0 {
			for _, item := range resp.Items {
				fmt.Println(item)
			}
			if action == "streamoperationstatus" && resp.Message != "" {
				fmt.Println(resp.Message)
			}
			return nil
		}
		if resp.OperationID != "" {
			fmt.Printf("operationId: %s\n", resp.OperationID)
		}
		if resp.RequestedBy != "" {
			fmt.Printf("requestedBy: %s\n", resp.RequestedBy)
		}
		if resp.Message != "" {
			fmt.Println(resp.Message)
			return nil
		}
		switch action {
		case "listoptionalinstalls", "listuserplacements":
			fmt.Println("none")
		case "installitem":
			fmt.Println("InstallItem command completed successfully")
		case "removeitem":
			fmt.Println("RemoveItem command completed successfully")
		case "streamoperationstatus":
			fmt.Println("StreamOperationStatus command completed successfully")
		default:
			fmt.Println("Service command completed successfully")
		}
		return nil
	}

	if cfg.ServiceMode {
		return runServiceFunc(cfg)
	}

	rep, err := managedRunFunc(cfg, nil, nil)
	// A check-only run prints the inventory JSON to stdout; keep it parseable.
	if !cfg.CheckOnly {
		printRunSummary(rep)
	}
	return err
}

// printRun prints `-S run`'s failed and deferred items, even when the run
// failed, and returns the run's error.
func printRun(resp service.CommandResponse, err error) error {
	for _, item := range resp.Items {
		fmt.Println(item)
	}
	if err == nil {
		fmt.Println(resp.Message)
	}
	return err
}

// manageService runs one service management command. Its steps and outcome
// go to stderr and, on Windows, to the Application log under the brand's
// source (sofcatlog.StartManagementLog), the one record an uninstall leaves.
func manageService(cfg config.Configuration, action, done string) error {
	defer startManagementLogFunc(identity.Slug, cfg.Debug, os.Stderr)()
	cmd := "-service" + action
	slog.Info(cmd+" started", "event", sofcatlog.EventCommand)
	if action == "install" {
		// Before the install, so a reinstall over an existing service still
		// fixes the tree. The service applies it again at every start.
		protectStep("data directory", cfg.AppDataPath, protectAppDataFunc)
		protectStep("config file", cfg.ConfigPath, protectConfigFunc)
	}
	if err := runServiceActionFunc(cfg, action); err != nil {
		slog.Error(cmd+" failed", "err", err, "event", sofcatlog.EventCommand)
		return err
	}
	slog.Info(cmd+" succeeded", "event", sofcatlog.EventCommand)
	fmt.Printf("Service %s successfully\n", done)
	return nil
}

// protectStep applies protect to path and logs the result; a failure is only
// a warning, as at every service start.
func protectStep(what, path string, protect func(string) error) {
	if err := protect(path); err != nil {
		slog.Warn("unable to protect the "+what, "path", path, "err", err, "event", sofcatlog.EventProtected)
		return
	}
	slog.Info("protected the "+what, "path", path, "event", sofcatlog.EventProtected)
}

// printRunSummary prints what the run left waiting: deferred items with their
// reason, and conditional_items blocks held because facts are missing. The
// run log and inventory.json record the same.
func printRunSummary(rep *report.Report) {
	if rep == nil {
		return
	}
	for _, d := range rep.DeferredItems {
		fmt.Printf("deferred: %s (%s): %s\n", d.Name, d.Action, d.Reason)
	}
	for _, h := range rep.HeldConditions {
		fmt.Printf("held: conditional_items %q: %s (%s); items: %s\n",
			h.Condition, h.Reason, strings.Join(h.Missing, ", "), strings.Join(h.Items, ", "))
	}
}

// runService protects the data directory and the config file, then hands
// over to the service manager. Protecting them at every start fixes a tree
// deployed by hand or by an older version. A failure is only logged: the
// run-time guard on admin-managed items still holds.
func runService(cfg config.Configuration) error {
	if err := sofcatlog.NewLog(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if err := protectAppDataFunc(cfg.AppDataPath); err != nil {
		slog.Warn("unable to protect the data directory", "path", cfg.AppDataPath, "err", err)
	}
	if err := protectConfigFunc(cfg.ConfigPath); err != nil {
		slog.Warn("unable to protect the config file", "path", cfg.ConfigPath, "err", err)
	}
	return service.Run(cfg, managedRunFunc)
}
