package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The sample table under examples/ is documentation; it must stay valid.
func TestExampleTableParses(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "examples", "harnesses.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, p := range table.Placements {
		kinds[p.Kind] = true
	}
	if len(kinds) != 3 {
		t.Errorf("example shows kinds %v, want all three", kinds)
	}
}

func TestParse(t *testing.T) {
	table, err := Parse([]byte(`
placements:
  codex-skills:
    kind: directory
    paths:
      darwin: /etc/codex/skills
      windows: ${USERPROFILE}\.agents\skills
    scope: {windows: user}
  market:
    kind: marketplace
    paths: {darwin: /m}
    marketplace: {name: acme, owner: {name: Acme IT}}
`))
	if err != nil {
		t.Fatal(err)
	}
	codex := table.Placements["codex-skills"]
	if codex.ScopeFor(Darwin) != ScopeMachine || codex.ScopeFor(Windows) != ScopeUser {
		t.Errorf("scopes darwin=%s windows=%s", codex.ScopeFor(Darwin), codex.ScopeFor(Windows))
	}
	if _, ok := codex.Path(Windows); !ok {
		t.Error("no windows path")
	}
	if got := table.Placements["market"].Marketplace.Files(); !reflect.DeepEqual(got, DefaultManifestFiles) {
		t.Errorf("manifest files %v", got)
	}
	if got := table.Names(); !reflect.DeepEqual(got, []string{"codex-skills", "market"}) {
		t.Errorf("names %v", got)
	}
	if empty, err := Parse(nil); err != nil || len(empty.Placements) != 0 {
		t.Errorf("empty file: %v, %v", empty, err)
	}
}

// Action labels and "Works in" follow harness-table order, which is the
// order the author wrote, not the alphabet.
func TestNamesKeepFileOrder(t *testing.T) {
	table, err := Parse([]byte(`
placements:
  zed: {kind: directory, paths: {darwin: /z}}
  alpha: {kind: directory, paths: {darwin: /a}}
  mid: {kind: directory, paths: {darwin: /m}}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := table.Names(); !reflect.DeepEqual(got, []string{"zed", "alpha", "mid"}) {
		t.Errorf("names %v, want file order", got)
	}

	// A duplicate key would leave Order and Placements disagreeing.
	_, err = Parse([]byte(`
placements:
  p: {kind: directory, paths: {darwin: /a}}
  p: {kind: directory, paths: {darwin: /b}}
`))
	if err == nil || !strings.Contains(err.Error(), `"p"`) {
		t.Errorf("duplicate placement: error %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	tests := map[string]struct{ yaml, want string }{
		"unknown field": {`
placements:
  p: {kind: directory, paths: {darwin: /p}, colour: red}`, "colour"},
		"unknown top-level field": {"placement: {}\n", "placement"},
		"bad kind": {`
placements:
  p: {kind: folder, paths: {darwin: /p}}`, "unknown kind"},
		"no paths": {`
placements:
  p: {kind: directory}`, "no paths"},
		"unknown path OS": {`
placements:
  p: {kind: directory, paths: {linux: /p}}`, "unknown OS"},
		"darwin user scope": {`
placements:
  p: {kind: directory, paths: {darwin: /p}, scope: {darwin: user}}`, "darwin: user"},
		"bad scope value": {`
placements:
  p: {kind: directory, paths: {windows: 'C:\p'}, scope: {windows: everyone}}`, "want machine or user"},
		"USERPROFILE in machine scope": {`
placements:
  p: {kind: directory, paths: {windows: '${USERPROFILE}\skills'}}`, "${USERPROFILE}"},
		"unknown action label": {`
placements:
  p: {kind: directory, paths: {darwin: /p}, action_labels: {buy: Buy}}`, "action_labels"},
		"marketplace without block": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}}`, "needs a marketplace block"},
		"marketplace without owner": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: acme}}`, "name and owner"},
		"marketplace manifest outside the root": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b, manifest_files: [../x.json]}}`, "manifest_files"},
		"user-scope marketplace": {`
placements:
  m: {kind: marketplace, paths: {windows: '${USERPROFILE}\m'}, scope: {windows: user}, marketplace: {name: a, owner: b}}`, "kind directory only"},
		"marketplace block on directory": {`
placements:
  p: {kind: directory, paths: {darwin: /p}, marketplace: {name: a, owner: b}}`, "marketplace block on kind"},
		"settings registering a missing marketplace": {`
placements:
  s: {kind: settings, paths: {darwin: /s.json}, registers: nowhere}`, "registers"},
		"settings registering a directory": {`
placements:
  p: {kind: directory, paths: {darwin: /p}}
  s: {kind: settings, paths: {darwin: /s.json}, registers: p}`, "not a marketplace"},
		"registers on directory": {`
placements:
  p: {kind: directory, paths: {darwin: /p}, registers: p}`, "registers on kind"},
		"name with a path": {`
placements:
  a/b: {kind: directory, paths: {darwin: /p}}`, "invalid name"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want one containing %q", err, tc.want)
			}
		})
	}
}
