package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/admin"
	"github.com/hurricanehrndz/sofcat/pkg/categorize"
)

// applyCategories gives the items of set that have no category the one in
// dir's lock file, when dir has a categorize.yaml. With ask it first has
// the model categorize the items the lock does not cover, if the API key is
// set, and writes the lock back. A model that fails is a warning: the
// catalogs are still written, with those items uncategorized until the next
// run. Only an unusable config or lock file is an error.
func applyCategories(dir string, set admin.CatalogSet, ask bool, stdout, stderr io.Writer) error {
	cfg, err := categorize.Load(dir)
	if cfg == nil || err != nil {
		return err
	}
	old, err := categorize.ReadLock(dir)
	if err != nil {
		return err
	}
	var items []categorize.Item
	for _, catalogName := range set.Names() {
		for name, item := range set.Catalogs[catalogName] {
			items = append(items, categorize.Item{
				Name: name, DisplayName: item.DisplayName, Description: item.Description,
				Developer: item.Developer, Category: item.Category,
			})
		}
	}

	var asker categorize.Asker
	if key := os.Getenv(categorize.APIKeyEnv); ask && key != "" {
		asker = &categorize.Client{BaseURL: cfg.BaseURL, Model: cfg.Model, APIKey: key}
	}
	res, askErr := categorize.Resolve(context.Background(), cfg, asker, old, items)
	if askErr != nil {
		_, _ = fmt.Fprintf(stderr, "warning: %s: %v\n", categorize.ConfigName, askErr)
	}
	if ask {
		if len(res.Pending) > 0 {
			reason := "the model gave no answer"
			if asker == nil {
				reason = categorize.APIKeyEnv + " is not set"
			}
			_, _ = fmt.Fprintf(stderr, "warning: %d item(s) left without a category, %s: %s\n",
				len(res.Pending), reason, strings.Join(res.Pending, ", "))
		}
		if res.Asked > 0 {
			_, _ = fmt.Fprintf(stdout, "Categorized %d item(s) with %s\n", res.Asked, cfg.Model)
		}
		// An unchanged lock is left alone, so a build that asked nothing
		// does not touch the repository.
		if !reflect.DeepEqual(normalLock(old), res.Lock) {
			if err = categorize.WriteLock(dir, res.Lock); err != nil {
				return err
			}
		}
	}

	for _, catalogItems := range set.Catalogs {
		for name, item := range catalogItems {
			if entry, ok := res.Lock.Items[name]; ok && item.Category == "" {
				item.Category = entry.Category
				catalogItems[name] = item
			}
		}
	}
	return nil
}

// normalLock gives an empty lock the shape Resolve returns one in.
func normalLock(lock categorize.Lock) categorize.Lock {
	if lock.Items == nil {
		lock.Items = map[string]categorize.Entry{}
	}
	return lock
}
