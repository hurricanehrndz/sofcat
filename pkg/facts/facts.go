// Package facts gathers what installable_condition expressions are evaluated
// against: built-in facts about this machine and the ones admin scripts in the
// conditions directory print. See docs/conditions.md. It is separate from
// pkg/conditions so makecatalogs can check expressions without the Windows
// fact code.
package facts

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/version"
)

// scriptTimeout bounds each admin condition script.
const scriptTimeout = 60 * time.Second

// Dir is the admin conditions directory under the app data path. It inherits
// the app data ACL, so only SYSTEM and Administrators can add scripts to it.
func Dir(appDataPath string) string {
	return filepath.Join(appDataPath, "conditions")
}

var (
	mu         sync.Mutex
	adminCache conditions.Facts
	haveAdmin  bool

	// Test seams.
	runScript    = runConditionScript
	builtinFacts = collectBuiltinFacts
	commandPs1   = filepath.Join(os.Getenv("WINDIR"), "system32", "WindowsPowershell", "v1.0", "powershell.exe")
)

// Collect returns this machine's facts. Built-in facts are gathered every
// call and override admin facts of the same name, so a script cannot spoof
// them. refresh runs the admin scripts in dir again; otherwise the admin facts
// from the last refresh are reused, so a self-service list call does not run
// scripts. The first call runs them either way.
func Collect(dir string, refresh bool) conditions.Facts {
	builtins := builtinFacts()

	mu.Lock()
	if refresh || !haveAdmin {
		adminCache = adminFacts(dir)
		haveAdmin = true
		for name := range adminCache {
			if _, ok := builtins[name]; ok {
				slog.Warn("condition script tried to set a built-in fact, ignoring it", "fact", name)
				delete(adminCache, name)
			}
		}
	}
	facts := maps.Clone(adminCache)
	mu.Unlock()

	if facts == nil {
		facts = conditions.Facts{}
	}
	maps.Copy(facts, builtins)
	return facts
}

// adminFacts runs every script in dir, in name order, and merges the JSON
// object each prints on stdout; a later script wins a shared name. A missing
// directory means no admin facts. A script that fails, times out, or prints
// anything but a JSON object is logged and contributes nothing.
func adminFacts(dir string) conditions.Facts {
	facts := conditions.Facts{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("unable to read the conditions directory", "path", dir, "err", err)
		}
		return facts
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		out, err := runScript(path)
		if err != nil {
			slog.Warn("condition script failed", "path", path, "err", err)
			continue
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			slog.Warn("condition script did not print a JSON object", "path", path, "err", err)
			continue
		}
		slog.Debug("condition script facts", "path", path, "facts", got)
		maps.Copy(facts, got)
	}
	return facts
}

// runConditionScript runs a .ps1 with Windows PowerShell or an .exe directly
// and returns its stdout. Other files are skipped with an error.
func runConditionScript(path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ps1":
		cmd = exec.CommandContext(ctx, commandPs1, "-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
	case ".exe":
		cmd = exec.CommandContext(ctx, path)
	default:
		return nil, fmt.Errorf("unsupported script type %q: use .ps1 or .exe", filepath.Ext(path))
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("timed out after %s", scriptTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// collectBuiltinFacts gathers the facts SofCat provides itself. The portable
// ones are set here; platformFacts adds the Windows ones.
func collectBuiltinFacts() conditions.Facts {
	facts := conditions.Facts{
		"arch":           goArch(runtime.GOARCH),
		"engine_version": version.Version().Version,
		"ipv4_address":   ipv4Addresses(),
	}
	if hostname, err := os.Hostname(); err == nil {
		facts["hostname"] = hostname
	}
	platformFacts(facts)
	return facts
}

// goArch names an architecture the way the arch fact does: amd64, arm64 or x86.
func goArch(arch string) string {
	if arch == "386" {
		return "x86"
	}
	return arch
}

func ipv4Addresses() []string {
	addrs := []string{}
	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		slog.Warn("unable to list IP addresses", "err", err)
		return addrs
	}
	for _, a := range ifaceAddrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil {
			addrs = append(addrs, ip4.String())
		}
	}
	slices.Sort(addrs)
	return addrs
}
