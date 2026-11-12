// Package jsonmerge adds keys to a JSON document that sofcat does not own, and
// takes back only the keys it added. A rendered template gives the keys it
// wants (Leaves); Apply merges them into the user's document, given the keys
// sofcat added on an earlier run. The edit is a byte splice, so everything
// else in the document keeps its order, spacing and formatting. The package
// does no file I/O and keeps no record: the caller reads and writes the
// document and persists the owned keys.
package jsonmerge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
)

// ErrRefused is wrapped by every error from this package. The document or the
// request is something the merge will not touch; nothing was changed.
var ErrRefused = errors.New("jsonmerge: refused")

var errNotObject = errors.New("container is not an object")

// Key is the path of one leaf: one segment for a top-level member, two for a
// member of a top-level object. Segments are compared as unescaped strings and
// never joined, so {"a.b"} and {"a", "b"} are different keys.
type Key []string

// Leaf is a key and the JSON value to add under it.
type Leaf struct {
	Key   Key
	Value json.RawMessage
}

// Leaves parses a rendered template into the keys it wants. The template must
// be a JSON object. A top-level member whose value is not an object is a leaf
// of one segment; the members of a top-level object are leaves of two
// segments, and their values, objects included, are opaque. Empty or
// whitespace-only input has no leaves. Invalid JSON, a root that is not an
// object and a duplicate member name are refused.
func Leaves(rendered []byte) ([]Leaf, error) {
	if isBlank(rendered) {
		return nil, nil
	}
	if !json.Valid(rendered) {
		return nil, fmt.Errorf("%w: template is not valid JSON", ErrRefused)
	}
	root, err := parseObject(rendered, 0)
	if err != nil {
		return nil, err
	}
	var leaves []Leaf
	for _, m := range root.members {
		if rendered[m.valStart] != '{' {
			leaves = append(leaves, Leaf{Key{m.key}, bytes.Clone(rendered[m.valStart:m.valEnd])})
			continue
		}
		obj, err := parseObject(rendered, m.valStart)
		if err != nil {
			return nil, err
		}
		for _, inner := range obj.members {
			leaves = append(leaves, Leaf{Key{m.key, inner.key}, bytes.Clone(rendered[inner.valStart:inner.valEnd])})
		}
	}
	return leaves, nil
}

// Apply merges want into doc, given the keys sofcat added earlier (owned), and
// returns the new document, the keys sofcat now owns, and whether the document
// bytes changed. A wanted key that is absent is added and owned. A wanted key
// that is present is never changed, and stays owned only if it was. An owned
// key that is no longer wanted is deleted whatever its value has become, and a
// key that is not owned is never deleted. Deletes run before adds, so an owned
// scalar can make way for a wanted object of the same name. A container sofcat
// created stays behind as an empty object when its last key is removed.
//
// changed covers the document only. The owned set can change on its own, for
// example when an owned key is already gone from the document, so callers
// always persist nowOwned. When changed is false, out is doc itself.
//
// An empty or missing doc is no document: if anything is added, out is a fresh
// object, indented by two spaces and ending in a newline; if not, nothing is
// created. New members copy the separator and colon spacing of the last member
// of the object they join, so pretty, compact and irregular layouts all keep
// their look.
//
// Apply refuses, returning nil out and an error wrapping ErrRefused, when a key
// segment is not valid UTF-8, when doc is not a single JSON object, when a
// container that an add needs holds something other than an object, when a
// member name appears twice in the root or in a container it touches, or when
// want repeats a key. An owned key whose container is not an object is simply
// gone: it is dropped from the owned set.
//
// Ownership is by key only. If a user deletes an owned key and adds it back
// themselves, sofcat still owns it and will delete it when it leaves want;
// the record cannot tell the two apart.
func Apply(doc []byte, want []Leaf, owned []Key) (out []byte, nowOwned []Key, changed bool, err error) {
	want, err = checkWant(want)
	if err != nil {
		return nil, nil, false, err
	}
	owned, err = checkOwned(owned)
	if err != nil {
		return nil, nil, false, err
	}
	start := doc
	fresh := isBlank(doc)
	if fresh {
		start = []byte(freshDoc)
	} else if !json.Valid(doc) {
		return nil, nil, false, fmt.Errorf("%w: document is not valid JSON", ErrRefused)
	}
	if _, err = parseObject(start, 0); err != nil {
		return nil, nil, false, err
	}

	src := start
	wanted := func(k Key) bool {
		return slices.ContainsFunc(want, func(l Leaf) bool { return slices.Equal(l.Key, k) })
	}
	for _, k := range owned {
		if wanted(k) {
			nowOwned = append(nowOwned, k)
			continue
		}
		if src, err = remove(src, k); err != nil {
			return nil, nil, false, err
		}
	}
	for _, l := range want {
		var added bool
		if src, added, err = add(src, l); err != nil {
			return nil, nil, false, err
		}
		if added && !slices.ContainsFunc(nowOwned, func(k Key) bool { return slices.Equal(k, l.Key) }) {
			nowOwned = append(nowOwned, l.Key)
		}
	}
	slices.SortFunc(nowOwned, slices.Compare[Key])

	if bytes.Equal(src, start) {
		return doc, nowOwned, false, nil
	}
	return src, nowOwned, true, nil
}

