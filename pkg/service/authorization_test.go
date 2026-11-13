package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/process"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"github.com/hurricanehrndz/sofcat/pkg/status"
)

// These tests drive the service as different local users (R32, R32a). Each
// request goes through handleConn on a connection whose Peer is whoever the
// test says, as the transport would resolve it.

var (
	userA      = peer{Name: `HOST\alice`, ID: "S-1-5-21-1-2-3-1001"}
	userB      = peer{Name: `HOST\bob`, ID: "S-1-5-21-1-2-3-1002"}
	unresolved = peer{}
	// nameless is a user whose SID resolved but whose account lookup failed.
	nameless = peer{ID: "S-1-5-21-x"}
)

// callerConn is one request from caller; unresolved fails Peer as a
// transport does when it cannot read the client's identity, and a caller
// with an ID but no name returns that ID with the failed name lookup's error.
type callerConn struct {
	io.Reader
	out    bytes.Buffer
	caller peer
}

func (c *callerConn) Write(p []byte) (int, error) { return c.out.Write(p) }
func (c *callerConn) Close() error                { return nil }
func (c *callerConn) Abort()                      {}
func (c *callerConn) Peer() (peer, error) {
	if c.caller == unresolved {
		return peer{}, errors.New("identity not available")
	}
	if c.caller.Name == "" {
		return c.caller, errors.New("look up account: none mapped")
	}
	return c.caller, nil
}

// callAs sends method with params as caller and returns the response.
func callAs(t *testing.T, sr *serviceRunner, caller peer, method string, params any) rpcResponse {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf(`{"jsonrpc":"2.0","id":"t","method":%q,"params":%s}`, method, raw)
	conn := &callerConn{Reader: strings.NewReader(request + "\n"), caller: caller}
	sr.handleConn(t.Context(), conn)
	var resp rpcResponse
	if err := json.NewDecoder(&conn.out).Decode(&resp); err != nil {
		t.Fatalf("%s as %q: response is not JSON: %v", method, caller.Name, err)
	}
	return resp
}

// mutateAs sends installItem or removeItem for Skill as caller. It returns
// the operation ID, or the error's data.code.
func mutateAs(t *testing.T, sr *serviceRunner, caller peer, method string) (operationID, code string) {
	t.Helper()
	resp := callAs(t, sr, caller, method, itemParams{ItemName: "Skill"})
	if resp.Error != nil {
		return "", resp.Error.Data.Code
	}
	var accepted AcceptedOperation
	if err := json.Unmarshal(resp.Result, &accepted); err != nil {
		t.Fatal(err)
	}
	return accepted.OperationID, ""
}

func cancelAs(t *testing.T, sr *serviceRunner, caller peer, operationID string) string {
	t.Helper()
	resp := callAs(t, sr, caller, methodCancelOperation, operationParams{OperationID: operationID})
	if resp.Error != nil {
		if resp.Error.Data.Code == "not_authorized" && resp.Error.Code != codeNotAuthorized {
			t.Fatalf("not_authorized sent with code %d", resp.Error.Code)
		}
		return resp.Error.Data.Code
	}
	return ""
}

