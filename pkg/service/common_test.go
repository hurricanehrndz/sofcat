package service

import (
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/branding"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

func TestParseCommandSpecInstallItem(t *testing.T) {
	cmd, err := parseCommandSpec("InstallItem:GoogleChrome")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cmd.Action != actionInstallItem {
		t.Fatalf("expected action %s, got %s", actionInstallItem, cmd.Action)
	}
	if len(cmd.Items) != 1 || cmd.Items[0] != "GoogleChrome" {
		t.Fatalf("unexpected items: %#v", cmd.Items)
	}
}

func TestParseCommandSpecRemoveItem(t *testing.T) {
	cmd, err := parseCommandSpec("RemoveItem:GoogleChrome")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cmd.Action != actionRemoveItem {
		t.Fatalf("expected action %s, got %s", actionRemoveItem, cmd.Action)
	}
	if len(cmd.Items) != 1 || cmd.Items[0] != "GoogleChrome" {
		t.Fatalf("unexpected items: %#v", cmd.Items)
	}
}

func TestParseCommandSpecListOptionalInstalls(t *testing.T) {
	cmd, err := parseCommandSpec("ListOptionalInstalls")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cmd.Action != actionListOptionalInstalls {
		t.Fatalf("expected action %s, got %s", actionListOptionalInstalls, cmd.Action)
	}
	if len(cmd.Items) != 0 {
		t.Fatalf("expected no items, got %#v", cmd.Items)
	}
}

func TestParseCommandSpecStreamOperationStatus(t *testing.T) {
	cmd, err := parseCommandSpec("StreamOperationStatus:op-123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cmd.Action != actionStreamOperationStatus {
		t.Fatalf("expected action %s, got %s", actionStreamOperationStatus, cmd.Action)
	}
	if len(cmd.Items) != 1 || cmd.Items[0] != "op-123" {
		t.Fatalf("unexpected items: %#v", cmd.Items)
	}
}

func TestParseCommandSpecInvalid(t *testing.T) {
	_, err := parseCommandSpec("InstallItem")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestParseCommandSpecLegacyActionInvalid(t *testing.T) {
	_, err := parseCommandSpec("install:foo")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestValidateCommandRunWithItems(t *testing.T) {
	err := validateCommand(Command{
		Action: actionRun,
		Items:  []string{"foo"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestValidateCommandInstallItemRequiresOneArgument(t *testing.T) {
	err := validateCommand(Command{
		Action: actionInstallItem,
		Items:  []string{"foo", "bar"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestExecuteCommandRunPassesProgressCallback(t *testing.T) {
	called := false
	progress := func(catalog.Item, []string, string, int, string) { called = true }
	_, err := executeCommand(config.Configuration{}, Command{Action: actionRun, progress: progress}, func(_ config.Configuration, got installer.ProgressFn, _ *installer.Cancels) (*report.Report, error) {
		got(catalog.Item{}, nil, "installing", 50, "")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if !called {
		t.Fatal("managed run did not receive command progress callback")
	}
}

func TestServiceInstallArgs(t *testing.T) {
	configPath := `C:\ProgramData\SofCat\config.yaml`
	got := serviceInstallArgs(configPath)
	if len(got) != 3 {
		t.Fatalf("expected 3 args, got %d: %#v", len(got), got)
	}
	if got[0] != "-c" {
		t.Fatalf("expected first arg -c, got %q", got[0])
	}
	if got[1] != configPath {
		t.Fatalf("expected config path %q, got %q", configPath, got[1])
	}
	if got[2] != "-service" {
		t.Fatalf("expected final arg -service, got %q", got[2])
	}
}

func TestParseCommandSpecGetBranding(t *testing.T) {
	cmd, err := parseCommandSpec("getbranding")
	if err != nil || cmd.Action != actionGetBranding || len(cmd.Items) != 0 {
		t.Fatalf("parse GetBranding = %#v, %v", cmd, err)
	}
	if _, err := parseCommandSpec("GetBranding:extra"); err == nil {
		t.Fatal("GetBranding accepted an argument")
	}
}

// GetBranding resolves the config block in the service (off Windows there is
// no policy store), and validation drops a bad field without failing the call.
func TestExecuteCommandGetBranding(t *testing.T) {
	cfg := config.Configuration{Branding: config.Branding{Title: " Acme ", Accent: "green", HelpURL: "javascript:alert(1)"}}
	resp, err := executeCommand(cfg, Command{Action: actionGetBranding}, nil)
	if err != nil || resp.Branding == nil {
		t.Fatalf("GetBranding = %#v, %v", resp, err)
	}
	if want := (branding.Branding{Title: "Acme"}); *resp.Branding != want {
		t.Fatalf("branding = %#v, want %#v", *resp.Branding, want)
	}
}

func TestBrandingSummaryReplacesLogoWithSize(t *testing.T) {
	line, err := brandingSummary(branding.Branding{Title: "Acme", LogoMime: "image/png", LogoBase64: "AAECAw==", ListHeading: "Skills"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(line, "logoBase64") || !strings.Contains(line, `"logoBytes":4`) || !strings.Contains(line, `"title":"Acme"`) || !strings.Contains(line, `"listHeading":"Skills"`) {
		t.Fatalf("summary %s", line)
	}
}

func TestParseCommandSpecCancelOperation(t *testing.T) {
	cmd, err := parseCommandSpec("canceloperation:12345")
	if err != nil || cmd.Action != actionCancelOperation || len(cmd.Items) != 1 || cmd.Items[0] != "12345" {
		t.Fatalf("parseCommandSpec = %#v, %v", cmd, err)
	}
	if _, err := parseCommandSpec("CancelOperation"); err == nil {
		t.Fatal("CancelOperation without an operationId must be rejected")
	}
}
