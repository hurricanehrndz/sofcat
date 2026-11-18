// Package categorize fills in the category of catalog items that have none
// by asking a language model behind an OpenAI-compatible chat completions
// endpoint (OpenRouter, a Cloudflare AI Gateway, LiteLLM). Answers are kept
// in a lock file in the repository, so the model is asked once per item and
// a build without the API key gives the same catalogs. Like pkg/admin it is
// repository-side tooling and stays free of agent dependencies.
package categorize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v4"
)

const (
	// ConfigName is the file in the repository that turns categorization on.
	ConfigName = "categorize.yaml"
	// LockName is the file in the repository that keeps the model's answers.
	LockName = "categories.lock.yaml"
	// APIKeyEnv names the environment variable that holds the API key. The
	// key is never read from a file in the repository, which is published.
	APIKeyEnv = "SOFCAT_AI_API_KEY" //nolint:gosec // G101: the name of the variable, not a key

	// ModeClosed lets the model choose only from the known categories.
	ModeClosed = "closed"
	// ModeOpen also lets it propose a new category.
	ModeOpen = "open"

	defaultBaseURL   = "https://openrouter.ai/api/v1"
	defaultMinWeight = 0.5
	// newCategoryFactor scales the weight of a category the repository does
	// not have yet, so in open mode a new one must clearly beat the existing
	// ones before it is coined.
	newCategoryFactor = 0.75
	maxCategoryLen    = 40
	batchSize         = 25
	maxExamples       = 20
)

// DefaultCategories are the known categories of a repository whose config
// lists none. The storefront has a glyph for each but the last.
var DefaultCategories = []string{
	"Browsers", "Communication", "Development", "Media", "Productivity", "Security", "Utilities", "AI",
}

// Config is <repo>/categorize.yaml.
type Config struct {
	// BaseURL is the endpoint's base, to which /chat/completions is added.
	BaseURL string `yaml:"base_url"`
	// Model is the model name as the endpoint knows it.
	Model string `yaml:"model"`
	// Mode is closed (the default) or open.
	Mode string `yaml:"mode"`
	// Categories replaces DefaultCategories. Categories written by hand in
	// package-info files are always known as well.
	Categories []string `yaml:"categories"`
	// MinWeight is the least weight the best category needs to be used;
	// below it the item stays without a category.
	MinWeight float64 `yaml:"min_weight"`
}

// Load reads dir's categorize.yaml. A repository without one has
// categorization off, and Load returns nil.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, ConfigName)
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // no config is not an error: the feature is off
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err = yaml.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeClosed
	}
	if cfg.MinWeight == 0 {
		cfg.MinWeight = defaultMinWeight
	}
	switch {
	case cfg.Model == "":
		return nil, fmt.Errorf("%s: model is required", path)
	case cfg.Mode != ModeClosed && cfg.Mode != ModeOpen:
		return nil, fmt.Errorf("%s: mode must be %s or %s, not %q", path, ModeClosed, ModeOpen, cfg.Mode)
	case cfg.MinWeight < 0 || cfg.MinWeight > 1:
		return nil, fmt.Errorf("%s: min_weight must be between 0 and 1", path)
	}
	for _, c := range cfg.Categories {
		if cleanCategory(c) != c || c == "" {
			return nil, fmt.Errorf("%s: invalid category %q", path, c)
		}
	}
	if len(cfg.Categories) == 0 {
		cfg.Categories = DefaultCategories
	}
	return &cfg, nil
}

// Item is what the model is told about a catalog item, in the JSON it reads.
type Item struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	Developer   string `json:"developer,omitempty"`
	// Category is the one written by hand; such an item is never asked
	// about and serves the model as an example.
	Category string `json:"category,omitempty"`
}

func (i Item) fingerprint() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{i.Name, i.DisplayName, i.Description, i.Developer}, "\x00")))
	return hex.EncodeToString(sum[:8])
}

// Entry is the model's answer for one item.
type Entry struct {
	// Category is the best category, or empty when none reached MinWeight.
	Category string `yaml:"category,omitempty"`
	// Weights are the model's weights for its candidate categories, after
	// scaling and normalising to a sum of 1.
	Weights map[string]float64 `yaml:"weights,omitempty"`
	// Fingerprint identifies the item text the answer was given for; the
	// item is asked about again when it changes.
	Fingerprint string `yaml:"fingerprint"`
}

// Lock is <repo>/categories.lock.yaml: item name to answer.
type Lock struct {
	Items map[string]Entry `yaml:"items"`
}

const lockHeader = "# Written by makecatalogs from the answers of the model in " + ConfigName + ".\n" +
	"# Commit it: builds reuse it instead of asking again. Delete an item's entry\n" +
	"# to have it asked about again.\n"

// ReadLock reads dir's lock file; a missing one is empty.
func ReadLock(dir string) (Lock, error) {
	path := filepath.Join(dir, LockName)
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Lock{}, nil
	}
	if err != nil {
		return Lock{}, err
	}
	var lock Lock
	if err = yaml.Unmarshal(body, &lock); err != nil {
		return Lock{}, fmt.Errorf("%s: %w", path, err)
	}
	return lock, nil
}

// WriteLock writes dir's lock file.
func WriteLock(dir string, lock Lock) error {
	body, err := yaml.Marshal(lock)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, LockName), append([]byte(lockHeader), body...), 0o644)
}

// Asker asks the model about a batch of items and returns its raw weights
// by item name. Client is the real one; tests supply their own.
type Asker interface {
	Ask(ctx context.Context, req Request) (map[string]map[string]float64, error)
}

