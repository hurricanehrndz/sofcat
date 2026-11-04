package installer

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// The strip's bar follows an item's percent, so a zip item's downloads must
// climb across all its archives without falling back at the second one, and
// the install that follows must not claim a percentage nobody measured.
func TestZipDownloadProgressSpansArchives(t *testing.T) {
	item, archive := packVersion(t, "1.0.0", map[string]string{"SKILL.md": "hello"})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	// One archive per placement, so both are downloaded.
	exclusive := item.Installer.Variants["exclusive"][testOS()]
	exclusive.Location = "hello-exclusive.zip"
	item.Installer.Variants["exclusive"][testOS()] = exclusive
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		for i := 0; i < len(data); i += 64 {
			_, _ = w.Write(data[i:min(i+64, len(data))])
			w.(http.Flusher).Flush()
		}
	}))
	defer ts.Close()

	z, _ := newTestZip(t)
	z.CachePath = filepath.Join(t.TempDir(), "cache")
	z.URLPackages = ts.URL + "/"
	r := newTestRunner()
	r.Zip = z
	var events []string
	var downloads []int
	r.Emit = func(_ catalog.Item, _ []string, state string, percent int, _ string) {
		events = append(events, fmt.Sprintf("%s:%d", state, percent))
		if state == "downloading" && percent != NoPercent {
			downloads = append(downloads, percent)
		}
	}

	if _, err := r.Install(item, "install"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(downloads) < 2 || downloads[len(downloads)-1] != 100 {
		t.Fatalf("download percents %v, want several ending at 100 (events %v)", downloads, events)
	}
	halfway := false
	for i, p := range downloads {
		if i > 0 && p < downloads[i-1] {
			t.Fatalf("download percent fell %d -> %d: %v", downloads[i-1], p, downloads)
		}
		halfway = halfway || p == 50
	}
	if !halfway {
		t.Fatalf("download percents %v: the first of two archives should end at 50", downloads)
	}
	if got := events[len(events)-2:]; got[0] != "installing:-1" || got[1] != "done:100" {
		t.Fatalf("events end %v, want an unmeasured install then done:100", got)
	}
}
