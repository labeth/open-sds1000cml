package agent

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"open-sds/ota/internal/rpcproto"
)

// TestHandleConnSpeaksOtactlWire drives handleConn over an in-memory pipe with
// requests encoded exactly as the otactl transport encodes them (the shared
// rpcproto envelope + newline framing), and decodes responses the same way —
// the agent<->otactl protocol-drift canary.
func TestHandleConnSpeaksOtactlWire(t *testing.T) {
	a := testAgent(t)
	srv, cli := net.Pipe()
	defer cli.Close()
	go a.handleConn(srv)

	r := bufio.NewReaderSize(cli, 1<<20)
	call := func(cmd string, args any) rpcproto.Response {
		t.Helper()
		req, err := json.Marshal(rpcproto.Request{Cmd: cmd, Args: args})
		if err != nil {
			t.Fatal(err)
		}
		cli.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := cli.Write(append(req, '\n')); err != nil {
			t.Fatalf("write: %v", err)
		}
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var resp rpcproto.Response
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Fatalf("agent response does not decode as rpcproto.Response: %v (%s)", err, line)
		}
		return resp
	}

	// Several requests over ONE connection: the loop must not close early.
	resp := call("ping", nil)
	if !resp.OK {
		t.Fatalf("ping: %s", resp.Err)
	}
	var ping struct {
		Device string `json:"device"`
	}
	if err := json.Unmarshal(resp.Data, &ping); err != nil || ping.Device != a.cfg.DeviceID {
		t.Errorf("ping data = %s (err %v), want device %q", resp.Data, err, a.cfg.DeviceID)
	}

	resp = call("help", nil)
	if !resp.OK || !strings.Contains(string(resp.Data), "commands") {
		t.Errorf("help = %+v", resp)
	}

	resp = call("logs", map[string]any{"file": "boot", "tail": 16})
	if resp.OK {
		t.Error("logs for a missing file should propagate the error over the wire")
	}

	// A garbage line must produce an error response and keep the connection.
	cli.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := cli.Write([]byte("{oops\n")); err != nil {
		t.Fatal(err)
	}
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read after garbage: %v", err)
	}
	var bad rpcproto.Response
	if err := json.Unmarshal(line, &bad); err != nil {
		t.Fatalf("error response not decodable: %v (%s)", err, line)
	}
	if bad.OK || !strings.Contains(bad.Err, "bad request json") {
		t.Errorf("garbage line response = %+v", bad)
	}
	if resp := call("ping", nil); !resp.OK {
		t.Errorf("connection unusable after a garbage line: %+v", resp)
	}
}
