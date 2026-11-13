package branding

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/config"
)

func TestResolveNothingConfiguredIsEmpty(t *testing.T) {
	if got := resolve(config.Branding{}); !reflect.DeepEqual(got, Branding{}) {
		t.Fatalf("empty branding resolved to %#v", got)
	}
}

func TestResolveValidatesText(t *testing.T) {
	got := resolve(config.Branding{
		Title:     "  Acme\r\nSoftware\tCenter  ",
		Tagline:   strings.Repeat("é", maxTagline+5),
		HelpLabel: " Get help ",
	})
	if got.Title != "AcmeSoftwareCenter" {
		t.Errorf("title %q kept control characters", got.Title)
	}
	if n := len([]rune(got.Tagline)); n != maxTagline {
		t.Errorf("tagline has %d runes, want the %d cap", n, maxTagline)
	}
	if got.HelpLabel != "Get help" {
		t.Errorf("help label %q not trimmed", got.HelpLabel)
	}
}

// The list heading is admin text shown in place of "Available software": it
// is validated like the title.
func TestResolveListHeading(t *testing.T) {
	if got := resolve(config.Branding{ListHeading: "  Skills\nand plugins  "}).ListHeading; got != "Skillsand plugins" {
		t.Errorf("heading %q kept control characters or padding", got)
	}
	if n := len([]rune(resolve(config.Branding{ListHeading: strings.Repeat("x", maxHeading+9)}).ListHeading)); n != maxHeading {
		t.Errorf("heading has %d runes, want the %d cap", n, maxHeading)
	}
}

// Each section name is a tab label, so it is validated like a heading; an
// empty or repeated name would make an empty or duplicate tab.
func TestResolveSections(t *testing.T) {
	got := resolve(config.Branding{Sections: []string{" Skills\n", "", "\t", "Plugins", "skills", strings.Repeat("x", maxHeading+3)}}).Sections
	want := []string{"Skills", "Plugins", strings.Repeat("x", maxHeading)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sections = %q, want %q", got, want)
	}

	many := make([]string, maxSections+5)
	for i := range many {
		many[i] = fmt.Sprintf("S%d", i)
	}
	if got := resolve(config.Branding{Sections: append([]string{"S0"}, many...)}).Sections; len(got) != maxSections || got[maxSections-1] != many[maxSections-1] {
		t.Errorf("sections = %d entries ending %q, want the first %d", len(got), got[len(got)-1], maxSections)
	}
}

// The help link is opened in the system browser, so anything but an absolute
// http(s) URL (javascript:, file:, a bare path) must never reach the UI.
func TestResolveHelpURL(t *testing.T) {
	for value, want := range map[string]string{
		"https://example.invalid/help": "https://example.invalid/help",
		" HTTP://intranet/help ":       "http://intranet/help", //nolint:gocritic // mapKey: the whitespace is the input under test
		"javascript:alert(1)":          "",
		"file:///C:/Windows/notepad":   "",
		"/help":                        "",
		"https://":                     "",
		"https://x/" + strings.Repeat("a", maxHelpURL): "",
	} {
		if got := resolve(config.Branding{HelpURL: value}).HelpURL; got != want {
			t.Errorf("help url %q resolved to %q, want %q", value, got, want)
		}
	}
}

func TestResolveAccent(t *testing.T) {
	for value, want := range map[string]string{
		"#0B6E4F":       "#0b6e4f",
		" #0b6e4f ":     "#0b6e4f",
		"0b6e4f":        "",
		"#0b6e4":        "",
		"red":           "",
		"#0b6e4f; x:y":  "",
		"var(--accent)": "",
	} {
		if got := resolve(config.Branding{Accent: value}).Accent; got != want {
			t.Errorf("accent %q resolved to %q, want %q", value, got, want)
		}
	}
}

