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
		"branding": map[string]any{"title": "MDM title", "sections": []string{"MDM", "Other"}, "title_size": "large", "content_width": "compact", "filter_placement": "left", "banner_style": "logo", "banner_height": "large", "nav_placement": "left", "category_filter": "list", "item_view": "dialog", "progress": "button"},
	}, warn)
	want := Configuration{
		URL:      "https://mdm.example/",
		Manifest: "file_manifest",
		Catalogs: []string{"mdm"},
		Verbose:  true,
		Branding: Branding{Title: "MDM title", Tagline: "File tagline", Sections: []string{"MDM", "Other"}, TitleSize: "large", ContentWidth: "compact", FilterPlacement: "left", BannerStyle: "logo", BannerHeight: "large", NavPlacement: "left", CategoryFilter: "list", ItemView: "dialog", Progress: "button"},
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
	<key>branding</key><dict><key>accent</key><string>#0b6e4f</string><key>tagline_size</key><string>small</string><key>content_width</key><string>full</string><key>filter_placement</key><string>right</string><key>banner_align</key><string>right</string><key>banner_height</key><string>small</string><key>link_placement</key><string>app_bar</string><key>link_position</key><string>bottom_left</string></dict>
</dict>
</plist>`)
	managed, err := parseManagedPlist(data)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Configuration
	applyManaged(&cfg, managed, func(string) {})
	want := Configuration{URL: "https://mdm.example/", Catalogs: []string{"production"}, TLSAuth: true, Verbose: true, Branding: Branding{Accent: "#0b6e4f", TaglineSize: "small", ContentWidth: "full", FilterPlacement: "right", BannerAlign: "right", BannerHeight: "small", LinkPlacement: "app_bar", LinkPosition: "bottom_left"}}
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

// A field a rebranded build's compiled branding sets is fixed: neither
// config.yaml nor the managed settings change it, so the brand's look cannot
// be undone by a profile. The fields it leaves out still come from them, and
// MDM still supplies the repository.
func TestBuiltInBrandingWinsOverFileAndPolicy(t *testing.T) {
	old := builtInBranding
	t.Cleanup(func() { builtInBranding = old })
	builtInBranding = func() (Branding, error) {
		return parseBuiltInBranding([]byte("title: Brand title\ntagline: Brand tagline\naccent: '#112233'\nhelp_url: https://brand/help\nsections: [Brand]\n"))
	}
	managed := map[string]any{
		"url":      "https://mdm.example/",
		"branding": map[string]any{"accent": "#445566", "link_url": "https://mdm/help", "link_label": "MDM label"},
	}
	got := resolve(Configuration{Branding: Branding{Title: "File title", ListHeading: "File heading"}}, managed, func(w string) { t.Errorf("warning %q", w) })
	want := Branding{Title: "Brand title", Tagline: "Brand tagline", Accent: "#112233", LinkURL: "https://brand/help", LinkLabel: "MDM label", ListHeading: "File heading", Sections: []string{"Brand"}}
	if got.URL != "https://mdm.example/" || !reflect.DeepEqual(got.Branding, want) {
		t.Fatalf("got url %q branding %+v, want %+v", got.URL, got.Branding, want)
	}
}

// A branding.yaml with a key outside the branding block is refused whole and
// reported, and a build without one fixes nothing.
func TestBuiltInBrandingRejectsUnknownKeys(t *testing.T) {
	if _, err := parseBuiltInBranding([]byte("title: T\nurl: https://x/\n")); err == nil {
		t.Fatal("want an error for a key outside the branding block")
	}
	if b, err := parseBuiltInBranding(nil); err != nil || !reflect.DeepEqual(b, Branding{}) {
		t.Fatalf("empty: %+v, %v", b, err)
	}
	old := builtInBranding
	t.Cleanup(func() { builtInBranding = old })
	builtInBranding = func() (Branding, error) { return parseBuiltInBranding([]byte("nope: 1\n")) }
	warnings, warn := collect()
	if got := resolve(Configuration{}, nil, warn).Branding; !reflect.DeepEqual(got, Branding{}) || len(*warnings) != 1 {
		t.Fatalf("got %+v, warnings %q", got, *warnings)
	}
}

// fillFrom handles strings and lists; a Branding field of another type
// would silently never take the build's value.
func TestFillFromCoversEveryBrandingField(t *testing.T) {
	var def Branding
	v := reflect.ValueOf(&def).Elem()
	for i := range v.NumField() {
		switch f := v.Field(i); f.Kind() {
		case reflect.String:
			f.SetString("x")
		case reflect.Slice:
			f.Set(reflect.ValueOf([]string{"x"}))
		default:
			t.Fatalf("Branding.%s: fillFrom does not handle %s", v.Type().Field(i).Name, f.Kind())
		}
	}
	var got Branding
	got.fillFrom(def)
	if !reflect.DeepEqual(got, def) {
		t.Fatalf("got %+v", got)
	}
}

// collect returns a warn func and the warnings it has received.
func collect() (*[]string, func(string)) {
	var got []string
	return &got, func(w string) { got = append(got, w) }
}

// A profile or GPO that arrives, changes or goes away while the service runs
// must apply at its next run without a restart: Refresh reads policy again
// over the config.yaml Get parsed, so a dropped setting falls back to the
// file's value rather than keeping the policy's last one.
func TestRefreshRereadsManagedSettings(t *testing.T) {
	origRead := readManagedSettings
	defer func() { readManagedSettings = origRead }()
	managed := map[string]any{}
	readManagedSettings = func(func(string)) map[string]any { return managed }
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	origExit := osExit
	defer func() { osExit = origExit }()
	osExit = func(code int) { t.Fatalf("unexpected exit %d", code) }
	resetServiceFlags()
	defer func() { fileConfig = nil }()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("url: https://file.example/\nmanifest: file_manifest\ncatalogs: [file]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"sofcat.exe", "--config", configPath}
	start := Get()
	if start.URL != "https://file.example/" || start.ServiceInterval != "1h" {
		t.Fatalf("start: %+v", start)
	}

	// Policy written after the start, including keys that must match the
	// installed build and so are never managed.
	managed = map[string]any{
		"url": "https://mdm.example/", "catalogs": []any{"mdm"}, "service_interval": "15m", "tls_auth": true,
		"app_data_path": "/elsewhere", "service_name": "other", "service_pipe_name": "other", "auth_pass": "leaked",
	}
	got := Refresh(start)
	if got.URL != "https://mdm.example/" || got.URLPackages != "https://mdm.example/" || got.Manifest != "file_manifest" ||
		!reflect.DeepEqual(got.Catalogs, []string{"mdm"}) || got.ServiceInterval != "15m" || !got.TLSAuth {
		t.Fatalf("policy written after the start did not apply: %+v", got)
	}
	if got.AppDataPath != start.AppDataPath || got.CachePath != start.CachePath || got.ServiceName != start.ServiceName ||
		got.ServicePipeName != start.ServicePipeName || got.ConfigPath != configPath {
		t.Fatalf("a refresh changed what is fixed at process start: %+v", got)
	}
	if len(got.ManagedWarnings) != 1 {
		t.Fatalf("want only the auth_pass warning, got %q", got.ManagedWarnings)
	}

	// Policy removed: every key is config.yaml's again.
	managed = nil
	if back := Refresh(got); !reflect.DeepEqual(back, start) {
		t.Fatalf("policy removed:\ngot  %+v\nwant %+v", back, start)
	}
}

// The service's tests and other callers build a Configuration by hand; a
// refresh must not replace it with defaults.
func TestRefreshWithoutGetKeepsConfiguration(t *testing.T) {
	cfg := Configuration{URL: "https://hand.example/"}
	if got := Refresh(cfg); !reflect.DeepEqual(got, cfg) {
		t.Fatalf("got %+v", got)
	}
}

// help_url and help_label still work as link_url and link_label. Within one
// source the new name wins; across sources managed settings win as for any
// key, whichever name they use, so a policy written with the old names keeps
// overriding config.yaml after the file moves to the new ones.
func TestLinkAliasesFollowSourcePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    Branding
		managed map[string]any
		url     string
		label   string
	}{
		{"old names in the file", Branding{HelpURL: "https://file/old", HelpLabel: "File old"}, nil, "https://file/old", "File old"},
		{"new name wins in the file", Branding{HelpURL: "https://file/old", LinkURL: "https://file/new", HelpLabel: "File old"}, nil, "https://file/new", "File old"},
		{"new name wins in policy", Branding{}, map[string]any{"help_url": "https://mdm/old", "link_url": "https://mdm/new", "help_label": "MDM old", "link_label": "MDM new"}, "https://mdm/new", "MDM new"},
		{"old name in policy beats new name in the file", Branding{LinkURL: "https://file/new", LinkLabel: "File new"}, map[string]any{"help_url": "https://mdm/old"}, "https://mdm/old", "File new"},
		{"blank new name in policy keeps its old one", Branding{LinkURL: "https://file/new"}, map[string]any{"help_url": "https://mdm/old", "link_url": " "}, "https://mdm/old", ""},
	} {
		var managed map[string]any
		if tc.managed != nil {
			managed = map[string]any{"branding": tc.managed}
		}
		got := resolve(Configuration{Branding: tc.file}, managed, func(w string) { t.Errorf("%s: warning %q", tc.name, w) }).Branding
		if got.LinkURL != tc.url || got.LinkLabel != tc.label || got.HelpURL != "" || got.HelpLabel != "" {
			t.Errorf("%s: got url %q label %q (help %q %q), want %q %q", tc.name, got.LinkURL, got.LinkLabel, got.HelpURL, got.HelpLabel, tc.url, tc.label)
		}
	}
}
