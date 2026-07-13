package decode

// SPI vs the sigrok `spi` decoder (clk + mosi, no chip-select). Cases cover
// clean Mode-0 traffic, all four CPOL/CPHA modes, both bit orders, byte
// values that expose off-by-one edge sampling (0x80/0x01), fractional
// samples-per-bit, long idle gaps, back-to-back words, and byte-boundary
// gaps straddling the repo's 1.5x-period word-reset threshold.
//
// FRAMING CAVEAT (deliberate vector design): without CS, sigrok frames words
// purely by counting clock edges — an idle gap in the middle of a word is
// invisible to it. The repo decoder (which never gets a CS channel) instead
// re-frames on idle gaps > 1.5x the typical clock period. The two contracts
// agree whenever gaps fall on word boundaries, so every comparison subtest
// places gaps only between whole bytes; the one mid-word-gap subtest asserts
// each side's own documented behavior instead of equality.

import (
	"fmt"
	"testing"
)

// spiOWord is one SPI word for the oracle generator.
type spiOWord struct {
	v       int
	bits    int     // word length; 0 means 8. <8 emits a PARTIAL word (top bits, MSB-first)
	gapBits float64 // extra clock-idle time AFTER this word, in bit-times
}

// oracleSPIBits renders words on parallel CLK/MOSI logic timelines. The clock
// idles at CPOL; with CPHA=0 data is set up during the idle half-cycle and
// latched on the leading edge, with CPHA=1 data changes on the leading edge
// and is latched on the trailing edge — so the sampling edge is rising iff
// CPOL==CPHA, exactly the repo decoder's tiebreak. Durations accumulate in
// seconds so fractional samples-per-bit behave like a real capture; both
// timelines floor the same time values, keeping the channels sample-aligned.
func oracleSPIBits(sr, bitRate float64, cpol, cpha, msb bool, words []spiOWord) (clk, mosi []byte) {
	ck, da := newTimeline(sr), newTimeline(sr)
	bt := 1 / bitRate
	idle := byte(0)
	if cpol {
		idle = 1
	}
	active := 1 - idle
	seg := func(c, d byte, dur float64) { ck.add(c, dur); da.add(d, dur) }
	seg(idle, 0, 4*bt) // lead idle
	for _, w := range words {
		nb := w.bits
		if nb == 0 {
			nb = 8
		}
		for i := 0; i < nb; i++ {
			k := 7 - i // MSB-first: bit 7 down; partial words send the TOP bits
			if !msb {
				k = i
			}
			b := byte(w.v>>k) & 1
			if !cpha {
				seg(idle, b, bt/2)   // setup while clock inactive
				seg(active, b, bt/2) // latched on the leading edge
			} else {
				seg(active, b, bt/2) // data changes on the leading edge
				seg(idle, b, bt/2)   // latched on the trailing edge
			}
		}
		seg(idle, 0, w.gapBits*bt)
	}
	seg(idle, 0, 4*bt) // trail idle
	return ck.bits, da.bits
}

// spiOWords builds whole-byte words with a uniform inter-word gap (bit-times);
// gap 0 means continuous clocking (back-to-back words).
func spiOWords(gapBits float64, bytes ...int) []spiOWord {
	ws := make([]spiOWord, len(bytes))
	for i, b := range bytes {
		ws[i] = spiOWord{v: b, gapBits: gapBits}
	}
	return ws
}