// pngHeader is enough for http.DetectContentType to call it a PNG.
var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveLogo(t *testing.T) {
	svg := []byte("\xef\xbb\xbf<?xml version=\"1.0\"?>\n<!-- logo -->\n<svg xmlns=\"http://www.w3.org/2000/svg\"/>")
	tests := []struct {
		name     string
		path     string
		wantMime string
	}{
		{"png", writeFile(t, "logo.png", pngHeader), "image/png"},
		{"jpeg", writeFile(t, "logo.jpg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF")), "image/jpeg"},
		{"svg", writeFile(t, "logo.svg", svg), "image/svg+xml"},
		{"html named png", writeFile(t, "evil.png", []byte("<html><script>alert(1)</script>")), ""},
		{"svg-like element", writeFile(t, "x.svg", []byte("<svgfoo/>")), ""},
		{"empty", writeFile(t, "empty.png", nil), ""},
		{"too large", writeFile(t, "big.png", append(pngHeader, make([]byte, MaxLogoBytes)...)), ""},
		{"missing", filepath.Join(t.TempDir(), "missing.png"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(config.Branding{Title: "kept", Logo: tt.path})
			if got.LogoMime != tt.wantMime {
				t.Fatalf("mime %q, want %q", got.LogoMime, tt.wantMime)
			}
			if got.Title != "kept" {
				t.Fatal("a bad logo must not drop the other fields")
			}
			if tt.wantMime == "" {
				if got.LogoBase64 != "" {
					t.Fatal("rejected logo still carried bytes")
				}
				return
			}
			want, _ := os.ReadFile(tt.path)
			if decoded, _ := base64.StdEncoding.DecodeString(got.LogoBase64); string(decoded) != string(want) {
				t.Fatal("logo bytes did not round-trip")
			}
		})
	}
}

// The banner's background image goes through the same checks as the logo,
// with its own larger cap, and neither image depends on the other: an admin
// may set an accent, a background image or both.
func TestResolveBackgroundImage(t *testing.T) {
	jpeg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF")
	photo := writeFile(t, "banner.jpg", append(jpeg, make([]byte, MaxLogoBytes)...))
	got := resolve(config.Branding{Accent: "#D32323", BackgroundImage: photo})
	if got.BackgroundMime != "image/jpeg" || got.Accent != "#d32323" || got.LogoMime != "" {
		t.Fatalf("background over the logo cap, with an accent: %+v", got)
	}
	want, _ := os.ReadFile(photo)
	if decoded, _ := base64.StdEncoding.DecodeString(got.BackgroundBase64); string(decoded) != string(want) {
		t.Fatal("background bytes did not round-trip")
	}

	for name, path := range map[string]string{
		"too large": writeFile(t, "big.jpg", append(jpeg, make([]byte, MaxBackgroundBytes)...)),
		"not image": writeFile(t, "page.jpg", []byte("<html><script>alert(1)</script>")),
		"missing":   filepath.Join(t.TempDir(), "missing.jpg"),
	} {
		got := resolve(config.Branding{Accent: "#d32323", BackgroundImage: path})
		if got.BackgroundMime != "" || got.BackgroundBase64 != "" {
			t.Errorf("%s: background kept: %s, %d bytes", name, got.BackgroundMime, len(got.BackgroundBase64))
		}
		if got.Accent != "#d32323" {
			t.Errorf("%s: a rejected background dropped the accent the banner falls back to", name)
		}
	}
}

// The admin picks the banner text colour for their picture. Anything but
// light or dark is dropped, so the UI falls back to its default instead of
// putting an arbitrary string into the page.
func TestResolveBackgroundText(t *testing.T) {
	for in, want := range map[string]string{
		"dark": "dark", " Light ": "light", "DARK": "dark", "": "", "#000000": "", "dark; x": "",
	} {
		if got := resolve(config.Branding{BackgroundText: in}).BackgroundText; got != want {
			t.Errorf("background_text %q resolved to %q, want %q", in, got, want)
		}
	}
}
