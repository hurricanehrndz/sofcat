package catalog

import "testing"

// An icon name becomes a URL path and a cache file path, so it must not
// leave icons/ on any OS.
func TestValidIconName(t *testing.T) {
	for name, want := range map[string]bool{
		"firefox.png":       true,
		"browsers/edge.png": true,
		"":                  false,
		".":                 false,
		"../secret.png":     false,
		"a/../../b.png":     false,
		"/etc/passwd":       false,
		`..\b.png`:          false,
		"C:evil.png":        false,
	} {
		if got := ValidIconName(name); got != want {
			t.Errorf("ValidIconName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestIconType(t *testing.T) {
	if got := IconType([]byte("\x89PNG\r\n\x1a\n....")); got != "image/png" {
		t.Errorf("PNG sniffed as %q", got)
	}
	if got := IconType([]byte("\xff\xd8\xff\xe0....")); got != "image/jpeg" {
		t.Errorf("JPEG sniffed as %q", got)
	}
	for _, data := range []string{"", `<svg xmlns="http://www.w3.org/2000/svg"/>`, "<html>", "GIF89a...."} {
		if got := IconType([]byte(data)); got != "" {
			t.Errorf("IconType(%q) = %q, want none", data, got)
		}
	}
}
