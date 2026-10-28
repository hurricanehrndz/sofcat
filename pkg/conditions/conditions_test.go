package conditions

import (
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
		// The forms docs/conditions.md shows.
		{`hostname matches "^LAB-[A-Z]+-[0-9]+$"`, true},
		{`hostname endsWith "-01" && hostname contains "GPU"`, true},
		{`arch in ["arm64", "amd64"]`, true},
		{`now() > date("2020-01-01")`, true},
		{`!(os_build < 22000) || false`, true},
	}
	for _, tt := range tests {
		got, err := Eval(tt.cond, facts)
		if err != nil {
			t.Errorf("Eval(%q) error: %v", tt.cond, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Eval(%q) = %v, want %v", tt.cond, got, tt.want)
		}
	}
}

// A typo or a missing fact must never make a condition true, or an item meant
// for a few machines installs everywhere.
func TestEvalErrorsAreNotInstallable(t *testing.T) {
	facts := Facts{"hostname": "PC1", "engine_version": "dev"}
	for _, cond := range []string{
		`hostnmae != "PC2"`,              // unknown name
		`!("lab" in machine_groups)`,     // admin fact never set
		`hostname`,                       // not a boolean
		`version(engine_version) >= "1"`, // untagged build
		`hostname ==`,                    // syntax
	} {
		if ok, err := Eval(cond, facts); err == nil || ok {
			t.Errorf("Eval(%q) = %v, %v; want an error", cond, ok, err)
		}
	}
}

func TestApply(t *testing.T) {
	catalogs := map[int]map[string]catalog.Item{
		1: {
			"Plain":  {Name: "Plain"},
			"ArmApp": {Name: "ArmApp", InstallableCondition: `arch == "arm64"`},
			"X64App": {Name: "X64App", InstallableCondition: `arch == "amd64"`},
			"Broken": {Name: "Broken", InstallableCondition: `nope == 1`},
		},
	}
	Apply(catalogs, Facts{"arch": "amd64"})
	for name, want := range map[string]string{"Plain": "", "X64App": "", "ArmApp": "is false", "Broken": "cannot be evaluated"} {
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
