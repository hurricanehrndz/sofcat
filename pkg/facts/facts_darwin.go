//go:build darwin

package facts

import "github.com/hurricanehrndz/sofcat/pkg/conditions"

// platformFacts adds the macOS facts (macFacts). Each command runs under the
// admin-script timeout.
func platformFacts(facts conditions.Facts) { macFacts(facts, runCommand) }
