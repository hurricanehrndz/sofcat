package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const buildUsage = `Usage: makecatalogs build <source> -o <out> [--location-prefix <prefix>]

Builds a repository in <out> from <source>: placements.yaml, items/*.yaml
(item files with placements:, as zip-item reads them), item-trees/<item_name>/
(one tree per item) and the optional templates/, manifests/, conditions/ and
packages-info/, which are copied as they are. It packs each item's zips
under <out>/<prefix>/, writes <out>/packages-info/<item_name>.yaml, compiles
the catalogs and runs the -check pass. It plans every item before it touches
<out>, then wipes <out> down to an empty catalogs/ directory and rebuilds
it, so a build that fails part way can be rerun; it refuses an <out> that is
not empty and has no catalogs/ directory.

Options:
`

// copiedDirs are the source directories build copies into <out> as they are.
var copiedDirs = []string{"templates", "manifests", "conditions", "packages-info"}

// plannedItem is one item of a build, checked and hashed, waiting to be packed.
type plannedItem struct {
	item     zipItem
	variants []zipVariant
}

func runBuild(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("build", buildUsage, stderr)
	out := flags.String("o", "", "repository to build; wiped and rebuilt (required)")
	prefix := flags.String("location-prefix", "packages", "repo-relative directory for the zips")
	positional, code := parseArgs(flags, args, 1)
	if code >= 0 {
		return code
	}
	if *out == "" {
		flags.Usage()
		return 2
	}
	source := positional[0]
	if err := checkPrefix(*prefix); err != nil {
		return fail(stderr, err)
	}
	items, err := planBuild(source)
	if err != nil {
		return fail(stderr, err)
	}
	if err = prepareOut(source, *out); err != nil {
		return fail(stderr, err)
	}
	if err = writeBuild(source, *out, *prefix, items); err != nil {
		return fail(stderr, err)
	}

	// The check pass reports every problem the write pass would warn about,
	// so the write pass's warnings are shown only if it fails.
	var warnings bytes.Buffer
	if code := compileCatalogs(*out, false, stdout, &warnings); code != 0 {
		_, _ = io.Copy(stderr, &warnings)
		return code
	}
	return compileCatalogs(*out, true, io.Discard, stderr)
}

// planBuild reads placements.yaml and every item file under source and
// plans each item's tree, writing nothing.
func planBuild(source string) ([]plannedItem, error) {
	placementsPath := filepath.Join(source, "placements.yaml")
	table, err := readPlacements(placementsPath)
	if err != nil {
		return nil, err
	}
	itemPaths, err := filepath.Glob(filepath.Join(source, "items", "*.yaml"))
	if err != nil {
		return nil, err
	}
	var items []plannedItem
	byName := map[string]string{}
	for _, itemPath := range itemPaths {
		var p plannedItem
		if p.item, err = readZipItem(itemPath); err != nil {
			return nil, err
		}
		item := p.item
		name := item.info.ItemName
		// items/x.yaml, item_name x and item-trees/x/ name one item, and the
		// file name keeps item names unique.
		if base := strings.TrimSuffix(filepath.Base(itemPath), ".yaml"); name != base {
			return nil, fmt.Errorf("%s: item_name %q does not match the file name %q", itemPath, name, base)
		}
		byName[name] = itemPath
		// A hand-written package-info for the item would compete with the
		// generated one in the same file.
		handWritten := filepath.Join(source, "packages-info", name+".yaml")
		if _, statErr := os.Lstat(handWritten); statErr == nil {
			return nil, fmt.Errorf("%s: %s would be generated over it", handWritten, itemPath)
		}
		if p.variants, err = planZipItem(table, placementsPath, item, filepath.Join(source, "item-trees", name)); err != nil {
			return nil, err
		}
		items = append(items, p)
	}

	// A tree no item names would silently never ship.
	trees, err := os.ReadDir(filepath.Join(source, "item-trees"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range trees {
		if _, ok := byName[e.Name()]; !ok && !strings.HasPrefix(e.Name(), ".") {
			return nil, fmt.Errorf("%s: no item in %s has item_name %q", filepath.Join(source, "item-trees", e.Name()),
				filepath.Join(source, "items"), e.Name())
		}
	}
	return items, nil
}

// prepareOut empties out for a rebuild and leaves an empty catalogs/ in it,
// the mark of a build, so a build that fails after this can be rerun. It
// refuses an out that is source or holds it, and a non-empty out with no
// catalogs/, which does not look like a previous build and might be anything.
func prepareOut(source, out string) error {
	src, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	dst, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if rel, relErr := filepath.Rel(dst, src); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("-o %s: the output would replace the source %s", out, source)
	}
	catalogs := filepath.Join(dst, "catalogs")
	entries, err := os.ReadDir(dst)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(catalogs, 0o755)
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		if fi, err := os.Stat(catalogs); err != nil || !fi.IsDir() {
			return fmt.Errorf("-o %s: not empty and has no catalogs/ directory, so it does not look like a previous build; refusing to wipe it", out)
		}
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return os.Mkdir(catalogs, 0o755)
}

// writeBuild copies the source directories into out, then packs each item
// and writes its package-info.
func writeBuild(source, out, prefix string, items []plannedItem) error {
	if err := copyFile(filepath.Join(source, "placements.yaml"), filepath.Join(out, "placements.yaml")); err != nil {
		return err
	}
	for _, dir := range copiedDirs {
		from := filepath.Join(source, dir)
		if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
			continue
		}
		// CopyFS keeps executable bits, which condition scripts need.
		if err := os.CopyFS(filepath.Join(out, dir), os.DirFS(from)); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(out, "packages-info"), 0o755); err != nil {
		return err
	}
	for _, p := range items {
		installer, err := writeZips(p.item.info, p.variants, prefix, out)
		if err != nil {
			return err
		}
		var info bytes.Buffer
		if err = encodeZipItem(&info, p.item.doc, installer); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(out, "packages-info", p.item.info.ItemName+".yaml"), info.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string) error {
	body, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, body, 0o644) //nolint:gosec // G703: to is under the -o the operator chose
}
