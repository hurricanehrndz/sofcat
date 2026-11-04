//go:build brand_roots

package placement

import _ "embed"

// brandRoots is the brand's write-roots.yaml, staged here (gitignored) by
// `just brand=… build`. See roots_stock.go.
//
//go:embed brand-write-roots.yaml
var brandRoots []byte

// compiledRoots parses the brand's roots. A malformed file is a broken build,
// so it panics at first use rather than writing anywhere.
func compiledRoots() Roots {
	r, err := ParseRoots(brandRoots)
	if err != nil {
		panic(err)
	}
	return r
}
