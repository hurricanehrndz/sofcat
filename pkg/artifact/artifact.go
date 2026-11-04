// Package artifact defines the zip artifact format: the tree hash and the
// deterministic zip writer that makecatalogs and the agent share (see
// docs/zip-items.md). Keep it free of agent dependencies, like pkg/catalog.
package artifact

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Target OS names. Any other value keeps the files' executable bits, as
// darwin does; windows records every file as 644.
const (
	Darwin  = "darwin"
	Windows = "windows"
)

// epoch is the timestamp of every zip entry: the earliest a zip can carry.
var epoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

type file struct {
	path string // slash-separated, relative to the tree root
	exec bool
	src  string // path on disk
}

// Tree is a directory read for hashing or packing: its regular files, sorted
// by slash-separated relative path in byte order.
type Tree struct {
	files []file
}

// Read lists the regular files under dir. goos is the target OS: for
// Windows every file is recorded as 644, otherwise a file with any
// executable bit set is 755. Read refuses a tree that holds a symlink or any
// other non-regular file, and one with no files; empty directories do not
// count.
func Read(dir, goos string) (*Tree, error) {
	var t Tree
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: not a regular file (%s)", path, d.Type())
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		t.files = append(t.files, file{
			path: filepath.ToSlash(rel),
			exec: goos != Windows && info.Mode()&0o111 != 0,
			src:  path,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(t.files) == 0 {
		return nil, fmt.Errorf("%s: no files", dir)
	}
	sort.Slice(t.files, func(i, j int) bool { return t.files[i].path < t.files[j].path })
	return &t, nil
}

// Paths returns the slash-separated relative path of every file, sorted.
func (t *Tree) Paths() []string {
	paths := make([]string, len(t.files))
	for i, f := range t.files {
		paths[i] = f.path
	}
	return paths
}

// Hash returns the tree hash, "sha256:" and the lowercase hex sha256 of one
// record per file: "<path>\x00<755|644>\x00<hex sha256 of content>\n".
func (t *Tree) Hash() (string, error) {
	stream := sha256.New()
	for _, f := range t.files {
		if err := record(stream, f, io.Discard); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(stream.Sum(nil)), nil
}

// record copies f's content to w and appends f's tree-hash record to stream.
func record(stream hash.Hash, f file, w io.Writer) error {
	in, err := os.Open(f.src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	content := sha256.New()
	if _, err = io.Copy(io.MultiWriter(content, w), in); err != nil {
		return fmt.Errorf("read %s: %w", f.src, err)
	}
	mode := "644"
	if f.exec {
		mode = "755"
	}
	_, err = fmt.Fprintf(stream, "%s\x00%s\x00%x\n", f.path, mode, content.Sum(nil))
	return err
}

// Packed is what Pack reports about the zip it wrote.
type Packed struct {
	Hash     string `json:"hash"`      // lowercase hex sha256 of the zip
	TreeHash string `json:"tree_hash"` // "sha256:<hex>"
}

// WriteZip writes the tree as a deterministic zip to w: one Deflate entry per
// file in tree order, no directory entries, every entry dated 1980-01-01
// 00:00:00 UTC with mode 0644 or 0755. The same tree and Go version give the
// same bytes on every OS.
func (t *Tree) WriteZip(w io.Writer) (Packed, error) {
	zipSum := sha256.New()
	zw := zip.NewWriter(io.MultiWriter(w, zipSum))
	stream := sha256.New()
	for _, f := range t.files {
		header := &zip.FileHeader{Name: f.path, Method: zip.Deflate, Modified: epoch}
		header.SetMode(0o644)
		if f.exec {
			header.SetMode(0o755)
		}
		entry, err := zw.CreateHeader(header)
		if err != nil {
			return Packed{}, err
		}
		if err := record(stream, f, entry); err != nil {
			return Packed{}, err
		}
	}
	if err := zw.Close(); err != nil {
		return Packed{}, err
	}
	return Packed{
		Hash:     hex.EncodeToString(zipSum.Sum(nil)),
		TreeHash: "sha256:" + hex.EncodeToString(stream.Sum(nil)),
	}, nil
}

// TreeHash reads dir for goos and returns its tree hash.
func TreeHash(dir, goos string) (string, error) {
	t, err := Read(dir, goos)
	if err != nil {
		return "", err
	}
	return t.Hash()
}

// PackFile packs dir for goos into the zip file out. It writes a temporary
// file beside out and renames it into place, so a failure leaves no partial
// zip at out.
func PackFile(dir, goos, out string) (Packed, error) {
	t, err := Read(dir, goos)
	if err != nil {
		return Packed{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".pack-*.zip")
	if err != nil {
		return Packed{}, err
	}
	packed, err := t.WriteZip(tmp)
	// CreateTemp makes the file 0600; a zip is published, so make it readable.
	err = errors.Join(err, tmp.Chmod(0o644), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), out)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return Packed{}, err
	}
	return packed, nil
}
