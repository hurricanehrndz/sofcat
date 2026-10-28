//go:build brand

package main

import _ "embed"

// defaultIcon is the brand's icon.png, staged here (gitignored) by
// `just brand=… build`. See icon_stock.go.
//
//go:embed brand-icon.png
var defaultIcon []byte
