// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT
package agent

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
