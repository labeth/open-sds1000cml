// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package framecalc

import (
	"math"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// sineFrame is a frame of a sine of cycles periods over n samples.
// TRLC-LINKS: REQ-SDS-216
func sineFrame(n int, cycles, amp float64, seq uint64) *engine.Frame {
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, SampleS: 1e-6, Seq: seq}
	for i := range f.C1 {
		f.C1[i] = uint8(128 + amp*math.Sin(2*math.Pi*cycles*float64(i)/float64(n)))
		f.C2[i] = 128
	}
	return f
}

// The spectrum's peak sits at the tone's bin, its level reads the tone's
// amplitude (A*N/4 through the Hann window), and a second request for the
// same frame is the cached result.
// TRLC-LINKS: REQ-SDS-216
func TestSpectrumPeakAndCache(t *testing.T) {
	f := sineFrame(4096, 64, 80, 7)
	sp, ok := Spectrum(f, 0, f.SampleS)
	if !ok || sp.N != 4096 {
		t.Fatalf("spectrum: ok=%v N=%d", ok, sp.N)
	}
	if sp.PeakK != 64 {
		t.Fatalf("peak bin %d, want 64", sp.PeakK)
	}
	if amp := 4 * sp.Peak / float64(sp.N); math.Abs(amp-80) > 2 {
		t.Fatalf("tone amplitude %.1f codes, want 80", amp)
	}
	if hz := float64(sp.PeakK) * sp.BinHz; math.Abs(hz-64/(4096*1e-6)) > 1 {
		t.Fatalf("peak at %.1f Hz", hz)
	}
	again, _ := Spectrum(f, 0, f.SampleS)
	if &again.Mags[0] != &sp.Mags[0] {
		t.Fatal("the same frame's spectrum was computed again")
	}
	if _, ok := Spectrum(&engine.Frame{C1: make([]uint8, 8), Valid: 8}, 0, 1e-6); ok {
		t.Fatal("8 samples made a spectrum")
	}
}

// Decode is shared: the strip and the packet list asking for the same frame
// get one decode; a different format is a different decode.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func TestDecodeCacheShared(t *testing.T) {
	f := sineFrame(2048, 4, 100, 11)
	p := DecodeParams{Proto: ProtoUART, Baud: 115200, Format: "hex"}
	a := Decode(f, p)
	b := Decode(f, p)
	if len(a.Spans) != len(b.Spans) || a.Error != b.Error {
		t.Fatal("the cached decode differs")
	}
	if r := Decode(f, DecodeParams{}); r.OK || len(r.Spans) != 0 {
		t.Fatal("decode off returned spans")
	}
}

// A decimated capture's spectrum ends at its filter's bandwidth: above it the
// bins hold the stopband (a 32 ns capture filtered at 4.2 MHz drawn to
// 15.6 MHz shows a square's harmonics above 4.2 MHz gone). An
// unfiltered frame keeps its whole Nyquist.
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-216
func TestSpectrumEndsAtTheFilterBand(t *testing.T) {
	f := sineFrame(4096, 64, 80, 21)
	f.SampleS = 32e-9
	full, _ := Spectrum(f, 0, f.SampleS)
	if full.SpanHz != full.EffNyq || len(full.Mags) != full.N/2 {
		t.Fatalf("unfiltered: span %g of Nyquist %g, %d bins", full.SpanHz, full.EffNyq, len(full.Mags))
	}
	g := sineFrame(4096, 64, 80, 22)
	g.SampleS, g.BandwidthHz = 32e-9, 0.1346/32e-9 // the precision filter's -3 dB: 4.2 MHz at 32 ns
	sp, ok := Spectrum(g, 0, g.SampleS)
	if !ok || math.Abs(sp.SpanHz-g.BandwidthHz) > sp.BinHz || len(sp.Mags) >= sp.N/2 {
		t.Fatalf("filtered: span %g, want %g; %d of %d bins", sp.SpanHz, g.BandwidthHz, len(sp.Mags), sp.N/2)
	}
	if sp.EffNyq != full.EffNyq || sp.PeakK != 64 {
		t.Fatalf("Nyquist %g (want %g), peak bin %d", sp.EffNyq, full.EffNyq, sp.PeakK)
	}
}

// A frame the hardware UART matcher triggered carries where the matched
// character's stop bit lay (Frame.MatchEndS after EdgeX): the decode frames
// the characters back to back with it from there, where a record opening
// inside the traffic read "A5 ER ER ER" on the owner's AA trigger.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestDecodeFramesFromTheHardwareMatch(t *testing.T) {
	const spb = 20
	var w []uint8
	bit := func(b int) {
		for j := 0; j < spb; j++ {
			w = append(w, uint8(40+170*b))
		}
	}
	for _, c := range []int{0x48, 0x69, 0x20, 0x55, 0xAA, 0x0F, 0xF0, 0x0A} {
		bit(0)
		for k := 0; k < 8; k++ {
			bit(c >> k & 1)
		}
		bit(1)
	}
	cut := 2*10*spb + 3*spb // the record opens inside 20
	rec := append([]uint8(nil), w[cut:]...)
	aaStart := float64(4*10*spb - cut)
	f := &engine.Frame{C1: rec, C2: rec, Valid: len(rec), SampleS: 1 / (115200.0 * spb), Seq: 7,
		EdgeX: aaStart, MatchEndS: 9.5 / 115200, HasMatchEnd: true}
	r := Decode(f, DecodeParams{Proto: ProtoUART, Baud: 115200, Format: "hex"})
	if !strings.Contains(r.Text, "55 AA 0F F0") || strings.Contains(r.Text, "!") {
		t.Fatalf("decoded %q, want 55 AA 0F F0 in sync", r.Text)
	}
}
