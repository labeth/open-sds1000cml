package decode

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// --- red-team helpers -------------------------------------------------------
//
// This file adversarially attacks DecodeSENT. It reuses sentWave (the WAVEFORM
// SYNTHESIZER from decode_sent_test.go) to build valid SENT pulse trains, and
// adds a self-consistent SAE J2716 CRC-4 so "valid" frames carry a correct CRC
// nibble. The decoder itself never computes this CRC — that fact is the core
// finding below (false-positive class).

// sentCRC4Table is the SAE J2716 CRC-4 nibble table (polynomial x^4+x^3+x^2+1).
// (removed duplicate sentCRC4Table — provided by the decoder)

// sentCRC4 computes the J2716 CRC-4 over the data nibbles (seed 5, augmented).
// Used ONLY by this test to build frames whose last nibble is a legitimate CRC,
// so that (a) the false-negative class exercises genuinely valid frames and
// (b) the false-positive class can corrupt the *one* correct value. The decoder
// under test does not implement this.
// (removed duplicate func sentCRC4 — provided by the decoder / another test)

// padHigh wraps a synthesized wave in extra idle-high samples (SENT idles high),
// simulating a real capture that does not begin exactly on a frame.
func padHigh(w []uint8, before, after int) []uint8 {
	out := make([]uint8, 0, before+len(w)+after)
	for i := 0; i < before; i++ {
		out = append(out, 210)
	}
	out = append(out, w...)
	for i := 0; i < after; i++ {
		out = append(out, 210)
	}
	return out
}

// sentSpanCounts tallies span kinds in a Result.
func sentSpanCounts(r Result) (nSync, nData, nCRC, nFerr, nPause int) {
	for _, s := range r.Spans {
		switch s.Kind {
		case "sync":
			nSync++
		case "data":
			nData++
		case "crc":
			nCRC++
		case "frame-error":
			nFerr++
		case "pause":
			nPause++
		}
	}
	return
}

// buildFrame makes one nib-nibble frame with a correct trailing CRC-4 nibble.
func buildFrame(rng *rand.Rand, nib int) []int {
	nibs := make([]int, nib)
	for i := range nibs {
		nibs[i] = rng.Intn(16)
	}
	if nib >= 2 {
		nibs[nib-1] = sentCRC4(nibs[1 : nib-1]) // CRC over the data nibbles
	}
	return nibs
}

