package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// codexTemplate is a Codex-format catalog, versions stamped with the placed
// hash, as the Tapas fixture writes it.
const codexTemplate = `{
  "name": {{ json .Placement.Vars.name }},
  "interface": {"displayName": {{ json .Placement.Vars.owner.name }}},
  "plugins": [
    {{- range $i, $p := .Items }}{{ if $i }},{{ end }}
    {
      "name": {{ json $p.Name }},
      "version": "{{ $p.Version }}+{{ slice $p.PlacedHash 7 19 }}",
      "source": {"source": "local", "path": "./plugins/{{ $p.Name }}"},
      "policy": {"installation": {{ if $p.Managed }}"INSTALLED_BY_DEFAULT"{{ else }}"AVAILABLE"{{ end }}}
    }
    {{- end }}
  ]
}
`

// indexTemplate is a file of a directory placement: a JSON index of its
// items, gone when there is none.
const indexTemplate = `{{- if .Items }}
{"os": {{ json (fact "os") }}, "root": {{ json .Placement.Root }}, "skills": {{ json .Items }}}
{{- end }}
`

// templateTest is an installer with a marketplace "market" holding the
// Claude catalog and a Codex catalog, and a directory "skills" whose file is
// an index at its own path.
type templateTest struct {
	z            *ZipInstaller
	market, root string // the marketplace root and the skills root
	codex, index string
}

func newTemplateTest(t *testing.T, index string) templateTest {
	t.Helper()
	base := tempDir(t)
	roots := filepath.Join(base, "roots")
	market := filepath.Join(roots, "market")
	skills := filepath.Join(roots, "skills")
	indexPath := filepath.Join(roots, "skills-index.json")
	catalog, _ := claudeTemplates(t)
	table, err := placement.Parse(fmt.Appendf(nil, `placements:
  skills:
    paths: {%[1]s: '%[2]s'}
    files:
      - paths: {%[1]s: '%[4]s'}
        template: |
%[6]s
  market:
    paths: {%[1]s: '%[3]s'}
    items_dir: plugins
    vars:
      name: acme
      owner: {name: Acme IT}
    files:
      - path: .claude-plugin/marketplace.json
        template: |
%[7]s
      - path: .agents/plugins/marketplace.json
        template: |
%[5]s
`, testOS(), skills, market, indexPath, indent(codexTemplate), indent(index), indent(catalog)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return templateTest{
		z: &ZipInstaller{
			Guard: placement.Guard{
				GOOS:    testOS(),
				Roots:   placement.Roots{Darwin: []string{roots}, Windows: []string{roots}},
				DataDir: filepath.Join(base, "data"),
			},
			Table:      table,
			ReceiptDir: filepath.Join(base, "data", "receipts"),
			Slug:       "sofcat-test",
			Facts:      conditions.Facts{"os": testOS(), "os_build": 22631},
		},
		market: market,
		root:   skills,
		codex:  filepath.Join(market, ".agents", "plugins", "marketplace.json"),
		index:  indexPath,
	}
}

// indent puts s in a YAML block scalar nested under files.
func indent(s string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "          " + l
	}
	return strings.Join(lines, "\n")
}

func (tt templateTest) install(t *testing.T, name, placementName string, managed bool) Result {
	t.Helper()
	item, archive := plugin(t, name, placementName)
	tt.z.Managed = func(string) bool { return managed }
	return tt.z.Apply(item, target(t, tt.z, item, placementName), archive)
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, data)
	}
	return v
}

// TestManifestTemplate: a second catalog is rendered from the same
// receipts as the Claude catalog, canonical, with the version stamped
// from the placed hash and the policy from the managed flag; the Claude
// catalog beside it is unchanged.
func TestManifestTemplate(t *testing.T) {
	tt := newTemplateTest(t, indexTemplate)
	wantOutcome(t, "install zeta", tt.install(t, "zeta", "market", true), OutcomeInstalled)
	wantOutcome(t, "install alpha", tt.install(t, "alpha", "market", false), OutcomeInstalled)

	codex := readJSON(t, tt.codex)
	if codex["name"] != "acme" || codex["interface"].(map[string]any)["displayName"] != "Acme IT" {
		t.Errorf("codex catalog = %v", codex)
	}
	plugins := codex["plugins"].([]any)
	if len(plugins) != 2 {
		t.Fatalf("plugins = %v", plugins)
	}
	stamped := regexp.MustCompile(`^1\.0\.0\+[0-9a-f]{12}$`)
	for i, want := range []struct {
		name, policy string
	}{{"alpha", "AVAILABLE"}, {"zeta", "INSTALLED_BY_DEFAULT"}} {
		p := plugins[i].(map[string]any)
		if p["name"] != want.name || !stamped.MatchString(p["version"].(string)) ||
			p["source"].(map[string]any)["path"] != "./plugins/"+want.name ||
			p["policy"].(map[string]any)["installation"] != want.policy {
			t.Errorf("plugin %d = %v", i, p)
		}
	}
	// Canonical: sorted keys, two-space indent, trailing newline.
	data, _ := os.ReadFile(tt.codex)
	if !strings.HasPrefix(string(data), "{\n  \"interface\": {\n    \"displayName\": \"Acme IT\"\n  },\n  \"name\": \"acme\",\n  \"plugins\": [\n") || !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("codex catalog is not canonical:\n%s", data)
	}
	builtin, _ := os.ReadFile(filepath.Join(tt.market, ".claude-plugin", "marketplace.json"))
	if !strings.Contains(string(builtin), `"defaultEnabled": true`) || strings.Contains(string(builtin), "policy") {
		t.Errorf("built-in manifest changed:\n%s", builtin)
	}
}

