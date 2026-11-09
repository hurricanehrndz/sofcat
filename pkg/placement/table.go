package placement

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// Scopes. An OS without a scope is ScopeMachine.
const (
	ScopeMachine = "machine"
	ScopeUser    = "user"
)

// OS keys of Spec.Paths and Spec.Scope.
const (
	Darwin  = "darwin"
	Windows = "windows"
)

// Table is a parsed placements.yaml.
type Table struct {
	Placements map[string]Spec `yaml:"placements"`
	// CheckFacts are the facts makecatalogs -check renders templates with,
	// over the built-in samples (conditions.SampleFacts): the admin facts
	// the templates read.
	CheckFacts map[string]any `yaml:"check_facts,omitempty"`
	// Order holds the placement names in file order. Parse fills it.
	Order []string `yaml:"-"`
}

// Spec is a named destination for zip items: a directory that holds their
// trees and, optionally, files rendered from the trees it holds.
type Spec struct {
	// Paths maps an OS (darwin, windows) to the placement root on it.
	Paths map[string]string `yaml:"paths,omitempty" json:"paths,omitempty"`
	// Scope maps an OS to machine or user. An OS left out is machine.
	Scope map[string]string `yaml:"scope,omitempty" json:"scope,omitempty"`
	// AppliesTo is display text for the storefront, never scoping.
	AppliesTo    []string          `yaml:"applies_to,omitempty" json:"applies_to,omitempty"`
	ActionLabels map[string]string `yaml:"action_labels,omitempty" json:"action_labels,omitempty"`
	Exclusive    bool              `yaml:"exclusive,omitempty" json:"exclusive,omitempty"`
	// ItemsDir puts item trees in <root>/<ItemsDir>/<item> rather than
	// <root>/<item>.
	ItemsDir string `yaml:"items_dir,omitempty" json:"items_dir,omitempty"`
	// Vars is free data for the placement's templates (.Placement.Vars).
	Vars map[string]any `yaml:"vars,omitempty" json:"vars,omitempty"`
	// Files are rendered from the placement's receipts after every change
	// to it.
	Files []File `yaml:"files,omitempty" json:"files,omitempty"`
	// FilesAtomic writes none of Files unless every one renders and passes
	// its checks. Without it, each is written on its own.
	FilesAtomic bool `yaml:"files_atomic,omitempty" json:"files_atomic,omitempty"`
}

// File is a file a placement renders (render.FileContext): at Path,
// relative to the placement root, or at Paths, one absolute path per OS.
// An empty render removes the file.
type File struct {
	Path     string            `yaml:"path,omitempty" json:"path,omitempty"`
	Paths    map[string]string `yaml:"paths,omitempty" json:"paths,omitempty"`
	Template string            `yaml:"template" json:"template"`
}

// View describes placement name, rooted at root, to a template.
func (p Spec) View(name, root string) render.Placement {
	v := render.Placement{Name: name, Root: root}
	// Nil without vars, so tree inputs (installer.treeInputs) leave it out.
	if len(p.Vars) > 0 {
		v.Vars = integral(render.JSONValue(p.Vars))
	}
	return v
}

// integral turns every whole float64 in a JSON value into an int, in place,
// so vars read the same from placements.yaml and from the user plan's JSON,
// which decodes every number as float64: the service, the helper and
// makecatalogs -check render alike. YAML 3.0 is therefore 3. A float beyond
// 2^53 is left alone.
func integral(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, e := range v {
			v[k] = integral(e)
		}
	case []any:
		for i, e := range v {
			v[i] = integral(e)
		}
	case float64:
		if v == math.Trunc(v) && math.Abs(v) <= 1<<53 {
			return int(v)
		}
	}
	return v
}

// Path returns the placement root for goos and whether it has one.
func (p Spec) Path(goos string) (string, bool) {
	path, ok := p.Paths[goos]
	return path, ok && path != ""
}

// ScopeFor returns the placement's scope on goos.
func (p Spec) ScopeFor(goos string) string {
	if scope := p.Scope[goos]; scope != "" {
		return scope
	}
	return ScopeMachine
}

// Names returns the placement names in file order. This is the table's
// order wherever an order matters, such as label fallback.
func (t Table) Names() []string {
	return slices.Clone(t.Order)
}

