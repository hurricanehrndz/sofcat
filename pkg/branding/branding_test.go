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
		LinkLabel: " Get help ",
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

// The applies-to filter label is admin text like the list heading, capped
// at the link label length.
func TestResolveAppliesToFilterLabel(t *testing.T) {
	if got := resolve(config.Branding{AppliesToFilterLabel: "  Works\nwith  "}).AppliesToFilterLabel; got != "Workswith" {
		t.Errorf("label %q kept control characters or padding", got)
	}
	if n := len([]rune(resolve(config.Branding{AppliesToFilterLabel: strings.Repeat("x", maxLinkLabel+9)}).AppliesToFilterLabel)); n != maxLinkLabel {
		t.Errorf("label has %d runes, want the %d cap", n, maxLinkLabel)
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
func TestResolveLinkURL(t *testing.T) {
	for value, want := range map[string]string{
		"https://example.invalid/help": "https://example.invalid/help",
		" HTTP://intranet/help ":       "http://intranet/help", //nolint:gocritic // mapKey: the whitespace is the input under test
		"javascript:alert(1)":          "",
		"file:///C:/Windows/notepad":   "",
		"/help":                        "",
		"https://":                     "",
		"https://x/" + strings.Repeat("a", maxLinkURL): "",
	} {
		if got := resolve(config.Branding{LinkURL: value}).HelpURL; got != want {
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

// The font sizes are a closed scale; anything else, a number included, is
// dropped so the UI keeps today's size instead of an arbitrary one.
func TestResolveFontSizes(t *testing.T) {
	for in, want := range map[string]string{
		"small": "small", "X-Large": "x-large", "medium": "medium", "": "", "huge": "", "24px": "", "2": "",
	} {
		got := resolve(config.Branding{TitleSize: in, TaglineSize: in})
		if got.TitleSize != want || got.TaglineSize != want {
			t.Errorf("size %q resolved to title %q, tagline %q, want %q", in, got.TitleSize, got.TaglineSize, want)
		}
	}
}

// content_width is a closed set too: an unknown value must leave the UI's
// default rather than reach it as a layout the CSS has no rules for.
func TestResolveContentWidth(t *testing.T) {
	for in, want := range map[string]string{
		"compact": "compact", "Wide": "wide", "FULL": "full", "": "", "narrow": "", "1200px": "", "100%": "",
	} {
		if got := resolve(config.Branding{ContentWidth: in}).ContentWidth; got != want {
			t.Errorf("content_width %q resolved to %q, want %q", in, got, want)
		}
	}
}

// filter_placement is a closed set: an unknown value must leave the UI's
// default, right, rather than a placement the CSS has no rule for.
func TestResolveFilterPlacement(t *testing.T) {
	for in, want := range map[string]string{
		"right": "right", "Left": "left", "LEFT": "left", "": "", "center": "", "top": "",
	} {
		if got := resolve(config.Branding{FilterPlacement: in}).FilterPlacement; got != want {
			t.Errorf("filter_placement %q resolved to %q, want %q", in, got, want)
		}
	}
}

// progress is a closed set: an unknown value must leave the UI's default,
// strip, so a typo never moves running work out of the strip.
func TestResolveProgress(t *testing.T) {
	for in, want := range map[string]string{
		"strip": "strip", "Button": "button", "BUTTON": "button", "": "", "bar": "", "both": "",
	} {
		if got := resolve(config.Branding{Progress: in}).Progress; got != want {
			t.Errorf("progress %q resolved to %q, want %q", in, got, want)
		}
	}
}

// item_view is a closed set: an unknown value must leave the UI's default,
// the detail page, rather than a mode the UI has no code for.
func TestResolveItemView(t *testing.T) {
	for in, want := range map[string]string{
		"page": "page", "Dialog": "dialog", "DIALOG": "dialog", "": "", "modal": "", "sheet": "",
	} {
		if got := resolve(config.Branding{ItemView: in}).ItemView; got != want {
			t.Errorf("item_view %q resolved to %q, want %q", in, got, want)
		}
	}
}

// nav_placement and category_filter are closed sets: an unknown value must
// leave the UI's defaults, the bar at the top and the Category select, so a
// typo never moves the bar.
func TestResolveNavPlacementAndCategoryFilter(t *testing.T) {
	for in, want := range map[string]string{
		"top": "top", "Left": "left", "LEFT": "left", "": "", "sidebar": "", "right": "",
	} {
		if got := resolve(config.Branding{NavPlacement: in}).NavPlacement; got != want {
			t.Errorf("nav_placement %q resolved to %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"select": "select", "List": "list", "LIST": "list", "": "", "chips": "", "sidebar": "",
	} {
		if got := resolve(config.Branding{CategoryFilter: in}).CategoryFilter; got != want {
			t.Errorf("category_filter %q resolved to %q, want %q", in, got, want)
		}
	}
}

// The banner and app-bar keys are closed sets: an unknown value must leave the
// UI's default rather than a layout the CSS has no rule for.
func TestResolveBannerLayout(t *testing.T) {
	for _, tc := range []struct{ brand, style, align, want string }{
		{"full", "text", "left", "full text left"},
		{"Mark", " LOGO ", "Center", "mark logo center"},
		{"none", "logo", "right", "none logo right"},
		{"", "", "", "  "},
		{"hidden", "image", "middle", "  "},
	} {
		got := resolve(config.Branding{AppBarBrand: tc.brand, BannerStyle: tc.style, BannerAlign: tc.align})
		if s := got.AppBarBrand + " " + got.BannerStyle + " " + got.BannerAlign; s != tc.want {
			t.Errorf("%q %q %q resolved to %q, want %q", tc.brand, tc.style, tc.align, s, tc.want)
		}
	}
}

// The example logo banners must pass the logo loader as they are: an SVG
// under the size cap, with its raster embedded as a data URI. They carry no
// name of their own: WKWebView reads an SVG's <title> or aria-label to screen
// readers over the <img> alt text the UI sets from branding title.
func TestResolveExampleLogoBanners(t *testing.T) {
	for _, name := range []string{"sofcat-logo-banner-light.svg", "sofcat-logo-banner-dark.svg"} {
		path := filepath.Join("..", "..", "examples", "branding", name)
		got := resolve(config.Branding{Logo: path})
		if got.LogoMime != "image/svg+xml" || got.LogoBase64 == "" {
			t.Errorf("%s resolved to logo %q (%d base64 bytes)", name, got.LogoMime, len(got.LogoBase64))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "<title") || strings.Contains(string(data), "aria-label") {
			t.Errorf("%s names itself; the UI's alt text must be its only name", name)
		}
	}
}

// banner_height is a closed set: a free size such as "300px" must leave the
// UI's default rather than a height the CSS has no rule for.
func TestResolveBannerHeight(t *testing.T) {
	for in, want := range map[string]string{
		"small": "small", "Medium": "medium", "LARGE": "large", "x-large": "x-large", "": "", "xx-large": "", "300px": "", "tall": "",
	} {
		if got := resolve(config.Branding{BannerHeight: in}).BannerHeight; got != want {
			t.Errorf("banner_height %q resolved to %q, want %q", in, got, want)
		}
	}
}

// link_placement is a closed set; anything else leaves the button in the
// banner, where it was before the key.
func TestResolveLinkPlacement(t *testing.T) {
	for in, want := range map[string]string{
		"banner": "banner", "App_Bar": "app_bar", "": "", "app-bar": "", "header": "",
	} {
		if got := resolve(config.Branding{LinkPlacement: in}).LinkPlacement; got != want {
			t.Errorf("link_placement %q resolved to %q, want %q", in, got, want)
		}
	}
}

// link_position is a closed set too; a free value keeps the button where it
// was before the key (auto).
func TestResolveLinkPosition(t *testing.T) {
	for in, want := range map[string]string{
		"auto": "auto", "Top_Left": "top_left", "top_right": "top_right", "bottom_left": "bottom_left", "BOTTOM_RIGHT": "bottom_right", "": "", "top-right": "", "top": "", "center": "",
	} {
		if got := resolve(config.Branding{LinkPosition: in}).LinkPosition; got != want {
			t.Errorf("link_position %q resolved to %q, want %q", in, got, want)
		}
	}
}
