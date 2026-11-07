//go:build windows

package facts

import (
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
)

// Every Windows fact has a sample for makecatalogs, the SMBIOS ones included
// whether or not this machine can read them (TestBuiltinFactsSampled checks
// only what it collected).
func TestWindowsFactsSampled(t *testing.T) {
	samples := conditions.SampleFacts("windows")
	for _, name := range []string{"os_version", "os_build", "os_edition", "arch", "domain", "serial_number", "machine_manufacturer", "machine_model", "machine_type"} {
		if _, ok := samples[name]; !ok {
			t.Errorf("Windows fact %s has no sample in conditions.SampleFacts(\"windows\")", name)
		}
	}
	facts := conditions.Facts{}
	platformFacts(facts)
	for name := range facts {
		if _, ok := samples[name]; !ok {
			t.Errorf("collected Windows fact %s has no sample", name)
		}
	}
}
