package process

import (
	"reflect"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// A zip item names its archives per placement and OS in variants, not in
// location, and is removed from its receipts, so it needs no uninstaller.
func TestMissingFieldsZip(t *testing.T) {
	withVariants := catalog.Item{Installer: catalog.InstallerItem{
		Type: "zip",
		Variants: map[string]map[string]catalog.Variant{
			"claude-code-skills": {"windows": {Location: "packages/a.zip", Hash: "abc", TreeHash: "sha256:def"}},
		},
	}}
	if got := missingFields(withVariants); got != nil {
		t.Errorf("zip item with variants: missing %v", got)
	}
	if got := missingFields(catalog.Item{Installer: catalog.InstallerItem{Type: "zip"}}); !reflect.DeepEqual(got, []string{"installer.variants"}) {
		t.Errorf("zip item without variants: missing %v, want installer.variants", got)
	}
}
