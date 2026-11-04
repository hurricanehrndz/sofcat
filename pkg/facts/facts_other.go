//go:build !windows && !darwin

package facts

import "github.com/hurricanehrndz/sofcat/pkg/conditions"

// platformFacts adds nothing on Linux: the Windows and macOS facts stay unset,
// so a condition that uses them is not installable during development.
func platformFacts(conditions.Facts) {}