const freshDoc = "{\n}\n"

func isBlank(b []byte) bool {
	return len(bytes.Trim(b, " \t\r\n")) == 0
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// validKey allows one or two segments of valid UTF-8: the document would
// store an invalid segment as U+FFFD and never find it again.
func validKey(k Key) bool {
	return (len(k) == 1 || len(k) == 2) && !slices.ContainsFunc(k, func(s string) bool { return !utf8.ValidString(s) })
}

// checkWant validates want and returns a copy with every value compacted, so
// whitespace in a template value never lands in the document.
func checkWant(want []Leaf) ([]Leaf, error) {
	out := make([]Leaf, 0, len(want))
	for i, l := range want {
		if !validKey(l.Key) {
			return nil, fmt.Errorf("%w: key %q must have one or two segments of valid UTF-8", ErrRefused, []string(l.Key))
		}
		var v bytes.Buffer
		if err := json.Compact(&v, l.Value); err != nil {
			return nil, fmt.Errorf("%w: value of %q is not valid JSON", ErrRefused, []string(l.Key))
		}
		for _, o := range want[:i] {
			// A key and its own parent cannot both be wanted: one needs an
			// object where the other needs a value.
			if slices.Equal(o.Key, l.Key) || o.Key[0] == l.Key[0] && (len(o.Key) == 1 || len(l.Key) == 1) {
				return nil, fmt.Errorf("%w: key %q is wanted twice", ErrRefused, []string(l.Key))
			}
		}
		out = append(out, Leaf{l.Key, v.Bytes()})
	}
	return out, nil
}

// checkOwned validates the owned keys and returns them sorted without repeats.
func checkOwned(owned []Key) ([]Key, error) {
	for _, k := range owned {
		if !validKey(k) {
			return nil, fmt.Errorf("%w: owned key %q must have one or two segments of valid UTF-8", ErrRefused, []string(k))
		}
	}
	out := slices.Clone(owned)
	slices.SortFunc(out, slices.Compare[Key])
	return slices.CompactFunc(out, slices.Equal), nil
}

// member is one "key": value pair of an object, as byte offsets into the
// document. keyStart is the opening quote of the key, keyEnd is just past its
// closing quote, and the value spans valStart to valEnd.
type member struct {
	key                                string
	keyStart, keyEnd, valStart, valEnd int
}

// object is a parsed object: open is its brace and closeStart its closing
// brace.
type object struct {
	open, closeStart int
	members          []member
}

func (o object) find(key string) int {
	return slices.IndexFunc(o.members, func(m member) bool { return m.key == key })
}

// parseObject walks the object that starts at src[at], which must already be
// known to be valid JSON, and refuses a duplicate member name.
func parseObject(src []byte, at int) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(src[at:]))
	tok, err := dec.Token()
	if err != nil {
		return object{}, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return object{}, fmt.Errorf("%w: not a JSON object", ErrRefused)
	}
	obj := object{open: at}
	seen := map[string]bool{}
	for dec.More() {
		keyStart := at + int(dec.InputOffset())
		for keyStart < len(src) && isSpace(src[keyStart]) {
			keyStart++
		}
		if src[keyStart] == ',' {
			keyStart++
		}
		for keyStart < len(src) && isSpace(src[keyStart]) {
			keyStart++
		}
		tok, err := dec.Token()
		if err != nil {
			return object{}, fmt.Errorf("%w: %w", ErrRefused, err)
		}
		key, _ := tok.(string)
		keyEnd := at + int(dec.InputOffset())
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return object{}, fmt.Errorf("%w: %w", ErrRefused, err)
		}
		valEnd := at + int(dec.InputOffset())
		if seen[key] {
			return object{}, fmt.Errorf("%w: member %q appears twice", ErrRefused, key)
		}
		seen[key] = true
		obj.members = append(obj.members, member{key, keyStart, keyEnd, valEnd - len(raw), valEnd})
	}
	if _, err := dec.Token(); err != nil {
		return object{}, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	obj.closeStart = at + int(dec.InputOffset()) - 1
	return obj, nil
}

