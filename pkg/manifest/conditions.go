package manifest

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"go.yaml.in/yaml/v4"
)

// GetConditions fetches and parses <url>catalogs/conditions.yaml, the index
// of the condition scripts the repository delivers. A repository without one
// (404) delivers none. Any other fetch or parse error is returned, so the
// caller can keep the scripts it already has.
func GetConditions(cfg config.Configuration) ([]conditions.Script, error) {
	url := cfg.URL + conditions.IndexPath
	data, err := downloadGet(url)
	var status *download.StatusError
	if errors.As(err, &status) && status.Code == http.StatusNotFound {
		slog.Debug("repository has no condition script index", "url", url)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve %s: %w", url, err)
	}
	var scripts []conditions.Script
	if err := yaml.Unmarshal(data, &scripts); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	return scripts, nil
}

// GetConditionScript fetches one script the index lists, by its path in the
// repository.
func GetConditionScript(cfg config.Configuration, repoPath string) ([]byte, error) {
	return downloadGet(cfg.URL + repoPath)
}
