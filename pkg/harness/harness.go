// Package harness declares harnesses.yaml, the repository file that defines
// where zip items are placed (see site/src/content/docs/guides/zip-items.md). Keep it free of agent
// dependencies, like pkg/catalog.
package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// Placement kinds.
const (
	KindDirectory   = "directory"
	KindMarketplace = "marketplace"
	KindSettings    = "settings"
)

// Scopes. An OS without a scope is ScopeMachine.
const (
	ScopeMachine = "machine"
	ScopeUser    = "user"
)

// OS keys of Placement.Paths and Placement.Scope.
const (
	Darwin  = "darwin"
	Windows = "windows"
)

// DefaultManifestFiles is where a marketplace's manifest is written when its
// block lists no manifest_files.
var DefaultManifestFiles = []string{".claude-plugin/marketplace.json"}

// Table is a parsed harnesses.yaml.
type Table struct {
	Placements map[string]Placement `yaml:"placements"`
	// CheckFacts are the facts makecatalogs -check renders templates with,
	// over the built-in samples (conditions.SampleFacts): the admin facts
	// the templates read.
	CheckFacts map[string]any `yaml:"check_facts,omitempty"`
	// Order holds the placement names in file order. Parse fills it.
	Order []string `yaml:"-"`
}

// Placement is a named destination for zip items.
type Placement struct {
	Kind string `yaml:"kind"`
	// Paths maps an OS (darwin, windows) to the placement root on it.
	Paths map[string]string `yaml:"paths,omitempty"`
	// Scope maps an OS to machine or user. An OS left out is machine.
	Scope        map[string]string `yaml:"scope,omitempty"`
	WorksIn      []string          `yaml:"works_in,omitempty"`
	ActionLabels map[string]string `yaml:"action_labels,omitempty"`
	Exclusive    bool              `yaml:"exclusive,omitempty"`
	// Marketplace is set for KindMarketplace only.
	Marketplace *Marketplace `yaml:"marketplace,omitempty"`
	// Registers names the placement a KindSettings file registers: a
	// marketplace, or with Template any directory or marketplace.
	Registers string `yaml:"registers,omitempty"`
	// Template renders a KindSettings file in place of the built-in Claude
	// Code drop-in (render.FileContext). An empty output removes the file.
	Template string `yaml:"template,omitempty"`
}

// Marketplace configures a marketplace placement's manifest. Owner, Renames
// and AllowCrossMarketplaceDependenciesOn are written to the manifest
// unchanged, so they stay untyped.
type Marketplace struct {
	Name                                string         `yaml:"name"`
	Owner                               any            `yaml:"owner"`
	Renames                             any            `yaml:"renames,omitempty"`
	AllowCrossMarketplaceDependenciesOn any            `yaml:"allowCrossMarketplaceDependenciesOn,omitempty"`
	ManifestFiles                       []ManifestFile `yaml:"manifest_files,omitempty"`
}

// ManifestFile is one manifest_files entry: a path, which gets the built-in
// Claude-format catalog, or {path, template}, which gets the rendered
// template (render.FileContext). In YAML a bare string is a path.
type ManifestFile struct {
	Path     string `yaml:"path"`
	Template string `yaml:"template,omitempty"`
}

// UnmarshalYAML accepts a string or a {path, template} mapping.
func (f *ManifestFile) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&f.Path)
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: manifest_files entry must be a path or {path, template}", node.Line)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if key := node.Content[i].Value; key != "path" && key != "template" {
			return fmt.Errorf("line %d: manifest_files entry: unknown field %q", node.Content[i].Line, key)
		}
	}
	type plain ManifestFile
	return node.Decode((*plain)(f))
}

// Manifests returns the manifest files to write, relative to the placement
// root: the default when the block lists none.
func (m Marketplace) Manifests() []ManifestFile {
	if len(m.ManifestFiles) == 0 {
		files := make([]ManifestFile, len(DefaultManifestFiles))
		for i, p := range DefaultManifestFiles {
			files[i] = ManifestFile{Path: p}
		}
		return files
	}
	return m.ManifestFiles
}

// Files returns the paths of Manifests.
func (m Marketplace) Files() []string {
	manifests := m.Manifests()
	files := make([]string, len(manifests))
	for i, f := range manifests {
		files[i] = f.Path
	}
	return files
}

// View describes placement name, rooted at root, to a template.
func (p Placement) View(name, root string) render.Placement {
	v := render.Placement{Name: name, Root: root, Kind: p.Kind}
	if p.Kind == KindMarketplace && p.Marketplace != nil {
		v.Marketplace = &render.Marketplace{
			Name:                                p.Marketplace.Name,
			Owner:                               render.JSONValue(p.Marketplace.Owner),
			Renames:                             render.JSONValue(p.Marketplace.Renames),
			AllowCrossMarketplaceDependenciesOn: render.JSONValue(p.Marketplace.AllowCrossMarketplaceDependenciesOn),
		}
	}
	return v
}

// Path returns the placement root for goos and whether it has one.
func (p Placement) Path(goos string) (string, bool) {
	path, ok := p.Paths[goos]
	return path, ok && path != ""
}

// ScopeFor returns the placement's scope on goos.
func (p Placement) ScopeFor(goos string) string {
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
// labels and the harnesses it works in. A label comes from the item's
// custom_action_labels, else from the first of its placements with a variant
// on goos, in table order, that sets it; a key set nowhere is left out, for
// the UI's built-in label. worksIn is the union of those placements'
// works_in, in table order. Only a zip item has placements.
func (t Table) Storefront(item catalog.Item, goos string) (labels map[string]string, worksIn []string) {
	var placements []Placement
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
		for _, harness := range p.WorksIn {
			if !slices.Contains(worksIn, harness) {
				worksIn = append(worksIn, harness)
			}
		}
	}
	return labels, worksIn
}

