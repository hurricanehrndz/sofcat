package identity

import (
	"path/filepath"
	"testing"
)

func rebrand(t *testing.T, slug, name string) {
	t.Helper()
	origSlug, origName := Slug, Name
	t.Cleanup(func() { Slug, Name = origSlug, origName })
	Slug, Name = slug, name
}

// The stock names are what existing installs, GPOs and MDM profiles already
// use; changing any of them strands those machines.
func TestStockNamesUnchanged(t *testing.T) {
	t.Setenv("ProgramData", "PD")
	want := map[string]string{
		"service": "sofcat",
		"pipe":    "sofcat-service",
		"data":    filepath.Join("PD", "SofCat"),
		"config":  filepath.Join("PD", "SofCat", "config.yaml"),
		"policy":  `SOFTWARE\Policies\SofCat`,
	}
	assertNames(t, want)
}

// Two builds with different identities must not share any name the OS or MDM
// keys on, or the second install hijacks or breaks the first.
func TestRebrandMovesEveryName(t *testing.T) {
	t.Setenv("ProgramData", "PD")
	rebrand(t, "acme", "Acme Software Center")
	want := map[string]string{
		"service": "acme",
		"pipe":    "acme-service",
		"data":    filepath.Join("PD", "Acme Software Center"),
		"config":  filepath.Join("PD", "Acme Software Center", "config.yaml"),
		"policy":  `SOFTWARE\Policies\Acme Software Center`,
	}
	assertNames(t, want)
}

func assertNames(t *testing.T, want map[string]string) {
	t.Helper()
	got := map[string]string{
		"service": ServiceName(),
		"pipe":    PipeName(),
		"data":    DataDir(),
		"config":  ConfigPath(),
		"policy":  PolicyKey(),
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}
}
