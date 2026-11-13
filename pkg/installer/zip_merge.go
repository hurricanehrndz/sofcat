package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/jsonmerge"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// A merge files entry (placement.MergeAddIfAbsent) adds the keys its template
// renders to a JSON file the user also edits, and takes back only those keys
// (pkg/jsonmerge). The keys sofcat added are recorded per user in
// ZipInstaller.MergeRecord, since the file is the user's and a key the user
// already had must never be removed.

// mergeRecord is the file at ZipInstaller.MergeRecord: the keys sofcat added,
// for each placement and JSON file.
type mergeRecord struct {
	Entries []mergeEntry `json:"entries"`
}

// mergeEntry is the keys placement added to the JSON file at Path. Path is
// compared without regard to case, as claim does: Windows and macOS do.
type mergeEntry struct {
	Path      string          `json:"path"`
	Placement string          `json:"placement"`
	Keys      []jsonmerge.Key `json:"keys"`
}

func (e mergeEntry) is(name, path string) bool {
	return e.Placement == name && strings.EqualFold(e.Path, path)
}

// owned returns the keys placement name added to path.
func (r mergeRecord) owned(name, path string) []jsonmerge.Key {
	for _, e := range r.Entries {
		if e.is(name, path) {
			return e.Keys
		}
	}
	return nil
}

// set records keys as all that placement name added to path; none removes the
// entry.
func (r *mergeRecord) set(name, path string, keys []jsonmerge.Key) {
	r.Entries = slices.DeleteFunc(r.Entries, func(e mergeEntry) bool { return e.is(name, path) })
	if len(keys) > 0 {
		r.Entries = append(r.Entries, mergeEntry{Path: path, Placement: name, Keys: keys})
	}
	slices.SortFunc(r.Entries, func(a, b mergeEntry) int {
		return strings.Compare(a.Placement+"\x00"+strings.ToLower(a.Path), b.Placement+"\x00"+strings.ToLower(b.Path))
	})
}

// loadMerges reads the record; a missing file is an empty record. One that
// cannot be read or decoded is refused, not taken for empty: every key would
// look like the user's and sofcat would lose track of what it added.
func (z *ZipInstaller) loadMerges() (mergeRecord, error) {
	var rec mergeRecord
	if z.MergeRecord == "" {
		return rec, fmt.Errorf("%w: no merge record to keep the keys sofcat adds", placement.ErrRefused)
	}
	data, err := os.ReadFile(z.MergeRecord)
	if errors.Is(err, fs.ErrNotExist) {
		return rec, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &rec)
	}
	if err != nil {
		return mergeRecord{}, fmt.Errorf("%w: merge record %s is unreadable, leaving merged files alone: %w", placement.ErrRefused, z.MergeRecord, err)
	}
	return rec, nil
}

func (z *ZipInstaller) saveMerges(rec mergeRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return writeFileAtomic(z.MergeRecord, data, 0o600)
}

// applyMerge merges o's keys into its JSON file for placement name.
func (z *ZipInstaller) applyMerge(o fileOutput, name string) error {
	if z.merged == nil {
		z.merged = map[mergeKey]bool{}
	}
	z.merged[mergeKey{name, strings.ToLower(o.leaf)}] = true
	return z.mergeInto(name, o.named, o.leaf, o.path, o.want)
}

// mergeInto makes the JSON file at path hold want for placement name and none
// of the other keys the record says it added. named is the file's path before
// any symlink was resolved: a symlinked directory on it below the user's home
// is refused (realDirs). leaf is path with only its parent's symlinks
// resolved: a file that is itself a symlink or anything but a regular file is
// refused, and a missing one is an empty document. A write keeps the file's
// permission bits; a refusal leaves the file and the record as they were.
func (z *ZipInstaller) mergeInto(name, named, leaf, path string, want []jsonmerge.Leaf) error {
	if err := z.realDirs(named); err != nil {
		return err
	}
	var doc []byte
	mode := fs.FileMode(0o644)
	switch info, err := os.Lstat(leaf); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case !info.Mode().IsRegular():
		return fmt.Errorf("%w: %s is not a regular file; leaving it alone", placement.ErrRefused, leaf)
	default:
		mode = info.Mode().Perm()
		if doc, err = os.ReadFile(path); err != nil {
			return err
		}
	}
	rec, err := z.loadMerges()
	if err != nil {
		return err
	}
	owned := rec.owned(name, leaf)
	out, nowOwned, changed, err := jsonmerge.Apply(doc, want, owned)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", placement.ErrRefused, leaf, err)
	}
	// The record goes first: one claiming a key whose write then failed is
	// harmless, since Apply drops or re-adds it, but a key written with no
	// record would never be taken back.
	if !slices.EqualFunc(owned, nowOwned, slices.Equal) {
		rec.set(name, leaf, nowOwned)
		if err := z.saveMerges(rec); err != nil {
			return err
		}
	}
	if !changed {
		return nil
	}
	if err := writeFileAtomic(path, out, mode); err != nil {
		return err
	}
	slog.Info("Merged placement file", "placement", name, "path", path, "owned", len(nowOwned))
	return nil
}

