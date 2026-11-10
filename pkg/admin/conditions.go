package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"go.yaml.in/yaml/v4"
)

// conditionsIndexName is the catalog name the index's file would have. A
// catalog must not take it.
var conditionsIndexName = strings.TrimSuffix(path.Base(conditions.IndexPath), ".yaml")

// collectConditionScripts lists the condition scripts under
// <repo>/conditions/darwin and <repo>/conditions/windows, in path order, with
// what makes a file there unusable: anything outside those two directories, a
// subdirectory or other non-regular file, a Windows script that is not .ps1
// or .exe, and two names in one directory that differ only by case. Unusable
// files are left out of the list. Dotfiles are ignored. A repo without
// conditions/ has no scripts.
func collectConditionScripts(repoPath string) ([]conditions.Script, []string, error) {
	root := filepath.Join(repoPath, "conditions")
	top, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var scripts []conditions.Script
	var problems []string
	for _, entry := range top {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !entry.IsDir() || (name != placement.Darwin && name != placement.Windows) {
			problems = append(problems, fmt.Sprintf("%s: condition scripts belong in conditions/%s or conditions/%s; ignored", filepath.Join(root, name), placement.Darwin, placement.Windows))
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, name))
		if err != nil {
			return nil, nil, err
		}
		// folded maps a lowercased script name to the first spelling seen:
		// macOS and Windows would store both as one file.
		folded := make(map[string]string)
		for _, file := range entries {
			fileName := file.Name()
			if strings.HasPrefix(fileName, ".") {
				continue
			}
			filePath := filepath.Join(root, name, fileName)
			ext := strings.ToLower(filepath.Ext(fileName))
			first, collides := folded[strings.ToLower(fileName)]
			switch {
			case !file.Type().IsRegular():
				problems = append(problems, fmt.Sprintf("%s: not a regular file; ignored", filePath))
				continue
			case name == placement.Windows && ext != ".ps1" && ext != ".exe":
				problems = append(problems, fmt.Sprintf("%s: Windows runs only .ps1 and .exe condition scripts; ignored", filePath))
				continue
			case collides:
				problems = append(problems, fmt.Sprintf("%s: name differs from %q only by case; ignored", filePath, first))
				continue
			}
			folded[strings.ToLower(fileName)] = fileName
			body, err := os.ReadFile(filePath)
			if err != nil {
				return nil, nil, fmt.Errorf("read condition script %s: %w", filePath, err)
			}
			sum := sha256.Sum256(body)
			scripts = append(scripts, conditions.Script{
				Path:   path.Join("conditions", name, fileName),
				OS:     name,
				SHA256: hex.EncodeToString(sum[:]),
			})
		}
	}
	return scripts, problems, nil
}

// conditionsIndex renders the index file for scripts, or nil when there are
// none: a repo without condition scripts has no index.
func conditionsIndex(scripts []conditions.Script) ([]byte, error) {
	if len(scripts) == 0 {
		return nil, nil
	}
	return yaml.Marshal(scripts)
}

// CheckConditionsIndex compares the index already in <repo>/catalogs with
// the one set would write, for makecatalogs --check. It reports each listed
// script that is gone and an index that differs in any other way. A repo
// whose index was never written is not stale, as one whose catalogs were
// never written is not.
func CheckConditionsIndex(repoPath string, set CatalogSet) []string {
	indexPath := filepath.Join(repoPath, filepath.FromSlash(conditions.IndexPath))
	have, err := os.ReadFile(indexPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", indexPath, err)}
	}
	want, err := conditionsIndex(set.Conditions)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", indexPath, err)}
	}
	if bytes.Equal(have, want) {
		return nil
	}

	var problems []string
	var listed []conditions.Script
	// An index that does not parse is simply stale.
	_ = yaml.Unmarshal(have, &listed)
	current := make(map[string]bool, len(set.Conditions))
	for _, script := range set.Conditions {
		current[script.Path] = true
	}
	for _, script := range listed {
		if !current[script.Path] {
			problems = append(problems, fmt.Sprintf("%s: lists %s, which is missing", indexPath, script.Path))
		}
	}
	return append(problems, fmt.Sprintf("%s: stale; run makecatalogs to regenerate it", indexPath))
}
