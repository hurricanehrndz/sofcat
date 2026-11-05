//go:build windows

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// ${ProgramData}\<Name> holds config.yaml (which may carry auth_user,
// auth_pass and TLS client keys), the Data directory (the self-serve
// manifest, the cache of installers that run as SYSTEM, the log) and
// sofcat's own placements, such as a marketplace. ProgramData grants Users
// read and create rights, so each gets a protected DACL of its own. The
// executables live under Program Files, whose default ACL already lets Users
// read and execute them. See site/src/content/docs/reference/data-directory.md.
const (
	// parentSDDL is ${ProgramData}\<Name>'s: only administrators create
	// entries there (no user-planted config.yaml), and Users read what sofcat
	// places, as the harnesses need.
	parentSDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)"
	// appDataSDDL is Data's: SYSTEM and Administrators only, inherited by
	// everything below.
	appDataSDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	// userArtifactsSDDL adds Users read and execute, inherited by the staged
	// archives, on top of what user-artifacts inherits from Data: the
	// per-user helper reads them.
	userArtifactsSDDL = "O:BAD:(A;OICI;0x1200a9;;;BU)"
	// configSDDL is config.yaml's, which sits beside Data rather than in it.
	configSDDL = "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)"
	// inheritSDDL has no explicit ACEs: the entry keeps only what it inherits.
	inheritSDDL = "O:BAD:"
	// userArtifactsDir is the staging directory's name (service.UserArtifactsDir).
	userArtifactsDir = "user-artifacts"
)

// protectAppData applies the data directory DACL to root and resets every
// entry below it to inherit from root, like `icacls /reset /t`. Owners become
// Administrators, so a file a user created before the fix loses the implicit
// WRITE_DAC its owner keeps. inventory.json keeps its stricter DACL, and
// user-artifacts then gets Users read back. When root is <Name>\Data, its
// parent gets parentSDDL too, on that directory only. Symbolic links and
// junctions are skipped, never followed. It is idempotent and keeps going
// past entries it cannot fix, returning every error.
func protectAppData(root string) error {
	// CEILING: only a directory named Data inside one named after this
	// build (identity.Name), or the bare <Name> an older config still points
	// app_data_path at, is protected, so a mistyped app_data_path such as
	// C:\ProgramData cannot have its ACL replaced. Upgrade: an explicit
	// config opt-in for other names if anyone needs one.
	parent := ""
	switch {
	case strings.EqualFold(filepath.Base(root), identity.Name):
	case strings.EqualFold(filepath.Base(root), "Data") && strings.EqualFold(filepath.Base(filepath.Dir(root)), identity.Name):
		parent = filepath.Dir(root)
	default:
		return fmt.Errorf("not protecting %s: the directory is not %s or %s\\Data", root, identity.Name, identity.Name)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	// A junction here would point the ACL at somebody else's directory.
	if err := plainDir(root); err != nil {
		return err
	}

	// SYSTEM and elevated administrators hold the privilege but it is off by
	// default; without it, a file a user locked to themselves cannot be fixed.
	restore := enablePrivilege("SeTakeOwnershipPrivilege")
	defer restore()

	if parent != "" {
		if err := plainDir(parent); err != nil {
			return err
		}
		if err := setSecurity(parent, parentSDDL, windows.PROTECTED_DACL_SECURITY_INFORMATION); err != nil {
			return err
		}
	}
	if err := setSecurity(root, appDataSDDL, windows.PROTECTED_DACL_SECURITY_INFORMATION); err != nil {
		return err
	}
	var errs []error
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		switch {
		case path == root:
		case !d.IsDir() && !d.Type().IsRegular():
			errs = append(errs, fmt.Errorf("skipped %s: not a plain file or directory", path))
		case filepath.Dir(path) == root && strings.EqualFold(d.Name(), inventoryFile):
		default:
			errs = append(errs, setSecurity(path, inheritSDDL, windows.UNPROTECTED_DACL_SECURITY_INFORMATION))
		}
		return nil
	})
	errs = append(errs, walkErr)

	// The staging directory is created here, not only by the first run that
	// stages an archive, so the archives are readable from the moment they
	// land rather than after the next service start.
	artifacts := filepath.Join(root, userArtifactsDir)
	if err := os.Mkdir(artifacts, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		errs = append(errs, err)
	} else if err := plainDir(artifacts); err != nil {
		errs = append(errs, err)
	} else {
		errs = append(errs, setSecurity(artifacts, userArtifactsSDDL, windows.UNPROTECTED_DACL_SECURITY_INFORMATION))
	}
	return errors.Join(errs...)
}

// plainDir refuses anything at path but a directory: a junction or symbolic
// link would point the ACL at somebody else's directory.
func plainDir(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("not protecting %s: not a plain directory", path)
	}
	return nil
}

// setSecurity sets the owner, then the DACL, from sddl. The owner goes first:
// with the take-ownership privilege that works on any file, and the new owner
// (Administrators, which SYSTEM's token also holds) may then write the DACL.
func setSecurity(path, sddl string, protection windows.SECURITY_INFORMATION) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, owner, nil, nil, nil); err != nil {
		return &os.PathError{Op: "set owner", Path: path, Err: err}
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|protection, nil, nil, dacl, nil); err != nil {
		return &os.PathError{Op: "set DACL", Path: path, Err: err}
	}
	return nil
}

// enablePrivilege enables name on the process token, best effort, and returns
// a function that restores the previous state.
func enablePrivilege(name string) func() {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return func() {}
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0].Attributes = windows.SE_PRIVILEGE_ENABLED
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &tp.Privileges[0].Luid); err != nil {
		_ = token.Close()
		return func() {}
	}
	var prev windows.Tokenprivileges
	var n uint32
	if err := windows.AdjustTokenPrivileges(token, false, &tp, uint32(unsafe.Sizeof(prev)), &prev, &n); err != nil {
		_ = token.Close()
		return func() {}
	}
	return func() {
		if prev.PrivilegeCount > 0 {
			_ = windows.AdjustTokenPrivileges(token, false, &prev, 0, nil, nil)
		}
		_ = token.Close()
	}
}

// protectConfig gives config.yaml, which sits beside Data rather than in it
// and can hold auth_pass and TLS key paths, owner Administrators and
// configSDDL. A missing config is left alone (the installer ships none), and
// anything but a regular file is refused, so a symbolic link or other reparse
// point cannot redirect the ACL. It is the Windows twin of protectConfigFile.
func protectConfig(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("not protecting %s: not a regular file", path)
	}
	restore := enablePrivilege("SeTakeOwnershipPrivilege")
	defer restore()
	return setSecurity(path, configSDDL, windows.PROTECTED_DACL_SECURITY_INFORMATION)
}
