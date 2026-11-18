// Package branding validates the organisation branding SofCat UI shows: the
// config.yaml `branding:` block with managed settings merged over it field by
// field (config.Get). Every field is validated here, in the SYSTEM service, so
// the UI only ever receives plain text, an http(s) URL, a #rrggbb colour and a
// sniffed image. A field that fails validation is dropped; it never fails the request.
package branding

import (
	"bytes"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/hurricanehrndz/sofcat/pkg/config"
)

// Caps on the admin-supplied values. Text is counted in runes.
const (
	maxTitle     = 120
	maxTagline   = 240
	maxLinkLabel = 60
	maxHeading   = 120
	maxLinkURL   = 2048
	maxSections  = 50
	MaxLogoBytes = 512 << 10
	// MaxBackgroundBytes is larger, for a photograph. The UI keeps the payload
	// in localStorage, so it cannot grow much further.
	MaxBackgroundBytes = 1 << 20
)

var accentPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Branding is the GetBranding payload. Unset or rejected fields are empty.
type Branding struct {
	Title   string `json:"title"`
	Tagline string `json:"tagline"`
	// HelpURL and HelpLabel are branding link_url and link_label (or their
	// old names, help_url and help_label). The protocol keeps the field names
	// it shipped with, so a cached payload or an older UI still shows the
	// button.
	HelpURL   string `json:"helpUrl"`
	HelpLabel string `json:"helpLabel"`
	// LinkPlacement and LinkPosition are one of LinkPlacements and
	// LinkPositions, or "" for the UI's defaults, banner and auto.
	LinkPlacement string `json:"linkPlacement"`
	LinkPosition  string `json:"linkPosition"`
	Accent        string `json:"accent"`
	LogoMime      string `json:"logoMime"`
	LogoBase64    string `json:"logoBase64"`
	// Background is the banner's background image. When set, the UI draws it
	// over the accent colour, with the banner text in BackgroundText: "light"
	// or "dark", or "" for the UI's default, light.
	BackgroundMime   string `json:"backgroundMime"`
	BackgroundBase64 string `json:"backgroundBase64"`
	BackgroundText   string `json:"backgroundText"`
	// ListHeading replaces the UI's "Available software" heading when set.
	ListHeading string `json:"listHeading"`
	// Sections is the storefront tab order; sections it omits follow it.
	Sections []string `json:"sections,omitempty"`
	// TitleSize and TaglineSize are one of FontSizes, or "" for the UI's
	// default, medium. TitleSize also scales the page headings.
	TitleSize   string `json:"titleSize"`
	TaglineSize string `json:"taglineSize"`
	// ContentWidth is one of ContentWidths, or "" for the UI's default, wide.
	ContentWidth string `json:"contentWidth"`
	// FilterPlacement is one of FilterPlacements, or "" for the UI's default,
	// right.
	FilterPlacement string `json:"filterPlacement"`
	// NavPlacement and CategoryFilter are one of NavPlacements and
	// CategoryFilters, or "" for the UI's defaults, top and select.
	NavPlacement   string `json:"navPlacement"`
	CategoryFilter string `json:"categoryFilter"`
	// AppBarBrand, BannerStyle and BannerAlign are one of AppBarBrands,
	// BannerStyles and BannerAligns, or "" for the UI's defaults: full, text
	// and left.
	AppBarBrand string `json:"appBarBrand"`
	BannerStyle string `json:"bannerStyle"`
	BannerAlign string `json:"bannerAlign"`
	// BannerHeight is one of BannerHeights, or "" for the UI's default,
	// medium.
	BannerHeight string `json:"bannerHeight"`
	// Progress is one of Progresses, or "" for the UI's default, strip.
	Progress string `json:"progress"`
	// ItemView is one of ItemViews, or "" for the UI's default, page.
	ItemView string `json:"itemView"`
}

// FontSizes are the names title_size and tagline_size accept: a small fixed
// scale rather than a number, so a typo cannot wreck the layout.
var FontSizes = []string{"small", "medium", "large", "x-large"}

// ContentWidths are the names content_width accepts: compact keeps the layout
// from before the key (a 76rem column of fixed-size cards), wide caps the
// column at 96rem and lets the cards fill the row, and full drops the cap.
var ContentWidths = []string{"compact", "wide", "full"}

