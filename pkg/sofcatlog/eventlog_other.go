//go:build !windows

package sofcatlog

// openEventSink has no event log to open off Windows; the management
// commands log to stderr only. macOS's unified log is a later item.
func openEventSink(string) (EventSink, func(), error) {
	return nil, func() {}, nil
}
