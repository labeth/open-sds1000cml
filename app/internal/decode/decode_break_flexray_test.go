package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// ---------------------------------------------------------------------------
// Red-team helpers (br* prefix to avoid clashing with decode_flexray_test.go,
// which already defines flexrayWave/headerNote in this same package).
// ---------------------------------------------------------------------------

// brFlexPad prepends `lead` and appends `trail` samples of `level` — a realistic
// idle GAP so the capture does not start/stop exactly on a frame.
func brFlexPad(w []uint8, lead, trail int, level uint8) []uint8 {
	out := make([]uint8, 0, lead+len(w)+trail)
	for i := 0; i < lead; i++ {
		out = append(out, level)
	}
	out = append(out, w...)
	for i := 0; i < trail; i++ {
		out = append(out, level)
	}
	return out
}

// brFlexFrame builds ONE frame with fully controllable lead/trail idle and TSS
// length so the corruption tests can index individual bits. Layout mirrors
// flexrayWave exactly (idle HIGH; TSS LOW; FSS 1 HIGH; per byte BSS HIGH,LOW +
// 8 data MSB-first; FES LOW,HIGH).
func brFlexFrame(bytes []int, spb, tssBits, leadBits, trailBits int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	var w []uint8
	push := func(bit, k int) {
		v := lo
		if bit == 1 {
			v = hi
		}
		for j := 0; j < k; j++ {
			w = append(w, v)
		}
	}
	push(1, spb*leadBits)
	push(0, spb*tssBits)
	push(1, spb)
	for _, b := range bytes {
		push(1, spb)
		push(0, spb)
		for d := 7; d >= 0; d-- {
			push((b>>d)&1, spb)
		}
	}
	push(0, spb)
	push(1, spb)
	push(1, spb*trailBits)
	return w
}

// brFlexBitOffsetBSS0 returns the bit index (in units of `spb` samples) of byte
// i's BSS bit0 for a frame built by brFlexFrame.
func brFlexBitOffsetBSS0(i, tssBits, leadBits int) int {
	return leadBits + tssBits + 1 + i*10
}

// brFlexSetBit overwrites bit `off` (0-based in bit units) with `level`.
func brFlexSetBit(w []uint8, off, spb int, level uint8) {
	for j := off * spb; j < (off+1)*spb && j < len(w); j++ {
		if j >= 0 {
			w[j] = level
		}
	}
}

// brFlexTwoNoGap builds two frames back-to-back with NO idle between them: the
// first frame's FES HIGH bit runs straight into the second frame's TSS LOW.
func brFlexTwoNoGap(f1, f2 []int, spb, tssBits int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	var w []uint8
	push := func(bit, k int) {
		v := lo
		if bit == 1 {
			v = hi
		}
		for j := 0; j < k; j++ {
			w = append(w, v)
		}
	}
	emit := func(bytes []int) {
		push(0, spb*tssBits) // TSS
		push(1, spb)         // FSS
		for _, b := range bytes {
			push(1, spb)
			push(0, spb)
			for d := 7; d >= 0; d-- {
				push((b>>d)&1, spb)
			}
		}
		push(0, spb) // FES low
		push(1, spb) // FES high
	}
	push(1, spb*8) // lead idle
	emit(f1)
	emit(f2)       // no idle between: FES high of f1 abuts TSS low of f2
	push(1, spb*8) // trail idle
	return w
}

func brFlexEq(got, want []int) bool {
	return fmt.Sprintf("%v", got) == fmt.Sprintf("%v", want)
}

// brFlexHeaderCRC11 computes the FlexRay header CRC-11 (poly 0x385, init 0x1A)
// over the 20 header bits sync(1) startup(1) frameID(11) payloadLen(7) — used to
// build a frame whose header CRC is genuinely CORRECT before we corrupt it.
func brFlexHeaderCRC11(sync, startup, frameID, payloadLen int) int {
	bits := make([]int, 0, 20)
	bits = append(bits, sync&1, startup&1)
	for b := 10; b >= 0; b-- {
		bits = append(bits, (frameID>>b)&1)
	}
	for b := 6; b >= 0; b-- {
		bits = append(bits, (payloadLen>>b)&1)
	}
	crc := 0x1A
	for _, bit := range bits {
		msb := (crc >> 10) & 1
		crc = (crc << 1) & 0x7FF
		if (msb ^ bit) == 1 {
			crc ^= 0x385
		}
	}
	return crc & 0x7FF
}

