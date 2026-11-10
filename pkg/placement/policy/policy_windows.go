//go:build windows

package policy

import (
	"errors"
	"log/slog"

	"golang.org/x/sys/windows/registry"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The policy key MDM or GPO writes. Variables only so the Windows test can
// point the reader at a scratch key under HKCU.
var (
	policyRoot = registry.LOCAL_MACHINE
	policyPath = identity.PolicyKey() + `\WriteRoots`
)

// readPolicyRoots returns the REG_MULTI_SZ values Machine and User under the
// policy key. A missing key or value has no roots; a value of another type is
// dropped with a warning.
func readPolicyRoots() (machine, user []string) {
	key, err := registry.OpenKey(policyRoot, policyPath, registry.QUERY_VALUE)
	if err != nil {
		if !errors.Is(err, registry.ErrNotExist) {
			slog.Warn("write roots policy key unreadable", "path", policyPath, "err", err)
		}
		return nil, nil
	}
	defer func() { _ = key.Close() }()
	value := func(name string) []string {
		v, _, err := key.GetStringsValue(name)
		if err != nil && !errors.Is(err, registry.ErrNotExist) {
			slog.Warn("write roots policy value dropped", "name", name, "err", err)
		}
		return v
	}
	return value("Machine"), value("User")
}
