//go:build windows

package service

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"golang.org/x/sys/windows"
)

// agentExes are the file names, beside <slug>.exe, of the processes
// -serviceremove ends: the per-user helper and the storefront, <slug>-ui.exe
// (the name `just build` gives it). Ending the storefront closes a visible
// window, even during a silent uninstall; left open, it keeps its executable
// locked and the uninstaller cannot delete it.
func agentExes() []string {
	return []string{userAgentExe(), identity.Slug + "-ui.exe"}
}

// endAgents ends every running process whose image is one of agentExes
// beside this executable, so the uninstaller that runs next can delete them.
// A logon agent waiting for a service that is gone retries for a minute, and
// an open storefront stays open; either would keep its executable in place.
// The same file name in another directory is left alone. Nothing here fails the remove: each failure is
// logged and the sweep goes on.
//
// The agent runs as the signed-in user, whose process DACL does not grant
// Administrators, so the elevated remove enables SeDebugPrivilege first.
func endAgents() {
	exe, err := os.Executable()
	if err != nil {
		slog.Warn("unable to find the running agents to end", "err", err)
		return
	}
	dir := filepath.Dir(filepath.Clean(exe))
	names := agentExes()
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = filepath.Join(dir, name)
	}

	if err = enableDebugPrivilege(); err != nil {
		slog.Warn("unable to enable SeDebugPrivilege; agents of other users may be left running", "err", err)
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		slog.Warn("unable to list processes; running agents left alone", "err", err)
		return
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	pe := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		// The snapshot's name is the base name only; the image path decides.
		name := windows.UTF16ToString(pe.ExeFile[:])
		for _, n := range names {
			if strings.EqualFold(name, n) {
				endAgent(pe.ProcessID, paths)
				break
			}
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		slog.Warn("unable to list every process; some agents may be left running", "err", err)
	}
}

// endAgent ends process pid if its full image path is one of paths, and
// waits briefly for it to exit, so its executable is free to delete.
func endAgent(pid uint32, paths []string) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		slog.Warn("unable to open a running agent", "pid", pid, "err", err)
		return
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var buf [windows.MAX_LONG_PATH]uint16
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		slog.Warn("unable to read a running agent's image path", "pid", pid, "err", err)
		return
	}
	image := windows.UTF16ToString(buf[:n])
	if !isAgentImage(image, paths) {
		slog.Debug("leaving a process of the same name from another directory", "pid", pid, "image", image)
		return
	}
	if err := windows.TerminateProcess(h, 1); err != nil {
		slog.Warn("unable to end a running agent", "pid", pid, "image", image, "err", err)
		return
	}
	if ev, err := windows.WaitForSingleObject(h, 5000); err != nil || ev != windows.WAIT_OBJECT_0 {
		slog.Warn("a running agent has not exited yet", "pid", pid, "image", image, "err", err)
		return
	}
	slog.Info("ended a running agent", "pid", pid, "image", image)
}

// enableDebugPrivilege enables SeDebugPrivilege on this process's token for
// the rest of its life; -serviceremove exits right after. A token that does
// not hold the privilege, one that is not elevated, still returns nil here
// (AdjustTokenPrivileges reports that only through the last error); the
// OpenProcess that then fails logs it.
func enableDebugPrivilege() error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer func() { _ = token.Close() }()
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0].Attributes = windows.SE_PRIVILEGE_ENABLED
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeDebugPrivilege"), &tp.Privileges[0].Luid); err != nil {
		return err
	}
	return windows.AdjustTokenPrivileges(token, false, &tp, 0, nil, nil)
}
