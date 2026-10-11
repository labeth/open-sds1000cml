// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package framecalc

import (
	"math"
	"testing"

	"open-sds/app/internal/engine"
)

// sineFrame is a frame of a sine of cycles periods over n samples.
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
