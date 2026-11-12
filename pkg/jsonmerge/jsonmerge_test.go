package jsonmerge

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"unicode/utf8"
)

var aPlugin = Leaf{Key{"enabledPlugins", "a@m"}, json.RawMessage("true")}

func pk(name string) Key { return Key{"enabledPlugins", name} }

// duplicateIn walks every object in b with the token decoder and returns the
// first member name that appears twice in one object. json.Valid accepts
// duplicates, so this is the check that output stays unambiguous.
func duplicateIn(b []byte) (string, bool) {
	type frame struct {
		obj     bool
		wantKey bool
		seen    map[string]bool
	}
	var stack []frame
	dec := json.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		d, isDelim := tok.(json.Delim)
		if isDelim && (d == '}' || d == ']') {
			stack = stack[:len(stack)-1]
			if n := len(stack); n > 0 && stack[n-1].obj {
				stack[n-1].wantKey = true
			}
			continue
		}
		if n := len(stack); n > 0 && stack[n-1].obj && stack[n-1].wantKey {
			key := tok.(string)
			if stack[n-1].seen[key] {
				return key, true
			}
			stack[n-1].seen[key] = true
			stack[n-1].wantKey = false
			continue
		}
		if isDelim {
			stack = append(stack, frame{obj: d == '{', wantKey: d == '{', seen: map[string]bool{}})
			continue
		}
		if n := len(stack); n > 0 && stack[n-1].obj {
			stack[n-1].wantKey = true
		}
	}
}

// checkOutput asserts what must hold for any successful Apply: valid JSON, no
// duplicate members, and a second run with the same inputs changes nothing.
func checkOutput(t *testing.T, out []byte, want []Leaf, nowOwned []Key) {
	t.Helper()
	if len(out) == 0 {
		return
	}
	if !json.Valid(out) {
		t.Fatalf("output is not valid JSON:\n%s", out)
	}
	if k, dup := duplicateIn(out); dup {
		t.Fatalf("output repeats member %q:\n%s", k, out)
	}
	out2, owned2, changed, err := Apply(out, want, nowOwned)
	if err != nil || changed || !bytes.Equal(out2, out) || !slices.EqualFunc(owned2, nowOwned, slices.Equal) {
		t.Fatalf("second Apply is not a no-op: changed=%v err=%v\n%s\nowned %v -> %v", changed, err, out2, nowOwned, owned2)
	}
}