// brFlexHeaderBytes packs the 40-bit FlexRay header (flags(5) frameID(11)
// payloadLen(7) headerCRC(11) cycle(6), MSB-first) into 5 bytes — matching the
// decoder's own header split (sync=bit36, startup=bit35, crc=bits6..16).
func brFlexHeaderBytes(sync, startup, frameID, payloadLen, crc, cycle int) []int {
	var h uint64
	h |= uint64(sync&1) << 36
	h |= uint64(startup&1) << 35
	h |= uint64(frameID&0x7FF) << 24
	h |= uint64(payloadLen&0x7F) << 17
	h |= uint64(crc&0x7FF) << 6
	h |= uint64(cycle & 0x3F)
	return []int{
		int((h >> 32) & 0xFF), int((h >> 24) & 0xFF), int((h >> 16) & 0xFF),
		int((h >> 8) & 0xFF), int(h & 0xFF),
	}
}

// brFlexFixCRC rewrites the header-CRC field (bits 6..16 of the 40-bit header) of a
// >=5-byte frame so it matches the frame's own sync/startup/frameID/payloadLen,
// turning an otherwise-random header into a genuinely valid FlexRay frame that the
// (now CRC-checking) decoder accepts. Frames shorter than a header are unchanged.
func brFlexFixCRC(fb []int) []int {
	if len(fb) < 5 {
		return fb
	}
	var h uint64
	for i := 0; i < 5; i++ {
		h = (h << 8) | uint64(fb[i]&0xff)
	}
	sync := int((h >> 36) & 1)
	startup := int((h >> 35) & 1)
	frameID := int((h >> 24) & 0x7FF)
	payloadLen := int((h >> 17) & 0x7F)
	crc := brFlexHeaderCRC11(sync, startup, frameID, payloadLen)
	h = (h &^ (uint64(0x7FF) << 6)) | (uint64(crc&0x7FF) << 6)
	fb[0] = int((h >> 32) & 0xFF)
	fb[1] = int((h >> 24) & 0xFF)
	fb[2] = int((h >> 16) & 0xFF)
	fb[3] = int((h >> 8) & 0xFF)
	fb[4] = int(h & 0xFF)
	return fb
}

// brFlexSafe runs DecodeFlexRay under a recover so a panic is reported as a
// finding instead of crashing the whole suite.
func brFlexSafe(t *testing.T, tag string, codes []uint8, ct float64, cfg FlexRayCfg) (r Result, panicked bool) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			panicked = true
			t.Errorf("PANIC [%s]: %v (n=%d ct=%g cfg=%+v)", tag, p, len(codes), ct, cfg)
		}
	}()
	r = DecodeFlexRay(codes, ct, cfg)
	return
}

// ctForExact returns a colTimeS that makes the decoder's sample-per-bit T land
// exactly on spb for the given integer bitrate: T = 1/(bitrate*ct) = spb.
func ctForExact(bitrate, spb int) float64 {
	return 1.0 / (float64(bitrate) * float64(spb))
}

