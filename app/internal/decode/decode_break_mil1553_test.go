package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// ============================================================================
// Red-team regression suite for DecodeMIL1553 (see decode_mil1553.go).
//
// It reuses the WAVEFORM SYNTHESIZER (mil1553Wave / mil1553OddParity) already
// defined in decode_mil1553_test.go. MIL-STD-1553B has a fixed wire convention
// (bi-phase Manchester, MSB-first, one odd-parity bit per 20-bit-time word), so
// there are no MSB/LSB/convention flags to vary — only the payload words, the
// command/data sync type, the samples-per-bit (bit rate / tick), single vs
// back-to-back words, and the surrounding idle.
//
// FINDINGS (all confirmed against the code as-is):
//   * The EXPLICIT-bitrate path is robust: it recovers every valid frame
//     exactly, flags every corrupted parity bit, and rejects noise/DC/ramp/
//     Nyquist/wrong-protocol squares/truncated frames as non-confident.
//   * The AUTO-bitrate path (cfg.Bitrate == 0) has two FALSE-NEGATIVE bugs in
//     its bit-period inference; both are pinned by t.Errorf below and both
//     decode correctly the moment the caller supplies an explicit Bitrate:
//       (a) a pure alternating-bit payload (0xAAAA) — a whole stream of them —
//           carries NO half-period (T/2) edge gaps, so the 10th-percentile
//           heuristic locks onto the full-period gap and infers T = 2*T_true,
//           after which no 1.5*T sync gap is found -> "no MIL-STD-1553 sync".
//       (b) samples-per-bit == 5 (a legal rate; minSPB is 4) — the coarse
//           2/3-sample half-cell quantisation defeats the same heuristic.
// ============================================================================

// mbConfident reports whether the decoder returned a "confident valid" 1553
// result: OK with at least one word whose parity was accepted (no frame-error
// span over it). A corrupted or garbage input must NOT be confident.
func mbConfident(r Result) bool {
	if !r.OK {
		return false
	}
	return mbFrameErrs(r) < len(r.Bytes) // some word survived with intact parity
}

func mbFrameErrs(r Result) int {
	fe := 0
	for _, s := range r.Spans {
		if s.Kind == "frame-error" {
			fe++
		}
	}
	return fe
}

