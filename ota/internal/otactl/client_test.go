package otactl

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"open-sds/ota/internal/agent"
	"open-sds/ota/internal/config"
)

// startAgentLineServer runs a REAL agent behind a loopback TCP listener with
// the same one-JSON-line-per-request framing the on-device tcp listener
// serves, so the otactl transport and Client are exercised end to end against
// the actual dispatch path. Everything stays on 127.0.0.1.
func startAgentLineServer(t *testing.T) (addr string, a *agent.Agent) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OTA_DIR", dir+"/ota")
	t.Setenv("OTA_SLOT_ROOT", dir+"/slots")
	t.Setenv("OTA_HEALTH_DIR", dir)
	t.Setenv("OTA_LISTEN", "")
	t.Setenv("OTA_NATS", "")
	// Keep every device path inside the temp dir (never the host's /dev).
	wd := dir + "/watchdog"
	if err := os.WriteFile(wd, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTA_WD_DEV", wd)
	t.Setenv("OTA_GPMC", dir+"/Gpmc")
	t.Setenv("OTA_FPGA_KEY", dir+"/fpga_key")
	if err := os.MkdirAll(dir+"/ota", 0o755); err != nil {
		t.Fatal(err)
	}
	a = agent.New(config.Load())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReaderSize(c, 1<<20)
				for {
					line, err := r.ReadBytes('\n')
					if len(line) > 0 {
						if _, werr := c.Write(append(a.DispatchJSON(line), '\n')); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), a
}

func TestTCPTransportCall(t *testing.T) {
	addr, _ := startAgentLineServer(t)
	tr := NewTCP(addr)
	defer tr.Close()
	c := &Client{T: tr}

	raw, err := c.Call("ping", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !strings.Contains(string(raw), "device") {
		t.Errorf("ping data = %s", raw)
	}

	// Args must survive the wire: exec a real echo through the device path.
	raw, err = c.Call("exec", map[string]any{"argv": []string{"/bin/echo", "wire"}}, 5*time.Second)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(string(raw), "wire") {
		t.Errorf("exec data = %s", raw)
	}
}

func TestTCPTransportDeviceError(t *testing.T) {
	addr, _ := startAgentLineServer(t)
	c := &Client{T: NewTCP(addr)}
	_, err := c.Call("no.such.command", nil, 5*time.Second)
	if err == nil {
		t.Fatal("unknown command must surface as an error")
	}
	if !strings.Contains(err.Error(), "device error") || !strings.Contains(err.Error(), "unknown cmd") {
		t.Errorf("err = %v", err)
	}
}

func TestTCPTransportDialFailure(t *testing.T) {
	// A port nothing listens on: the dial error must be reported, not hang.
	c := &Client{T: NewTCP("127.0.0.1:1")}
	_, err := c.Call("ping", nil, 2*time.Second)
	if err == nil {
		t.Fatal("dial to a closed port must fail")
	}
	if !strings.Contains(err.Error(), "dial") {
		t.Errorf("err = %v", err)
	}
}

func TestPutFileGetFileRoundTrip(t *testing.T) {
	addr, _ := startAgentLineServer(t)
	c := &Client{T: NewTCP(addr)}

	// > 2 chunks at the 100 KiB test chunk size, non-compressible.
	payload := make([]byte, 300*1024+37)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "local.bin")
	if err := os.WriteFile(local, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "device", "dest.bin")

	var lastDone, total int64
	sum, err := c.PutFile(local, dest, 0o755, 100*1024, func(done, tot int64) { lastDone, total = done, tot })
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	want := sha256.Sum256(payload)
	if sum != hex.EncodeToString(want[:]) {
		t.Errorf("device sha = %s, want %s", sum, hex.EncodeToString(want[:]))
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("device file: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Error("device file content differs from the uploaded payload")
	}
	if fi, _ := os.Stat(dest); fi.Mode().Perm() != 0o755 {
		t.Errorf("device file mode = %v, want 0755", fi.Mode().Perm())
	}
	if lastDone != int64(len(payload)) || total != int64(len(payload)) {
		t.Errorf("progress ended at %d/%d, want %d/%d", lastDone, total, len(payload), len(payload))
	}

	// And back: GetFile must reproduce the bytes across the 256 KiB chunking.
	back := filepath.Join(t.TempDir(), "back.bin")
	if err := c.GetFile(dest, back, nil); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	b, err := os.ReadFile(back)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, payload) {
		t.Error("downloaded file differs from the original payload")
	}
}

func TestPutFileMissingLocal(t *testing.T) {
	addr, _ := startAgentLineServer(t)
	c := &Client{T: NewTCP(addr)}
	if _, err := c.PutFile(filepath.Join(t.TempDir(), "nope"), "/x", 0, 0, nil); err == nil {
		t.Fatal("PutFile of a missing local file must fail")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate([]byte("abcdef"), 3); got != "abc…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate([]byte("ab"), 3); got != "ab" {
		t.Errorf("truncate short = %q", got)
	}
}