// ===========================================================================
func TestBreakFlexray(t *testing.T) {
	// ------------------------------------------------------------------
	// CLASS 1 — FALSE NEGATIVES: valid frames MUST decode byte-exact.
	// ------------------------------------------------------------------
	t.Run("false_negatives", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0xF1E57))
		const iters = 80
		fails := 0
		for it := 0; it < iters; it++ {
			spb := 6 + rng.Intn(35)    // 6..40 samples/bit (legal: T>=4)
			tssBits := 5 + rng.Intn(8) // 5..12 bit TSS
			nframes := 1 + rng.Intn(2) // 1 or 2 frames
			var frames [][]int
			var want []int
			for f := 0; f < nframes; f++ {
				nb := 5 + rng.Intn(20) // 5..24 bytes (>=5 exercises the header note)
				fb := make([]int, nb)
				for i := range fb {
					fb[i] = rng.Intn(256)
				}
				brFlexFixCRC(fb) // a real FlexRay frame carries a valid header CRC
				frames = append(frames, fb)
				want = append(want, fb...)
			}
			w := flexrayWave(frames, spb, tssBits)
			// random idle gap before/after (clean HIGH == idle level 210)
			w = brFlexPad(w, rng.Intn(4*spb+1), rng.Intn(4*spb+1), 210)

			var cfg FlexRayCfg
			var ct float64
			if it%2 == 0 { // explicit bitrate, T lands exactly on spb
				const br = 10_000_000
				ct = ctForExact(br, spb)
				cfg = FlexRayCfg{Bitrate: br}
			} else { // auto-infer the bit period
				ct = 1e-8
				cfg = FlexRayCfg{}
			}
			r, pan := brFlexSafe(t, "fn", w, ct, cfg)
			if pan {
				continue
			}
			if !r.OK {
				fails++
				t.Errorf("FALSE-NEGATIVE it=%d spb=%d tss=%d nf=%d: ok=false err=%q want=%v",
					it, spb, tssBits, nframes, r.Error, want)
				continue
			}
			if !brFlexEq(r.Bytes, want) {
				fails++
				t.Errorf("FALSE-NEGATIVE it=%d spb=%d tss=%d nf=%d: bytes=%v want=%v",
					it, spb, tssBits, nframes, r.Bytes, want)
			}
		}
		if fails == 0 {
			t.Logf("false_negatives: %d valid frames all decoded byte-exact", iters)
		}
	})

	// ------------------------------------------------------------------
	// CLASS 2 — FALSE POSITIVES: non-frames / corrupted frames must NOT be
	// confidently decoded. FlexRay-here validates only framing (TSS+BSS), not
	// the header CRC, so per the brief this is treated as a checksum-less shape:
	//   * flat/DC, ramp, Nyquist, wrong-protocol square  => no frame at all
	//   * a too-short TSS (coding violation)             => not the intact frame
	//   * a flipped BSS bit / a mid-frame truncation      => not the intact frame
	// ------------------------------------------------------------------
	t.Run("false_positives", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0xBADF1E))
		strict := 0 // count of strictly-asserted adversarial inputs

		// -- deterministic non-signals (each MUST yield ok=false) --
		nonSignal := func(tag string, w []uint8, cfg FlexRayCfg) {
			strict++
			r, pan := brFlexSafe(t, tag, w, 1e-8, cfg)
			if pan {
				return
			}
			if r.OK {
				t.Errorf("FALSE-POSITIVE [%s]: decoded a frame from a non-signal: bytes=%v", tag, r.Bytes)
			}
		}

		// flat / DC at several levels
		for k := 0; k < 15; k++ {
			lvl := uint8(rng.Intn(256))
			w := make([]uint8, 800+rng.Intn(1200))
			for i := range w {
				w[i] = lvl
			}
			nonSignal("flat", w, FlexRayCfg{})
		}
		// slow monotonic ramp (single threshold crossing)
		for k := 0; k < 10; k++ {
			n := 600 + rng.Intn(1400)
			w := make([]uint8, n)
			for i := range w {
				w[i] = uint8(i * 255 / (n - 1))
			}
			nonSignal("ramp", w, FlexRayCfg{})
		}
		// Nyquist toggle (alternate every sample)
		for k := 0; k < 10; k++ {
			n := 500 + rng.Intn(1500)
			w := make([]uint8, n)
			for i := range w {
				if i%2 == 0 {
					w[i] = 210
				} else {
					w[i] = 40
				}
			}
			nonSignal("nyquist", w, FlexRayCfg{})
		}
		// wrong-protocol 50% square wave (no LOW run >= 4T)
		for k := 0; k < 10; k++ {
			half := 6 + rng.Intn(30)
			n := 40 * half
			w := make([]uint8, n)
			for i := range w {
				if (i/half)%2 == 0 {
					w[i] = 210
				} else {
					w[i] = 40
				}
			}
			nonSignal("square", w, FlexRayCfg{})
		}
		// too-short TSS (1..3 LOW bits): a coding violation, must not decode as the frame
		for k := 0; k < 10; k++ {
			spb := 8 + rng.Intn(16)
			tss := 1 + rng.Intn(3) // 1..3  (< the 4-bit minimum)
			nb := 5 + rng.Intn(6)
			fb := make([]int, nb)
			for i := range fb {
				fb[i] = rng.Intn(256)
			}
			w := brFlexFrame(fb, spb, tss, 8, 8)
			strict++
			r, pan := brFlexSafe(t, "shortTSS", w, ctForExact(10_000_000, spb), FlexRayCfg{Bitrate: 10_000_000})
			if pan {
				continue
			}
			if r.OK && brFlexEq(r.Bytes, fb) {
				t.Errorf("FALSE-POSITIVE [shortTSS=%d]: accepted a frame with a sub-minimum TSS: %v", tss, fb)
			}
		}
		// corrupted framing: flip a BSS bit inside a valid frame — must NOT
		// reproduce the intact byte sequence.
		for k := 0; k < 20; k++ {
			spb := 10 + rng.Intn(14)
			tss := 8
			nb := 6 + rng.Intn(8)
			fb := make([]int, nb)
			for i := range fb {
				fb[i] = rng.Intn(256)
			}
			w := brFlexFrame(fb, spb, tss, 8, 8)
			victim := 1 + rng.Intn(nb-1) // not the first byte, so >=1 byte survives
			off := brFlexBitOffsetBSS0(victim, tss, 8)
			if k%2 == 0 {
				brFlexSetBit(w, off, spb, 40) // BSS bit0 HIGH->LOW
			} else {
				brFlexSetBit(w, off+1, spb, 210) // BSS bit1 LOW->HIGH
			}
			strict++
			r, pan := brFlexSafe(t, "badBSS", w, ctForExact(10_000_000, spb), FlexRayCfg{Bitrate: 10_000_000})
			if pan {
				continue
			}
			if r.OK && brFlexEq(r.Bytes, fb) {
				t.Errorf("FALSE-POSITIVE [badBSS byte=%d]: corrupted framing decoded as the intact frame %v", victim, fb)
			}
		}
		// truncation mid-frame: cut inside a non-final byte — the intact frame
		// must not come back.
		for k := 0; k < 10; k++ {
			spb := 10 + rng.Intn(14)
			tss := 8
			nb := 6 + rng.Intn(8)
			fb := make([]int, nb)
			for i := range fb {
				fb[i] = rng.Intn(256)
			}
			w := brFlexFrame(fb, spb, tss, 8, 8)
			// cut so at least the final 2 bytes are lost
			cutByte := nb - 2
			cutBit := brFlexBitOffsetBSS0(cutByte, tss, 8) + 4 // mid-byte
			cut := cutBit * spb
			if cut > len(w) {
				cut = len(w)
			}
			trunc := w[:cut]
			strict++
			r, pan := brFlexSafe(t, "trunc", trunc, ctForExact(10_000_000, spb), FlexRayCfg{Bitrate: 10_000_000})
			if pan {
				continue
			}
			if r.OK && brFlexEq(r.Bytes, fb) {
				t.Errorf("FALSE-POSITIVE [trunc]: truncated frame decoded as the intact frame %v", fb)
			}
		}

		// -- pure random noise: no CRC to lean on, so a stray byte-run is
		// inherent; we only require it never panics and record the rate. --
		noiseOK := 0
		for k := 0; k < 20; k++ {
			n := 400 + rng.Intn(3000)
			w := make([]uint8, n)
			for i := range w {
				w[i] = uint8(rng.Intn(256))
			}
			cfg := FlexRayCfg{}
			if k%2 == 0 {
				cfg = FlexRayCfg{Bitrate: 10_000_000}
			}
			r, pan := brFlexSafe(t, "noise", w, 1e-8, cfg)
			if pan {
				continue
			}
			if r.OK {
				noiseOK++
			}
		}
		t.Logf("false_positives: %d strict adversarial inputs asserted; %d/20 noise inputs spuriously OK (inherent for a CRC-less byte extractor)", strict, noiseOK)
		if strict < 50 {
			t.Fatalf("class-2 sanity: only %d strict adversarial inputs (<50)", strict)
		}
	})

	// ------------------------------------------------------------------
	// CLASS 3 — EDGE CASES: boundaries must not panic and behave sanely.
	// ------------------------------------------------------------------
	t.Run("edge_cases", func(t *testing.T) {
		// boundary sample counts + degenerate configs (must not panic).
		degenerate := [][]uint8{
			nil, {}, {200}, {40, 210}, {210, 210, 210},
			make([]uint8, 7), // < 8 valid samples
		}
		cts := []float64{0, -1, 1e-12, 1e-8, 1e6}
		brs := []int{0, -100, 1, 10_000_000, 1 << 30}
		for _, w := range degenerate {
			for _, ct := range cts {
				for _, br := range brs {
					r, pan := brFlexSafe(t, "degenerate", w, ct, FlexRayCfg{Bitrate: br})
					if pan {
						continue
					}
					if r.OK && len(r.Bytes) == 0 {
						t.Errorf("EDGE: ok=true with zero bytes on degenerate input (n=%d)", len(w))
					}
					if !r.OK && r.Error == "" {
						t.Errorf("EDGE: !ok but empty Error on degenerate input (n=%d)", len(w))
					}
				}
			}
		}

		// minimum legal bit rate: spb == 4 (T == minSPB).
		{
			spb, tss := 4, 8
			fb := brFlexFixCRC([]int{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC})
			w := brFlexFrame(fb, spb, tss, 8, 8)
			r, pan := brFlexSafe(t, "min-spb-explicit", w, ctForExact(10_000_000, spb), FlexRayCfg{Bitrate: 10_000_000})
			if !pan {
				if !r.OK {
					t.Errorf("EDGE min spb=4 (explicit): ok=false err=%q", r.Error)
				} else if !brFlexEq(r.Bytes, fb) {
					t.Errorf("EDGE min spb=4 (explicit): bytes=%v want=%v", r.Bytes, fb)
				}
			}
			ra, pan2 := brFlexSafe(t, "min-spb-auto", w, 1e-8, FlexRayCfg{})
			if !pan2 && ra.OK && !brFlexEq(ra.Bytes, fb) {
				t.Errorf("EDGE min spb=4 (auto): bytes=%v want=%v", ra.Bytes, fb)
			}
		}

		// maximum legal bit rate span: very slow bit clock (large spb).
		{
			spb, tss := 200, 6
			fb := brFlexFixCRC([]int{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0xFF})
			w := brFlexFrame(fb, spb, tss, 4, 4)
			r, pan := brFlexSafe(t, "max-spb", w, ctForExact(1_000_000, spb), FlexRayCfg{Bitrate: 1_000_000})
			if !pan {
				if !r.OK {
					t.Errorf("EDGE large spb=200: ok=false err=%q", r.Error)
				} else if !brFlexEq(r.Bytes, fb) {
					t.Errorf("EDGE large spb=200: bytes=%v want=%v", r.Bytes, fb)
				}
			}
		}

		// exactly one frame, tight lead/trail.
		{
			spb, tss := 16, 6
			fb := brFlexFixCRC([]int{0x81, 0x02, 0x03, 0x04, 0x05, 0xAA})
			w := brFlexFrame(fb, spb, tss, 1, 1)
			r, pan := brFlexSafe(t, "one-frame", w, ctForExact(10_000_000, spb), FlexRayCfg{Bitrate: 10_000_000})
			if !pan && (!r.OK || !brFlexEq(r.Bytes, fb)) {
				t.Errorf("EDGE one-frame: ok=%v bytes=%v want=%v err=%q", r.OK, r.Bytes, fb, r.Error)
			}
		}

		// back-to-back frames with NO idle gap between them.
		{
			spb, tss := 16, 6
			f1 := brFlexFixCRC([]int{0x11, 0x22, 0x33, 0x44, 0x55, 0x66})
			f2 := brFlexFixCRC([]int{0x77, 0x88, 0x99, 0xAA, 0xBB, 0xCC, 0xDD})
			w := brFlexTwoNoGap(f1, f2, spb, tss)
			want := append(append([]int{}, f1...), f2...)
			r, pan := brFlexSafe(t, "no-gap", w, ctForExact(10_000_000, spb), FlexRayCfg{Bitrate: 10_000_000})
			if !pan {
				if !r.OK {
					t.Errorf("EDGE no-gap back-to-back: ok=false err=%q", r.Error)
				} else if !brFlexEq(r.Bytes, want) {
					t.Errorf("EDGE no-gap back-to-back: bytes=%v want=%v", r.Bytes, want)
				}
			}
		}

		// all-0x00 and all-0xFF payloads.
		for _, fill := range []int{0x00, 0xFF} {
			spb, tss := 16, 8
			fb := make([]int, 9)
			for i := range fb {
				fb[i] = fill
			}
			brFlexFixCRC(fb) // valid header CRC over the fill bytes
			w := brFlexFrame(fb, spb, tss, 8, 8)
			r, pan := brFlexSafe(t, fmt.Sprintf("fill%02X-explicit", fill), w, ctForExact(10_000_000, spb), FlexRayCfg{Bitrate: 10_000_000})
			if !pan && (!r.OK || !brFlexEq(r.Bytes, fb)) {
				t.Errorf("EDGE all-0x%02X (explicit): ok=%v bytes=%v want=%v err=%q", fill, r.OK, r.Bytes, fb, r.Error)
			}
			ra, pan2 := brFlexSafe(t, fmt.Sprintf("fill%02X-auto", fill), w, 1e-8, FlexRayCfg{})
			if !pan2 && ra.OK && !brFlexEq(ra.Bytes, fb) {
				t.Errorf("EDGE all-0x%02X (auto): bytes=%v want=%v", fill, ra.Bytes, fb)
			}
		}

		// very long record: many frames back-to-back-ish, must decode all.
		{
			spb, tss := 8, 6
			var frames [][]int
			var want []int
			for f := 0; f < 40; f++ {
				fb := brFlexFixCRC([]int{f & 0xff, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55})
				frames = append(frames, fb)
				want = append(want, fb...)
			}
			w := flexrayWave(frames, spb, tss)
			r, pan := brFlexSafe(t, "long", w, ctForExact(10_000_000, spb), FlexRayCfg{Bitrate: 10_000_000})
			if !pan {
				if !r.OK {
					t.Errorf("EDGE long record: ok=false err=%q", r.Error)
				} else if !brFlexEq(r.Bytes, want) {
					t.Errorf("EDGE long record: got %d bytes want %d", len(r.Bytes), len(want))
				}
			}
		}
	})

	// ------------------------------------------------------------------
	// INTEGRITY — the one confirmed FALSE-POSITIVE gap.
	//
	// DecodeFlexRay does NO CRC/parity validation: Result.OK reflects only the
	// TSS/BSS *framing*. A frame with a genuinely correct 11-bit header CRC that
	// is then corrupted in exactly one CRC-field bit is still returned OK, with
	// the corrupted header silently decoded. The red-team brief requires a
	// corrupted CRC to be flagged, so this is reported as a false-positive bug.
	//
	// KNOWN BUG — guarded by reportCRCGap so the integrator can flip it to false
	// if they judge CRC validation out of scope for this single-line
	// byte-extractor; the rest of TestBreakFlexray is unaffected either way
	// (t.Errorf does not abort sibling assertions).
	// ------------------------------------------------------------------
	t.Run("integrity_no_crc", func(t *testing.T) {
		const reportCRCGap = true // set false to treat missing-CRC as out-of-scope
		rng := rand.New(rand.NewSource(0xC12C))
		accepted := 0
		const trials = 24
		for trial := 0; trial < trials; trial++ {
			sync := rng.Intn(2)
			startup := rng.Intn(2)
			frameID := rng.Intn(0x800)
			payloadLen := 1 + rng.Intn(10)
			cycle := rng.Intn(0x40)
			crc := brFlexHeaderCRC11(sync, startup, frameID, payloadLen)
			hdr := brFlexHeaderBytes(sync, startup, frameID, payloadLen, crc, cycle)
			payload := make([]int, payloadLen*2)
			for i := range payload {
				payload[i] = rng.Intn(256)
			}
			good := append(append([]int{}, hdr...), payload...)

			spb := 12
			wGood := brFlexFrame(good, spb, 8, 8, 8)
			rGood, pan := brFlexSafe(t, "crc-good", wGood, ctForExact(10_000_000, spb), FlexRayCfg{Bitrate: 10_000_000})
			if pan {
				continue
			}
			if !rGood.OK || !brFlexEq(rGood.Bytes, good) {
				t.Fatalf("baseline (correct-CRC) frame failed to decode: ok=%v got=%v want=%v", rGood.OK, rGood.Bytes, good)
			}
			// Flip a single bit that lives inside the 11-bit header-CRC field
			// (crc = header bits 6..16; header byte index 3 = bits 8..15, all CRC).
			bad := append([]int{}, good...)
			bad[3] ^= 0x40
			wBad := brFlexFrame(bad, spb, 8, 8, 8)
			rBad, pan2 := brFlexSafe(t, "crc-bad", wBad, ctForExact(10_000_000, spb), FlexRayCfg{Bitrate: 10_000_000})
			if pan2 {
				continue
			}
			if rBad.OK {
				accepted++
			}
		}
		if accepted > 0 {
			msg := fmt.Sprintf("FALSE-POSITIVE (integrity): DecodeFlexRay accepted %d/%d frames whose header CRC bit was deliberately corrupted (OK=true, no CRC/parity check). A corrupted CRC must be flagged.", accepted, trials)
			if reportCRCGap {
				t.Errorf("%s", msg)
			} else {
				t.Logf("known-gap (unreported): %s", msg)
			}
		}
	})
}