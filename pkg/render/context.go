package render

// FileContext is what a placement file template renders with (zip-items
// guide, "Templates for placement files"). Facts are reached through the
// fact and cond functions, not a field.
type FileContext struct {
	// OS is windows or darwin.
	OS string
	// Scope is machine or user.
	Scope string
	// Placement is the placement the file belongs to: for a settings
	// placement, the settings placement itself.
	Placement Placement
	// Registers is the registered placement of a settings placement; nil
	// for a marketplace manifest.
	Registers *Registered
	// Items are the receipts of the placement the file describes (the
	// registered one for a settings placement), sorted by item.
	Items []Item
	// User is the login name in user scope; empty in machine scope.
	User string
}

// Placement describes a placement to a template.
type Placement struct {
	Name string
	// Root is the expanded placement root: the file's path for a settings
	// placement.
	Root string
	// Kind is directory, marketplace or settings.
	Kind string
	// Marketplace is the marketplace block, for a marketplace placement
	// only; nil otherwise.
	Marketplace *Marketplace
}

// Marketplace is a marketplace block as harnesses.yaml holds it. Owner,
// Renames and AllowCrossMarketplaceDependenciesOn are the YAML values.
type Marketplace struct {
	Name                                string
	Owner                               any
	Renames                             any
	AllowCrossMarketplaceDependenciesOn any
}

// Registered is the placement a settings placement registers.
type Registered struct {
	Name string
	Root string
	Kind string
}

// Item is one placed tree, from its receipt. The JSON names are the
// receipt's, so {{ json .Items }} reads like the receipts.
type Item struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Managed is true when a manifest lists the item in managed_installs.
	Managed bool `json:"managed"`
	// Path is the tree's directory.
	Path string `json:"path"`
	// TreeHash is the catalog's hash of the archive's contents; PlacedHash
	// the hash of the tree as sofcat wrote it, which changes when a rendered
	// file does.
	TreeHash   string `json:"tree_hash"`
	PlacedHash string `json:"placed_hash"`
}

// TreeContext is what a template inside a placed tree renders with. It has
// no Items or Registers, and the item has no PlacedHash, which is the hash
// of the output.
type TreeContext struct {
	OS        string
	Scope     string
	Placement Placement
	Item      TreeItem
	// User is the login name of the account the helper runs as, without
	// domain, in user scope; empty in machine scope.
	User string
}

// TreeItem is the item whose tree is being rendered.
type TreeItem struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Managed  bool   `json:"managed"`
	Path     string `json:"path"`
	TreeHash string `json:"tree_hash"`
}
