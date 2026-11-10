package manifest

import (
	"errors"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/download"
)

// TestGetConditions: a repository without an index delivers no scripts, while
// any other failure is an error, which the run answers by keeping the scripts
// it synced before.
func TestGetConditions(t *testing.T) {
	const url = "https://repo.example/catalogs/conditions.yaml"
	cases := []struct {
		name    string
		body    []byte
		err     error
		wantErr bool
		scripts int
	}{
		{name: "parsed", body: []byte("- path: conditions/darwin/10-user\n  os: darwin\n  sha256: abc\n"), scripts: 1},
		{name: "404 is no scripts", err: &download.StatusError{URL: url, Code: 404}},
		{name: "500 fails", err: &download.StatusError{URL: url, Code: 500}, wantErr: true},
		{name: "network error fails", err: errors.New("connection refused"), wantErr: true},
		{name: "not a list fails", body: []byte("Chrome:\n  version: 1\n"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := downloadGet
			t.Cleanup(func() { downloadGet = orig })
			downloadGet = fakeDownloadByURL(map[string][]byte{url: tc.body}, nil)
			if tc.err != nil {
				downloadGet = fakeDownloadByURL(nil, map[string]error{url: tc.err})
			}
			scripts, err := GetConditions(config.Configuration{URL: "https://repo.example/"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if len(scripts) != tc.scripts {
				t.Fatalf("scripts = %d, want %d", len(scripts), tc.scripts)
			}
			if tc.scripts == 1 && (scripts[0].Path != "conditions/darwin/10-user" || scripts[0].OS != "darwin" || scripts[0].SHA256 != "abc") {
				t.Fatalf("script = %+v", scripts[0])
			}
		})
	}
}
