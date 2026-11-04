// Package conditions decides which catalog items may be installed on this
// machine. Each item's installable_condition is an expr expression
// (github.com/expr-lang/expr) evaluated against facts that pkg/facts
// gathers. See docs/conditions.md. It has no platform code, so makecatalogs
// can check expressions.
package conditions

import (
	"fmt"
	"slices"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	goversion "github.com/hashicorp/go-version"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// Facts maps a fact name to its value: a string, number, bool, or a list of
// them for the built-in facts; whatever JSON value a script printed for admin
// facts.
type Facts map[string]any

// Result is a condition's outcome. Unknown means the condition names a fact
// this machine does not have, so it can be neither honoured nor refused.
type Result int

const (
	False Result = iota
	True
	Unknown
)

func (r Result) String() string {
	switch r {
	case True:
		return "true"
	case Unknown:
		return "unknown"
	case False:
		return "false"
	}
	return fmt.Sprintf("Result(%d)", int(r))
}

// Evaluate decides condition against facts. An empty condition is True. A
// condition that names an identifier absent from facts is Unknown, and missing
// lists those names. A syntax error, a runtime error, or a non-boolean result
// is False with the error, so a typo hides an item rather than installing it
// everywhere.
func Evaluate(condition string, facts Facts) (_ Result, missing []string, _ error) {
	if strings.TrimSpace(condition) == "" {
		return True, nil, nil
	}
	parsed, err := expr.Compile(condition, append(versionOptions(), expr.AllowUndefinedVariables())...)
	if err != nil {
		return False, nil, err
	}
	if missing := missingFacts(parsed.Node(), facts); len(missing) > 0 {
		return Unknown, missing, nil
	}
	program, err := expr.Compile(condition, append(versionOptions(), expr.Env(map[string]any(facts)), expr.AsBool())...)
	if err != nil {
		return False, nil, err
	}
	out, err := expr.Run(program, map[string]any(facts))
	if err != nil {
		return False, nil, err
	}
	ok, isBool := out.(bool)
	if !isBool {
		return False, nil, fmt.Errorf("condition returned %T, not a boolean", out)
	}
	if !ok {
		return False, nil, nil
	}
	return True, nil, nil
}

// missingFacts returns, sorted, the identifiers in node that facts lacks.
// Called functions (version and the version operators) and let-bound names
// are not facts; expr builtins such as now() and any() are not identifiers.
func missingFacts(node ast.Node, facts Facts) []string {
	v := identifiers{used: map[string]bool{}, local: map[string]bool{}}
	ast.Walk(&node, v)
	var missing []string
	for name := range v.used {
		if _, ok := facts[name]; !ok && !v.local[name] {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}

type identifiers struct {
	used, local map[string]bool
}

func (v identifiers) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.IdentifierNode:
		// $env is expr's built-in handle on the whole environment, not a fact.
		if n.Value != "$env" {
			v.used[n.Value] = true
		}
	case *ast.CallNode:
		if callee, ok := n.Callee.(*ast.IdentifierNode); ok {
			v.local[callee.Value] = true
		}
	case *ast.VariableDeclaratorNode:
		v.local[n.Name] = true
	}
}

// Apply evaluates every item's installable_condition against facts and sets
// NotInstallable on the items it rejects, so the lookups that choose an item
// to install skip them. Unknown rejects an item like false: the item is not
// installable here until the machine has the facts it names.
func Apply(catalogs map[int]map[string]catalog.Item, facts Facts) {
	for _, items := range catalogs {
		for name, item := range items {
			if item.InstallableCondition == "" {
				continue
			}
			result, missing, err := Evaluate(item.InstallableCondition, facts)
			switch {
			case err != nil:
				item.NotInstallable = fmt.Sprintf("installable_condition %q cannot be evaluated: %v", item.InstallableCondition, err)
			case result == Unknown:
				item.NotInstallable = fmt.Sprintf("installable_condition %q needs facts this machine does not have: %s", item.InstallableCondition, strings.Join(missing, ", "))
			case result == False:
				item.NotInstallable = fmt.Sprintf("installable_condition %q is false", item.InstallableCondition)
			default:
				item.NotInstallable = ""
			}
			items[name] = item
		}
	}
}

// versionOptions add version(s), which parses a dotted version, and overload
// the comparison operators so version(a) >= version(b), or
// version(a) >= "1.2", compare numerically: as text "10.0.9" > "10.0.22631".
// A leading "v" and anything from the first "-" or "+" on are dropped, so a
// git-describe build such as v1.4.0-3-gabc123 compares as 1.4.0.
func versionOptions() []expr.Option {
	versionType := new(func(string) *goversion.Version)
	opts := []expr.Option{
		expr.Function("version", func(params ...any) (any, error) {
			return asVersion(params[0])
		}, versionType),
	}
	compare := map[string]func(int) bool{
		"==": func(c int) bool { return c == 0 },
		"!=": func(c int) bool { return c != 0 },
		"<":  func(c int) bool { return c < 0 },
		"<=": func(c int) bool { return c <= 0 },
		">":  func(c int) bool { return c > 0 },
		">=": func(c int) bool { return c >= 0 },
	}
	names := map[string]string{"==": "versionEq", "!=": "versionNe", "<": "versionLt", "<=": "versionLe", ">": "versionGt", ">=": "versionGe"}
	for op, holds := range compare {
		opts = append(opts,
			expr.Function(names[op], func(params ...any) (any, error) {
				a, err := asVersion(params[0])
				if err != nil {
					return nil, err
				}
				b, err := asVersion(params[1])
				if err != nil {
					return nil, err
				}
				return holds(a.Compare(b)), nil
			},
				new(func(*goversion.Version, *goversion.Version) bool),
				new(func(*goversion.Version, string) bool),
				new(func(string, *goversion.Version) bool),
			),
			expr.Operator(op, names[op]),
		)
	}
	return opts
}

func asVersion(v any) (*goversion.Version, error) {
	switch v := v.(type) {
	case string:
		return parseVersion(v)
	case *goversion.Version:
		return v, nil
	}
	return nil, fmt.Errorf("not a version: %v", v)
}

func parseVersion(s string) (*goversion.Version, error) {
	core, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(s), "v"), "-")
	core, _, _ = strings.Cut(core, "+")
	v, err := goversion.NewVersion(core)
	if err != nil {
		return nil, fmt.Errorf("not a version: %q", s)
	}
	return v, nil
}

// Check reports a syntax error in condition without facts, for tools that
// build catalogs. A condition that passes can still fail on a machine, for
// example by naming a fact the machine does not have.
func Check(condition string) error {
	if strings.TrimSpace(condition) == "" {
		return nil
	}
	_, err := expr.Compile(condition, append(versionOptions(), expr.AllowUndefinedVariables())...)
	return err
}
