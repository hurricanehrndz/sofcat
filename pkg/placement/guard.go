package placement

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// ErrRefused wraps every refusal of a placement path. The item then fails
// with report.CodePlacementRefused.
var ErrRefused = errors.New("placement refused")

// Guard checks placement paths against the write roots and the directories
// sofcat must never write into.
type Guard struct {
	// GOOS picks the placement paths and roots: Darwin or
	// Windows. Paths compare case-insensitively for both.
	GOOS  string
	Roots Roots
	// DataDir is sofcat's data directory; nothing is placed inside it.
	DataDir string
	// BinaryDirs hold sofcat's executables; no placement may be inside one,
	// equal one, or contain one.
	BinaryDirs []string
	// Vars holds values only the caller knows, such as the user's home
	// (ProfileVar) for a user-scope placement. The service has no user and
	// leaves it empty.
	Vars map[string]string
}

// ProfileVar is the variable a user-scope path on goos names the user's home
// with: ${USERPROFILE} on Windows, ${HOME} on macOS.
func ProfileVar(goos string) string {
	if goos == Darwin {
		return "HOME"
	}
	return "USERPROFILE"
}

// HostOS is the placement OS key of the running system, or runtime.GOOS when
// placements.yaml has no key for it (no variant then matches).
func HostOS() string { return runtime.GOOS }

// MachineGuard is the service's guard: this OS, roots (the build's plus
// managed policy's, policy.EffectiveRoots), the data directory, and the
// directories holding this build's binaries.
func MachineGuard(dataDir string, roots Roots) Guard {
	return Guard{GOOS: HostOS(), Roots: roots, DataDir: dataDir, BinaryDirs: BinaryDirs(HostOS())}
}

// BinaryDirs returns the directories holding sofcat's binaries: the running
// executable's, and on macOS /Library/Application Support/<Name>/bin.
func BinaryDirs(goos string) []string {
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if goos == Darwin {
		dirs = append(dirs, identity.BinDir())
	}
	return dirs
}

// Resolve expands a placement path for scope, resolves its symlinks and checks
// it (see Check). It returns the resolved path.
func (g Guard) Resolve(raw, scope string) (string, error) {
	path, err := g.expand(raw, scope)
	if err != nil {
		return "", err
	}
	return g.Check(path, scope)
}

// Check resolves the symlinks of an absolute path and refuses it when the
// result is outside every write root for scope on g.GOOS, inside the data
// directory or the user-artifacts staging directory, or inside, equal to or
// containing a binary directory. It returns
// the resolved path.
func (g Guard) Check(path, scope string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %s is not an absolute path", ErrRefused, path)
	}
	resolved, err := resolve(path)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrRefused, path, err)
	}
	if err := g.checkResolved(resolved, scope); err != nil {
		return "", err
	}
	return resolved, nil
}

// ResolveLeaf is Resolve without following the final element: see
// CheckLeaf.
func (g Guard) ResolveLeaf(raw, scope string) (string, error) {
	path, err := g.expand(raw, scope)
	if err != nil {
		return "", err
	}
	return g.CheckLeaf(path, scope)
}

// CheckLeaf is Check for a path that is to be removed: it resolves the
// symlinks of the parent directory only, so a final symlink is checked and
// returned as itself, never as its target.
func (g Guard) CheckLeaf(path, scope string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %s is not an absolute path", ErrRefused, path)
	}
	path = filepath.Clean(path)
	parent, err := resolve(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrRefused, path, err)
	}
	leaf := filepath.Join(parent, filepath.Base(path))
	if err := g.checkResolved(leaf, scope); err != nil {
		return "", err
	}
	return leaf, nil
}

