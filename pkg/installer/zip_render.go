package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
)

// treeRenderer is the seam between placing a tree and the templates it ships
// (zip-items guide, "Installing"). The installer asks it what to render and
// writes the answer itself, so an in-place re-render is all or nothing
// whatever the renderer does. Until tree templates land, noTemplates is the
// only implementation.
type treeRenderer interface {
	// render renders item's templates in tree, item's tree for t, staged or
	// placed, to memory: one output per template, in path order, with the
	// record the receipt keeps. managed is the flag the receipt will record.
	// It writes nothing.
	render(item catalog.Item, t Target, tree string, managed bool) ([]rendered, error)
	// inputsMatch reports whether rc's rendered records still describe what
	// render would produce for tree now, with managed as the item's flag:
	// every record's inputs equal, compared as canonical JSON (inputsEqual),
	// and every template in the tree recorded (templatesRecorded).
	inputsMatch(item catalog.Item, t Target, tree string, rc receipt.Receipt, managed bool) (bool, error)
}

// rendered is one rendered file: its receipt record and the bytes to write
// at File.
type rendered struct {
	receipt.Rendered
	data []byte
}

// noTemplates renders nothing, so a tree is placed as shipped and every
// receipt's inputs match.
type noTemplates struct{}

func (noTemplates) render(catalog.Item, Target, string, bool) ([]rendered, error) {
	return nil, nil
}

func (noTemplates) inputsMatch(catalog.Item, Target, string, receipt.Receipt, bool) (bool, error) {
	return true, nil
}

// records returns the receipt records of outs, never nil.
func records(outs []rendered) []receipt.Rendered {
	recs := make([]receipt.Rendered, len(outs))
	for i, out := range outs {
		recs[i] = out.Rendered
	}
	return recs
}

// canonicalJSON re-encodes raw with object keys sorted and numbers as
// written, so two encodings of one value compare equal.
func canonicalJSON(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// inputsEqual reports whether stored, a rendered record's inputs as the
// receipt holds them, equals live, the value this run would record, as
// canonical JSON: an int or a list that round-tripped through the receipt
// compares equal to its live value.
func inputsEqual(stored json.RawMessage, live any) (bool, error) {
	liveJSON, err := json.Marshal(live)
	if err != nil {
		return false, err
	}
	a, err := canonicalJSON(stored)
	if err != nil {
		return false, fmt.Errorf("rendered inputs: %w", err)
	}
	b, err := canonicalJSON(liveJSON)
	if err != nil {
		return false, err
	}
	return bytes.Equal(a, b), nil
}

// templatesRecorded reports whether every .tmpl regular file in tree has a
// record in rendered naming it as the template. One without is a template
// nothing has rendered yet, so its inputs count as changed.
func templatesRecorded(tree, goos string, rendered []receipt.Rendered) (bool, error) {
	recorded := make(map[string]bool, len(rendered))
	for _, r := range rendered {
		recorded[r.Template] = true
	}
	tr, err := artifact.Read(tree, goos)
	if err != nil {
		return false, err
	}
	for _, p := range tr.Paths() {
		if strings.HasSuffix(p, templateSuffix) && !recorded[p] {
			return false, nil
		}
	}
	return true, nil
}

// templateSuffix marks a file in a tree as a template for the file of the
// same name without it.
const templateSuffix = ".tmpl"

// writeRendered writes outs into tree, all or nothing: each file goes to a
// temporary file beside its target and is renamed over it, and when one
// fails every file already replaced gets its previous bytes and mode back
// (one that did not exist is removed) before the error returns, so the tree
// still matches its receipt. On success undo does the same, for a caller
// that cannot finish what the render began (the rehash or the receipt). A
// crash between two renames is the one window that leaves a tree matching
// neither hash (zip-items guide, "Receipts").
func writeRendered(tree string, outs []rendered) (undo func() error, err error) {
	for _, out := range outs {
		for _, seg := range strings.Split(out.File, "/") {
			if segErr := checkSegment(seg); segErr != nil {
				return nil, fmt.Errorf("rendered file %q: %w", out.File, segErr)
			}
		}
	}
	type previous struct {
		path    string
		data    []byte
		mode    fs.FileMode
		existed bool
	}
	var replaced []previous
	restore := func() error {
		var errs error
		for i := len(replaced) - 1; i >= 0; i-- {
			p := replaced[i]
			var restoreErr error
			if p.existed {
				restoreErr = writeFileAtomic(p.path, p.data, p.mode)
			} else {
				restoreErr = os.Remove(p.path)
			}
			if restoreErr != nil {
				errs = errors.Join(errs, fmt.Errorf("restore %s: %w", p.path, restoreErr))
			}
		}
		return errs
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, restore())
		}
	}()
	for _, out := range outs {
		prev := previous{path: filepath.Join(tree, filepath.FromSlash(out.File)), mode: 0o644}
		info, statErr := os.Lstat(prev.path)
		switch {
		case errors.Is(statErr, fs.ErrNotExist):
		case statErr != nil:
			return nil, statErr
		case !info.Mode().IsRegular():
			return nil, fmt.Errorf("rendered file %s is not a regular file", prev.path)
		default:
			prev.existed, prev.mode = true, info.Mode().Perm()
			if prev.data, err = os.ReadFile(prev.path); err != nil {
				return nil, err
			}
		}
		if err = writeFileAtomic(prev.path, out.data, prev.mode); err != nil {
			return nil, err
		}
		replaced = append(replaced, prev)
	}
	return restore, nil
}
