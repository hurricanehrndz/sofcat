package render

import (
	"errors"
	"fmt"
	"reflect"
	"text/template"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
)

// funcs is the function set of one render: json, fact and cond, and the
// value builders dict, set, list, append, default and hasKey, which take
// their names, argument order and results from sprig's functions of the same
// names (zip-items guide, "Functions"). Sprig itself is not imported: its
// env, now and rand functions would make a render depend on more than its
// inputs. Parse binds the same set, so a name outside it fails to parse.
// fact and cond read facts and record in read; they and nothing else fail
// once out has stopped.
func funcs(out *limitedWriter, facts, read conditions.Facts) template.FuncMap {
	// The dicts this render built, by map pointer. set changes a map in
	// place, and .Placement.Vars and a fact's value outlive the render.
	built := map[uintptr]bool{}
	return template.FuncMap{
		"json": jsonLiteral,
		"fact": func(name string) (any, error) {
			if out.stopped.Load() {
				return nil, errTimeout
			}
			v, ok := facts[name]
			if !ok {
				return nil, fmt.Errorf("fact %q: no such fact", name)
			}
			read[name] = v
			return v, nil
		},
		"cond": func(expression string) (bool, error) {
			if out.stopped.Load() {
				return false, errTimeout
			}
			return cond(expression, facts, read)
		},
		"dict": func(pairs ...any) (map[string]any, error) {
			if len(pairs)%2 != 0 {
				return nil, errors.New("dict: a key without a value")
			}
			d := make(map[string]any, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				key, ok := pairs[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %v is not a string", pairs[i])
				}
				d[key] = pairs[i+1]
			}
			built[reflect.ValueOf(d).Pointer()] = true
			return d, nil
		},
		"set": func(d map[string]any, key string, value any) (map[string]any, error) {
			if !built[reflect.ValueOf(d).Pointer()] {
				return nil, fmt.Errorf("set %q: not a dict this template built with dict", key)
			}
			d[key] = value
			return d, nil
		},
		"list": func(values ...any) []any {
			return append([]any{}, values...)
		},
		"append": func(list, value any) ([]any, error) {
			l := reflect.ValueOf(list)
			if list == nil || l.Kind() != reflect.Slice && l.Kind() != reflect.Array {
				return nil, fmt.Errorf("append: %T is not a list", list)
			}
			out := make([]any, 0, l.Len()+1)
			for i := range l.Len() {
				out = append(out, l.Index(i).Interface())
			}
			return append(out, value), nil
		},
		"default": func(fallback, value any) any {
			if truth, _ := template.IsTrue(value); truth {
				return value
			}
			return fallback
		},
		"hasKey": func(d map[string]any, key string) bool {
			_, ok := d[key]
			return ok
		},
	}
}
