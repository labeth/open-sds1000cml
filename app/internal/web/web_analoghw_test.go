package web

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"open-sds/app/internal/analog"
	"sync"
	"testing"
	"time"
)

// spyTr is a fake SPI transport: it records what the front end actually puts on
// the wire, so these tests assert the relay WORD, not just that a method was
// called. That is the point of the new verbs — the bit encoding is the product.
type spyTr struct {
	mu    sync.Mutex
	words []uint32
	gains [][2]uint8 // {ch2, ch1}
}

func (s *spyTr) WriteRelay(w uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.words = append(s.words, w)
	return nil
}

func (s *spyTr) WriteGain(ch2, ch1 uint8) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gains = append(s.gains, [2]uint8{ch2, ch1})
	return nil
}

func (s *spyTr) lastWord(t *testing.T) uint32 {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.words) == 0 {
		t.Fatal("no relay word emitted")
	}
	return s.words[len(s.words)-1]
}

// newHWServer wires a real *analog.FrontEnd (over a spy transport) into the web
// server, so the verbs are exercised end to end: JSON → analogHW → relay word.
func newHWServer(t *testing.T) (*Server, *analog.FrontEnd, *spyTr) {
	t.Helper()
	tr := &spyTr{}
	fe := analog.New(tr, func(time.Duration) {}, nil)
	return New(&fakeScope{}, fe, nil, nil), fe, tr
}

func postFields(t *testing.T, s *Server, fields map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(fields)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/set", bytes.NewReader(body)))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json reply: %v", err)
	}
	return out
}

// TestHWRelayVerbs covers the three named actuators AF-0.4 adds, end to end.
func TestHWRelayVerbs(t *testing.T) {
	s, fe, tr := newHWServer(t)

	if out := post(t, s, "bwl1", 1); out["ok"] != true {
		t.Fatalf("bwl1 on: %v", out)
	}
	if w := tr.lastWord(t); w != 0x70ad2c {
		t.Fatalf("bwl1 on → %#06x, want 0x70ad2c (CH1 bit0 cleared)", w)
	}
	if out := post(t, s, "bwl2", 1); out["ok"] != true {
		t.Fatalf("bwl2 on: %v", out)
	}
	if w := tr.lastWord(t); w != 0x70ac2c {
		t.Fatalf("bwl2 on → %#06x, want 0x70ac2c", w)
	}
	if out := post(t, s, "bwl1", 0); out["ok"] != true {
		t.Fatalf("bwl1 off: %v", out)
	}
	if out := post(t, s, "bwl2", 0); out["ok"] != true || tr.lastWord(t) != 0x70ad2d {
		t.Fatalf("bwl restore → %#06x, want 0x70ad2d", tr.lastWord(t))
	}

	// Hardware coupling relay: 0=DC, 1=AC, 2=GND. Captured vendor words.
	if out := post(t, s, "couplinghw1", 1); out["ok"] != true || tr.lastWord(t) != 0x70ad25 {
		t.Fatalf("couplinghw1 AC → %#06x (%v), want 0x70ad25", tr.lastWord(t), out)
	}
	if out := post(t, s, "couplinghw1", 2); out["ok"] != true || tr.lastWord(t) != 0x70ad27 {
		t.Fatalf("couplinghw1 GND → %#06x (%v), want 0x70ad27", tr.lastWord(t), out)
	}
	if out := post(t, s, "couplinghw2", 1); out["ok"] != true || tr.lastWord(t) != 0x70a527 {
		t.Fatalf("couplinghw2 AC → %#06x (%v), want 0x70a527", tr.lastWord(t), out)
	}
	if out := post(t, s, "couplinghw1", 7); out["ok"] != false {
		t.Fatalf("bad couplinghw accepted: %v", out)
	}
	// The hardware relay is NOT the software display transform: the shipped
	// "coupling1" verb must be unaffected by it and vice versa.
	if fe.Coupling(0) != analog.CplDC {
		t.Fatalf("couplinghw changed the software coupling to %d", fe.Coupling(0))
	}
	if out := post(t, s, "coupling1", 1); out["ok"] != true {
		t.Fatalf("coupling1: %v", out)
	}
	if fe.CouplingHW(0) != analog.CplGND {
		t.Fatalf("software coupling changed the relay state to %d", fe.CouplingHW(0))
	}

	// Restore both channels, then walk the trigger-coupling nibble.
	if out := post(t, s, "couplinghw1", 0); out["ok"] != true {
		t.Fatalf("couplinghw1 DC: %v", out)
	}
	if out := post(t, s, "couplinghw2", 0); out["ok"] != true || tr.lastWord(t) != 0x70ad2d {
		t.Fatalf("coupling restore → %#06x, want 0x70ad2d", tr.lastWord(t))
	}
	for _, c := range []struct {
		mode int
		want uint32
	}{{0, 0x70ad2d}, {1, 0x50ad2d}, {2, 0xf0ad2d}, {3, 0x40ad2d}} {
		if out := post(t, s, "trigcpl", float64(c.mode)); out["ok"] != true || tr.lastWord(t) != c.want {
			t.Fatalf("trigcpl %d → %#06x (%v), want %#06x", c.mode, tr.lastWord(t), out, c.want)
		}
	}
	if out := post(t, s, "trigcpl", 4); out["ok"] != false {
		t.Fatalf("out-of-range trigcpl accepted: %v", out)
	}
}