// Storefront returns what the storefront shows for item on goos: its action
// labels and the names on its applies_to line. A label comes from the
// item's custom_action_labels, else from the first of its placements with a
// variant on goos, in table order, that sets it; a key set nowhere is left
// out, for the UI's built-in label. appliesTo is the union of those
// placements' applies_to, in table order. Only a zip item has placements.
func (t Table) Storefront(item catalog.Item, goos string) (labels map[string]string, appliesTo []string) {
	var placements []Spec
	if item.Installer.Type == "zip" {
		for _, name := range t.Names() {
			if _, ok := item.Installer.Variants[name][goos]; ok {
				placements = append(placements, t.Placements[name])
			}
		}
	}
	for _, key := range catalog.ActionLabelKeys {
		label := item.CustomActionLabels[key]
		for i := 0; strings.TrimSpace(label) == "" && i < len(placements); i++ {
			label = placements[i].ActionLabels[key]
		}
		if strings.TrimSpace(label) != "" {
			if labels == nil {
				labels = map[string]string{}
			}
			labels[key] = label
		}
	}
	for _, p := range placements {
		for _, name := range p.AppliesTo {
			if !slices.Contains(appliesTo, name) {
				appliesTo = append(appliesTo, name)
			}
		}
	}
	return labels, appliesTo
}

// Parse decodes placements.yaml, rejecting unknown fields, and validates it.
// An empty file is an empty table.
func Parse(data []byte) (Table, error) {
	var t Table
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&t); err != nil && !errors.Is(err, io.EOF) {
		return Table{}, fmt.Errorf("parse placements.yaml: %w", err)
	}
	order, err := placementOrder(data)
	if err != nil {
		return Table{}, fmt.Errorf("parse placements.yaml: %w", err)
	}
	t.Order = order
	if err := t.Validate(); err != nil {
		return Table{}, err
	}
	return t, nil
}

// placementOrder returns the keys of the placements mapping in file order.
// The strict decode already rejects a duplicate key; the check here keeps
// Order from ever disagreeing with Placements.
func placementOrder(data []byte) ([]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	top := doc.Content[0].Content
	for i := 0; i+1 < len(top); i += 2 {
		if top[i].Value != "placements" || top[i+1].Kind != yaml.MappingNode {
			continue
		}
		var order []string
		seen := map[string]bool{}
		placements := top[i+1].Content
		for j := 0; j+1 < len(placements); j += 2 {
			name := placements[j].Value
			if seen[name] {
				return nil, fmt.Errorf("line %d: placement %q defined twice", placements[j].Line, name)
			}
			seen[name] = true
			order = append(order, name)
		}
		return order, nil
	}
	return nil, nil
}

