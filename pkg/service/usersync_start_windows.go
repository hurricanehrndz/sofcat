//go:build windows

package service

import (
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
// WTSQueryUserToken needs SYSTEM (SeTcbPrivilege). An elevated
// administrator's -config run saves the plan too; its sessions are skipped at
// debug level and those users catch up at their next logon.
func StartUserSync() {
	exe, err := os.Executable()
	if err != nil {
		slog.Warn("user sync: unable to find the helper", "err", err)
		return
	}
	// Always next to this executable, never from config.
	helper := filepath.Join(filepath.Dir(exe), identity.Slug+"-ui.exe")
	sessions, err := userSessions()
	if err != nil {
		slog.Warn("user sync: unable to list sessions", "err", err)
		return
	}
	started := map[string]bool{}
	for _, session := range sessions {
		startInSession(session, helper, started)
	}
}

// userSessions lists every session but session 0, the services' own.
func userSessions() ([]uint32, error) {
	var info *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &info, &count); err != nil {
		return nil, err
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(info)))
	var ids []uint32
	for _, s := range unsafe.Slice(info, count) {
		if s.SessionID != 0 {
			ids = append(ids, s.SessionID)
		}
	}
	return ids, nil
}

// startInSession starts the helper in session unless its user, by SID, is in
// started already: a user signed in twice gets one helper.
func startInSession(session uint32, helper string, started map[string]bool) {
	var token windows.Token
	if err := windows.WTSQueryUserToken(session, &token); err != nil {
		// ERROR_NO_TOKEN: nobody is signed in to it. ERROR_PRIVILEGE_NOT_HELD:
		// not SYSTEM.
		slog.Debug("user sync: no user token for session", "session", session, "err", err)
		return
	}
	defer func() { _ = token.Close() }()
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		slog.Debug("user sync: unable to read the session's user", "session", session, "err", err)
		return
	}
	sid := tokenUser.User.Sid.String()
	if started[sid] {
		return
	}
	started[sid] = true

	var env *uint16
	if err = windows.CreateEnvironmentBlock(&env, token, false); err != nil {
		slog.Warn("user sync: unable to build the user's environment", "session", session, "sid", sid, "err", err)
		return
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
		return
	}
	_ = windows.CloseHandle(pi.Thread)
	_ = windows.CloseHandle(pi.Process)
	slog.Info("user sync: started the helper", "session", session, "sid", sid, "pid", pi.ProcessId)
}
