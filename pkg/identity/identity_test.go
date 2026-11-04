package identity

import (
	"path/filepath"
	"testing"
)

func rebrand(t *testing.T, slug, name, identifier string) {
	t.Helper()
	origSlug, origName, origID := Slug, Name, Identifier
	t.Cleanup(func() { Slug, Name, Identifier = origSlug, origName, origID })
	Slug, Name, Identifier = slug, name, identifier
}

// The stock names are what existing installs, GPOs and MDM profiles already
// use; changing any of them strands those machines.
func TestStockNamesUnchanged(t *testing.T) {
	t.Setenv("ProgramData", "PD")
	want := map[string]string{
		"service":       "sofcat",
		"pipe":          "sofcat-service",
		"data":          filepath.Join("PD", "SofCat"),
		"config":        filepath.Join("PD", "SofCat", "config.yaml"),
		"policy":        `SOFTWARE\Policies\SofCat`,
		"mac data":      "/Library/Application Support/SofCat/Data",
		"mac config":    "/Library/Application Support/SofCat/config.yaml",
		"mac socket":    "/var/run/sofcat",
		"linux socket":  "/run/sofcat",
		"launchd label": "ca.hrndz.sofcat",
		"launchd plist": "/Library/LaunchDaemons/ca.hrndz.sofcat.plist",
		"cli":           "/Library/Application Support/SofCat/bin/sofcat",
		"app":           "/Applications/SofCat.app",
	}
	assertNames(t, want)
}

// Two builds with different identities must not share any name the OS or MDM
// keys on, or the second install hijacks or breaks the first.
func TestRebrandMovesEveryName(t *testing.T) {
	t.Setenv("ProgramData", "PD")
	rebrand(t, "acme", "Acme Software Center", "com.example.acme")
	want := map[string]string{
		"service":       "acme",
		"pipe":          "acme-service",
		"data":          filepath.Join("PD", "Acme Software Center"),
		"config":        filepath.Join("PD", "Acme Software Center", "config.yaml"),
		"policy":        `SOFTWARE\Policies\Acme Software Center`,
		"mac data":      "/Library/Application Support/Acme Software Center/Data",
		"mac config":    "/Library/Application Support/Acme Software Center/config.yaml",
		"mac socket":    "/var/run/acme",
		"linux socket":  "/run/acme",
		"launchd label": "com.example.acme",
		"launchd plist": "/Library/LaunchDaemons/com.example.acme.plist",
		"cli":           "/Library/Application Support/Acme Software Center/bin/acme",
		"app":           "/Applications/Acme Software Center.app",
	}
	assertNames(t, want)
}

func assertNames(t *testing.T, want map[string]string) {
	t.Helper()
	got := map[string]string{
		"service":       ServiceName(),
		"pipe":          PipeName(),
		"data":          DataDirFor("windows"),
		"config":        ConfigPathFor("windows"),
		"policy":        PolicyKey(),
		"mac data":      DataDirFor("darwin"),
		"mac config":    ConfigPathFor("darwin"),
		"mac socket":    SocketDirFor("darwin"),
		"linux socket":  SocketDirFor("linux"),
		"launchd label": LaunchdLabel(),
		"launchd plist": LaunchdPlistPath(),
		"cli":           CLIPath(),
		"app":           AppPath(),
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}
}