// TestRawVerbsAreDebugGated: the escape hatches refuse to emit until the front
// end's raw hatches are armed, and when armed they honour the absolute-word
// discipline (relay word verbatim + both gain bytes).
func TestRawVerbsAreDebugGated(t *testing.T) {
	s, fe, tr := newHWServer(t)

	if out := post(t, s, "relayraw", 0x70ad2d); out["ok"] != false {
		t.Fatalf("relayraw accepted while disarmed: %v", out)
	}
	if out := postFields(t, s, map[string]any{"control": "gainraw", "lo": 12.0, "hi": 57.0}); out["ok"] != false {
		t.Fatalf("gainraw accepted while disarmed: %v", out)
	}
	if len(tr.words) != 0 || len(tr.gains) != 0 {
		t.Fatalf("a refused raw verb still emitted: words=%v gains=%v", tr.words, tr.gains)
	}

	fe.SetRawDebug(true)
	// AF-2.5's unassigned-bit probe: channel bits 4/6 and byte-2 bits [1:0].
	if out := post(t, s, "relayraw", 0x73ed69); out["ok"] != true || out["applied"] != float64(0x73ed69) {
		t.Fatalf("relayraw armed: %v", out)
	}
	if w := tr.lastWord(t); w != 0x73ed69 {
		t.Fatalf("relayraw word = %#06x, want it verbatim", w)
	}
	if len(tr.gains) != 1 || tr.gains[0] != [2]uint8{57, 57} {
		t.Fatalf("relayraw gain flush = %v, want one CH2/CH1 pair from the seeded shadows", tr.gains)
	}
	// Range and structural guards.
	if out := post(t, s, "relayraw", 0x1000000); out["ok"] != false {
		t.Fatalf("relayraw wider than 24 bits accepted: %v", out)
	}
	if out := post(t, s, "relayraw", -1); out["ok"] != false {
		t.Fatalf("negative relayraw accepted: %v", out)
	}
	if out := post(t, s, "relayraw", 0x702d2d); out["ok"] != false {
		t.Fatalf("relayraw with both bytes addressed to CH1 accepted: %v", out)
	}

	// gainraw: lo = CH1 byte, hi = CH2 byte; both go out, CH2 first, no relay.
	nWords := len(tr.words)
	if out := postFields(t, s, map[string]any{"control": "gainraw", "lo": 0xe5, "hi": 0xe6}); out["ok"] != true {
		t.Fatalf("gainraw armed: %v", out)
	}
	if len(tr.words) != nWords {
		t.Fatal("gainraw emitted a relay word")
	}
	if g := tr.gains[len(tr.gains)-1]; g != [2]uint8{0xe6, 0xe5} {
		t.Fatalf("gainraw = ch2:%#02x ch1:%#02x, want e6/e5", g[0], g[1])
	}
	for _, bad := range []map[string]any{
		{"control": "gainraw", "lo": 256.0, "hi": 0.0},
		{"control": "gainraw", "lo": 0.0, "hi": -1.0},
	} {
		if out := postFields(t, s, bad); out["ok"] != false {
			t.Fatalf("out-of-range gainraw %v accepted: %v", bad, out)
		}
	}
}

// TestHWVerbsRefusedWithoutFrontEnd: a front end that does not implement the
// optional hardware surface (or is absent entirely) refuses cleanly rather than
// panicking or silently succeeding.
func TestHWVerbsRefusedWithoutFrontEnd(t *testing.T) {
	for _, s := range []*Server{
		New(&fakeScope{}, nil, nil, nil),           // no front end at all
		New(&fakeScope{}, &fakeAnalog{}, nil, nil), // Analog without the hw surface
	} {
		for _, verb := range []string{"bwl1", "bwl2", "couplinghw1", "couplinghw2", "trigcpl", "relayraw", "gainraw"} {
			if out := post(t, s, verb, 0); out["ok"] != false {
				t.Fatalf("%s accepted without a hardware front end: %v", verb, out)
			}
		}
	}
}
