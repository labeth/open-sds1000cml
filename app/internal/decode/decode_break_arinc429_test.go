package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestBreakArinc429 red-teams DecodeARINC429 across three attack classes. It
// reuses the synthesizer helpers from decode_arinc429_test.go (arincMakeWord,
// arincExpect, arincAppendWord, arincIdle) to build bipolar RZ pulse trains
// (NULL=128, HI=210, LO=40) at a chosen samples-per-bit (spb) and colTimeS.
//
// FINDINGS (see the t.Errorf messages for repros):
//   - FALSE-POSITIVE (class 2d): dense random NOISE is accepted as a CONFIDENT
//     valid ARINC 429 word (OK=true, intact odd parity, no frame-error span) in
//     ~1-2% of records. Root cause: the decoder bins every detected pulse into a
//     32-slot window (round((pulse.i-s0)/T)) and only requires all 32 slots to be
//     filled — it never checks that the pulse COUNT is ~32 or that pulses are
//     actually one-per-cell. Dense noise makes one big segment whose slots all
//     get filled by the nearest crossings; the 1-bit parity then passes ~50% of
//     the time. This is the classic weak-integrity false accept for a parity-only
//     protocol, exposed here by the missing pulse-density sanity check.
//
// The false-negative and edge classes PASS: the round-trip is exact across the
// whole legal bit-rate range and single-bit corruption is always flagged.
func TestBreakArinc429(t *testing.T) {
	frameErrs := func(r Result) int {
		c := 0
		for _, sp := range r.Spans {
			if sp.Kind == "frame-error" {
				c++
			}
		}
		return c
	}
	// A "confident valid" ARINC frame = decoded OK with intact parity/framing
	// (no frame-error span) over at least one word. That is what a parity/framing
	// protocol must NEVER report over non-signal or a corrupted frame.
	confidentValid := func(r Result) bool {
		if !r.OK || len(r.Bytes) == 0 {
			return false
		}
		return frameErrs(r) == 0
	}
	collect := func(r Result) (labels, datas, ssms []string) {
		for _, sp := range r.Spans {
			switch sp.Kind {
			case "addr":
				labels = append(labels, sp.Text)
			case "data":
				datas = append(datas, sp.Text)
			case "rw":
				ssms = append(ssms, sp.Text)
			}
		}
		return
	}
	randFrag := func(rng *rand.Rand, w *[]uint8, spb int) {
		pf := 1 + rng.Intn(31) // 1..31 bits -> can never fill a 32-slot word
		frag := make([]int, pf)
		for i := range frag {
			frag[i] = rng.Intn(2)
		}
		arincAppendWord(w, frag, spb)
	}

	// =====================================================================
	// CLASS 1: FALSE NEGATIVES — >=50 fully VALID frames must decode exactly.
	// Vary spb (4..64), bit rate, word count, idle gaps, leading/trailing
	// partial fragments (a real capture starts at a random phase), and the
	// decode mode (explicit bitrate / auto-infer / explicit NULL threshold).
	// =====================================================================
	t.Run("false_negatives", func(t *testing.T) {
		rng := rand.New(rand.NewSource(0xA429F1))
		bitrates := []int{12500, 25000, 50000, 100000}
		for it := 0; it < 60; it++ {
			spb := 4 + rng.Intn(61) // 4..64 samples/bit (T>=minSPB=4)
			bitrate := bitrates[rng.Intn(len(bitrates))]
			if rng.Intn(3) == 0 {
				bitrate = 10000 + rng.Intn(190000) // arbitrary legal rate
			}
			// colTimeS chosen so the decoder's T == spb EXACTLY on both paths.
			colTimeS := 1.0 / (float64(spb) * float64(bitrate))
			nWords := 1 + rng.Intn(4)

			var w []uint8
			var wantLabels, wantDatas, wantSSMs []string
			var wantBytes []int
			arincIdle(&w, rng.Intn(5*spb+1)) // realistic leading idle (may be 0)
			if rng.Intn(2) == 0 {            // leading partial fragment -> must be dropped
				randFrag(rng, &w, spb)
				arincIdle(&w, (3+rng.Intn(4))*spb) // proper inter-segment gap
			}
			for wi := 0; wi < nWords; wi++ {
				bits := arincMakeWord(rng.Intn(256), rng.Intn(4), rng.Intn(1<<19), rng.Intn(4))
				l, d, s, b := arincExpect(bits)
				wantLabels = append(wantLabels, l)
				wantDatas = append(wantDatas, d)
				wantSSMs = append(wantSSMs, s)
				wantBytes = append(wantBytes, b...)
				arincAppendWord(&w, bits, spb)
				if wi < nWords-1 {
					arincIdle(&w, (3+rng.Intn(4))*spb) // inter-word gap (>2.5*T)
				}
			}
			if rng.Intn(2) == 0 { // trailing partial fragment -> must be dropped
				arincIdle(&w, (3+rng.Intn(4))*spb)
				randFrag(rng, &w, spb)
			}
			arincIdle(&w, rng.Intn(5*spb+1)) // realistic trailing idle

			var cfg ARINC429Cfg
			switch it % 3 {
			case 0:
				cfg = ARINC429Cfg{Bitrate: bitrate} // explicit bit rate
			case 1:
				cfg = ARINC429Cfg{} // auto-infer from pulse spacing
			case 2:
				cfg = ARINC429Cfg{Bitrate: bitrate, Threshold: 128, HaveThr: true} // explicit NULL
			}
			r := DecodeARINC429(w, colTimeS, cfg)
			if !r.OK {
				t.Errorf("FN it=%d spb=%d br=%d nWords=%d mode=%d: valid frame FAILED: %s",
					it, spb, bitrate, nWords, it%3, r.Error)
				continue
			}
			gl, gd, gs := collect(r)
			if got := len(r.Bytes) / 4; got != nWords {
				t.Errorf("FN it=%d spb=%d: recovered %d words, want %d (labels=%v)", it, spb, got, nWords, gl)
				continue
			}
			if fmt.Sprint(gl) != fmt.Sprint(wantLabels) {
				t.Errorf("FN it=%d spb=%d: labels got %v want %v", it, spb, gl, wantLabels)
			}
			if fmt.Sprint(gd) != fmt.Sprint(wantDatas) {
				t.Errorf("FN it=%d spb=%d: data got %v want %v", it, spb, gd, wantDatas)
			}
			if fmt.Sprint(gs) != fmt.Sprint(wantSSMs) {
				t.Errorf("FN it=%d spb=%d: ssm got %v want %v", it, spb, gs, wantSSMs)
			}
			if fmt.Sprint(r.Bytes) != fmt.Sprint(wantBytes) {
				t.Errorf("FN it=%d spb=%d: bytes got %v want %v", it, spb, r.Bytes, wantBytes)
			}
			if fe := frameErrs(r); fe != 0 {
				t.Errorf("FN it=%d spb=%d: %d spurious frame-errors on valid words", it, spb, fe)
			}
		}
	})

	// =====================================================================
	// CLASS 2: FALSE POSITIVES — adversarial inputs must NOT be confirmed as
	// a valid frame with intact parity.
	// =====================================================================
	t.Run("false_positives", func(t *testing.T) {
		// (a) A valid word with a single deliberately-corrupted bit MUST be
		// flagged (frame-error), never silently accepted with intact parity.
		rngA := rand.New(rand.NewSource(0xA429F2))
		for it := 0; it < 60; it++ {
			spb := 8 + rngA.Intn(40)
			bitrate := 100000
			colTimeS := 1.0 / (float64(spb) * float64(bitrate))
			bits := arincMakeWord(rngA.Intn(256), rngA.Intn(4), rngA.Intn(1<<19), rngA.Intn(4))
			flip := rngA.Intn(32)
			bits[flip] ^= 1 // single-bit corruption -> odd parity violated
			var w []uint8
			arincIdle(&w, 4*spb)
			arincAppendWord(&w, bits, spb)
			arincIdle(&w, 4*spb)
			r := DecodeARINC429(w, colTimeS, ARINC429Cfg{Bitrate: bitrate})
			if r.OK && frameErrs(r) == 0 {
				t.Errorf("FP(corrupt) it=%d flip-bit=%d: corrupted word ACCEPTED with intact parity (no frame-error): text=%q", it, flip, r.Text)
			}
		}

		// (b) A valid frame TRUNCATED to a partial word (<32 pulses) must not
		// decode as valid.
		rngB := rand.New(rand.NewSource(0xA429F3))
		for it := 0; it < 60; it++ {
			spb := 8 + rngB.Intn(40)
			bitrate := 100000
			colTimeS := 1.0 / (float64(spb) * float64(bitrate))
			bits := arincMakeWord(rngB.Intn(256), rngB.Intn(4), rngB.Intn(1<<19), rngB.Intn(4))
			var w []uint8
			leadN := 4 * spb
			arincIdle(&w, leadN)
			arincAppendWord(&w, bits, spb)
			keep := 1 + rngB.Intn(31) // keep only the first <32 bit cells
			cut := leadN + keep*spb
			if cut > len(w) {
				cut = len(w)
			}
			w = w[:cut]
			r := DecodeARINC429(w, colTimeS, ARINC429Cfg{Bitrate: bitrate})
			if confidentValid(r) {
				t.Errorf("FP(truncate) it=%d keep=%d: partial word decoded as valid: text=%q bytes=%v", it, keep, r.Text, r.Bytes)
			}
		}

		// (c) Non-signal shapes (flat DC / slow ramp / Nyquist toggle / wrong-
		// protocol square wave) must not produce a confident valid word.
		rngC := rand.New(rand.NewSource(0xA429F4))
		shapeNames := []string{"flatDC", "ramp", "nyquist", "square"}
		for it := 0; it < 60; it++ {
			n := 400 + rngC.Intn(3000)
			s := make([]uint8, n)
			kind := it % 4
			switch kind {
			case 0:
				lvl := uint8(rngC.Intn(256))
				for i := range s {
					s[i] = lvl
				}
			case 1:
				for i := range s {
					s[i] = uint8(i * 255 / max(1, n-1))
				}
			case 2:
				for i := range s {
					s[i] = uint8(i % 2 * 255)
				}
			case 3:
				period := 8 + rngC.Intn(60)
				for i := range s {
					if (i/(period/2))%2 == 0 {
						s[i] = 210
					} else {
						s[i] = 40
					}
				}
			}
			r := DecodeARINC429(s, 1e-7, ARINC429Cfg{Bitrate: 100000})
			if confidentValid(r) {
				t.Errorf("FP(nonsignal) it=%d %s: decoded as valid ARINC word: text=%q", it, shapeNames[kind], r.Text)
			}
		}

		// (d) Dense random NOISE. Strict integrity: a parity/framing protocol
		// must NOT confirm a valid word (intact odd parity, no frame-error) over
		// pure noise. KNOWN BUG — the decoder does (see file header). Reported via
		// t.Errorf so the rest of the suite still runs.
		rngD := rand.New(rand.NewSource(0xBADC0DE))
		const iters = 300
		confident := 0
		firstRepro := ""
		for it := 0; it < iters; it++ {
			n := 400 + rngD.Intn(3600)
			s := make([]uint8, n)
			for i := range s {
				s[i] = uint8(rngD.Intn(256))
			}
			// Fixed T = (1/100000)/1e-6 = 10 samples/bit.
			r := DecodeARINC429(s, 1e-6, ARINC429Cfg{Bitrate: 100000})
			if confidentValid(r) {
				confident++
				if firstRepro == "" {
					firstRepro = fmt.Sprintf("it=%d n=%d words=%d text=%q bytes=%v", it, n, len(r.Bytes)/4, r.Text, r.Bytes)
				}
			}
		}
		if confident > 0 {
			t.Errorf("FALSE-POSITIVE: %d/%d dense-random-noise records decoded as CONFIDENT valid ARINC 429 words "+
				"(OK=true, intact odd parity, no frame-error). The decoder bins all pulses into 32 slots without a "+
				"pulse-density/one-per-cell sanity check, so noise manufactures a word and the 1-bit parity passes "+
				"~50%% of the time. first repro: %s", confident, iters, firstRepro)
		}
	})

	// =====================================================================
	// CLASS 3: EDGE — extremes must round-trip or fail cleanly, never panic.
	// =====================================================================
	t.Run("edge", func(t *testing.T) {
		roundTripOne := func(spb, bitrate, label, sdi, data, ssm int, useAuto bool) (Result, [3]string) {
			colTimeS := 1.0 / (float64(spb) * float64(bitrate))
			bits := arincMakeWord(label, sdi, data, ssm)
			l, d, sTxt, _ := arincExpect(bits)
			var w []uint8
			arincIdle(&w, 5*spb)
			arincAppendWord(&w, bits, spb)
			arincIdle(&w, 5*spb)
			cfg := ARINC429Cfg{Bitrate: bitrate}
			if useAuto {
				cfg = ARINC429Cfg{}
			}
			return DecodeARINC429(w, colTimeS, cfg), [3]string{l, d, sTxt}
		}
		checkOne := func(name string, r Result, want [3]string) {
			if !r.OK {
				t.Errorf("edge %s: decode failed: %s", name, r.Error)
				return
			}
			gl, gd, gs := collect(r)
			if len(gl) != 1 || gl[0] != want[0] || gd[0] != want[1] || gs[0] != want[2] {
				t.Errorf("edge %s: got %v/%v/%v want %v", name, gl, gd, gs, want)
			}
		}
		// Minimum legal bit rate (spb=4 -> T=minSPB).
		r, want := roundTripOne(4, 100000, 0312, 1, 0x2ABCD, 3, false)
		checkOne("min-spb4", r, want)
		// Large spb / very slow bit rate, auto-inferred.
		r, want = roundTripOne(250, 4000, 0107, 2, 0x15A3F, 1, true)
		checkOne("large-spb250-auto", r, want)
		// All-0x00 payload/fields.
		r, want = roundTripOne(20, 100000, 0, 0, 0, 0, false)
		checkOne("all-zero", r, want)
		// All-ones fields / 0x7FFFF data.
		r, want = roundTripOne(20, 100000, 0xFF, 3, 0x7FFFF, 3, false)
		checkOne("all-ones", r, want)

		// Back-to-back words with NO inter-word gap (not valid ARINC framing):
		// must not panic and must not emit a partial/garbage byte count.
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("edge back-to-back panicked: %v", p)
				}
			}()
			spb, bitrate := 16, 100000
			colTimeS := 1.0 / (float64(spb) * float64(bitrate))
			var w []uint8
			arincIdle(&w, 5*spb)
			arincAppendWord(&w, arincMakeWord(0312, 1, 0x11111, 3), spb)
			arincAppendWord(&w, arincMakeWord(0107, 2, 0x22222, 1), spb) // no gap
			arincIdle(&w, 5*spb)
			r := DecodeARINC429(w, colTimeS, ARINC429Cfg{Bitrate: bitrate})
			if r.OK && len(r.Bytes)%4 != 0 {
				t.Errorf("edge back-to-back: bytes len %d not a multiple of 4", len(r.Bytes))
			}
		}()

		// Boundary sample counts (0,1,2, tiny) and degenerate configs (huge /
		// negative / zero colTimeS and bit rate): never panic, always OK or Error.
		degenerate := [][]uint8{nil, {}, {200}, {40, 210}, {128, 128}, {128}}
		rngE := rand.New(rand.NewSource(0xA429F5))
		for i := 0; i < 60; i++ {
			s := make([]uint8, rngE.Intn(40))
			for j := range s {
				s[j] = uint8(rngE.Intn(256))
			}
			degenerate = append(degenerate, s)
		}
		cts := []float64{0, -1, 1e-12, 2.5e-7, 1, 1e9}
		brs := []int{0, -100, 1, 100000, 1 << 30}
		for _, in := range degenerate {
			for _, ct := range cts {
				for _, br := range brs {
					func() {
						defer func() {
							if p := recover(); p != nil {
								t.Errorf("edge PANIC n=%d ct=%g br=%d: %v", len(in), ct, br, p)
							}
						}()
						r := DecodeARINC429(in, ct, ARINC429Cfg{Bitrate: br})
						if !r.OK && r.Error == "" {
							t.Errorf("edge n=%d ct=%g br=%d: returned neither OK nor Error", len(in), ct, br)
						}
					}()
				}
			}
		}
	})
}
