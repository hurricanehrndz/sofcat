//go:build windows

package config

import (
	"os"
	"reflect"
	"testing"

	"golang.org/x/sys/windows/registry"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The reader runs against a scratch HKCU key so the test needs no admin token
// and never touches a real policy. It checks the registry types an admin
// would use for each key, and that names match case-insensitively.
func TestReadManagedFromRegistry(t *testing.T) {
	path := `Software\SofCatTest\` + t.Name()
	managedRoot, managedPath = registry.CURRENT_USER, path
	t.Cleanup(func() {
		managedRoot, managedPath = registry.LOCAL_MACHINE, identity.PolicyKey()
		_ = registry.DeleteKey(registry.CURRENT_USER, path+`\Branding`)
		_ = registry.DeleteKey(registry.CURRENT_USER, path)
	})

	if got := readManaged(func(w string) { t.Error(w) }); got != nil {
		t.Fatalf("missing key read as %#v", got)
	}

	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = key.Close() }()
	branding, _, err := registry.CreateKey(registry.CURRENT_USER, path+`\Branding`, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = branding.Close() }()
	for _, set := range []error{
		key.SetStringValue("URL", "https://mdm.example/"),
		key.SetExpandStringValue("tls_client_cert", `%ProgramData%\SofCat\client.pem`),
		key.SetStringsValue("catalogs", []string{"production", "testing"}),
		key.SetDWordValue("tls_auth", 1),
		key.SetDWordValue("debug", 0),
		key.SetStringValue("auth_pass", "leaked"),
		branding.SetStringValue("Title", "Policy Title"),
		branding.SetStringsValue("sections", []string{"Skills", "Plugins"}),
	} {
		if set != nil {
			t.Fatal(set)
		}
	}

	cfg := Configuration{Debug: true, AuthPass: "file"}
	var warnings []string
	warn := func(w string) { warnings = append(warnings, w) }
	applyManaged(&cfg, readManaged(warn), warn)
	want := Configuration{
		URL:           "https://mdm.example/",
		TLSClientCert: os.Getenv("ProgramData") + `\SofCat\client.pem`,
		Catalogs:      []string{"production", "testing"},
		TLSAuth:       true,
		AuthPass:      "file",
		Branding:      Branding{Title: "Policy Title", Sections: []string{"Skills", "Plugins"}},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got  %#v\nwant %#v", cfg, want)
	}
	if len(warnings) != 1 {
		t.Fatalf("want only the auth_pass warning, got %q", warnings)
	}
}
