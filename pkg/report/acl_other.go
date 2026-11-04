//go:build !windows

package report

import "os"

// createProtectedTemp is a plain temp file off Windows, where SofCat only
// runs for development.
func createProtectedTemp(dir, prefix string) (*os.File, error) {
	return os.CreateTemp(dir, prefix+"*.tmp")
}

// renameRetryable: off Windows a rename never fails for a transient reason.
func renameRetryable(error) bool { return false }
