//go:build windows

package service

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"golang.org/x/sys/windows"
)

// StartUserSync starts the per-user helper, <slug>-ui.exe --user-sync, once
// for each user signed in to the machine, so a new user-placement plan
// reaches their profiles without waiting for a logon. Locked, disconnected
// and switched-away sessions count: their profiles are loaded. Each helper
// runs with the session's own token (UAC-filtered for an administrator, so
// unelevated) and that user's environment, and the service does not wait for
// it; the helper reports its results through reportUserPlacements.
//
// It starts nothing unless the stored plan differs from the one the helpers
// were last started for (userSyncDue), and records the plan only when no
// session failed. A missing helper, a failed start or a run without SYSTEM
// rights leaves it unrecorded, so the next run tries again.
//
// WTSQueryUserToken needs SYSTEM (SeTcbPrivilege). An elevated
// administrator's -config run saves the plan too; its sessions are skipped at
// debug level and the next service run catches those users up.
func StartUserSync(appDataPath string) {
	hash, due := userSyncDue(appDataPath)
	if !due {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		slog.Warn("user sync: unable to find the helper", "err", err)
		return
	}
	// Always next to this executable, never from config.
	helper := filepath.Join(filepath.Dir(exe), identity.Slug+"-ui.exe")
	if _, err = os.Stat(helper); err != nil {
		slog.Debug("user sync: no helper to start", "helper", helper, "err", err)
		return
	}
	sessions, err := userSessions()
	if err != nil {
		slog.Warn("user sync: unable to list sessions", "err", err)
		return
	}
	started := map[string]bool{}
	failed := false
	for _, session := range sessions {
		if !startInSession(session, helper, started) {
			failed = true
		}
	}
	if failed {
		return
	}
	if err := markUserSyncStarted(appDataPath, hash); err != nil {
		slog.Warn("user sync: unable to record the plan the helpers were started for", "err", err)
	}
}

// userSessions lists every session but session 0, the services' own, and
// listener sessions, which no user signs in to.
func userSessions() ([]uint32, error) {
	var info *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &info, &count); err != nil {
		return nil, err
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(info)))
	var ids []uint32
	for _, s := range unsafe.Slice(info, count) {
		if s.SessionID != 0 && s.State != windows.WTSListen {
			ids = append(ids, s.SessionID)
		}
	}
	return ids, nil
}

// startInSession starts the helper in session unless its user, by SID, is in
// started already: a user signed in twice gets one helper. A SID counts only
// once its helper started, so a failure in one session is tried again in the
// user's next. It reports false on a failure the next run should retry; a
// session nobody is signed in to is not one.
func startInSession(session uint32, helper string, started map[string]bool) bool {
	var token windows.Token
	if err := windows.WTSQueryUserToken(session, &token); err != nil {
		// ERROR_NO_TOKEN: nobody is signed in to it. ERROR_PRIVILEGE_NOT_HELD:
		// not SYSTEM, a failure so that the next service run catches up.
		slog.Debug("user sync: no user token for session", "session", session, "err", err)
		return errors.Is(err, windows.ERROR_NO_TOKEN)
	}
	defer func() { _ = token.Close() }()
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		slog.Debug("user sync: unable to read the session's user", "session", session, "err", err)
		return false
	}
	sid := tokenUser.User.Sid.String()
	if started[sid] {
		return true
	}

	var env *uint16
	if err = windows.CreateEnvironmentBlock(&env, token, false); err != nil {
		slog.Warn("user sync: unable to build the user's environment", "session", session, "sid", sid, "err", err)
		return false
	}
	defer func() { _ = windows.DestroyEnvironmentBlock(env) }()

	// The service was answering a moment ago, so the helper does not retry
	// the pipe: one left waiting would hold its executable open through an
	// uninstall.
	cmdLine := windows.ComposeCommandLine([]string{helper, "--user-sync", "--no-retry"})
	si := &windows.StartupInfo{Desktop: windows.StringToUTF16Ptr(`winsta0\default`)}
	si.Cb = uint32(unsafe.Sizeof(*si))
	var pi windows.ProcessInformation
	err = windows.CreateProcessAsUser(token, windows.StringToUTF16Ptr(helper), windows.StringToUTF16Ptr(cmdLine),
		nil, nil, false, windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW, env, nil, si, &pi)
	if err != nil {
		slog.Warn("user sync: unable to start the helper", "session", session, "sid", sid, "helper", helper, "err", err)
		return false
	}
	started[sid] = true
	_ = windows.CloseHandle(pi.Thread)
	_ = windows.CloseHandle(pi.Process)
	slog.Info("user sync: started the helper", "session", session, "sid", sid, "pid", pi.ProcessId)
	return true
}
