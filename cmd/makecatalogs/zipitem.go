package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/admin"
	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/render"
	"go.yaml.in/yaml/v4"
)

const zipItemUsage = `Usage: makecatalogs zip-item --placements <placements.yaml> --item <item.yaml>
       --trees <dir> --location-prefix <prefix> -o <out>

Packs <dir>/<placement>/<darwin|windows>/ trees into zips under
<out>/<prefix>/ and prints <item.yaml> with installer.type zip and its
variants filled in. It checks everything first and writes nothing on failure.

Options:
`

// zipVariant is one item tree, checked and hashed, waiting to be packed.
type zipVariant struct {
	placement, goos, dir, treeHash string
}

func runZipItem(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("zip-item", zipItemUsage, stderr)
	placementsPath := flags.String("placements", "", "placements.yaml (required)")
	itemPath := flags.String("item", "", "package-info file without installer (required)")
	trees := flags.String("trees", "", "item trees, laid out <placement>/<os>/ (required)")
	prefix := flags.String("location-prefix", "", "repo-relative directory for the zips, such as packages/skills (required)")
	out := flags.String("o", "", "repo root to write <prefix>/*.zip under (required)")
	if _, code := parseArgs(flags, args, 0); code >= 0 {
		return code
	}
	if *placementsPath == "" || *itemPath == "" || *trees == "" || *prefix == "" || *out == "" {
		flags.Usage()
		return 2
	}

	info, doc, variants, err := planZipItem(*placementsPath, *itemPath, *trees, *prefix)
	if err != nil {
		return fail(stderr, err)
	}
	installer, err := writeZips(info, variants, *prefix, *out)
	if err != nil {
		return fail(stderr, err)
	}
	// Append installer to the author's mapping, so keys catalog.Item does not
	// know pass through in their order.
	var key, value yaml.Node
	key.SetString("installer")
	if err = value.Encode(installer); err != nil {
		return fail(stderr, err)
	}
	doc.Content = append(doc.Content, &key, &value)
	// Two-space indent, as yamlfmt writes it, so the output is committed as is.
	enc := yaml.NewEncoder(stdout)
	enc.SetIndent(2)
	if err = errors.Join(enc.Encode(doc), enc.Close()); err != nil {
		return fail(stderr, err)
	}
	return 0
}

// planZipItem checks the inputs and hashes every item tree without
// writing anything. It returns the item file's top-level mapping for
// runZipItem to complete.
func planZipItem(placementsPath, itemPath, trees, prefix string) (admin.PackageInfo, *yaml.Node, []zipVariant, error) {
	body, err := os.ReadFile(placementsPath)
	if err != nil {
		return admin.PackageInfo{}, nil, nil, err
	}
	// Template files sit beside placements.yaml, at the repository root.
	table, err := placement.Parse(body, func(rel string) ([]byte, error) {
		return fs.ReadFile(os.DirFS(filepath.Dir(placementsPath)), rel)
	})
	if err != nil {
		return admin.PackageInfo{}, nil, nil, fmt.Errorf("%s: %w", placementsPath, err)
	}
	info, doc, err := readZipItem(itemPath)
	if err != nil {
		return info, nil, nil, err
	}
	if path.IsAbs(prefix) || filepath.IsAbs(prefix) || strings.Contains(prefix, `\`) ||
		strings.HasPrefix(path.Clean(prefix), "..") {
		return info, nil, nil, fmt.Errorf("--location-prefix %q must be a relative slash path inside the repo", prefix)
	}

	placements, err := subdirs(trees)
	if err != nil {
		return info, nil, nil, err
	}
	if len(placements) == 0 {
		return info, nil, nil, fmt.Errorf("%s: no placement directories", trees)
	}
	var variants []zipVariant
	for _, placement := range placements {
		if _, ok := table.Placements[placement]; !ok {
			return info, nil, nil, fmt.Errorf("%s: placement %q is not in %s", trees, placement, placementsPath)
		}
		found, err := planPlacement(filepath.Join(trees, placement), placement, table, info)
		if err != nil {
			return info, nil, nil, err
		}
		variants = append(variants, found...)
	}
	return info, doc, variants, nil
}

// readZipItem reads a package-info file that zip-item may complete: it has
// no installer, and its item_name and version can be part of a file name.
// It also returns the file's top-level mapping, comments stripped, so every
// author key reaches the output, including keys catalog.Item does not know.
func readZipItem(itemPath string) (admin.PackageInfo, *yaml.Node, error) {
	var info admin.PackageInfo
	body, err := os.ReadFile(itemPath)
	if err != nil {
		return info, nil, err
	}
	var doc yaml.Node
	if err = yaml.Unmarshal(body, &doc); err != nil {
		return info, nil, fmt.Errorf("%s: %w", itemPath, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return info, nil, fmt.Errorf("%s: not a mapping", itemPath)
	}
	top := doc.Content[0]
	for i := 0; i < len(top.Content); i += 2 {
		if top.Content[i].Value == "installer" {
			return info, nil, fmt.Errorf("%s: already has an installer; zip-item writes it", itemPath)
		}
	}
	if err = top.Decode(&info); err != nil {
		return info, nil, fmt.Errorf("%s: %w", itemPath, err)
	}
	for field, value := range map[string]string{"item_name": info.ItemName, "version": info.Item.Version} {
		if !safeName(value) {
			return info, nil, fmt.Errorf("%s: %s %q must be set and must not be a path", itemPath, field, value)
		}
	}
	// The output is generated: an input comment such as "Input to zip-item"
	// would be wrong there, so comments do not carry over.
	stripComments(top)
	return info, top, nil
}

func stripComments(n *yaml.Node) {
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	for _, c := range n.Content {
		stripComments(c)
	}
}

// planPlacement checks, hashes and test-renders the <os>/ trees under dir,
// one placement's item trees for info's item.
func planPlacement(dir, name string, table placement.Table, info admin.PackageInfo) ([]zipVariant, error) {
	osDirs, err := subdirs(dir)
	if err != nil {
		return nil, err
	}
	if len(osDirs) == 0 {
		return nil, fmt.Errorf("%s: no darwin or windows directory", dir)
	}
	p := table.Placements[name]
	var variants []zipVariant
	for _, goos := range osDirs {
		treeDir := filepath.Join(dir, goos)
		if goos != placement.Darwin && goos != placement.Windows {
			return nil, fmt.Errorf("%s: OS directory must be darwin or windows", treeDir)
		}
		if _, ok := p.Path(goos); !ok {
			return nil, fmt.Errorf("%s: placement %q has no %s path", treeDir, name, goos)
		}
		tree, err := artifact.Read(treeDir, goos)
		if err != nil {
			return nil, err
		}
		if wrappedInItemDir(tree.Paths(), info.ItemName) {
			return nil, fmt.Errorf("%s: the tree is wrapped in a %s/ directory; put the item's files at its root", treeDir, info.ItemName)
		}
		sum, err := tree.Hash()
		if err != nil {
			return nil, err
		}
		if err := checkTemplates(treeDir, tree, name, goos, table, info, sum); err != nil {
			return nil, err
		}
		variants = append(variants, zipVariant{name, goos, treeDir, sum})
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

// subdirs lists the directories in dir, sorted, skipping dotfiles such as
// .DS_Store. Any other file is an error: the layout has no place for it.
func subdirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.IsDir() {
			return nil, errors.New(filepath.Join(dir, e.Name()) + ": not a directory")
		}
		names = append(names, e.Name())
	}
	return names, nil
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