// checkResolved applies Check's refusals to resolved, a path whose symlinks
// are already resolved.
func (g Guard) checkResolved(resolved, scope string) error {
	inRoot := false
	for _, raw := range g.Roots.For(g.GOOS, scope) {
		root, err := g.root(raw, scope)
		if err != nil {
			continue
		}
		if g.within(resolved, root) {
			inRoot = true
			break
		}
	}
	if !inRoot {
		return fmt.Errorf("%w: %s is outside the write roots for %s scope", ErrRefused, resolved, scope)
	}
	if g.DataDir != "" {
		if data, err := resolve(g.DataDir); err == nil && g.within(resolved, data) {
			return fmt.Errorf("%w: %s is inside the data directory", ErrRefused, resolved)
		}
		// On macOS the staged archives sit beside Data, under the stock root.
		if staged, err := resolve(identity.UserArtifactsDirFor(g.GOOS, g.DataDir)); err == nil && g.within(resolved, staged) {
			return fmt.Errorf("%w: %s is inside the user-artifacts directory", ErrRefused, resolved)
		}
	}
	for _, dir := range g.BinaryDirs {
		bin, err := resolve(dir)
		if err != nil {
			continue
		}
		if g.within(resolved, bin) || g.within(bin, resolved) {
			return fmt.Errorf("%w: %s overlaps the binary directory %s", ErrRefused, resolved, bin)
		}
	}
	return nil
}

// root expands and resolves one write root.
func (g Guard) root(raw, scope string) (string, error) {
	path, err := g.expand(raw, scope)
	if err != nil {
		return "", err
	}
	return resolve(path)
}

// Expand returns raw as Resolve sees it before resolving any symlink: its
// variables expanded for scope, absolute and clean.
func (g Guard) Expand(raw, scope string) (string, error) {
	return g.expand(raw, scope)
}

// expand replaces the variables a path may use: ${ProgramFiles} and
// ${ProgramData} on Windows, and the user's home (ProfileVar) in user scope
// when the caller supplied it. A user-scope path that is still relative is
// taken relative to the home. Anything left unexpanded is refused.
func (g Guard) expand(raw, scope string) (string, error) {
	vars := map[string]string{}
	if g.GOOS == Windows {
		vars["ProgramFiles"] = os.Getenv("ProgramFiles")
		vars["ProgramData"] = os.Getenv("ProgramData")
	}
	profile := ""
	if scope == ScopeUser {
		name := ProfileVar(g.GOOS)
		profile = g.Vars[name]
		vars[name] = profile
	}
	path := raw
	for name, value := range vars {
		if value != "" {
			path = strings.ReplaceAll(path, "${"+name+"}", value)
		}
	}
	if strings.Contains(path, "${") {
		return "", fmt.Errorf("%w: %s: unexpanded variable", ErrRefused, raw)
	}
	if scope == ScopeUser && profile != "" && !filepath.IsAbs(path) {
		path = filepath.Join(profile, path)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %s is not an absolute path", ErrRefused, raw)
	}
	return filepath.Clean(path), nil
}

// resolve evaluates the symlinks of the longest existing prefix of path and
// appends the rest, since a placement root may not exist yet. A dangling
// symlink on the way is an error: whatever it points to would be written.
func resolve(path string) (string, error) {
	path = filepath.Clean(path)
	var rest []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			return filepath.Join(append([]string{resolved}, rest...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if _, lerr := os.Lstat(path); lerr == nil {
			return "", fmt.Errorf("%s: dangling symlink", path)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		rest = append([]string{filepath.Base(path)}, rest...)
		path = parent
	}
}

// within reports whether path is dir or inside it, comparing
// case-insensitively for Windows and macOS, whose volumes ignore case by
// default. On a case-sensitive APFS volume this only refuses more.
func (g Guard) within(path, dir string) bool {
	if g.GOOS == Windows || g.GOOS == Darwin {
		path, dir = strings.ToLower(path), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// RelativeTo returns path relative to dir, slash-separated, and whether path
// is within dir, comparing as the guard does: case-insensitively on Windows
// and macOS, where the relative path comes back lower-cased.
func (g Guard) RelativeTo(path, dir string) (string, bool) {
	if !g.within(path, dir) {
		return "", false
	}
	if g.GOOS == Windows || g.GOOS == Darwin {
		path, dir = strings.ToLower(path), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, path)
	return filepath.ToSlash(rel), err == nil
}
