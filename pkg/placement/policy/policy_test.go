package policy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const xmlPolicy = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadIdentifier</key><string>com.example.sofcat</string>
	<key>WriteRoots</key>
	<dict>
		<key>Machine</key>
		<array>
			<string>/Library/Application Support/ClaudeCode</string>
			<integer>7</integer>
			<string>/etc/codex</string>
		</array>
		<key>User</key>
		<string>${HOME}/.claude</string>
	</dict>
</dict>
</plist>`

// TestParsePlist: MDM delivers the payload as XML or binary, and an admin's
// typo in one entry or key must not cost them the rest of the policy, while
// a file that does not parse adds nothing.
func TestParsePlist(t *testing.T) {
	// Written by Python's plistlib (FMT_BINARY); the library only encodes XML.
	binary, err := os.ReadFile(filepath.Join("testdata", "write-roots-binary.plist"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name          string
		data          string
		machine, user []string
		bad           bool
	}{
		{
			"xml, mixed valid and invalid", xmlPolicy,
			[]string{"/Library/Application Support/ClaudeCode", "/etc/codex"},
			nil, false,
		},
		{
			"binary", string(binary),
			[]string{"/Library/Application Support/ClaudeCode", "/etc/codex"},
			[]string{"${HOME}/.claude"},
			false,
		},
		{"no WriteRoots key", `<plist><dict><key>Other</key><string>x</string></dict></plist>`, nil, nil, false},
		{"WriteRoots not a dict", `<plist><dict><key>WriteRoots</key><string>/etc</string></dict></plist>`, nil, nil, true},
		{"garbage", "not a plist <dict", nil, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machine, user, err := parsePlist([]byte(c.data))
			if (err != nil) != c.bad {
				t.Fatalf("err = %v, want error %v", err, c.bad)
			}
			if !reflect.DeepEqual(machine, c.machine) || !reflect.DeepEqual(user, c.user) {
				t.Errorf("machine %q user %q, want %q %q", machine, user, c.machine, c.user)
			}
		})
	}
}