func TestBreakSent(t *testing.T) {
	// =========================================================================
	// CLASS 1 — FALSE NEGATIVES: >=50 fully VALID frames must decode exactly.
	// Varies payload, tick (bit rate), nibble count, single/back-to-back frames,
	// pause pulse, jitter, tick-override vs auto, and a random idle gap around it.
	// =========================================================================
	fn := rand.New(rand.NewSource(0xBADC0DE))
	fnIters := 80
	for it := 0; it < fnIters; it++ {
		tick := 3 + fn.Intn(30)     // 3..32 samples per tick (legal bit-rate range)
		nFrames := 1 + fn.Intn(3)   // 1..3 frames
		nib := 2 + fn.Intn(19)      // 2..20 nibbles (a real frame carries a CRC nibble)
		usePause := fn.Intn(2) == 0 // trailing pause pulse or not
		pauseTicks := 0
		if usePause {
			pauseTicks = 80 + fn.Intn(80) // 80..159: clear of SYNC(56) and nibble(12..27)
		}
		jit := 0
		if tick >= 8 {
			jit = fn.Intn(tick/4 + 1) // deterministic jitter, always < tick/2 (rounds true)
		}

		var frames [][]int
		var expected []int
		for f := 0; f < nFrames; f++ {
			nibs := buildFrame(fn, nib)
			frames = append(frames, nibs)
			expected = append(expected, nibs...)
		}
		w := sentWave(frames, tick, pauseTicks, jit)
		w = padHigh(w, fn.Intn(5*tick+1), fn.Intn(5*tick+1))

		cfg := SENTCfg{Nibbles: nib, PausePulse: usePause}
		ct := 1e-6
		if fn.Intn(2) == 0 { // exercise the tick-override path with an EXACT tick
			ct = 5e-7
			cfg.TickNs = float64(tick) * ct * 1e9
		}

		r := DecodeSENT(w, ct, cfg)
		desc := fmt.Sprintf("it=%d tick=%d nib=%d frames=%d pause=%v jit=%d override=%v",
			it, tick, nib, nFrames, usePause, jit, cfg.TickNs > 0)
		if !r.OK {
			t.Errorf("FALSE-NEGATIVE: valid SENT frame rejected: %q [%s]", r.Error, desc)
			continue
		}
		if got, want := fmt.Sprintf("%v", r.Bytes), fmt.Sprintf("%v", expected); got != want {
			t.Errorf("FALSE-NEGATIVE: nibble mismatch\n got=%v\nwant=%v [%s]", r.Bytes, expected, desc)
		}
	}

	// =========================================================================
	// CLASS 2 — FALSE POSITIVES: adversarial NON-frames / corrupted frames must
	// not be confirmed as valid. Well over 50 adversarial inputs below.
	// =========================================================================
	fp := rand.New(rand.NewSource(0x5EED))

	// (2a) Flat / DC at several levels — must be rejected outright (no signal).
	for _, lvl := range []uint8{0, 40, 128, 210, 255} {
		flat := make([]uint8, 800)
		for i := range flat {
			flat[i] = lvl
		}
		r := DecodeSENT(flat, 1e-6, SENTCfg{Nibbles: 8})
		if r.OK {
			t.Errorf("FALSE-POSITIVE: flat/DC level %d decoded ok=true (bytes=%d)", lvl, len(r.Bytes))
		}
	}

	// (2b) Monotonic ramp (no falling edges) — must be rejected.
	ramp := make([]uint8, 512)
	for i := range ramp {
		ramp[i] = uint8((i * 255) / 511)
	}
	if r := DecodeSENT(ramp, 1e-6, SENTCfg{Nibbles: 8}); r.OK {
		t.Errorf("FALSE-POSITIVE: monotonic ramp decoded ok=true (bytes=%d)", len(r.Bytes))
	}

	// (2c) Nyquist toggle + fixed-period square waves (wrong protocol). These may
	// report ok=true structurally, but must NOT fabricate any DATA nibbles.
	nyq := make([]uint8, 2000)
	for i := range nyq {
		if i%2 == 0 {
			nyq[i] = 40
		} else {
			nyq[i] = 210
		}
	}
	if r := DecodeSENT(nyq, 1e-6, SENTCfg{Nibbles: 8}); len(r.Bytes) != 0 {
		t.Errorf("FALSE-POSITIVE: Nyquist toggle produced %d data nibbles %v", len(r.Bytes), r.Bytes)
	}
	for _, T := range []int{8, 10, 16, 24, 40, 56, 80, 120} {
		sq := make([]uint8, 3000)
		for i := range sq {
			if (i/(T/2))%2 == 0 {
				sq[i] = 210
			} else {
				sq[i] = 40
			}
		}
		if r := DecodeSENT(sq, 1e-6, SENTCfg{Nibbles: 8}); len(r.Bytes) != 0 {
			t.Errorf("FALSE-POSITIVE: fixed square wave T=%d produced %d data nibbles", T, len(r.Bytes))
		}
	}

	// (2d) A coding-violation pulse (illegal width, clearly not a nibble and not a
	// SYNC) MUST be flagged as a frame-error. Width 35 ticks => value 23, and
	// |35-56|>tol so it is not mistaken for a SYNC.  This is a robustness check
	// the decoder is expected to PASS.
	{
		good := buildFrame(fp, 8)
		corrupt := append([]int{}, good...)
		corrupt[3] = 23 // 35-tick pulse => out of nibble range
		w := sentWave([][]int{corrupt}, 8, 0, 0)
		r := DecodeSENT(w, 1e-6, SENTCfg{Nibbles: 8})
		_, _, _, nFerr, _ := sentSpanCounts(r)
		if nFerr == 0 {
			t.Errorf("MISSED coding violation: illegal 35-tick pulse not flagged as frame-error (spans=%+v)", r.Spans)
		}
	}

	// (2e) PURE NOISE — a robust CRC-gated SENT decoder should not confirm a frame
	// of data out of random noise. Count how many of 50 noise captures yield a
	// full frame's worth of DATA nibbles with ok=true.
	noiseFrameHits := 0
	const noiseIters = 50
	for s := 0; s < noiseIters; s++ {
		g := make([]uint8, 3000)
		for i := range g {
			g[i] = uint8(fp.Intn(256))
		}
		r := DecodeSENT(g, 1e-6, SENTCfg{Nibbles: 8})
		if r.OK && len(r.Bytes) >= 8 {
			noiseFrameHits++
		}
	}
	if noiseFrameHits > noiseIters/2 { // CRC-4 residual ~1/16/frame; pre-fix was ~100%
		// KNOWN BUG (false-positive). Non-fatal (t.Errorf) so the rest of the
		// suite still runs. Root cause: no CRC-4 integrity gate — see (2f).
		t.Errorf("FALSE-POSITIVE (no CRC gate): %d/%d pure-noise captures decoded as ok=true "+
			"with >=8 data nibbles; the CRC-4 gate should reject the majority of noise",
			noiseFrameHits, noiseIters)
	}

	// (2f) CORRUPTED CRC — build a frame with a CORRECT J2716 CRC-4 nibble, then
	// change ONLY the CRC nibble to each of the 15 wrong values. At most one of
	// the 16 possible last-nibble values is the true CRC, so every changed value
	// is a corrupted frame that a real decoder MUST flag (ok=false, or a
	// frame-error/parity-error span). Repeated over several random payloads.
	acceptedCorruptCRC, totalCorrupt := 0, 0
	for trial := 0; trial < 10; trial++ {
		nib := 8
		base := buildFrame(fp, nib)
		trueCRC := base[nib-1]
		// sanity: the un-corrupted frame decodes cleanly
		if r := DecodeSENT(sentWave([][]int{base}, 8, 0, 0), 1e-6, SENTCfg{Nibbles: nib}); !r.OK {
			t.Errorf("setup: correct-CRC frame unexpectedly rejected: %s", r.Error)
		}
		for c := 0; c < 16; c++ {
			if c == trueCRC {
				continue
			}
			bad := append([]int{}, base...)
			bad[nib-1] = c // corrupt ONLY the CRC nibble to a wrong (but in-range) value
			w := sentWave([][]int{bad}, 8, 0, 0)
			r := DecodeSENT(w, 1e-6, SENTCfg{Nibbles: nib})
			_, _, _, nFerr, _ := sentSpanCounts(r)
			flagged := !r.OK || r.Error != "" || nFerr > 0
			totalCorrupt++
			if !flagged {
				acceptedCorruptCRC++
			}
		}
	}
	if acceptedCorruptCRC > 0 {
		// KNOWN BUG (false-positive): the decoder never verifies the CRC-4. It
		// labels the last nibble "crc" but returns ok=true for any value, so a
		// deliberately corrupted CRC is accepted identically to a valid one.
		t.Errorf("FALSE-POSITIVE (CRC not verified): %d/%d frames with a CORRUPTED CRC nibble "+
			"were accepted as valid (ok=true, no error/frame-error); a corrupted CRC MUST be flagged",
			acceptedCorruptCRC, totalCorrupt)
	}

	// (2g) TRUNCATED frame — a valid frame cut mid-payload. The decoder may return
	// what it read, but must not fabricate MORE data nibbles than were physically
	// present (this passes; it is documented here for the record).
	{
		full := buildFrame(fp, 8)
		w := sentWave([][]int{full}, 8, 0, 0)
		half := w[:len(w)*3/5] // cut mid-frame
		r := DecodeSENT(half, 1e-6, SENTCfg{Nibbles: 8})
		if r.OK && len(r.Bytes) >= 8 {
			t.Errorf("FALSE-POSITIVE: truncated frame yielded a FULL %d-nibble payload %v", len(r.Bytes), r.Bytes)
		}
	}

	// =========================================================================
	// CLASS 3 — EDGE CASES: extremes of bit rate, record length, payload, sample
	// count, and degenerate configs. Must never panic; valid extremes must decode.
	// =========================================================================

	// Min & max legal tick (bit rate) on a single frame.
	for _, tick := range []int{1, 2, 120, 200} {
		nibs := buildFrame(fp, 8)
		w := sentWave([][]int{nibs}, tick, 0, 0)
		r := DecodeSENT(w, 1e-6, SENTCfg{Nibbles: 8})
		if !r.OK {
			t.Errorf("EDGE: tick=%d single frame rejected: %s", tick, r.Error)
			continue
		}
		if got, want := fmt.Sprintf("%v", r.Bytes), fmt.Sprintf("%v", nibs); got != want {
			t.Errorf("EDGE: tick=%d nibble mismatch got %v want %v", tick, r.Bytes, nibs)
		}
	}

	// Exactly one frame.
	{
		nibs := buildFrame(fp, 8)
		r := DecodeSENT(sentWave([][]int{nibs}, 8, 0, 0), 1e-6, SENTCfg{Nibbles: 8})
		if !r.OK || fmt.Sprintf("%v", r.Bytes) != fmt.Sprintf("%v", nibs) {
			t.Errorf("EDGE: single frame failed ok=%v bytes=%v want %v", r.OK, r.Bytes, nibs)
		}
	}

	// Back-to-back frames with NO gap.
	{
		fa := buildFrame(fp, 8)
		fb := buildFrame(fp, 8)
		fc := buildFrame(fp, 8)
		w := sentWave([][]int{fa, fb, fc}, 7, 0, 0)
		r := DecodeSENT(w, 1e-6, SENTCfg{Nibbles: 8})
		want := append(append(append([]int{}, fa...), fb...), fc...)
		if !r.OK || fmt.Sprintf("%v", r.Bytes) != fmt.Sprintf("%v", want) {
			t.Errorf("EDGE: back-to-back frames failed ok=%v\n got=%v\nwant=%v", r.OK, r.Bytes, want)
		}
	}

	// All-0x0 and all-0xF payloads (min & max nibble pulse widths).
	for _, v := range []int{0x0, 0xF} {
		nibs := make([]int, 8)
		for i := range nibs {
			nibs[i] = v
		}
		nibs[7] = sentCRC4(nibs[1:7]) // valid CRC so this is a legitimate frame
		r := DecodeSENT(sentWave([][]int{nibs}, 10, 0, 0), 1e-6, SENTCfg{Nibbles: 8})
		if !r.OK || fmt.Sprintf("%v", r.Bytes) != fmt.Sprintf("%v", nibs) {
			t.Errorf("EDGE: all-0x%X payload failed ok=%v bytes=%v", v, r.OK, r.Bytes)
		}
	}

	// Shortest meaningful record (nib=1, small tick) and a very long record.
	{
		mini := []int{0x5, sentCRC4(nil)} // status + CRC over no data (a valid 2-nibble frame)
		short := sentWave([][]int{mini}, 4, 0, 0)
		if r := DecodeSENT(short, 1e-6, SENTCfg{Nibbles: 2}); !r.OK || len(r.Bytes) != 2 || r.Bytes[0] != 5 {
			t.Errorf("EDGE: shortest record failed ok=%v bytes=%v", r.OK, r.Bytes)
		}
		var longFrames [][]int
		for f := 0; f < 40; f++ {
			longFrames = append(longFrames, buildFrame(fp, 12))
		}
		longW := sentWave(longFrames, 20, 0, 0)
		if r := DecodeSENT(longW, 1e-6, SENTCfg{Nibbles: 12}); !r.OK {
			t.Errorf("EDGE: very long record (%d samples) rejected: %s", len(longW), r.Error)
		}
	}

	// Boundary sample counts: 0, 1, 2 samples must not panic and must be rejected.
	for _, n := range []int{0, 1, 2} {
		b := make([]uint8, n)
		for i := range b {
			b[i] = 210
		}
		if r := DecodeSENT(b, 1e-6, SENTCfg{Nibbles: 8}); r.OK {
			t.Errorf("EDGE: %d-sample record decoded ok=true", n)
		}
	}

	// Degenerate colTimeS / tick / nibble configs must not panic (fuzz-style).
	valid := sentWave([][]int{buildFrame(fp, 8)}, 8, 0, 0)
	garbage := make([]uint8, 2500)
	for i := range garbage {
		garbage[i] = uint8(fp.Intn(256))
	}
	degenerate := []struct {
		name string
		w    []uint8
		ct   float64
		cfg  SENTCfg
	}{
		{"colTimeS=0", valid, 0, SENTCfg{Nibbles: 8}},
		{"colTimeS<0", valid, -1e-6, SENTCfg{Nibbles: 8}},
		{"colTimeS=+Inf", valid, math.Inf(1), SENTCfg{Nibbles: 8}},
		{"colTimeS=NaN", garbage, math.NaN(), SENTCfg{TickNs: 1000, Nibbles: 8}},
		{"TickNs<0", valid, 1e-6, SENTCfg{TickNs: -5, Nibbles: 8}},
		{"TickNs=+Inf", garbage, 1e-6, SENTCfg{TickNs: math.Inf(1), Nibbles: 8}},
		{"TickNs=1e300 ct=1e-300", garbage, 1e-300, SENTCfg{TickNs: 1e300, Nibbles: 8}},
		{"Nibbles<0", garbage, 1e-6, SENTCfg{Nibbles: -5}},
		{"Nibbles huge", garbage, 1e-6, SENTCfg{Nibbles: 1 << 20, PausePulse: true}},
		{"Threshold override", valid, 1e-6, SENTCfg{Nibbles: 8, Threshold: 125, HaveThr: true}},
	}
	for _, d := range degenerate {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("PANIC on degenerate config %q: %v", d.name, p)
				}
			}()
			_ = DecodeSENT(d.w, d.ct, d.cfg)
		}()
	}
}
