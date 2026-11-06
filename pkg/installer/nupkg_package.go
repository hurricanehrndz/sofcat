package installer

import (
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A kept copy is the whole extracted package sofcat keeps after a successful
// nupkg install, at <root>/<lower(id)>, as Chocolatey keeps lib\<id>. It
// supplies the uninstall script, the upgrade's before-modify and the
// version a status check reads. This file builds in every flavour, so the
// status hook can read kept copies in a zip-only build too; the engine that
// writes them is in nupkg.go.

// nuspec is the part of a package's root .nuspec sofcat reads. The tags name
// no namespace, so they match whichever one the nuspec declares.
type nuspec struct {
	ID      string `xml:"metadata>id"`
	Version string `xml:"metadata>version"`
}

// parseNuspec reads a nuspec and requires its id and version.
func parseNuspec(r io.Reader) (nuspec, error) {
	var spec nuspec
	if err := xml.NewDecoder(r).Decode(&spec); err != nil {
		return nuspec{}, fmt.Errorf("unable to read the nuspec: %w", err)
	}
	spec.ID = strings.TrimSpace(spec.ID)
	spec.Version = strings.TrimSpace(spec.Version)
	if err := checkPackageID(spec.ID); err != nil {
		return nuspec{}, err
	}
	if spec.Version == "" {
		return nuspec{}, fmt.Errorf("the nuspec has no version")
	}
	return spec, nil
}

// packageIDPattern is NuGet's id alphabet. The id names a directory under
// the kept-copy root, so nothing else may reach the path.
var packageIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func checkPackageID(id string) error {
	if !packageIDPattern.MatchString(id) {
		return fmt.Errorf("package id %q is not a valid NuGet id", id)
	}
	if err := checkSegment(id); err != nil {
		return fmt.Errorf("package id %q: %w", id, err)
	}
	return nil
}

// isRootNuspec reports whether a slash-separated package path is a .nuspec
// at the package root.
func isRootNuspec(name string) bool {
	return !strings.Contains(name, "/") && strings.HasSuffix(strings.ToLower(name), ".nuspec")
}

// keptCopyDir is the kept copy's directory for id under root.
func keptCopyDir(root, id string) string {
	return filepath.Join(root, strings.ToLower(id))
}

// readDirNuspec parses the root .nuspec of the extracted package in dir.
func readDirNuspec(dir string) (nuspec, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nuspec{}, err
	}
	for _, e := range entries {
		if e.Type().IsRegular() && isRootNuspec(e.Name()) {
			f, err := os.Open(filepath.Join(dir, e.Name()))
			if err != nil {
				return nuspec{}, err
			}
			defer func() { _ = f.Close() }()
			return parseNuspec(f)
		}
	}
	return nuspec{}, fmt.Errorf("%s has no .nuspec", dir)
}

// KeptNupkgVersion returns the nuspec version of the kept copy of package id
// under root, and whether a kept copy exists. A kept copy whose nuspec cannot
// be read exists with an empty version, so a status check treats it as
// installed at an unknown version rather than absent.
func KeptNupkgVersion(root, id string) (string, bool) {
	if root == "" || checkPackageID(id) != nil {
		return "", false
	}
	dir := keptCopyDir(root, id)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", false
	}
	spec, err := readDirNuspec(dir)
	if err != nil {
		slog.Warn("Unable to read the kept nupkg copy", "path", dir, "err", err)
		return "", true
	}
	return spec.Version, true
}
