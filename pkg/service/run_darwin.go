//go:build darwin

package service

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// Run is the launchd daemon: it serves the portable runner until launchd
// sends SIGTERM (bootout, shutdown) or the process gets SIGINT, then stops it
// with the Windows service's stop timeout. launchd restarts it on exit
// (KeepAlive), so a start failure is returned and logged by launchd.
func Run(cfg config.Configuration, managedRun func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error)) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	runner := newServiceRunner(cfg, managedRun)
	if err := runner.start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	slog.Info("service stopping")

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	runner.stop(stopCtx)
	return nil
}
