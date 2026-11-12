package admin

import (
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
			problems := checkPlacementTemplates(mergeTable(t, tc.template), zipCatalog())
			joined := strings.Join(problems, "\n")
			if tc.want == "" && len(problems) != 0 || tc.want != "" && !strings.Contains(joined, tc.want) {
				t.Errorf("problems = %q, want %q", joined, tc.want)
			}
		})
	}
}
