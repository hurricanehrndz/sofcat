//go:build windows

package config

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The policy key MDM or GPO writes. Variables only so the Windows test can
// point the reader at a scratch key under HKCU.
var (
	managedRoot = registry.LOCAL_MACHINE
	managedPath = identity.PolicyKey()
)

// readManaged returns the values under the policy key, plus its branding
// subkey's as "branding". A missing key has none.
func readManaged(warn func(string)) map[string]any {
	key, err := registry.OpenKey(managedRoot, managedPath, registry.QUERY_VALUE)
	if err != nil {
		if !errors.Is(err, registry.ErrNotExist) {
			warn(fmt.Sprintf("managed settings key %s unreadable: %v", managedPath, err))
		}
		return nil
	}
	defer func() { _ = key.Close() }()
	out := readValues(key, warn)
	if sub, err := registry.OpenKey(key, "branding", registry.QUERY_VALUE); err == nil {
		out["branding"] = readValues(sub, warn)
		_ = sub.Close()
	}
	return out
}

// readValues returns a key's string, multi-string and integer values by
// lower-cased name: registry names are case-insensitive.
func readValues(key registry.Key, warn func(string)) map[string]any {
	out := map[string]any{}
	names, err := key.ReadValueNames(0)
	if err != nil {
		warn(fmt.Sprintf("managed settings unreadable: %v", err))
		return out
	}
	for _, name := range names {
		_, typ, err := key.GetValue(name, nil)
		var v any
		switch {
		case err != nil:
		case typ == registry.SZ:
			v, _, err = key.GetStringValue(name)
		case typ == registry.EXPAND_SZ:
			var s string
			if s, _, err = key.GetStringValue(name); err == nil {
				v, err = registry.ExpandString(s)
			}
		case typ == registry.MULTI_SZ:
			v, _, err = key.GetStringsValue(name)
		case typ == registry.DWORD || typ == registry.QWORD:
			v, _, err = key.GetIntegerValue(name)
		default:
			err = registry.ErrUnexpectedType
		}
		if err != nil {
			warn(fmt.Sprintf("managed setting %s dropped: %v", name, err))
			continue
		}
		out[strings.ToLower(name)] = v
	}
	return out
}
