package admin

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
)

func mergeTable(t *testing.T, template string) repoPlacements {
	t.Helper()
	body := `placements:
  market:
    paths: {darwin: /Library/market, windows: 'C:\market'}
    files:
      - paths:
          darwin: ${HOME}/.claude/settings.json
          windows: ${USERPROFILE}\.claude\settings.json
        scope: user
        merge: add_if_absent
        template: |
` + template
	table, err := placement.Parse([]byte(body), nil)
	if err != nil {
		t.Fatal(err)
	}
	return repoPlacements{path: "placements.yaml", table: table, present: true}
}

func zipCatalog() map[string]map[string]catalog.Item {
	item := catalog.Item{Name: "a", Version: "1", Installer: catalog.InstallerItem{Type: "zip", Variants: map[string]map[string]catalog.Variant{
		"market": {"darwin": {Location: "a.zip"}, "windows": {Location: "a.zip"}},
	}}}
	return map[string]map[string]catalog.Item{"main": {"a": item}}
}

// A merge template must render a JSON object whatever its items, and a user
// file renders with a user, as the helper will.
func TestCheckMergeTemplates(t *testing.T) {
	tests := map[string]struct{ template, want string }{
		"good": {`          {"enabledPlugins": { {{- range $i, $p := .Items }}{{ if $i }},{{ end }}{{ json $p.Name }}: true{{ end -}} }}
`, ""},
		"user in scope": {`          {"who": {{ json .User }}, "scope": {{ json .Scope }}}
`, ""},
		"array": {`          [{{ range $i, $p := .Items }}{{ if $i }},{{ end }}{{ json $p.Name }}{{ end }}]
`, "not a JSON object"},
		"invalid JSON with items": {`          {"a": {{ range .Items }}1{{ end }}}
`, "not JSON"},
		"nested objects are opaque": {`          {"a": {"b": {"c": 1}}}
`, ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			problems := checkPlacementTemplates(t.TempDir(), mergeTable(t, tc.template), zipCatalog())
			joined := strings.Join(problems, "\n")
			if tc.want == "" && len(problems) != 0 || tc.want != "" && !strings.Contains(joined, tc.want) {
				t.Errorf("problems = %q, want %q", joined, tc.want)
			}
		})
	}
}

// The check gives each item its archive as the tree, so a template that
// lists items by what they ship renders as it will on a device. An archive
// the repository does not hold fails only a template that looks inside.
func TestCheckTemplatesReadTheArchive(t *testing.T) {
	table := func(template string) repoPlacements {
		parsed, err := placement.Parse([]byte(`placements:
  market:
    paths: {darwin: /Library/market, windows: 'C:\market'}
    files:
      - path: .index/catalog.json
        template: '`+template+`'
`), nil)
		if err != nil {
			t.Fatal(err)
		}
		return repoPlacements{path: "placements.yaml", table: parsed, present: true}
	}
	inTree := `[{{ range .Items }}{{ if .HasFile "plugin.json" }}{{ json (.ReadJSON "plugin.json").category }}{{ end }}{{ end }}]`

	repo := t.TempDir()
	f, err := os.Create(filepath.Join(repo, "a.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`{"category": 7}`)); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(zw.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}

	if problems := checkPlacementTemplates(repo, table(inTree), zipCatalog()); len(problems) != 0 {
		t.Errorf("archive in the repository: %q", problems)
	}
	// Not JSON only when the value read is the archive's.
	wrong := `{{ range .Items }}{{ if eq (.ReadJSON "plugin.json").category 7 }}not json{{ end }}{{ end }}`
	if problems := checkPlacementTemplates(repo, table(wrong), zipCatalog()); len(problems) == 0 {
		t.Error("the template did not read the archive")
	}
	problems := strings.Join(checkPlacementTemplates(t.TempDir(), table(inTree), zipCatalog()), "\n")
	if !strings.Contains(problems, `archive "a.zip" is not in the repository`) {
		t.Errorf("archive elsewhere: %q", problems)
	}
	if problems := checkPlacementTemplates(t.TempDir(), table(`{{ json .Items }}`), zipCatalog()); len(problems) != 0 {
		t.Errorf("a template that does not look inside: %q", problems)
	}
}