// realDirs refuses path, a merge target as named before any symlink is
// resolved, when a directory on the way to it below the user's home is a
// symlink, a junction or anything else but a plain directory. The record of
// added keys is this machine's, but such a link usually leads somewhere
// shared or versioned, such as a dotfiles repository or a synced folder: a
// key added through it would appear on another machine with no record of it
// and never be removed there. The home itself, and anything above it, may be
// a symlink. A missing directory is fine; the write creates it. A path
// outside the home is left to the guard. The check runs just before the
// merge reads and writes, so a link made in between still wins that race;
// making one takes no more than the rights the user has over the file.
func (z *ZipInstaller) realDirs(path string) error {
	home := z.Guard.Vars[placement.ProfileVar(z.Guard.GOOS)]
	if home == "" {
		return nil
	}
	// The record holds paths with the home resolved.
	homes := []string{home}
	if resolved, err := filepath.EvalSymlinks(home); err == nil && resolved != home {
		homes = append(homes, resolved)
	}
	for _, h := range homes {
		rel, in := z.Guard.RelativeTo(path, h)
		if !in {
			continue
		}
		dir := path
		for range strings.Count(rel, "/") {
			dir = filepath.Dir(dir)
			switch info, err := os.Lstat(dir); {
			case errors.Is(err, fs.ErrNotExist):
			case err != nil:
				return err
			case !info.IsDir():
				return fmt.Errorf("%w: %s is a symlink or not a plain directory; leaving %s alone", placement.ErrRefused, dir, path)
			}
		}
		return nil
	}
	return nil
}

// mergeKey names a merged file of a placement, its path in lower case.
type mergeKey struct{ placement, path string }

// SweepMerges takes back the keys of every record entry this installer did
// not merge into, whose placement skip does not name: the placement left the
// plan, its merge entry was removed or its path changed. A placement whose
// files failed to render this run, or whose items are unknown, is for skip to
// name, since an empty set of items would remove the keys of items still
// placed. An entry is dropped or shrunk only by a successful merge; a refusal
// keeps it, so ownership is never lost. It returns a failed Result per entry
// that could not be swept. An entry whose path the guard now refuses, as when
// policy narrowed the write roots, is kept too: such keys stay until the root
// is allowed again.
func (z *ZipInstaller) SweepMerges(skip func(placement string) bool) []Result {
	if z.MergeRecord == "" {
		return nil
	}
	rec, err := z.loadMerges()
	if err != nil {
		return []Result{fail(Target{}, z.MergeRecord, err)}
	}
	var failed []Result
	for _, e := range rec.Entries {
		if z.merged[mergeKey{e.Placement, strings.ToLower(e.Path)}] || skip(e.Placement) {
			continue
		}
		_, err := z.Guard.CheckLeaf(e.Path, placement.ScopeUser)
		if err == nil {
			err = z.mergeInto(e.Placement, e.Path, e.Path, e.Path, nil)
		}
		if err != nil {
			failed = append(failed, fail(Target{Placement: e.Placement}, e.Path, err))
		}
	}
	return failed
}

// RegenerateUserFiles writes the files entries of machine placement name,
// described by spec, whose scope is user, into the user's profile: the
// per-user helper calls it with the root and items the service put in the
// plan (service.UserFileSet), since the service does not expose a machine
// placement's root to the user guard. It returns the failed Result, or ok.
func (z *ZipInstaller) RegenerateUserFiles(name string, spec placement.Spec, root string, items []render.Item) (res Result, ok bool) {
	if err := z.writeFiles(name, spec, root, items, placement.ScopeUser); err != nil {
		return fail(Target{Placement: name}, root, err), false
	}
	return Result{}, true
}

// UserFileSet returns what the user-scope files of machine placement name
// render from: its resolved root and the items its receipts describe, sorted
// by name and never nil. A root that does not exist yet has no items. A
// rootless placement has neither.
func (z *ZipInstaller) UserFileSet(name string, spec placement.Spec) (string, []render.Item, error) {
	root, rcs, err := z.fileInputs(name, spec)
	if err != nil {
		return "", nil, err
	}
	if root != "" {
		if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
			rcs = nil
		}
	}
	slices.SortFunc(rcs, func(a, b receipt.Receipt) int { return strings.Compare(a.Item, b.Item) })
	return root, fileItems(rcs), nil
}
