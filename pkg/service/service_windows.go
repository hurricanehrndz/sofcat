//go:build windows

package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"golang.org/x/sys/windows/svc"
)

type sofcatWindowsService struct {
	cfg        config.Configuration
	managedRun func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error)
}

func (g *sofcatWindowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := newServiceRunner(g.cfg, g.managedRun)
	if err := runner.start(ctx); err != nil {
		slog.Warn("failed to start service runner", "err", err)
		return false, 1
	}

	changes <- svc.Status{State: svc.Running, Accepts: accepted}

	for req := range requests {
		switch req.Cmd {
		case svc.Interrogate:
			changes <- req.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			cancel()
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
			runner.stop(stopCtx)
			stopCancel()
			return false, 0
		default:
		}
	}

	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	runner.stop(stopCtx)
	stopCancel()
	return false, 0
}

func Run(cfg config.Configuration, managedRun func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error)) error {
	return svc.Run(cfg.ServiceName, &sofcatWindowsService{cfg: cfg, managedRun: managedRun})
}
