//go:build !windows && !darwin

package service

import (
	"errors"

	"github.com/hurricanehrndz/sofcat/pkg/config"
)

func RunAction(_ config.Configuration, _ string) error {
	return errors.New("service install/remove/start/stop is only supported on Windows and macOS")
}

func ServiceStatus(_ config.Configuration) (string, error) {
	return "", errors.New("service status is only supported on Windows and macOS")
}
