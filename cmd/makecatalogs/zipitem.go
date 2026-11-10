package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/admin"
	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/render"
	"go.yaml.in/yaml/v4"
)

const zipItemUsage = `Usage: makecatalogs zip-item --placements <placements.yaml> --item <item.yaml>
       --tree <dir> --location-prefix <prefix> -o <out>

Packs <dir>, the item's files at its root, into zips under <out>/<prefix>/
for each placement <item.yaml> lists under placements:, on every OS that
placement has a path for. Prints <item.yaml> without placements: and with
installer.type zip and its variants filled in. It checks everything first
and writes nothing on failure.

Options:
`

// zipVariant is one item tree, checked and hashed, waiting to be packed.
type zipVariant struct {
	placement, goos, dir, treeHash string
}

// zipItem is an item file as readZipItem reads it: the package-info, the
// placements it lists, and its top-level mapping without placements.
type zipItem struct {
	info       admin.PackageInfo
	placements []string
	doc        *yaml.Node
}

func runZipItem(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("zip-item", zipItemUsage, stderr)
	placementsPath := flags.String("placements", "", "placements.yaml (required)")
	itemPath := flags.String("item", "", "package-info file with placements and without installer (required)")
	tree := flags.String("tree", "", "the item's tree, its files at the root (required)")
	prefix := flags.String("location-prefix", "", "repo-relative directory for the zips, such as packages/skills (required)")
	out := flags.String("o", "", "repo root to write <prefix>/*.zip under (required)")
	if _, code := parseArgs(flags, args, 0); code >= 0 {
		return code
	}
	if *placementsPath == "" || *itemPath == "" || *tree == "" || *prefix == "" || *out == "" {
		flags.Usage()
		return 2
	}

	table, err := readPlacements(*placementsPath)
	if err != nil {
		return fail(stderr, err)
	}
	item, err := readZipItem(*itemPath)
	if err != nil {
		return fail(stderr, err)
	}
	if err = checkPrefix(*prefix); err != nil {
		return fail(stderr, err)
	}
	variants, err := planZipItem(table, *placementsPath, item, *tree)
	if err != nil {
		return fail(stderr, err)
	}
	installer, err := writeZips(item.info, variants, *prefix, *out)
	if err != nil {
		return fail(stderr, err)
	}
	if err = encodeZipItem(stdout, item.doc, installer); err != nil {
		return fail(stderr, err)
	}
	return 0
}

// readPlacements parses placements.yaml. Template files sit beside it, at
// the repository root.
func readPlacements(placementsPath string) (placement.Table, error) {
	body, err := os.ReadFile(placementsPath)
	if err != nil {
		return placement.Table{}, err
	}
	table, err := placement.Parse(body, func(rel string) ([]byte, error) {
		return fs.ReadFile(os.DirFS(filepath.Dir(placementsPath)), rel)
	})
	if err != nil {
		return table, fmt.Errorf("%s: %w", placementsPath, err)
	}
	return table, nil
}

