// Package render is the template engine behind the files sofcat writes for a
// placement and, later, the templates a placed tree ships (zip-items guide,
// "Templates for placement files"). Templates are Go text/template with
// missingkey=error and three functions: json, fact and cond. Facts come from
// pkg/conditions, never from pkg/facts, so makecatalogs can render the same
// templates against sample facts without the platform code.
package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"text/template"
	"text/template/parse"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
)

// Limits. A template over MaxTemplate does not parse; an output over
// MaxOutput, or a render past Timeout, fails.
const (
	MaxTemplate = 256 << 10
	MaxOutput   = 1 << 20
	Timeout     = 5 * time.Second
)

// timeout is Timeout; tests run it on testing/synctest's clock.
var timeout = Timeout

// Template is a parsed template whose literal cond expressions passed
// conditions.Inputs, so the validator and the device reject the same ones.
type Template struct {
	name string
	t    *template.Template
}

// Parse parses source as the template errors call name. It rejects a source
// over MaxTemplate and a literal cond expression with a syntax error or a
// call to a time-dependent builtin such as now().
func Parse(name, source string) (*Template, error) {
	if len(source) > MaxTemplate {
		return nil, fmt.Errorf("%s: %d bytes, over the %d-byte limit", name, len(source), MaxTemplate)
	}
	// The real functions are bound per render (Render); these fix the names
	// and arities so parsing can check calls.
	t, err := template.New(name).Option("missingkey=error").Funcs(template.FuncMap{
		"json": func(any) (string, error) { return "", nil },
		"fact": func(string) (any, error) { return nil, nil },
		"cond": func(string) (bool, error) { return false, nil },
	}).Parse(source)
	if err != nil {
		return nil, err
	}
	for _, tmpl := range t.Templates() {
		if tmpl.Tree == nil {
			continue
		}
		if err := checkConds(tmpl.Tree, tmpl.Root); err != nil {
			return nil, err
		}
	}
	return &Template{name: name, t: t}, nil
}

// checkConds walks the parse tree for cond calls with a literal expression
// and checks each with conditions.Inputs.
func checkConds(tree *parse.Tree, node parse.Node) error {
	if cmd, ok := node.(*parse.CommandNode); ok {
		if err := checkCondCall(tree, cmd); err != nil {
			return err
		}
	}
	for _, child := range children(node) {
		if err := checkConds(tree, child); err != nil {
			return err
		}
	}
	return nil
}

// checkCondCall checks cmd when it is cond with a literal expression.
func checkCondCall(tree *parse.Tree, cmd *parse.CommandNode) error {
	ident, ok := cmd.Args[0].(*parse.IdentifierNode)
	if !ok || ident.Ident != "cond" || len(cmd.Args) != 2 {
		return nil
	}
	lit, ok := cmd.Args[1].(*parse.StringNode)
	if !ok {
		return nil
	}
	if _, err := conditions.Inputs(lit.Text); err != nil {
		location, _ := tree.ErrorContext(cmd)
		return fmt.Errorf("template: %s: cond %q: %w", location, lit.Text, err)
	}
	return nil
}

// children returns the nodes under node that can hold a command.
func children(node parse.Node) []parse.Node {
	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return nil
		}
		return n.Nodes
	case *parse.ActionNode:
		return []parse.Node{n.Pipe}
	case *parse.PipeNode:
		if n == nil {
			return nil
		}
		out := make([]parse.Node, len(n.Cmds))
		for i, c := range n.Cmds {
			out[i] = c
		}
		return out
	case *parse.CommandNode:
		return n.Args
	case *parse.IfNode:
		return branch(&n.BranchNode)
	case *parse.RangeNode:
		return branch(&n.BranchNode)
	case *parse.WithNode:
		return branch(&n.BranchNode)
	case *parse.TemplateNode:
		return []parse.Node{n.Pipe}
	}
	return nil
}

func branch(b *parse.BranchNode) []parse.Node {
	return []parse.Node{b.Pipe, b.List, b.ElseList}
}

