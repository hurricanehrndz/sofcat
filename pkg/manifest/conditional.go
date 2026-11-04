package manifest

import (
	"log/slog"
	"slices"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
)

// ConditionalItem is one conditional_items block: lists that apply only when
// its condition holds on this machine. Blocks nest.
type ConditionalItem struct {
	Condition        string            `yaml:"condition"`
	Installs         []string          `yaml:"managed_installs"`
	OptionalInstalls []string          `yaml:"optional_installs"`
	DefaultInstalls  []string          `yaml:"default_installs"`
	Uninstalls       []string          `yaml:"managed_uninstalls"`
	Updates          []string          `yaml:"managed_updates"`
	ConditionalItems []ConditionalItem `yaml:"conditional_items"`
}

// Held is a conditional_items block whose condition is unknown on this
// machine: Items, every name under it including nested blocks, are neither
// added nor removed until Missing facts appear.
type Held struct {
	Condition string
	Missing   []string
	Items     []string
}

// Expand resolves every manifest's conditional_items against facts. A true
// block's lists join its manifest's plain lists, so callers read only
// Installs, OptionalInstalls and the rest; a false block, or one whose
// condition cannot be evaluated, adds nothing; an unknown block is returned in
// held instead. The effective manifests carry no conditional_items. Every
// reader of manifest lists, the managed run and the service alike, must read
// Expand's output, or the list call and the run disagree.
func Expand(manifests []Item, facts conditions.Facts) (effective []Item, held []Held) {
	effective = make([]Item, 0, len(manifests))
	for _, m := range manifests {
		blocks := m.ConditionalItems
		m.ConditionalItems = nil
		// Clip so appending never writes into the caller's backing arrays.
		m.Installs = slices.Clip(m.Installs)
		m.OptionalInstalls = slices.Clip(m.OptionalInstalls)
		m.DefaultInstalls = slices.Clip(m.DefaultInstalls)
		m.Uninstalls = slices.Clip(m.Uninstalls)
		m.Updates = slices.Clip(m.Updates)
		held = expandBlocks(&m, blocks, facts, held)
		effective = append(effective, m)
	}
	return effective, held
}

func expandBlocks(m *Item, blocks []ConditionalItem, facts conditions.Facts, held []Held) []Held {
	for _, b := range blocks {
		result, missing, err := conditions.Evaluate(b.Condition, facts)
		switch {
		case err != nil:
			slog.Warn("conditional_items condition cannot be evaluated, skipping its items", "manifest", m.Name, "condition", b.Condition, "err", err)
		case result == conditions.True:
			m.Installs = append(m.Installs, b.Installs...)
			m.OptionalInstalls = append(m.OptionalInstalls, b.OptionalInstalls...)
			m.DefaultInstalls = append(m.DefaultInstalls, b.DefaultInstalls...)
			m.Uninstalls = append(m.Uninstalls, b.Uninstalls...)
			m.Updates = append(m.Updates, b.Updates...)
			held = expandBlocks(m, b.ConditionalItems, facts, held)
		case result == conditions.Unknown:
			held = append(held, Held{Condition: b.Condition, Missing: missing, Items: b.names(nil)})
		}
	}
	return held
}

// names appends every item name in b and its nested blocks to names, once each.
func (b ConditionalItem) names(names []string) []string {
	for _, list := range [][]string{b.Installs, b.OptionalInstalls, b.DefaultInstalls, b.Uninstalls, b.Updates} {
		for _, name := range list {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	for _, nested := range b.ConditionalItems {
		names = nested.names(names)
	}
	return names
}
