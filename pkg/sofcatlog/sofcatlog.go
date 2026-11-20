package sofcatlog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

var (
	logMu     sync.Mutex
	logWriter *lumberjack.Logger

	// consoleOut is the console sink; overridable in tests.
	consoleOut io.Writer = os.Stdout
	// checkOnlyConsoleOut is the console sink in check-only, whose stdout is
	// only the inventory JSON; overridable in tests.
	checkOnlyConsoleOut io.Writer = os.Stderr
)

// SetOutput redirects the console sink; intended for tests. It takes effect
// on the next NewLog call.
func SetOutput(w io.Writer) {
	logMu.Lock()
	defer logMu.Unlock()
	consoleOut = w
}

// consoleHandler returns a text handler for out, or nil when out is a real
// file with no usable handle — e.g. the NULL stdout of an SCM-started Windows
// service. Non-file writers (test buffers) skip the probe.
func consoleHandler(out io.Writer, opts *slog.HandlerOptions) slog.Handler {
	if f, ok := out.(*os.File); ok {
		if _, err := f.Stat(); err != nil {
			return nil
		}
	}
	return slog.NewTextHandler(out, opts)
}

// fanoutHandler forwards each record to every child handler that has the
// record's level enabled.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (f fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	// Sinks are independent: one failing child (e.g. an invalid stdout handle
	// when running as a Windows service) must not block the others.
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (f fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return fanoutHandler{handlers: hs}
}

func (f fanoutHandler) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithGroup(name)
	}
	return fanoutHandler{handlers: hs}
}

// NewLog builds the fan-out logger: a text console sink, on stderr in
// check-only, plus a rotated file sink (structured JSON by default, plain text
// when cfg.LogFilePlain). The result is installed as slog's default logger.
func NewLog(cfg config.Configuration) error {
	logMu.Lock()
	defer logMu.Unlock()

	consoleLevel := slog.LevelWarn
	if cfg.Verbose {
		consoleLevel = slog.LevelInfo
	}
	if cfg.Debug {
		consoleLevel = slog.LevelDebug
	}

	// UI text sink extension point: Workstream D adds a third child handler
	// here (alongside the console and file sinks) that renders records for the
	// UI; D owns wiring that handler's transport. The fan-out handler already
	// forwards every record to each enabled child, so no other change is needed.
	out := consoleOut
	if cfg.CheckOnly {
		out = checkOnlyConsoleOut
	}
	var handlers []slog.Handler
	if h := consoleHandler(out, &slog.HandlerOptions{Level: consoleLevel}); h != nil {
		handlers = append(handlers, h)
	}

	logPath := filepath.Join(cfg.AppDataPath, "sofcat.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("unable to create log directory %s: %w", filepath.Dir(logPath), err)
	}

	if logWriter != nil {
		_ = logWriter.Close()
	}
	logWriter = &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    10,
		MaxBackups: 3,
		MaxAge:     28,
		Compress:   true,
	}

	fileLevel := slog.LevelInfo
	if cfg.Debug {
		fileLevel = slog.LevelDebug
	}
	fileOpts := &slog.HandlerOptions{Level: fileLevel}

	var fileHandler slog.Handler
	if cfg.LogFilePlain {
		fileHandler = slog.NewTextHandler(logWriter, fileOpts)
	} else {
		fileHandler = slog.NewJSONHandler(logWriter, fileOpts)
	}
	handlers = append(handlers, fileHandler)

	slog.SetDefault(slog.New(fanoutHandler{handlers: handlers}))
	return nil
}

// Close releases the active log file writer and resets logging state.
func Close() {
	logMu.Lock()
	defer logMu.Unlock()

	if logWriter != nil {
		_ = logWriter.Close()
		logWriter = nil
	}
	h := consoleHandler(consoleOut, nil)
	if h == nil {
		h = slog.DiscardHandler
	}
	slog.SetDefault(slog.New(h))
}
