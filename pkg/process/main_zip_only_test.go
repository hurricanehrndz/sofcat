//go:build zip_only

package process

import (
	"os"
	"testing"
)

// The process tests resolve and walk command-installer fixtures (msi, ps1),
// which the zip-only rule set rejects, so a zip_only test run checks them
// under the stock rules. ziponly_test.go sets the rule set it checks itself.
func TestMain(m *testing.M) {
	zipOnly = false
	os.Exit(m.Run())
}
