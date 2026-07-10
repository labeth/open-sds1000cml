package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// mBits expands bytes into a bit stream, MSB- or LSB-first, `bits` per byte —
// the same order DecodeManchester packs cells back into bytes.
func mBits(bytes []int, msb bool, bits int) []int {
	var out []int
	for _, b := range bytes {
		if msb {
			for k := bits - 1; k >= 0; k-- {
				out = append(out, (b>>k)&1)
			}
		} else {
			for k := 0; k < bits; k++ {
				out = append(out, (b>>k)&1)
			}
		}
	}
	return out
}

// manchesterWave renders a Manchester bit stream into 0..255 codes at spb
// samples/bit. Each cell is two half-cells: for IEEE 802.3 a '1' is a low->high
// (rising) mid-cell transition and a '0' is high->low; the Thomas convention is
// the opposite. Idle sits high; a cell-boundary transition appears naturally
// wherever adjacent half-cells differ. Mirrors the on-wire signal a 1553/telemetry
// source would produce.
func manchesterWave(bitsSeq []int, ieee bool, spb int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	lvl := func(x int) uint8 {
		if x == 1 {
			return hi
		}
		return lo
	}
	var w []uint8
	for j := 0; j < spb*6; j++ { // lead idle (high)
		w = append(w, hi)
	}
	half := spb / 2
	for _, bit := range bitsSeq {
		first, second := 1-bit, bit // IEEE: rising@mid for a 1 => first half low
		if !ieee {
			first, second = bit, 1-bit // Thomas: inverted
		}
		for j := 0; j < half; j++ {
			w = append(w, lvl(first))
		}
		for j := 0; j < spb-half; j++ {
			w = append(w, lvl(second))
		}
	}
	for j := 0; j < spb*6; j++ { // trail idle (high)
		w = append(w, hi)
	}
	return w
}

func TestDecodeManchesterRoundTrip(t *testing.T) {
	// A short alternating preamble (0xAA => 10101010) then varied data so the
	// phase lock has real transitions to grab.
	want := []int{0xAA, 0xB3, 0x2C, 0x47}
	spb := 40
	colTimeS := 1e-6
	bitrate := int(1.0 / (float64(spb) * colTimeS)) // 25000 bit/s
	w := manchesterWave(mBits(want, true, 8), true /*IEEE*/, spb)

	// explicit bitrate
	r := DecodeManchester(w, colTimeS, ManchesterCfg{Bitrate: bitrate, IEEE: true, MSB: true})
	if !r.OK {
		t.Fatalf("explicit-bitrate decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", want) {
		t.Errorf("explicit bitrate: got %v want %v", r.Bytes, want)
	}
	if r.Proto != "manchester" {
		t.Errorf("proto = %q, want manchester", r.Proto)
	}

	// auto bitrate (infer T from the edge statistics)
	ra := DecodeManchester(w, colTimeS, ManchesterCfg{IEEE: true, MSB: true})
	if !ra.OK {
		t.Fatalf("auto-bitrate decode failed: %s", ra.Error)
	}
	if got := fmt.Sprintf("%v", ra.Bytes); got != fmt.Sprintf("%v", want) {
		t.Errorf("auto bitrate: got %v want %v", ra.Bytes, want)
	}
	if ra.SPB < float64(spb)-10 || ra.SPB > float64(spb)+10 {
		t.Errorf("auto SPB %.1f not near %d", ra.SPB, spb)
	}

	// a preamble/sync span must be flagged (kind "start") from the 0xAA run
	var kinds string
	sawSync := false
	for _, s := range r.Spans {
		kinds += s.Kind + " "
		if s.Kind == "start" && s.Text == "SYNC" {
			sawSync = true
		}
	}
	if !sawSync {
		t.Errorf("expected a SYNC/start span; got kinds: %s", kinds)
	}
	if !containsWord(kinds, "data") {
		t.Errorf("expected data spans; got kinds: %s", kinds)
	}
}

func TestDecodeManchesterThomas(t *testing.T) {
	// Thomas/G.E. convention (IEEE=false): rising@mid = 0, falling = 1.
	want := []int{0x3C, 0xD2, 0x66, 0x99}
	spb := 32
	colTimeS := 2e-7
	bitrate := int(1.0 / (float64(spb) * colTimeS))
	w := manchesterWave(mBits(want, true, 8), false /*Thomas*/, spb)

	r := DecodeManchester(w, colTimeS, ManchesterCfg{Bitrate: bitrate, IEEE: false, MSB: true})
	if !r.OK {
		t.Fatalf("Thomas decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", want) {
		t.Errorf("Thomas: got %v want %v", r.Bytes, want)
	}

	// Decoding the SAME wave under the IEEE mapping must invert every bit
	// (the recovered bytes are the ones-complement of the Thomas bytes).
	ri := DecodeManchester(w, colTimeS, ManchesterCfg{Bitrate: bitrate, IEEE: true, MSB: true})
	if !ri.OK || len(ri.Bytes) != len(want) {
		t.Fatalf("IEEE-on-Thomas decode failed: %s", ri.Error)
	}
	for i := range want {
		if ri.Bytes[i] != (^want[i] & 0xff) {
			t.Errorf("IEEE vs Thomas byte %d: got %02X want %02X", i, ri.Bytes[i], ^want[i]&0xff)
		}
	}
}

func TestDecodeManchesterLSB(t *testing.T) {
	// LSB-first packing must round-trip too.
	want := []int{0x81, 0x2D, 0xF0, 0x0F}
	spb := 30
	colTimeS := 1e-6
	bitrate := int(1.0 / (float64(spb) * colTimeS))
	w := manchesterWave(mBits(want, false, 8), true, spb)
	r := DecodeManchester(w, colTimeS, ManchesterCfg{Bitrate: bitrate, IEEE: true, MSB: false})
	if !r.OK {
		t.Fatalf("LSB decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", want) {
		t.Errorf("LSB: got %v want %v", r.Bytes, want)
	}
}

// TestDecodeManchesterNoPanic feeds degenerate/hostile inputs — a decoder must
// return an error, never panic or hang (the package also runs a broader fuzz).
func TestDecodeManchesterNoPanic(t *testing.T) {
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
		case 3: // fast alternating square (ambiguous phase)
			for i := range s {
				s[i] = uint8((i / 3 % 2) * 255)
			}
		}
		return s
	}
	inputs := [][]uint8{nil, {}, {200}, mk(3, 2)}
	for n := 0; n < 60; n++ {
		inputs = append(inputs, mk(rng.Intn(3000), rng.Intn(4)))
	}
	colTimes := []float64{0, 1e-12, 1e-6, 1}
	bitrates := []int{0, -100, 25000, 1 << 30}
	bitsVals := []int{0, 1, 8, 9, 64}
	for _, in := range inputs {
		for _, ct := range colTimes {
			cfg := ManchesterCfg{
				Bitrate:   bitrates[rng.Intn(len(bitrates))],
				IEEE:      rng.Intn(2) == 0,
				MSB:       rng.Intn(2) == 0,
				Bits:      bitsVals[rng.Intn(len(bitsVals))],
				Format:    []string{"", "hex", "ascii", "both", "garbage"}[rng.Intn(5)],
				Threshold: rng.Float64() * 260,
				HaveThr:   rng.Intn(2) == 0,
			}
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("panic on cfg %+v: %v", cfg, p)
					}
				}()
				r := DecodeManchester(in, ct, cfg)
				// Contract: either OK, or an Error string — never both empty.
				if !r.OK && r.Error == "" {
					t.Fatalf("degenerate input returned neither OK nor Error")
				}
			}()
		}
	}
}