func mbIntEq(a []int, b []int) bool {
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

// mbPad extends a capture with `pre` leading and `post` trailing samples held at
// the wave's existing idle levels (its first/last sample), so no spurious edge
// is introduced. It models a real capture that does not begin/end on a frame.
func mbPad(w []uint8, pre, post int) []uint8 {
	if len(w) == 0 {
		return w
	}
	out := make([]uint8, 0, pre+len(w)+post)
	for i := 0; i < pre; i++ {
		out = append(out, w[0])
	}
	out = append(out, w...)
	for i := 0; i < post; i++ {
		out = append(out, w[len(w)-1])
	}
	return out
}

func TestBreakMil1553(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1553beef))

	// ---------------------------------------------------------------------
	// CLASS 1 — FALSE NEGATIVES: >=50 fully VALID frames must decode exactly.
	// Vary payload, sync type, samples-per-bit, bit rate, word count, and the
	// surrounding idle. The EXPLICIT-bitrate assertions are hard (the decoder
	// must never miss a valid frame). The AUTO path is exercised too; because
	// its inference is known-fragile (see pinned bugs) its misses are only
	// logged here and pinned deterministically further down.
	// ---------------------------------------------------------------------
	spbSet := []int{6, 8, 10, 12, 16, 20, 32, 40, 64, 100}
	brSet := []int{1_000_000, 2_000_000, 4_000_000, 500_000}
	autoMiss, autoWrong := 0, 0
	for it := 0; it < 60; it++ {
		nw := 1 + rng.Intn(4)
		words := make([]int, nw)
		cmd := make([]bool, nw)
		par := make([]int, nw)
		for i := range words {
			words[i] = rng.Intn(0x10000)
			cmd[i] = rng.Intn(2) == 0
			par[i] = mil1553OddParity(words[i])
		}
		spb := spbSet[rng.Intn(len(spbSet))]
		br := brSet[rng.Intn(len(brSet))]
		ct := 1.0 / (float64(spb) * float64(br))
		w := mbPad(mil1553Wave(words, cmd, par, spb), rng.Intn(6*spb), rng.Intn(6*spb))

		// EXPLICIT bit rate — must recover the exact word stream, no frame-error.
		r := DecodeMIL1553(w, ct, MIL1553Cfg{Bitrate: br})
		if !r.OK {
			t.Errorf("FALSE-NEGATIVE it=%d spb=%d br=%d words=%v: explicit decode failed: %s",
				it, spb, br, words, r.Error)
		} else {
			if !mbIntEq(r.Bytes, words) {
				t.Errorf("FALSE-NEGATIVE it=%d spb=%d br=%d: explicit got %v want %v",
					it, spb, br, r.Bytes, words)
			}
			if fe := mbFrameErrs(r); fe != 0 {
				t.Errorf("FALSE-NEGATIVE it=%d spb=%d br=%d: %d spurious frame-errors on correct parity",
					it, spb, br, fe)
			}
			if !mbConfident(r) {
				t.Errorf("FALSE-NEGATIVE it=%d: a valid frame was not confident-valid", it)
			}
		}

		// AUTO bit rate — soft: a miss is logged; a WRONG value is a hard bug.
		ra := DecodeMIL1553(w, ct, MIL1553Cfg{})
		if !ra.OK {
			autoMiss++
		} else if !mbIntEq(ra.Bytes, words) {
			autoWrong++
			t.Errorf("FALSE-NEGATIVE it=%d spb=%d words=%v: auto decoded WRONG value %v",
				it, spb, words, ra.Bytes)
		}
	}
	if autoMiss > 0 {
		t.Logf("auto-bitrate missed %d/60 random valid frames (fragile inference; see pinned bugs)", autoMiss)
	}

	// --- Pinned FALSE-NEGATIVE (a): all-alternating payload, AUTO bit rate. ---
	// A whole stream of 0xAAAA words. Explicit decodes it fine; auto must too.
	{
		spb := 40
		ct := 1.0 / (float64(spb) * 1e6)
		words := []int{0xAAAA, 0xAAAA}
		cmd := []bool{true, false}
		par := []int{mil1553OddParity(0xAAAA), mil1553OddParity(0xAAAA)}
		w := mil1553Wave(words, cmd, par, spb)
		if re := DecodeMIL1553(w, ct, MIL1553Cfg{Bitrate: 1_000_000}); !re.OK || !mbIntEq(re.Bytes, words) {
			t.Errorf("sanity: explicit should decode 0xAAAA stream, got ok=%v bytes=%v", re.OK, re.Bytes)
		}
		ra := DecodeMIL1553(w, ct, MIL1553Cfg{})
		if !ra.OK || !mbIntEq(ra.Bytes, words) {
			// KNOWN BUG: auto bit-period inference doubles T on alternating data.
			t.Errorf("FALSE-NEGATIVE (auto inference): valid 0xAAAA stream missed: ok=%v spb=%.1f err=%q bytes=%v (want %v)",
				ra.OK, ra.SPB, ra.Error, ra.Bytes, words)
		}
	}

	// --- Pinned FALSE-NEGATIVE (b): samples-per-bit == 5, AUTO bit rate. ---
	{
		spb := 5 // legal: minSPB is 4
		ct := 1.0 / (float64(spb) * 1e6)
		wd := 0x1234
		w := mil1553Wave([]int{wd}, []bool{true}, []int{mil1553OddParity(wd)}, spb)
		if re := DecodeMIL1553(w, ct, MIL1553Cfg{Bitrate: 1_000_000}); !re.OK || !mbIntEq(re.Bytes, []int{wd}) {
			t.Errorf("sanity: explicit should decode spb=5 frame, got ok=%v bytes=%v", re.OK, re.Bytes)
		}
		ra := DecodeMIL1553(w, ct, MIL1553Cfg{})
		if !ra.OK || !mbIntEq(ra.Bytes, []int{wd}) {
			// KNOWN BUG: auto inference fails at the coarse spb=5 quantisation.
			t.Errorf("FALSE-NEGATIVE (auto inference): valid spb=5 frame missed: ok=%v spb=%.1f err=%q bytes=%v (want [%d])",
				ra.OK, ra.SPB, ra.Error, ra.Bytes, wd)
		}
	}

	// ---------------------------------------------------------------------
	// CLASS 2 — FALSE POSITIVES: >=50 adversarial inputs must NOT be reported
	// as a confident valid frame, and any DELIBERATELY corrupted parity must be
	// FLAGGED (frame-error), never silently accepted.
	// ---------------------------------------------------------------------
	ctFP := 1.0 / (40.0 * 1e6)
	fpCount := 0
	assertNotConfident := func(name string, w []uint8) {
		fpCount++
		for _, cfg := range []MIL1553Cfg{{Bitrate: 1_000_000}, {}} {
			r := DecodeMIL1553(w, ctFP, cfg)
			if mbConfident(r) {
				mode := "explicit"
				if cfg.Bitrate == 0 {
					mode = "auto"
				}
				t.Errorf("FALSE-POSITIVE: %s (%s) reported a confident valid frame: bytes=%v spans=%d",
					name, mode, r.Bytes, len(r.Spans))
			}
		}
	}

	// (i) 40 pure-noise captures.
	for s := 0; s < 40; s++ {
		g := rand.New(rand.NewSource(int64(1000 + s)))
		n := 400 + g.Intn(3000)
		w := make([]uint8, n)
		for i := range w {
			w[i] = uint8(g.Intn(256))
		}
		assertNotConfident(fmt.Sprintf("noise#%d", s), w)
	}
	// (ii) flat/DC at several levels.
	for _, lvl := range []uint8{0, 40, 128, 210, 255} {
		w := make([]uint8, 2000)
		for i := range w {
			w[i] = lvl
		}
		assertNotConfident(fmt.Sprintf("dc-%d", lvl), w)
	}
	// (iii) slow ramp / sawtooth.
	{
		w := make([]uint8, 3000)
		for i := range w {
			w[i] = uint8(i % 256)
		}
		assertNotConfident("ramp", w)
	}
	// (iv) Nyquist toggle.
	{
		w := make([]uint8, 3000)
		for i := range w {
			w[i] = uint8((i % 2) * 255)
		}
		assertNotConfident("nyquist", w)
	}
	// (v) wrong-protocol square waves of many periods (a clock, not Manchester).
	for _, p := range []int{2, 3, 4, 6, 8, 13, 16, 20, 40, 60, 80, 128} {
		w := make([]uint8, 4000)
		for i := range w {
			if (i/p)%2 == 0 {
				w[i] = 210
			} else {
				w[i] = 40
			}
		}
		assertNotConfident(fmt.Sprintf("square-p%d", p), w)
	}
	// (vi) VALID frame with a CORRUPTED parity bit — must be FLAGGED, and (for a
	// single-word frame) must NOT be confident. This is the core "don't confirm a
	// parity you did not verify" test, swept over many payloads and sync types.
	for s := 0; s < 40; s++ {
		g := rand.New(rand.NewSource(int64(7000 + s)))
		wd := g.Intn(0x10000)
		spb := spbSet[g.Intn(len(spbSet))]
		cmd := g.Intn(2) == 0
		ct := 1.0 / (float64(spb) * 1e6)
		w := mil1553Wave([]int{wd}, []bool{cmd}, []int{mil1553OddParity(wd) ^ 1}, spb) // flipped parity
		r := DecodeMIL1553(w, ct, MIL1553Cfg{Bitrate: 1_000_000})
		if r.OK && mbFrameErrs(r) == 0 {
			t.Errorf("FALSE-POSITIVE: corrupted parity for word %04X (spb=%d) was ACCEPTED with no frame-error", wd, spb)
		}
		if mbConfident(r) {
			t.Errorf("FALSE-POSITIVE: single word %04X with corrupted parity reported confident-valid", wd)
		}
	}
	// (vii) VALID frame TRUNCATED mid-word at several cut points.
	for _, cut := range []int{1, 4, 8, 12, 16} {
		spb := 40
		wd := 0xBEEF
		w := mil1553Wave([]int{wd}, []bool{true}, []int{mil1553OddParity(wd)}, spb)
		keep := (4+3)*spb + cut*spb + spb/2 // lead + sync + cut whole data cells + a half
		if keep > len(w) {
			keep = len(w)
		}
		assertNotConfident(fmt.Sprintf("truncated-%dbits", cut), append([]uint8(nil), w[:keep]...))
	}
	// (viii) VALID frame with one data cell smashed into a coding violation.
	{
		spb := 40
		wd := 0x1234
		w := mil1553Wave([]int{wd}, []bool{true}, []int{mil1553OddParity(wd)}, spb)
		start := (4 + 3 + 5) * spb // 6th data cell
		for i := start; i < start+spb && i < len(w); i++ {
			w[i] = 210 // hold high for the whole cell => no mid transition
		}
		assertNotConfident("data-cell-violation", w)
	}
	if fpCount < 50 {
		t.Errorf("class-2 ran only %d adversarial inputs; need >=50", fpCount)
	}

	// ---------------------------------------------------------------------
	// CLASS 3 — EDGE CASES: boundaries + degenerate inputs. Must never panic
	// and must honour the OK-xor-Error contract.
	// ---------------------------------------------------------------------
	edgeChk := func(name string, w []uint8, ct float64, cfg MIL1553Cfg, wantWords []int) {
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("PANIC in edge case %q (n=%d ct=%g br=%d): %v", name, len(w), ct, cfg.Bitrate, p)
			}
		}()
		r := DecodeMIL1553(w, ct, cfg)
		if !r.OK && r.Error == "" {
			t.Errorf("edge %q: neither OK nor Error set", name)
		}
		if r.OK && r.Error != "" {
			t.Errorf("edge %q: both OK and Error set (%q)", name, r.Error)
		}
		if wantWords != nil {
			if !r.OK || !mbIntEq(r.Bytes, wantWords) {
				t.Errorf("edge %q: got ok=%v bytes=%v want %v (err=%q)", name, r.OK, r.Bytes, wantWords, r.Error)
			}
		}
	}

	// Minimum legal samples-per-bit (4) and a large one (250), explicit.
	for _, spb := range []int{4, 250} {
		ct := 1.0 / (float64(spb) * 1e6)
		for _, wd := range []int{0x0000, 0xFFFF, 0x1234} {
			w := mil1553Wave([]int{wd}, []bool{true}, []int{mil1553OddParity(wd)}, spb)
			edgeChk(fmt.Sprintf("spb%d-%04X", spb, wd), w, ct, MIL1553Cfg{Bitrate: 1_000_000}, []int{wd})
		}
	}
	// Exactly one frame.
	{
		spb := 40
		w := mil1553Wave([]int{0x00FF}, []bool{false}, []int{mil1553OddParity(0x00FF)}, spb)
		edgeChk("one-frame", w, 1.0/(float64(spb)*1e6), MIL1553Cfg{Bitrate: 1_000_000}, []int{0x00FF})
	}
	// Back-to-back words with no inter-word gap (the synthesizer emits words
	// contiguously) — all must decode.
	{
		spb := 40
		words := []int{0x0000, 0xFFFF, 0xA5A5, 0x0F0F}
		cmd := []bool{true, false, true, false}
		par := make([]int, len(words))
		for i, wd := range words {
			par[i] = mil1553OddParity(wd)
		}
		w := mil1553Wave(words, cmd, par, spb)
		edgeChk("back-to-back", w, 1.0/(float64(spb)*1e6), MIL1553Cfg{Bitrate: 1_000_000}, words)
	}
	// All-0x00 and all-0xFF payloads over many words.
	for _, fill := range []int{0x0000, 0xFFFF} {
		spb := 20
		words := make([]int, 8)
		cmd := make([]bool, 8)
		par := make([]int, 8)
		for i := range words {
			words[i] = fill
			cmd[i] = i%2 == 0
			par[i] = mil1553OddParity(fill)
		}
		w := mil1553Wave(words, cmd, par, spb)
		edgeChk(fmt.Sprintf("fill-%04X", fill), w, 1.0/(float64(spb)*1e6), MIL1553Cfg{Bitrate: 1_000_000}, words)
	}
	// Shortest meaningful record (one word, min spb) and a very long record.
	{
		spb := 4
		w := mil1553Wave([]int{0x2A2A}, []bool{true}, []int{mil1553OddParity(0x2A2A)}, spb)
		edgeChk("shortest", w, 1.0/(float64(spb)*1e6), MIL1553Cfg{Bitrate: 1_000_000}, []int{0x2A2A})
	}
	{
		spb := 40
		words := make([]int, 30)
		cmd := make([]bool, 30)
		par := make([]int, 30)
		for i := range words {
			words[i] = (i * 0x0111) & 0xFFFF
			cmd[i] = i%3 == 0
			par[i] = mil1553OddParity(words[i])
		}
		w := mil1553Wave(words, cmd, par, spb)
		edgeChk("very-long-30words", w, 1.0/(float64(spb)*1e6), MIL1553Cfg{Bitrate: 1_000_000}, words)
	}
	// Boundary sample counts.
	edgeChk("nil", nil, 1.0/40e6, MIL1553Cfg{Bitrate: 1_000_000}, nil)
	edgeChk("empty", []uint8{}, 1.0/40e6, MIL1553Cfg{Bitrate: 1_000_000}, nil)
	edgeChk("one-sample", []uint8{200}, 1.0/40e6, MIL1553Cfg{Bitrate: 1_000_000}, nil)
	edgeChk("two-samples", []uint8{200, 40}, 1.0/40e6, MIL1553Cfg{Bitrate: 1_000_000}, nil)

	// >=50 randomized degenerate iterations with hostile ct / bitrate — must not
	// panic and must honour the OK-xor-Error contract.
	cts := []float64{0, -1, -1e-6, 1e-15, 1e-9, 1e9, 1.0 / 40e6}
	brs := []int{0, -100, -1, 1, 2, 1_000_000, 1 << 30, 1 << 62}
	kinds := 5
	edgeIters := 0
	for it := 0; it < 60; it++ {
		n := rng.Intn(2500)
		w := make([]uint8, n)
		switch rng.Intn(kinds) {
		case 0: // noise
			for i := range w {
				w[i] = uint8(rng.Intn(256))
			}
		case 1: // flat
			f := uint8(rng.Intn(256))
			for i := range w {
				w[i] = f
			}
		case 2: // nyquist
			for i := range w {
				w[i] = uint8((i % 2) * 255)
			}
		case 3: // ramp
			for i := range w {
				w[i] = uint8(i % 256)
			}
		case 4: // a real-ish frame, then hostile ct/br
			spb := 4 + rng.Intn(60)
			wd := rng.Intn(0x10000)
			w = mil1553Wave([]int{wd}, []bool{rng.Intn(2) == 0}, []int{mil1553OddParity(wd)}, spb)
		}
		cfg := MIL1553Cfg{
			Bitrate:   brs[rng.Intn(len(brs))],
			Threshold: rng.Float64() * 300,
			HaveThr:   rng.Intn(2) == 0,
		}
		edgeChk(fmt.Sprintf("degen#%d", it), w, cts[rng.Intn(len(cts))], cfg, nil)
		edgeIters++
	}
	if edgeIters < 50 {
		t.Errorf("class-3 ran only %d degenerate iterations; need >=50", edgeIters)
	}
}