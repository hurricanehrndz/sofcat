//go:build darwin

package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// userState is ~/Library/Application Support/<Name>, which holds the
// receipts and the log, and the placement variables of the user's own
// session: $HOME.
func userState() (string, map[string]string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return "", nil, errors.New("HOME is not set")
	}
	return filepath.Join(home, "Library", "Application Support", identity.Name), map[string]string{"HOME": home}, nil
}
