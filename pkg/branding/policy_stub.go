//go:build !windows

package branding

import "github.com/hurricanehrndz/sofcat/pkg/config"

// readPolicy has no policy store off Windows; only config.yaml applies.
func readPolicy() config.Branding { return config.Branding{} }
