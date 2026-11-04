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

// windowsReserved are device names Windows will not use as a file name, with
// or without an extension. The port names include digit 0 and the
// superscript digits ¹ ² ³, which Windows also treats as devices.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM0": true, "COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"COM¹": true, "COM²": true, "COM³": true,
	"LPT0": true, "LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	"LPT¹": true, "LPT²": true, "LPT³": true,
}

// checkSegment refuses a path segment Windows cannot hold, on every OS, so an
// archive behaves the same everywhere.
func checkSegment(seg string) error {
	switch {
	case seg == "" || seg == "." || seg == "..":
		return fmt.Errorf("empty, . or .. segment")
	case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
		return fmt.Errorf("segment %q ends in a dot or a space", seg)
	}
	for _, c := range []byte(seg) {
		if c < 0x20 || strings.IndexByte(`<>:"|?*\`, c) >= 0 {
			return fmt.Errorf("segment %q has a character Windows forbids", seg)
		}
	}
	stem, _, _ := strings.Cut(seg, ".")
	if windowsReserved[strings.ToUpper(strings.TrimRight(stem, " "))] {
		return fmt.Errorf("segment %q is a Windows reserved name", seg)
	}
	return nil
}

// checkEntries refuses an archive whose headers break R11 or R12 before
// anything is extracted.
func checkEntries(files []*zip.File) error {
	if len(files) > maxZipEntries {
		return fmt.Errorf("%w: %d entries, more than %d", errUnsafeArchive, len(files), maxZipEntries)
	}
	seen := map[string]bool{} // lowercased file paths
	dirs := map[string]bool{} // lowercased parent directories
	var total uint64
	for _, f := range files {
		name := f.Name
		if name == "" || strings.HasPrefix(name, "/") {
			return fmt.Errorf("%w: %q is an absolute path", errUnsafeArchive, name)
		}
		if !f.Mode().IsRegular() {
			return fmt.Errorf("%w: %q is not a regular file (%s)", errUnsafeArchive, name, f.Mode().Type())
		}
		segs := strings.Split(name, "/")
		for _, seg := range segs {
			if err := checkSegment(seg); err != nil {
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
	if err = checkEntries(zr.File); err != nil {
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
	var total uint64
	for _, f := range zr.File {
		var n uint64
		n, err = extractFile(f, dest)
		if err != nil {
			return err
		}
		if total += n; total > maxZipTotalSize {
			return fmt.Errorf("%w: more than %d bytes uncompressed", errUnsafeArchive, maxZipTotalSize)
		}
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

// extractFile writes one checked entry under dest and returns the bytes it
// wrote. It counts what it actually reads, so a header that understates the
// size cannot get past the limits.
func extractFile(f *zip.File, dest string) (uint64, error) {
	target := filepath.Join(dest, filepath.FromSlash(f.Name))
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
		return 0, fmt.Errorf("%w: %q holds more than its header declares", errUnsafeArchive, f.Name)
	}
	if runtime.GOOS != "windows" {
		// Exact mode whatever the umask, since the tree hash reads it.
		if err := os.Chmod(target, mode); err != nil {
			return 0, err
		}
	}
	return written, nil
}
