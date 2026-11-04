// Package identity names this build of SofCat. A rebranded build sets Slug,
// Name and Identifier with -ldflags "-X"; every runtime name that two
// side-by-side installs must not share (service, pipe or socket, data
// directory, config path, policy key, launchd label, UI log directory)
// derives from them here, so distinct slugs cannot collide. The defaults are
// the stock SofCat names.
package identity

import (
	"os"
	"path/filepath"
	"runtime"
)

var (
	// Slug is the lowercase machine name: service name, pipe prefix and
	// per-user directory.
	Slug = "sofcat"
	// Name is the display name: the ProgramData directory and the policy key.
	Name = "SofCat"
	// Identifier is the reverse-DNS name: the launchd label and the app's
	// bundle identifier on macOS.
	Identifier = "ca.hrndz.sofcat"
)

// ServiceName is the default Windows service name.
func ServiceName() string { return Slug }

// PipeName is the default service pipe name; off Windows it names the socket.
func PipeName() string { return Slug + "-service" }

// DataDir is the default app data directory: %ProgramData%\<Name> on Windows,
// /Library/Application Support/<Name>/Data on macOS.
func DataDir() string { return DataDirFor(runtime.GOOS) }

// ConfigPath is the default config.yaml path: inside DataDir on Windows,
// beside it in SupportDir on macOS.
func ConfigPath() string { return ConfigPathFor(runtime.GOOS) }

// SocketDir is the directory holding the service socket off Windows.
func SocketDir() string { return SocketDirFor(runtime.GOOS) }

// PolicyKey is the HKLM policy key MDM or GPO writes, without the hive.
func PolicyKey() string { return `SOFTWARE\Policies\` + Name }

// SupportDir is the macOS install directory,
// /Library/Application Support/<Name>: the config, Data and bin live in it.
func SupportDir() string { return "/Library/Application Support/" + Name }

// BinDir holds the macOS CLI.
func BinDir() string { return SupportDir() + "/bin" }

// CLIPath is the installed macOS CLI.
func CLIPath() string { return BinDir() + "/" + Slug }

// AppPath is the installed macOS app bundle.
func AppPath() string { return "/Applications/" + Name + ".app" }

// LaunchdLabel is the macOS daemon's launchd label.
func LaunchdLabel() string { return Identifier }

// LaunchdPlistPath is the macOS daemon's property list.
func LaunchdPlistPath() string { return "/Library/LaunchDaemons/" + Identifier + ".plist" }

// DataDirFor is DataDir on goos.
func DataDirFor(goos string) string {
	if goos == "darwin" {
		return SupportDir() + "/Data"
	}
	return filepath.Join(os.Getenv("ProgramData"), Name)
}

// ConfigPathFor is ConfigPath on goos.
func ConfigPathFor(goos string) string {
	if goos == "darwin" {
		return SupportDir() + "/config.yaml"
	}
	return filepath.Join(DataDirFor(goos), "config.yaml")
}

// SocketDirFor is SocketDir on goos.
func SocketDirFor(goos string) string {
	if goos == "linux" {
		return "/run/" + Slug
	}
	return "/var/run/" + Slug
}
