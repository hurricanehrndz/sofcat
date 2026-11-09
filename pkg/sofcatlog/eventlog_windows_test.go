//go:build windows

package sofcatlog

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/eventlog"
)

// A management command's entries must land in the Application log under the
// brand's source, readable as text, and registering a source that already
// exists, as a reinstall does, must succeed. Registering needs an elevated
// token, which GitHub's windows-latest runner has.
func TestEventLogRoundTrip(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("registering an event source needs an elevated token")
	}
	source := fmt.Sprintf("sofcat-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	sink, closeSink, err := openEventSink(source)
	if err != nil {
		t.Fatalf("openEventSink: %v", err)
	}
	t.Cleanup(func() {
		closeSink()
		_ = eventlog.Remove(source)
	})
	if err = registerSource(source); err != nil {
		t.Fatalf("registering an existing source: %v", err)
	}

	msg := "ended a running agent pid=42 token=" + source
	if err = sink.Warning(uint32(EventAgentEnded), msg); err != nil {
		t.Fatalf("Warning: %v", err)
	}
	query := fmt.Sprintf("*[System[Provider[@Name='%s']]]", source)
	out, err := exec.Command("wevtutil", "qe", "Application", "/q:"+query, "/c:1", "/rd:true", "/f:text").CombinedOutput()
	if err != nil {
		t.Fatalf("wevtutil: %v\n%s", err, out)
	}
	text := string(out)
	for _, s := range []string{msg, fmt.Sprintf("Event ID: %d", EventAgentEnded), "Level: Warning"} {
		if !strings.Contains(text, s) {
			t.Errorf("event lacks %q:\n%s", s, text)
		}
	}
}

// A source key without EventMessageFile, left by a registration that stopped
// part way, is repaired rather than counted as registered: otherwise every
// entry would read "description not found" forever.
func TestRegisterSourceRepairsHalfRegistered(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("registering an event source needs an elevated token")
	}
	source := fmt.Sprintf("sofcat-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	path := `SYSTEM\CurrentControlSet\Services\EventLog\Application\` + source
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	_ = k.Close()
	t.Cleanup(func() { _ = eventlog.Remove(source) })

	if err = registerSource(source); err != nil {
		t.Fatalf("registerSource: %v", err)
	}
	k, err = registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("OpenKey: %v", err)
	}
	defer func() { _ = k.Close() }()
	if v, _, verr := k.GetStringValue("EventMessageFile"); verr != nil || !strings.Contains(v, "EventCreate.exe") {
		t.Errorf("EventMessageFile = %q, %v; want the EventCreate message file", v, verr)
	}
	if v, _, verr := k.GetIntegerValue("TypesSupported"); verr != nil || v != uint64(eventlog.Error|eventlog.Warning|eventlog.Info) {
		t.Errorf("TypesSupported = %d, %v", v, verr)
	}
}
