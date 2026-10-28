//go:build !windows

package facts

import "github.com/hurricanehrndz/sofcat/pkg/conditions"

// platformFacts adds nothing off Windows: the Windows-only facts stay unset,
// so a condition that uses them is not installable during development.
func platformFacts(conditions.Facts) {}