// Parse decodes harnesses.yaml, rejecting unknown fields, and validates it.
// An empty file is an empty table.
func Parse(data []byte) (Table, error) {
	var t Table
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&t); err != nil && !errors.Is(err, io.EOF) {
		return Table{}, fmt.Errorf("parse harnesses.yaml: %w", err)
	}
	order, err := placementOrder(data)
	if err != nil {
		return Table{}, fmt.Errorf("parse harnesses.yaml: %w", err)
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
		for _, err := range t.validatePlacement(name) {
			errs = append(errs, fmt.Errorf("placement %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (t Table) validatePlacement(name string) []error {
	p := t.Placements[name]
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	// The name becomes a directory name in rendered trees and receipts.
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\:`) {
		fail("invalid name")
	}
	switch p.Kind {
	case KindDirectory, KindMarketplace, KindSettings:
	default:
		fail("unknown kind %q", p.Kind)
	}
	if len(p.Paths) == 0 {
		fail("no paths")
	}
	for goos, path := range p.Paths {
		if goos != Darwin && goos != Windows {
			fail("paths: unknown OS %q", goos)
		}
		if path == "" {
			fail("paths: empty path for %s", goos)
		}
		if strings.Contains(path, "${USERPROFILE}") && p.ScopeFor(goos) != ScopeUser {
			fail("paths: ${USERPROFILE} in %s path, which is not user scope", goos)
		}
	}
	for goos, scope := range p.Scope {
		if goos != Darwin && goos != Windows {
			fail("scope: unknown OS %q", goos)
		}
		if scope != ScopeMachine && scope != ScopeUser {
			fail("scope: %s is %q, want machine or user", goos, scope)
		}
	}
	if p.ScopeFor(Darwin) == ScopeUser {
		fail("scope: darwin: user is not supported")
	}
	for key := range p.ActionLabels {
		if !slices.Contains(catalog.ActionLabelKeys, key) {
			fail("action_labels: unknown key %q", key)
		}
	}

	return append(errs, t.validateKindBlocks(p)...)
}

// validateKindBlocks checks the fields that belong to one kind only.
func (t Table) validateKindBlocks(p Placement) []error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if p.Kind != KindDirectory && (p.ScopeFor(Darwin) == ScopeUser || p.ScopeFor(Windows) == ScopeUser) {
		// Generated files are written by the service only.
		fail("scope: user is supported on kind directory only")
	}
	if p.Kind == KindMarketplace {
		errs = append(errs, validateMarketplace(p.Marketplace)...)
	} else if p.Marketplace != nil {
		fail("marketplace block on kind %q", p.Kind)
	}
	if p.Kind == KindSettings {
		errs = append(errs, t.validateSettings(p)...)
	} else {
		if p.Registers != "" {
			fail("registers on kind %q", p.Kind)
		}
		if p.Template != "" {
			fail("template on kind %q", p.Kind)
		}
	}
	return errs
}

// validateMarketplace checks a marketplace placement's block: name and
// owner, clean manifest paths, and templates that parse.
func validateMarketplace(m *Marketplace) []error {
	if m == nil {
		return []error{errors.New("kind marketplace needs a marketplace block")}
	}
	var errs []error
	if m.Name == "" || m.Owner == nil {
		errs = append(errs, errors.New("marketplace: name and owner are required"))
	}
	seen := map[string]bool{}
	for _, f := range m.ManifestFiles {
		// Slash-separated on every OS, like the default.
		if !filepath.IsLocal(f.Path) || strings.Contains(f.Path, `\`) || f.Path != path.Clean(f.Path) {
			errs = append(errs, fmt.Errorf("marketplace: manifest_files: %q is not a clean relative path", f.Path))
		}
		if seen[f.Path] {
			errs = append(errs, fmt.Errorf("marketplace: manifest_files: %q is listed twice", f.Path))
		}
		seen[f.Path] = true
		if f.Template != "" {
			if _, err := render.Parse(f.Path, f.Template); err != nil {
				errs = append(errs, fmt.Errorf("marketplace: manifest_files %q: %w", f.Path, err))
			}
		}
	}
	return errs
}

// validateSettings checks what a settings placement registers and its
// template: a marketplace, or with a template any directory or marketplace.
func (t Table) validateSettings(p Placement) []error {
	var errs []error
	target, ok := t.Placements[p.Registers]
	switch {
	case !ok:
		errs = append(errs, fmt.Errorf("registers %q, which is not a placement", p.Registers))
	case target.Kind == KindSettings:
		errs = append(errs, fmt.Errorf("registers %q, which is a settings placement", p.Registers))
	case target.Kind != KindMarketplace && p.Template == "":
		// Only a template knows how to describe a directory placement.
		errs = append(errs, fmt.Errorf("registers %q, which is not a marketplace placement, without a template", p.Registers))
	case target.ScopeFor(Darwin) == ScopeUser || target.ScopeFor(Windows) == ScopeUser:
		// The service writes generated files; it cannot expand a user path.
		errs = append(errs, fmt.Errorf("registers %q, which has user scope: generated files are machine scope only", p.Registers))
	}
	if p.Template != "" {
		if _, err := render.Parse("template", p.Template); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
