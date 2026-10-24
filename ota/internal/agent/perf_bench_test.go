// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT
package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"open-sds/ota/internal/config"
)

// TRLC-LINKS: REQ-SDS-113
func benchAgent(b *testing.B) *Agent {
	b.Helper()
	dir := b.TempDir()
	b.Setenv("OTA_DIR", dir+"/ota")
	b.Setenv("OTA_SLOT_ROOT", dir+"/slots")
	b.Setenv("OTA_HEALTH_DIR", dir)
	b.Setenv("OTA_LISTEN", "")
	b.Setenv("OTA_NATS", "")
	wd := dir + "/watchdog"
	if err := os.WriteFile(wd, nil, 0o644); err != nil {
		b.Fatal(err)
	}
	b.Setenv("OTA_WD_DEV", wd)
	b.Setenv("OTA_GPMC", dir+"/Gpmc")
	b.Setenv("OTA_FPGA_KEY", dir+"/fpga_key")
	if err := os.MkdirAll(dir+"/ota", 0o755); err != nil {
		b.Fatal(err)
	}
	return New(config.Load())
}

// TRLC-LINKS: REQ-SDS-106
func BenchmarkPutChunk(b *testing.B) {
	a := benchAgent(b)
	id, err := a.newUpload(filepath.Join(b.TempDir(), "x.bin"), 0, "", 0o644)
	if err != nil {
		b.Fatal(err)
	}
	chunk := make([]byte, 128*1024)
	rand.Read(chunk)
	req, _ := json.Marshal(Request{Cmd: "put.chunk", Args: mustJSON(map[string]any{
		"id": id, "offset": 0, "data": base64.StdEncoding.EncodeToString(chunk)})})
	for b.Loop() {
		if r := a.DispatchJSON(req); len(r) < 10 {
			b.Fatal(string(r))
		}
	}
}

// TRLC-LINKS: REQ-SDS-116
func BenchmarkGetChunk(b *testing.B) {
	a := benchAgent(b)
	p := filepath.Join(b.TempDir(), "y.bin")
	data := make([]byte, 256*1024)
	rand.Read(data)
	os.WriteFile(p, data, 0o644)
	req, _ := json.Marshal(Request{Cmd: "get", Args: mustJSON(map[string]any{"path": p, "offset": 0, "len": 256 * 1024})})
	for b.Loop() {
		if r := a.DispatchJSON(req); len(r) < 1000 {
			b.Fatal(string(r))
		}
	}
}

// mustJSON marshals args the way otactl does, inside a cmd-first Request.
// TRLC-LINKS: REQ-SDS-113
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// One connection per call, as otactl's TCP transport dials.
// TRLC-LINKS: REQ-SDS-107
func BenchmarkTCPCall(b *testing.B) {
	a := benchAgent(b)
	req := []byte(`{"cmd":"ping"}` + "\n")
	buf := make([]byte, 4096)
	for b.Loop() {
		c, s := net.Pipe()
		done := make(chan struct{})
		go func() { a.handleConn(s); close(done) }()
		c.Write(req)
		if n, _ := c.Read(buf); n < 10 {
			b.Fatal("short response")
		}
		c.Close()
		<-done
	}
}

// An 8 MiB upload in otactl's 128 KiB chunks, committed.
// TRLC-LINKS: REQ-SDS-106
func BenchmarkUpload8M(b *testing.B) {
	a := benchAgent(b)
	data := make([]byte, 8<<20)
	rand.Read(data)
	sum := sha256.Sum256(data)
	dest := filepath.Join(b.TempDir(), "u.bin")
	for b.Loop() {
		id, err := a.newUpload(dest, int64(len(data)), hex.EncodeToString(sum[:]), 0o644)
		if err != nil {
			b.Fatal(err)
		}
		for off := 0; off < len(data); off += 128 << 10 {
			if _, err := a.writeUpload(id, int64(off), data[off:off+128<<10]); err != nil {
				b.Fatal(err)
			}
		}
		if _, _, err := a.commitUpload(id); err != nil {
			b.Fatal(err)
		}
	}
}

// The supervisor polls the health token every 500 ms for the app's life.
// TRLC-LINKS: REQ-SDS-105
func BenchmarkHealthPoll(b *testing.B) {
	p := filepath.Join(b.TempDir(), "health")
	os.WriteFile(p, []byte("frames 1234 fps 38\n"), 0o644)
	h := &healthWatcher{path: p}
	for b.Loop() {
		h.poll()
	}
}

// Takeover re-asserts TakenOver on every boot and request.
// TRLC-LINKS: REQ-SDS-104
func BenchmarkStateUpdateSame(b *testing.B) {
	sf := loadState(filepath.Join(b.TempDir(), "state.json"))
	sf.update(func(s *State) { s.TakenOver = true })
	for b.Loop() {
		if err := sf.update(func(s *State) { s.TakenOver = true }); err != nil {
			b.Fatal(err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-114
func BenchmarkProbe(b *testing.B) {
	a := benchAgent(b)
	for b.Loop() {
		a.probe(false)
	}
}

// The logs RPC tails the agent log (8 KiB by default).
// TRLC-LINKS: REQ-SDS-115
func BenchmarkTailFile(b *testing.B) {
	p := filepath.Join(b.TempDir(), "agent.log")
	os.WriteFile(p, make([]byte, 256<<10), 0o644)
	for b.Loop() {
		if t, err := tailFile(p, 8192); err != nil || len(t) != 8192 {
			b.Fatal(err)
		}
	}
}

// A heartbeat as it goes on the wire.
// TRLC-LINKS: REQ-SDS-108
func BenchmarkHeartbeatJSON(b *testing.B) {
	a := benchAgent(b)
	hb := a.heartbeat()
	for b.Loop() {
		if _, err := json.Marshal(hb); err != nil {
			b.Fatal(err)
		}
	}
}
