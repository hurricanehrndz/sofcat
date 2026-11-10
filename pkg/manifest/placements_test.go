package manifest

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// TestGetPlacements: a missing placements.yaml is an empty table so a
// repository without zip items keeps working, while any other failure stops
// the run like a catalog fetch error would.
func TestGetPlacements(t *testing.T) {
	const url = "https://repo.example/placements.yaml"
	cases := []struct {
		name       string
		body       []byte
		err        error
		wantErr    bool
		placements int
	}{
		{name: "parsed", body: []byte("placements:\n  skills:\n    paths: {darwin: /x}\n"), placements: 1},
		{name: "404 is an empty table", err: &download.StatusError{URL: url, Code: 404}},
		{name: "500 fails", err: &download.StatusError{URL: url, Code: 500}, wantErr: true},
		{name: "network error fails", err: errors.New("connection refused"), wantErr: true},
		{name: "invalid table fails", body: []byte("placements:\n  skills:\n    kind: bogus\n"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := downloadGet
			t.Cleanup(func() { downloadGet = orig })
			downloadGet = fakeDownloadByURL(map[string][]byte{url: tc.body}, map[string]error{url: tc.err})
			if tc.err == nil {
				downloadGet = fakeDownloadByURL(map[string][]byte{url: tc.body}, nil)
			}
			table, err := GetPlacements(config.Configuration{URL: "https://repo.example/"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if len(table.Placements) != tc.placements {
				t.Fatalf("placements = %d, want %d", len(table.Placements), tc.placements)
			}
		})
	}
}

// TestGetPlacementsTemplateFile: a template_file is fetched from the
// repository once per read and becomes the entry's template. One that cannot
// be fetched, a 404 included, or is over the template limit fails the whole
// table like an invalid placements.yaml, naming the placement, the entry and
// the URL.
func TestGetPlacementsTemplateFile(t *testing.T) {
	const (
		tableURL = "https://repo.example/placements.yaml"
		fileURL  = "https://repo.example/templates/index.json.tmpl"
	)
	table := []byte(`placements:
  skills:
    paths: {darwin: /x}
    files:
      - {path: .one.json, template_file: templates/index.json.tmpl}
      - {path: .two.json, template_file: templates/index.json.tmpl}
`)
	cases := []struct {
		name string
		body []byte
		err  error
		want string // in the error; empty for success
		url  bool   // the error names the file URL: every fetch failure does
	}{
		{name: "fetched", body: []byte(`{"n": {{ len .Items }}}`)},
		{name: "404 fails", err: &download.StatusError{URL: fileURL, Code: 404}, want: "404", url: true},
		{name: "500 fails", err: &download.StatusError{URL: fileURL, Code: 500}, want: "500", url: true},
		{name: "network error fails", err: errors.New("connection refused"), want: "connection refused", url: true},
		{name: "oversize fails", body: bytes.Repeat([]byte("x"), render.MaxTemplate+1), want: "over the 262144-byte limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := downloadGet
			t.Cleanup(func() { downloadGet = orig })
			fetches := map[string]int{}
			downloadGet = func(url string) ([]byte, error) {
				fetches[url]++
				if url == tableURL {
					return table, nil
				}
				return tc.body, tc.err
			}
			got, err := GetPlacements(config.Configuration{URL: "https://repo.example/"})
			if fetches[tableURL] != 1 || fetches[fileURL] != 1 || len(fetches) != 2 {
				t.Errorf("fetches = %v, want the table and the template file once each", fetches)
			}
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				for i, f := range got.Placements["skills"].Files {
					if f.Template != string(tc.body) || f.TemplateFile != "" {
						t.Errorf("files[%d] = %+v, want the fetched text as its template", i, f)
					}
				}
				return
			}
			if err == nil {
				t.Fatal("no error")
			}
			if len(got.Placements) != 0 {
				t.Errorf("placements = %v, want none", got.Placements)
			}
			for _, w := range []string{`placement "skills"`, "files[0]", "files[1]", "templates/index.json.tmpl", tc.want} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error lacks %q: %v", w, err)
				}
			}
			if tc.url && !strings.Contains(err.Error(), fileURL) {
				t.Errorf("error lacks the URL %s: %v", fileURL, err)
			}
		})
	}
}
