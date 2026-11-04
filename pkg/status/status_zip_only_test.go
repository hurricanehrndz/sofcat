//go:build zip_only

package status

import (
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// A zip-only binary cannot run a check.script, so such an item must not read
// as needing (or not needing) action: the check fails instead.
func TestZipOnlyCheckScriptFails(t *testing.T) {
	item := catalog.Item{Check: catalog.InstallCheck{Script: "exit 0"}}
	if _, err := (&Checker{}).CheckStatus(item, "install", t.TempDir()); err == nil {
		t.Fatal("CheckStatus ran a check script in a zip-only build")
	}
}
