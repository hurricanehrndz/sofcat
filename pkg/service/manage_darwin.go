//go:build darwin

package service

import (
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// RunAction manages the installed launchd daemon (see launchdAction).
func RunAction(cfg config.Configuration, action string) error {
	return launchdAction(cfg, action, identity.LaunchdPlistPath(), identity.UserAgentPlistPath())
}

// ServiceStatus summarises `launchctl print system/<label>`.
func ServiceStatus(_ config.Configuration) (string, error) {
	return launchdServiceStatus()
}
