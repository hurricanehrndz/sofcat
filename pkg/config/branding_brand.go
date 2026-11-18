//go:build brand_branding

package config

import _ "embed"

// compiledBranding is the brand's branding.yaml, staged here (gitignored) by
// `just brand=… build`. See branding_stock.go.
//
//go:embed brand-branding.yaml
var compiledBranding []byte