func checkPrefix(prefix string) error {
	if path.IsAbs(prefix) || filepath.IsAbs(prefix) || strings.Contains(prefix, `\`) ||
		strings.HasPrefix(path.Clean(prefix), "..") {
		return fmt.Errorf("--location-prefix %q must be a relative slash path inside the repo", prefix)
	}
	return nil
}

// encodeZipItem writes doc, an item's top-level mapping, with installer
// appended, so keys catalog.Item does not know pass through in their order.
func encodeZipItem(w io.Writer, doc *yaml.Node, installer catalog.InstallerItem) error {
	var key, value yaml.Node
	key.SetString("installer")
	if err := value.Encode(installer); err != nil {
		return err
	}
	out := *doc
	out.Content = append(slices.Clip(doc.Content), &key, &value)
	// Two-space indent, as yamlfmt writes it, so the output is committed as is.
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	return errors.Join(enc.Encode(&out), enc.Close())
}

// readZipItem reads a package-info file that zip-item may complete: it lists
// its placements, has no installer, and its item_name and version can be
// part of a file name. It also keeps the file's top-level mapping, comments
// and placements stripped, so every other author key reaches the output,
// including keys catalog.Item does not know.
func readZipItem(itemPath string) (zipItem, error) {
	var item zipItem
	body, err := os.ReadFile(itemPath)
	if err != nil {
		return item, err
	}
	var doc yaml.Node
	if err = yaml.Unmarshal(body, &doc); err != nil {
		return item, fmt.Errorf("%s: %w", itemPath, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return item, fmt.Errorf("%s: not a mapping", itemPath)
	}
	top := doc.Content[0]
	for i := 0; i < len(top.Content); i += 2 {
		switch top.Content[i].Value {
		case "installer":
			return item, fmt.Errorf("%s: already has an installer; zip-item writes it", itemPath)
		case "placements":
			if err = top.Content[i+1].Decode(&item.placements); err != nil {
				return item, fmt.Errorf("%s: placements: %w", itemPath, err)
			}
			// The output names the placements in its variants instead.
			top.Content = slices.Delete(top.Content, i, i+2)
			i -= 2
		}
	}
	if len(item.placements) == 0 {
		return item, fmt.Errorf("%s: placements must list the placements to pack the tree for", itemPath)
	}
	for i, name := range item.placements {
		if slices.Contains(item.placements[:i], name) {
			return item, fmt.Errorf("%s: placement %q is listed twice", itemPath, name)
		}
	}
	if err = top.Decode(&item.info); err != nil {
		return item, fmt.Errorf("%s: %w", itemPath, err)
	}
	for field, value := range map[string]string{"item_name": item.info.ItemName, "version": item.info.Item.Version} {
		if !safeName(value) {
			return item, fmt.Errorf("%s: %s %q must be set and must not be a path", itemPath, field, value)
		}
	}
	// The output is generated: an input comment such as "Input to zip-item"
	// would be wrong there, so comments do not carry over.
	stripComments(top)
	item.doc = top
	return item, nil
}

func stripComments(n *yaml.Node) {
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	for _, c := range n.Content {
		stripComments(c)
	}
}

// planZipItem checks, hashes and test-renders tree for each placement the
// item lists, on every OS that placement has a path for, and writes nothing.
func planZipItem(table placement.Table, placementsPath string, item zipItem, tree string) ([]zipVariant, error) {
	info := item.info
	var variants []zipVariant
	for _, name := range item.placements {
		p, ok := table.Placements[name]
		if !ok {
			return nil, fmt.Errorf("item %q: placement %q is not in %s", info.ItemName, name, placementsPath)
		}
		// placement.Parse refuses a placement with no darwin or windows path,
		// so every listed placement gets at least one variant.
		for _, goos := range []string{placement.Darwin, placement.Windows} {
			if _, ok := p.Path(goos); !ok {
				continue
			}
			t, err := artifact.Read(tree, goos)
			if err != nil {
				return nil, err
			}
			if wrappedInItemDir(t.Paths(), info.ItemName) {
				return nil, fmt.Errorf("%s: the tree is wrapped in a %s/ directory; put the item's files at its root", tree, info.ItemName)
			}
			sum, err := t.Hash()
			if err != nil {
				return nil, err
			}
			if err := checkTemplates(tree, t, name, goos, table, info, sum); err != nil {
				return nil, err
			}
			variants = append(variants, zipVariant{name, goos, tree, sum})
		}
	}
	return variants, nil
}

// checkTemplates renders the templates the tree at treeDir ships as the
// device would (admin.CheckTreeTemplates), so a template that would fail on
// every device fails the pack, naming the item, the OS and the template.
func checkTemplates(treeDir string, tree *artifact.Tree, placement, goos string, table placement.Table, info admin.PackageInfo, treeHash string) error {
	templates, err := artifact.ShippedTemplates(tree.Paths())
	if err != nil {
		return fmt.Errorf("%s: %w", treeDir, err)
	}
	sources := make(map[string]string, len(templates))
	for _, p := range templates {
		body, err := os.ReadFile(filepath.Join(treeDir, filepath.FromSlash(p)))
		if err != nil {
			return err
		}
		sources[p] = string(body)
	}
	item := render.TreeItem{Name: info.ItemName, Version: info.Item.Version, TreeHash: treeHash}
	if errs := admin.CheckTreeTemplates(table, placement, goos, item, sources); len(errs) > 0 {
		return fmt.Errorf("%s: item %q on %s: %w", treeDir, info.ItemName, goos, errors.Join(errs...))
	}
	return nil
}

// writeZips packs each distinct tree once into <out>/<prefix> and returns
// the zip installer that names them. Trees with the same tree hash share a
// zip: its bytes depend only on the paths, modes and contents the hash covers.
func writeZips(info admin.PackageInfo, variants []zipVariant, prefix, out string) (catalog.InstallerItem, error) {
	installer := catalog.InstallerItem{Type: "zip", Variants: map[string]map[string]catalog.Variant{}}
	dest := filepath.Join(out, filepath.FromSlash(prefix))
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return installer, err
	}
	packed := map[string]catalog.Variant{}
	var written []string
	for _, v := range variants {
		zip, ok := packed[v.treeHash]
		if !ok {
			name := fmt.Sprintf("%s-%s-%s.zip", info.ItemName, info.Item.Version, strings.TrimPrefix(v.treeHash, "sha256:")[:12])
			file := filepath.Join(dest, name)
			result, err := artifact.PackFile(v.dir, v.goos, file)
			if err == nil && result.TreeHash != v.treeHash {
				err = fmt.Errorf("%s changed while packing", v.dir)
			}
			if err != nil {
				for _, f := range append(written, file) {
					_ = os.Remove(f)
				}
				return installer, err
			}
			written = append(written, file)
			zip = catalog.Variant{Location: path.Join(prefix, name), Hash: result.Hash, TreeHash: result.TreeHash}
			packed[v.treeHash] = zip
		}
		if installer.Variants[v.placement] == nil {
			installer.Variants[v.placement] = map[string]catalog.Variant{}
		}
		installer.Variants[v.placement][v.goos] = zip
	}
	return installer, nil
}

// wrappedInItemDir reports whether every path sits under one top directory
// named after the item. sofcat places a tree at <placement path>/<item>, so
// such a tree would land at <item>/<item>.
func wrappedInItemDir(paths []string, item string) bool {
	for _, p := range paths {
		top, _, nested := strings.Cut(p, "/")
		if !nested || !strings.EqualFold(top, item) {
			return false
		}
	}
	return true
}

func safeName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\:`)
}
