package sofcatlog

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

type sinkEntry struct {
	kind string
	id   EventID
	msg  string
}

type fakeSink struct{ entries []sinkEntry }

func (s *fakeSink) Info(id uint32, msg string) error {
	s.entries = append(s.entries, sinkEntry{"info", EventID(id), msg})
	return nil
}

func (s *fakeSink) Warning(id uint32, msg string) error {
	s.entries = append(s.entries, sinkEntry{"warning", EventID(id), msg})
	return nil
}

func (s *fakeSink) Error(id uint32, msg string) error {
	s.entries = append(s.entries, sinkEntry{"error", EventID(id), msg})
	return fmt.Errorf("event log full")
}

// An uninstall's steps must reach the event log with the right entry type and
// ID, so an admin filtering Event Viewer finds the warnings and errors, and
// must still print on stderr, which is all macOS and the installer's failure
// dialog see. A failing event log never stops the stderr line.
func TestTeeHandler(t *testing.T) {
	var stderr bytes.Buffer
	sink := &fakeSink{}
	log := slog.New(newTeeHandler(&stderr, slog.LevelInfo, sink)).With("cmd", "remove")

	log.Debug("not shown")
	log.Info("deleted the service", "service", "sofcat", "event", EventServiceDeleted)
	log.Warn("a running agent has not exited yet", "pid", 42, "event", EventAgentNotExited)
	log.Error("remove failed", "err", "access denied")
	log.Error("after a failing sink")

	want := []sinkEntry{
		{"info", EventServiceDeleted, "deleted the service cmd=remove service=sofcat"},
		{"warning", EventAgentNotExited, "a running agent has not exited yet cmd=remove pid=42"},
		{"error", EventGeneral, `remove failed cmd=remove err="access denied"`},
		{"error", EventGeneral, "after a failing sink cmd=remove"},
	}
	if fmt.Sprint(sink.entries) != fmt.Sprint(want) {
		t.Errorf("event log entries:\n got %v\nwant %v", sink.entries, want)
	}

	out := stderr.String()
	for _, s := range []string{
		`level=INFO msg="deleted the service" cmd=remove service=sofcat`,
		`level=WARN msg="a running agent has not exited yet" cmd=remove pid=42`,
		`level=ERROR msg="remove failed" cmd=remove err="access denied"`,
		`level=ERROR msg="after a failing sink"`,
	} {
		if !strings.Contains(out, s) {
			t.Errorf("stderr lacks %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "not shown") || strings.Contains(out, "event=") {
		t.Errorf("stderr shows a debug record or the event ID:\n%s", out)
	}
}

// With no event log, off Windows or when it cannot be opened, the commands
// still log to stderr, and debug records go there only.
func TestTeeHandlerWithoutSink(t *testing.T) {
	var stderr bytes.Buffer
	log := slog.New(newTeeHandler(&stderr, slog.LevelDebug, nil))
	log.Debug("leaving a process", "event", EventSweep)
	if !strings.Contains(stderr.String(), `level=DEBUG msg="leaving a process"`) {
		t.Errorf("stderr = %q", stderr.String())
	}
}
