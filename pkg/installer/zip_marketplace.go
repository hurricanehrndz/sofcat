package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/hurricanehrndz/sofcat/pkg/harness"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
)

// pluginsDir is the directory under a marketplace root that holds one tree
// per plugin.
const pluginsDir = "plugins"

// The generated files have no receipts: sofcat owns them outright and
// rewrites them from the receipts after every change to the marketplace and
// once per run (RegenerateAll).

// marketplaceManifest is the generated marketplace.json. Field order is the
// key order in the file.
type marketplaceManifest struct {
	Name                                string              `json:"name"`
	Owner                               any                 `json:"owner"`
	Renames                             any                 `json:"renames,omitempty"`
	AllowCrossMarketplaceDependenciesOn any                 `json:"allowCrossMarketplaceDependenciesOn,omitempty"`
	Plugins                             []marketplacePlugin `json:"plugins"`
}

type marketplacePlugin struct {
	Name           string `json:"name"`
	Source         string `json:"source"`
	Version        string `json:"version"`
	DefaultEnabled bool   `json:"defaultEnabled"`
}

// settingsDropIn is the generated settings file of a settings placement.
type settingsDropIn struct {
	ExtraKnownMarketplaces map[string]knownMarketplace `json:"extraKnownMarketplaces"`
	EnabledPlugins         map[string]bool             `json:"enabledPlugins"`
}

type knownMarketplace struct {
	Source marketplaceSource `json:"source"`
}

type marketplaceSource struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

// RegenerateAll regenerates every marketplace placement in the harness table
// with a path on this OS and returns a failed Result per placement that
// failed. A
// managed run calls it once, after scope exit, so a generated file deleted,
// refused or left stale by an earlier failure is restored even when no tree
// changes. A placement whose root does not exist is skipped: nothing was
// ever placed there, and an empty marketplace would only be clutter.
func (z *ZipInstaller) RegenerateAll() []Result {
	var failed []Result
	for _, name := range z.Harnesses.Names() {
		spec := z.Harnesses.Placements[name]
		if _, ok := spec.Path(z.Guard.GOOS); !ok || spec.Kind != harness.KindMarketplace {
			continue
		}
		root, err := z.resolve(name, spec)
		if err == nil {
			if _, statErr := os.Lstat(root); errors.Is(statErr, fs.ErrNotExist) {
				continue
			}
			err = z.regenerate(name)
		}
		if err != nil {
			failed = append(failed, fail(Target{Placement: name}, root, err))
		}
	}
	return failed
}

