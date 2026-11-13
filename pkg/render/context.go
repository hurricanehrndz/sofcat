package render

import "io/fs"

// FileContext is what a placement file template renders with (zip-items
// guide, "Templates for placement files"). Facts are reached through the
// fact and cond functions, not a field.
type FileContext struct {
	// OS is windows or darwin.
	OS string
	// Scope is machine or user.
	Scope string
	// Placement is the placement the file belongs to.
	Placement Placement
	// Items are the receipts of the placement, sorted by item.
	Items []Item
	// User is the login name in user scope; empty in machine scope.
	User string
}

// Placement describes a placement to a template.
type Placement struct {
	Name string
	// Root is the expanded placement root, whatever file renders.
	Root string
	// Vars is the placement's vars, as JSON values.
	Vars any
}

// Item is one placed tree, from its receipt. The JSON names are the
// receipt's, so {{ json .Items }} reads like the receipts. HasFile and
// ReadJSON look inside the tree.
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
	// tree is where HasFile and ReadJSON look (WithTree); nil is the
	// directory at Path.
	tree fs.FS
}

// TreeContext is what a template inside a placed tree renders with. It has
// no Items, and the item has no PlacedHash, which is the hash
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
