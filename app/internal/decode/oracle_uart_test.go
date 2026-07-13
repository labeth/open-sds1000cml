package decode

// UART vs the sigrok `uart` decoder. Cases cover the clean path plus the
// edge cases that historically break UART decoders: back-to-back frames,
// fractional samples-per-bit, parity (ok and violated), frame errors (bad
// stop), 7-bit data, all-zeros/all-ones payloads, auto-baud inference
// checked against sigrok running at the true explicit baud (clean AND with
// ring-glitched edges), a BREAK condition, and 9-bit data.

import (
	"fmt"
	"testing"
)

// uframe is one UART frame for the oracle generator.
type uframe struct {
	v          int
	flipParity bool // emit the WRONG parity bit
	badStop    bool // drive the stop bit low (frame error)
	gapBits    float64
}

// oracleUARTBits renders frames at the given rates: LSB-first data, optional
// parity, one stop bit, per-frame idle gap. Timings accumulate in seconds so
// non-integer samples-per-bit behave like a real async capture.
func oracleUARTBits(sr, baud float64, dataBits int, parity string, frames []uframe) []byte {
	w := newTimeline(sr)
	bt := 1 / baud
	w.add(1, 8*bt) // lead idle
	for _, f := range frames {
		w.add(0, bt) // start
		ones := 0
		for i := 0; i < dataBits; i++ {
			b := byte(f.v>>i) & 1
			ones += int(b)
			w.add(b, bt)
		}
		if parity != "none" && parity != "" {
			p := byte(ones & 1) // odd count -> 1 makes total even
			if parity == "odd" {
				p ^= 1
			}
			if f.flipParity {
				p ^= 1
			}
			w.add(p, bt)
		}
		stop := byte(1)
		if f.badStop {
			stop = 0
		}
		w.add(stop, bt)
		w.add(1, (1+f.gapBits)*bt)
	}
	w.add(1, 8*bt) // trail idle
	return w.bits
}

func frames(bytes ...int) []uframe {
	fs := make([]uframe, len(bytes))
	for i, b := range bytes {
		fs[i] = uframe{v: b}
	}
	return fs
}