func TestApply(t *testing.T) {
	const (
		pretty  = "{\n  \"enabledPlugins\": {\n    \"x@m\": false\n  }\n}\n"
		prettyA = "{\n  \"enabledPlugins\": {\n    \"x@m\": false,\n    \"a@m\": true\n  }\n}\n"
	)
	tests := []struct {
		name        string
		doc         string
		want        []Leaf
		owned       []Key
		out         string
		nowOwned    []Key
		changed     bool
		refused     bool
		wantNilOut  bool
		wantDocSame bool
	}{
		{
			name: "absent key is added copying the sibling layout and owned",
			doc:  pretty, want: []Leaf{aPlugin},
			out: prettyA, nowOwned: []Key{pk("a@m")}, changed: true,
		},
		{
			name: "missing container is created and the root layout kept",
			doc:  "{\n  \"model\": \"opus\"\n}\n", want: []Leaf{aPlugin},
			out:      "{\n  \"model\": \"opus\",\n  \"enabledPlugins\": {\n    \"a@m\": true\n  }\n}\n",
			nowOwned: []Key{pk("a@m")}, changed: true,
		},
		{
			name: "compact document gets compact inserts",
			doc:  `{"model":"opus"}`, want: []Leaf{aPlugin},
			out:      `{"model":"opus","enabledPlugins":{"a@m":true}}`,
			nowOwned: []Key{pk("a@m")}, changed: true,
		},
		{
			name: "key the user set to false before sofcat is left alone and not owned",
			doc:  "{\n  \"enabledPlugins\": {\n    \"a@m\": false\n  }\n}\n", want: []Leaf{aPlugin},
			out:         "{\n  \"enabledPlugins\": {\n    \"a@m\": false\n  }\n}\n",
			wantDocSame: true,
		},
		{
			name: "unowned key stays when it leaves want",
			doc:  "{\"enabledPlugins\": {\"a@m\": false}}", want: nil,
			out:         "{\"enabledPlugins\": {\"a@m\": false}}",
			wantDocSame: true,
		},
		{
			name: "owned key that leaves want while true is deleted",
			doc:  `{"enabledPlugins":{"x@m":false,"a@m":true}}`, owned: []Key{pk("a@m")},
			out: `{"enabledPlugins":{"x@m":false}}`, changed: true,
		},
		{
			name: "owned key the user flipped to false is still deleted",
			doc:  `{"enabledPlugins":{"x@m":false,"a@m":false}}`, owned: []Key{pk("a@m")},
			out: `{"enabledPlugins":{"x@m":false}}`, changed: true,
		},
		{
			name: "owned key already gone is dropped from the owned set without touching the document",
			doc:  `{"enabledPlugins":{"x@m":false}}`, owned: []Key{pk("a@m")},
			out: `{"enabledPlugins":{"x@m":false}}`, wantDocSame: true,
		},
		{
			name: "removed then wanted again comes back",
			doc:  `{"enabledPlugins":{"x@m":false}}`, want: []Leaf{aPlugin},
			out:      `{"enabledPlugins":{"x@m":false,"a@m":true}}`,
			nowOwned: []Key{pk("a@m")}, changed: true,
		},
		{
			name: "owned key the user uninstalled while still wanted is re-added",
			doc:  `{"enabledPlugins":{}}`, want: []Leaf{aPlugin}, owned: []Key{pk("a@m")},
			out:      `{"enabledPlugins":{"a@m":true}}`,
			nowOwned: []Key{pk("a@m")}, changed: true,
		},
		{
			name: "owned key still wanted and flipped by the user is not rewritten",
			doc:  `{"enabledPlugins":{"a@m":false}}`, want: []Leaf{aPlugin}, owned: []Key{pk("a@m")},
			out:      `{"enabledPlugins":{"a@m":false}}`,
			nowOwned: []Key{pk("a@m")}, wantDocSame: true,
		},
		{
			name: "missing document with a want becomes a fresh formatted object",
			doc:  "", want: []Leaf{aPlugin},
			out:      "{\n  \"enabledPlugins\": {\n    \"a@m\": true\n  }\n}\n",
			nowOwned: []Key{pk("a@m")}, changed: true,
		},
		{
			name: "whitespace-only document is treated as missing",
			doc:  " \n", want: []Leaf{aPlugin},
			out:      "{\n  \"enabledPlugins\": {\n    \"a@m\": true\n  }\n}\n",
			nowOwned: []Key{pk("a@m")}, changed: true,
		},
		{
			name: "missing document with nothing wanted is not created",
			doc:  "", owned: []Key{pk("a@m")},
			wantNilOut: true, wantDocSame: true,
		},
		{
			name: "html characters in a new key stay readable",
			doc:  `{}`, want: []Leaf{{Key{"a&b<c>"}, json.RawMessage("1")}},
			out: `{"a&b<c>":1}`, nowOwned: []Key{{"a&b<c>"}}, changed: true,
		},
		{
			name: "added keys follow want order",
			doc:  `{}`, want: []Leaf{{Key{"z"}, json.RawMessage("1")}, {Key{"a"}, json.RawMessage("2")}},
			out: `{"z":1,"a":2}`, nowOwned: []Key{{"a"}, {"z"}}, changed: true,
		},
		{
			name: "owned scalar makes way for a wanted object of the same name",
			doc:  `{"foo": true}`, owned: []Key{{"foo"}},
			want: []Leaf{{Key{"foo", "bar"}, json.RawMessage("1")}},
			out:  `{"foo":{"bar":1}}`, nowOwned: []Key{{"foo", "bar"}}, changed: true,
		},
		{
			name: "a dotted segment is not a two-segment key",
			doc:  `{"a":{"b":false}}`, want: []Leaf{{Key{"a.b"}, json.RawMessage("1")}},
			out: `{"a":{"b":false},"a.b":1}`, nowOwned: []Key{{"a.b"}}, changed: true,
		},
		{
			name: "the empty string is a legal key",
			doc:  `{}`, want: []Leaf{{Key{""}, json.RawMessage("true")}},
			out: `{"":true}`, nowOwned: []Key{{""}}, changed: true,
		},
		{
			name:     "everything else survives byte for byte, escaped key included",
			doc:      "{\"z\":1,\n\t \"enabled\\u0050lugins\" :{ \"x\" :false}  ,\"\\u0061\":[1, 2 ]}\n",
			want:     []Leaf{aPlugin},
			out:      "{\"z\":1,\n\t \"enabled\\u0050lugins\" :{ \"x\" :false, \"a@m\" :true}  ,\"\\u0061\":[1, 2 ]}\n",
			nowOwned: []Key{pk("a@m")}, changed: true,
		},
		{
			name: "owned key spelled with an escape is found after unescaping",
			doc:  `{"enabledPlugins":{"a\u0040m":true,"x":1}}`, owned: []Key{pk("a@m")},
			out: `{"enabledPlugins":{"x":1}}`, changed: true,
		},
		// Deleting needs exactly one comma gone: first, middle, last and only
		// member, in pretty and compact layouts.
		{
			name: "delete first member, pretty",
			doc:  "{\n  \"p\": {\n    \"a\": 1,\n    \"b\": 2,\n    \"c\": 3\n  }\n}\n", owned: []Key{{"p", "a"}},
			out: "{\n  \"p\": {\n    \"b\": 2,\n    \"c\": 3\n  }\n}\n", changed: true,
		},
		{
			name: "delete middle member, pretty",
			doc:  "{\n  \"p\": {\n    \"a\": 1,\n    \"b\": 2,\n    \"c\": 3\n  }\n}\n", owned: []Key{{"p", "b"}},
			out: "{\n  \"p\": {\n    \"a\": 1,\n    \"c\": 3\n  }\n}\n", changed: true,
		},
		{
			name: "delete last member, pretty",
			doc:  "{\n  \"p\": {\n    \"a\": 1,\n    \"b\": 2,\n    \"c\": 3\n  }\n}\n", owned: []Key{{"p", "c"}},
			out: "{\n  \"p\": {\n    \"a\": 1,\n    \"b\": 2\n  }\n}\n", changed: true,
		},
		{
			name: "delete only member, pretty",
			doc:  "{\n  \"p\": {\n    \"a\": 1\n  }\n}\n", owned: []Key{{"p", "a"}},
			out: "{\n  \"p\": {\n  }\n}\n", changed: true,
		},
		{
			name: "delete first member, compact",
			doc:  `{"p":{"a":1,"b":2,"c":3}}`, owned: []Key{{"p", "a"}},
			out: `{"p":{"b":2,"c":3}}`, changed: true,
		},
		{
			name: "delete middle member, compact",
			doc:  `{"p":{"a":1,"b":2,"c":3}}`, owned: []Key{{"p", "b"}},
			out: `{"p":{"a":1,"c":3}}`, changed: true,
		},
		{
			name: "delete last member, compact",
			doc:  `{"p":{"a":1,"b":2,"c":3}}`, owned: []Key{{"p", "c"}},
			out: `{"p":{"a":1,"b":2}}`, changed: true,
		},
		{
			name: "delete only member, compact; the container stays",
			doc:  `{"p":{"a":1}}`, owned: []Key{{"p", "a"}},
			out: `{"p":{}}`, changed: true,
		},
		{
			name: "delete a top-level member, first and last",
			doc:  `{"a": 1, "b": 2, "c": 3}`, owned: []Key{{"a"}, {"c"}},
			out: `{"b": 2}`, changed: true,
		},
		{
			name: "compact document with a trailing newline gets a fully compact insert",
			doc:  "{\"model\":\"opus\"}\n", want: []Leaf{aPlugin},
			out:      "{\"model\":\"opus\",\"enabledPlugins\":{\"a@m\":true}}\n",
			nowOwned: []Key{pk("a@m")}, changed: true,
		},
		{
			name: "value with surrounding whitespace is compacted",
			doc:  `{}`, want: []Leaf{{Key{"a"}, json.RawMessage(" {\n \"b\": 1 }\n")}},
			out: `{"a":{"b":1}}`, nowOwned: []Key{{"a"}}, changed: true,
		},
		{
			name: "CRLF document: add into a non-empty container keeps CRLF",
			doc:  "{\r\n  \"enabledPlugins\": {\r\n    \"x@m\": false\r\n  }\r\n}\r\n", want: []Leaf{aPlugin},
			out:      "{\r\n  \"enabledPlugins\": {\r\n    \"x@m\": false,\r\n    \"a@m\": true\r\n  }\r\n}\r\n",
			nowOwned: []Key{pk("a@m")}, changed: true,
		},
		{
			name: "CRLF document: add into an empty container keeps CRLF",
			doc:  "{\r\n  \"enabledPlugins\": {}\r\n}\r\n", want: []Leaf{aPlugin},
			out:      "{\r\n  \"enabledPlugins\": {\r\n    \"a@m\": true\r\n  }\r\n}\r\n",
			nowOwned: []Key{pk("a@m")}, changed: true,
		},
		{
			name: "one run removes from one container and adds to another",
			doc:  `{"p":{"a":1,"keep":2},"q":{"z":3}}`, owned: []Key{{"p", "a"}},
			want: []Leaf{{Key{"q", "n"}, json.RawMessage("4")}},
			out:  `{"p":{"keep":2},"q":{"z":3,"n":4}}`, nowOwned: []Key{{"q", "n"}}, changed: true,
		},
		{
			name: "owned key under a null container is gone: dropped, document untouched",
			doc:  `{"enabledPlugins":null}`, owned: []Key{pk("a@m")},
			out: `{"enabledPlugins":null}`, wantDocSame: true,
		},
		{name: "add into a null container is refused", doc: `{"enabledPlugins":null}`, want: []Leaf{aPlugin}, refused: true},
		{name: "invalid UTF-8 in a wanted key is refused", doc: `{}`, want: []Leaf{{Key{"a\xff"}, json.RawMessage("1")}}, refused: true},
		{name: "invalid UTF-8 in an owned key is refused", doc: `{}`, owned: []Key{{"p", "\xff"}}, refused: true},
		// Refusals: nothing partial.
		{name: "invalid JSON is refused", doc: `{"a":`, want: []Leaf{aPlugin}, refused: true},
		{name: "trailing content is refused", doc: `{} {}`, want: []Leaf{aPlugin}, refused: true},
		{name: "root array is refused", doc: `[]`, want: []Leaf{aPlugin}, refused: true},
		{name: "root array is refused even with nothing to do", doc: `[]`, refused: true},
		{name: "container holding a string is refused", doc: `{"enabledPlugins":"x"}`, want: []Leaf{aPlugin}, refused: true},
		{name: "container holding an array is refused", doc: `{"enabledPlugins":[]}`, want: []Leaf{aPlugin}, refused: true},
		{name: "duplicate member in the root is refused", doc: `{"a":1,"a":2}`, want: []Leaf{aPlugin}, refused: true},
		{name: "duplicate member in a touched container is refused", doc: `{"enabledPlugins":{"x":1,"x":2}}`, want: []Leaf{aPlugin}, refused: true},
		{name: "duplicate wanted key is refused", doc: `{}`, want: []Leaf{aPlugin, aPlugin}, refused: true},
		{name: "wanted key and its parent together are refused", doc: `{}`, want: []Leaf{{Key{"a"}, json.RawMessage("1")}, {Key{"a", "b"}, json.RawMessage("1")}}, refused: true},
		{name: "key with three segments is refused", doc: `{}`, want: []Leaf{{Key{"a", "b", "c"}, json.RawMessage("1")}}, refused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, nowOwned, changed, err := Apply([]byte(tt.doc), tt.want, tt.owned)
			if tt.refused {
				if !errors.Is(err, ErrRefused) || out != nil {
					t.Fatalf("got out=%q err=%v, want nil out and ErrRefused", out, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantNilOut {
				if len(out) != 0 {
					t.Fatalf("out = %q, want empty", out)
				}
			} else if string(out) != tt.out {
				t.Fatalf("out:\n%s\nwant:\n%s", out, tt.out)
			}
			if changed != tt.changed {
				t.Errorf("changed = %v, want %v", changed, tt.changed)
			}
			if tt.wantDocSame && changed {
				t.Errorf("document changed")
			}
			if !slices.EqualFunc(nowOwned, tt.nowOwned, slices.Equal) {
				t.Errorf("nowOwned = %v, want %v", nowOwned, tt.nowOwned)
			}
			checkOutput(t, out, tt.want, nowOwned)
		})
	}
}

func TestLeaves(t *testing.T) {
	t.Run("members of a top-level object are leaves and deeper values stay opaque", func(t *testing.T) {
		got, err := Leaves([]byte(`{"enabledPlugins": {"a@m": true, "b@m": {"x": [1, 2]}}, "model": "opus", "env": {}}`))
		if err != nil {
			t.Fatal(err)
		}
		want := []Leaf{
			{Key{"enabledPlugins", "a@m"}, json.RawMessage("true")},
			{Key{"enabledPlugins", "b@m"}, json.RawMessage(`{"x": [1, 2]}`)},
			{Key{"model"}, json.RawMessage(`"opus"`)},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})
	t.Run("empty render has no leaves", func(t *testing.T) {
		for _, in := range []string{"", " \n\t", "{}"} {
			got, err := Leaves([]byte(in))
			if err != nil || len(got) != 0 {
				t.Errorf("Leaves(%q) = %v, %v", in, got, err)
			}
		}
	})
	t.Run("refused renders", func(t *testing.T) {
		for _, in := range []string{`[1]`, `"x"`, `{"a":`, `{"a":1,"a":2}`, `{"p":{"b":1,"b":2}}`, `{} {}`} {
			if _, err := Leaves([]byte(in)); !errors.Is(err, ErrRefused) {
				t.Errorf("Leaves(%q) err = %v, want ErrRefused", in, err)
			}
		}
	})
	t.Run("leaves feed Apply", func(t *testing.T) {
		leaves, err := Leaves([]byte(`{"enabledPlugins": {"a@m": true}}`))
		if err != nil {
			t.Fatal(err)
		}
		out, _, _, err := Apply(nil, leaves, nil)
		if err != nil || string(out) != "{\n  \"enabledPlugins\": {\n    \"a@m\": true\n  }\n}\n" {
			t.Fatalf("out=%q err=%v", out, err)
		}
	})
}

// FuzzApply builds its documents with json.Marshal and json.MarshalIndent only;
// the Apply table covers irregular spacing, escapes and line endings.
func FuzzApply(f *testing.F) {
	f.Add("enabledPlugins", "a@m", true, uint8(0b00100))
	f.Add("enabledPlugins", "a@m", false, uint8(0b01110))
	f.Add("model", "", true, uint8(0b00101))
	f.Add("", "", false, uint8(0b11111))
	f.Add("sib", "other", true, uint8(0b11110))
	f.Fuzz(func(t *testing.T, k1, k2 string, prettyDoc bool, flags uint8) {
		if !utf8.ValidString(k1) || flags&8 != 0 && !utf8.ValidString(k2) {
			// The document would store U+FFFD and never find the key again.
			key := Key{k1, k2}
			if flags&8 == 0 {
				key = Key{k1}
			}
			if _, _, _, err := Apply([]byte("{}"), []Leaf{{key, json.RawMessage("true")}}, nil); !errors.Is(err, ErrRefused) {
				t.Fatalf("invalid UTF-8 key %q: err = %v, want ErrRefused", []string(key), err)
			}
			if _, _, _, err := Apply([]byte("{}"), nil, []Key{key}); !errors.Is(err, ErrRefused) {
				t.Fatalf("invalid UTF-8 owned key %q: err = %v, want ErrRefused", []string(key), err)
			}
			return
		}
		present, isOwned, isWanted := flags&1 != 0, flags&2 != 0, flags&4 != 0
		twoSeg, containerPresent := flags&8 != 0, flags&16 != 0
		if twoSeg && k1 == "other" {
			k1 = "other2"
		}

		key := Key{k1}
		root := map[string]any{"other": 1}
		if twoSeg {
			key = Key{k1, k2}
			if containerPresent || present {
				inner := map[string]any{"sib": 0}
				if present {
					inner[k2] = false
				}
				root[k1] = inner
			}
		} else if present {
			root[k1] = false
		}
		var doc []byte
		if prettyDoc {
			doc, _ = json.MarshalIndent(root, "", "  ")
			doc = append(doc, '\n')
		} else {
			doc, _ = json.Marshal(root)
		}
		var want []Leaf
		if isWanted {
			want = []Leaf{{key, json.RawMessage("true")}}
		}
		var owned []Key
		if isOwned {
			owned = []Key{key}
		}

		out, nowOwned, _, err := Apply(doc, want, owned)
		if err != nil {
			t.Fatalf("Apply(%s): %v", doc, err)
		}
		checkOutput(t, out, want, nowOwned)

		// Adding a key and then dropping it from want puts the document back,
		// unless a container sofcat had to create stays behind as {}.
		if !present && isWanted && !isOwned && (!twoSeg || containerPresent) {
			back, _, _, err := Apply(out, nil, nowOwned)
			if err != nil {
				t.Fatal(err)
			}
			var a, b any
			if json.Unmarshal(doc, &a) != nil || json.Unmarshal(back, &b) != nil || !reflect.DeepEqual(a, b) {
				t.Fatalf("add then remove changed the document:\n%s\n%s", doc, back)
			}
		}
	})
}
