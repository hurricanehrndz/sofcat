package render

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
)

var facts = conditions.Facts{
	"os":           "windows",
	"os_build":     22631,
	"os_version":   "10.0.22631.4460",
	"laptop":       true,
	"ipv4_address": []string{"10.1.2.3"},
	"site":         map[string]any{"name": "east", "floor": 3},
}

func render(t *testing.T, source string, data any, path string) ([]byte, conditions.Facts, error) {
	t.Helper()
	tmpl, err := Parse("t", source)
	if err != nil {
		t.Fatalf("Parse(%q): %v", source, err)
	}
	return tmpl.Render(data, facts, path)
}

func mustRender(t *testing.T, source string, data any, path string) string {
	t.Helper()
	out, _, err := render(t, source, data, path)
	if err != nil {
		t.Fatalf("Render(%q): %v", source, err)
	}
	return string(out)
}

// fact returns the fact's value whatever its type, so a template can print,
// index or test it; a fact the machine lacks fails the render naming it.
func TestFact(t *testing.T) {
	for source, want := range map[string]string{
		`{{ fact "os" }}`:                     "windows",
		`{{ fact "os_build" }}`:               "22631",
		`{{ if fact "laptop" }}yes{{ end }}`:  "yes",
		`{{ index (fact "ipv4_address") 0 }}`: "10.1.2.3",
		`{{ (fact "site").name }}`:            "east",
		`{{ json (fact "site") }}`:            `{"floor":3,"name":"east"}`,
		`{{ json (fact "ipv4_address") }}`:    `["10.1.2.3"]`,
		`{{ json (fact "laptop") }}`:          "true",
		`{{ json "<a&b>" }}`:                  `"<a&b>"`,
		`{{ json .Items }}`:                   `[{"name":"a","version":"1","managed":true,"path":"p","tree_hash":"sha256:t","placed_hash":"sha256:p"}]`,
		`{{ .Placement.Vars.name }}`:          "acme",
		`{{ json .Placement.Vars.owner }}`:    `{"1":"x","name":"Acme"}`,
	} {
		ctx := FileContext{
			Placement: Placement{Name: "m", Vars: JSONValue(map[string]any{"name": "acme", "owner": map[any]any{"name": "Acme", 1: "x"}})},
			Items:     []Item{{Name: "a", Version: "1", Managed: true, Path: "p", TreeHash: "sha256:t", PlacedHash: "sha256:p"}},
		}
		if got := mustRender(t, source, ctx, "out.txt"); got != want {
			t.Errorf("%s = %q, want %q", source, got, want)
		}
	}
	_, _, err := render(t, `{{ fact "missing" }}`, FileContext{}, "out.txt")
	if err == nil || !strings.Contains(err.Error(), `fact "missing"`) {
		t.Errorf("missing fact: %v", err)
	}
}

// cond has conditional_items' semantics, version() included; an unknown
// result, a syntax error and a non-boolean fail the render naming the
// expression, and now() is refused when the template parses.
func TestCond(t *testing.T) {
	for source, want := range map[string]string{
		`{{ if cond "os == \"windows\"" }}y{{ else }}n{{ end }}`:                          "y",
		`{{ if cond "os == \"darwin\"" }}y{{ else }}n{{ end }}`:                           "n",
		`{{ if cond "version(os_version) >= \"10.0.22000\" && os_build > 1" }}y{{ end }}`: "y",
		`{{ if cond "version(os_version) < \"10.0.22000\"" }}y{{ else }}n{{ end }}`:       "n",
		`{{ if cond "" }}y{{ end }}`:                                                      "y",
	} {
		if got := mustRender(t, source, FileContext{}, "out.txt"); got != want {
			t.Errorf("%s = %q, want %q", source, got, want)
		}
	}
	// Expressions a template builds are checked when it runs.
	for expr, want := range map[string]string{
		`zone == "a" || os == rack`:  `cond "zone == \"a\" || os == rack": no fact named rack, zone`,
		`os ==`:                      `cond "os ==": `,
		`os_build + 1`:               `expected bool`,
		`now() > date("2020-01-01")`: `calls now()`,
	} {
		_, _, err := render(t, `{{ cond .Expr }}`, map[string]string{"Expr": expr}, "out.txt")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("cond %s: err = %v, want %q", expr, err, want)
		}
	}
	if _, err := Parse("t", `{{ cond "os ==" }}`); err == nil || !strings.Contains(err.Error(), `t:1:3: cond "os =="`) {
		t.Errorf("literal syntax error: %v", err)
	}
	// A literal now() fails to parse, so the validator rejects it too.
	_, err := Parse("t", "line one\n{{ if cond \"now() > date(\\\"2020-01-01\\\")\" }}{{ end }}")
	if err == nil || !strings.Contains(err.Error(), "t:2:") || !strings.Contains(err.Error(), "now()") {
		t.Errorf("literal now(): %v", err)
	}
}

