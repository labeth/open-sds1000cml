// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package agent

import (
	"bufio"
	"encoding/json"
	"net"
	"open-sds/ota/internal/rpcproto"
	"strings"
	"testing"
	"time"
)

// TestHandleConnSpeaksOtactlWire drives handleConn over an in-memory pipe with
// requests encoded exactly as the otactl transport encodes them (the shared
// rpcproto envelope + newline framing), and decodes responses the same way —
// the agent<->otactl protocol-drift canary.
// TRLC-LINKS: REQ-SDS-107
func TestHandleConnSpeaksOtactlWire(t *testing.T) {
	a := testAgent(t)
	srv, cli := net.Pipe()
	defer func() { _ = cli.Close() }()
	go a.handleConn(srv)

	r := bufio.NewReaderSize(cli, 1<<20)
	call := func(cmd string, args any) rpcproto.Response {
		t.Helper()
		req, err := json.Marshal(rpcproto.Request{Cmd: cmd, Args: args})
		if err != nil {
			t.Fatal(err)
		}
		_ = cli.SetDeadline(time.Now().Add(5 * time.Second))
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
	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))
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

// A request line over the limit is answered with an error and the connection
// closed, rather than buffered until the peer stops sending.
// TRLC-LINKS: REQ-SDS-107
func TestHandleConnRefusesAnOverlongLine(t *testing.T) {
	a := testAgent(t)
	srv, cli := net.Pipe()
	defer func() { _ = cli.Close() }()
	go a.handleConn(srv)
	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))

	got := make(chan []byte, 1)
	go func() {
		line, _ := bufio.NewReader(cli).ReadBytes('\n')
		got <- line
	}()
	chunk := make([]byte, 64<<10)
	for i := range chunk {
		chunk[i] = 'x'
	}
	for sent := 0; sent <= maxRequestLine; sent += len(chunk) {
		if _, err := cli.Write(chunk); err != nil {
			break // the agent stopped reading: it has answered
		}
	}
	var resp Response
	if err := json.Unmarshal(<-got, &resp); err != nil || resp.OK || !strings.Contains(resp.Err, "over") {
		t.Fatalf("overlong line: %+v %v, want an error reply", resp, err)
	}
}
