package agent

// serveTCP tests: the local/LAN fallback transport end-to-end over a real
// socket — accept → handleConn → one JSON response line per request — plus
// the listener's close-on-Stop contract. OTA_LISTEN=127.0.0.1:0 binds an
// ephemeral port; the bound address is observed through TCPAddr().

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// startTCPAgent brings up serveTCP on an ephemeral localhost port and returns
// the agent plus the bound address.
func startTCPAgent(t *testing.T) (*Agent, string) {
	t.Helper()
	a := testAgent(t)
	a.cfg.TCPListen = "127.0.0.1:0" // ephemeral port; never a fixed LAN address
	go a.serveTCP()

	deadline := time.Now().Add(5 * time.Second)
	for a.TCPAddr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("tcp control listener never came up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return a, a.TCPAddr()
}

// stopTCPAgent stops the agent and pokes the listener once so the accept loop
// observes the stop and closes it (the loop checks a.stopped between accepts).
func stopTCPAgent(a *Agent, addr string) {
	a.Stop()
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close()
	}
}

func roundTrip(t *testing.T, conn net.Conn, r *bufio.Reader, req string) Response {
	t.Helper()
	if _, err := conn.Write([]byte(req + "\n")); err != nil {
		t.Fatalf("write %q: %v", req, err)
	}
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read response to %q: %v", req, err)
	}
	var resp Response
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("bad response line %q: %v", line, err)
	}
	return resp
}

func TestServeTCPDispatchesOverRealSocket(t *testing.T) {
	a, addr := startTCPAgent(t)
	defer stopTCPAgent(a, addr)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)

	// Request 1: ping — a full accept → handleConn → Dispatch → response line.
	resp := roundTrip(t, conn, r, `{"cmd":"ping"}`)
	if !resp.OK {
		t.Fatalf("ping over tcp failed: %s", resp.Err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok || data["device"] != a.cfg.DeviceID {
		t.Errorf("ping data = %v, want device %q", resp.Data, a.cfg.DeviceID)
	}

	// Request 2 on the SAME connection: a malformed line must be answered
	// with an error response, not drop the connection (one request per line).
	resp = roundTrip(t, conn, r, `{not json`)
	if resp.OK || !strings.Contains(resp.Err, "bad request json") {
		t.Errorf("malformed line: got %+v, want bad-json error", resp)
	}

	// Request 3: the connection still serves after the bad line.
	resp = roundTrip(t, conn, r, `{"cmd":"help"}`)
	if !resp.OK {
		t.Errorf("help after bad line failed: %s", resp.Err)
	}
}

func TestServeTCPConcurrentConnections(t *testing.T) {
	a, addr := startTCPAgent(t)
	defer stopTCPAgent(a, addr)

	// The accept loop hands each connection to its own handleConn goroutine;
	// two interleaved clients must both be served.
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	r1, r2 := bufio.NewReader(c1), bufio.NewReader(c2)

	if resp := roundTrip(t, c1, r1, `{"cmd":"ping"}`); !resp.OK {
		t.Errorf("conn1 ping failed: %s", resp.Err)
	}
	if resp := roundTrip(t, c2, r2, `{"cmd":"ping"}`); !resp.OK {
		t.Errorf("conn2 ping failed: %s", resp.Err)
	}
	if resp := roundTrip(t, c1, r1, `{"cmd":"help"}`); !resp.OK {
		t.Errorf("conn1 second request failed: %s", resp.Err)
	}
}

func TestServeTCPListenerClosesAfterStop(t *testing.T) {
	a, addr := startTCPAgent(t)

	// Sanity: reachable before Stop.
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("pre-stop dial: %v", err)
	}
	c.Close()

	// Contract (accept loop): Stop() flags a.stopped; the loop notices it on
	// the next accept and closes the listener. So after Stop, at most a few
	// in-flight dials get through, then the port must refuse connections.
	a.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return // closed — contract satisfied
		}
		c.Close() // unblocked one accept; loop will see stopped and close
		if time.Now().After(deadline) {
			t.Fatal("listener still accepting 5s after Stop")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
