package installer

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zipEntry struct {
	name string
	body string
	mode fs.FileMode // 0 means 0644
}

// writeTestZip writes entries as a zip in a new temp dir and returns its path
// and sha256. Unlike the packer it writes whatever it is given, so tests can
// build hostile archives.
func writeTestZip(t *testing.T, entries ...zipEntry) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return path, hex.EncodeToString(sum[:])
}

// TestExtractRejects covers every R11 and R12 rejection: each archive is
// refused before anything is placed, and leaves nothing behind.
func TestExtractRejects(t *testing.T) {
	many := make([]zipEntry, 4)
	for i := range many {
		many[i] = zipEntry{name: fmt.Sprintf("f%d", i), body: "x"}
	}
	cases := []struct {
		name    string
		entries []zipEntry
		limits  func()
	}{
		{name: "absolute path", entries: []zipEntry{{name: "/etc/passwd", body: "x"}}},
		{name: "dot-dot segment", entries: []zipEntry{{name: "../evil", body: "x"}}},
		{name: "inner dot-dot", entries: []zipEntry{{name: "a/../../evil", body: "x"}}},
		{name: "symlink entry", entries: []zipEntry{{name: "link", body: "/etc", mode: fs.ModeSymlink | 0o777}}},
		{name: "device entry", entries: []zipEntry{{name: "dev", mode: fs.ModeDevice | 0o644}}},
		{name: "directory entry", entries: []zipEntry{{name: "dir/", mode: fs.ModeDir | 0o755}}},
		{name: "duplicate path", entries: []zipEntry{{name: "a.md", body: "1"}, {name: "a.md", body: "2"}}},
		{name: "case collision", entries: []zipEntry{{name: "Skill.md", body: "1"}, {name: "skill.md", body: "2"}}},
		{name: "file and directory", entries: []zipEntry{{name: "a", body: "1"}, {name: "a/b", body: "2"}}},
		{name: "reserved name", entries: []zipEntry{{name: "CON", body: "x"}}},
		{name: "reserved name with extension", entries: []zipEntry{{name: "docs/nul.txt", body: "x"}}},
		{name: "reserved COM port", entries: []zipEntry{{name: "com7.md", body: "x"}}},
		{name: "reserved LPT port", entries: []zipEntry{{name: "Lpt1", body: "x"}}},
		{name: "reserved port zero", entries: []zipEntry{{name: "COM0", body: "x"}}},
		{name: "reserved superscript port", entries: []zipEntry{{name: "LPT¹.txt", body: "x"}}},
		{name: "reserved console name", entries: []zipEntry{{name: "CONIN$", body: "x"}}},
		{name: "alternate data stream", entries: []zipEntry{{name: "SKILL.md:stream", body: "x"}}},
		{name: "backslash", entries: []zipEntry{{name: `a\b`, body: "x"}}},
		{name: "forbidden character", entries: []zipEntry{{name: "a<b>", body: "x"}}},
		{name: "control byte", entries: []zipEntry{{name: "a\x01b", body: "x"}}},
		{name: "trailing dot", entries: []zipEntry{{name: "dir./a", body: "x"}}},
		{name: "trailing space", entries: []zipEntry{{name: "a ", body: "x"}}},
		{name: "too many entries", entries: many, limits: func() { maxZipEntries = 3 }},
		{name: "file over the size limit", entries: []zipEntry{{name: "big", body: strings.Repeat("ab", 64)}}, limits: func() { maxZipFileSize = 64 }},
		{name: "total over the size limit", entries: []zipEntry{{name: "a", body: strings.Repeat("ab", 40)}, {name: "b", body: strings.Repeat("ab", 40)}}, limits: func() { maxZipTotalSize = 100 }},
		{name: "zip bomb", entries: []zipEntry{{name: "bomb", body: strings.Repeat("\x00", 1<<20)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, fileSize, totalSize := maxZipEntries, maxZipFileSize, maxZipTotalSize
			t.Cleanup(func() { maxZipEntries, maxZipFileSize, maxZipTotalSize = entries, fileSize, totalSize })
			if tc.limits != nil {
				tc.limits()
			}
			archive, hash := writeTestZip(t, tc.entries...)
			dest := filepath.Join(t.TempDir(), "stage")
			err := extractArchive(archive, hash, "sha256:any", dest, testOS())
			t.Log(err)
			if !errors.Is(err, errUnsafeArchive) {
				t.Fatalf("extractArchive err = %v, want an unsafe-archive refusal", err)
			}
			if _, statErr := os.Lstat(dest); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("refused archive left %s behind", dest)
			}
		})
	}
}

// TestExtractVerifies: a wrong archive hash (R10) or a tree that does not
// hash to the catalog's tree_hash (R13) fails and leaves nothing on disk,
// while a matching archive extracts with its executable bit.
func TestExtractVerifies(t *testing.T) {
	archive, hash := writeTestZip(t,
		zipEntry{name: "SKILL.md", body: "# hi\n"},
		zipEntry{name: "bin/run", body: "#!/bin/sh\n", mode: 0o755},
	)
	treeHash := treeHashOf(t, map[string]string{"SKILL.md": "# hi\n", "bin/run": "#!/bin/sh\n"}, "bin/run")

	dest := filepath.Join(t.TempDir(), "stage")
	if err := extractArchive(archive, strings.Repeat("0", 64), treeHash, dest, testOS()); err == nil {
		t.Fatal("hash mismatch accepted")
	}
	assertAbsent(t, dest)

	if err := extractArchive(archive, hash, "sha256:"+strings.Repeat("0", 64), dest, testOS()); err == nil {
		t.Fatal("tree hash mismatch accepted")
	}
	assertAbsent(t, dest)

	if err := extractArchive(archive, hash, treeHash, dest, testOS()); err != nil {
		t.Fatalf("valid archive refused: %v", err)
	}
	info, err := os.Stat(filepath.Join(dest, "bin", "run"))
	if err != nil {
		t.Fatal(err)
	}
	if want := fs.FileMode(0o755); osHasModes() && info.Mode().Perm() != want {
		t.Errorf("bin/run mode = %v, want %v", info.Mode().Perm(), want)
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s exists after a failed extraction", path)
	}
}
