package manifest

import (
	"reflect"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	yaml "go.yaml.in/yaml/v4"
)

const conditionalManifest = `
name: site
managed_installs: [Base]
conditional_items:
  - condition: os == "windows"
    managed_installs: [WinTool]
    managed_uninstalls: [OldWinTool]
    conditional_items:
      - condition: arch == "arm64"
        managed_installs: [ArmTool]
      - condition: arch == "amd64"
        optional_installs: [X64Optional]
  - condition: os == "darwin"
    managed_installs: [MacTool]
    conditional_items:
      - condition: arch == "amd64"
        managed_installs: [IntelMacTool]
  - condition: '"lab" in machine_groups'
    managed_installs: [LabTool]
    optional_installs: [LabOptional]
    default_installs: [LabDefault]
    managed_uninstalls: [LabRemoved]
    managed_updates: [LabUpdate]
    conditional_items:
      - condition: os == "windows"
        managed_installs: [LabWinTool]
  - condition: arch ==
    managed_installs: [Broken]
`

func TestExpand(t *testing.T) {
	var m Item
	if err := yaml.Unmarshal([]byte(conditionalManifest), &m); err != nil {
		t.Fatal(err)
	}
	effective, held := Expand([]Item{m}, conditions.Facts{"os": "windows", "arch": "amd64"})

	// Inner true under outer true contributes; anything under outer false
	// contributes nothing; a syntax error contributes nothing; an unknown
	// block's items are in no effective list, removals included.
	want := Item{
		Name:             "site",
		Installs:         []string{"Base", "WinTool"},
		OptionalInstalls: []string{"X64Optional"},
		Uninstalls:       []string{"OldWinTool"},
	}
	if len(effective) != 1 || !reflect.DeepEqual(effective[0], want) {
		t.Errorf("effective = %+v\nwant        %+v", effective, want)
	}
	// The unknown block holds every name under it, nested blocks included.
	wantHeld := []Held{{
		Condition: `"lab" in machine_groups`,
		Missing:   []string{"machine_groups"},
		Items:     []string{"LabTool", "LabOptional", "LabDefault", "LabRemoved", "LabUpdate", "LabWinTool"},
	}}
	if !reflect.DeepEqual(held, wantHeld) {
		t.Errorf("held = %+v\nwant   %+v", held, wantHeld)
	}
	// The caller's manifests are left alone.
	if len(m.Installs) != 1 || len(m.ConditionalItems) != 4 {
		t.Errorf("Expand changed its input: %+v", m)
	}
}
