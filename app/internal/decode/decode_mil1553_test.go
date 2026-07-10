package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// mil1553Wave synthesizes a stream of MIL-STD-1553B words into 0..255 codes at
// spb samples/bit. Each WORD = a 3-bit-time SYNC + 16 Manchester data bits (MSB
// first) + 1 parity bit. 1553 Manchester: a logic 1 is HIGH then LOW (falling
// mid-cell), a logic 0 is LOW then HIGH (rising mid-cell). A command/status sync
// holds HIGH for 1.5 bit-times then LOW for 1.5; a data sync is the inverse.
// Words are transmitted contiguously (no idle between them), as on a real bus.
// The lead idle is set OPPOSITE the first sync's first half so a clean sync-start
// edge always exists; parity[i] is emitted verbatim so a test can corrupt it.
func mil1553Wave(words []int, cmd []bool, parity []int, spb int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	half := spb / 2
	var w []uint8
	push := func(v uint8, n int) {
		for j := 0; j < n; j++ {
			w = append(w, v)
		}
	}
	emitBit := func(bit int) {
		first, second := hi, lo // logic 1: high then low (falling mid)
		if bit == 0 {
			first, second = lo, hi // logic 0: low then high (rising mid)
		}
		push(first, half)
		push(second, spb-half)
	}
	emitSync := func(command bool) {
		first, second := hi, lo // command/status: high 1.5T then low 1.5T
		if !command {
			first, second = lo, hi // data: low then high
		}
		n1 := spb + half // 1.5 bit-times
		push(first, n1)
		push(second, 3*spb-n1)
	}
	lead := lo // opposite the first sync's first half => guaranteed sync-start edge
	if !cmd[0] {
		lead = hi
	}
	push(lead, spb*4)
	for i, wd := range words {
		emitSync(cmd[i])
		for b := 15; b >= 0; b-- { // 16 data bits, MSB first
			emitBit((wd >> b) & 1)
		}
		emitBit(parity[i] & 1)
	}
	push(lo, spb*6) // trail idle
	return w
}

// mil1553OddParity returns the parity bit that makes the total 1-count of the 16
// data bits + parity bit odd (the MIL-STD-1553B rule).
func mil1553OddParity(word int) int { return 1 - (popcount(word) & 1) }

func TestDecodeMIL1553RoundTrip(t *testing.T) {
	words := []int{0x1234, 0xABCD, 0x0F0F} // command word then two data-carrying words
	cmd := []bool{true, false, true}       // command sync, data sync, command sync
	spb := 40
	colTimeS := 1.0 / (float64(spb) * 1e6) // pick col time so the bit rate is exactly 1 Mbit/s
	bitrate := 1_000_000
	par := make([]int, len(words))
	for i, wd := range words {
		par[i] = mil1553OddParity(wd)
	}
	w := mil1553Wave(words, cmd, par, spb)

	// explicit bitrate
	r := DecodeMIL1553(w, colTimeS, MIL1553Cfg{Bitrate: bitrate})
	if !r.OK {
		t.Fatalf("explicit-bitrate decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", words) {
		t.Errorf("explicit bitrate: got %v want %v", r.Bytes, words)
	}
	if r.Proto != "mil1553" {
		t.Errorf("proto = %q, want mil1553", r.Proto)
	}

	// auto bitrate (infer T from the edge statistics)
	ra := DecodeMIL1553(w, colTimeS, MIL1553Cfg{})
	if !ra.OK {
		t.Fatalf("auto-bitrate decode failed: %s", ra.Error)
	}
	if got := fmt.Sprintf("%v", ra.Bytes); got != fmt.Sprintf("%v", words) {
		t.Errorf("auto bitrate: got %v want %v", ra.Bytes, words)
	}
	if ra.SPB < float64(spb)-8 || ra.SPB > float64(spb)+8 {
		t.Errorf("auto SPB %.1f not near %d", ra.SPB, spb)
	}

	// The span stream must carry a command sync, a data sync, and data words, and
	// no frame-error on a clean, correctly-parity'd capture.
	var kinds, syncTexts string
	for _, s := range r.Spans {
		kinds += s.Kind + " "
		if s.Kind == "start" {
			syncTexts += s.Text + " "
		}
		if s.Kind == "frame-error" {
			t.Errorf("unexpected frame-error on a clean capture: %+v", s)
		}
	}
	if !containsWord(kinds, "start") || !containsWord(kinds, "data") {
		t.Errorf("expected start + data spans; got kinds: %s", kinds)
	}
	if !containsWord(syncTexts, "csync") {
		t.Errorf("expected a csync (command) span; got sync texts: %s", syncTexts)
	}
	if !containsWord(syncTexts, "dsync") {
		t.Errorf("expected a dsync (data) span; got sync texts: %s", syncTexts)
	}
}

func TestDecodeMIL1553ParityError(t *testing.T) {
	words := []int{0x1234, 0xABCD, 0x0F0F}
	cmd := []bool{true, false, true}
	spb := 40
	colTimeS := 1.0 / (float64(spb) * 1e6)
	par := make([]int, len(words))
	for i, wd := range words {
		par[i] = mil1553OddParity(wd)
	}
	par[1] ^= 1 // corrupt the parity of the middle (data) word
	w := mil1553Wave(words, cmd, par, spb)

	r := DecodeMIL1553(w, colTimeS, MIL1553Cfg{Bitrate: 1_000_000})
	if !r.OK {
		t.Fatalf("decode failed: %s", r.Error)
	}
	// The word values still recover (the corruption is only in the parity bit).
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", words) {
		t.Errorf("parity-error capture: got %v want %v", r.Bytes, words)
	}
	fe := 0
	for _, s := range r.Spans {
		if s.Kind == "frame-error" {
			fe++
		}
	}
	if fe != 1 {
		t.Errorf("expected exactly 1 frame-error span, got %d", fe)
	}
}

// TestDecodeMIL1553NoPanic feeds degenerate/hostile inputs — a decoder must
// return an error, never panic or hang (the package also runs a broader fuzz).
func TestDecodeMIL1553NoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(1553))
	mk := func(n, kind int) []uint8 {
		s := make([]uint8, n)
		switch kind {
		case 0: // noise
			for i := range s {
				s[i] = uint8(rng.Intn(256))
			}
		case 1: // nyquist toggle
			for i := range s {
				s[i] = uint8(i % 2 * 255)
			}
		case 2: // flat
			for i := range s {
				s[i] = 128
			}
		case 3: // slow-ish alternating square (ambiguous phase)
			for i := range s {
				s[i] = uint8((i / 5 % 2) * 255)
			}
		}
		return s
	}
	inputs := [][]uint8{nil, {}, {200}, mk(3, 2)}
	for n := 0; n < 60; n++ {
		inputs = append(inputs, mk(rng.Intn(3000), rng.Intn(4)))
	}
	colTimes := []float64{0, 1e-12, 1e-6, 1}
	bitrates := []int{0, -100, 1_000_000, 1 << 30}
	for _, in := range inputs {
		for _, ct := range colTimes {
			cfg := MIL1553Cfg{
				Bitrate:   bitrates[rng.Intn(len(bitrates))],
				Threshold: rng.Float64() * 260,
				HaveThr:   rng.Intn(2) == 0,
			}
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("panic on cfg %+v: %v", cfg, p)
					}
				}()
				r := DecodeMIL1553(in, ct, cfg)
				if !r.OK && r.Error == "" { // contract: OK or an Error string, never both empty
					t.Fatalf("degenerate input returned neither OK nor Error")
				}
			}()
		}
	}
}