// A render reports every fact it read, through fact or inside a cond, and
// nothing else, so a receipt can tell which fact changes matter.
func TestRenderRecordsFacts(t *testing.T) {
	_, read, err := render(t, `{{ fact "os" }} {{ if cond "os_build >= 22000 && version(os_version) >= \"10\"" }}y{{ end }}`, FileContext{}, "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := conditions.Facts{"os": "windows", "os_build": 22631, "os_version": "10.0.22631.4460"}
	if !reflect.DeepEqual(read, want) {
		t.Errorf("read = %v, want %v", read, want)
	}
	if _, read, _ := render(t, "static", FileContext{}, "x.txt"); len(read) != 0 {
		t.Errorf("a static template read %v", read)
	}
}

// A field the context lacks fails, naming line and column, rather than
// rendering <no value>.
func TestMissingKeyAndPositions(t *testing.T) {
	_, _, err := render(t, "\n\n  {{ .Nope }}", FileContext{}, "x.txt")
	if err == nil || !strings.HasPrefix(err.Error(), "template: t:3:5") || !strings.Contains(err.Error(), "Nope") {
		t.Errorf("missing field: %v", err)
	}
	if _, _, err = render(t, "{{ .Map.k }}", map[string]any{"Map": map[string]string{}}, "x.txt"); err == nil {
		t.Error("missing map key rendered")
	}
	_, err = Parse("t", "\n{{ if }}")
	if err == nil || !strings.Contains(err.Error(), "t:2:") {
		t.Errorf("parse error: %v", err)
	}
}

// A .json output is canonical, a large integer intact; anything else is
// written as rendered.
func TestCanonicalJSON(t *testing.T) {
	source := "{\"z\": 1,   \"n\": 12345678901234567890, \"a\": {\"y\": [1, 2], \"x\": \"<&>\"}}\n\n"
	want := "{\n  \"a\": {\n    \"x\": \"<&>\",\n    \"y\": [\n      1,\n      2\n    ]\n  },\n  \"n\": 12345678901234567890,\n  \"z\": 1\n}\n"
	if got := mustRender(t, source, nil, `C:\x\out.JSON`); got != want {
		t.Errorf("canonical =\n%s\nwant\n%s", got, want)
	}
	if got := mustRender(t, source, nil, "out.toml"); got != source {
		t.Errorf("verbatim = %q", got)
	}
	for _, bad := range []string{"{", "{} {}", "[1,]", "x"} {
		if _, _, err := render(t, bad, nil, "out.json"); err == nil || !strings.Contains(err.Error(), "out.json") {
			t.Errorf("%q to a .json path: %v", bad, err)
		}
	}
}

// Whitespace-only output is empty, whatever the path, so a settings
// template can remove its file with {{ if .Items }}.
func TestEmptyOutput(t *testing.T) {
	for _, path := range []string{"out.json", "out.txt"} {
		out, read, err := render(t, "{{ if .Items }}x{{ end }}\n  \n", FileContext{}, path)
		if err != nil || out != nil || read == nil {
			t.Errorf("%s: %q, %v, %v", path, out, read, err)
		}
	}
}

// The caps and the timeout bound what a template can cost.
func TestLimits(t *testing.T) {
	if _, err := Parse("t", strings.Repeat("x", MaxTemplate+1)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("oversized template: %v", err)
	}
	if _, err := Parse("t", strings.Repeat("x", MaxTemplate)); err != nil {
		t.Errorf("template at the limit: %v", err)
	}
	big := make([]string, 1025)
	_, _, err := render(t, `{{ range .Items }}`+strings.Repeat("x", 1024)+`{{ end }}`, FileContext{Items: make([]Item, len(big))}, "x.txt")
	// The writer's error comes back from Execute bare, so Render names the
	// template; Execute's own errors already do (TestMissingKeyAndPositions).
	if !errors.Is(err, errTooLarge) || !strings.HasPrefix(err.Error(), "t: output over") {
		t.Errorf("oversized output: %v", err)
	}
}

// A render past the timeout is abandoned and its goroutine ends at its next
// write, fact or cond call. The bubble's clock fires the timeout once the
// render blocks on the channel; the send then wakes it after the timeout,
// and Test waits for the goroutine to exit, so one that kept ranging would
// fail the test as a deadlock. Run under -race: stop and the checks are on
// different goroutines.
func TestTimeout(t *testing.T) {
	for name, source := range map[string]string{
		"write": `{{ range .C }}{{ . }}{{ end }}`,
		"fact":  `{{ range .C }}{{ if fact . }}{{ end }}{{ end }}`,
		"cond":  `{{ range .C }}{{ if cond . }}{{ end }}{{ end }}`,
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := make(chan string)
				_, _, err := render(t, source, map[string]any{"C": c}, "x.txt")
				if err == nil || !strings.Contains(err.Error(), "t: render took longer than") {
					t.Errorf("slow render: %v", err)
				}
				c <- "laptop" // a bool fact, so only the stop ends the loop
			})
		})
	}
	w := &limitedWriter{}
	w.stop()
	if _, err := w.Write([]byte("x")); !errors.Is(err, errTimeout) {
		t.Errorf("stopped writer accepted a write: %v", err)
	}
}

// Templates cannot reach files or the network: the function set is exactly
// json, fact, cond and the value builders, plus text/template's own.
func TestFunctionSet(t *testing.T) {
	for _, source := range []string{`{{ readFile "x" }}`, `{{ include "x" }}`, `{{ env "HOME" }}`} {
		if _, err := Parse("t", source); err == nil {
			t.Errorf("%s parsed", source)
		}
	}
}
