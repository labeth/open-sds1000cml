package decode

import (
	"math"
	"math/rand"
	"testing"
)

// ----------------------------------------------------------------------------
// Red-team harness for DecodeUART. Three attack classes, each >= 50 seeded
// iterations:
//   1. FALSE NEGATIVES — fully valid frames that must round-trip exactly.
//   2. FALSE POSITIVES — noise / DC / ramp / corrupted framing must not be
//      confirmed as a clean frame.
//   3. EDGE CASES — boundary bit rates, degenerate configs, no panics.
//
// The decoder is LSB-first, 8N1-style, and has NO MSB/LSB config flag, so all
// synthesized frames are LSB-first. Data bits 1..16 and none/even/odd parity
// are exercised.
// ----------------------------------------------------------------------------

// buExpParity mirrors decode_uart.go parityOf(): the expected parity BIT value.
func buExpParity(val, bits int, parity string) int {
	m := val & ((1 << uint(bits)) - 1)
	p := 0
	for m != 0 {
		p ^= m & 1
		m >>= 1
	}
	if parity == "even" {
		return p
	}
	return 1 - p // odd
}

// buBuildSeq builds the ordered bit sequence of a UART capture in BIT-TIME
// units: lead idle-high bits, then for each byte { start(0), data LSB-first,
// optional parity, stop(1) } separated by `gap` idle bits, then trail idle.
func buBuildSeq(bytes []int, bits int, parity string, lead, trail, gap int) []int {
	var seq []int
	for i := 0; i < lead; i++ {
		seq = append(seq, 1)
	}
	for bi, b := range bytes {
		if bi > 0 {
			for i := 0; i < gap; i++ {
				seq = append(seq, 1)
			}
		}
		seq = append(seq, 0) // start
		for c := 0; c < bits; c++ {
			seq = append(seq, (b>>uint(c))&1) // LSB first
		}
		if parity != "none" && parity != "" {
			seq = append(seq, buExpParity(b, bits, parity))
		}
		seq = append(seq, 1) // stop
	}
	for i := 0; i < trail; i++ {
		seq = append(seq, 1)
	}
	return seq
}

// buRasterize samples the bit sequence at (possibly fractional) spb samples/bit,
// with `frontPad` extra idle-high samples up front (a capture rarely starts on
// a bit boundary). High level -> 210, low -> 40 (matches the repo synthesizer).
func buRasterize(seq []int, spb float64, frontPad int) []uint8 {
	const lo, hi = 40, 210
	total := frontPad + int(math.Round(float64(len(seq))*spb))
	if total < 0 {
		total = 0
	}
	w := make([]uint8, total)
	for j := 0; j < total; j++ {
		b := 1
		if j >= frontPad && len(seq) > 0 {
			idx := int(math.Floor(float64(j-frontPad) / spb))
			if idx < 0 {
				idx = 0
			}
			if idx >= len(seq) {
				idx = len(seq) - 1
			}
			b = seq[idx]
		}
		if b == 1 {
			w[j] = hi
		} else {
			w[j] = lo
		}
	}
	return w
}

func buEq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func buCountKinds(spans []Span) (frameErr, parityErr, gap int) {
	for _, s := range spans {
		switch s.Kind {
		case "frame-error":
			frameErr++
		case "parity-error":
			parityErr++
		case "gap":
			gap++
		}
	}
	return
}

