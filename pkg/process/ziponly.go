package process

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// zipOnly selects the zip-only rule set (spec R21). It is zipOnlyBuild, set
// by the zip_only build tag; tests flip it to check both rule sets.
var zipOnly = zipOnlyBuild

// ZipOnlyReason says why a zip-only build must not offer, install or remove
// item, or returns "" when it may (always, in a stock build). Such a build has
// no command installer, so it rejects any item that needs one: a non-zip
// installer.type or a set uninstaller.type other than zip. Catalog scripts
// are in every build.
func ZipOnlyReason(item catalog.Item) string {
	if !zipOnly {
		return ""
	}
	var reasons []string
	if item.Installer.Type != "zip" {
		reasons = append(reasons, fmt.Sprintf("installer.type %q is not zip", item.Installer.Type))
	}
	if t := item.Uninstaller.Type; t != "" && t != "zip" {
		reasons = append(reasons, fmt.Sprintf("uninstaller.type %q is not zip", t))
	}
	return strings.Join(reasons, "; ")
}

// zipOnlyWarned holds the item/reason pairs already logged, so a long-running
// service that resolves an item on every run and list call warns once.
var zipOnlyWarned sync.Map

// WarnZipOnly logs, once per item and reason for the life of the process,
// that a zip-only build rejected the item.
func WarnZipOnly(itemName, reason string) {
	if _, seen := zipOnlyWarned.LoadOrStore(itemName+"\x00"+reason, true); seen {
		return
	}
	slog.Warn("skipping catalog item: rejected by this zip-only build", "item", itemName, "reason", reason)
}
