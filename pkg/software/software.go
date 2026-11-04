// Package software checks a catalog item's requires_software entries: software
// sofcat needs on the machine but never installs (see docs/zip-items.md).
package software

import (
	"bytes"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

var (
	// GOOS is the OS whose entries are checked: munki on darwin, chocolatey
	// on windows. Tests set it.
	GOOS = runtime.GOOS
	// MunkiReport is Munki's run report, which lists every installed managed
	// item under InstalledItems. Tests point it at a fixture.
	MunkiReport = "/Library/Managed Installs/ManagedInstallReport.plist"
	// ChocolateyLib returns Chocolatey's package directory, which holds one
	// directory per installed package id. Tests replace it.
	ChocolateyLib = func() string {
		dir := os.Getenv("ProgramData")
		if dir == "" {
			dir = `C:\ProgramData`
		}
		return filepath.Join(dir, "chocolatey", "lib")
	}

	// munkiInstalled and chocolateyInstalled are the per-OS checks.
	munkiInstalled      = munkiReportLists
	chocolateyInstalled = chocolateyLibHas
)

// Missing returns the display names of item's required software that is
// absent on this OS, in catalog order. An entry with no key for this OS is
// skipped.
func Missing(item catalog.Item) []string {
	var missing []string
	for _, req := range item.RequiresSoftware {
		var present bool
		switch {
		case GOOS == "darwin" && req.Munki != "":
			present = munkiInstalled(req.Munki)
		case GOOS == "windows" && req.Chocolatey != "":
			present = chocolateyInstalled(req.Chocolatey)
		default:
			continue
		}
		if !present {
			missing = append(missing, req.DisplayName)
		}
	}
	return missing
}

// chocolateyLibHas reports whether Chocolatey's lib directory holds id. An id
// that is not a single path element never matches.
func chocolateyLibHas(id string) bool {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\:`) {
		return false
	}
	info, err := os.Stat(filepath.Join(ChocolateyLib(), id))
	return err == nil && info.IsDir()
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