func TestBreakUart(t *testing.T) {
	rng := rand.New(rand.NewSource(0xBEEF))
	const ct = 1e-6

	// ------------------------------------------------------------------
	// CLASS 1: FALSE NEGATIVES — a valid frame must round-trip exactly.
	// ------------------------------------------------------------------
	fnIter := 80
	for it := 0; it < fnIter; it++ {
		bits := []int{5, 6, 7, 8, 9}[rng.Intn(5)]
		parity := []string{"none", "even", "odd"}[rng.Intn(3)]
		// Target a random (usually fractional) samples/bit across the legal
		// range, then derive the integer baud the decoder will actually use so
		// its recomputed spb matches the synthesis spb exactly.
		spbTarget := 3.3 + rng.Float64()*40.0 // 3.3 .. 43.3
		baud := int(1.0/(spbTarget*ct) + 0.5)
		spb := (1.0 / float64(baud)) / ct
		if spb < 3.05 {
			continue
		}
		nb := 1 + rng.Intn(8)
		bytes := make([]int, nb)
		for i := range bytes {
			bytes[i] = rng.Intn(1 << uint(bits))
		}
		lead := 4 + rng.Intn(20)         // realistic random idle before
		trail := bits + 8 + rng.Intn(20) // ample idle after (so the last stop is verifiable)
		gap := rng.Intn(4)               // single vs back-to-back-ish
		front := rng.Intn(int(spb) + 1)  // sub-bit capture phase
		seq := buBuildSeq(bytes, bits, parity, lead, trail, gap)
		w := buRasterize(seq, spb, front)

		// Half the iterations decode with explicit baud, half auto-infer.
		auto := it%2 == 0 && nb >= 4 // auto-baud needs enough clean bit gaps
		cfg := UARTCfg{Bits: bits, Parity: parity}
		if !auto {
			cfg.Baud = baud
		}
		r := DecodeUART(w, ct, cfg)
		if !r.OK {
			t.Errorf("FALSE-NEGATIVE: valid %s frame not decoded (bits=%d baud=%d spb=%.3f auto=%v): %s",
				parity, bits, baud, spb, auto, r.Error)
			continue
		}
		if !buEq(r.Bytes, bytes) {
			t.Errorf("FALSE-NEGATIVE: payload mismatch (bits=%d par=%s baud=%d spb=%.3f auto=%v)\n  want=%v\n  got =%v",
				bits, parity, baud, spb, auto, bytes, r.Bytes)
			continue
		}
		if fe, pe, _ := buCountKinds(r.Spans); fe != 0 || pe != 0 {
			t.Errorf("FALSE-NEGATIVE: clean %s frame flagged frame-err=%d parity-err=%d (bits=%d baud=%d spb=%.3f)",
				parity, fe, pe, bits, baud, spb)
		}
	}

	// ------------------------------------------------------------------
	// CLASS 2: FALSE POSITIVES — non-frames / corrupted frames must not be
	// confirmed as a clean valid frame.
	// ------------------------------------------------------------------
	fpIter := 66
	for it := 0; it < fpIter; it++ {
		kind := it % 6
		switch kind {
		case 0: // flat / DC: no long clean byte run may be reported.
			lvl := uint8(rng.Intn(256))
			w := make([]uint8, 500+rng.Intn(3000))
			for i := range w {
				w[i] = lvl
			}
			for _, cfg := range []UARTCfg{{}, {Baud: 100000}} {
				r := DecodeUART(w, ct, cfg)
				if len(r.Bytes) != 0 {
					t.Errorf("FALSE-POSITIVE: flat/DC level=%d decoded %d bytes %v", lvl, len(r.Bytes), r.Bytes)
				}
			}
		case 1: // slow ramp: at most one threshold crossing -> no frame.
			n := 500 + rng.Intn(3000)
			w := make([]uint8, n)
			for i := range w {
				w[i] = uint8(i * 255 / (n - 1))
			}
			for _, cfg := range []UARTCfg{{}, {Baud: 50000}} {
				r := DecodeUART(w, ct, cfg)
				if len(r.Bytes) != 0 {
					t.Errorf("FALSE-POSITIVE: ramp decoded %d bytes %v", len(r.Bytes), r.Bytes)
				}
			}
		case 2: // Nyquist toggle: spb ~ 1, below the 3.0 floor.
			n := 1000 + rng.Intn(2000)
			w := make([]uint8, n)
			for i := range w {
				w[i] = uint8((i % 2) * 255)
			}
			r := DecodeUART(w, ct, UARTCfg{}) // auto: gaps of 1 are dropped
			if len(r.Bytes) != 0 {
				t.Errorf("FALSE-POSITIVE: Nyquist toggle auto-decoded %d bytes %v", len(r.Bytes), r.Bytes)
			}
		case 3: // corrupted STOP bit (full frame, ample trail) -> frame-error.
			bits := 8
			baud := 50000 + rng.Intn(80000)
			spb := (1.0 / float64(baud)) / ct
			val := rng.Intn(256)
			seq := buBuildSeq([]int{val}, bits, "none", 6, bits+16, 0)
			seq[6+1+bits] = 0 // stop -> low
			w := buRasterize(seq, spb, rng.Intn(3))
			r := DecodeUART(w, ct, UARTCfg{Baud: baud, Bits: bits})
			fe, _, _ := buCountKinds(r.Spans)
			if len(r.Bytes) > 0 && fe == 0 {
				t.Errorf("FALSE-POSITIVE: corrupted stop bit accepted as clean data (val=%02X baud=%d) spans=%v", val, baud, r.Spans)
			}
		case 4: // corrupted PARITY bit -> parity-error.
			bits := 8
			parity := []string{"even", "odd"}[rng.Intn(2)]
			baud := 50000 + rng.Intn(80000)
			spb := (1.0 / float64(baud)) / ct
			val := rng.Intn(256)
			seq := buBuildSeq([]int{val}, bits, parity, 6, bits+16, 0)
			seq[6+1+bits] ^= 1 // flip the parity bit
			w := buRasterize(seq, spb, rng.Intn(3))
			r := DecodeUART(w, ct, UARTCfg{Baud: baud, Bits: bits, Parity: parity})
			_, pe, _ := buCountKinds(r.Spans)
			if len(r.Bytes) > 0 && pe == 0 {
				t.Errorf("FALSE-POSITIVE: corrupted %s parity accepted (val=%02X baud=%d) spans=%v", parity, val, baud, r.Spans)
			}
		case 5: // single data-bit corruption in a parity frame -> parity-error.
			bits := 8
			parity := []string{"even", "odd"}[rng.Intn(2)]
			baud := 50000 + rng.Intn(80000)
			spb := (1.0 / float64(baud)) / ct
			val := rng.Intn(256)
			seq := buBuildSeq([]int{val}, bits, parity, 6, bits+16, 0)
			fb := rng.Intn(bits)
			seq[6+1+fb] ^= 1 // flip one data bit -> parity must now mismatch
			w := buRasterize(seq, spb, rng.Intn(3))
			r := DecodeUART(w, ct, UARTCfg{Baud: baud, Bits: bits, Parity: parity})
			_, pe, _ := buCountKinds(r.Spans)
			if len(r.Bytes) > 0 && pe == 0 {
				t.Errorf("FALSE-POSITIVE: single-bit data error not caught by %s parity (val=%02X baud=%d) spans=%v", parity, val, baud, r.Spans)
			}
		}
	}

	// ------------------------------------------------------------------
	// CLASS 2b (KNOWN BUG): corrupted STOP on a truncated PARITY frame.
	//
	// decode_uart.go computes  need := ceil((bits+2)*spb)  which does NOT
	// reserve room for the parity bit. A parity frame occupies bits+3 bit-times
	// (start+data+parity+stop), so the loop can start the final byte with only
	// enough samples for start+data+stop-without-parity. The stop-bit sample
	// then lands at index >= n, logicAt() returns -1, and the guard
	// `if sb >= 0 && sb != idle` SKIPS the framing check — a corrupted (or
	// simply unseen) stop bit is accepted as clean data. This is a genuine
	// FALSE POSITIVE: a corrupted frame is confirmed as valid.
	//
	// The assertion below encodes CORRECT behavior, so it fails until the
	// decoder is fixed (t.Errorf, not Fatalf, so the rest of the suite runs).
	// Fix: reserve the parity bit in `need`, or treat sb<0 as frame-error/gap.
	{
		bugHits := 0
		for _, parity := range []string{"even", "odd"} {
			for _, baud := range []int{60000, 90000, 120000} {
				spb := (1.0 / float64(baud)) / ct
				bits := 8
				val := 0x55
				seq := buBuildSeq([]int{val}, bits, parity, 6, 0, 0)
				seq[len(seq)-1] = 0 // corrupt the stop bit (framing violation)
				full := buRasterize(seq, spb, 0)
				// Truncate ~half a bit so the stop sample index >= n.
				cut := int(spb/2) + 1
				if cut >= len(full) {
					continue
				}
				trunc := full[:len(full)-cut]
				r := DecodeUART(trunc, ct, UARTCfg{Baud: baud, Bits: bits, Parity: parity})
				fe, pe, _ := buCountKinds(r.Spans)
				if len(r.Bytes) > 0 && fe == 0 && pe == 0 {
					bugHits++
				}
			}
		}
		if bugHits > 0 {
			t.Errorf("FALSE-POSITIVE (KNOWN BUG): corrupted stop bit on a truncated parity frame "+
				"accepted as clean data in %d/6 cases — `need` in decode_uart.go omits the parity "+
				"bit, so the out-of-range stop sample (logicAt=-1) skips the framing check", bugHits)
		}
	}

	// ------------------------------------------------------------------
	// CLASS 3: EDGE CASES — boundaries + degenerate configs, no panics.
	// ------------------------------------------------------------------
	noPanic := func(name string, fn func()) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("PANIC in %s: %v", name, r)
			}
		}()
		fn()
	}

	// Min & max legal samples/bit (min spb=3 = fastest baud; large spb = slowest).
	for _, spbT := range []float64{3.0, 3.05, 500.0} {
		baud := int(1.0/(spbT*ct) + 0.5)
		spb := (1.0 / float64(baud)) / ct
		if spb < 3.0 {
			continue
		}
		want := []int{0x41, 0x55, 0xF0}
		seq := buBuildSeq(want, 8, "none", 6, 24, 1)
		w := buRasterize(seq, spb, 0)
		r := DecodeUART(w, ct, UARTCfg{Baud: baud})
		if !r.OK || !buEq(r.Bytes, want) {
			t.Errorf("EDGE: boundary spb=%.3f baud=%d failed ok=%v got=%v err=%s", spb, baud, r.OK, r.Bytes, r.Error)
		}
	}

	// Exactly one frame.
	{
		seq := buBuildSeq([]int{0x3C}, 8, "none", 6, 20, 0)
		w := buRasterize(seq, 10, 0)
		r := DecodeUART(w, ct, UARTCfg{Baud: 100000})
		if !r.OK || !buEq(r.Bytes, []int{0x3C}) {
			t.Errorf("EDGE: single frame ok=%v got=%v err=%s", r.OK, r.Bytes, r.Error)
		}
	}

	// Back-to-back frames with no inter-byte gap (only the stop bit separates).
	{
		want := make([]int, 40)
		for i := range want {
			want[i] = rng.Intn(256)
		}
		seq := buBuildSeq(want, 8, "none", 6, 20, 0) // gap=0
		w := buRasterize(seq, 8.681, 2)              // fractional spb, resync each start
		r := DecodeUART(w, ct, UARTCfg{Baud: 115200})
		if !r.OK || !buEq(r.Bytes, want) {
			t.Errorf("EDGE: back-to-back 40 bytes ok=%v got %d bytes (want %d)", r.OK, len(r.Bytes), len(want))
		}
	}

	// All-0x00 and all-0xFF payloads (explicit baud — auto-baud is inherently
	// ambiguous on runs with no bit transitions).
	for _, fill := range []int{0x00, 0xFF} {
		want := []int{fill, fill, fill, fill}
		seq := buBuildSeq(want, 8, "none", 6, 24, 1)
		w := buRasterize(seq, 12, 0)
		r := DecodeUART(w, ct, UARTCfg{Baud: int(math.Round(1.0/(12*ct)))})
		if !r.OK || !buEq(r.Bytes, want) {
			t.Errorf("EDGE: all-0x%02X payload ok=%v got=%v err=%s", fill, r.OK, r.Bytes, r.Error)
		}
	}

	// bits = 1 (min) and bits = 16 (max) legal data widths.
	for _, bits := range []int{1, 16} {
		want := make([]int, 4)
		for i := range want {
			want[i] = rng.Intn(1<<uint(bits)) & ((1 << uint(bits)) - 1)
		}
		seq := buBuildSeq(want, bits, "none", 6, bits+20, 1)
		w := buRasterize(seq, 12, 0)
		r := DecodeUART(w, ct, UARTCfg{Baud: int(math.Round(1.0/(12*ct))), Bits: bits})
		if !r.OK || !buEq(r.Bytes, want) {
			t.Errorf("EDGE: bits=%d ok=%v want=%v got=%v err=%s", bits, r.OK, want, r.Bytes, r.Error)
		}
	}

	// Shortest and longest records.
	{
		short := buRasterize(buBuildSeq([]int{0x5A}, 8, "none", 4, 4, 0), 4, 0)
		noPanic("short-record", func() { DecodeUART(short, ct, UARTCfg{Baud: 250000}) })
		long := make([]int, 400)
		for i := range long {
			long[i] = rng.Intn(256)
		}
		lw := buRasterize(buBuildSeq(long, 8, "none", 6, 24, 0), 10, 0)
		r := DecodeUART(lw, ct, UARTCfg{Baud: 100000})
		if !r.OK || !buEq(r.Bytes, long) {
			t.Errorf("EDGE: long 400-byte record ok=%v got %d bytes", r.OK, len(r.Bytes))
		}
	}

	// Boundary sample counts 0,1,2,3 must not panic and must not invent bytes.
	for _, n := range []int{0, 1, 2, 3} {
		w := make([]uint8, n)
		for i := range w {
			w[i] = uint8(rng.Intn(256))
		}
		noPanic("tiny-auto", func() {
			r := DecodeUART(w, ct, UARTCfg{})
			if len(r.Bytes) != 0 {
				t.Errorf("EDGE: %d-sample input decoded %d bytes", n, len(r.Bytes))
			}
		})
		noPanic("tiny-explicit", func() { DecodeUART(w, ct, UARTCfg{Baud: 100000}) })
	}

	// Huge / negative / zero colTimeS and baud, out-of-range bits: no panic.
	base := buRasterize(buBuildSeq([]int{0x55, 0xAA, 0x0F}, 8, "none", 6, 24, 0), 10, 0)
	for _, cts := range []float64{0, -1e-6, 1e-30, 1e30} {
		noPanic("ct-explicit", func() { DecodeUART(base, cts, UARTCfg{Baud: 100000}) })
		noPanic("ct-auto", func() { DecodeUART(base, cts, UARTCfg{}) })
	}
	for _, b := range []int{0, -9600, 1, 115200, 1 << 30} {
		noPanic("baud", func() { DecodeUART(base, ct, UARTCfg{Baud: b}) })
	}
	// Bits==0 is the "unset -> default 8" sentinel (decode_uart.go), so it must
	// decode base as an 8-bit stream; genuinely out-of-range widths (<1 after the
	// default, or >16) must be rejected and never produce bytes.
	{
		r0 := DecodeUART(base, ct, UARTCfg{Bits: 0})
		if !r0.OK || !buEq(r0.Bytes, []int{0x55, 0xAA, 0x0F}) {
			t.Errorf("EDGE: bits=0 (default 8) failed ok=%v got=%v err=%s", r0.OK, r0.Bytes, r0.Error)
		}
	}
	for _, bits := range []int{-1, 17, 64, 1 << 20} {
		noPanic("bits", func() {
			r := DecodeUART(base, ct, UARTCfg{Bits: bits})
			// bits outside 1..16 must be rejected, never decoded.
			if r.OK && len(r.Bytes) > 0 {
				t.Errorf("EDGE: out-of-range bits=%d produced bytes %v", bits, r.Bytes)
			}
		})
	}
	for _, p := range []string{"none", "even", "odd", "x", ""} {
		noPanic("parity", func() { DecodeUART(base, ct, UARTCfg{Parity: p}) })
	}
}