package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// ---------------------------------------------------------------------------
// Red-team regression suite for DecodeManchester. Reuses manchesterWave / mBits
// from decode_manchester_test.go (same package). Three attack classes, each with
// >= 50 iterations under a seeded RNG for determinism.
//
// Findings pinned by failing (isolated) assertions, clearly commented [BUG n]:
//   [BUG 1] constant-bit inversion  — an all-same-bit payload whose leading half-
//           cell equals the idle level (high) is decoded as its ONE'S-COMPLEMENT
//           (e.g. IEEE all-0x00 -> 0xFF), OK=true, silently. Real payloads dodge
//           this with a transition-rich preamble; a bare constant frame does not.
//   [BUG 2] coding violation NOT flagged — corrupting one cell (removing its mid
//           transition) often makes the decoder split the frame at that point and
//           silently drop the tail with OK=true and NO frame-error span. Manchester's
//           only integrity check (the coding violation) is thereby bypassed.
//   [BUG 3] auto-bitrate miss at spb=5 — a perfectly valid frame that decodes fine
//           with an explicit bitrate returns ok=false ("no frame") under auto-infer.
// ---------------------------------------------------------------------------

func bkEqual(a []int, b []int) bool {
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

func bkHasKind(r Result, kind string) bool {
	for _, s := range r.Spans {
		if s.Kind == kind {
			return true
		}
	}
	return false
}

// bkAltWord returns the value whose expanded bit stream is [1,0,1,0,...] (length
// `bits`), packed the SAME way DecodeManchester packs cells back into a word. Used
// as a transition-rich preamble so phase lock is unambiguous.
func bkAltWord(bits int, msb bool) int {
	v := 0
	for i := 0; i < bits; i++ {
		bit := 0
		if i%2 == 0 {
			bit = 1
		}
		if msb {
			v = (v << 1) | bit
		} else {
			v |= bit << i
		}
	}
	return v
}

// bkPad returns n samples of the idle (high) level.
func bkPad(n int) []uint8 {
	p := make([]uint8, n)
	for i := range p {
		p[i] = 210
	}
	return p
}

// bkBuild synthesizes a valid single-segment Manchester capture for `want` and
// pads it with `pre`/`post` extra idle-high samples. colTimeS is chosen so the
// bit period T lands on `spb` samples EXACTLY (ct = 1/(spb*bitrate)).
func bkBuild(want []int, ieee, msb bool, bits, spb, bitrate, pre, post int) (w []uint8, ct float64) {
	ct = 1.0 / (float64(spb) * float64(bitrate))
	core := manchesterWave(mBits(want, msb, bits), ieee, spb)
	w = append(append(bkPad(pre), core...), bkPad(post)...)
	return
}

func TestBreakManchester(t *testing.T) {
	// -----------------------------------------------------------------------
	// CLASS 1 — FALSE NEGATIVES: >=50 fully VALID frames must round-trip EXACTLY.
	// Vary payload, spb across the legal range (>=4), bitrate/tick, MSB/LSB,
	// IEEE/Thomas, word width, single vs back-to-back cells, and random idle pad.
	// Each frame leads with a transition-rich alternating word (a real Manchester
	// preamble) so the phase lock is deterministic — the decoder's designed mode.
	// -----------------------------------------------------------------------
	t.Run("FalseNegatives_explicit", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0xA5A5))
		bitrates := []int{9600, 31250, 100000, 250000, 1000000}
		fails := 0
		for it := 0; it < 80; it++ {
			bits := 1 + rng.Intn(16) // 1..16
			ieee := rng.Intn(2) == 0
			msb := rng.Intn(2) == 0
			spb := 4 + rng.Intn(77) // 4..80
			bitrate := bitrates[rng.Intn(len(bitrates))]
			maxv := 1 << bits
			nData := 1 + rng.Intn(6) // 1..6 data words -> single vs multi-word
			want := []int{bkAltWord(bits, msb)}
			for i := 0; i < nData; i++ {
				want = append(want, rng.Intn(maxv))
			}
			// occasionally a second alternating word mid-stream (back-to-back cells)
			if rng.Intn(2) == 0 {
				want = append(want, bkAltWord(bits, msb))
				for i := 0; i < 1+rng.Intn(4); i++ {
					want = append(want, rng.Intn(maxv))
				}
			}
			pre, post := rng.Intn(3*spb), rng.Intn(3*spb)
			w, ct := bkBuild(want, ieee, msb, bits, spb, bitrate, pre, post)
			r := DecodeManchester(w, ct, ManchesterCfg{Bitrate: bitrate, IEEE: ieee, MSB: msb, Bits: bits})
			if !r.OK {
				fails++
				if fails <= 8 {
					t.Errorf("FN: valid frame rejected: ieee=%v msb=%v bits=%d spb=%d br=%d want=%v err=%q",
						ieee, msb, bits, spb, bitrate, want, r.Error)
				}
				continue
			}
			if !bkEqual(r.Bytes, want) {
				fails++
				if fails <= 8 {
					t.Errorf("FN: mismatch: ieee=%v msb=%v bits=%d spb=%d br=%d\n got=%v\nwant=%v",
						ieee, msb, bits, spb, bitrate, r.Bytes, want)
				}
			}
			if r.Proto != "manchester" {
				t.Errorf("proto=%q want manchester", r.Proto)
			}
		}
		if fails == 0 {
			t.Logf("explicit-bitrate round-trip: 80/80 exact (robust)")
		}
	})

	// Auto-bitrate: same idea but let the decoder infer T. Kept to a range where
	// inference is expected to work; spb=5 handled separately as [BUG 3].
	t.Run("FalseNegatives_auto", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0x5A5A))
		fails := 0
		for it := 0; it < 60; it++ {
			bits := 8
			ieee := rng.Intn(2) == 0
			msb := rng.Intn(2) == 0
			// spb 8..64, skip 5 (its own known-bug case). Auto needs several edges.
			spb := 8 + rng.Intn(57)
			bitrate := 100000
			want := []int{bkAltWord(bits, msb)}
			for i := 0; i < 4+rng.Intn(4); i++ {
				want = append(want, rng.Intn(256))
			}
			w, ct := bkBuild(want, ieee, msb, bits, spb, bitrate, rng.Intn(spb), rng.Intn(spb))
			r := DecodeManchester(w, ct, ManchesterCfg{IEEE: ieee, MSB: msb, Bits: bits})
			if !r.OK || !bkEqual(r.Bytes, want) {
				fails++
				if fails <= 8 {
					t.Errorf("FN(auto): ieee=%v msb=%v spb=%d ok=%v got=%v want=%v err=%q inferredSPB=%.2f",
						ieee, msb, spb, r.OK, r.Bytes, want, r.Error, r.SPB)
				}
			}
		}
		if fails == 0 {
			t.Logf("auto-bitrate round-trip (spb 8..64): robust")
		}
	})

	// [BUG 3] auto-bitrate at spb=5: valid frame, explicit path OK, auto path fails.
	t.Run("BUG3_auto_spb5", func(t *testing.T) {
		want := []int{0xAA, 0xB3, 0x2C, 0x47, 0x99}
		spb, bitrate := 5, 100000
		w, ct := bkBuild(want, true, true, 8, spb, bitrate, 0, 0)
		re := DecodeManchester(w, ct, ManchesterCfg{Bitrate: bitrate, IEEE: true, MSB: true})
		ra := DecodeManchester(w, ct, ManchesterCfg{IEEE: true, MSB: true})
		if !re.OK || !bkEqual(re.Bytes, want) {
			t.Errorf("sanity: explicit spb=5 should decode; ok=%v got=%v err=%q", re.OK, re.Bytes, re.Error)
		}
		if !ra.OK || !bkEqual(ra.Bytes, want) {
			// FALSE NEGATIVE: auto-infer rejects a frame the explicit path decodes fine.
			t.Errorf("[BUG 3] auto-bitrate FALSE NEGATIVE at spb=5: ok=%v got=%v err=%q (explicit got %v)",
				ra.OK, ra.Bytes, ra.Error, re.Bytes)
		}
	})

	// -----------------------------------------------------------------------
	// CLASS 2 — FALSE POSITIVES: non-frames / corrupted frames must not be
	// accepted as confident valid data.
	// -----------------------------------------------------------------------

	// 2a. Pure FLAT / DC at random levels must yield NO frame (no long clean run).
	t.Run("FP_flat_dc", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0xF1A7))
		for it := 0; it < 60; it++ {
			n := 50 + rng.Intn(4000)
			lvl := uint8(rng.Intn(256))
			s := make([]uint8, n)
			for i := range s {
				s[i] = lvl
			}
			br := 100000
			ct := 1.0 / (40.0 * float64(br))
			r := DecodeManchester(s, ct, ManchesterCfg{Bitrate: br, IEEE: true, MSB: true})
			ra := DecodeManchester(s, ct, ManchesterCfg{IEEE: true, MSB: true})
			if r.OK || len(r.Bytes) > 0 {
				t.Errorf("FP: flat level=%d n=%d decoded as valid: bytes=%v", lvl, n, r.Bytes)
			}
			if ra.OK || len(ra.Bytes) > 0 {
				t.Errorf("FP(auto): flat level=%d n=%d decoded as valid: bytes=%v", lvl, n, ra.Bytes)
			}
		}
	})

	// 2b. Ramp, Nyquist toggle, plain square (wrong-protocol) — must not fabricate
	// arbitrary structured payloads.
	t.Run("FP_shapes", func(t *testing.T) {
		br := 100000
		ct := 1.0 / (40.0 * float64(br)) // T = 40 samples
		// ramp
		ramp := make([]uint8, 3000)
		for i := range ramp {
			ramp[i] = uint8(i * 255 / 3000)
		}
		if r := DecodeManchester(ramp, ct, ManchesterCfg{Bitrate: br, IEEE: true, MSB: true}); r.OK {
			t.Errorf("FP: slow ramp decoded as valid: bytes=%v", r.Bytes)
		}
		// Nyquist toggle (alternating every sample) => T~2 < minSPB
		nyq := make([]uint8, 3000)
		for i := range nyq {
			nyq[i] = uint8((i % 2) * 255)
		}
		if r := DecodeManchester(nyq, ct, ManchesterCfg{IEEE: true, MSB: true}); r.OK {
			t.Errorf("FP: Nyquist toggle decoded as valid: bytes=%v spb=%.2f", r.Bytes, r.SPB)
		}
		// Plain square wave period 2T. This is genuinely an ambiguous Manchester
		// constant/alternating stream; a decoder may accept it, but it must NOT
		// fabricate a VARIED structured payload — every byte must be identical.
		for _, period := range []int{80, 80, 80} {
			sq := make([]uint8, 3000)
			for i := range sq {
				if (i/(period/2))%2 == 0 {
					sq[i] = 210
				} else {
					sq[i] = 40
				}
			}
			r := DecodeManchester(sq, ct, ManchesterCfg{Bitrate: br, IEEE: true, MSB: true})
			if r.OK {
				allEq := true
				for _, b := range r.Bytes {
					if b != r.Bytes[0] {
						allEq = false
						break
					}
				}
				if !allEq {
					t.Errorf("FP: plain square wave produced a varied payload: bytes=%v", r.Bytes)
				}
			}
		}
	})

	// 2c. Pure random noise: must never panic, and must obey the OK/Error contract.
	// (Arbitrary short byte runs on noise are inherent for a checksum-less code.)
	t.Run("FP_noise", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0x0FF1CE))
		for it := 0; it < 60; it++ {
			n := rng.Intn(3000)
			s := make([]uint8, n)
			for i := range s {
				s[i] = uint8(rng.Intn(256))
			}
			br := []int{0, 100000}[rng.Intn(2)]
			r := DecodeManchester(s, 1e-6, ManchesterCfg{Bitrate: br, IEEE: rng.Intn(2) == 0, MSB: rng.Intn(2) == 0})
			if !r.OK && r.Error == "" {
				t.Errorf("noise: neither OK nor Error (n=%d)", n)
			}
		}
	})

	// 2d. CORRUPTED valid frame: remove one cell's mid-cell transition (a Manchester
	// CODING VIOLATION — the code's ONLY integrity check). It must be surfaced: the
	// result must NOT be OK-with-the-full-clean-byte-stream-and-no-error, and a
	// coding violation ought to raise a frame-error span. We measure how often it
	// is silently swallowed.
	t.Run("FP_corrupted_frame", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0xC0FFEE))
		spb, bitrate := 40, 100000
		ct := 1.0 / (float64(spb) * float64(bitrate))
		acceptedAsClean := 0 // worst case: corrupted frame == clean stream, no error
		unflagged := 0       // OK=true, corruption not reported (no frame-error) though output changed
		iters := 60
		var sample string
		for it := 0; it < iters; it++ {
			nb := 3 + rng.Intn(5)
			want := []int{0xAA}
			for i := 1; i < nb; i++ {
				want = append(want, rng.Intn(256))
			}
			clean := manchesterWave(mBits(want, true, 8), true, spb)
			rc := DecodeManchester(clean, ct, ManchesterCfg{Bitrate: bitrate, IEEE: true, MSB: true})
			if !rc.OK || !bkEqual(rc.Bytes, want) {
				t.Fatalf("clean control failed: ok=%v got=%v want=%v", rc.OK, rc.Bytes, want)
			}
			// flatten one post-preamble cell -> removes its mid transition
			ncells := nb * 8
			cell := 8 + rng.Intn(ncells-8)
			start := 6*spb + cell*spb
			lvl := uint8(210)
			if rng.Intn(2) == 0 {
				lvl = 40
			}
			w := append([]uint8{}, clean...)
			for j := start; j < start+spb && j < len(w); j++ {
				w[j] = lvl
			}
			r := DecodeManchester(w, ct, ManchesterCfg{Bitrate: bitrate, IEEE: true, MSB: true})

			// Hard invariant: a corrupted frame must never decode byte-identical to
			// the clean frame with no error flag (that would be full silent accept).
			if r.OK && bkEqual(r.Bytes, want) && !bkHasKind(r, "frame-error") {
				acceptedAsClean++
				if sample == "" {
					sample = fmt.Sprintf("cell=%d want=%v", cell, want)
				}
			}
			// Softer integrity gap: OK=true, output differs, but NO frame-error span
			// -> the coding violation was silently swallowed (frame split + tail drop).
			if r.OK && !bkHasKind(r, "frame-error") {
				unflagged++
				if sample == "" {
					sample = fmt.Sprintf("cell=%d want=%v -> got=%v", cell, want, r.Bytes)
				}
			}
		}
		if acceptedAsClean > 0 {
			t.Errorf("FP(severe): %d/%d corrupted frames decoded byte-identical to clean with no frame-error [%s]",
				acceptedAsClean, iters, sample)
		}
		if unflagged > 0 {
			// [BUG 2] — a coding violation is Manchester's only integrity signal; the
			// decoder returns OK=true and silently drops the corrupted tail without a
			// frame-error span in a large fraction of cases.
			t.Errorf("[BUG 2] coding violation NOT reported in %d/%d corruptions (OK=true, no frame-error) [%s]",
				unflagged, iters, sample)
		}
	})

	// 2e. TRUNCATED valid frame (cut mid-frame): must not fabricate a tail. Whatever
	// bytes are returned must be a prefix of the clean decode (never longer/other).
	t.Run("FP_truncated", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0x7A17))
		spb, bitrate := 40, 100000
		ct := 1.0 / (float64(spb) * float64(bitrate))
		for it := 0; it < 50; it++ {
			want := []int{0xAA}
			for i := 0; i < 5; i++ {
				want = append(want, rng.Intn(256))
			}
			clean := manchesterWave(mBits(want, true, 8), true, spb)
			// cut somewhere inside the data region (drop the trailing idle + some cells)
			cut := 6*spb + (1+rng.Intn(5))*spb + rng.Intn(spb)
			if cut > len(clean) {
				cut = len(clean)
			}
			w := append([]uint8{}, clean[:cut]...)
			r := DecodeManchester(w, ct, ManchesterCfg{Bitrate: bitrate, IEEE: true, MSB: true})
			if r.OK {
				if len(r.Bytes) > len(want) {
					t.Errorf("FP: truncated frame produced MORE bytes than the full frame: got=%v want<=%v", r.Bytes, want)
				}
				for i := range r.Bytes {
					if i < len(want) && r.Bytes[i] != want[i] {
						// A prefix mismatch is acceptable only if a frame-error was raised.
						if !bkHasKind(r, "frame-error") {
							t.Errorf("FP: truncated frame fabricated byte %d: got=%v want-prefix=%v", i, r.Bytes, want)
						}
						break
					}
				}
			}
		}
	})

	// -----------------------------------------------------------------------
	// CLASS 3 — EDGE CASES.
	// -----------------------------------------------------------------------

	// 3a. Min & max legal samples/bit; exactly one frame; back-to-back no gap;
	// all-0x00 / all-0xFF (WITH preamble => must be exact).
	t.Run("Edge_rates_and_payloads", func(t *testing.T) {
		type C struct {
			name string
			want []int
			ieee bool
			spb  int
		}
		cases := []C{
			{"max-rate spb=4", []int{0xAA, 0x3C, 0x59}, true, 4},
			{"spb=5", []int{0xAA, 0x3C, 0x59}, true, 5},
			{"min-rate spb=200", []int{0xAA, 0x3C, 0x59}, true, 200},
			{"single-frame", []int{0xAA, 0x5A}, true, 40},
			{"all-00 w/preamble", []int{0xAA, 0x00, 0x00, 0x00}, true, 40},
			{"all-FF w/preamble", []int{0xAA, 0xFF, 0xFF, 0xFF}, true, 40},
			{"all-00 thomas w/pre", []int{0x55, 0x00, 0x00}, false, 40},
			{"all-FF thomas w/pre", []int{0x55, 0xFF, 0xFF}, false, 40},
		}
		for _, c := range cases {
			bitrate := 100000
			w, ct := bkBuild(c.want, c.ieee, true, 8, c.spb, bitrate, 0, 0)
			r := DecodeManchester(w, ct, ManchesterCfg{Bitrate: bitrate, IEEE: c.ieee, MSB: true})
			if !r.OK || !bkEqual(r.Bytes, c.want) {
				t.Errorf("edge %q: ok=%v got=%v want=%v err=%q", c.name, r.OK, r.Bytes, c.want, r.Error)
			}
		}
		// back-to-back with NO gap: one continuous cell stream, all words returned.
		{
			want := []int{0xAA, 0x11, 0x22, 0xAA, 0x33, 0x44, 0x55, 0x66}
			bitrate := 100000
			w, ct := bkBuild(want, true, true, 8, 40, bitrate, 0, 0)
			r := DecodeManchester(w, ct, ManchesterCfg{Bitrate: bitrate, IEEE: true, MSB: true})
			if !r.OK || !bkEqual(r.Bytes, want) {
				t.Errorf("edge back-to-back: ok=%v got=%v want=%v", r.OK, r.Bytes, want)
			}
		}
		// two separate frames with an idle gap (each preambled) — both recovered.
		{
			bitrate, spb := 100000, 40
			ct := 1.0 / (float64(spb) * float64(bitrate))
			f1 := manchesterWave(mBits([]int{0xAA, 0x11, 0x22}, true, 8), true, spb)
			f2 := manchesterWave(mBits([]int{0xAA, 0x33, 0x44}, true, 8), true, spb)
			w := append(append(append([]uint8{}, f1...), bkPad(5*spb)...), f2...)
			r := DecodeManchester(w, ct, ManchesterCfg{Bitrate: bitrate, IEEE: true, MSB: true})
			want := []int{0xAA, 0x11, 0x22, 0xAA, 0x33, 0x44}
			if !r.OK || !bkEqual(r.Bytes, want) {
				t.Errorf("edge two-frames-gap: ok=%v got=%v want=%v", r.OK, r.Bytes, want)
			}
		}
	})

	// [BUG 1] constant-bit inversion: a bare (no transition-rich preamble) all-same
	// payload whose leading half-cell equals idle is returned as its complement.
	t.Run("BUG1_constant_bit_inversion", func(t *testing.T) {
		bitrate, spb := 100000, 40
		ct := 1.0 / (float64(spb) * float64(bitrate))
		type C struct {
			ieee bool
			want []int
		}
		for _, c := range []C{
			{true, []int{0x00, 0x00, 0x00}},  // IEEE: leading half-cell high == idle -> inverted
			{false, []int{0xFF, 0xFF, 0xFF}}, // Thomas: same failure, opposite bit
		} {
			w := manchesterWave(mBits(c.want, true, 8), c.ieee, spb)
			r := DecodeManchester(w, ct, ManchesterCfg{Bitrate: bitrate, IEEE: c.ieee, MSB: true})
			if !r.OK || !bkEqual(r.Bytes, c.want) {
				inv := []int{}
				for _, b := range c.want {
					inv = append(inv, ^b&0xff)
				}
				t.Errorf("[BUG 1] constant-bit MISDECODE ieee=%v: want=%v got=%v (one's-complement=%v), OK=%v",
					c.ieee, c.want, r.Bytes, inv, r.OK)
			}
		}
		// Control: the OTHER constant (leading half-cell differs from idle) is fine.
		for _, c := range []C{
			{true, []int{0xFF, 0xFF, 0xFF}},
			{false, []int{0x00, 0x00, 0x00}},
		} {
			w := manchesterWave(mBits(c.want, true, 8), c.ieee, spb)
			r := DecodeManchester(w, ct, ManchesterCfg{Bitrate: bitrate, IEEE: c.ieee, MSB: true})
			if !r.OK || !bkEqual(r.Bytes, c.want) {
				t.Errorf("control constant ieee=%v want=%v got=%v ok=%v (expected exact)", c.ieee, c.want, r.Bytes, r.OK)
			}
		}
	})

	// 3b. Degenerate sizes & hostile scalars: must never panic; must honor the
	// OK/Error contract (never both empty).
	t.Run("Edge_no_panic", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0xDEAD))
		inputs := [][]uint8{nil, {}, {200}, {200, 40}, {40, 210}, bkPad(1), bkPad(2), bkPad(500)}
		// a couple of tiny real fragments
		inputs = append(inputs, manchesterWave(mBits([]int{0xAA}, true, 8), true, 4))
		for i := 0; i < 30; i++ {
			n := rng.Intn(400)
			s := make([]uint8, n)
			for j := range s {
				s[j] = uint8(rng.Intn(256))
			}
			inputs = append(inputs, s)
		}
		colTimes := []float64{0, -1, -1e-6, 1e-12, 1e-6, 1, 1e12}
		bitrates := []int{0, -100, -1, 1, 25000, 1 << 30, 1 << 62}
		bitsVals := []int{-1, 0, 1, 8, 9, 16, 17, 64, 1 << 20}
		for _, in := range inputs {
			for k := 0; k < 12; k++ {
				cfg := ManchesterCfg{
					Bitrate:   bitrates[rng.Intn(len(bitrates))],
					IEEE:      rng.Intn(2) == 0,
					MSB:       rng.Intn(2) == 0,
					Bits:      bitsVals[rng.Intn(len(bitsVals))],
					Format:    []string{"", "hex", "dec", "bin", "ascii", "both", "??"}[rng.Intn(7)],
					Threshold: rng.Float64()*300 - 20,
					HaveThr:   rng.Intn(2) == 0,
				}
				ct := colTimes[rng.Intn(len(colTimes))]
				func() {
					defer func() {
						if p := recover(); p != nil {
							t.Fatalf("PANIC on cfg=%+v ct=%g len=%d: %v", cfg, ct, len(in), p)
						}
					}()
					r := DecodeManchester(in, ct, cfg)
					if !r.OK && r.Error == "" {
						t.Errorf("contract: neither OK nor Error (len=%d ct=%g cfg=%+v)", len(in), ct, cfg)
					}
					if r.OK && r.Proto != "manchester" {
						t.Errorf("proto=%q on OK result", r.Proto)
					}
				}()
			}
		}
	})

	// 3c. Very long record must decode and stay linear-ish (no hang).
	t.Run("Edge_long_record", func(t *testing.T) {
		bitrate, spb := 100000, 40
		ct := 1.0 / (float64(spb) * float64(bitrate))
		want := []int{0xAA}
		for i := 0; i < 200; i++ {
			want = append(want, (i*37+5)&0xff)
		}
		w, _ := bkBuild(want, true, true, 8, spb, bitrate, 0, 0)
		r := DecodeManchester(w, ct, ManchesterCfg{Bitrate: bitrate, IEEE: true, MSB: true})
		if !r.OK || !bkEqual(r.Bytes, want) {
			t.Errorf("long record: ok=%v n=%d (want %d) err=%q", r.OK, len(r.Bytes), len(want), r.Error)
		}
	})
}