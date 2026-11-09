//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// userState is %LOCALAPPDATA%\<Name>, which holds the receipts and the log,
// and the placement variables of the user's own session: %USERPROFILE%.
func userState() (string, map[string]string, error) {
	profile, localAppData := os.Getenv("USERPROFILE"), os.Getenv("LOCALAPPDATA")
	if profile == "" || localAppData == "" {
		return "", nil, errors.New("USERPROFILE or LOCALAPPDATA is not set")
	}
	return filepath.Join(localAppData, identity.Name), map[string]string{"USERPROFILE": profile}, nil
}
