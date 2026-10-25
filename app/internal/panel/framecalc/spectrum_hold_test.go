// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package framecalc

import (
	"math"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// holdFrame is a 4096-sample tone frame with sequence number seq.
// TRLC-LINKS: REQ-SDS-070
func holdFrame(seq uint64, cycles float64) *engine.Frame {
	c := make([]uint8, 4096)
	for i := range c {
		c[i] = uint8(128 + 90*math.Sin(2*math.Pi*cycles*float64(i)/4096))
	}
	return &engine.Frame{Seq: seq, C1: c, C2: c, Valid: len(c), WinCols: len(c), SampleS: 2e-9, TdivS: 1e-6}
}

// Held, a later frame of the same acquisition gets the spectrum taken
// before (until the hold lapses); Spectrum and an unheld display always
// take the frame's own.
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-216
func TestSpectrumHeld(t *testing.T) {
	defer SetSpectrumHold(0)
	a, b := holdFrame(101, 100), holdFrame(102, 300)
	own := func(f *engine.Frame) int { sp, _ := Spectrum(f, 0, f.SampleS); return sp.PeakK }
	ka, kb := own(a), own(b)
	if ka == kb {
		t.Fatalf("test frames share their peak bin %d", ka)
	}
	SetSpectrumHold(0)
	SpectrumHeld(a, 0, a.SampleS)
	if sp, _ := SpectrumHeld(b, 0, b.SampleS); sp.PeakK != kb {
		t.Fatalf("unheld: peak bin %d, want the frame's own %d", sp.PeakK, kb)
	}
	SetSpectrumHold(time.Hour)
	SpectrumHeld(a, 0, a.SampleS)
	if sp, _ := SpectrumHeld(b, 0, b.SampleS); sp.PeakK != ka {
		t.Fatalf("held: peak bin %d, want the held %d", sp.PeakK, ka)
	}
	if sp, _ := Spectrum(b, 0, b.SampleS); sp.PeakK != kb {
		t.Fatalf("Spectrum while held: peak bin %d, want the frame's own %d", sp.PeakK, kb)
	}
	SetSpectrumHold(time.Nanosecond)
	SpectrumHeld(a, 0, a.SampleS)
	time.Sleep(time.Millisecond)
	if sp, _ := SpectrumHeld(b, 0, b.SampleS); sp.PeakK != kb {
		t.Fatalf("lapsed hold: peak bin %d, want the frame's own %d", sp.PeakK, kb)
	}
}
