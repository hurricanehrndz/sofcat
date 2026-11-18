//go:build !brand_branding

package config

// compiledBranding is empty in a build without a brand's branding.yaml. A
// brand that ships one is built with the brand_branding tag instead
// (branding_brand.go).
var compiledBranding []byte
