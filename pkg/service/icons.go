package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/download"
)

// iconMaxAge is how long a cached icon whose item has no icon_hash is served
// before the next list call fetches it again.
const iconMaxAge = 24 * time.Hour

// iconGet fetches an icon from the repository; tests swap it.
var iconGet = download.Get

// ItemIcon is getItemIcon's result: an item's icon as the service cached it.
// Both fields are empty when the service holds no icon by that name.
type ItemIcon struct {
	Mime   string `json:"mime"`
	Base64 string `json:"base64"`
}

// iconFile is where the icon named name is cached, under the data
// directory's icons/, or false for a name that would leave it.
func iconFile(cfg config.Configuration, name string) (string, bool) {
	if !catalog.ValidIconName(name) {
		return "", false
	}
	rel := filepath.FromSlash(name)
	// Windows device names (NUL, COM1) pass ValidIconName.
	if !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.Join(cfg.AppDataPath, catalog.IconsDir, rel), true
}

func iconSum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// cacheIcon makes sure item's icon is in the icon cache and returns the
// sha256 of the cached file, or "" when there is none. It fetches
// <url>/icons/<icon_name> when the cached file is missing, is not the one
// icon_hash names or, without an icon_hash, is older than iconMaxAge. Only a
// PNG or JPEG within catalog.MaxIconBytes is stored, and a fetch that fails
// keeps the copy already there. Deliberately simple: an icon no item names
// any more stays in the cache until the data directory is cleared; sweep the
// directory here if that ever adds up.
func cacheIcon(cfg config.Configuration, item catalog.Item) string {
	name := strings.TrimSpace(item.IconName)
	if name == "" {
		return ""
	}
	path, ok := iconFile(cfg, name)
	if !ok {
		slog.Debug("icon dropped", "icon", name, "reason", "not a path under icons/")
		return ""
	}
	want := strings.ToLower(strings.TrimSpace(item.IconHash))
	have := ""
	if info, err := os.Stat(path); err == nil {
		if data, readErr := os.ReadFile(path); readErr == nil {
			have = iconSum(data)
			if have == want || (want == "" && time.Since(info.ModTime()) < iconMaxAge) {
				return have
			}
		}
	}

	url := cfg.URL + catalog.IconsDir + "/" + name
	// download.Get reads the whole body: the repository is the admin's, as
	// for catalogs, and the size is checked before anything is stored.
	data, err := iconGet(url)
	reason := ""
	switch {
	case err != nil:
		reason = err.Error()
	case len(data) == 0 || len(data) > catalog.MaxIconBytes:
		reason = "empty or over the size limit"
	case catalog.IconType(data) == "":
		reason = "not a PNG or JPEG"
	case want != "" && iconSum(data) != want:
		reason = "does not match icon_hash"
	default:
		if err := writeIcon(path, data); err != nil {
			reason = err.Error()
		}
	}
	if reason != "" {
		slog.Debug("icon not fetched", "url", url, "reason", reason)
		return have
	}
	return iconSum(data)
}

// writeIcon replaces path through a rename, so getItemIcon, which does not
// wait for the command queue, never reads half a file.
func writeIcon(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".icon-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// readIcon is getItemIcon: the cached icon named name, or the empty ItemIcon
// when there is none. It reads only the cache, never the network.
func readIcon(cfg config.Configuration, name string) ItemIcon {
	path, ok := iconFile(cfg, strings.TrimSpace(name))
	if !ok {
		return ItemIcon{}
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > catalog.MaxIconBytes {
		return ItemIcon{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ItemIcon{}
	}
	mime := catalog.IconType(data)
	if mime == "" {
		return ItemIcon{}
	}
	return ItemIcon{Mime: mime, Base64: base64.StdEncoding.EncodeToString(data)}
}
