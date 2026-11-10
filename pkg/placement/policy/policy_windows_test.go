//go:build windows

package policy

import (
	"reflect"
	"testing"

	"golang.org/x/sys/windows/registry"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The reader runs against a scratch HKCU key so the test needs no admin token
// and never touches a real policy.
func TestReadPolicyRootsFromRegistry(t *testing.T) {
	path := `Software\SofCatTest\` + t.Name()
	policyRoot, policyPath = registry.CURRENT_USER, path
	t.Cleanup(func() {
		policyRoot, policyPath = registry.LOCAL_MACHINE, identity.PolicyKey()+`\WriteRoots`
		_ = registry.DeleteKey(registry.CURRENT_USER, path)
	})

	if machine, user := readPolicyRoots(); machine != nil || user != nil {
		t.Fatalf("missing key read as %q %q", machine, user)
	}

	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err = key.SetStringsValue("Machine", []string{`${ProgramFiles}\ClaudeCode`, `C:\codex`}); err != nil {
		t.Fatal(err)
	}
	// A REG_SZ where REG_MULTI_SZ belongs is dropped, not split or guessed at.
	if err = key.SetStringValue("User", `${USERPROFILE}\.claude`); err != nil {
		t.Fatal(err)
	}
	_ = key.Close()

	machine, user := readPolicyRoots()
	if want := []string{`${ProgramFiles}\ClaudeCode`, `C:\codex`}; !reflect.DeepEqual(machine, want) {
		t.Errorf("Machine = %q, want %q", machine, want)
	}
	if user != nil {
		t.Errorf("REG_SZ User read as %q, want nothing", user)
	}
}
