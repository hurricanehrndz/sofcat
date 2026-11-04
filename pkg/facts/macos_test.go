package facts

import (
	"errors"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
)

const (
	swVers    = "/usr/bin/sw_vers -productVersion"
	hwModel   = "/usr/sbin/sysctl -n hw.model"
	platform  = "/usr/sbin/ioreg -rd1 -c IOPlatformExpertDevice"
	batteries = "/usr/sbin/ioreg -rd1 -c AppleSmartBattery"
)

// Canned output of the stock macOS tools on an Apple silicon MacBook Air,
// trimmed.
func macBookAir() map[string]string {
	return map[string]string{
		swVers:  "15.1\n",
		hwModel: "Mac16,12\n",
		platform: `+-o J713AP  <class IOPlatformExpertDevice, id 0x100000276, registered, matched, active, busy 0 (15656 ms), retain 38>
    {
      "manufacturer" = <"Apple Inc.">
      "IOPlatformSerialNumber" = "CV44JQPY7F"
      "model" = <"Mac16,12">
    }
`,
		batteries: `+-o AppleSmartBattery  <class AppleSmartBattery, id 0x10000096c, registered, matched, active, busy 0 (5 ms), retain 9>
    {
      "BatteryInstalled" = Yes
    }
`,
	}
}

// cannedRun answers from outputs; a command missing from it fails.
func cannedRun(outputs map[string]string) commandRunner {
	return func(name string, args ...string) ([]byte, error) {
		out, ok := outputs[strings.Join(append([]string{name}, args...), " ")]
		if !ok {
			return nil, errors.New("exit status 1")
		}
		return []byte(out), nil
	}
}

// Conditions written for Windows (machine_type == "laptop", version(os_version)
// >= ...) must read the same names and types on a Mac.
func TestMacFacts(t *testing.T) {
	facts := conditions.Facts{}
	macFacts(facts, cannedRun(macBookAir()))
	want := conditions.Facts{
		"os_version":           "15.1",
		"machine_model":        "Mac16,12",
		"machine_type":         "laptop",
		"machine_manufacturer": "Apple",
		"serial_number":        "CV44JQPY7F",
	}
	for k, v := range want {
		if facts[k] != v {
			t.Errorf("%s = %#v, want %#v", k, facts[k], v)
		}
	}
	if _, ok := facts["os_build"]; ok {
		t.Error("os_build is set; a macOS build string is not a Windows build number")
	}
}

// A source that fails leaves its facts missing, so conditions on them are
// unknown rather than false or wrong.
func TestMacFactsFailedSource(t *testing.T) {
	outputs := macBookAir()
	delete(outputs, hwModel)
	delete(outputs, platform)
	delete(outputs, batteries)
	facts := conditions.Facts{}
	macFacts(facts, cannedRun(outputs))
	for _, k := range []string{"machine_model", "machine_type", "serial_number"} {
		if _, ok := facts[k]; ok {
			t.Errorf("%s = %v after its source failed", k, facts[k])
		}
	}
	if facts["os_version"] != "15.1" {
		t.Errorf("os_version = %v; the other sources still count", facts["os_version"])
	}
}

func TestMacMachineType(t *testing.T) {
	noBattery := cannedRun(map[string]string{batteries: ""})
	for _, tc := range []struct {
		model string
		run   commandRunner
		want  string
	}{
		{"MacBookPro16,1", noBattery, "laptop"}, // Intel: the model says so
		{"Mac16,12", cannedRun(macBookAir()), "laptop"},
		{"Mac16,10", noBattery, "desktop"}, // Apple silicon Mac mini
		{"iMac21,1", noBattery, "desktop"},
	} {
		if got, ok := macMachineType(tc.model, tc.run); !ok || got != tc.want {
			t.Errorf("macMachineType(%q) = %q, %v; want %q", tc.model, got, ok, tc.want)
		}
	}
}
