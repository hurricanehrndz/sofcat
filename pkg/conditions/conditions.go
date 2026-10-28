// Package conditions decides which catalog items may be installed on this
// machine. Each item's installable_condition is an expr expression
// (github.com/expr-lang/expr) evaluated against facts that pkg/facts
// gathers. See docs/conditions.md. It has no platform code, so makecatalogs
// can check expressions.
package conditions

import (
	"fmt"
	"strings"

	"github.com/expr-lang/expr"
	goversion "github.com/hashicorp/go-version"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// Facts maps a fact name to its value: a string, number, bool, or a list of
// them for the built-in facts; whatever JSON value a script printed for admin
// facts.
type Facts map[string]any

// Eval reports whether condition holds for facts. An empty condition holds.
// A condition that names an unknown fact, fails to evaluate, or is not a
// boolean is an error; callers treat an error as "not installable", so a typo
// hides an item rather than installing it everywhere.
func Eval(condition string, facts Facts) (bool, error) {
	if strings.TrimSpace(condition) == "" {
		return true, nil
	}
	program, err := expr.Compile(condition, append(versionOptions(), expr.Env(map[string]any(facts)), expr.AsBool())...)
	if err != nil {
		return false, err
	}
	out, err := expr.Run(program, map[string]any(facts))
	if err != nil {
		return false, err
	}
	ok, isBool := out.(bool)
	if !isBool {
		return false, fmt.Errorf("condition returned %T, not a boolean", out)
	}
	return ok, nil
}

// Apply evaluates every item's installable_condition against facts and sets
// NotInstallable on the items it rejects, so the lookups that choose an item
// to install skip them.
func Apply(catalogs map[int]map[string]catalog.Item, facts Facts) {
	for _, items := range catalogs {
		for name, item := range items {
			if item.InstallableCondition == "" {
				continue
			}
			ok, err := Eval(item.InstallableCondition, facts)
			switch {
			case err != nil:
				item.NotInstallable = fmt.Sprintf("installable_condition %q cannot be evaluated: %v", item.InstallableCondition, err)
			case !ok:
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
