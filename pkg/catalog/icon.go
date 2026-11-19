package catalog

import (
	"io/fs"
	"net/http"
	"strings"
)

// IconsDir is the repository directory an item's icon_name is relative to.
const IconsDir = "icons"

// MaxIconBytes caps an item icon. The storefront holds every listed item's
// icon in memory, so it stays well under a branding background.
const MaxIconBytes = 512 << 10

// ValidIconName reports whether name is a slash-separated path that stays
// under icons/ on every OS: no "..", no leading slash, no backslash or colon.
func ValidIconName(name string) bool {
	return fs.ValidPath(name) && name != "." && !strings.ContainsAny(name, `\:`)
}

// IconType is the MIME type of an icon the storefront shows, image/png or
// image/jpeg, sniffed from data, or "" for anything else.
func IconType(data []byte) string {
	switch detected := http.DetectContentType(data); detected {
	case "image/png", "image/jpeg":
		return detected
	}
	return ""
}
