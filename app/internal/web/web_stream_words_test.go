// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// wordsScope is a scope whose digital stream a page decodes: two chunks of
// LIN's quiet line and a byte, the second after lost words.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
type wordsScope struct {
	*fakeScope
	mu       sync.Mutex
	asked    []uint64
	waits    []time.Duration
	posted   []byte
	session  uint32
	through  uint64
	released int
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *wordsScope) ReadClientWords(seq uint64, max int, wait time.Duration, dst []engine.ClientChunk) (engine.ClientWords, func()) {
	s.mu.Lock()
	s.asked = append(s.asked, seq)
	s.waits = append(s.waits, wait)
	s.mu.Unlock()
	info := engine.ClientStreamInfo{Session: 0xC0000003, Proto: "lin", PeriodS: 8.192e-6, Lines: 1, CloseN: 900, MaxN: 4000, PushN: 1 << 12,
		PushMax: 1 << 18, KeepWords: 60, LeadWords: 4, Mask: 0xFFFF, Active: true,
		Params: engine.SerialParams{Proto: 11, Baud: 19200, Threshold: 90, HaveThr: true}}
	quiet := []byte{3, 0, 0, 0x80, 0xFF, 0xFF, 0, 0} // a run of three quiet words
	busy := []byte{2, 0, 0, 0, 0x0F, 0xF0, 0, 0, 0xFF, 0x00, 0, 0}
	dst = append(dst[:0], engine.ClientChunk{Seq: seq, Index: 160, Words: 3, Check: true, Enc: quiet},
		engine.ClientChunk{Seq: seq + 1, Index: 1600, Words: 2, Break: true, Check: true, Enc: busy})
	return engine.ClientWords{Info: info, Next: seq + 2, Dropped: 1, Lag: 4096, Chunks: dst}, func() {
		s.mu.Lock()
		s.released++
		s.mu.Unlock()
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *wordsScope) PostClientFrames(b []byte) error {
	// the frames' layout walked as the engine reads them (engine's tests
	// hold what it makes of them)
	if len(b) < 20 || string(b[:4]) != "SDF1" {
		return engine.ErrClientFramesBody
	}
	at := 20
	for range binary.LittleEndian.Uint32(b[16:]) {
		if at+20 > len(b) {
			return engine.ErrClientFramesBody
		}
		at += 20 + int(b[at+18]) + 13*int(binary.LittleEndian.Uint16(b[at+16:]))
	}
	if at != len(b) {
		return engine.ErrClientFramesBody
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session, s.through = binary.LittleEndian.Uint32(b[4:]), binary.LittleEndian.Uint64(b[8:])
	s.posted = append(s.posted[:0], b...)
	return nil
}

// /api/stream/words.bin serves the engine's chunks as the page reads them -
// the header, each chunk's head, then the coded words - with the read's
// seq, size and wait passed on; /api/stream/frames.bin hands a page's
// frames to the engine and refuses a body that is not frames; a scope
// without client decode answers 404. The page's reader (webdec.js) reads
// the reply, decodes it and posts frames the engine reads.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestStreamWordsEndpoints(t *testing.T) {
	sc := &wordsScope{fakeScope: &fakeScope{}}
	srv := httptest.NewServer(New(sc, nil, nil, nil).Handler())
	defer srv.Close()
	r, err := http.Get(srv.URL + "/api/stream/words.bin?from=7&max=100&wait=20000")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || string(body[:4]) != "SDW1" {
		t.Fatalf("status %d, %q", r.StatusCode, body[:min(8, len(body))])
	}
	if len(sc.asked) != 1 || sc.asked[0] != 7 || sc.waits[0] != 1500*time.Millisecond {
		t.Fatalf("asked %v waits %v", sc.asked, sc.waits)
	}
	jl := int(binary.LittleEndian.Uint32(body[4:]))
	if jl%4 != 0 {
		t.Fatalf("the header is %d bytes: the words are not aligned", jl)
	}
	var h map[string]any
	if err := json.Unmarshal(body[8:8+jl], &h); err != nil {
		t.Fatal(err)
	}
	if h["proto"] != "lin" || h["active"] != true || h["next"].(float64) != 9 || h["chunks"].(float64) != 2 || h["dropped"].(float64) != 1 ||
		h["lag"].(float64) != 4096 || h["params"].(map[string]any)["haveThr"] != true {
		t.Fatalf("header %v", h)
	}
	heads := body[8+jl:]
	want := []uint32{160, 0, 3, 2, 8, 1600, 0, 2, 3, 12}
	for i, v := range want {
		if got := binary.LittleEndian.Uint32(heads[4*i:]); got != v {
			t.Fatalf("chunk head word %d: %d, want %d", i, got, v)
		}
	}
	if enc := heads[4*len(want):]; len(enc) != 20 || enc[3] != 0x80 {
		t.Fatalf("coded words %x", enc)
	}

	// the page's reader takes the reply and posts frames the engine reads
	t.Run("page", func(t *testing.T) {
		testenv.NeedNode(t)
		dir := t.TempDir()
		rep := filepath.Join(dir, "r.bin")
		os.WriteFile(rep, body, 0o644)
		cases, _ := json.Marshal([]map[string]any{{"name": "web", "replies": []string{rep}, "out": filepath.Join(dir, "f.bin")}})
		os.WriteFile(filepath.Join(dir, "cases.json"), cases, 0o644)
		out, err := exec.Command("node", "webdec_check.mjs", "session", filepath.Join(dir, "cases.json")).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "WEBDEC OK") {
			t.Fatalf("webdec_check.mjs: %v\n%s", err, out)
		}
		fb, _ := os.ReadFile(filepath.Join(dir, "f.bin"))
		pr, err := http.Post(srv.URL+"/api/stream/frames.bin", "application/octet-stream", bytes.NewReader(fb))
		if err != nil || pr.StatusCode != http.StatusNoContent {
			t.Fatalf("frames post: %v %v", err, pr.Status)
		}
		// the decode starts at the first chunk, the second follows lost words
		if sc.session != 0xC0000003 || sc.through != 1600+32 {
			t.Fatalf("posted session %x through %d", sc.session, sc.through)
		}
	})
	// a hand-made frame: its start text, a data byte and a loss
	var fb []byte
	put := func(v ...uint32) {
		for _, x := range v {
			fb = binary.LittleEndian.AppendUint32(fb, x)
		}
	}
	fb = append(fb, "SDF1"...)
	put(5, 900, 0, 2)
	put(100, 0, 140, 0)
	fb = append(fb, 2, 0, 3, 0)
	fb = append(fb, "BRK"...)
	put(100, 0, 0)
	fb = append(fb, 4)
	put(110, 0, 0x55)
	fb = append(fb, 0)
	put(200, 0, 200, 0)
	fb = append(fb, 1, 0, 0, 0)
	put(200, 0, 0)
	fb = append(fb, 6)
	pr, err := http.Post(srv.URL+"/api/stream/frames.bin", "application/octet-stream", bytes.NewReader(fb))
	if err != nil || pr.StatusCode != http.StatusNoContent {
		t.Fatalf("frames post: %v %v", err, pr.Status)
	}
	if sc.session != 5 || sc.through != 900 || !bytes.Equal(sc.posted, fb) {
		t.Fatalf("posted %d %d %x", sc.session, sc.through, sc.posted)
	}
	if sc.released != len(sc.asked) {
		t.Fatalf("%d reads, %d released", len(sc.asked), sc.released)
	}
	for _, bad := range [][]byte{nil, []byte("SDF1"), fb[:len(fb)-3], append([]byte("XXXX"), fb[4:]...)} {
		pr, err := http.Post(srv.URL+"/api/stream/frames.bin", "application/octet-stream", bytes.NewReader(bad))
		if err != nil || pr.StatusCode != http.StatusBadRequest {
			t.Fatalf("a bad body %q: %v %v", bad, err, pr.Status)
		}
	}
	// a scope without client decode
	plain := httptest.NewServer(New(&fakeScope{}, nil, nil, nil).Handler())
	defer plain.Close()
	if r, err := http.Get(plain.URL + "/api/stream/words.bin"); err != nil || r.StatusCode != http.StatusNotFound {
		t.Fatalf("words.bin without client decode: %v %v", err, r.Status)
	}
}

