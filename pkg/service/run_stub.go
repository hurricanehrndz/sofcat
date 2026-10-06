//go:build !windows

package service

import (
	"errors"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

func Run(_ config.Configuration, _ func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error)) error {
	return errors.New("service mode is only supported on Windows")
}
