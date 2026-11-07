// Package placement declares placements.yaml, the repository file that
// defines where zip items are placed (see
// site/src/content/docs/guides/zip-items.md), and decides where they may be
// written: the write roots compiled into the build, and the checks every
// placement path passes before sofcat writes under it. It is portable and
// holds no service state, so the service and a per-user helper share it.
package placement

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"go.yaml.in/yaml/v4"
)

// Roots are the directories a build may place trees under. They come from the
// build only (a brand's write-roots.yaml, or the stock defaults), never from
// runtime config or the placement table.
type Roots struct {
	// Darwin and Windows are the machine-scope roots on each OS. Windows
	// roots may use ${ProgramFiles} and ${ProgramData}.
	Darwin  []string `yaml:"darwin"`
	Windows []string `yaml:"windows"`
	// WindowsUser are the user-scope roots on Windows, relative to the user's
	// profile: either starting with ${USERPROFILE} or a relative path.
	WindowsUser []string `yaml:"windows_user"`
}

// ParseRoots decodes write-roots.yaml, rejecting unknown fields.
func ParseRoots(data []byte) (Roots, error) {
	var r Roots
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil && !errors.Is(err, io.EOF) {
		return Roots{}, fmt.Errorf("parse write-roots.yaml: %w", err)
	}
	return r, nil
}

// StockRoots are the roots of a build without write-roots.yaml: the directory
// holding its config and Data for machine scope (${ProgramData}\<Name> on
// Windows, /Library/Application Support/<Name> on macOS), and no user roots.
// Data itself and the executables' directory stay refused (Guard.Check).
func StockRoots() Roots {
	return Roots{
		Darwin:  []string{identity.SupportDir()},
		Windows: []string{`${ProgramData}\` + identity.Name},
	}
}

// Compiled returns the roots compiled into this build.
func Compiled() Roots { return compiledRoots() }

// For returns the roots for scope on goos.
func (r Roots) For(goos, scope string) []string {
	switch {
	case goos == Darwin && scope == ScopeMachine:
		return r.Darwin
	case goos == Windows && scope == ScopeMachine:
		return r.Windows
	case goos == Windows && scope == ScopeUser:
		return r.WindowsUser
	default:
		return nil
	}
}
