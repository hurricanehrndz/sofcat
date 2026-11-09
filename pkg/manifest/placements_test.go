package manifest

import (
	"errors"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/download"
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
