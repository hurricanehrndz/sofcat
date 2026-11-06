package conditions

import (
	"slices"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

func TestEval(t *testing.T) {
	facts := Facts{
		"arch":           "amd64",
		"os_build":       22631,
		"os_version":     "10.0.22631.4460",
		"engine_version": "v1.10.0-3-gabc123",
		"ipv4_address":   []string{"10.1.2.3", "192.168.1.5"},
		"machine_groups": []any{"lab", "gpu"},
		"hostname":       "LAB-GPU-01",
	}
	tests := []struct {
		cond string
		want bool
	}{
		{"", true},
		{`arch == "amd64"`, true},
		{`arch == "arm64"`, false},
		{`os_build >= 22000`, true},
		{`"lab" in machine_groups`, true},
		{`hostname startsWith "LAB-" && "gpu" in machine_groups`, true},
		{`any(ipv4_address, # startsWith "10.")`, true},
		// Version comparison is numeric, not textual: "10.0.9" sorts after "10.0.22631" as text.
		{`version(os_version) >= version("10.0.9")`, true},
		{`version(os_version) >= "10.0.22000"`, true},
		{`version(os_version) < "10.0.22000"`, false},
		// A build past a tag compares as the tag, so it is not older than it.
		{`version(engine_version) >= "1.10"`, true},
		{`version(engine_version) == "1.10.0"`, true},
		{`version(engine_version) > "1.9.9"`, true},
		{`arch == "x86"`, false},
		// The forms site/src/content/docs/guides/conditions.md shows.
		{`hostname matches "^LAB-[A-Z]+-[0-9]+$"`, true},
		{`hostname endsWith "-01" && hostname contains "GPU"`, true},
		{`arch in ["arm64", "amd64"]`, true},
		{`now() > date("2020-01-01")`, true},
		{`!(os_build < 22000) || false`, true},
	}
	for _, tt := range tests {
		got, _, err := Evaluate(tt.cond, facts)
		if err != nil {
			t.Errorf("Evaluate(%q) error: %v", tt.cond, err)
			continue
		}
		if (got == True) != tt.want || got == Unknown {
			t.Errorf("Evaluate(%q) = %v, want %v", tt.cond, got, tt.want)
		}
	}
}

// A condition that names a fact this machine lacks is unknown, not false, so
// manifests can hold its items rather than remove them. A typo in a fact name
// looks the same and is unknown too.
func TestEvaluateMissingFactIsUnknown(t *testing.T) {
	facts := Facts{"hostname": "PC1"}
	for cond, want := range map[string][]string{
		`!("lab" in machine_groups)`:                      {"machine_groups"},
		`hostnmae != "PC2"`:                               {"hostnmae"},
		`hostname == "PC1" && version(os_version) > "10"`: {"os_version"},
		`zone == "a" || any(groups, # == hostname)`:       {"groups", "zone"},
	} {
		got, missing, err := Evaluate(cond, facts)
		if got != Unknown || err != nil || !slices.Equal(missing, want) {
			t.Errorf("Evaluate(%q) = %v, %q, %v; want unknown, %q", cond, got, missing, err, want)
		}
	}
}

// $env is expr's built-in environment map, not a fact, so indexing it by a
// present fact must decide the condition rather than report $env missing.
func TestEvaluateEnvIsNotAFact(t *testing.T) {
	got, missing, err := Evaluate(`$env["hostname"] == "PC1"`, Facts{"hostname": "PC1"})
	if got != True || len(missing) != 0 || err != nil {
		t.Errorf("Evaluate($env) = %v, %q, %v; want true, no missing", got, missing, err)
	}
}

// A syntax error, a runtime error, or a non-boolean result must never make a
// condition true, or an item meant for a few machines installs everywhere.
func TestEvaluateErrorsAreFalse(t *testing.T) {
	facts := Facts{"hostname": "PC1", "engine_version": "dev"}
	for _, cond := range []string{
		`hostname`,                       // not a boolean
		`version(engine_version) >= "1"`, // untagged build: runtime error
		`hostname ==`,                    // syntax
		`hostname = "PC1"`,               // syntax
		`nosuchfn(hostname)`,             // unknown function
	} {
		if got, _, err := Evaluate(cond, facts); err == nil || got != False {
			t.Errorf("Evaluate(%q) = %v, %v; want false with an error", cond, got, err)
		}
	}
}

func TestApply(t *testing.T) {
	catalogs := map[int]map[string]catalog.Item{
		1: {
			"Plain":  {Name: "Plain"},
			"ArmApp": {Name: "ArmApp", InstallableCondition: `arch == "arm64"`},
			"X64App": {Name: "X64App", InstallableCondition: `arch == "amd64"`},
			"Broken": {Name: "Broken", InstallableCondition: `arch ==`},
			"NoFact": {Name: "NoFact", InstallableCondition: `"lab" in machine_groups`},
		},
	}
	Apply(catalogs, Facts{"arch": "amd64"})
	for name, want := range map[string]string{"Plain": "", "X64App": "", "ArmApp": "is false", "Broken": "cannot be evaluated", "NoFact": "does not have: machine_groups"} {
		got := catalogs[1][name].NotInstallable
		if want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("%s NotInstallable = %q, want %q", name, got, want)
		}
	}
}

func TestCheck(t *testing.T) {
	for cond, ok := range map[string]bool{
		``:                                    true,
		`arch == "arm64" && "lab" in groups`:  true,
		`version(os_version) >= "10.0.22000"`: true,
		`arch == `:                            false,
		`arch = "arm64"`:                      false,
	} {
		if err := Check(cond); (err == nil) != ok {
			t.Errorf("Check(%q) = %v, want ok=%v", cond, err, ok)
		}
	}
}
