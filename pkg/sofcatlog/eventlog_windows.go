//go:build windows

package sofcatlog

import (
	"errors"

	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/eventlog"
)

// openEventSink registers source in the Application log if it is missing,
// then opens it.
func openEventSink(source string) (EventSink, func(), error) {
	if err := registerSource(source); err != nil {
		return nil, func() {}, err
	}
	l, err := eventlog.Open(source)
	if err != nil {
		return nil, func() {}, err
	}
	return l, func() { _ = l.Close() }, nil
}

// registerSource registers source as an EventCreate source, whose message
// file prints each entry's text as given. A source that already exists, from
// an earlier install or an older version, counts as registered and is left
// as it is, unless it has no EventMessageFile: a key a half-finished
// registration left behind, which is removed and registered again. Nothing
// else removes it, not even -serviceremove, so the uninstall's own entries
// stay readable afterwards.
func registerSource(source string) error {
	const types = eventlog.Error | eventlog.Warning | eventlog.Info
	err := eventlog.InstallAsEventCreate(source, types)
	if err == nil {
		return nil
	}
	k, kerr := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\EventLog\Application\`+source, registry.QUERY_VALUE)
	if kerr != nil {
		return err
	}
	_, _, verr := k.GetStringValue("EventMessageFile")
	_ = k.Close()
	if !errors.Is(verr, registry.ErrNotExist) {
		return nil
	}
	if rerr := eventlog.Remove(source); rerr != nil {
		return rerr
	}
	return eventlog.InstallAsEventCreate(source, types)
}
