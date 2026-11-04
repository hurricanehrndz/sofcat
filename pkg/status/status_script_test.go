//go:build !zip_only

package status

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

var (
	scriptActionNoError = catalog.Item{
		Installer: catalog.InstallerItem{Type: `ps1`},
	}
	scriptNoActionNoError = catalog.Item{
		Installer:   catalog.InstallerItem{Type: `ps1`},
		DisplayName: `scriptNoActionNoError`,
	}
	scriptCheckItem = catalog.Item{
		Check: catalog.InstallCheck{
			Script: `echo "pizza"`,
		},
		DisplayName: `scriptCheckItem`,
	}
)

// TestCheckScript validates that a script is properly written disk, ran, and then deleted
// and the status is retrieved properly.
func TestCheckScript(t *testing.T) {
	// Override execCommand with our fake version
	execCommand = fakeExecCommand
	defer func() {
		execCommand = origExec
	}()

	// Set cachepath and run checkScript for scriptActionNoError
	cachepath := fmt.Sprintf("testdata/%s/", statusActionNoError)
	actionNeeded, err := checkScript(scriptActionNoError, cachepath, "install")
	if !actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript to action and no error")
	}

	// Set cachepath and run checkScript for scriptNoActionNoError
	cachepath = fmt.Sprintf("testdata/%s/", statusActionNoError)
	actionNeeded, err = checkScript(scriptActionNoError, cachepath, "uninstall")
	if actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript to no action and no error")
	}

	// Set cachepath and run checkScript for scriptNoActionNoError
	cachepath = fmt.Sprintf("testdata/%s/", statusNoActionNoError)
	actionNeeded, err = checkScript(scriptNoActionNoError, cachepath, "install")
	if actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript to return no action and no error")
	}

	// Set cachepath and run checkScript for scriptActionNoError
	cachepath = fmt.Sprintf("testdata/%s/", statusNoActionNoError)
	actionNeeded, err = checkScript(scriptNoActionNoError, cachepath, "uninstall")
	if !actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript to action and no error")
	}

	// Set cachepath and run checkScript for scriptActionNoError as update
	cachepath = fmt.Sprintf("testdata/%s/", statusActionNoError)
	actionNeeded, err = checkScript(scriptActionNoError, cachepath, "update")
	if !actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript update to action and no error")
	}

	// Set cachepath and run checkScript for scriptNoActionNoError as update
	cachepath = fmt.Sprintf("testdata/%s/", statusNoActionNoError)
	actionNeeded, err = checkScript(scriptNoActionNoError, cachepath, "update")
	if actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript update to no action and no error")
	}
}

// TestCheckStatusScript validates that a script check is ran
func TestCheckStatusScript(t *testing.T) {
	// Override execCommand with our fake version
	execCommand = fakeExecCommand
	defer func() { execCommand = origExec }()
	console := captureConsole(t)

	// Run CheckStatus with an item that has a script check
	_, _ = (&Checker{}).CheckStatus(scriptCheckItem, "install", "testdata/")

	if want := `msg="Checking status via script" item=scriptCheckItem`; !strings.Contains(console.String(), want) {
		t.Errorf("console output missing %q:\n%s", want, console.String())
	}
}
