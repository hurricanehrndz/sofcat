package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// adminUser is a caller the transport resolved as a local administrator.
var adminUser = peer{Name: `HOST\admin`, ID: "S-1-5-21-1-2-3-500", Admin: true}

// runAs sends a run request as caller and returns the response and, when the
// request was accepted, the runResult notification and the raw bytes sent.
func runAs(t *testing.T, ctx context.Context, sr *serviceRunner, caller peer) (rpcResponse, *RunResult, string) {
	t.Helper()
	conn := runRequest(caller)
	sr.handleConn(ctx, conn)
	return readRun(t, conn)
}

// runRequest is a connection that sends a run request as caller.
func runRequest(caller peer) *callerConn {
	return &callerConn{Reader: strings.NewReader(`{"jsonrpc":"2.0","id":"t","method":"run"}` + "\n"), caller: caller}
}

// readRun reads what the service sent on conn.
func readRun(t *testing.T, conn *callerConn) (rpcResponse, *RunResult, string) {
	t.Helper()
	caller := conn.caller
	raw := conn.out.String()
	dec := json.NewDecoder(&conn.out)
	var resp rpcResponse
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("run as %q: response is not JSON: %v", caller.Name, err)
	}
	if resp.Error != nil {
		return resp, nil, raw
	}
	var note rpcNotification
	if err := dec.Decode(&note); err != nil {
		t.Fatalf("run as %q: no runResult after the ack: %v", caller.Name, err)
	}
	if note.Method != notificationRunResult {
		t.Fatalf("notification method = %q, want %q", note.Method, notificationRunResult)
	}
	var result RunResult
	if err := json.Unmarshal(note.Params, &result); err != nil {
		t.Fatal(err)
	}
	return resp, &result, raw
}

// countingRun counts managed runs and returns rep and err from each.
func countingRun(runs *atomic.Int32, rep *report.Report, err error) managedRunFunc {
	return func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		runs.Add(1)
		return rep, err
	}
}

// waitFor polls cond until it holds or a few seconds pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// An administrator gets the ack at once and then the run's outcome: the items
// its report lists as failed or deferred, and a fatal error only as a code,
// since the error can name repository URLs and local paths.
func TestRunRequestReportsTheRunOutcome(t *testing.T) {
	failing := &report.Report{
		FailedItems:   []report.FailedItem{{Name: "Chrome", Error: "exit status 1603"}},
		DeferredItems: []report.DeferredItem{{Name: "Slack", Reason: "Slack is running"}},
	}
	for _, tt := range []struct {
		name        string
		rep         *report.Report
		err         error
		want        RunResult
		mustNotLeak string
	}{
		{name: "success", want: RunResult{Completed: true, FailedItems: []string{}, DeferredItems: []string{}}},
		{name: "failed item", rep: failing, want: RunResult{Completed: true, FailedItems: []string{"Chrome"}, DeferredItems: []string{"Slack"}}},
		{
			name:        "fatal error",
			err:         errors.New("unable to retrieve manifest: GET https://repo.example/secret-token: 500"),
			want:        RunResult{ErrorCode: "managed_run_failed", FailedItems: []string{}, DeferredItems: []string{}},
			mustNotLeak: "secret-token",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var runs atomic.Int32
			sr, ctx := startTestRunner(t, testServiceConfig(t), countingRun(&runs, tt.rep, tt.err))
			waitFor(t, "the startup run", func() bool { return runs.Load() == 1 })

			resp, result, raw := runAs(t, ctx, sr, adminUser)
			if resp.Error != nil {
				t.Fatalf("run refused: %+v", resp.Error)
			}
			var accepted AcceptedOperation
			if err := json.Unmarshal(resp.Result, &accepted); err != nil || !accepted.Accepted || accepted.RequestedBy != adminUser.Name || accepted.OperationID != "" {
				t.Fatalf("ack = %s, %v", resp.Result, err)
			}
			if result.Completed != tt.want.Completed || result.ErrorCode != tt.want.ErrorCode ||
				!slices.Equal(result.FailedItems, tt.want.FailedItems) || !slices.Equal(result.DeferredItems, tt.want.DeferredItems) {
				t.Fatalf("runResult = %+v, want %+v", *result, tt.want)
			}
			if tt.mustNotLeak != "" && strings.Contains(raw, tt.mustNotLeak) {
				t.Fatalf("the client was sent the run's error: %s", raw)
			}
			if got := runs.Load(); got != 2 {
				t.Fatalf("managed runs = %d, want the startup run and the requested one", got)
			}
		})
	}
}

