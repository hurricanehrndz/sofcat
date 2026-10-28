// Package identity names this build of SofCat. A rebranded build sets Slug
// and Name with -ldflags "-X"; every runtime name that two side-by-side
// installs must not share (service, pipe, data directory, config path,
// policy key, UI log directory) derives from them here, so distinct slugs
// cannot collide. The defaults are the stock SofCat names.
package identity

import (
	"os"
	"path/filepath"
)

var (
	// Slug is the lowercase machine name: service name, pipe prefix and
	// per-user directory.
	Slug = "sofcat"
	// Name is the display name: the ProgramData directory and the policy key.
	Name = "SofCat"
)

// ServiceName is the default Windows service name.
func ServiceName() string { return Slug }

// PipeName is the default service pipe name.
func PipeName() string { return Slug + "-service" }

// DataDir is the default app data directory, %ProgramData%\<Name>.
func DataDir() string { return filepath.Join(os.Getenv("ProgramData"), Name) }

// ConfigPath is the default config.yaml path inside DataDir.
func ConfigPath() string { return filepath.Join(DataDir(), "config.yaml") }

// PolicyKey is the HKLM policy key MDM or GPO writes, without the hive.
func PolicyKey() string { return `SOFTWARE\Policies\` + Name }
