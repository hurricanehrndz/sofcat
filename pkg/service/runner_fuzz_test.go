package service

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

// fuzzConn feeds one request to handleConn and records what it answers.
type fuzzConn struct {
	io.Reader
	out bytes.Buffer
}

func (c *fuzzConn) Write(p []byte) (int, error) { return c.out.Write(p) }
func (c *fuzzConn) Close() error                { return nil }
func (c *fuzzConn) Peer() (peer, error)         { return peer{Name: "fuzz"}, nil }
func (c *fuzzConn) Abort()                      {}

// The service runs as SYSTEM and reads requests from any local user, so no
// request may crash it or get anything but a well-formed JSON-RPC answer.
func FuzzHandleConn(f *testing.F) {
	for _, seed := range []string{
		``,
		`{"jsonrpc" "2.0"}`,
		`[{"jsonrpc":"2.0","id":1,"method":"getServiceInfo"}]`,
		`{"jsonrpc":"2.0","id":7,"method":"getServiceInfo"}`,
		`{"jsonrpc":"2.0","id":{},"method":"getServiceInfo"}`,
		`{"jsonrpc":"2.0","id":"a","method":"listOptionalInstalls"}`,
		`{"jsonrpc":"2.0","id":"b","method":"getBranding"}`,
		`{"jsonrpc":"2.0","id":"c","method":"installItem","params":{"itemName":"Slack"}}`,
		`{"jsonrpc":"2.0","id":"d","method":"removeItem","params":{"itemName":"Slack"}}`,
		`{"jsonrpc":"2.0","id":"e","method":"cancelOperation","params":["op"]}`,
		`{"jsonrpc":"2.0","id":"f","method":"streamOperationStatus","params":{"operationId":"x"}}`,
		`{"jsonrpc":"2.0","method":"installItem","params":{"itemName":"Slack"}}`,
	} {
		f.Add([]byte(seed))
	}

	cfg := testServiceConfig(f)
	stubOptional(f, "Slack")
	sr, ctx := startTestRunner(f, cfg, noopRun)

	f.Fuzz(func(t *testing.T, request []byte) {
		conn := &fuzzConn{Reader: bytes.NewReader(request)}
		sr.handleConn(ctx, conn)
		if conn.out.Len() == 0 {
			return // no request, or a notification
		}
		var resp rpcResponse
		if err := json.NewDecoder(&conn.out).Decode(&resp); err != nil {
			t.Fatalf("response is not JSON: %v", err)
		}
		if resp.JSONRPC != jsonrpcVersion || !validRequestID(resp.ID) || (resp.Result == nil) == (resp.Error == nil) {
			t.Fatalf("malformed response %+v (error %+v)", resp, resp.Error)
		}
		if resp.Error != nil && resp.Error.Code == codeInternalError {
			t.Fatalf("request %q panicked the handler: %s", request, resp.Error.Message)
		}
	})
}