// TestDirectoryPlacementFile: a directory placement's file at its own path
// follows the receipts, reads facts, is removed when the template renders
// nothing, and is restored by the per-run pass.
func TestDirectoryPlacementFile(t *testing.T) {
	tt := newTemplateTest(t, indexTemplate)
	if failed := tt.z.RegenerateAll(); len(failed) != 0 || exists(tt.index) {
		t.Fatalf("before any tree: %+v, index exists %v", failed, exists(tt.index))
	}
	wantOutcome(t, "install hello", tt.install(t, "hello", "skills", true), OutcomeInstalled)
	index := readJSON(t, tt.index)
	skills := index["skills"].([]any)
	if index["os"] != testOS() || index["root"] != tt.root || len(skills) != 1 {
		t.Fatalf("index = %v", index)
	}
	if s := skills[0].(map[string]any); s["name"] != "hello" || s["managed"] != true || s["path"] != filepath.Join(tt.root, "hello") || !strings.HasPrefix(s["placed_hash"].(string), "sha256:") {
		t.Errorf("skill entry = %v", s)
	}

	if err := os.Remove(tt.index); err != nil {
		t.Fatal(err)
	}
	if failed := tt.z.RegenerateAll(); len(failed) != 0 || !exists(tt.index) {
		t.Fatalf("RegenerateAll = %+v, index restored %v", failed, exists(tt.index))
	}

	rc := readReceiptFor(t, tt.z, "skills", "hello")
	wantOutcome(t, "remove hello", tt.z.Remove(rc), OutcomeRemoved)
	if exists(tt.index) {
		t.Error("index kept with no skill left")
	}
}

// TestTemplateRenderFailureRefuses: a template that cannot render (a fact
// this machine lacks) refuses the placement with placement_refused; the tree
// is placed and nothing is written for the file. The placement's other files
// are still written, unless it sets files_atomic.
func TestTemplateRenderFailureRefuses(t *testing.T) {
	tt := newTemplateTest(t, `{{ json (fact "assigned_user") }}`)
	res := tt.install(t, "hello", "skills", true)
	if res.Outcome != OutcomeRefused || res.Code() != report.CodePlacementRefused || !strings.Contains(res.Err.Error(), `"skills"`) || !strings.Contains(res.Err.Error(), `fact "assigned_user"`) {
		t.Errorf("render failure = %s %q (%v)", res.Outcome, res.Code(), res.Err)
	}
	if !exists(filepath.Join(tt.root, "hello", "README.md")) || exists(tt.index) {
		t.Error("tree not placed, or the settings file written")
	}
	// A fact the next run has heals it.
	tt.z.Facts["assigned_user"] = "cs"
	if failed := tt.z.RegenerateAll(); len(failed) != 0 || !exists(tt.index) {
		t.Errorf("with the fact: %+v, index %v", failed, exists(tt.index))
	}

	empty := newTemplateTest(t, indexTemplate)
	wantOutcome(t, "install one", empty.install(t, "one", "market", true), OutcomeInstalled)
	catalogPath := filepath.Join(empty.market, ".claude-plugin", "marketplace.json")
	empty.z.Table.Placements["market"].Files[1].Template = `{{ fact "assigned_user" }}`
	res = empty.install(t, "two", "market", true)
	if res.Outcome != OutcomeRefused || !strings.Contains(res.Err.Error(), `fact "assigned_user"`) {
		t.Errorf("failing second file = %s (%v)", res.Outcome, res.Err)
	}
	if got := readFile(t, catalogPath); !strings.Contains(got, `"two"`) {
		t.Errorf("Claude catalog not rewritten beside the failing file:\n%s", got)
	}

	// With files_atomic the failing template keeps the catalog as it was.
	spec := empty.z.Table.Placements["market"]
	spec.FilesAtomic = true
	empty.z.Table.Placements["market"] = spec
	before := readFile(t, catalogPath)
	res = empty.install(t, "three", "market", true)
	if res.Outcome != OutcomeRefused {
		t.Errorf("failing second file, atomic = %s (%v)", res.Outcome, res.Err)
	}
	if after := readFile(t, catalogPath); after != before {
		t.Errorf("Claude catalog rewritten despite files_atomic:\n%s", after)
	}
}

func readReceiptFor(t *testing.T, z *ZipInstaller, placementName, item string) receipt.Receipt {
	t.Helper()
	all, err := receipt.List(z.ReceiptDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rc := range all {
		if rc.Placement == placementName && rc.Item == item {
			return rc
		}
	}
	t.Fatalf("no receipt for %s/%s", placementName, item)
	return receipt.Receipt{}
}
