//go:build !windows

package service

// StartUserSync does nothing off Windows: user-scope placements are
// Windows-only until macOS has a per-user agent.
func StartUserSync() {}
