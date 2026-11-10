package manifest

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
)

// GetPlacements fetches and parses <url>placements.yaml, the repository's
// placement table. A repository without one (404) has an empty table: items
// that are not zip items do not need it, and zip items then fail because no
// placement is defined. Any other fetch or parse error fails like a catalog
// fetch error, and so does a files entry whose <url><template_file> cannot
// be fetched, a 404 included.
func GetPlacements(cfg config.Configuration) (placement.Table, error) {
	url := cfg.URL + "placements.yaml"
	data, err := downloadGet(url)
	var status *download.StatusError
	if errors.As(err, &status) && status.Code == http.StatusNotFound {
		slog.Debug("repository has no placements.yaml", "url", url)
		return placement.Table{}, nil
	}
	if err != nil {
		return placement.Table{}, fmt.Errorf("unable to retrieve %s: %w", url, err)
	}
	table, err := placement.Parse(data, func(rel string) ([]byte, error) {
		fileURL := cfg.URL + rel
		body, fileErr := downloadGet(fileURL)
		if fileErr != nil {
			return nil, fmt.Errorf("unable to retrieve %s: %w", fileURL, fileErr)
		}
		return body, nil
	})
	if err != nil {
		return placement.Table{}, fmt.Errorf("%s: %w", url, err)
	}
	return table, nil
}