// regenerate rewrites the generated files of marketplace placement name from
// its receipts: every manifest file, then every settings placement that
// registers it. It does nothing for any other placement. Every write passes
// the guard; a refused path wraps placement.ErrRefused. Apply and Remove call
// it after each change, so the files are current at once, and RegenerateAll
// once per run, so a file deleted or refused on one run is restored on the
// next.
func (z *ZipInstaller) regenerate(name string) error {
	spec, ok := z.Harnesses.Placements[name]
	if !ok || spec.Kind != harness.KindMarketplace || spec.Marketplace == nil {
		return nil
	}
	root, err := z.resolve(name, spec)
	if err != nil {
		return err
	}
	all, err := receipt.List(z.ReceiptDir)
	if err != nil {
		return fmt.Errorf("placement %q: unable to read receipts: %w", name, err)
	}
	rcs := slices.DeleteFunc(all, func(rc receipt.Receipt) bool { return rc.Placement != name })

	var errs []error
	if err := z.writeManifests(name, *spec.Marketplace, root, rcs); err != nil {
		errs = append(errs, err)
	}
	for _, settings := range z.Harnesses.Names() {
		if s := z.Harnesses.Placements[settings]; s.Kind == harness.KindSettings && s.Registers == name {
			if err := z.writeSettings(settings, s, spec.Marketplace.Name, root, rcs); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// writeManifests writes the marketplace manifest, listing every plugin rcs
// records, to each of the block's manifest files under root.
func (z *ZipInstaller) writeManifests(name string, m harness.Marketplace, root string, rcs []receipt.Receipt) error {
	manifest := marketplaceManifest{
		Name:                                m.Name,
		Owner:                               jsonValue(m.Owner),
		Renames:                             jsonValue(m.Renames),
		AllowCrossMarketplaceDependenciesOn: jsonValue(m.AllowCrossMarketplaceDependenciesOn),
		Plugins:                             []marketplacePlugin{},
	}
	for _, rc := range rcs { // receipt.List sorts by item
		manifest.Plugins = append(manifest.Plugins, marketplacePlugin{
			Name:           rc.Item,
			Source:         "./" + pluginsDir + "/" + rc.Item,
			Version:        rc.Version,
			DefaultEnabled: rc.Managed,
		})
	}
	data, err := encodeJSON(manifest)
	if err != nil {
		return fmt.Errorf("placement %q: %w", name, err)
	}
	for _, rel := range m.Files() {
		local := filepath.FromSlash(rel)
		if !filepath.IsLocal(local) {
			return fmt.Errorf("%w: placement %q: manifest file %q is not a path inside the placement", placement.ErrRefused, name, rel)
		}
		path, err := z.Guard.Check(filepath.Join(root, local), harness.ScopeMachine)
		if err != nil {
			return fmt.Errorf("placement %q: %w", name, err)
		}
		if err := writeFileAtomic(path, data, 0o644); err != nil {
			return fmt.Errorf("placement %q: %w", name, err)
		}
		slog.Info("Wrote marketplace manifest", "placement", name, "path", path, "plugins", len(manifest.Plugins))
	}
	return nil
}

// writeSettings writes settings placement name's drop-in, registering the
// marketplace at root under market and enabling its managed plugins. With no
// plugin left it removes the file.
func (z *ZipInstaller) writeSettings(name string, spec harness.Placement, market, root string, rcs []receipt.Receipt) error {
	if _, ok := spec.Path(z.Guard.GOOS); !ok {
		return nil
	}
	path, err := z.resolve(name, spec)
	if err != nil {
		return err
	}
	if len(rcs) == 0 {
		if err = os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("settings placement %q: %w", name, err)
		}
		slog.Info("Removed settings drop-in: no plugin left", "placement", name, "path", path)
		return nil
	}
	dropIn := settingsDropIn{
		ExtraKnownMarketplaces: map[string]knownMarketplace{
			market: {Source: marketplaceSource{Source: "directory", Path: root}},
		},
		EnabledPlugins: map[string]bool{},
	}
	for _, rc := range rcs {
		if rc.Managed {
			dropIn.EnabledPlugins[rc.Item+"@"+market] = true
		}
	}
	data, err := encodeJSON(dropIn)
	if err != nil {
		return fmt.Errorf("settings placement %q: %w", name, err)
	}
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		return fmt.Errorf("settings placement %q: %w", name, err)
	}
	slog.Info("Wrote settings drop-in", "placement", name, "path", path, "enabled", len(dropIn.EnabledPlugins))
	return nil
}

// encodeJSON is v as indented JSON with a trailing newline. HTML characters
// stay as they are: harnesses read the file, not browsers.
func encodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// jsonValue converts an untyped YAML value for encoding/json, which cannot
// encode a map with non-string keys.
func jsonValue(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = jsonValue(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[fmt.Sprint(k)] = jsonValue(e)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = jsonValue(e)
		}
		return out
	default:
		return v
	}
}

// writeFileAtomic writes data to path with mode through a temporary file in
// the same directory, renamed into place, so a harness never reads a partial
// file. The directory is created if missing.
func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sofcat-*.tmp")
	if err != nil {
		return err
	}
	// Harnesses run as users; CreateTemp's 0600 would hide the file off
	// Windows, where no inherited ACL applies.
	err = tmp.Chmod(mode)
	if err == nil {
		_, err = tmp.Write(data)
	}
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
