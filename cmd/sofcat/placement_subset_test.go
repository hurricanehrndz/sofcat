package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/service"
)

// TestTapasPlacementSubset: a self-serve item narrowed to some placements is
// placed only there, in machine scope and through the user plan; narrowing it
// to others removes its tree from the placement it left and keeps the item;
// clearing the subset sends it everywhere again. An item an admin manifest
// requires ignores its subset.
func TestTapasPlacementSubset(t *testing.T) {
	e := newTapasEnv(t)
	claude, codex := filepath.Join(e.roots, "claude"), filepath.Join(e.roots, "codex")
	e.servePlacements(map[string]string{
		"claude":  claude,
		"codex":   codex,
		"profile": "${USERPROFILE}/.agents/skills",
	}, nil, []string{"profile"})
	skill := map[string]string{"SKILL.md": "x"}
	e.zipItem("multi", "1.0", map[string]map[string]string{"claude": skill, "codex": skill, "profile": skill})
	e.zipItem("req", "1.0", map[string]map[string]string{"claude": skill, "codex": skill})
	e.publishCatalog()
	e.put("manifests/tapas.yaml", []byte("name: tapas\ncatalogs: [tapas]\nmanaged_installs: [req]\noptional_installs: [multi, req]\n"))

	choose := func(subset []string) {
		t.Helper()
		entry := manifest.Item{Installs: []string{"multi", "req"}, PlacementSelections: map[string][]string{"req": {"claude"}}}
		if subset != nil {
			entry.PlacementSelections["multi"] = subset
		}
		if err := manifest.SaveSelfServe(manifest.SelfServePath(e.cfg.AppDataPath), entry); err != nil {
			t.Fatal(err)
		}
	}
	planned := func() []string {
		t.Helper()
		plan, err := service.LoadUserPlan(e.cfg.AppDataPath)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, entry := range plan.Entries {
			got = append(got, entry.Item+"/"+entry.Placement)
		}
		return got
	}

	choose([]string{"claude"})
	rep := e.run()
	if !exists(filepath.Join(claude, "multi")) || exists(filepath.Join(codex, "multi")) || len(planned()) != 0 {
		t.Fatalf("narrowed to claude: claude %v, codex %v, plan %v", exists(filepath.Join(claude, "multi")), exists(filepath.Join(codex, "multi")), planned())
	}
	if !exists(filepath.Join(codex, "req")) || failedCodes(rep, "multi") != nil {
		t.Fatalf("req not placed everywhere, or multi failed: %v", rep.FailedItems)
	}

	choose([]string{"codex"})
	rep = e.run()
	if exists(filepath.Join(claude, "multi")) || !exists(filepath.Join(codex, "multi")) {
		t.Fatalf("narrowed to codex: claude %v, codex %v", exists(filepath.Join(claude, "multi")), exists(filepath.Join(codex, "multi")))
	}
	if listed(rep.UninstalledItems, "multi") || failedCodes(rep, "multi") != nil {
		t.Errorf("leaving a placement reported multi uninstalled or failed: %+v", rep)
	}

	choose(nil)
	e.run()
	if !exists(filepath.Join(claude, "multi")) || !exists(filepath.Join(codex, "multi")) || !slices.Equal(planned(), []string{"multi/profile"}) {
		t.Fatalf("every placement: claude %v, codex %v, plan %v", exists(filepath.Join(claude, "multi")), exists(filepath.Join(codex, "multi")), planned())
	}
}
