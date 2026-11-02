// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// A peer that never finishes its header loses the connection, while a
// request that is past its header may take longer than the header bound.
// TRLC-LINKS: REQ-SDS-161
func TestWebServerBoundsTheHeaderOnly(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newWebServer("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(webHeaderTimeout + time.Second) // a parked long-poll
		_, _ = io.WriteString(w, "late")
	}))
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	d := net.Dialer{Timeout: time.Second}
	slow, err := d.DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slow.Close() }()
	if _, err := io.WriteString(slow, "GET / HTTP/1.1\r\nHost: x\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := slow.SetReadDeadline(time.Now().Add(webHeaderTimeout + 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := io.ReadAll(slow); err != nil {
		t.Fatalf("a half header kept its connection: %v", err)
	}
	if took := time.Since(start); took > webHeaderTimeout+2*time.Second {
		t.Fatalf("a half header held its connection %v", took)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if body, err := io.ReadAll(resp.Body); err != nil || string(body) != "late" {
		t.Fatalf("a parked request: %q, %v", body, err)
	}
}
