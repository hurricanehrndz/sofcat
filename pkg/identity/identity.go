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

// DataDir is the default app data directory: %ProgramData%\<Name>\Data on
// Windows, /Library/Application Support/<Name>/Data on macOS.
func DataDir() string { return DataDirFor(runtime.GOOS) }

// ConfigPath is the default config.yaml path, beside DataDir on both:
// %ProgramData%\<Name>\config.yaml on Windows, in SupportDir on macOS.
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

// UserAgentPath is the installed macOS per-user helper, beside the CLI.
func UserAgentPath() string { return BinDir() + "/" + Slug + "-useragent" }

// AppPath is the installed macOS app bundle.
func AppPath() string { return "/Applications/" + Name + ".app" }

// LaunchdLabel is the macOS daemon's launchd label.
func LaunchdLabel() string { return Identifier }

// LaunchdPlistPath is the macOS daemon's property list.
func LaunchdPlistPath() string { return "/Library/LaunchDaemons/" + Identifier + ".plist" }

// UserAgentLabel is the launchd label of the macOS per-user helper's
// LaunchAgent.
func UserAgentLabel() string { return Identifier + ".useragent" }

// UserAgentPlistPath is the per-user helper's LaunchAgent property list.
func UserAgentPlistPath() string { return "/Library/LaunchAgents/" + UserAgentLabel() + ".plist" }

// ProgramDataDir is the Windows twin of SupportDir, %ProgramData%\<Name>:
// the config, Data and sofcat's own placements live in it. The executables
// stay under Program Files, which sofcat never writes.
func ProgramDataDir() string { return filepath.Join(os.Getenv("ProgramData"), Name) }

// UserArtifactsDirFor is where the service stages the archives per-user
// helpers read, for the data directory dataDir on goos: user-artifacts
// inside it, whose own ACL lets Users in on Windows. On macOS a <Name>/Data
// directory is root-only 0700 and Unix has no inherited ACL to open one
// entry of it, so the archives sit beside it, in the 0755 <Name> directory
// that already holds the CLI. Any other macOS data directory is not made
// 0700, so its archives stay inside it.
func UserArtifactsDirFor(goos, dataDir string) string {
	if goos == "darwin" && filepath.Base(dataDir) == "Data" && filepath.Base(filepath.Dir(dataDir)) == Name {
		return filepath.Join(filepath.Dir(dataDir), "user-artifacts")
	}
	return filepath.Join(dataDir, "user-artifacts")
}

// DataDirFor is DataDir on goos.
func DataDirFor(goos string) string {
	if goos == "darwin" {
		return SupportDir() + "/Data"
	}
	return filepath.Join(ProgramDataDir(), "Data")
}

// ConfigPathFor is ConfigPath on goos.
func ConfigPathFor(goos string) string {
	if goos == "darwin" {
		return SupportDir() + "/config.yaml"
	}
	return filepath.Join(ProgramDataDir(), "config.yaml")
}

// SocketDirFor is SocketDir on goos.
func SocketDirFor(goos string) string {
	if goos == "linux" {
		return "/run/" + Slug
	}
	return "/var/run/" + Slug
}
