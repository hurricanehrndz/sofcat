//go:build !windows && !darwin

package config

// readManaged has no policy store off Windows and macOS.
func readManaged(func(string)) map[string]any { return nil }
