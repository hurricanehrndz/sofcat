package facts

import (
	"log/slog"
	"regexp"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
)

// commandRunner runs a program and returns its stdout.
type commandRunner func(name string, args ...string) ([]byte, error)

var (
	ioregSerial  = regexp.MustCompile(`"IOPlatformSerialNumber"\s*=\s*"([^"]*)"`)
	ioregBattery = regexp.MustCompile(`"BatteryInstalled"\s*=\s*Yes`)
)

// macFacts adds the macOS facts, with the Windows facts' names and types,
// from the stock tools run returns the output of: os_version from sw_vers,
// machine_model from sysctl hw.model, serial_number from ioreg, and
// machine_type from the model and the battery. machine_manufacturer is always
// Apple. A source that fails or prints nothing leaves its facts missing, so
// conditions that use them are unknown. There is no os_build: a macOS build
// string is not a Windows build number. It is portable so the parsing is
// tested on every host.
func macFacts(facts conditions.Facts, run commandRunner) {
	facts["machine_manufacturer"] = "Apple"
	if v := macOutput(run, "/usr/bin/sw_vers", "-productVersion"); v != "" {
		facts["os_version"] = v
	}
	model := macOutput(run, "/usr/sbin/sysctl", "-n", "hw.model")
	if model != "" {
		facts["machine_model"] = model
	}
	if out := macOutput(run, "/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice"); out != "" {
		if m := ioregSerial.FindStringSubmatch(out); m != nil && m[1] != "" {
			facts["serial_number"] = m[1]
		}
	}
	if t, ok := macMachineType(model, run); ok {
		facts["machine_type"] = t
	}
}

// macMachineType is laptop for a MacBook. An Intel MacBook's model says so
// (MacBookPro16,1); an Apple silicon model does not (a MacBook Air is
// Mac16,12), so a Mac with an internal battery is a laptop too. It is unknown
// when neither the model nor the battery query answers.
func macMachineType(model string, run commandRunner) (string, bool) {
	if strings.Contains(model, "Book") {
		return "laptop", true
	}
	out, err := run("/usr/sbin/ioreg", "-rd1", "-c", "AppleSmartBattery")
	if err != nil {
		slog.Warn("unable to read a macOS fact source", "command", "ioreg -c AppleSmartBattery", "err", err)
		return "", false
	}
	if ioregBattery.Match(out) {
		return "laptop", true
	}
	return "desktop", true
}

func macOutput(run commandRunner, name string, args ...string) string {
	out, err := run(name, args...)
	if err != nil {
		slog.Warn("unable to read a macOS fact source", "command", name, "err", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}