// Any local user reaches the socket, so only an administrator may start a
// run; anyone else, an unresolved caller included, is refused before a run is
// queued.
func TestRunRequestIsForAdministratorsOnly(t *testing.T) {
	var runs atomic.Int32
	sr, ctx := startTestRunner(t, testServiceConfig(t), countingRun(&runs, nil, nil))
	waitFor(t, "the startup run", func() bool { return runs.Load() == 1 })

	for _, caller := range []peer{userA, nameless, unresolved} {
		resp, _, _ := runAs(t, ctx, sr, caller)
		if resp.Error == nil || resp.Error.Code != codeNotAuthorized || resp.Error.Data.Code != "not_authorized" {
			t.Errorf("run as %+v = %+v, want not_authorized", caller, resp)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Fatalf("managed runs = %d after refused requests, want only the startup run", got)
	}
}

// A requested run goes through the queue: it waits for the run under way and
// never overlaps it.
func TestRunRequestWaitsForTheRunUnderWay(t *testing.T) {
	var runs, active atomic.Int32
	var overlapped atomic.Bool
	release := make(chan struct{})
	var once sync.Once
	sr, ctx := startTestRunner(t, testServiceConfig(t), func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		if active.Add(1) > 1 {
			overlapped.Store(true)
		}
		defer active.Add(-1)
		if runs.Add(1) == 1 {
			<-release
		}
		return nil, nil
	})
	defer once.Do(func() { close(release) })
	waitFor(t, "the startup run", func() bool { return runs.Load() == 1 })

	conn := runRequest(adminUser)
	done := make(chan struct{})
	go func() {
		sr.handleConn(ctx, conn)
		close(done)
	}()
	waitFor(t, "the run request to wait", func() bool { return len(sr.runSem) == 1 })
	time.Sleep(50 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Fatalf("managed runs = %d while the startup run is under way, want 1", got)
	}
	once.Do(func() { close(release) })

	<-done
	_, result, _ := readRun(t, conn)
	if result == nil || !result.Completed {
		t.Fatalf("runResult = %+v, want completed", result)
	}
	if runs.Load() != 2 || overlapped.Load() {
		t.Fatalf("runs = %d, overlapped = %v; want 2 runs, one at a time", runs.Load(), overlapped.Load())
	}
	if len(sr.runSem) != 0 {
		t.Fatal("the run request kept its waiter slot")
	}
}

// Waiting run requests have their own cap, so they cannot take the
// storefront's handler slots.
func TestRunRequestBeyondTheWaiterCapIsBusy(t *testing.T) {
	sr, ctx := startTestRunner(t, testServiceConfig(t), noopRun)
	for i := 0; i < cap(sr.runSem); i++ {
		sr.runSem <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(sr.runSem); i++ {
			<-sr.runSem
		}
	}()

	resp, _, _ := runAs(t, ctx, sr, adminUser)
	if resp.Error == nil || resp.Error.Data.Code != "server_busy" {
		t.Fatalf("run with every waiter slot taken = %+v, want server_busy", resp)
	}
}

// hangUpConn takes the ack and then fails every write, as a connection whose
// client hung up while waiting.
type hangUpConn struct {
	callerConn
	writes int
}

func (c *hangUpConn) Write(p []byte) (int, error) {
	c.writes++
	if c.writes > 1 {
		return 0, errors.New("broken pipe")
	}
	return c.callerConn.Write(p)
}

