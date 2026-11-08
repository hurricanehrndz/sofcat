package installer

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
)

// Archive limits. Variables so tests can lower them instead of writing
// gigabytes.
var (
	maxZipEntries   = 10_000
	maxZipFileSize  = uint64(256 << 20)
	maxZipTotalSize = uint64(1 << 30)
	maxZipRatio     = uint64(100)
)

// errUnsafeArchive wraps every archive rejection.
var errUnsafeArchive = errors.New("unsafe archive")

// archiveEntry is a zip entry and the relative, slash-separated path it
// extracts to: a zip item's raw entry name, a nupkg's decoded one.
type archiveEntry struct {
	f    *zip.File
	name string
}

// zipEntries names each entry by its raw name, as a zip item extracts it.
func zipEntries(files []*zip.File) []archiveEntry {
	entries := make([]archiveEntry, len(files))
	for i, f := range files {
		entries[i] = archiveEntry{f: f, name: f.Name}
	}
	return entries
}

// checkEntries refuses an archive whose headers break R11 or R12 before
// anything is extracted.
func checkEntries(entries []archiveEntry) error {
	if len(entries) > maxZipEntries {
		return fmt.Errorf("%w: %d entries, more than %d", errUnsafeArchive, len(entries), maxZipEntries)
	}
	seen := map[string]bool{} // lowercased file paths
	dirs := map[string]bool{} // lowercased parent directories
	var total uint64
	for _, e := range entries {
		f, name := e.f, e.name
		if name == "" || strings.HasPrefix(name, "/") {
			return fmt.Errorf("%w: %q is an absolute path", errUnsafeArchive, name)
		}
		if !f.Mode().IsRegular() {
			return fmt.Errorf("%w: %q is not a regular file (%s)", errUnsafeArchive, name, f.Mode().Type())
		}
		segs := strings.Split(name, "/")
		for _, seg := range segs {
			if err := placement.CheckSegment(seg); err != nil {
				return fmt.Errorf("%w: %q: %w", errUnsafeArchive, name, err)
			}
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("%w: %q is listed twice, or twice differing only in case", errUnsafeArchive, name)
		}
		seen[key] = true
		for i := 1; i < len(segs); i++ {
			dirs[strings.ToLower(strings.Join(segs[:i], "/"))] = true
		}

		size := f.UncompressedSize64
		if size > maxZipFileSize {
			return fmt.Errorf("%w: %q is %d bytes, more than %d", errUnsafeArchive, name, size, maxZipFileSize)
		}
		if total += size; total > maxZipTotalSize {
			return fmt.Errorf("%w: more than %d bytes uncompressed", errUnsafeArchive, maxZipTotalSize)
		}
		if size > f.CompressedSize64*maxZipRatio {
			return fmt.Errorf("%w: %q compresses more than %d:1", errUnsafeArchive, name, maxZipRatio)
		}
	}
	for dir := range dirs {
		if seen[dir] {
			return fmt.Errorf("%w: %q is both a file and a directory", errUnsafeArchive, dir)
		}
	}
	return nil
}

// extractArchive verifies archive against hash, refuses it if unsafe, and
// extracts it into dest, which it creates and which must not exist. It then
// checks dest's tree hash for goos against treeHash. On any failure dest is
// removed.
func extractArchive(archive, hash, treeHash, dest, goos string) (err error) {
	if !download.Verify(archive, hash) {
		return fmt.Errorf("%s does not match its catalog hash", filepath.Base(archive))
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("%w: %w", errUnsafeArchive, err)
	}
	defer func() { _ = zr.Close() }()
	entries := zipEntries(zr.File)
	if err = checkEntries(entries); err != nil {
		return err
	}

	if err = os.Mkdir(dest, 0o755); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	if err = extractEntries(entries, dest); err != nil {
		return err
	}

	got, err := artifact.TreeHash(dest, goos)
	if err != nil {
		return err
	}
	if got != treeHash {
		return fmt.Errorf("extracted tree hash %s does not match the catalog's %s", got, treeHash)
	}
	return nil
}

// extractEntries writes checked entries under dest, which must exist, and
// stops once more than maxZipTotalSize bytes have actually been written.
func extractEntries(entries []archiveEntry, dest string) error {
	var total uint64
	for _, e := range entries {
		n, err := extractFile(e, dest)
		if err != nil {
			return err
		}
		if total += n; total > maxZipTotalSize {
			return fmt.Errorf("%w: more than %d bytes uncompressed", errUnsafeArchive, maxZipTotalSize)
		}
	}
	return nil
}

// extractFile writes one checked entry under dest and returns the bytes it
// wrote. It counts what it actually reads, so a header that understates the
// size cannot get past the limits.
func extractFile(e archiveEntry, dest string) (uint64, error) {
	f := e.f
	target := filepath.Join(dest, filepath.FromSlash(e.name))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	// Only the executable bit survives, and only where modes exist.
	mode := os.FileMode(0o644)
	if runtime.GOOS != "windows" && f.Mode()&0o111 != 0 {
		mode = 0o755
	}
	in, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return 0, err
	}
	limit := min(f.UncompressedSize64, maxZipFileSize)
	n, err := io.Copy(out, io.LimitReader(in, int64(limit)+1)) //nolint:gosec // G115: limit is at most maxZipFileSize
	err = errors.Join(err, out.Close())
	if err != nil {
		return 0, err
	}
	written := uint64(n) //nolint:gosec // G115: io.Copy never returns a negative count
	if written > limit || written > f.CompressedSize64*maxZipRatio {
		return 0, fmt.Errorf("%w: %q holds more than its header declares", errUnsafeArchive, e.name)
	}
	if runtime.GOOS != "windows" {
		// Exact mode whatever the umask, since the tree hash reads it.
		if err := os.Chmod(target, mode); err != nil {
			return 0, err
		}
	}
	return written, nil
}
