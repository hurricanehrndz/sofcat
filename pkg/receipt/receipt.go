// Package receipt reads and writes the records of the trees sofcat placed
// for zip items: one JSON file per tree at <dir>/<placement>/<item>.json.
// The service keeps its receipts under <data dir>/receipts; other callers,
// such as a per-user helper, pass their own directory.
package receipt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Receipt records one placed tree.
type Receipt struct {
	Item      string `json:"item"`
	Version   string `json:"version"`
	Placement string `json:"placement"`
	// Path is the tree's directory: <placement root>/<item>, or
	// <placement root>/plugins/<item> in a marketplace.
	Path string `json:"path"`
	// TreeHash is the catalog's hash of the archive's contents.
	TreeHash string `json:"tree_hash"`
	// PlacedHash is the hash of the tree as sofcat wrote it, rendered files
	// included: TreeHash when nothing was rendered. Read fills it from
	// TreeHash for a receipt written before it existed.
	PlacedHash string `json:"placed_hash"`
	// Rendered records each file sofcat rendered inside the tree, in path
	// order. Write saves it as [] when there is none, never omits it.
	Rendered []Rendered `json:"rendered"`
	// Managed is true when a manifest lists the item in managed_installs.
	Managed        bool      `json:"managed"`
	InstalledAtUTC time.Time `json:"installed_at_utc"`
}

// Rendered records one file sofcat rendered inside a tree from a template
// the tree ships.
type Rendered struct {
	// File and Template are slash-separated paths relative to the tree.
	File     string `json:"file"`
	Template string `json:"template"`
	// Inputs is what the render used: os, scope, user, item (name, version,
	// managed) and the facts the template read. It is kept as written, so a
	// number compares as written when the next run asks whether the inputs
	// changed.
	Inputs json.RawMessage `json:"inputs"`
}

// MachineDir is the service's receipt directory inside its data directory.
func MachineDir(dataDir string) string {
	return filepath.Join(dataDir, "receipts")
}

// file is the receipt's path. Placement and item names come from the
// repository, so they must be single path segments.
func file(dir, placement, item string) (string, error) {
	for _, name := range []string{placement, item} {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			return "", fmt.Errorf("invalid receipt name %q", name)
		}
	}
	return filepath.Join(dir, placement, item+".json"), nil
}

// Read returns the receipt for item in placement. ok is false, with a nil
// error, when there is none.
func Read(dir, placement, item string) (r Receipt, ok bool, err error) {
	path, err := file(dir, placement, item)
	if err != nil {
		return Receipt{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return Receipt{}, false, fmt.Errorf("%s: %w", path, err)
	}
	// A receipt from before rendering existed records a tree placed as
	// shipped.
	if r.PlacedHash == "" {
		r.PlacedHash = r.TreeHash
	}
	if r.Rendered == nil {
		r.Rendered = []Rendered{}
	}
	return r, true, nil
}

// Write saves r, replacing any receipt for the same placement and item. It
// writes a temporary file beside the receipt and renames it into place, so a
// reader never sees a partial receipt.
func Write(dir string, r Receipt) error {
	path, err := file(dir, r.Placement, r.Item)
	if err != nil {
		return err
	}
	if r.Rendered == nil {
		r.Rendered = []Rendered{}
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".receipt-*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(data, '\n'))
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// Delete removes the receipt for item in placement. A missing receipt is not
// an error.
func Delete(dir, placement, item string) error {
	path, err := file(dir, placement, item)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// List returns every receipt under dir, sorted by placement and then item. A
// missing dir has none. A receipt that cannot be read fails the whole list:
// a caller deciding what to remove must not act on a partial view.
func List(dir string) ([]Receipt, error) {
	placements, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Receipt
	for _, p := range placements {
		if !p.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, p.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			item, isJSON := strings.CutSuffix(e.Name(), ".json")
			if !isJSON || !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			r, ok, err := Read(dir, p.Name(), item)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, r)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Placement != out[j].Placement {
			return out[i].Placement < out[j].Placement
		}
		return out[i].Item < out[j].Item
	})
	return out, nil
}

// ForItem returns item's receipts under dir, in List order.
func ForItem(dir, item string) ([]Receipt, error) {
	all, err := List(dir)
	if err != nil {
		return nil, err
	}
	var out []Receipt
	for _, r := range all {
		if r.Item == item {
			out = append(out, r)
		}
	}
	return out, nil
}
