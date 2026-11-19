package service

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

const (
	testPNG  = "\x89PNG\r\n\x1a\n-one"
	testPNG2 = "\x89PNG\r\n\x1a\n-two"
)

// stubIconGet serves body for every icon URL and records the URLs asked for.
func stubIconGet(t *testing.T, body *string, fail *error) *[]string {
	t.Helper()
	var urls []string
	orig := iconGet
	t.Cleanup(func() { iconGet = orig })
	iconGet = func(url string) ([]byte, error) {
		urls = append(urls, url)
		if *fail != nil {
			return nil, *fail
		}
		return []byte(*body), nil
	}
	return &urls
}

// The icon cache is what the storefront shows: it must follow icon_hash,
// refuse anything that is not a small PNG or JPEG, and never trade a good
// copy for a failed fetch.
func TestCacheIcon(t *testing.T) {
	cfg := config.Configuration{AppDataPath: t.TempDir(), URL: "https://repo.example/"}
	body := testPNG
	var fail error
	urls := stubIconGet(t, &body, &fail)
	cached := filepath.Join(cfg.AppDataPath, "icons", "sub", "tool.png")
	item := catalog.Item{IconName: "sub/tool.png", IconHash: iconSum([]byte(testPNG))}

	if got := cacheIcon(cfg, item); got != item.IconHash {
		t.Fatalf("first fetch = %q, want %q", got, item.IconHash)
	}
	if len(*urls) != 1 || (*urls)[0] != "https://repo.example/icons/sub/tool.png" {
		t.Fatalf("fetched %v", *urls)
	}
	if got := cacheIcon(cfg, item); got != item.IconHash || len(*urls) != 1 {
		t.Fatalf("a cached icon matching icon_hash was fetched again: %q, %v", got, *urls)
	}

	// A new icon_hash is a new icon.
	body = testPNG2
	item.IconHash = iconSum([]byte(testPNG2))
	if got := cacheIcon(cfg, item); got != item.IconHash || len(*urls) != 2 {
		t.Fatalf("changed icon_hash = %q after %d fetches", got, len(*urls))
	}

	// What the repository serves is not what icon_hash names, is not an
	// image, is too large, or cannot be fetched: the cached copy stays.
	for name, tc := range map[string]struct {
		body string
		err  error
	}{
		"wrong hash":   {body: testPNG},
		"not an image": {body: "<svg/>"},
		"too large":    {body: testPNG + string(make([]byte, catalog.MaxIconBytes))},
		"fetch failed": {err: errors.New("offline")},
	} {
		body, fail = tc.body, tc.err
		stale := item
		stale.IconHash = "0000"
		if got := cacheIcon(cfg, stale); got != item.IconHash {
			t.Errorf("%s: cacheIcon = %q, want the cached copy %q", name, got, item.IconHash)
		}
		if data, err := os.ReadFile(cached); err != nil || string(data) != testPNG2 {
			t.Errorf("%s: cached icon replaced: %q, %v", name, data, err)
		}
	}
	fail = nil

	// Without an icon_hash a cached copy is served until it is a day old.
	body = testPNG
	item.IconHash = ""
	before := len(*urls)
	if got := cacheIcon(cfg, item); got != iconSum([]byte(testPNG2)) || len(*urls) != before {
		t.Fatalf("fresh icon without a hash was fetched again: %q", got)
	}
	old := time.Now().Add(-2 * iconMaxAge)
	if err := os.Chtimes(cached, old, old); err != nil {
		t.Fatal(err)
	}
	if got := cacheIcon(cfg, item); got != iconSum([]byte(testPNG)) || len(*urls) != before+1 {
		t.Fatalf("day-old icon without a hash was not refreshed: %q", got)
	}

	// A name that leaves icons/ is never fetched or stored.
	before = len(*urls)
	for _, name := range []string{"../config.yaml", "/etc/passwd", `..\x.png`, " "} {
		if got := cacheIcon(cfg, catalog.Item{IconName: name}); got != "" || len(*urls) != before {
			t.Errorf("cacheIcon(%q) = %q after %d fetches", name, got, len(*urls)-before)
		}
	}
}

// getItemIcon answers a standard user from the cache alone, while a run
// holds the queue, and hands out nothing but a cached image.
func TestGetItemIcon(t *testing.T) {
	cfg := testServiceConfig(t)
	release := make(chan struct{})
	_, ctx := startTestRunner(t, cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		<-release
		return nil, nil
	})
	defer close(release)
	if err := writeIcon(filepath.Join(cfg.AppDataPath, "icons", "tool.png"), []byte(testPNG)); err != nil {
		t.Fatal(err)
	}
	// Not an image, and a file outside the cache.
	if err := os.WriteFile(filepath.Join(cfg.AppDataPath, "icons", "notes.png"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.AppDataPath, "outside.png"), []byte(testPNG), 0o600); err != nil {
		t.Fatal(err)
	}

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := NewClient(cfg.ServicePipeName)
	got, err := client.GetItemIcon(callCtx, "tool.png")
	if err != nil {
		t.Fatalf("GetItemIcon failed: %v", err)
	}
	if want := (ItemIcon{Mime: "image/png", Base64: base64.StdEncoding.EncodeToString([]byte(testPNG))}); got != want {
		t.Fatalf("GetItemIcon = %#v, want %#v", got, want)
	}
	for _, name := range []string{"none.png", "notes.png", "../outside.png"} {
		if got, err := client.GetItemIcon(callCtx, name); err != nil || got != (ItemIcon{}) {
			t.Errorf("GetItemIcon(%q) = %#v, %v, want the empty icon", name, got, err)
		}
	}
}
