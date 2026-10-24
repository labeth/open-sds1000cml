// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// benchServer serves a 5000-sample frame with its Q8.8 record.
// TRLC-LINKS: REQ-SDS-162
func benchServer() *Server {
	n := 5000
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Q1: make([]uint16, n), Q2: make([]uint16, n),
		Valid: n, WinCols: n, Seq: 9, SampleS: 2e-9, EdgeX: 2500}
	for i := range f.C1 {
		v := uint8(60 + 130*(i/40%2))
		f.C1[i], f.C2[i] = v, 255-v
		f.Q1[i], f.Q2[i] = uint16(v)<<8|0x40, uint16(255-v)<<8
	}
	fs := &fakeScope{stats: engine.Stats{Frames: 5, Running: true, TdivS: 1e-6, TrigCode: 31437}, frame: f}
	return New(fs, nil, nil, nil)
}

// TRLC-LINKS: REQ-SDS-164
func BenchmarkHTTPStatus(b *testing.B) {
	h := benchServer().Handler()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/status", nil))
	}
}

// TRLC-LINKS: REQ-SDS-163
func BenchmarkHTTPFrameBin(b *testing.B) {
	h := benchServer().Handler()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/frame.bin?since=0", nil))
	}
}

// TRLC-LINKS: REQ-SDS-162
func BenchmarkHTTPSet(b *testing.B) {
	h := benchServer().Handler()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/set", strings.NewReader(`{"control":"tdiv","value":0.000002}`)))
	}
}

// TRLC-LINKS: REQ-SDS-161
func BenchmarkHTTPAsset(b *testing.B) {
	h := benchServer().Handler()
	for b.Loop() {
		r := httptest.NewRequest("GET", "/app_core.js", nil)
		r.Header.Set("Accept-Encoding", "gzip")
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
}

// TRLC-LINKS: REQ-SDS-161
func BenchmarkHTTPRoot(b *testing.B) {
	h := benchServer().Handler()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}
}

// TRLC-LINKS: REQ-SDS-161
func BenchmarkHTTPClaim(b *testing.B) {
	h := benchServer().Handler()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/claim", nil))
	}
}

// The claim reads as the encoder wrote it.
// TRLC-LINKS: REQ-SDS-161
func TestClaimText(t *testing.T) {
	rec := httptest.NewRecorder()
	benchServer().Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/claim", nil))
	var got map[string]uint64
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	json.NewEncoder(&want).Encode(map[string]uint64{"epoch": got["epoch"]})
	if rec.Body.String() != want.String() {
		t.Fatalf("%q, want %q", rec.Body.String(), want.String())
	}
}

// TRLC-LINKS: REQ-SDS-163
func BenchmarkWindowFrame(b *testing.B) {
	sig := make([]uint8, 5000)
	for i := range sig {
		sig[i] = uint8(i)
	}
	for b.Loop() {
		window(sig, 5000, 5000, 2500, false, 800, 0.5)
		window(sig, 5000, 1000, 2500, true, 800, 0.5)
	}
}
