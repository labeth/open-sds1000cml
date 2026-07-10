package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// ---------------------------------------------------------------------------
// Red-team suite for DecodeSPI (app/internal/decode/decode_i2c_spi.go).
//
// SPI here has NO chip-select and NO CRC/parity: it is a "checksum-less" shape.
// The decoder slices CLK+DATA, requires >=2 CLK edges, samples DATA on the
// rising edge when CPOL==CPHA else the falling edge, derives the typical clock
// period from the SAMPLING-edge gap cluster (must be >=6 cols), and re-frames
// on an idle gap ( > 1.5x that typical period while a byte is partially
// assembled ). Because there is no integrity field, "false positive" for SPI
// is limited to: flat/DC or degenerate clock must NOT yield a confident
// (OK && bytes>0) decode, and a frame TRUNCATED mid-byte must not have the
// partial byte reported.
//
// This file does not modify the decoder; it only documents true behaviour.
// ---------------------------------------------------------------------------

// (removed duplicate func eqInts — provided by the decoder / another test)

// spiSynth builds a fully-valid multi-frame SPI capture.
//
//	frames       : each inner slice is one back-to-back run of bytes
//	h            : samples per clock half-period (the decoder's halfGap)
//	msb          : bit order (true = MSB-first, matching cfg.MSB)
//	sampleRising : decoder samples on the rising edge (CPOL==CPHA)
//	lead/mid/trail: idle samples before / between / after frames
//
// For each data bit we emit [inactive h][active h] on CLK with DATA held
// stable across the whole bit; the inactive->active transition is the sample
// edge and lands h samples after DATA settled, so recovery is unambiguous.
func spiSynth(frames [][]int, h int, msb, sampleRising bool, lead, mid, trail int) (clk, data []uint8, want []int) {
	lo, hi := uint8(40), uint8(210)
	var inact, act uint8
	if sampleRising {
		inact, act = lo, hi
	} else {
		inact, act = hi, lo
	}
	seg := func(c, d uint8, n int) {
		for i := 0; i < n; i++ {
			clk = append(clk, c)
			data = append(data, d)
		}
	}
	seg(inact, lo, lead)
	for fi, fr := range frames {
		if fi > 0 {
			seg(inact, lo, mid)
		}
		for _, b := range fr {
			for j := 0; j < 8; j++ {
				k := j
				if msb {
					k = 7 - j
				}
				dv := lo
				if (b>>k)&1 == 1 {
					dv = hi
				}
				seg(inact, dv, h) // setup half: DATA stable, no sample
				seg(act, dv, h)   // active half: sample edge at its first sample
			}
			want = append(want, b&0xff)
		}
	}
	seg(inact, lo, trail)
	return
}

// modeCfg maps a desired sampleRising to a random legal (CPOL,CPHA) pair so we
// exercise all four SPI modes, and stitches in bit order + format.
func modeCfg(rnd *rand.Rand, sampleRising, msb bool) SPICfg {
	var cpol, cpha bool
	if sampleRising { // cpol == cpha
		if rnd.Intn(2) == 0 {
			cpol, cpha = false, false
		} else {
			cpol, cpha = true, true
		}
	} else { // cpol != cpha
		if rnd.Intn(2) == 0 {
			cpol, cpha = false, true
		} else {
			cpol, cpha = true, false
		}
	}
	return SPICfg{CPOL: cpol, CPHA: cpha, MSB: msb, Format: "hex"}
}

func safeDecodeSPI(t *testing.T, tag string, clk, data []uint8, ct float64, cfg SPICfg) (r Result) {
	defer func() {
		if e := recover(); e != nil {
			t.Errorf("PANIC in %s: %v", tag, e)
		}
	}()
	return DecodeSPI(clk, data, ct, cfg)
}