// holder returns the object that holds, or would hold, the last segment of
// key. ok is false when the key's container is missing.
func holder(src []byte, root object, key Key) (obj object, ok bool, err error) {
	if len(key) == 1 {
		return root, true, nil
	}
	i := root.find(key[0])
	if i < 0 {
		return object{}, false, nil
	}
	m := root.members[i]
	if src[m.valStart] != '{' {
		return object{}, false, fmt.Errorf("%w: %w: %q", ErrRefused, errNotObject, key[0])
	}
	obj, err = parseObject(src, m.valStart)
	return obj, err == nil, err
}

// remove deletes key from src, with exactly one adjacent comma. A key that is
// already gone is not an error.
func remove(src []byte, key Key) ([]byte, error) {
	root, err := parseObject(src, 0)
	if err != nil {
		return nil, err
	}
	h, ok, err := holder(src, root, key)
	if errors.Is(err, errNotObject) {
		return src, nil // the key cannot be there
	}
	if err != nil || !ok {
		return src, err
	}
	i := h.find(key[len(key)-1])
	if i < 0 {
		return src, nil
	}
	m := h.members
	switch {
	case len(m) == 1:
		return splice(src, h.open+1, m[i].valEnd, ""), nil
	case i < len(m)-1:
		return splice(src, m[i].keyStart, m[i+1].keyStart, ""), nil
	default:
		return splice(src, m[i-1].valEnd, m[i].valEnd, ""), nil
	}
}

// add inserts the leaf unless its key is present, creating the container of a
// two-segment key first when it is missing.
func add(src []byte, l Leaf) ([]byte, bool, error) {
	root, err := parseObject(src, 0)
	if err != nil {
		return nil, false, err
	}
	// The root interior decides the layout of an empty container.
	nl := ""
	if inner := src[root.open+1 : root.closeStart]; bytes.IndexByte(inner, 0x0a) >= 0 {
		nl = "\n"
		if bytes.Contains(inner, []byte("\r\n")) {
			nl = "\r\n"
		}
	}
	h, ok, err := holder(src, root, l.Key)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		src = insert(src, root, l.Key[0], []byte("{}"), nl)
		return add(src, l)
	}
	last := l.Key[len(l.Key)-1]
	if h.find(last) >= 0 {
		return src, false, nil
	}
	return insert(src, h, last, l.Value, nl), true, nil
}

// insert writes a new member at the end of h. In an object with members it
// copies the whitespace before the last key and the spacing around its colon.
// An empty object has no sibling to copy: nl is the line ending of the root
// (empty when the root is on one line), and the member goes on its own line,
// two more spaces in than the line its brace is on, or with no whitespace at
// all when nl is empty.
func insert(src []byte, h object, key string, value []byte, nl string) []byte {
	k := quote(key)
	if n := len(h.members); n > 0 {
		last := h.members[n-1]
		sep := last.keyStart
		for sep > 0 && isSpace(src[sep-1]) {
			sep--
		}
		ins := "," + string(src[sep:last.keyStart]) + k + string(src[last.keyEnd:last.valStart]) + string(value)
		return splice(src, last.valEnd, last.valEnd, ins)
	}
	if nl == "" {
		return splice(src, h.open+1, h.closeStart, k+":"+string(value))
	}
	ls := bytes.LastIndexByte(src[:h.open], '\n') + 1
	end := ls
	for end < h.open && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	indent := string(src[ls:end])
	return splice(src, h.open+1, h.closeStart, nl+indent+"  "+k+": "+string(value)+nl+indent)
}

func splice(src []byte, start, end int, ins string) []byte {
	out := make([]byte, 0, len(src)-(end-start)+len(ins))
	out = append(out, src[:start]...)
	out = append(out, ins...)
	return append(out, src[end:]...)
}

// quote encodes s as a JSON string without escaping <, > and &.
func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	return string(bytes.TrimRight(b.Bytes(), "\n"))
}