// benchWords is a scope whose ring always holds a megabyte of busy words
// in 64 KB chunks, copied out per read as the engine's ring copies them.
type benchWords struct {
	*fakeScope
	enc []byte
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *benchWords) ReadClientWords(seq uint64, max int, wait time.Duration, dst []engine.ClientChunk) (engine.ClientWords, func()) {
	cw := engine.ClientWords{Info: engine.ClientStreamInfo{Session: 1, Proto: "swd", Lines: 2, Mask: 0xFFFFFFFF, Active: true}, Next: seq + 16}
	n := min(max>>16, len(s.enc)>>16)
	for k := 0; k < n; k++ {
		dst = append(dst, engine.ClientChunk{Seq: seq + uint64(k), Index: uint64(k) << 18, Words: 16383, Enc: s.enc[k<<16 : (k+1)<<16]})
	}
	cw.Chunks = dst
	return cw, func() {}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *benchWords) PostClientFrames([]byte) error { return nil }

// BenchmarkStreamWordsHTTP is the scope's cost of serving a page the
// densest digital stream: a megabyte reply (a quarter second at 2^9, two
// busy lines) through the handler and the loopback, the client's reading
// included (an upper bound for the scope's share).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func BenchmarkStreamWordsHTTP(b *testing.B) {
	sc := &benchWords{fakeScope: &fakeScope{}, enc: make([]byte, 1<<20)}
	srv := httptest.NewServer(New(sc, nil, nil, nil).Handler())
	defer srv.Close()
	cl := srv.Client()
	b.SetBytes(1 << 20)
	for b.Loop() {
		r, err := cl.Get(srv.URL + "/api/stream/words.bin?from=0&max=1048576")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
	}
}

// discardRW is a response writer that drops the body: the handler's own cost.
type discardRW struct{ h http.Header }

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *discardRW) Header() http.Header { return d.h }

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *discardRW) Write(b []byte) (int, error) { return len(b), nil }

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *discardRW) WriteHeader(int) {}

// BenchmarkStreamWordsHandler is the handler's own cost of a megabyte reply,
// the socket's left out: the scope's per-request overhead (the header, the
// chunk heads, the routing).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func BenchmarkStreamWordsHandler(b *testing.B) {
	sc := &benchWords{fakeScope: &fakeScope{}, enc: make([]byte, 1<<20)}
	h := New(sc, nil, nil, nil).Handler()
	req := httptest.NewRequest("GET", "/api/stream/words.bin?from=0&max=1048576", nil)
	w := &discardRW{h: http.Header{}}
	b.ReportAllocs()
	for b.Loop() {
		clear(w.h)
		h.ServeHTTP(w, req)
	}
}
