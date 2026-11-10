// Package catalog declares the catalog schema shared by the agent and the
// repository tools. Keep it free of agent dependencies (config, download).
package catalog

// Item contains an individual entry from the catalog
type Item struct {
	Name                string        `yaml:"-"`
	Dependencies        []string      `yaml:"dependencies,omitempty"`
	DisplayName         string        `yaml:"display_name,omitempty"`
	Check               InstallCheck  `yaml:"check,omitempty"`
	Installer           InstallerItem `yaml:"installer,omitempty"`
	Uninstaller         InstallerItem `yaml:"uninstaller,omitempty"`
	Version             string        `yaml:"version,omitempty"`
	BlockingApps        []string      `yaml:"blocking_apps,omitempty"`
	UpdateFor           []string      `yaml:"update_for,omitempty"`
	Description         string        `yaml:"description,omitempty"`
	Category            string        `yaml:"category,omitempty"`
	Section             string        `yaml:"section,omitempty"` // storefront tab; free text like Category
	Developer           string        `yaml:"developer,omitempty"`
	IconName            string        `yaml:"icon_name,omitempty"`
	RestartAction       string        `yaml:"restart_action,omitempty"`
	PreScript           string        `yaml:"preinstall_script,omitempty"`
	PostScript          string        `yaml:"postinstall_script,omitempty"`
	PreUninstallScript  string        `yaml:"preuninstall_script,omitempty"`
	PostUninstallScript string        `yaml:"postuninstall_script,omitempty"`
	// InstallableCondition is an expression over this machine's facts that
	// must be true for the item to be installed here (see site/src/content/docs/guides/conditions.md).
	InstallableCondition string `yaml:"installable_condition,omitempty"`
	// DependsCheck lists software the item needs and sofcat does not install
	// for it. While any is missing, installs and updates are deferred.
	DependsCheck []DependsCheck `yaml:"depends_check,omitempty"`
	// CustomActionLabels replaces the storefront's button and status labels,
	// keyed by ActionLabelKeys (Munki's keys plus cancel, pending_install and
	// pending_remove). A key it leaves out falls back to the item's
	// placements, then the built-in label.
	CustomActionLabels map[string]string `yaml:"custom_action_labels,omitempty"`
	// NotInstallable says why InstallableCondition rejected this machine. It is
	// set on the agent by conditions.Apply and is empty when the item may be
	// installed.
	NotInstallable string `yaml:"-"`
}

// DependsCheck is one depends_check entry: software named DisplayName, which
// is present when any of its checks that apply on this OS passes. An entry
// with no check for this OS is skipped.
type DependsCheck struct {
	DisplayName string `yaml:"display_name,omitempty"`
	// Sofcat is a catalog item name, present when sofcat finds that item installed.
	Sofcat string `yaml:"sofcat,omitempty"`
	// Munki is a Munki item name, checked on macOS: present when Munki's
	// report lists it as installed.
	Munki string `yaml:"munki,omitempty"`
	// Script holds a script per OS (DependsCheckScriptOS); exit 0 means present.
	Script map[string]string `yaml:"script,omitempty"`
}

// DependsCheckScriptOS are the keys of a depends_check script: Windows
// PowerShell under windows, an executable with a #! line under darwin.
var DependsCheckScriptOS = []string{"darwin", "windows"}

// InstallerItem holds information about how to install a catalog item
type InstallerItem struct {
	Type      string   `yaml:"type,omitempty"`
	Location  string   `yaml:"location,omitempty"`
	Hash      string   `yaml:"hash,omitempty"`
	PackageID string   `yaml:"package_id,omitempty"`
	Arguments []string `yaml:"arguments,omitempty"`
	// SuccessCodes are non-zero exit codes that still mean the action worked.
	// Unset, the type's defaults apply (msi and nupkg accept 1641 and 3010,
	// "success, restart required"); a list, even an empty one, replaces them.
	SuccessCodes []int `yaml:"success_codes,omitempty"`
	// Variants holds a zip item's archives: placement name, then OS (darwin
	// or windows). Only type zip uses it, in place of Location and Hash.
	Variants map[string]map[string]Variant `yaml:"variants,omitempty"`
}

// Variant is one zip archive of a zip item (see site/src/content/docs/guides/zip-items.md).
type Variant struct {
	Location string `yaml:"location,omitempty"`
	// Hash is the lowercase hex sha256 of the zip file.
	Hash string `yaml:"hash,omitempty"`
	// TreeHash is the tree hash of the zip's contents, "sha256:<hex>".
	TreeHash string `yaml:"tree_hash,omitempty"`
}

// InstallCheck holds information about how to check the status of a catalog item
type InstallCheck struct {
	File     []FileCheck `yaml:"file,omitempty"`
	Script   string      `yaml:"script,omitempty"`
	Registry RegCheck    `yaml:"registry,omitempty"`
	Appx     AppxCheck   `yaml:"appx,omitempty"`
}

// FileCheck holds information about checking via a file
type FileCheck struct {
	Path        string `yaml:"path,omitempty"`
	Version     string `yaml:"version,omitempty"`
	ProductName string `yaml:"product_name,omitempty"`
	Hash        string `yaml:"hash,omitempty"`
}

// RegCheck holds information about checking via registry
type RegCheck struct {
	Name    string `yaml:"name,omitempty"`
	Version string `yaml:"version,omitempty"`
}

// AppxCheck holds information about checking an installed AppX/MSIX package
type AppxCheck struct {
	Name    string `yaml:"name,omitempty"`
	Version string `yaml:"version,omitempty"`
}

// ActionLabelKeys are the keys of custom_action_labels and of a
// placement's action_labels: Munki's, plus sofcat's cancel, the
// pending_install and pending_remove statuses of a queued request, and
// applies_to, the prefix of the storefront's applies_to line.
var ActionLabelKeys = []string{
	"install", "installing", "installed", "remove", "removing", "update", "cancel",
	"pending_install", "pending_remove", "applies_to",
}