// completeRemovals is the end of a managed run that removed every pending
// item: the prune drops each confirmed-gone managed_uninstalls entry, as
// cmd/sofcat's managedRun does. Skill is a zip item with no receipts, so it
// reads as gone.
func completeRemovals(t *testing.T, cfg config.Configuration) {
	t.Helper()
	catalogs := map[int]map[string]catalog.Item{1: {"Skill": {Installer: catalog.InstallerItem{
		Type:     "zip",
		Variants: map[string]map[string]catalog.Variant{"skills": {"windows": {Location: "skill.zip", Hash: "abc", TreeHash: "sha256:def"}}},
	}}}}
	entry, err := loadServiceLocalManifest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	gone := process.GoneUninstalls(entry.Uninstalls, catalogs, &status.Checker{}, t.TempDir())
	err = manifest.UpdateSelfServe(serviceLocalManifestPath(cfg), func(entry *manifest.Item) bool {
		return process.PruneSelfServeUninstalls(entry, gone)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOnlyTheSelectorOrAnAdminUndoesASelection(t *testing.T) {
	cfg := testServiceConfig(t)
	stubOptional(t, "Skill")
	sr, _ := startTestRunner(t, cfg, noopRun)
	admin := peer{Name: `HOST\admin`, ID: "S-1-5-21-1-2-3-500", Admin: true}

	expect := func(caller peer, method, wantCode string) {
		t.Helper()
		if _, code := mutateAs(t, sr, caller, method); code != wantCode {
			t.Fatalf("%s as %q = %q, want %q", method, caller.Name, code, wantCode)
		}
	}
	expectFile := func(installs, uninstalls []string, selector string) {
		t.Helper()
		got := loadManifest(t, cfg)
		if !slices.Equal(got.Installs, installs) || !slices.Equal(got.Uninstalls, uninstalls) || got.SelectedBy["Skill"] != selector {
			t.Fatalf("self-serve file installs=%v uninstalls=%v selected_by=%v, want %v/%v/%q",
				got.Installs, got.Uninstalls, got.SelectedBy, installs, uninstalls, selector)
		}
	}
	selected, pending := []string{"Skill"}, []string{"Skill"}

	// A selects first and is recorded; B selecting too leaves A recorded.
	expect(userA, methodInstallItem, "")
	expectFile(selected, nil, userA.ID)
	expect(userB, methodInstallItem, "")
	expectFile(selected, nil, userA.ID)

	// Nobody but A or an admin removes it; a refusal changes nothing.
	expect(userB, methodRemoveItem, "not_authorized")
	expect(unresolved, methodRemoveItem, "not_authorized")
	expectFile(selected, nil, userA.ID)

	// While A's removal is pending, an install undoes it, so it is checked too.
	expect(userA, methodRemoveItem, "")
	expectFile(nil, pending, userA.ID)
	expect(userB, methodInstallItem, "not_authorized")
	expect(unresolved, methodInstallItem, "not_authorized")
	expectFile(nil, pending, userA.ID)
	expect(admin, methodInstallItem, "")
	expectFile(selected, nil, userA.ID)

	// An admin removes it and so owns the removal (R32a): A cannot take it
	// back, the admin can, and the admin is then the selector.
	expect(admin, methodRemoveItem, "")
	expectFile(nil, pending, admin.ID)
	expect(userA, methodInstallItem, "not_authorized")
	expectFile(nil, pending, admin.ID)
	expect(admin, methodInstallItem, "")
	expectFile(selected, nil, admin.ID)

	// Once the removal completes the selector is gone: B selects next and
	// owns it.
	expect(admin, methodRemoveItem, "")
	completeRemovals(t, cfg)
	expectFile(nil, nil, "")
	expect(userB, methodInstallItem, "")
	expectFile(selected, nil, userB.ID)
	expect(userA, methodRemoveItem, "not_authorized")
	expect(userB, methodRemoveItem, "")
	expectFile(nil, pending, userB.ID)
}

// An item with no recorded selector, such as a default_installs item or one
// selected before selectors were recorded, stays removable by anyone, but the
// pending removal is then the remover's to take back (R32a).
func TestAnItemWithNoSelectorIsRemovableByAnyone(t *testing.T) {
	cfg := testServiceConfig(t)
	stubOptional(t, "Skill")
	sr, _ := startTestRunner(t, cfg, noopRun)
	userC := peer{Name: `HOST\carol`, ID: "S-1-5-21-1-2-3-1003"}
	if err := manifest.SaveSelfServe(serviceLocalManifestPath(cfg), manifest.Item{Installs: []string{"Skill"}}); err != nil {
		t.Fatal(err)
	}
	if _, code := mutateAs(t, sr, userB, methodRemoveItem); code != "" {
		t.Fatalf("removeItem as B = %q, want accepted", code)
	}
	if got := loadManifest(t, cfg); !slices.Equal(got.Uninstalls, []string{"Skill"}) || got.SelectedBy["Skill"] != userB.ID {
		t.Fatalf("after B's removal uninstalls=%v selected_by=%v, want B recorded", got.Uninstalls, got.SelectedBy)
	}
	for _, other := range []peer{userC, unresolved} {
		if _, code := mutateAs(t, sr, other, methodInstallItem); code != "not_authorized" {
			t.Fatalf("installItem as %q = %q, want not_authorized", other.Name, code)
		}
	}
	if _, code := mutateAs(t, sr, userB, methodInstallItem); code != "" {
		t.Fatalf("installItem as B = %q, want accepted", code)
	}
	if got := loadManifest(t, cfg); !slices.Equal(got.Installs, []string{"Skill"}) || got.SelectedBy["Skill"] != userB.ID {
		t.Fatalf("after B took it back installs=%v selected_by=%v, want B recorded", got.Installs, got.SelectedBy)
	}
}

// Canceling a removal puts back the selection it replaced, selector included:
// the remover's ownership lasts only as long as the removal.
func TestCancelingARemovalRestoresTheSelector(t *testing.T) {
	admin := peer{Name: `HOST\admin`, ID: "S-1-5-21-1-2-3-500", Admin: true}
	for _, tc := range []struct {
		name     string
		selector string // the item's selector before the removal
		remover  peer
	}{
		{"an admin removes A's item", userA.ID, admin},
		{"B removes an unowned item", "", userB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testServiceConfig(t)
			stubOptional(t, "Skill")
			var runs atomic.Int32
			release := make(chan struct{})
			// Every run after start-up waits, so the removal stays cancelable.
			sr, _ := startTestRunner(t, cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
				if runs.Add(1) > 1 {
					<-release
				}
				return nil, nil
			})
			t.Cleanup(func() { close(release) })
			before := manifest.Item{Installs: []string{"Skill"}}
			if tc.selector != "" {
				before.SelectedBy = map[string]string{"Skill": tc.selector}
			}
			if err := manifest.SaveSelfServe(serviceLocalManifestPath(cfg), before); err != nil {
				t.Fatal(err)
			}
			operationID, code := mutateAs(t, sr, tc.remover, methodRemoveItem)
			if code != "" {
				t.Fatalf("removeItem as %q = %q, want accepted", tc.remover.Name, code)
			}
			if got := loadManifest(t, cfg).SelectedBy["Skill"]; got != tc.remover.ID {
				t.Fatalf("selected_by during the removal = %q, want the remover", got)
			}
			if code := cancelAs(t, sr, tc.remover, operationID); code != "" {
				t.Fatalf("cancel as %q = %q, want accepted", tc.remover.Name, code)
			}
			got := loadManifest(t, cfg)
			if !slices.Equal(got.Installs, []string{"Skill"}) || len(got.Uninstalls) != 0 || got.SelectedBy["Skill"] != tc.selector {
				t.Fatalf("after the cancel installs=%v uninstalls=%v selected_by=%v, want selector %q",
					got.Installs, got.Uninstalls, got.SelectedBy, tc.selector)
			}
		})
	}
}

// A caller whose SID resolved but whose account name did not is still that
// user for authorization; only the audit name is missing.
func TestACallerWithAnIDButNoNameIsResolved(t *testing.T) {
	cfg := testServiceConfig(t)
	stubOptional(t, "Skill")
	sr, _ := startTestRunner(t, cfg, noopRun)

	resp := callAs(t, sr, nameless, methodInstallItem, itemParams{ItemName: "Skill"})
	if resp.Error != nil {
		t.Fatalf("installItem without a name = %+v, want accepted", resp.Error)
	}
	var accepted AcceptedOperation
	if err := json.Unmarshal(resp.Result, &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.RequestedBy != "" {
		t.Fatalf("requestedBy = %q, want empty", accepted.RequestedBy)
	}
	if got := loadManifest(t, cfg).SelectedBy["Skill"]; got != nameless.ID {
		t.Fatalf("selected_by = %q, want %q", got, nameless.ID)
	}
	if _, code := mutateAs(t, sr, nameless, methodRemoveItem); code != "" {
		t.Fatalf("removing their own item = %q, want accepted", code)
	}

	completeRemovals(t, cfg)
	if _, code := mutateAs(t, sr, userA, methodInstallItem); code != "" {
		t.Fatalf("installItem as A = %q", code)
	}
	if _, code := mutateAs(t, sr, nameless, methodRemoveItem); code != "not_authorized" {
		t.Fatalf("removing A's item = %q, want not_authorized", code)
	}
}

func TestOnlyTheRequesterOrAnAdminCancels(t *testing.T) {
	cfg := testServiceConfig(t)
	stubOptional(t, "Skill")
	var runs atomic.Int32
	release := make(chan struct{})
	// The start-up run returns at once; every later one waits, before its
	// item, until the test ends, so each operation stays cancelable.
	sr, _ := startTestRunner(t, cfg, func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		if runs.Add(1) > 1 {
			<-release
		}
		return nil, nil
	})
	t.Cleanup(func() { close(release) })
	admin := peer{Name: `HOST\admin`, ID: "S-1-5-21-1-2-3-500", Admin: true}

	for _, canceler := range []peer{userA, admin} {
		operationID, code := mutateAs(t, sr, userA, methodInstallItem)
		if code != "" {
			t.Fatalf("installItem as A = %q", code)
		}
		for _, other := range []peer{userB, unresolved} {
			if code := cancelAs(t, sr, other, operationID); code != "not_authorized" {
				t.Fatalf("cancel of A's operation as %q = %q, want not_authorized", other.Name, code)
			}
		}
		if got := loadManifest(t, cfg).Installs; !slices.Equal(got, []string{"Skill"}) {
			t.Fatalf("a refused cancel changed the selection: %v", got)
		}
		if code := cancelAs(t, sr, canceler, operationID); code != "" {
			t.Fatalf("cancel as %q = %q, want accepted", canceler.Name, code)
		}
		if got := loadManifest(t, cfg); len(got.Installs) != 0 || len(got.SelectedBy) != 0 {
			t.Fatalf("the cancel left installs=%v selected_by=%v", got.Installs, got.SelectedBy)
		}
	}
}
