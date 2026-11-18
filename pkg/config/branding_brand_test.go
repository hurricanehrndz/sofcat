//go:build brand_branding

package config

import "testing"

// `just brand-check` runs this on the staged branding.yaml, so a key the
// build does not know fails the build, not the service at start.
func TestCompiledBrandingParses(t *testing.T) {
	if _, err := parseBuiltInBranding(compiledBranding); err != nil {
		t.Fatal(err)
	}
}
