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
	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/harness"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// treeRenderer is the seam between placing a tree and the templates it ships
// (zip-items guide, "Tree templates"). The installer asks it what to render
// and writes the answer itself, so an in-place re-render is all or nothing
// whatever the renderer does. treeTemplates is the renderer; tests
// substitute their own.
type treeRenderer interface {
	// render renders item's templates in dir, item's tree for t staged or
	// placed, to memory: one output per template, in path order, with the
	// record the receipt keeps. root is the placement root, which the
	// context names the tree's final path under; managed is the flag the
	// receipt will record. It writes nothing.
	render(item catalog.Item, t Target, root, dir string, managed bool) ([]rendered, error)
	// inputsMatch reports whether rc's rendered records still describe what
	// render would produce for tree, under root, now, with managed as the
	// item's flag: every record's inputs equal, compared as canonical JSON
	// (inputsEqual), and every template in the tree recorded
	// (templatesRecorded).
	inputsMatch(item catalog.Item, t Target, root, tree string, rc receipt.Receipt, managed bool) (bool, error)
}

// rendered is one rendered file: its receipt record and the bytes to write
// at File.
type rendered struct {
	receipt.Rendered
	data []byte
}

// treeTemplates renders the .tmpl files a tree ships (artifact.Templates)
// with render.TreeContext, the installer's facts and, in user scope, its
// user. The device reads templates from the tree it is placing and nowhere
// else.
type treeTemplates struct{ z *ZipInstaller }

// treeInputs is what a rendered record stores as inputs: the context fields
// a template can read and, of the facts, only those the render read, so a
// change to any other fact leaves the file alone.
type treeInputs struct {
	OS    string           `json:"os"`
	Scope string           `json:"scope"`
	User  string           `json:"user"`
	Item  treeInputsItem   `json:"item"`
	Facts conditions.Facts `json:"facts"`
}

type treeInputsItem struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Managed bool   `json:"managed"`
}

// context is the tree context for item at t under root with managed as its
// flag. User is set in user scope only; the service, which runs in machine
// scope, has none to set.
func (r treeTemplates) context(item catalog.Item, t Target, root string, managed bool) render.TreeContext {
	goos := r.z.Guard.GOOS
	ctx := render.TreeContext{
		OS:        goos,
		Scope:     t.Spec.ScopeFor(goos),
		Placement: t.Spec.View(t.Placement, root),
		Item: render.TreeItem{
			Name:     item.Name,
			Version:  item.Version,
			Managed:  managed,
			Path:     treeIn(t.Spec, root, item.Name),
			TreeHash: t.Variant.TreeHash,
		},
	}
	if ctx.Scope == harness.ScopeUser {
		ctx.User = r.z.User
	}
	return ctx
}

// inputs is the record of a render of ctx that read the facts in read.
func inputs(ctx render.TreeContext, read conditions.Facts) treeInputs {
	return treeInputs{
		OS:    ctx.OS,
		Scope: ctx.Scope,
		User:  ctx.User,
		Item:  treeInputsItem{Name: ctx.Item.Name, Version: ctx.Item.Version, Managed: ctx.Item.Managed},
		Facts: read,
	}
}

func (r treeTemplates) render(item catalog.Item, t Target, root, dir string, managed bool) ([]rendered, error) {
	templates, err := treeTemplatesIn(dir, r.z.Guard.GOOS)
	if err != nil {
		return nil, err
	}
	ctx := r.context(item, t, root, managed)
	var outs []rendered
	for _, p := range templates {
		out, err := r.renderOne(ctx, dir, p)
		if err != nil {
			return nil, err
		}
		outs = append(outs, out)
	}
	return outs, nil
}

