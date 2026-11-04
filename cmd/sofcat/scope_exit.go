package main

import (
	"log/slog"
	"slices"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/process"
)

// desiredItems is the run's desired set for scope exit: the names the run
// installs or updates (managed_installs, reconciled defaults and self-service
// selections, update_for riders, managed_updates), plus everything those pull
// in as dependencies or updaters. A receipt-tracked item outside it is removed.
func desiredItems(names []string, catalogs map[int]map[string]catalog.Item, index map[string][]string) map[string]bool {
	desired := make(map[string]bool)
	queue := slices.Clone(names)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if desired[name] {
			continue
		}
		desired[name] = true
		if item, err := process.ResolveRemoval(name, catalogs); err == nil {
			queue = append(queue, item.Dependencies...)
		}
		queue = append(queue, index[name]...)
	}
	return desired
}

// uninstallable is the subset of the run's uninstall list the uninstall pass
// acts on: the names that resolve in the catalogs. Scope exit leaves those to
// it and removes the rest by their receipts.
func uninstallable(uninstalls []string, catalogs map[int]map[string]catalog.Item) map[string]bool {
	set := make(map[string]bool)
	for _, name := range uninstalls {
		if _, err := process.ResolveRemoval(name, catalogs); err == nil {
			set[name] = true
		}
	}
	return set
}

// heldItems is the set of names under conditional_items blocks the run could
// not decide. They are neither installed nor removed.
func heldItems(held []manifest.Held) map[string]bool {
	set := make(map[string]bool)
	for _, h := range held {
		for _, name := range h.Items {
			set[name] = true
		}
	}
	return set
}

// dropHeld removes held names from the run's uninstall list, logging each.
func dropHeld(uninstalls []string, held map[string]bool) []string {
	return slices.DeleteFunc(uninstalls, func(name string) bool {
		if held[name] {
			slog.Info("not removing held item: facts unavailable", "item", name)
			return true
		}
		return false
	})
}

// managedInstalls is the set of names any effective manifest lists in
// managed_installs, for the receipts' managed flag.
func managedInstalls(manifests []manifest.Item) map[string]bool {
	set := make(map[string]bool)
	for _, m := range manifests {
		for _, name := range m.Installs {
			set[name] = true
		}
	}
	return set
}
