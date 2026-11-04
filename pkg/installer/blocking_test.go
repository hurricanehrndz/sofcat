package installer

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// origBlockingApps restores the blocking-app snapshot seam after each gate test.
var origBlockingApps = runningBlockingApps

// TestBlockingGateDefers verifies a running blocking app defers the install:
// ErrBlockingApps returned, a DeferredItem recorded (by catalog key Name), and
// nothing in FailedItems or InstalledItems (spec R6/R12).
func TestBlockingGateDefers(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	installItemFunc = func(_ *Runner, item catalog.Item, itemURL string) (string, error) {
		t.Error("installer ran despite a running blocking app")
		return "", nil
	}
	runningBlockingApps = func(apps []string) ([]string, error) { return []string{"notepad.exe"}, nil }
	defer func() {
		statusCheckStatus = origCheckStatus
		installItemFunc = origInstallItemFunc
		runningBlockingApps = origBlockingApps
	}()

	item := msiItem
	item.Name = "DemoBlocked"
	item.DisplayName = statusActionNoError
	item.BlockingApps = []string{"notepad"}

	r := newTestRunner()
	_, err := r.Install(item, "install")
	if !errors.Is(err, ErrBlockingApps) {
		t.Fatalf("expected ErrBlockingApps, got %v", err)
	}
	if len(r.Report.DeferredItems) != 1 {
		t.Fatalf("expected 1 deferred item, got %#v", r.Report.DeferredItems)
	}
	if d := r.Report.DeferredItems[0]; d.Name != "DemoBlocked" || d.Action != "install" || !strings.Contains(d.Reason, "notepad") {
		t.Errorf("unexpected deferred item: %#v", d)
	}
	if len(r.Report.FailedItems) != 0 {
		t.Errorf("deferred item must not be in FailedItems: %#v", r.Report.FailedItems)
	}
	if len(r.Report.InstalledItems) != 0 {
		t.Errorf("deferred item must not be in InstalledItems: %#v", r.Report.InstalledItems)
	}
}

// TestBlockingGateOnlyWhenActionNeeded verifies an already-satisfied item is
// never deferred: the gate lives behind the actionNeeded check, so the blocking
// snapshot is never even taken (spec R6).
func TestBlockingGateOnlyWhenActionNeeded(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	runningBlockingApps = func(apps []string) ([]string, error) {
		t.Error("blocking snapshot taken for an already-satisfied item")
		return []string{"notepad.exe"}, nil
	}
	defer func() {
		statusCheckStatus = origCheckStatus
		runningBlockingApps = origBlockingApps
	}()

	item := msiItem
	item.DisplayName = statusNoActionNoError // no action needed
	item.BlockingApps = []string{"notepad"}

	r := newTestRunner()
	if _, err := r.Install(item, "install"); err != nil {
		t.Fatalf("Install returned an error: %v", err)
	}
	if len(r.Report.DeferredItems) != 0 {
		t.Errorf("already-satisfied item must not be deferred: %#v", r.Report.DeferredItems)
	}
}

// TestBlockingGateUninstall verifies the uninstall arm is gated too.
func TestBlockingGateUninstall(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	uninstallItemFunc = func(_ *Runner, item catalog.Item, itemURL string) (string, error) {
		t.Error("uninstaller ran despite a running blocking app")
		return "", nil
	}
	runningBlockingApps = func(apps []string) ([]string, error) { return []string{"notepad.exe"}, nil }
	defer func() {
		statusCheckStatus = origCheckStatus
		uninstallItemFunc = origUninstallItemFunc
		runningBlockingApps = origBlockingApps
	}()

	item := msiItem
	item.Name = "DemoBlocked"
	item.DisplayName = statusActionNoError
	item.BlockingApps = []string{"notepad"}

	r := newTestRunner()
	_, err := r.Install(item, "uninstall")
	if !errors.Is(err, ErrBlockingApps) {
		t.Fatalf("expected ErrBlockingApps, got %v", err)
	}
	if len(r.Report.DeferredItems) != 1 || r.Report.DeferredItems[0].Action != "uninstall" {
		t.Errorf("expected 1 uninstall deferral, got %#v", r.Report.DeferredItems)
	}
}

// TestBlockingGateSnapshotErrorProceeds verifies a snapshot error does not block
// the run: the action proceeds (Munki treats a failed listing as none running).
func TestBlockingGateSnapshotErrorProceeds(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	installItemFunc = fakeInstallItem
	runningBlockingApps = func(apps []string) ([]string, error) { return nil, fmt.Errorf("snapshot boom") }
	defer func() {
		statusCheckStatus = origCheckStatus
		installItemFunc = origInstallItemFunc
		runningBlockingApps = origBlockingApps
	}()

	item := msiItem
	item.DisplayName = statusActionNoError
	item.BlockingApps = []string{"notepad"}

	r := newTestRunner()
	if _, err := r.Install(item, "install"); err != nil {
		t.Fatalf("snapshot error should proceed, got %v", err)
	}
	if len(r.Report.DeferredItems) != 0 {
		t.Errorf("snapshot error must not defer: %#v", r.Report.DeferredItems)
	}
}