// A client that hangs up after the ack does not stop its run or keep its
// waiter slot.
func TestRunRequestRunsAfterTheClientHangsUp(t *testing.T) {
	var runs atomic.Int32
	sr, ctx := startTestRunner(t, testServiceConfig(t), countingRun(&runs, nil, nil))
	waitFor(t, "the startup run", func() bool { return runs.Load() == 1 })

	conn := &hangUpConn{callerConn: callerConn{Reader: strings.NewReader(`{"jsonrpc":"2.0","id":"t","method":"run"}` + "\n"), caller: adminUser}}
	sr.handleConn(ctx, conn)
	if got := runs.Load(); got != 2 {
		t.Fatalf("managed runs = %d, want the requested run to have run", got)
	}
	if len(sr.runSem) != 0 {
		t.Fatal("the run request kept its waiter slot")
	}
	if !bytes.Contains(conn.out.Bytes(), []byte(`"accepted":true`)) {
		t.Fatalf("ack = %q", conn.out.String())
	}
}

// A run request still waiting when the service stops ends with canceled.
func TestRunRequestEndsWhenTheServiceStops(t *testing.T) {
	var runs atomic.Int32
	release := make(chan struct{})
	defer close(release)
	sr, _ := startTestRunner(t, testServiceConfig(t), func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error) {
		runs.Add(1)
		<-release
		return nil, nil
	})
	waitFor(t, "the startup run", func() bool { return runs.Load() == 1 })

	stopping, stop := context.WithCancel(t.Context())
	conn := runRequest(adminUser)
	done := make(chan struct{})
	go func() {
		sr.handleConn(stopping, conn)
		close(done)
	}()
	waitFor(t, "the run request to wait", func() bool { return len(sr.runSem) == 1 })
	stop()

	<-done
	_, result, _ := readRun(t, conn)
	if result == nil || result.Completed || result.ErrorCode != "canceled" {
		t.Fatalf("runResult = %+v, want canceled", result)
	}
}

func TestCapabilitiesIncludeRun(t *testing.T) {
	cfg := testServiceConfig(t)
	_, ctx := startTestRunner(t, cfg, noopRun)
	info, err := NewClient(cfg.ServicePipeName).GetServiceInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(info.Capabilities, methodRun) {
		t.Fatalf("capabilities %v lack %s", info.Capabilities, methodRun)
	}
}

// Over the real transport the test's own user is the caller: refused unless
// it is an administrator (root, or an elevated Windows token).
func TestClientRunOverTheTransport(t *testing.T) {
	me, err := connectedPeer(t)
	if err != nil {
		t.Fatalf("Peer: %v", err)
	}
	cfg := testServiceConfig(t)
	_, ctx := startTestRunner(t, cfg, noopRun)
	result, err := NewClient(cfg.ServicePipeName).Run(ctx)
	switch {
	case me.Admin && (err != nil || !result.Completed):
		t.Fatalf("Run as an administrator = %+v, %v", result, err)
	case !me.Admin && !IsErrorCode(err, "not_authorized"):
		t.Fatalf("Run as a standard user = %+v, %v; want not_authorized", result, err)
	}
}

