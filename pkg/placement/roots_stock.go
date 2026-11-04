//go:build !brand_roots

package placement

// compiledRoots are the stock roots. A brand that ships write-roots.yaml is
// built with the brand_roots tag instead (roots_brand.go).
func compiledRoots() Roots { return StockRoots() }
