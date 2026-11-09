package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// LaunchdPlist is the macOS daemon's property list: launchd starts cli with
// the service arguments at boot and restarts it whenever it exits, as root,
// logging stdout and stderr to launchd.log in dataDir. `-serviceinstall`
// writes it and `just pkg` ships the same bytes (`<cli> -print-launchd-plist`),
// so there is one definition.
func LaunchdPlist(cli, configPath, dataDir string) []byte {
	logPath := path.Join(dataDir, "launchd.log")
	b := plistStart(identity.LaunchdLabel(), append([]string{cli}, serviceInstallArgs(configPath)...))
	b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	fmt.Fprintf(b, "\t<key>StandardOutPath</key>\n\t%s\n", plistString(logPath))
	fmt.Fprintf(b, "\t<key>StandardErrorPath</key>\n\t%s\n", plistString(logPath))
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

// UserAgentPlist is the per-user helper's LaunchAgent: launchd runs agent
// once in each user's GUI (Aqua) session at logon, as that user, and the
// service starts it again on a plan change (StartUserSync). Like
// LaunchdPlist, -serviceinstall writes it and `just pkg` ships the same
// bytes (`<cli> -print-useragent-plist`). The helper logs to its own file.
//
// The arguments are fixed, so a helper the service starts runs without
// -no-retry. That is harmless on macOS: an open file does not block deleting
// it, usersync.PlanRetryFor bounds the wait, and -serviceremove boots the
// agents out.
func UserAgentPlist(agent string) []byte {
	b := plistStart(identity.UserAgentLabel(), []string{agent})
	b.WriteString("\t<key>LimitLoadToSessionType</key>\n\t<string>Aqua</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

// plistStart opens a launchd plist with its label, its program arguments and
// RunAtLoad; the caller adds its keys and closes it.
func plistStart(label string, args []string) *bytes.Buffer {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t%s\n", plistString(label))
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range args {
		fmt.Fprintf(&b, "\t\t%s\n", plistString(arg))
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	return &b
}

func plistString(s string) string {
	var e bytes.Buffer
	_ = xml.EscapeText(&e, []byte(s))
	return "<string>" + e.String() + "</string>"
}

// InstalledLaunchdPlist is the plist for the installed layout: the CLI in
// identity.CLIPath and the default macOS config and data paths.
func InstalledLaunchdPlist() []byte {
	return LaunchdPlist(identity.CLIPath(), identity.ConfigPathFor("darwin"), identity.DataDirFor("darwin"))
}

// InstalledUserAgentPlist is the LaunchAgent plist for the installed layout:
// the helper in identity.UserAgentPath.
func InstalledUserAgentPlist() []byte {
	return UserAgentPlist(identity.UserAgentPath())
}

// launchdStatus summarises `launchctl print system/<label>` in the shape of
// the Windows status: the daemon's state, pid, program and last exit code.
func launchdStatus(label, plist string, printed []byte) string {
	fields := launchdFields(printed)
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\n", label)
	fmt.Fprintf(&b, "plist: %s\n", plist)
	for _, key := range []string{"state", "pid", "program", "last exit code"} {
		if value, ok := fields[key]; ok {
			fmt.Fprintf(&b, "%s: %s\n", strings.ReplaceAll(key, " ", "_"), value)
		}
	}
	return strings.TrimSpace(b.String())
}

// launchdFields reads the top-level `key = value` lines of `launchctl print`
// output, such as state and pid.
func launchdFields(printed []byte) map[string]string {
	fields := map[string]string{}
	for line := range strings.SplitSeq(string(printed), "\n") {
		// Top-level keys are indented one tab; nested blocks more.
		if !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if ok {
			fields[key] = value
		}
	}
	return fields
}

// launchctl runs /bin/launchctl and returns its combined output; tests
// replace it. The launchd actions below are portable so their tests run on
// any host; manage_darwin.go binds them to the installed label and plist.
var launchctl = func(args ...string) ([]byte, error) {
	return exec.Command("/bin/launchctl", args...).CombinedOutput()
}

// launchdAction manages the launchd daemon, as the SCM actions do on
// Windows: install writes the plist and bootstraps it (RunAtLoad starts it),
// start restarts it (bootstrapping it again after a stop), stop boots it out
// and keeps the plist, and remove boots it out and deletes the plist. plist is
// where the daemon's plist lives (identity.LaunchdPlistPath()). Like the
// Windows Run value, the per-user helper's LaunchAgent plist, agentPlist
// (identity.UserAgentPlistPath()), is written by install and removed, with
// every running helper, by remove (removeUserAgents), which also ends a
// running storefront (quitStorefront).
func launchdAction(cfg config.Configuration, action, plist, agentPlist string) error {
	label := identity.LaunchdLabel()
	switch action {
	case "install":
		return installLaunchd(cfg, label, plist, agentPlist)
	case "start":
		if !launchdLoaded(label) {
			return launchctlRun("bootstrap", "system", plist)
		}
		return launchctlRun("kickstart", "-k", "system/"+label)
	case "stop":
		return bootout(label)
	case "remove":
		if err := bootout(label); err != nil {
			return err
		}
		if err := os.Remove(plist); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", plist, err)
		}
		// After the daemon, so it cannot start a helper behind the sweep.
		err := removeUserAgents(identity.UserAgentLabel(), agentPlist)
		quitStorefront(identity.AppPath())
		return err
	default:
		return fmt.Errorf("unsupported service action %q", action)
	}
}

// launchdServiceStatus summarises `launchctl print system/<label>` for the
// installed daemon.
func launchdServiceStatus() (string, error) {
	label := identity.LaunchdLabel()
	out, err := launchctl("print", "system/"+label)
	if err != nil {
		return "", fmt.Errorf("launchctl print system/%s: %w: %s", label, err, strings.TrimSpace(string(out)))
	}
	return launchdStatus(label, identity.LaunchdPlistPath(), out), nil
}

func installLaunchd(cfg config.Configuration, label, plist, agentPlist string) error {
	if cfg.ConfigPath == "" {
		return errors.New("config path is required to install service")
	}
	if launchdLoaded(label) {
		return fmt.Errorf("service %q already exists", label)
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
	// The helper sits beside this executable, as the service expects. Its
	// plist goes first: nothing loads it until the next logon or plan change.
	agent := filepath.Join(filepath.Dir(exePath), identity.Slug+"-useragent")
	if err := writePlist(agentPlist, UserAgentPlist(agent)); err != nil {
		return err
	}
	if err := writePlist(plist, LaunchdPlist(exePath, configPath, cfg.AppDataPath)); err != nil {
		return err
	}
	return launchctlRun("bootstrap", "system", plist)
}

// writePlist writes a launchd plist. launchd refuses one that is not
// root-owned or is group- or world-writable: root writes it, 0644 whatever
// the umask.
func writePlist(plist string, data []byte) error {
	if err := os.WriteFile(plist, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", plist, err)
	}
	if err := os.Chmod(plist, 0o644); err != nil {
		return fmt.Errorf("set mode of %s: %w", plist, err)
	}
	return nil
}

// bootout unloads the daemon; one that is not loaded is already stopped.
func bootout(label string) error {
	if !launchdLoaded(label) {
		return nil
	}
	return launchctlRun("bootout", "system/"+label)
}

func launchdLoaded(label string) bool {
	_, err := launchctl("print", "system/"+label)
	return err == nil
}

func launchctlRun(args ...string) error {
	out, err := launchctl(args...)
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