func TestBreakSpi(t *testing.T) {
	// ===================================================================
	// CLASS 1 — FALSE NEGATIVES: >=50 fully valid frames must round-trip.
	// ===================================================================
	t.Run("FalseNegatives", func(t *testing.T) {
		rnd := rand.New(rand.NewSource(0x5F1FA15E))
		const iters = 60
		fails := 0
		for it := 0; it < iters; it++ {
			nFrames := 1 + rnd.Intn(3)
			frames := make([][]int, nFrames)
			for f := 0; f < nFrames; f++ {
				nb := 1 + rnd.Intn(8)
				fr := make([]int, nb)
				for b := 0; b < nb; b++ {
					fr[b] = rnd.Intn(256)
				}
				frames[f] = fr
			}
			h := 3 + rnd.Intn(22) // 3..24 samples/half-bit (halfGap)
			msb := rnd.Intn(2) == 0
			sampleRising := rnd.Intn(2) == 0
			lead := rnd.Intn(6 * h)                    // realistic random idle before
			trail := rnd.Intn(6 * h)                   // ...and after
			mid := rnd.Intn(2) * (h*5 + rnd.Intn(4*h)) // sometimes a big inter-frame gap
			ct := 2e-7

			clk, data, want := spiSynth(frames, h, msb, sampleRising, lead, mid, trail)
			cfg := modeCfg(rnd, sampleRising, msb)
			tag := fmt.Sprintf("FN it=%d h=%d msb=%v rising=%v frames=%v", it, h, msb, sampleRising, frames)
			r := safeDecodeSPI(t, tag, clk, data, ct, cfg)
			if !r.OK {
				t.Errorf("FALSE-NEGATIVE (ok=false) %s: err=%q", tag, r.Error)
				fails++
				continue
			}
			if !eqInts(r.Bytes, want) {
				t.Errorf("FALSE-NEGATIVE (payload) %s: got %v want %v", tag, r.Bytes, want)
				fails++
			}
		}
		if fails == 0 {
			t.Logf("all %d valid frames round-tripped", iters)
		}
	})

	// ===================================================================
	// CLASS 2 — FALSE POSITIVES: >=50 non-frames must not decode as a
	// confident (OK && bytes>0) SPI frame; truncated frames must drop the
	// partial byte. Noise-yields-bytes is inherent to a checksum-less shape
	// and is NOT asserted (only that it never panics).
	// ===================================================================
	t.Run("FalsePositives", func(t *testing.T) {
		rnd := rand.New(rand.NewSource(0x0FF5E7))
		const iters = 66
		for it := 0; it < iters; it++ {
			cat := it % 6
			ct := 2e-7
			switch cat {
			case 0: // flat / DC on both channels
				lvl := uint8(20 + rnd.Intn(200))
				n := 500 + rnd.Intn(1500)
				clk := make([]uint8, n)
				data := make([]uint8, n)
				for i := range clk {
					clk[i], data[i] = lvl, uint8(20+rnd.Intn(200))
				}
				// data varies but clk is flat -> no clock -> must not decode.
				r := safeDecodeSPI(t, fmt.Sprintf("FP flat it=%d", it), clk, data, ct, SPICfg{MSB: true})
				if r.OK && len(r.Bytes) > 0 {
					t.Errorf("FALSE-POSITIVE (flat clk) it=%d: OK with %d bytes %v", it, len(r.Bytes), r.Bytes)
				}
			case 1: // Nyquist toggle: an edge every sample -> halfGap<3
				n := 800 + rnd.Intn(800)
				clk := make([]uint8, n)
				data := make([]uint8, n)
				for i := range clk {
					if i%2 == 0 {
						clk[i], data[i] = 40, 210
					} else {
						clk[i], data[i] = 210, 40
					}
				}
				r := safeDecodeSPI(t, fmt.Sprintf("FP nyquist it=%d", it), clk, data, ct, SPICfg{MSB: true})
				if r.OK && len(r.Bytes) > 0 {
					t.Errorf("FALSE-POSITIVE (nyquist) it=%d: OK with %d bytes", it, len(r.Bytes))
				}
			case 2: // slow full-scale ramp on the clock -> at most one crossing
				n := 600 + rnd.Intn(600)
				clk := make([]uint8, n)
				data := make([]uint8, n)
				for i := range clk {
					clk[i] = uint8(i * 255 / (n - 1))
					data[i] = uint8((i * 255 / (n - 1)) ^ 0x55)
				}
				r := safeDecodeSPI(t, fmt.Sprintf("FP ramp it=%d", it), clk, data, ct, SPICfg{MSB: true})
				if r.OK && len(r.Bytes) > 0 {
					t.Errorf("FALSE-POSITIVE (ramp) it=%d: OK with %d bytes", it, len(r.Bytes))
				}
			case 3: // pure random noise: must not panic (bytes are inherent, not asserted)
				n := 400 + rnd.Intn(1200)
				clk := make([]uint8, n)
				data := make([]uint8, n)
				for i := range clk {
					clk[i], data[i] = uint8(rnd.Intn(256)), uint8(rnd.Intn(256))
				}
				_ = safeDecodeSPI(t, fmt.Sprintf("FP noise it=%d", it), clk, data, ct, SPICfg{MSB: rnd.Intn(2) == 0})
			case 4: // valid frame TRUNCATED mid-byte: partial byte must be dropped
				nb := 1 + rnd.Intn(4)
				full := make([]int, nb)
				for b := range full {
					full[b] = rnd.Intn(256)
				}
				h := 4 + rnd.Intn(10)
				msb := rnd.Intn(2) == 0
				sampleRising := rnd.Intn(2) == 0
				// Keep a non-whole-byte number of sample cells: (nb-1) whole bytes
				// plus 1..7 dangling bits, so the last byte is never completed.
				extra := 1 + rnd.Intn(7)
				keepBits := (nb-1)*8 + extra
				tclk, tdata, _ := spiSynthBits(full, keepBits, h, msb, sampleRising, h*4)
				cfg := modeCfg(rnd, sampleRising, msb)
				r := safeDecodeSPI(t, fmt.Sprintf("FP trunc it=%d", it), tclk, tdata, ct, cfg)
				wantBytes := keepBits / 8
				if len(r.Bytes) != wantBytes {
					t.Errorf("FALSE-POSITIVE (truncation) it=%d: kept %d bits -> got %d bytes %v, want %d",
						it, keepBits, len(r.Bytes), r.Bytes, wantBytes)
				}
			case 5: // corrupted data bit — SPI has no integrity field, so it is
				// UNDETECTABLE by design. Assert only that it does not panic and
				// still returns; the mutated byte proves corruption is silent.
				nb := 1 + rnd.Intn(3)
				full := make([]int, nb)
				for b := range full {
					full[b] = rnd.Intn(256)
				}
				h := 5 + rnd.Intn(8)
				msb := rnd.Intn(2) == 0
				sampleRising := rnd.Intn(2) == 0
				clk, data, want := spiSynth([][]int{full}, h, msb, sampleRising, h*4, 0, h*4)
				// Flip one DATA sample region hard (invert an interior chunk).
				if len(data) > 4*h {
					s := 2 * h
					for i := s; i < s+h && i < len(data); i++ {
						if data[i] > 128 {
							data[i] = 40
						} else {
							data[i] = 210
						}
					}
				}
				cfg := modeCfg(rnd, sampleRising, msb)
				r := safeDecodeSPI(t, fmt.Sprintf("FP corrupt it=%d", it), clk, data, ct, cfg)
				if r.OK && eqInts(r.Bytes, want) {
					// Corruption happened to not change a sampled bit; fine.
					_ = r
				}
			}
		}
	})

	// ===================================================================
	// CLASS 3 — EDGE CASES: extremes must be sane and never panic.
	// ===================================================================
	t.Run("EdgeCases", func(t *testing.T) {
		rnd := rand.New(rand.NewSource(0xED9E5))

		// Degenerate/boundary sample counts must not panic and must not decode.
		for _, n := range []int{0, 1, 2, 3, 7} {
			clk := make([]uint8, n)
			data := make([]uint8, n)
			for i := 0; i < n; i++ {
				clk[i] = uint8(40 + 170*(i%2))
				data[i] = uint8(40 + 170*((i/1)%2))
			}
			r := safeDecodeSPI(t, fmt.Sprintf("edge n=%d", n), clk, data, 2e-7, SPICfg{MSB: true})
			if r.OK && len(r.Bytes) > 0 {
				t.Errorf("edge n=%d decoded %d bytes from a %d-sample record", n, len(r.Bytes), n)
			}
		}
		// nil slices.
		_ = safeDecodeSPI(t, "edge nil", nil, nil, 2e-7, SPICfg{MSB: true})
		_ = safeDecodeSPI(t, "edge one-nil", nil, []uint8{40, 210, 40}, 2e-7, SPICfg{MSB: true})

		// Pathological colTimeS values (unused by DecodeSPI, but must not panic).
		{
			clk, data, want := spiSynth([][]int{{0x3C, 0xA5}}, 8, true, true, 40, 0, 40)
			for _, ct := range []float64{0, -1e-6, 1e30, -1e30} {
				r := safeDecodeSPI(t, fmt.Sprintf("edge ct=%g", ct), clk, data, ct, SPICfg{MSB: true})
				if !r.OK || !eqInts(r.Bytes, want) {
					t.Errorf("edge ct=%g: ok=%v bytes=%v want=%v err=%q", ct, r.OK, r.Bytes, want, r.Error)
				}
			}
		}

		// Minimum bit rate (h=3), one frame, both bit orders, both edge senses.
		for _, msb := range []bool{true, false} {
			for _, rising := range []bool{true, false} {
				want := []int{0xC3}
				clk, data, w := spiSynth([][]int{want}, 3, msb, rising, 9, 0, 9)
				cfg := modeCfg(rnd, rising, msb)
				r := safeDecodeSPI(t, fmt.Sprintf("edge minrate msb=%v rising=%v", msb, rising), clk, data, 2e-7, cfg)
				if !r.OK || !eqInts(r.Bytes, w) {
					t.Errorf("edge min-rate msb=%v rising=%v: ok=%v bytes=%v want=%v err=%q", msb, rising, r.OK, r.Bytes, w, r.Error)
				}
			}
		}

		// Maximum-ish bit rate (large h), all-0xFF payload. With a low-idling
		// DATA line the idle->0xFF edges give the slicer its amplitude, so an
		// all-0xFF transfer decodes correctly.
		{
			want := []int{0xFF, 0xFF, 0xFF, 0xFF}
			clk, data, w := spiSynth([][]int{want}, 60, true, true, 120, 0, 120)
			r := safeDecodeSPI(t, "edge maxrate pat=FF", clk, data, 2e-7, SPICfg{MSB: true})
			if !r.OK || !eqInts(r.Bytes, w) {
				t.Errorf("edge max-rate pat=FF: ok=%v bytes=%v want=%v err=%q", r.OK, r.Bytes, w, r.Error)
			}
		}
		// FINDING (edge / borderline false-negative): an all-0x00 payload over a
		// low-idling DATA line NEVER toggles DATA, so sliceChannel reports
		// "DATA flat/no transitions" and the whole transfer is undecodable even
		// though CLK cleanly delimits every byte. Pinned as the decoder's TRUE
		// behaviour (no bytes, OK=false). A constant MOSI line is real (idle-low
		// + all-zero payload), so this is a genuine recovery gap: with only
		// CLK+DATA and no reference the slicer needs amplitude on DATA.
		{
			want := []int{0x00, 0x00, 0x00, 0x00}
			clk, data, _ := spiSynth([][]int{want}, 60, true, true, 120, 0, 120)
			r := safeDecodeSPI(t, "edge maxrate pat=00", clk, data, 2e-7, SPICfg{MSB: true})
			if r.OK || len(r.Bytes) != 0 {
				t.Errorf("edge pat=00 expected undecodable flat-DATA, got ok=%v bytes=%v", r.OK, r.Bytes)
			}
			if r.Error == "" {
				t.Errorf("edge pat=00: expected a flat-DATA error, got none")
			}
			t.Logf("DOCUMENTED LIMITATION: all-0x00 (constant-low DATA) -> ok=%v err=%q (undecodable)", r.OK, r.Error)
		}

		// Exactly one frame, one byte.
		{
			clk, data, w := spiSynth([][]int{{0x81}}, 10, true, true, 0, 0, 0)
			r := safeDecodeSPI(t, "edge one-byte", clk, data, 2e-7, SPICfg{MSB: true})
			if !r.OK || !eqInts(r.Bytes, w) {
				t.Errorf("edge one-byte: ok=%v bytes=%v want=%v err=%q", r.OK, r.Bytes, w, r.Error)
			}
		}

		// Back-to-back frames with NO inter-frame gap.
		{
			clk, data, w := spiSynth([][]int{{0x11, 0x22}, {0x33, 0x44}}, 8, true, true, 40, 0, 40)
			r := safeDecodeSPI(t, "edge back2back", clk, data, 2e-7, SPICfg{MSB: true})
			if !r.OK || !eqInts(r.Bytes, w) {
				t.Errorf("edge back2back: ok=%v bytes=%v want=%v err=%q", r.OK, r.Bytes, w, r.Error)
			}
		}

		// A very long record.
		{
			long := make([]int, 1500)
			for i := range long {
				long[i] = (i * 7) & 0xff
			}
			clk, data, w := spiSynth([][]int{long}, 4, true, true, 16, 0, 16)
			r := safeDecodeSPI(t, "edge long", clk, data, 2e-7, SPICfg{MSB: true})
			if !r.OK || !eqInts(r.Bytes, w) {
				t.Errorf("edge long: ok=%v got %d bytes want %d err=%q", r.OK, len(r.Bytes), len(w), r.Error)
			}
		}

		// Randomized edge loop to exceed 50 iterations, mixing tiny valid frames
		// and random small records; every call must be panic-free.
		for it := 0; it < 50; it++ {
			if rnd.Intn(2) == 0 {
				nb := 1 + rnd.Intn(3)
				fr := make([]int, nb)
				for b := range fr {
					fr[b] = rnd.Intn(256)
				}
				h := 3 + rnd.Intn(20)
				msb := rnd.Intn(2) == 0
				rising := rnd.Intn(2) == 0
				clk, data, w := spiSynth([][]int{fr}, h, msb, rising, rnd.Intn(4*h), 0, rnd.Intn(4*h))
				cfg := modeCfg(rnd, rising, msb)
				r := safeDecodeSPI(t, fmt.Sprintf("edge-rand valid it=%d", it), clk, data, 2e-7, cfg)
				if !r.OK || !eqInts(r.Bytes, w) {
					t.Errorf("edge-rand valid it=%d: ok=%v bytes=%v want=%v err=%q", it, r.OK, r.Bytes, w, r.Error)
				}
			} else {
				n := rnd.Intn(60)
				clk := make([]uint8, n)
				data := make([]uint8, n)
				for i := range clk {
					clk[i], data[i] = uint8(rnd.Intn(256)), uint8(rnd.Intn(256))
				}
				_ = safeDecodeSPI(t, fmt.Sprintf("edge-rand junk it=%d n=%d", it, n), clk, data, 2e-7, SPICfg{MSB: rnd.Intn(2) == 0})
			}
		}

		// ---- Robustness probe (informational): asymmetric clock duty. The old
		// gapReset=3*min-edge-gap heuristic mis-fired on skewed duty; the
		// sampling-cadence reset handles it (asserted in
		// TestBreakSpiSamplingCadence/AsymmetricDuty). Logged here for context.
		for _, duty := range [][2]int{{6, 6}, {4, 8}, {3, 9}, {2, 12}} {
			want := []int{0x5A, 0x3C}
			clk, data := spiWaveAsym(want, duty[0], duty[1])
			r := safeDecodeSPI(t, fmt.Sprintf("probe duty %d/%d", duty[0], duty[1]), clk, data, 2e-7, SPICfg{MSB: true})
			t.Logf("asym-duty setup=%d active=%d -> ok=%v bytes=%v (want %v)", duty[0], duty[1], r.OK, r.Bytes, want)
		}
	})
}

