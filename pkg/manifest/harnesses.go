package manifest

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/harness"
)

// GetHarnesses fetches and parses <url>harnesses.yaml, the repository's
// placement table. A repository without one (404) has an empty table: items
// that are not zip items do not need it, and zip items then fail because no
// placement is defined. Any other fetch or parse error fails like a catalog
// fetch error.
func GetHarnesses(cfg config.Configuration) (harness.Table, error) {
	url := cfg.URL + "harnesses.yaml"
	data, err := downloadGet(url)
	var status *download.StatusError
	if errors.As(err, &status) && status.Code == http.StatusNotFound {
		slog.Debug("repository has no harnesses.yaml", "url", url)
		return harness.Table{}, nil
	}
	if err != nil {
		return harness.Table{}, fmt.Errorf("unable to retrieve %s: %w", url, err)
	}
	table, err := harness.Parse(data)
	if err != nil {
		return harness.Table{}, fmt.Errorf("%s: %w", url, err)
	}
	return table, nil
}
