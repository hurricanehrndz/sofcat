package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
)

// MaxTreeFile is the largest file ReadJSON reads.
const MaxTreeFile = 1 << 20

// WithTree returns the item with tree as what HasFile and ReadJSON look in,
// instead of the directory at Path: makecatalogs -check passes the item's
// archive, since nothing is placed there.
func (i Item) WithTree(tree fs.FS) Item {
	i.tree = tree
	return i
}

// HasFile reports whether the item's tree holds a regular file at name, a
// slash-separated path relative to the tree: {{ if $p.HasFile "x/y.json" }}.
func (i Item) HasFile(name string) (bool, error) {
	var regular bool
	err := i.inTree(name, func(tree fs.FS) error {
		info, err := fs.Stat(tree, name)
		regular = err == nil && info.Mode().IsRegular()
		return err
	})
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return regular, err
}

// ReadJSON returns the JSON value in the tree's file at name, a path as for
// HasFile, with whole numbers as ints, like .Placement.Vars. A missing file,
// one over MaxTreeFile or one that is not exactly one JSON value fails the
// render.
func (i Item) ReadJSON(name string) (any, error) {
	var v any
	err := i.inTree(name, func(tree fs.FS) error {
		f, err := tree.Open(name)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		raw, err := io.ReadAll(io.LimitReader(f, MaxTreeFile+1))
		if err != nil {
			return err
		}
		if len(raw) > MaxTreeFile {
			return fmt.Errorf("over the %d-byte limit", MaxTreeFile)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("not JSON: %w", err)
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return errors.New("not JSON: data after the first value")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return Integral(v), nil
}

// inTree calls fn with the item's tree and names the item and the file in
// any error. Without WithTree the tree is the directory at Path, opened as
// an os.Root: the service renders as root or SYSTEM, and a link inside a
// tree must not make it read a file outside.
func (i Item) inTree(name string, fn func(fs.FS) error) error {
	err := func() error {
		if !fs.ValidPath(name) || name == "." {
			return errors.New("not a slash-separated path inside the tree")
		}
		if i.tree != nil {
			return fn(i.tree)
		}
		root, err := os.OpenRoot(i.Path)
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }()
		return fn(root.FS())
	}()
	if err != nil {
		return fmt.Errorf("item %q: %q: %w", i.Name, name, err)
	}
	return nil
}

// Integral turns every whole float64 in a JSON value into an int, in place,
// so a value reads the same from YAML and from JSON, which decodes every
// number as float64: the service, the helper and makecatalogs -check render
// alike. YAML 3.0 is therefore 3. A float beyond 2^53 is left alone.
func Integral(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, e := range v {
			v[k] = Integral(e)
		}
	case []any:
		for i, e := range v {
			v[i] = Integral(e)
		}
	case float64:
		if v == math.Trunc(v) && math.Abs(v) <= 1<<53 {
			return int(v)
		}
	}
	return v
}
