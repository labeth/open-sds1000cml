package decode

import (
	"fmt"
	"testing"
)

// sentWave synthesizes a SENT (SAE J2716) single-wire signal at `tick` samples
// per tick. Each pulse is 5 ticks low then high to fill its period, so the
// falling-edge-to-falling-edge period equals (12+value) ticks for a nibble and 56
// ticks for the SYNC. Layout: an idle-high lead, then for every frame a SYNC
// pulse + its nibbles (+ an optional pauseTicks pause pulse), then a final falling
// edge to close the last pulse. `jit` adds ±jit samples of deterministic period
// jitter to nibble pulses (0 = clean). The SYNC is never jittered so the derived
// tick stays exact.
func sentWave(frames [][]int, tick, pauseTicks, jit int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	var w []uint8
	pushLevel := func(v uint8, k int) {
		for j := 0; j < k; j++ {
			w = append(w, v)
		}
	}
	// pulse emits one pulse whose total period is totalTicks*tick + delta samples.
	// Because the period is exactly the number of samples emitted for this pulse,
	// delta perturbs THIS pulse's period only (no drift into the next).
	pulse := func(totalTicks, delta int) {
		low := 5 * tick
		high := totalTicks*tick - low + delta
		if high < 1 {
			high = 1
		}
		pushLevel(lo, low)
		pushLevel(hi, high)
	}
	pushLevel(hi, tick*8) // idle-high lead
	for _, nibs := range frames {
		pulse(56, 0) // SYNC/calibration (no jitter)
		for i, v := range nibs {
			d := 0
			if jit > 0 {
				d = (i%3 - 1) * jit // cycles -jit, 0, +jit
			}
			pulse(12+v, d)
		}
		if pauseTicks > 0 {
			pulse(pauseTicks, 0)
		}
	}
	// Close the final pulse with a trailing falling edge, then return to idle.
	pushLevel(lo, 5*tick)
	pushLevel(hi, tick*8)
	return w
}

func TestDecodeSENTRoundTrip(t *testing.T) {
	// status + 6 data + CRC. The CRC nibble is emitted, not verified.
	nibs := []int{0x1, 0xA, 0x5, 0xF, 0x0, 0xC, 0x3, 0}
	nibs[7] = sentCRC4(nibs[1:7]) // real J2716 CRC-4 over the 6 data nibbles
	w := sentWave([][]int{nibs}, 6, 0, 0)
	r := DecodeSENT(w, 1e-6, SENTCfg{Nibbles: 8})
	if !r.OK {
		t.Fatalf("SENT decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", nibs) {
		t.Errorf("SENT nibbles: got %v want %v", r.Bytes, nibs)
	}
	if len(r.Spans) < 2 || r.Spans[0].Kind != "sync" {
		t.Fatalf("expected a leading sync span, got %+v", r.Spans)
	}
	if last := r.Spans[len(r.Spans)-1]; last.Kind != "crc" || last.Val != nibs[7] {
		t.Errorf("expected trailing crc nibble %X, got kind=%q val=%d", nibs[7], last.Kind, last.Val)
	}
}

func TestDecodeSENTTickOverride(t *testing.T) {
	nibs := []int{0x2, 0x3, 0x4, 0x5, 0x6, 0x7, 0x8, 0}
	nibs[7] = sentCRC4(nibs[1:7])
	tick := 8
	colTimeS := 5e-7
	w := sentWave([][]int{nibs}, tick, 0, 0)
	// TickNs so that tickNs*1e-9/colTimeS == tick samples exactly.
	tickNs := float64(tick) * colTimeS * 1e9
	r := DecodeSENT(w, colTimeS, SENTCfg{TickNs: tickNs, Nibbles: 8})
	if !r.OK {
		t.Fatalf("SENT (tick override) decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", nibs) {
		t.Errorf("SENT override nibbles: got %v want %v", r.Bytes, nibs)
	}
}

func TestDecodeSENTJitter(t *testing.T) {
	// ±(tick/4)-sample jitter on each nibble must still round to the right value.
	nibs := []int{0xF, 0x0, 0x8, 0x1, 0x7, 0xE, 0x2, 0}
	nibs[7] = sentCRC4(nibs[1:7])
	tick := 12
	w := sentWave([][]int{nibs}, tick, 0, tick/4)
	r := DecodeSENT(w, 1e-6, SENTCfg{Nibbles: 8})
	if !r.OK {
		t.Fatalf("SENT (jitter) decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", nibs) {
		t.Errorf("SENT jitter nibbles: got %v want %v", r.Bytes, nibs)
	}
}

func TestDecodeSENTMultiFramePause(t *testing.T) {
	fa := []int{0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0x7, 0}
	fb := []int{0x9, 0xA, 0xB, 0xC, 0xD, 0xE, 0xF, 0}
	fa[7] = sentCRC4(fa[1:7])
	fb[7] = sentCRC4(fb[1:7])
	// A 100-tick pause pulse trails each frame; the flag makes the decoder skip it.
	w := sentWave([][]int{fa, fb}, 6, 100, 0)
	r := DecodeSENT(w, 1e-6, SENTCfg{Nibbles: 8, PausePulse: true})
	if !r.OK {
		t.Fatalf("SENT (multi-frame/pause) decode failed: %s", r.Error)
	}
	want := append(append([]int{}, fa...), fb...)
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", want) {
		t.Errorf("SENT multi-frame nibbles: got %v want %v", r.Bytes, want)
	}
	syncs, pauses := 0, 0
	for _, s := range r.Spans {
		switch s.Kind {
		case "sync":
			syncs++
		case "pause":
			pauses++
		}
	}
	if syncs != 2 || pauses != 2 {
		t.Errorf("expected 2 sync + 2 pause spans, got %d sync / %d pause", syncs, pauses)
	}
}

func TestDecodeSENTNoPanic(t *testing.T) {
	// Empty, flat, and pseudo-random inputs (with degenerate configs) must never
	// panic and must never loop unbounded.
	_ = DecodeSENT(nil, 1e-6, SENTCfg{})
	flat := make([]uint8, 500)
	for i := range flat {
		flat[i] = 128
	}
	_ = DecodeSENT(flat, 1e-6, SENTCfg{})

	garbage := make([]uint8, 2000)
	x := uint32(12345)
	for i := range garbage {
		x = x*1664525 + 1013904223
		garbage[i] = uint8(x >> 24)
	}
	_ = DecodeSENT(garbage, 1e-6, SENTCfg{Nibbles: 8, PausePulse: true})
	_ = DecodeSENT(garbage, 0, SENTCfg{TickNs: 1000})        // colTimeS 0 => derive
	_ = DecodeSENT(garbage, 1e-9, SENTCfg{TickNs: 1e12})     // absurd tick override
	_ = DecodeSENT(garbage, 1e-6, SENTCfg{Nibbles: -5})      // clamps to default
	_ = DecodeSENT(garbage, 1e-6, SENTCfg{Nibbles: 1 << 20}) // clamps to 64
}
