//go:build windows

package service

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// -serviceremove started through an 8.3 short path or a \\?\ path must still
// end the agent beside it, whose image path the process reports in its long
// form.
func TestIsAgentImageShortPath(t *testing.T) {
	long := filepath.Join(t.TempDir(), "sofcat-useragent-long-name.exe")
	if err := os.WriteFile(long, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Fatalf("GetShortPathName(%q): %v", long, err)
	}
	short := windows.UTF16ToString(buf[:n])
	if short == long {
		t.Skipf("8.3 names are disabled on the volume of %s", long)
	}
	for _, tc := range []struct{ image, agent string }{
		{long, short},
		{short, long},
		{`\\?\` + long, short},
	} {
		if !isAgentImage(tc.image, []string{tc.agent}) {
			t.Errorf("isAgentImage(%q, [%q]) = false, want true", tc.image, tc.agent)
		}
	}
}
