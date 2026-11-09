package sofcatlog

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
)

// Event IDs of the management commands' records in the Windows Application
// log, one per kind of step (site/src/content/docs/reference/event-log.md).
// The source is registered as an EventCreate source, which accepts IDs 1 to
// 1000. The IDs are a contract with whoever filters on them: never renumber
// one, only add.
const (
	EventGeneral          EventID = 1  // a record with no step of its own
	EventCommand          EventID = 2  // a command starting, succeeding or failing
	EventServiceInstalled EventID = 3  // the service registered with the SCM, or undone
	EventServiceStarted   EventID = 4  // the service started
	EventServiceStopped   EventID = 5  // the service stopped
	EventServiceDeleted   EventID = 6  // the service deleted
	EventRunValueWritten  EventID = 7  // the logon Run value written
	EventRunValueRemoved  EventID = 8  // the logon Run value removed
	EventProtected        EventID = 9  // the data directory or config ACL applied, or not
	EventAgentEnded       EventID = 10 // the sweep ended a running agent
	EventAgentNotExited   EventID = 11 // an ended agent had not exited 5 s later
	EventSweep            EventID = 12 // the sweep could not do part of its job
)

// EventID is a record's event ID. A record carries it as any attribute of
// this type, by convention `"event", sofcatlog.EventX`; the tee takes it out
// of the text. One without it is EventGeneral. Pass it on the record, not
// through Logger.With.
type EventID uint32

// EventSink is the part of *eventlog.Log the tee writes to, so the tests can
// record what it would write.
type EventSink interface {
	Info(eid uint32, msg string) error
	Warning(eid uint32, msg string) error
	Error(eid uint32, msg string) error
}

// teeHandler writes every record to a console text handler and, from Info up,
// to an event sink as an entry of the matching type: Info, Warning or Error.
// The entry's text is the message followed by the attributes. A sink that
// fails is ignored: logging never fails the command.
type teeHandler struct {
	console slog.Handler
	text    slog.Handler // renders the attributes into buf
	buf     *bytes.Buffer
	mu      *sync.Mutex
	sink    EventSink // nil writes the console only
}

func newTeeHandler(w io.Writer, level slog.Leveler, sink EventSink) *teeHandler {
	buf := &bytes.Buffer{}
	return &teeHandler{
		console: slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}),
		text: slog.NewTextHandler(buf, &slog.HandlerOptions{
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey || a.Key == slog.MessageKey) {
					return slog.Attr{}
				}
				return a
			},
		}),
		buf:  buf,
		mu:   &sync.Mutex{},
		sink: sink,
	}
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.console.Enabled(ctx, level)
}

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	id := EventGeneral
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if v, ok := a.Value.Any().(EventID); ok {
			id = v
		} else {
			out.AddAttrs(a)
		}
		return true
	})
	err := h.console.Handle(ctx, out)
	if h.sink == nil || r.Level < slog.LevelInfo {
		return err
	}

	h.mu.Lock()
	h.buf.Reset()
	_ = h.text.Handle(ctx, out)
	msg := strings.TrimSpace(r.Message + " " + h.buf.String())
	h.mu.Unlock()
	switch {
	case r.Level >= slog.LevelError:
		_ = h.sink.Error(uint32(id), msg)
	case r.Level >= slog.LevelWarn:
		_ = h.sink.Warning(uint32(id), msg)
	default:
		_ = h.sink.Info(uint32(id), msg)
	}
	return err
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.console = h.console.WithAttrs(attrs)
	c.text = h.text.WithAttrs(attrs)
	return &c
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.console = h.console.WithGroup(name)
	c.text = h.text.WithGroup(name)
	return &c
}

// StartManagementLog makes slog's default logger, for one service management
// command (-serviceinstall, -serviceremove, -servicestart, -servicestop),
// write to w and, on Windows, to the Application log under source, which it
// registers first if it is missing. The running service logs to sofcat.log
// instead (NewLog). When the event log cannot be registered or opened, it
// says so on w and writes w only. The returned func restores the previous
// default and closes the event log.
func StartManagementLog(source string, debug bool, w io.Writer) func() {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	sink, closeSink, err := openEventSink(source)
	prev := slog.Default()
	slog.SetDefault(slog.New(newTeeHandler(w, level, sink)))
	if err != nil {
		slog.Warn("unable to write to the event log; logging to stderr only", "source", source, "err", err)
	}
	return func() {
		slog.SetDefault(prev)
		closeSink()
	}
}
