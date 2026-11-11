package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// MDM is the highest authority key by key: a managed value replaces the
// config.yaml one, while keys policy leaves out (or blanks) keep the file's,
// so an admin can pin the repository and catalogs and still let the file
// carry the rest.
func TestApplyManagedWinsPerKey(t *testing.T) {
	warnings, warn := collect()
	cfg := Configuration{
		URL:      "https://file.example/",
		Manifest: "file_manifest",
		Catalogs: []string{"file_a", "file_b"},
		Debug:    true,
		Branding: Branding{Title: "File title", Tagline: "File tagline", Sections: []string{"File"}},
	}
	applyManaged(&cfg, map[string]any{
		"url":      "https://mdm.example/",
		"manifest": "   ",
		"catalogs": []any{"mdm"},
		"debug":    uint64(0), // REG_DWORD 0
		"verbose":  true,
		"branding": map[string]any{"title": "MDM title", "sections": []string{"MDM", "Other"}},
	}, warn)
	want := Configuration{
		URL:      "https://mdm.example/",
		Manifest: "file_manifest",
		Catalogs: []string{"mdm"},
		Verbose:  true,
		Branding: Branding{Title: "MDM title", Tagline: "File tagline", Sections: []string{"MDM", "Other"}},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got  %+v\nwant %+v", cfg, want)
	}
	if len(*warnings) != 0 {
		t.Fatalf("unexpected warnings %q", *warnings)
	}
}

// Both policy stores are readable by every local user, so basic-auth
// credentials are never taken from them (tls_client_key is only a path, so it
// is managed; the key file keeps its own ACL). Identity-bound keys would break the
// service, the UI and the installer if they differed from the build's.
func TestApplyManagedIgnoresSecretsAndIdentity(t *testing.T) {
	warnings, warn := collect()
	cfg := Configuration{AuthUser: "file", AuthPass: "file"}
	applyManaged(&cfg, map[string]any{
		"auth_user": "mdm", "auth_pass": "mdm", "tls_client_key": "mdm.key",
		"app_data_path": `D:\elsewhere`, "service_name": "x", "service_pipe_name": "x",
		"WriteRoots": map[string]any{}, "PayloadIdentifier": "com.example",
	}, warn)
	want := Configuration{AuthUser: "file", AuthPass: "file", TLSClientKey: "mdm.key"}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
	// The admin must learn the secrets were not applied; the other keys are
	// ignored silently, as the plist also carries payload metadata.
	if len(*warnings) != 2 {
		t.Fatalf("want a warning per secret, got %q", *warnings)
	}
}

// An admin's typo in one value must not cost them the rest of the policy.
func TestApplyManagedDropsWrongTypes(t *testing.T) {
	warnings, warn := collect()
	cfg := Configuration{URL: "https://file.example/", Catalogs: []string{"file"}, CheckOnly: true}
	applyManaged(&cfg, map[string]any{
		"url":       []any{"https://mdm.example/"},
		"catalogs":  []any{"ok", int64(7)},
		"checkonly": "false",
		"branding":  "not a dict",
		"manifest":  "mdm_manifest",
	}, warn)
	want := Configuration{URL: "https://file.example/", Catalogs: []string{"file"}, CheckOnly: true, Manifest: "mdm_manifest"}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
	if len(*warnings) != 4 {
		t.Fatalf("want a warning per dropped value, got %q", *warnings)
	}
}

// A configuration profile is a plist with the keys at the top level and
// branding as a dict.
func TestParseManagedPlist(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>url</key><string>https://mdm.example/</string>
	<key>catalogs</key><array><string>production</string></array>
	<key>tls_auth</key><true/>
	<key>verbose</key><integer>1</integer>
	<key>branding</key><dict><key>accent</key><string>#0b6e4f</string></dict>
</dict>
</plist>`)
	managed, err := parseManagedPlist(data)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Configuration
	applyManaged(&cfg, managed, func(string) {})
	want := Configuration{URL: "https://mdm.example/", Catalogs: []string{"production"}, TLSAuth: true, Verbose: true, Branding: Branding{Accent: "#0b6e4f"}}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
	if _, err := parseManagedPlist([]byte("not a plist")); err == nil {
		t.Fatal("garbage parsed")
	}
}

// With MDM supplying the repository, config.yaml is optional for a normal
// run, and the managed values beat the file's when both exist.
func TestGetManagedOverridesAndReplacesFile(t *testing.T) {
	origRead := readManagedSettings
	defer func() { readManagedSettings = origRead }()
	readManagedSettings = func(func(string)) map[string]any {
		return map[string]any{"url": "https://mdm.example/", "manifest": "mdm_manifest"}
	}
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	origExit := osExit
	defer func() { osExit = origExit }()
	osExit = func(code int) { t.Fatalf("unexpected exit %d", code) }
	resetServiceFlags()

	dir := t.TempDir()
	os.Args = []string{"sofcat.exe", "--config", filepath.Join(dir, "missing.yaml")}
	if cfg := Get(); cfg.URL != "https://mdm.example/" || cfg.URLPackages != "https://mdm.example/" || cfg.Manifest != "mdm_manifest" {
		t.Fatalf("missing config.yaml with MDM: %+v", cfg)
	}

	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("url: https://file.example/\nmanifest: file_manifest\ncatalogs: [file]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"sofcat.exe", "--config", configPath}
	cfg := Get()
	if cfg.URL != "https://mdm.example/" || cfg.Manifest != "mdm_manifest" || !reflect.DeepEqual(cfg.Catalogs, []string{"file"}) {
		t.Fatalf("MDM did not win key by key: %+v", cfg)
	}
}

// collect returns a warn func and the warnings it has received.
func collect() (*[]string, func(string)) {
	var got []string
	return &got, func(w string) { got = append(got, w) }
}