// renderOne renders template p in dir to memory. A template that does not
// parse or render refuses the tree; the error names the template.
func (r treeTemplates) renderOne(ctx render.TreeContext, dir, p string) (rendered, error) {
	src, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
	if err != nil {
		return rendered{}, err
	}
	tmpl, err := render.Parse(p, string(src))
	if err != nil {
		return rendered{}, fmt.Errorf("%w: %w", placement.ErrRefused, err)
	}
	file := strings.TrimSuffix(p, artifact.TemplateSuffix)
	data, read, err := tmpl.Render(ctx, r.z.Facts, file)
	if err != nil {
		return rendered{}, fmt.Errorf("%w: %w", placement.ErrRefused, err)
	}
	if data == nil && strings.HasSuffix(strings.ToLower(file), ".json") {
		return rendered{}, fmt.Errorf("%w: %s: rendered nothing for %s", placement.ErrRefused, p, file)
	}
	raw, err := json.Marshal(inputs(ctx, read))
	if err != nil {
		return rendered{}, err
	}
	return rendered{Rendered: receipt.Rendered{File: file, Template: p, Inputs: raw}, data: data}, nil
}

// inputsMatch compares each record's stored inputs with the current os,
// scope, user and item fields and, for each fact the record stored, that
// fact's current value only. A stored fact the machine no longer has leaves
// the live side without it, so the record differs and the re-render fails
// naming the fact, to be retried next run.
func (r treeTemplates) inputsMatch(item catalog.Item, t Target, root, tree string, rc receipt.Receipt, managed bool) (bool, error) {
	if ok, err := templatesRecorded(tree, r.z.Guard.GOOS, rc.Rendered); !ok || err != nil {
		return ok, err
	}
	live := inputs(r.context(item, t, root, managed), nil)
	for _, rec := range rc.Rendered {
		var stored treeInputs
		if err := json.Unmarshal(rec.Inputs, &stored); err != nil {
			return false, fmt.Errorf("receipt of %s: rendered inputs for %s: %w", item.Name, rec.File, err)
		}
		live.Facts = make(conditions.Facts, len(stored.Facts))
		for name := range stored.Facts {
			if v, ok := r.z.Facts[name]; ok {
				live.Facts[name] = v
			}
		}
		if eq, err := inputsEqual(rec.Inputs, live); !eq || err != nil {
			return eq, err
		}
	}
	return true, nil
}

// checkShipped refuses a tree as shipped (staged, or found on disk equal to
// the catalog's tree_hash) that ships a template beside the file it would
// render (artifact.ShippedTemplates); a placed tree holds both by design, so
// a re-render in place does not check it.
func checkShipped(stage, goos string) error {
	tr, err := artifact.Read(stage, goos)
	if err != nil {
		return err
	}
	if _, err := artifact.ShippedTemplates(tr.Paths()); err != nil {
		return fmt.Errorf("%w: %w", placement.ErrRefused, err)
	}
	return nil
}

// treeTemplatesIn lists the templates in dir, a tree for goos. A tree whose
// template names cannot be rendered (artifact.Templates) is refused.
func treeTemplatesIn(dir, goos string) ([]string, error) {
	tr, err := artifact.Read(dir, goos)
	if err != nil {
		return nil, err
	}
	templates, err := artifact.Templates(tr.Paths())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", placement.ErrRefused, err)
	}
	return templates, nil
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

// templatesRecorded reports whether every template in tree has a record in
// rendered naming it as the template. One without is a template nothing has
// rendered yet, so its inputs count as changed.
func templatesRecorded(tree, goos string, rendered []receipt.Rendered) (bool, error) {
	recorded := make(map[string]bool, len(rendered))
	for _, r := range rendered {
		recorded[r.Template] = true
	}
	templates, err := treeTemplatesIn(tree, goos)
	if err != nil {
		return false, err
	}
	for _, p := range templates {
		if !recorded[p] {
			return false, nil
		}
	}
	return true, nil
}

// writeRendered writes outs into tree, all or nothing: each file goes to a
// temporary file beside its target and is renamed over it, and when one
// fails every file already replaced gets its previous bytes and mode back
// (one that did not exist is removed) before the error returns, so the tree
// still matches its receipt. An output whose bytes the target already holds
// is left alone, so a re-render touches only the files an input changed. On
// success undo does the same, for a caller that cannot finish what the
// render began (the rehash or the receipt). A crash between two renames is
// the one window that leaves a tree matching neither hash (zip-items guide,
// "Receipts").
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
			if bytes.Equal(prev.data, out.data) {
				continue
			}
		}
		if err = writeFileAtomic(prev.path, out.data, prev.mode); err != nil {
			return nil, err
		}
		replaced = append(replaced, prev)
	}
	return restore, nil
}