func TestOracleUART(t *testing.T) {
	needSigrok(t)
	const sr = 1_000_000

	pd := func(baud int, extra string) string {
		return fmt.Sprintf("uart:rx=RX:baudrate=%d%s", baud, extra)
	}
	run := func(bits []byte, baud int, extra string, annClass string) []ann {
		return sigrokDecode(t, sr, []string{"RX"}, [][]byte{bits}, pd(baud, extra), "uart="+annClass)
	}

	t.Run("roundtrip-8n1", func(t *testing.T) {
		payload := []int{0x48, 0x69, 0x00, 0xFF, 0x55, 0xAA, 0x0F, 0xF0, 0x21}
		baud := 62500 // spb 16
		bits := oracleUARTBits(sr, float64(baud), 8, "none", frames(payload...))
		r := DecodeUART(bitsToCodes(bits), 1.0/sr, UARTCfg{Baud: baud})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		anns := run(bits, baud, "", "rx-data")
		eqBytes(t, "8N1 payload vs generated", r.Bytes, payload)
		eqBytes(t, "8N1 payload", r.Bytes, annBytes(t, anns))
		eqAligned(t, "8N1 data spans", r, "data", anns, sr/baud)
	})

	t.Run("back-to-back", func(t *testing.T) {
		payload := []int{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x80, 0x7F}
		baud := 62500
		fs := frames(payload...) // gapBits 0: stop bit runs straight into the next start
		bits := oracleUARTBits(sr, float64(baud), 8, "none", fs)
		r := DecodeUART(bitsToCodes(bits), 1.0/sr, UARTCfg{Baud: baud})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		eqBytes(t, "back-to-back payload vs generated", r.Bytes, payload)
		eqBytes(t, "back-to-back payload", r.Bytes, annBytes(t, run(bits, baud, "", "rx-data")))
	})

	t.Run("fractional-spb", func(t *testing.T) {
		payload := []int{0x13, 0x37, 0xC0, 0xDE}
		baud := 72900 // 13.717 samples/bit at 1 MHz
		bits := oracleUARTBits(sr, float64(baud), 8, "none", frames(payload...))
		r := DecodeUART(bitsToCodes(bits), 1.0/sr, UARTCfg{Baud: baud})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		eqBytes(t, "fractional-spb payload vs generated", r.Bytes, payload)
		eqBytes(t, "fractional-spb payload", r.Bytes, annBytes(t, run(bits, baud, "", "rx-data")))
	})

	t.Run("auto-baud-matches-oracle", func(t *testing.T) {
		payload := []int{0x41, 0x42, 0x43, 0x55, 0x00, 0xFF}
		baud := 38462 // spb 26
		bits := oracleUARTBits(sr, float64(baud), 8, "none", frames(payload...))
		r := DecodeUART(bitsToCodes(bits), 1.0/sr, UARTCfg{}) // Baud 0: infer
		if !r.OK {
			t.Fatalf("repo auto-baud decode failed: %s", r.Error)
		}
		eqBytes(t, "auto-baud payload vs generated", r.Bytes, payload)
		eqBytes(t, "auto-baud payload", r.Bytes, annBytes(t, run(bits, baud, "", "rx-data")))
	})

	for _, parity := range []string{"even", "odd"} {
		t.Run("parity-"+parity+"-clean", func(t *testing.T) {
			payload := []int{0x00, 0x01, 0xFE, 0xFF, 0x69}
			baud := 62500
			bits := oracleUARTBits(sr, float64(baud), 8, parity, frames(payload...))
			r := DecodeUART(bitsToCodes(bits), 1.0/sr, UARTCfg{Baud: baud, Parity: parity})
			if !r.OK {
				t.Fatalf("repo decode failed: %s", r.Error)
			}
			if n := countSpans(r, "parity-error"); n != 0 {
				t.Fatalf("repo flagged %d parity errors on clean traffic", n)
			}
			extra := ":parity=" + parity
			if errs := run(bits, baud, extra, "rx-parity-err"); len(errs) != 0 {
				t.Fatalf("sigrok flagged %d parity errors on clean traffic", len(errs))
			}
			eqBytes(t, parity+" parity payload vs generated", r.Bytes, payload)
			eqBytes(t, parity+" parity payload", r.Bytes, annBytes(t, run(bits, baud, extra, "rx-data")))
		})
	}

	t.Run("parity-error-flagged-by-both", func(t *testing.T) {
		baud := 62500
		fs := frames(0x11, 0x22, 0x33)
		fs[1].flipParity = true
		bits := oracleUARTBits(sr, float64(baud), 8, "even", fs)
		r := DecodeUART(bitsToCodes(bits), 1.0/sr, UARTCfg{Baud: baud, Parity: "even"})
		repoErrs := countSpans(r, "parity-error")
		oracleErrs := run(bits, baud, ":parity=even", "rx-parity-err")
		if repoErrs != 1 || len(oracleErrs) != 1 {
			t.Fatalf("parity error counts differ: repo %d, sigrok %d", repoErrs, len(oracleErrs))
		}
		// Position, not just count: the corrupted frame is index 1 — lead idle
		// (8 bits) + one 12-bit frame (start+8+parity+stop+gap) puts its parity
		// bit 9 bit-times into the second frame. A decoder flagging the WRONG
		// byte must fail here.
		spb := sr / baud
		parityBit := (8 + 12 + 9) * spb
		if a := oracleErrs[0]; a.I0 < parityBit-2*spb || a.I0 > parityBit+2*spb {
			t.Fatalf("sigrok parity error at sample %d, want ~%d (frame 1)", a.I0, parityBit)
		}
		for _, sp := range r.Spans {
			if sp.Kind == "parity-error" && (sp.I0 < (8+12)*spb-2*spb || sp.I0 > (8+2*12)*spb+2*spb) {
				t.Fatalf("repo parity error span at sample %d, outside frame 1 (%d..%d)", sp.I0, (8+12)*spb, (8+2*12)*spb)
			}
		}
	})

	t.Run("frame-error-flagged-by-both", func(t *testing.T) {
		baud := 62500
		fs := frames(0x44, 0x55, 0x66)
		fs[1].badStop = true
		bits := oracleUARTBits(sr, float64(baud), 8, "none", fs)
		r := DecodeUART(bitsToCodes(bits), 1.0/sr, UARTCfg{Baud: baud})
		repoErrs := countSpans(r, "frame-error")
		oracleWarns := run(bits, baud, "", "rx-warnings")
		if repoErrs < 1 || len(oracleWarns) < 1 {
			t.Fatalf("frame error not flagged by both: repo %d, sigrok warnings %d", repoErrs, len(oracleWarns))
		}
		// Position: the bad stop belongs to frame 1 — lead idle (8 bits) + one
		// 11-bit frame (start+8+stop+gap) + 9 bits into the second frame.
		spb := sr / baud
		stopBit := (8 + 11 + 9) * spb
		if a := oracleWarns[0]; a.I0 < stopBit-2*spb || a.I0 > stopBit+2*spb {
			t.Fatalf("sigrok frame-error warning at sample %d, want ~%d (frame 1)", a.I0, stopBit)
		}
		for _, sp := range r.Spans {
			if sp.Kind == "frame-error" && (sp.I0 < (8+11)*spb-2*spb || sp.I0 > (8+2*11)*spb+2*spb) {
				t.Fatalf("repo frame-error span at sample %d, outside frame 1 (%d..%d)", sp.I0, (8+11)*spb, (8+2*11)*spb)
			}
		}
	})

	t.Run("7-bit-data", func(t *testing.T) {
		payload := []int{0x00, 0x7F, 0x41, 0x2A}
		baud := 62500
		bits := oracleUARTBits(sr, float64(baud), 7, "none", frames(payload...))
		r := DecodeUART(bitsToCodes(bits), 1.0/sr, UARTCfg{Baud: baud, Bits: 7})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		eqBytes(t, "7-bit payload vs generated", r.Bytes, payload)
		eqBytes(t, "7-bit payload", r.Bytes, annBytes(t, run(bits, baud, ":data_bits=7", "rx-data")))
	})

	t.Run("9-bit-data", func(t *testing.T) {
		// 9-bit words (both rails, an alternating word, and two mixed values):
		// UARTCfg.Bits allows 1..16 and sigrok data_bits maxes at 9, so 9 is the
		// widest word the two sides can compare. sigrok renders 9-bit hex as
		// three digits ("1FF"), which annBytes parses fine.
		payload := []int{0x000, 0x1FF, 0x155, 0x0AA, 0x123}
		baud := 62500
		bits := oracleUARTBits(sr, float64(baud), 9, "none", frames(payload...))
		r := DecodeUART(bitsToCodes(bits), 1.0/sr, UARTCfg{Baud: baud, Bits: 9})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		anns := run(bits, baud, ":data_bits=9", "rx-data")
		eqBytes(t, "9-bit payload vs generated", r.Bytes, payload)
		eqBytes(t, "9-bit payload", r.Bytes, annBytes(t, anns))
		eqAligned(t, "9-bit data spans", r, "data", anns, sr/baud)
	})

	t.Run("break-condition", func(t *testing.T) {
		// A BREAK — the line held low for 15 bit-times, longer than one full
		// 10-bit frame — between a normal byte and two more normal bytes.
		// KNOWN DESIGN DIFFERENCE, not a divergence bug: sigrok has a dedicated
		// rx-break annotation spanning the whole low run (plus a Frame error
		// warning at the pseudo-frame's stop position); the repo decoder has no
		// break concept — it decodes the break's first 10 bit-times as one
		// all-zeros frame with a bad stop (ONE frame-error span, value 0x00),
		// then resyncs on the next real start edge. Both sides therefore report
		// the byte stream 41 00 42 43; each side's own contract is pinned below
		// so any future drift in either is caught.
		baud := 62500
		spb := sr / baud
		bt := 1.0 / float64(baud)
		w := newTimeline(sr)
		w.add(1, 8*bt) // lead idle
		emit := func(v int) {
			w.add(0, bt) // start
			for i := 0; i < 8; i++ {
				w.add(byte(v>>i)&1, bt)
			}
			w.add(1, 2*bt) // stop + one idle bit
		}
		emit(0x41)
		breakStart := (8 + 11) * spb // lead idle + one 11-bit frame (start+8+stop+gap)
		w.add(0, 15*bt)              // BREAK: low > one full frame time
		w.add(1, 2*bt)               // line recovers to idle
		emit(0x42)
		emit(0x43)
		w.add(1, 8*bt) // trail idle
		bits := w.bits

		r := DecodeUART(bitsToCodes(bits), 1.0/sr, UARTCfg{Baud: baud})
		if !r.OK {
			t.Fatalf("repo decode failed: %s", r.Error)
		}
		// Payloads agree on both sides, including the 0x00 pseudo-frame both
		// carve out of the break, and the post-break bytes decode identically.
		want := []int{0x41, 0x00, 0x42, 0x43}
		danns := run(bits, baud, "", "rx-data")
		eqBytes(t, "break stream vs expected", r.Bytes, want)
		eqBytes(t, "break stream", r.Bytes, annBytes(t, danns))
		// Post-break alignment: repo data spans (0x41, 0x42, 0x43) line up with
		// the matching sigrok rx-data annotations (skip the pseudo-frame's).
		eqAligned(t, "data spans around break", r, "data",
			[]ann{danns[0], danns[2], danns[3]}, spb)

		// Repo contract: exactly one frame-error span anchored at the break's
		// falling edge, and no break-specific span kind.
		if n := countSpans(r, "frame-error"); n != 1 {
			t.Fatalf("repo flagged %d frame errors on the break, want exactly 1", n)
		}
		if n := countSpans(r, "break"); n != 0 {
			t.Fatalf("repo emitted %d break spans — it grew a break concept; strengthen this test", n)
		}
		for _, sp := range r.Spans {
			if sp.Kind == "frame-error" && (sp.I0 < breakStart-2*spb || sp.I0 > breakStart+2*spb) {
				t.Fatalf("repo frame-error span at sample %d, want ~%d (break start)", sp.I0, breakStart)
			}
		}

		// sigrok contract: one rx-break annotation covering the entire low run,
		// one Frame error warning at the pseudo-frame's stop bit.
		brk := run(bits, baud, "", "rx-break")
		if len(brk) != 1 {
			t.Fatalf("sigrok emitted %d rx-break annotations, want 1", len(brk))
		}
		if a := brk[0]; a.I0 < breakStart-2*spb || a.I0 > breakStart+2*spb ||
			a.I1 < breakStart+13*spb || a.I1 > breakStart+17*spb {
			t.Fatalf("sigrok rx-break spans %d-%d, want ~%d-%d", a.I0, a.I1, breakStart, breakStart+15*spb)
		}
		warns := run(bits, baud, "", "rx-warnings")
		if len(warns) != 1 {
			t.Fatalf("sigrok emitted %d rx-warnings, want 1 (pseudo-frame stop)", len(warns))
		}
		if a := warns[0]; a.I0 < breakStart+7*spb || a.I0 > breakStart+11*spb {
			t.Fatalf("sigrok frame-error warning at %d, want ~%d (pseudo-frame stop)", a.I0, breakStart+9*spb)
		}
	})

	t.Run("auto-baud-ring-glitches", func(t *testing.T) {
		// Exercises inferUARTspb's ring-glitch cluster walk (see its doc
		// comment): every real transition gets a deterministic 1-2-sample
		// spurious toggle — rotating through a 1-sample bounce after the edge,
		// a 2-sample bounce, and a 1-sample spur 2 samples BEFORE the edge —
		// the documented "ringy edges" auto-baud killer. Spur gaps (1-2
		// samples) form their own cluster that a naive low-percentile estimate
		// would mistake for the bit width; the cluster walk must discard it
		// and land on the true 25 samples/bit. Glitches stay within 3 samples
		// of an edge, far under half a bit, so sigrok at the true explicit
		// baud (sampling mid-bit) reads the same waveform clean — as does the
		// repo's own mid-bit sampler once the baud is inferred right.
		payload := []int{0x55, 0xA3, 0x0F, 0xC1, 0x7E} // alternation-heavy: many 1-bit gaps
		baud := 40000                                  // spb 25 >= the requested ~25 samples/bit
		clean := oracleUARTBits(sr, float64(baud), 8, "none", frames(payload...))
		glitched := append([]byte(nil), clean...)
		tct := 0
		for i := 1; i < len(clean); i++ {
			if clean[i] == clean[i-1] {
				continue // transitions located on the CLEAN copy so glitches never cascade
			}
			old, nw := clean[i-1], clean[i]
			switch tct % 3 { // bounds always hold at spb 25 (lead/trail idle 8 bits); guard anyway
			case 0: // 1-sample bounce back to the old level right after the edge
				if i+1 < len(glitched) {
					glitched[i+1] = old
				}
			case 1: // 2-sample bounce
				if i+2 < len(glitched) {
					glitched[i+1], glitched[i+2] = old, old
				}
			case 2: // 1-sample pre-spur: the new level flickers before the edge
				if i-2 >= 0 {
					glitched[i-2] = nw
				}
			}
			tct++
		}
		if tct < 20 {
			t.Fatalf("only %d transitions glitched; vector too weak", tct)
		}
		r := DecodeUART(bitsToCodes(glitched), 1.0/sr, UARTCfg{}) // Baud 0: infer from glitched edges
		if !r.OK {
			t.Fatalf("repo auto-baud decode failed on ringy edges: %s", r.Error)
		}
		// The inference must land on the true bit width, not a ring-spur
		// sub-multiple (which would fail below anyway) — pin it directly too.
		if r.SPB < 24 || r.SPB > 26 {
			t.Fatalf("inferred %.2f samples/bit, want ~25", r.SPB)
		}
		if n := countSpans(r, "frame-error") + countSpans(r, "parity-error"); n != 0 {
			t.Fatalf("repo flagged %d errors on clean-payload glitched traffic", n)
		}
		eqBytes(t, "ring-glitch auto-baud payload vs generated", r.Bytes, payload)
		eqBytes(t, "ring-glitch auto-baud payload", r.Bytes, annBytes(t, run(glitched, baud, "", "rx-data")))
	})
}