// Render executes the template with data and facts and returns the output
// with the facts it read: every name passed to fact and every fact a cond
// expression named, with their values. path is where the output goes: one
// ending in .json must be JSON and comes back canonical (Canonical); any
// other is verbatim. An output that is empty or only whitespace is returned
// as nil, so a placement file template can decide to remove its file. The error
// names the template and, when the engine reports them, the line and column.
//
// text/template has no cancellation, so a render that outlives the timeout
// is abandoned: its goroutine runs until its next write or fact or cond
// call, each of which fails once the timeout has passed. A template that
// loops without writing or calling a function runs on, which is the limit of
// what the engine can enforce.
func (t *Template) Render(data any, facts conditions.Facts, path string) ([]byte, conditions.Facts, error) {
	read := conditions.Facts{}
	tmpl, err := t.t.Clone()
	if err != nil {
		return nil, nil, err
	}
	var out limitedWriter
	tmpl.Funcs(template.FuncMap{
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
	})

	done := make(chan error, 1)
	go func() { done <- tmpl.Execute(&out, data) }()
	select {
	case err = <-done:
	case <-time.After(timeout):
		out.stop()
		return nil, nil, fmt.Errorf("%s: render took longer than %s", t.name, timeout)
	}
	if err != nil {
		// Execute prefixes its own errors with the template name; a writer's
		// error, the output cap for one, comes back bare.
		if !strings.HasPrefix(err.Error(), "template: "+t.name+":") {
			err = fmt.Errorf("%s: %w", t.name, err)
		}
		return nil, nil, err
	}
	if strings.TrimSpace(out.buf.String()) == "" {
		return nil, read, nil
	}
	result := out.buf.Bytes()
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		if result, err = Canonical(result); err != nil {
			return nil, nil, fmt.Errorf("%s: output for %s: %w", t.name, path, err)
		}
	}
	return result, read, nil
}

// cond evaluates expression with conditions.Evaluate, so the semantics are
// those of conditional_items, and records the facts it named in read.
func cond(expression string, facts, read conditions.Facts) (bool, error) {
	names, err := conditions.Inputs(expression)
	if err != nil {
		return false, fmt.Errorf("cond %q: %w", expression, err)
	}
	result, missing, err := conditions.Evaluate(expression, facts)
	switch {
	case err != nil:
		return false, fmt.Errorf("cond %q: %w", expression, err)
	case result == conditions.Unknown:
		return false, fmt.Errorf("cond %q: no fact named %s", expression, strings.Join(missing, ", "))
	}
	for _, name := range names {
		read[name] = facts[name]
	}
	return result == conditions.True, nil
}

// jsonLiteral is the json function: v as a JSON literal, HTML left alone.
func jsonLiteral(v any) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(JSONValue(v)); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// JSONValue converts an untyped YAML value for encoding/json, which cannot
// encode a map with non-string keys.
func JSONValue(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = JSONValue(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[fmt.Sprint(k)] = JSONValue(e)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = JSONValue(e)
		}
		return out
	default:
		return v
	}
}

// Canonical re-encodes one JSON value: object keys sorted, two-space
// indent, numbers as written, a trailing newline and no HTML escaping, so
// the bytes do not depend on the template's whitespace. Anything that is not
// exactly one JSON value is an error.
func Canonical(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("not JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("not JSON: data after the first value")
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

var (
	errTooLarge = fmt.Errorf("output over the %d-byte limit", MaxOutput)
	errTimeout  = errors.New("render timed out")
)

// limitedWriter collects the output up to MaxOutput and fails every write
// after stop, so an abandoned render ends. Render calls stop from outside
// Execute's goroutine, hence the atomic.
type limitedWriter struct {
	buf     bytes.Buffer
	stopped atomic.Bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.stopped.Load() {
		return 0, errTimeout
	}
	if w.buf.Len()+len(p) > MaxOutput {
		return 0, errTooLarge
	}
	return w.buf.Write(p)
}

func (w *limitedWriter) stop() { w.stopped.Store(true) }