// TestBreakSpiSamplingCadence pins the HW-found byte-framing bug: on a real
// rebuilt 2 MHz clock the SAMPLING-edge gaps ran 374-376 cols while the frame
// reset was derived as 3x the minimum gap over ALL edges — a single narrow
// half-cycle (the partial clock cycle at the record start, 124 cols here) put
// that reset at 372 < every real inter-bit gap, so the byte assembly restarted
// on every bit and a clean capture decoded to 0 bytes. The fix derives the
// reset from the typical SAMPLING-edge cadence, so this exact shape must now
// round-trip. Also covers heavy duty-cycle asymmetry (setup 2 / active 12),
// which mis-fired the old min-gap reset the same way.
func TestBreakSpiSamplingCadence(t *testing.T) {
	lo, hi := uint8(40), uint8(210)
	t.Run("PartialFirstCycle374vs372", func(t *testing.T) {
		want := []int{0xA7, 0x3C, 0x81}
		var clk, data []uint8
		seg := func(c, d uint8, n int) {
			for i := 0; i < n; i++ {
				clk = append(clk, c)
				data = append(data, d)
			}
		}
		// Partial first clock cycle: HIGH for 124 cols (capture began mid-cycle),
		// then settle low. The 124-col half-cycle is the record's MINIMUM edge gap.
		seg(hi, lo, 124)
		seg(lo, lo, 187)
		// Mode-0 MSB bits at ~375 cols/period with ±1-col jitter: setup 187/188,
		// active 188 — sampling-edge gaps land on 374..376 like the real capture.
		bit := 0
		for _, b := range want {
			for k := 7; k >= 0; k-- {
				dv := lo
				if (b>>uint(k))&1 == 1 {
					dv = hi
				}
				seg(lo, dv, 187+bit%2) // setup half (187 or 188)
				seg(hi, dv, 188)       // active half; rising edge = sample
				bit++
			}
		}
		seg(lo, lo, 400)
		r := safeDecodeSPI(t, "sampling-cadence 374vs372", clk, data, 2e-7, SPICfg{MSB: true})
		if !r.OK || !eqInts(r.Bytes, want) {
			t.Errorf("HW-shape regression: ok=%v bytes=%v want=%v err=%q (min all-edge gap 124 must not derive the frame reset)",
				r.OK, r.Bytes, want, r.Error)
		}
		if r.OK && (r.SPB < 370 || r.SPB > 380) {
			t.Errorf("SPB (cols/clock) = %.1f, want ~375 from the sampling cadence", r.SPB)
		}
	})
	t.Run("AsymmetricDuty", func(t *testing.T) {
		// setup 2 / active 12: old reset = 3*min-gap = 6 < period 14 -> reset every
		// bit -> 0 bytes (the old suite could only LOG this). Now asserted fixed.
		want := []int{0x5A, 0x3C}
		clk, data := spiWaveAsym(want, 2, 12)
		r := safeDecodeSPI(t, "asym duty 2/12", clk, data, 2e-7, SPICfg{MSB: true})
		if !r.OK || !eqInts(r.Bytes, want) {
			t.Errorf("asymmetric duty 2/12: ok=%v bytes=%v want=%v err=%q", r.OK, r.Bytes, want, r.Error)
		}
	})
	t.Run("IdleGapStillReframes", func(t *testing.T) {
		// The new typical-cadence reset must still re-align on a real idle gap:
		// a partial burst (capture began mid-byte), idle, then a whole frame.
		frames := [][]int{{0x11, 0x22}, {0x33, 0x44}}
		clk, data, _ := spiSynth(frames, 8, true, true, 32, 8*10, 32)
		cut := 8*2*3 + 8 // slice off 3.5 bits so the first burst is misaligned
		r := safeDecodeSPI(t, "idle reframe", clk[cut:], data[cut:], 2e-7, SPICfg{MSB: true})
		if !r.OK || len(r.Bytes) < 2 || !eqInts(r.Bytes[len(r.Bytes)-2:], frames[1]) {
			t.Errorf("idle-gap reframe: ok=%v bytes=%v want tail %v err=%q", r.OK, r.Bytes, frames[1], r.Error)
		}
	})
}