// FilterPlacements are the names filter_placement accepts: right sets the
// list's search and category filters on the heading's row, at its right,
// and left keeps them above the heading, the layout from before the key.
var FilterPlacements = []string{"right", "left"}

// NavPlacements are the names nav_placement accepts: top keeps the bar with
// the product, the pages and the connection status across the window, the
// layout from before the key, and left makes it a column down its side.
var NavPlacements = []string{"top", "left"}

// CategoryFilters are the names category_filter accepts: select keeps the
// Category select beside the search field, as before the key, and list shows
// the categories as buttons in a left bar. With the bar at the top the UI
// shows the select for either.
var CategoryFilters = []string{"select", "list"}

// AppBarBrands are the names app_bar_brand accepts: what the app bar shows of
// the product, its mark and name (full), the mark only, or neither. The UI
// keeps the name for screen readers in all three.
var AppBarBrands = []string{"full", "mark", "none"}

// BannerStyles are the names banner_style accepts: text shows the logo in a
// tile beside the title, and logo shows the logo as a wordmark in the title's
// place, with the title as its text alternative.
var BannerStyles = []string{"text", "logo"}

// BannerAligns are the names banner_align accepts for the banner's content.
var BannerAligns = []string{"left", "center", "right"}

// BannerHeights are the names banner_height accepts, from a slim banner to a
// tall one; each is a minimum, so content that needs more room still grows it.
var BannerHeights = []string{"small", "medium", "large", "x-large"}

// Progresses are the names progress accepts: strip shows the working
// operation in a bar along the bottom, and button shows each item's progress
// in its own button, the strip only counting a queue of two or more.
var Progresses = []string{"strip", "button"}

// ItemViews are the names item_view accepts: page replaces the list with the
// item's detail page, the layout from before the key, and dialog opens the
// same details in a dialog over the list.
var ItemViews = []string{"page", "dialog"}

// LinkPlacements are the names link_placement accepts: the button in the
// banner, laid out by banner_align, or at the app bar's end before the
// connection status.
var LinkPlacements = []string{"banner", "app_bar"}

// LinkPositions are the names link_position accepts for the button in the
// banner: auto, beside or under the content as banner_align lays it out, or
// a corner of the banner. The app bar ignores it.
var LinkPositions = []string{"auto", "top_left", "top_right", "bottom_left", "bottom_right"}

// Resolve returns the validated branding; config.Get has already merged the
// managed settings over the config.yaml block. The logo file is read on every
// call, so a replaced logo shows up without a service restart.
func Resolve(cfg config.Branding) Branding {
	return resolve(cfg)
}

func resolve(in config.Branding) Branding {
	out := Branding{
		Title:     plainText(in.Title, maxTitle),
		Tagline:   plainText(in.Tagline, maxTagline),
		HelpURL:   linkURL(in.LinkURL),
		HelpLabel: plainText(in.LinkLabel, maxLinkLabel),

		ListHeading: plainText(in.ListHeading, maxHeading),
		Sections:    sectionList(in.Sections),
	}
	if accent := strings.TrimSpace(in.Accent); accentPattern.MatchString(accent) {
		out.Accent = strings.ToLower(accent)
	} else if accent != "" {
		slog.Debug("branding accent dropped", "accent", accent)
	}
	if path := strings.TrimSpace(in.Logo); path != "" {
		out.LogoMime, out.LogoBase64 = image("logo", path, MaxLogoBytes)
	}
	if path := strings.TrimSpace(in.BackgroundImage); path != "" {
		out.BackgroundMime, out.BackgroundBase64 = image("background_image", path, MaxBackgroundBytes)
	}
	out.BackgroundText = oneOf("background_text", in.BackgroundText, "light", "dark")
	out.TitleSize = oneOf("title_size", in.TitleSize, FontSizes...)
	out.TaglineSize = oneOf("tagline_size", in.TaglineSize, FontSizes...)
	out.ContentWidth = oneOf("content_width", in.ContentWidth, ContentWidths...)
	out.FilterPlacement = oneOf("filter_placement", in.FilterPlacement, FilterPlacements...)
	out.NavPlacement = oneOf("nav_placement", in.NavPlacement, NavPlacements...)
	out.CategoryFilter = oneOf("category_filter", in.CategoryFilter, CategoryFilters...)
	out.AppBarBrand = oneOf("app_bar_brand", in.AppBarBrand, AppBarBrands...)
	out.BannerStyle = oneOf("banner_style", in.BannerStyle, BannerStyles...)
	out.BannerAlign = oneOf("banner_align", in.BannerAlign, BannerAligns...)
	out.BannerHeight = oneOf("banner_height", in.BannerHeight, BannerHeights...)
	out.LinkPlacement = oneOf("link_placement", in.LinkPlacement, LinkPlacements...)
	out.LinkPosition = oneOf("link_position", in.LinkPosition, LinkPositions...)
	out.Progress = oneOf("progress", in.Progress, Progresses...)
	out.ItemView = oneOf("item_view", in.ItemView, ItemViews...)
	return out
}

