package service

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// guiUsers lists the uids signed in to a macOS GUI session; tests replace it.
var guiUsers = func() ([]int, error) {
	out, err := exec.Command("/bin/ps", "-axo", "uid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return loginwindowUIDs(out), nil
}

// loginwindowUIDs reads `ps -axo uid=,comm=`: every GUI session, the
// console's and each fast-user-switched one, has its own loginwindow running
// as its user, so their owners are the signed-in users. The sign-in screen's
// runs as root, and uids below 500 are system accounts (_mbsetupuser in
// Setup Assistant), so both are left out.
func loginwindowUIDs(ps []byte) []int {
	var uids []int
	for line := range strings.SplitSeq(string(ps), "\n") {
		field, comm, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || path.Base(strings.TrimSpace(comm)) != "loginwindow" {
			continue
		}
		uid, err := strconv.Atoi(field)
		if err == nil && uid >= 500 && !slices.Contains(uids, uid) {
			uids = append(uids, uid)
		}
	}
	return uids
}

// startUserSyncLaunchd is StartUserSync on macOS, with the helper and its
// LaunchAgent plist as arguments for the tests. Like Windows, it starts
// nothing unless the stored plan changed (userSyncDue), and records the plan
// only when every signed-in user's helper started, so a missing helper or
// plist, a run without root or one user's failure is retried at the next run.
func startUserSyncLaunchd(appDataPath, helper, label, plist string) {
	hash, due := userSyncDue(appDataPath)
	if !due {
		return
	}
	for _, f := range []string{helper, plist} {
		if _, err := os.Stat(f); err != nil {
			slog.Debug("user sync: no helper to start", "path", f, "err", err)
			return
		}
	}
	uids, err := guiUsers()
	if err != nil {
		slog.Warn("user sync: unable to list signed-in users", "err", err)
		return
	}
	if !startUserAgents(uids, label, plist) {
		return
	}
	if err := markUserSyncStarted(appDataPath, hash); err != nil {
		slog.Warn("user sync: unable to record the plan the helpers were started for", "err", err)
	}
}

// startUserAgents starts the helper's LaunchAgent in each uid's GUI domain:
// a kickstart where launchd loaded it at logon, a bootstrap, which runs it
// through RunAtLoad, where it is not loaded (a user signed in before the
// install). It reports false when any user's start failed.
//
// launchd runs one instance per job, and a kickstart does nothing to a
// helper that is still running, which may already have fetched its plan for
// the last time. Killing it (kickstart -k) could stop it mid-placement, so a
// running helper counts as a failed start instead: the plan stays unrecorded
// and the next service run starts it again. Windows has no such gap; there a
// second helper waits on the per-user lock and fetches the plan afresh.
func startUserAgents(uids []int, label, plist string) bool {
	ok := true
	for _, uid := range uids {
		domain := "gui/" + strconv.Itoa(uid)
		args := []string{"bootstrap", domain, plist}
		if out, err := launchctl("print", domain+"/"+label); err == nil {
			if launchdFields(out)["state"] == "running" {
				slog.Info("user sync: the helper is still running; starting it again next run", "uid", uid)
				ok = false
				continue
			}
			args = []string{"kickstart", domain + "/" + label}
		}
		if err := launchctlRun(args...); err != nil {
			slog.Warn("user sync: unable to start the helper", "uid", uid, "via", args[0], "err", err)
			ok = false
			continue
		}
		slog.Info("user sync: started the helper", "uid", uid, "via", args[0])
	}
	return ok
}

// removeUserAgents deletes the helper's LaunchAgent plist, so no logon
// starts it again, and then boots it out of every signed-in user's GUI
// domain, which ends a running helper. As on Windows (endAgents), a failure
// to list the users or boot one out is logged and the remove goes on; only
// a plist left behind fails it.
func removeUserAgents(label, plist string) error {
	var rmErr error
	if err := os.Remove(plist); err != nil && !errors.Is(err, os.ErrNotExist) {
		rmErr = fmt.Errorf("remove %s: %w", plist, err)
	}
	uids, err := guiUsers()
	if err != nil {
		slog.Warn("unable to list signed-in users; running helpers left alone", "err", err)
	}
	for _, uid := range uids {
		target := "gui/" + strconv.Itoa(uid) + "/" + label
		if _, err := launchctl("print", target); err != nil {
			continue
		}
		if err := launchctlRun("bootout", target); err != nil {
			slog.Warn("unable to boot the helper out", "uid", uid, "err", err)
			continue
		}
		slog.Info("booted the helper out", "uid", uid)
	}
	return rmErr
}

// storefrontPIDs lists the processes running from the storefront's app
// bundle; tests replace it.
var storefrontPIDs = func(app string) ([]int, error) {
	out, err := exec.Command("/bin/ps", "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return bundlePIDs(out, app), nil
}

// bundlePIDs reads `ps -axo pid=,comm=`, where comm is the executable's
// full path, and returns the processes whose executable is in app's
// Contents/MacOS. The same executable name anywhere else is left alone.
func bundlePIDs(ps []byte, app string) []int {
	var pids []int
	for line := range strings.SplitSeq(string(ps), "\n") {
		field, comm, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !strings.HasPrefix(strings.TrimSpace(comm), app+"/Contents/MacOS/") {
			continue
		}
		if pid, err := strconv.Atoi(field); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// terminate asks process pid to exit; tests replace it.
var terminate = func(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

// quitStorefront ends every running storefront, the app bundle at app, in
// any user's session: with the service gone it can do nothing. As on
// Windows (endAgents) the window closes without asking, and nothing here
// fails the remove: each failure is logged and the sweep goes on.
func quitStorefront(app string) {
	pids, err := storefrontPIDs(app)
	if err != nil {
		slog.Warn("unable to list processes; a running storefront is left open", "err", err)
		return
	}
	for _, pid := range pids {
		if err := terminate(pid); err != nil {
			slog.Warn("unable to end the storefront", "pid", pid, "err", err)
			continue
		}
		slog.Info("ended the storefront", "pid", pid)
	}
}