// spiSynthBits synthesizes CLK/DATA for exactly `bits` sample-cells drawn MSB or
// LSB from the byte slice, used to build TRUNCATED (non-whole-byte) captures.
func spiSynthBits(bytes []int, bits, h int, msb, sampleRising bool, lead int) (clk, data []uint8, emitted int) {
	lo, hi := uint8(40), uint8(210)
	var inact, act uint8
	if sampleRising {
		inact, act = lo, hi
	} else {
		inact, act = hi, lo
	}
	seg := func(c, d uint8, n int) {
		for i := 0; i < n; i++ {
			clk = append(clk, c)
			data = append(data, d)
		}
	}
	seg(inact, lo, lead)
	emitted = 0
	for _, b := range bytes {
		for j := 0; j < 8; j++ {
			if emitted >= bits {
				seg(inact, lo, lead) // trailing idle, no more sample edges
				return
			}
			k := j
			if msb {
				k = 7 - j
			}
			dv := lo
			if (b>>k)&1 == 1 {
				dv = hi
			}
			seg(inact, dv, h)
			seg(act, dv, h)
			emitted++
		}
	}
	seg(inact, lo, lead)
	return
}

// spiWaveAsym builds a Mode-0 MSB-first capture with an asymmetric clock duty:
// `setup` samples low (data set up) then `active` samples high (sampled on the
// rising edge). Used only by the informational duty probe.
func spiWaveAsym(bytes []int, setup, active int) (clk, data []uint8) {
	lo, hi := uint8(40), uint8(210)
	seg := func(c, d uint8, n int) {
		for i := 0; i < n; i++ {
			clk = append(clk, c)
			data = append(data, d)
		}
	}
	seg(lo, lo, (setup+active)*4)
	for _, b := range bytes {
		for k := 7; k >= 0; k-- {
			dv := lo
			if (b>>k)&1 == 1 {
				dv = hi
			}
			seg(lo, dv, setup)
			seg(hi, dv, active)
		}
	}
	seg(lo, lo, (setup+active)*4)
	return
}