// Request is one batch for the model.
type Request struct {
	// Known are the categories to choose from.
	Known []string
	// Open says the model may also propose a category outside Known.
	Open bool
	// Examples are items categorized by hand.
	Examples []Item
	Items    []Item
}

// Result is what Resolve found.
type Result struct {
	// Lock holds an entry for every item without a hand-written category
	// that has an answer, old or new.
	Lock Lock
	// Asked counts the items the model answered in this run.
	Asked int
	// Pending names the items that still have no answer: no asker, or the
	// model failed or left them out.
	Pending []string
	// Undecided names the items whose best category was under MinWeight.
	Undecided []string
}

// Resolve returns the lock for items: the entries of old that still stand,
// and the model's answers for the rest when asker is not nil. A failed
// request is returned as the error together with everything resolved before
// it, so a build can carry on.
func Resolve(ctx context.Context, cfg *Config, asker Asker, old Lock, items []Item) (Result, error) {
	res := Result{Lock: Lock{Items: map[string]Entry{}}}
	known := newCategorySet(cfg.Categories)
	var examples, todo []Item
	seen := map[string]bool{}
	sorted := slices.SortedFunc(slices.Values(items), func(a, b Item) int { return strings.Compare(a.Name, b.Name) })
	for _, item := range sorted {
		if item.Category != "" {
			known.add(item.Category)
			if len(examples) < maxExamples {
				examples = append(examples, item)
			}
		}
	}
	for _, item := range sorted {
		// One answer per name: an item in two catalogs is the same software.
		if item.Category != "" || seen[item.Name] {
			continue
		}
		seen[item.Name] = true
		entry, ok := old.Items[item.Name]
		_, inSet := known.canonical(entry.Category)
		if ok && entry.Fingerprint == item.fingerprint() && (entry.Category == "" || inSet || cfg.Mode == ModeOpen) {
			res.Lock.Items[item.Name] = entry
			known.add(entry.Category)
			continue
		}
		todo = append(todo, item)
	}

	var askErr error
	for batch := range slices.Chunk(todo, batchSize) {
		if asker == nil || askErr != nil {
			for _, item := range batch {
				res.Pending = append(res.Pending, item.Name)
			}
			continue
		}
		var answers map[string]map[string]float64
		answers, askErr = asker.Ask(ctx, Request{Known: known.names(), Open: cfg.Mode == ModeOpen, Examples: examples, Items: batch})
		for _, item := range batch {
			raw, ok := answers[item.Name]
			if !ok {
				res.Pending = append(res.Pending, item.Name)
				continue
			}
			entry := decide(cfg, known, raw)
			entry.Fingerprint = item.fingerprint()
			res.Lock.Items[item.Name] = entry
			res.Asked++
			// A category coined for one item is known to the batches after it.
			known.add(entry.Category)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(res.Lock.Items)) {
		if res.Lock.Items[name].Category == "" {
			res.Undecided = append(res.Undecided, name)
		}
	}
	return res, askErr
}

// decide turns the model's raw weights for one item into its entry. Names
// are matched to the known categories whatever their case; a name that is
// not known is dropped in closed mode and scaled by newCategoryFactor in
// open mode. The weights are then normalised to a sum of 1, and the best
// one wins if it reaches MinWeight and no other ties with it: an even split
// is the model not knowing.
func decide(cfg *Config, known *categorySet, raw map[string]float64) Entry {
	weights := map[string]float64{}
	var total float64
	for _, name := range slices.Sorted(maps.Keys(raw)) {
		weight := min(max(raw[name], 0), 1)
		name = cleanCategory(name)
		if name == "" || weight == 0 {
			continue
		}
		canonical, ok := known.canonical(name)
		if !ok {
			if cfg.Mode != ModeOpen {
				continue
			}
			canonical = name
			weight *= newCategoryFactor
		}
		weights[canonical] += weight
		total += weight
	}
	if total == 0 {
		return Entry{}
	}
	var best string
	tied := false
	for _, name := range slices.Sorted(maps.Keys(weights)) {
		// Three decimals keep the lock file readable and stable.
		weights[name] = float64(int(weights[name]/total*1000+0.5)) / 1000
		switch {
		case best == "" || weights[name] > weights[best]:
			best, tied = name, false
		case weights[name] == weights[best]:
			tied = true
		}
	}
	entry := Entry{Weights: weights}
	if weights[best] >= cfg.MinWeight && !tied {
		entry.Category = best
	}
	return entry
}

// cleanCategory makes a category name safe to show: one line, trimmed, and
// empty when it is longer than a category should be.
func cleanCategory(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if len(name) > maxCategoryLen {
		return ""
	}
	return name
}

// categorySet holds category names by their case-folded form, keeping the
// first spelling seen.
type categorySet struct {
	byFold map[string]string
}

func newCategorySet(names []string) *categorySet {
	s := &categorySet{byFold: map[string]string{}}
	for _, name := range names {
		s.add(name)
	}
	return s
}

func (s *categorySet) add(name string) {
	name = cleanCategory(name)
	if _, ok := s.byFold[strings.ToLower(name)]; name != "" && !ok {
		s.byFold[strings.ToLower(name)] = name
	}
}

func (s *categorySet) canonical(name string) (string, bool) {
	canonical, ok := s.byFold[strings.ToLower(cleanCategory(name))]
	return canonical, ok
}

func (s *categorySet) names() []string {
	names := slices.Collect(maps.Values(s.byFold))
	sort.Strings(names)
	return names
}