// Validate reports every problem in the table, one error per problem.
func (t Table) Validate() error {
	var errs []error
	for _, name := range t.Names() {
		for _, err := range validatePlacement(name, t.Placements[name]) {
			errs = append(errs, fmt.Errorf("placement %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func validatePlacement(name string, p Spec) []error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	// The name becomes a directory name in rendered trees and receipts.
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\:`) {
		fail("invalid name")
	}
	if len(p.Paths) == 0 {
		fail("no paths")
	}
	errs = append(errs, validatePaths("paths", p.Paths, p)...)
	for goos, scope := range p.Scope {
		if goos != Darwin && goos != Windows {
			fail("scope: unknown OS %q", goos)
		}
		if scope != ScopeMachine && scope != ScopeUser {
			fail("scope: %s is %q, want machine or user", goos, scope)
		}
	}
	for key := range p.ActionLabels {
		if !slices.Contains(catalog.ActionLabelKeys, key) {
			fail("action_labels: unknown key %q", key)
		}
	}
	if p.ItemsDir != "" && (!isCleanRelative(p.ItemsDir) || strings.HasPrefix(p.ItemsDir, ".")) {
		fail("items_dir: %q is not a clean relative path whose first segment does not start with .", p.ItemsDir)
	}
	if p.FilesAtomic && len(p.Files) == 0 {
		fail("files_atomic: set without files")
	}
	return append(errs, validateFiles(p)...)
}

// validatePaths checks an OS-to-path map: the placement's paths, or a
// files entry's.
func validatePaths(field string, paths map[string]string, p Spec) []error {
	var errs []error
	for goos, path := range paths {
		if goos != Darwin && goos != Windows {
			errs = append(errs, fmt.Errorf("%s: unknown OS %q", field, goos))
		}
		if path == "" {
			errs = append(errs, fmt.Errorf("%s: empty path for %s", field, goos))
		}
		if v := "${" + ProfileVar(goos) + "}"; strings.Contains(path, v) && p.ScopeFor(goos) != ScopeUser {
			errs = append(errs, fmt.Errorf("%s: %s in %s path, which is not user scope", field, v, goos))
		}
	}
	return errs
}

// validateFiles checks the files entries: a template that parses, and
// either a relative path that cannot land in an item tree or per-OS paths
// on the placement's OSes.
func validateFiles(p Spec) []error {
	var errs []error
	seen := map[string]bool{}
	for i, f := range p.Files {
		field := fmt.Sprintf("files[%d]", i)
		fail := func(format string, args ...any) {
			errs = append(errs, fmt.Errorf(field+": "+format, args...))
		}
		switch {
		case f.Path != "" && f.Paths != nil:
			fail("set path or paths, not both")
		case f.Path != "":
			if err := p.CheckFile(f.Path); err != nil {
				fail("%w", err)
			}
			if seen[f.Path] {
				fail("path %q is listed twice", f.Path)
			}
			seen[f.Path] = true
		case len(f.Paths) > 0:
			errs = append(errs, validatePaths(field+": paths", f.Paths, p)...)
			for goos := range f.Paths {
				if _, ok := p.Paths[goos]; !ok {
					fail("paths: %s, which the placement has no path for", goos)
				}
			}
		default:
			fail("needs path or paths")
		}
		if f.Template == "" {
			fail("needs a template")
		} else if _, err := render.Parse(field, f.Template); err != nil {
			fail("%q: %w", f.Path+strings.Join(slices.Sorted(maps.Values(f.Paths)), ", "), err)
		}
	}
	return errs
}

// CheckFile refuses rel, a file's path relative to the placement root,
// unless it is clean, relative, slash-separated, holdable on Windows and
// outside every item tree and sofcat's own entries at the root. Parse
// applies it to a relative files path and the installer to an absolute one
// that resolves inside the root.
func (p Spec) CheckFile(rel string) error {
	if !isCleanRelative(rel) {
		return fmt.Errorf("path %q is not a clean relative path", rel)
	}
	if why := itemTreeCollision(rel, p.ItemsDir); why != "" {
		return fmt.Errorf("path %q %s", rel, why)
	}
	return nil
}

// itemTreeCollision explains why file, relative to the placement root,
// could be inside an item tree, or returns "". Trees live under itemsDir,
// or at the root when it is empty, where item names never start with "."
// (checkItemName in pkg/installer).
func itemTreeCollision(file, itemsDir string) string {
	first, _, _ := strings.Cut(file, "/")
	// sofcat's own entries at the root: the ownership marker, staging and
	// backup trees (.<slug>-*) and temporary files (.sofcat-*.tmp).
	for _, own := range []string{"." + identity.Slug + "-", ".sofcat-"} {
		if len(first) >= len(own) && strings.EqualFold(first[:len(own)], own) {
			return fmt.Sprintf("starts with %s, which sofcat keeps for its own files", own)
		}
	}
	if itemsDir == "" {
		if !strings.HasPrefix(first, ".") {
			return "must start with a . segment, which no item name has, or the placement must set items_dir"
		}
		return ""
	}
	// Windows and macOS compare names case-insensitively.
	if dir, _, _ := strings.Cut(itemsDir, "/"); strings.EqualFold(first, dir) {
		return fmt.Sprintf("is under items_dir %q", dir)
	}
	return ""
}

// isCleanRelative reports whether p is clean, relative and slash-separated,
// with every segment one Windows can hold, so a table parses the same on
// every OS.
func isCleanRelative(p string) bool {
	if p != path.Clean(p) || strings.HasPrefix(p, "/") {
		return false
	}
	for seg := range strings.SplitSeq(p, "/") {
		if CheckSegment(seg) != nil {
			return false
		}
	}
	return true
}
