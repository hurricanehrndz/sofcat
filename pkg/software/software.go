// Package software checks a catalog item's depends_check entries: software
// sofcat needs on the machine but does not install for the item (see
// site/src/content/docs/guides/required-software.md).
package software

import (
	"bytes"
	"cmp"
	"encoding/xml"
	"errors"
	"os"
	"runtime"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

var (
	// GOOS is the OS whose checks apply: munki on darwin, and the script
	// under this key. Tests set it.
	GOOS = runtime.GOOS
	// MunkiReport is Munki's run report, which lists every installed managed
	// item under InstalledItems. Tests point it at a fixture. The
	// SOFCAT_TEST_MUNKI_REPORT environment variable replaces it for the macOS
	// smoke (test/manual/run-tapas-smoke.sh), which must not touch the rig's
	// real Munki report; only root can set a daemon's environment.
	MunkiReport = cmp.Or(os.Getenv("SOFCAT_TEST_MUNKI_REPORT"), "/Library/Managed Installs/ManagedInstallReport.plist")

	// munkiInstalled is the Munki check.
	munkiInstalled = munkiReportLists
)

// Checks are the checks the caller runs for Missing. A nil one never passes.
type Checks struct {
	// Sofcat reports whether sofcat finds the catalog item installed.
	Sofcat func(itemName string) bool
	// Script runs a depends_check script for this OS and reports whether it
	// exited 0.
	Script func(script string) bool
}

// Missing returns the display names of item's depends_check software that is
// absent, in catalog order. An entry is present when any of its checks that
// apply on this OS passes: sofcat everywhere, munki on darwin, and the script
// for this OS, tried in that order. An entry with none is skipped.
func Missing(item catalog.Item, checks Checks) []string {
	var missing []string
	for _, dep := range item.DependsCheck {
		script := dep.Script[GOOS]
		munki := ""
		if GOOS == "darwin" {
			munki = dep.Munki
		}
		if dep.Sofcat == "" && munki == "" && script == "" {
			continue
		}
		present := dep.Sofcat != "" && checks.Sofcat != nil && checks.Sofcat(dep.Sofcat) ||
			munki != "" && munkiInstalled(munki) ||
			script != "" && checks.Script != nil && checks.Script(script)
		if !present {
			missing = append(missing, dep.DisplayName)
		}
	}
	return missing
}

// munkiReportLists reports whether MunkiReport's InstalledItems names name.
// A missing, unreadable or binary report counts as not listing it.
func munkiReportLists(name string) bool {
	data, err := os.ReadFile(MunkiReport)
	if err != nil {
		return false
	}
	items, err := installedItems(data)
	if err != nil {
		return false
	}
	for _, item := range items {
		if item == name {
			return true
		}
	}
	return false
}

// plistNode is one element of an XML plist, with its children.
type plistNode struct {
	XMLName  xml.Name
	Text     string      `xml:",chardata"`
	Children []plistNode `xml:",any"`
}

// errBinaryPlist: only XML plists are read; Munki 7 writes XML.
var errBinaryPlist = errors.New("binary plist")

// installedItems returns the strings in the InstalledItems array of an XML
// plist whose top level is a dict. A plist without the key has none.
func installedItems(data []byte) ([]string, error) {
	if bytes.HasPrefix(data, []byte("bplist00")) {
		return nil, errBinaryPlist
	}
	var doc struct {
		XMLName xml.Name  `xml:"plist"`
		Dict    plistNode `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	// A dict alternates key and value elements.
	kids := doc.Dict.Children
	for i := 0; i+1 < len(kids); i++ {
		if kids[i].XMLName.Local != "key" || kids[i].Text != "InstalledItems" {
			continue
		}
		var items []string
		if kids[i+1].XMLName.Local == "array" {
			for _, v := range kids[i+1].Children {
				if v.XMLName.Local == "string" {
					items = append(items, v.Text)
				}
			}
		}
		return items, nil
	}
	return nil, nil
}
