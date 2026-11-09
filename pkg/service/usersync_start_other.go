//go:build !windows && !darwin

package service

// StartUserSync does nothing off Windows and macOS, the only systems with a
// per-user helper.
func StartUserSync(string) {}