func TestOracleSPI(t *testing.T) {
	needSigrok(t)
	const sr = 1_000_000
	const rate = 62_500 // 16 samples/bit; well above the repo's 6-col minimum
	const spb = sr / rate

	pd := func(cpol, cpha int, bitorder string) string {
		return fmt.Sprintf("spi:clk=CLK:mosi=MOSI:cpol=%d:cpha=%d:bitorder=%s", cpol, cpha, bitorder)
	}
	// mosi-data annotation texts are plain hex ("A5"); ss is the word's first
	// sampling clock edge — the same sample the repo uses for span I0.
	run := func(clk, mosi []byte, cpol, cpha int, bitorder string) []ann {
		return sigrokDecode(t, sr, []string{"CLK", "MOSI"}, [][]byte{clk, mosi},
			pd(cpol, cpha, bitorder), "spi=mosi-data")
	}

	t.Run("mode0-msb-multibyte", func(t *testing.T) {
		// 0x00 (no data edges at all) and 0xFF (data stuck high) verify the
		// decoder frames on the CLOCK, not on data activity; gaps of 2 bit-times
		// sit on byte boundaries so both framing contracts agree (see header).
		payload := []int{0x00, 0xFF, 0xA5, 0x5A, 0x0F, 0xF0, 0x81, 0x7E}
		clk, mosi := oracleSPIBits(sr, rate, false, false, true, spiOWords(2, payload...))
		r := DecodeSPI(bitsToCodes(clk), bitsToCodes(mosi), 1.0/sr, SPICfg{MSB: true})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		anns := run(clk, mosi, 0, 0, "msb-first")
		eqBytes(t, "mode0 payload vs generated", spanBytes(r, "data"), payload)
		eqBytes(t, "mode0 payload", spanBytes(r, "data"), annBytes(t, anns))
		eqAligned(t, "mode0 data spans", r, "data", anns, spb)
	})

	// All four CPOL/CPHA modes: the repo's sampleRising = (CPOL==CPHA)
	// tiebreak must land each mode on the same clock edge sigrok samples.
	// Back-to-back words (gap 0) so mode-1/3 trailing-edge sampling gets no
	// idle-gap help. 0x80/0x01 included: a one-edge-early/late sampler shifts
	// them into different bytes, so strict eqBytes catches the off-by-one.
	for _, m := range []struct{ cpol, cpha bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		name := fmt.Sprintf("mode-cpol%d-cpha%d", b2i(m.cpol), b2i(m.cpha))
		t.Run(name, func(t *testing.T) {
			payload := []int{0xA5, 0x3C, 0x80, 0x01, 0xFF, 0x00, 0x69}
			clk, mosi := oracleSPIBits(sr, rate, m.cpol, m.cpha, true, spiOWords(0, payload...))
			r := DecodeSPI(bitsToCodes(clk), bitsToCodes(mosi), 1.0/sr,
				SPICfg{CPOL: m.cpol, CPHA: m.cpha, MSB: true})
			if !r.OK {
				t.Fatalf("repo decode failed: %s", r.Error)
			}
			anns := run(clk, mosi, b2i(m.cpol), b2i(m.cpha), "msb-first")
			eqBytes(t, name+" payload vs generated", spanBytes(r, "data"), payload)
			eqBytes(t, name+" payload", spanBytes(r, "data"), annBytes(t, anns))
			eqAligned(t, name+" data spans", r, "data", anns, spb)
		})
	}

	t.Run("lsb-first", func(t *testing.T) {
		// Asymmetric bytes (0x01 vs 0x80, 0x13) so a bit-order mixup cannot
		// cancel out; mode 0.
		payload := []int{0x01, 0x80, 0xA5, 0x13, 0xFE}
		clk, mosi := oracleSPIBits(sr, rate, false, false, false, spiOWords(2, payload...))
		r := DecodeSPI(bitsToCodes(clk), bitsToCodes(mosi), 1.0/sr, SPICfg{MSB: false})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		eqBytes(t, "lsb-first payload vs generated", spanBytes(r, "data"), payload)
		eqBytes(t, "lsb-first payload", spanBytes(r, "data"), annBytes(t, run(clk, mosi, 0, 0, "lsb-first")))
	})

	t.Run("fractional-spb", func(t *testing.T) {
		// 72.9 kbit at 1 MHz = 13.717 samples/bit: sampling-edge gaps alternate
		// 13/14 cols, exercising the repo's clustered period estimate against
		// sigrok's edge-driven decode on the identical jittery wave.
		payload := []int{0x13, 0x37, 0xC0, 0xDE, 0xA5}
		clk, mosi := oracleSPIBits(sr, 72_900, false, false, true, spiOWords(2, payload...))
		r := DecodeSPI(bitsToCodes(clk), bitsToCodes(mosi), 1.0/sr, SPICfg{MSB: true})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		eqBytes(t, "fractional-spb payload vs generated", spanBytes(r, "data"), payload)
		eqBytes(t, "fractional-spb payload", spanBytes(r, "data"), annBytes(t, run(clk, mosi, 0, 0, "msb-first")))
	})

	t.Run("long-idle-gaps-reframe", func(t *testing.T) {
		// 40-bit-time idle gaps between bursts — far beyond the repo's
		// 1.5x-period reset. Gaps sit on byte boundaries, where the repo's
		// re-frame is a no-op and sigrok's clock counting reaches the same
		// boundaries, so payloads must still match exactly.
		words := []spiOWord{
			{v: 0x9A, gapBits: 40}, {v: 0x02}, {v: 0x40, gapBits: 40},
			{v: 0xF1}, {v: 0x1F, gapBits: 40}, {v: 0x55},
		}
		clk, mosi := oracleSPIBits(sr, rate, false, false, true, words)
		r := DecodeSPI(bitsToCodes(clk), bitsToCodes(mosi), 1.0/sr, SPICfg{MSB: true})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		anns := run(clk, mosi, 0, 0, "msb-first")
		if got := countSpans(r, "data"); got != len(words) {
			t.Fatalf("repo decoded %d words across gaps, want %d", got, len(words))
		}
		eqBytes(t, "gap-reframe payload vs generated", spanBytes(r, "data"), []int{0x9A, 0x02, 0x40, 0xF1, 0x1F, 0x55})
		eqBytes(t, "gap-reframe payload", spanBytes(r, "data"), annBytes(t, anns))
		eqAligned(t, "gap-reframe data spans", r, "data", anns, spb)
	})

	t.Run("back-to-back-minimal-gap", func(t *testing.T) {
		// Continuous clocking, zero idle between words: the last sampling edge
		// of one byte and the first of the next are exactly one bit apart, so
		// any spurious gap-reset in the repo would drop or shift bytes.
		payload := []int{0xDE, 0xAD, 0xBE, 0xEF, 0x55, 0xAA, 0x00, 0xFF}
		clk, mosi := oracleSPIBits(sr, rate, false, false, true, spiOWords(0, payload...))
		r := DecodeSPI(bitsToCodes(clk), bitsToCodes(mosi), 1.0/sr, SPICfg{MSB: true})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		anns := run(clk, mosi, 0, 0, "msb-first")
		eqBytes(t, "back-to-back payload vs generated", spanBytes(r, "data"), payload)
		eqBytes(t, "back-to-back payload", spanBytes(r, "data"), annBytes(t, anns))
		eqAligned(t, "back-to-back data spans", r, "data", anns, spb)
	})

	// Near-threshold byte-boundary gaps: the repo's word reset fires when the
	// sampling-edge gap exceeds 1.5x the CLUSTERED clock period (gapReset in
	// DecodeSPI). Every existing case sits far from that boundary — gap 0 is a
	// 1.0x sampling-edge spacing, gap 2 is 3.0x, gap 40 is 41x — so these two
	// straddle it. On byte boundaries a reset is a framing no-op (bitCount is
	// already 0) and sigrok's clock counting reaches the same boundaries, so
	// payloads must be identical on BOTH sides of the threshold; what the
	// near-threshold gaps really stress is the period estimator feeding it:
	// the ~1.2x gaps fall INSIDE its ±0.5-period cluster window and inflate
	// the estimate slightly (period ≈ 16.3 cols here, threshold ≈ 24.5), the
	// 2.0x gaps fall OUTSIDE and must be excluded (period stays 16, threshold
	// 24). A polluted estimate would drop the effective reset below the
	// in-word cadence and shred every byte — the hardware regression
	// documented above gapReset in decode_i2c_spi.go.
	for _, c := range []struct {
		name    string
		gapBits float64 // clock idle after each word, bit-times; boundary sampling-edge gap = (1+gapBits) bit-times
		above   bool    // must the boundary gap land above the reset threshold?
	}{
		// (1+0.2) = 1.2x the bit period: below 1.5x, must NOT re-frame.
		{"byte-gap-1.2x-below-reset", 0.2, false},
		// (1+1.0) = 2.0x: above 1.5x, the reset fires but the byte boundary
		// makes it a no-op — framing must stay coincident with sigrok's.
		{"byte-gap-2.0x-above-reset", 1.0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// 0x80/0x01 sentinels: a one-edge-early/late sampler (or a stray
			// mid-byte reset) shifts them into different byte values.
			payload := []int{0xB2, 0x80, 0x01, 0x6D, 0xC3, 0x2E}
			clk, mosi := oracleSPIBits(sr, rate, false, false, true, spiOWords(c.gapBits, payload...))
			r := DecodeSPI(bitsToCodes(clk), bitsToCodes(mosi), 1.0/sr, SPICfg{MSB: true})
			if !r.OK {
				t.Fatalf("repo decode failed: %s", r.Error)
			}
			// Self-check the vector really straddles the threshold the decoder
			// USED (1.5x r.SPB, the clustered estimate), not a nominal one:
			// span I1 of one byte and I0 of the next are consecutive sampling
			// edges, so their distance IS the boundary sampling-edge gap. If
			// the estimator or generator drifts, fail loudly here instead of
			// silently testing a non-boundary vector.
			gapReset := 1.5 * r.SPB
			var dspans []Span
			for _, s := range r.Spans {
				if s.Kind == "data" {
					dspans = append(dspans, s)
				}
			}
			for i := 1; i < len(dspans); i++ {
				g := float64(dspans[i].I0 - dspans[i-1].I1)
				if c.above != (g > gapReset) {
					t.Fatalf("vector broken: boundary gap %d is %.1f cols vs reset threshold %.2f (want above=%v)",
						i-1, g, gapReset, c.above)
				}
			}
			anns := run(clk, mosi, 0, 0, "msb-first")
			eqBytes(t, c.name+" payload vs generated", spanBytes(r, "data"), payload)
			eqBytes(t, c.name+" payload", spanBytes(r, "data"), annBytes(t, anns))
			eqAligned(t, c.name+" data spans", r, "data", anns, spb)
		})
	}

	t.Run("mid-word-gap-framing-difference", func(t *testing.T) {
		// KNOWN DESIGN DIFFERENCE, not a divergence bug: 4 orphan bits (1111),
		// a 40-bit idle gap, then A5 3C. sigrok (no CS) counts clocks straight
		// through the gap: 1111|1010 -> FA, 0101|0011 -> 53, trailing 1100
		// dropped. The repo, whose only CS substitute IS the gap, resets at the
		// gap: the orphan bits are discarded and A5 3C decode intact. The two
		// contracts only intersect when gaps fall on word boundaries (asserted
		// by every other subtest); here we pin each side's own behavior so any
		// future drift in either contract is caught.
		words := []spiOWord{{v: 0xF0, bits: 4, gapBits: 40}, {v: 0xA5}, {v: 0x3C}}
		clk, mosi := oracleSPIBits(sr, rate, false, false, true, words)
		r := DecodeSPI(bitsToCodes(clk), bitsToCodes(mosi), 1.0/sr, SPICfg{MSB: true})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		eqBytes(t, "repo re-frames on the gap", spanBytes(r, "data"), []int{0xA5, 0x3C})
		eqBytes(t, "sigrok stitches across the gap", annBytes(t, run(clk, mosi, 0, 0, "msb-first")), []int{0xFA, 0x53})
	})
}

// b2i converts a mode flag to the 0/1 sigrok option value.
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
