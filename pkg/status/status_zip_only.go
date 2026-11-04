//go:build zip_only

package status

import (
	"errors"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// scriptCheckFunc refuses a check.script: a zip-only build compiles no
// catalog script runner (status_script.go is left out). process rejects such
// items before they are checked; this fails any that still are.
var scriptCheckFunc = func(catalog.Item, string, string) (bool, error) {
	return false, errors.New("check scripts are not available in this build")
}