// oneOf returns value, trimmed and lowercased, when it is one of allowed, and
// otherwise "", which leaves the UI's default.
func oneOf(key, value string, allowed ...string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || slices.Contains(allowed, value) {
		return value
	}
	slog.Debug("branding "+key+" dropped", "value", value)
	return ""
}

// plainText trims, drops control characters (including newlines) and caps
// the value at limit runes.
func plainText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if runes := []rune(value); len(runes) > limit {
		value = strings.TrimSpace(string(runes[:limit]))
	}
	return value
}

// sectionList validates each entry like a heading, drops empties and
// case-insensitive duplicates (the first spelling wins) and keeps at most
// maxSections. The order is the storefront tab order.
func sectionList(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		value = plainText(value, maxHeading)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		if len(out) == maxSections {
			slog.Debug("branding sections capped", "max", maxSections)
			break
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func linkURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || len(value) > maxLinkURL || parsed.Host == "" ||
		(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
		slog.Debug("branding link_url (or help_url) dropped", "linkUrl", value, "err", err)
		return ""
	}
	return parsed.String()
}

// image reads the local file at path, the branding key what, and returns its
// MIME type and base64 bytes, or empty strings when it is missing, larger than
// limit or not a PNG, JPEG or SVG. The UI only ever renders it through <img>
// or as a CSS background, where an SVG cannot run script.
func image(what, path string, limit int64) (string, string) {
	file, err := os.Open(path)
	if err != nil {
		slog.Debug("branding "+what+" dropped", "path", path, "err", err)
		return "", ""
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		slog.Debug("branding "+what+" dropped", "path", path, "bytes", len(data), "err", err)
		return "", ""
	}
	mime := sniffImage(data)
	if mime == "" {
		slog.Debug("branding "+what+" dropped", "path", path, "reason", "not a PNG, JPEG or SVG")
		return "", ""
	}
	return mime, base64.StdEncoding.EncodeToString(data)
}

func sniffImage(data []byte) string {
	switch detected := http.DetectContentType(data); detected {
	case "image/png", "image/jpeg":
		return detected
	}
	if isSVG(data) {
		return "image/svg+xml"
	}
	return ""
}

// isSVG accepts markup whose first element is <svg>, after an optional BOM,
// XML declaration, doctype and comments. http.DetectContentType reports SVG
// as text/xml or text/plain, so it cannot tell on its own.
func isSVG(data []byte) bool {
	rest := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	for {
		rest = bytes.TrimLeft(rest, " \t\r\n")
		switch {
		case hasPrefixFold(rest, "<svg"):
			return len(rest) > 4 && (rest[4] == ' ' || rest[4] == '>' || rest[4] == '\t' || rest[4] == '\r' || rest[4] == '\n')
		case bytes.HasPrefix(rest, []byte("<?")):
			rest = skipPast(rest, "?>")
		case bytes.HasPrefix(rest, []byte("<!--")):
			rest = skipPast(rest, "-->")
		case hasPrefixFold(rest, "<!doctype"):
			rest = skipPast(rest, ">")
		default:
			return false
		}
		if rest == nil {
			return false
		}
	}
}

func hasPrefixFold(data []byte, prefix string) bool {
	return len(data) >= len(prefix) && strings.EqualFold(string(data[:len(prefix)]), prefix)
}

func skipPast(data []byte, marker string) []byte {
	i := bytes.Index(data, []byte(marker))
	if i < 0 {
		return nil
	}
	return data[i+len(marker):]
}