func TestRunResponse(t *testing.T) {
	for _, tt := range []struct {
		name      string
		result    RunResult
		wantItems []string
		wantErr   string
	}{
		{name: "success", result: RunResult{Completed: true}},
		{name: "deferred only", result: RunResult{Completed: true, DeferredItems: []string{"Slack"}}, wantItems: []string{"deferred: Slack"}},
		{name: "failed item", result: RunResult{Completed: true, FailedItems: []string{"Chrome"}, DeferredItems: []string{"Slack"}}, wantItems: []string{"failed: Chrome", "deferred: Slack"}, wantErr: "1 failed item"},
		{name: "fatal", result: RunResult{ErrorCode: "managed_run_failed"}, wantErr: "see the SofCat service log"},
		{name: "canceled", result: RunResult{ErrorCode: "canceled"}, wantErr: "service stopped"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := runResponse(tt.result)
			if !slices.Equal(resp.Items, tt.wantItems) {
				t.Errorf("items = %q, want %q", resp.Items, tt.wantItems)
			}
			if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// Waiting callers' slots are no reason to tell a non-administrator anything
// but not_authorized: the admin check comes first.
func TestRunRequestRefusesANonAdminEvenWhenBusy(t *testing.T) {
	sr, ctx := startTestRunner(t, testServiceConfig(t), noopRun)
	for i := 0; i < cap(sr.runSem); i++ {
		sr.runSem <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(sr.runSem); i++ {
			<-sr.runSem
		}
	}()

	resp, _, _ := runAs(t, ctx, sr, userA)
	if resp.Error == nil || resp.Error.Data.Code != "not_authorized" {
		t.Fatalf("run as a standard user with every waiter slot taken = %+v, want not_authorized", resp)
	}
}

// A requested run reports progress to an open operation whose item it
// reaches, so a storefront install queued behind it shows its progress.
func TestRunRequestReportsProgressToOpenOperations(t *testing.T) {
	sr, ctx := startTestRunner(t, testServiceConfig(t), func(_ config.Configuration, progress installer.ProgressFn, _ *installer.Cancels) (*report.Report, error) {
		if progress != nil {
			progress(catalog.Item{Name: "Slack", DisplayName: "Slack"}, nil, "downloading", 40, "Slack")
		}
		return nil, nil
	})
	sr.registerTrackedOperation("Slack", userA.Name, userA.ID, CommandResponse{OperationID: "op-slack"})

	if _, result, _ := runAs(t, ctx, sr, adminUser); result == nil || !result.Completed {
		t.Fatalf("runResult = %+v, want completed", result)
	}
	events, _, _ := sr.snapshotTrackedOperation("op-slack")
	if !slices.ContainsFunc(events, func(e OperationStatus) bool { return e.State == "Downloading" }) {
		t.Fatalf("operation records = %+v, want a Downloading record from the requested run", events)
	}
}

// peerConn is a real transport connection whose Peer is caller, so a Client
// talks to the real handler as an administrator on any host.
type peerConn struct {
	clientConn
	caller peer
}

func (c peerConn) Peer() (peer, error) { return c.caller, nil }

// serveOne listens on a test pipe of its own and hands its first connection
// to handle. It returns the pipe name for a Client.
func serveOne(t *testing.T, handle func(clientConn)) string {
	t.Helper()
	name := testPipeName(t)
	ln, err := listen(name)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		handle(conn)
		_ = conn.Close()
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
	return name
}

// Client.Run and `-S run` over the real transport and handler.
func TestClientRunEndToEnd(t *testing.T) {
	failing := &report.Report{FailedItems: []report.FailedItem{{Name: "Chrome"}}}
	for _, tt := range []struct {
		name      string
		rep       *report.Report
		wantItems []string
		wantErr   string
	}{
		{name: "success"},
		{name: "failed item", rep: failing, wantItems: []string{"failed: Chrome"}, wantErr: "1 failed item"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var runs atomic.Int32
			sr, ctx := startTestRunner(t, testServiceConfig(t), countingRun(&runs, tt.rep, nil))
			handle := func(c clientConn) { sr.handleConn(ctx, peerConn{c, adminUser}) }

			result, err := NewClient(serveOne(t, handle)).Run(ctx)
			if err != nil || !result.Completed || len(result.FailedItems) != len(tt.wantItems) {
				t.Fatalf("Run = %+v, %v", result, err)
			}
			resp, err := SendCommand(config.Configuration{ServicePipeName: serveOne(t, handle)}, "run")
			if !slices.Equal(resp.Items, tt.wantItems) {
				t.Errorf("-S run items = %q, want %q", resp.Items, tt.wantItems)
			}
			if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("-S run error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// A connection that closes after the ack leaves the outcome unknown: the
// client says so, and never reports it as a run that failed or succeeded.
func TestClientRunConnectionClosedAfterAck(t *testing.T) {
	name := serveOne(t, func(c clientConn) {
		var req rpcRequest
		if err := json.NewDecoder(c).Decode(&req); err != nil {
			return
		}
		_ = writeResult(c, req.ID, AcceptedOperation{Accepted: true, QueuedAtUTC: nowRFC3339UTC()})
	})
	_, err := SendCommand(config.Configuration{ServicePipeName: name}, "run")
	if err == nil || !strings.Contains(err.Error(), "closed the connection before the run ended") {
		t.Fatalf("-S run = %v, want the closed connection reported", err)
	}
}
