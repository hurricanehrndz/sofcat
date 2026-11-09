//go:build !windows && !darwin

package main

import "errors"

// userState fails off Windows and macOS: SofCat has no service there to
// serve a plan, so the agent places nothing and exits 0 at once.
func userState() (string, map[string]string, error) {
	return "", nil, errors.New("user-scope placements are supported on Windows and macOS only")
}
