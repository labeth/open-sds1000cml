// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
)

// MEASURE reads an inverted channel's live envelope with the complement duty,
// as the LCD's readouts and the web do: the envelope's timing comes before the
// invert, which would otherwise be undone.
// TRLC-LINKS: REQ-SDS-217
func TestMeasureInvertedEnvelopeDuty(t *testing.T) {
	n := 2048
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, WinCols: n, SampleS: 96e-9, PeakDetect: true, Ordered: true}
	for b := 0; b < n/2; b++ {
		v := uint8(78)
		if b%104 < 31 {
			v = 178
		}
		f.C1[2*b], f.C1[2*b+1] = v, v
	}
	s := &sv{probe: [2]float64{1, 1}, vidx: [2]int{10, 10}, hasInv: true}
	plain := measureCh(f, s, 0)
	s.inv[0] = true
	inv := measureCh(f, s, 0)
	if plain == nil || inv == nil || math.Abs(plain.Duty-30) > 2 || math.Abs(inv.Duty-70) > 2 {
		t.Fatalf("duty: plain %v, inverted %v", plain, inv)
	}
}

// MEASURE shows no mean or RMS for an envelope of a signal faster than its
// buckets (its (min, max) pairs' midpoint is not the signal's mean).
// TRLC-LINKS: REQ-SDS-217
func TestMeasureAliasedEnvelopeMeanUnknown(t *testing.T) {
	m := &measure.Result{Vmean: 1.6, Vrms: 2.3, VrmsAC: 1.6, Vpp: 3.3, EnvAliased: true}
	for i, p := range measParams {
		switch p.id {
		case "mean", "rms", "acrms":
			if v := measValue(m, i); !math.IsNaN(v) {
				t.Errorf("%s on an aliased envelope: %g", p.id, v)
			}
		case "pkpk":
			if v := measValue(m, i); v != 3.3 {
				t.Errorf("pkpk %g", v)
			}
		}
	}
}

// MEASURE's statistics count a reading once: a live envelope frame's rise
// comes from edge windows carried to the frames between reads, so it counts
// on the frame a read is new to, while each frame's own values count on
// every frame.
// TRLC-LINKS: REQ-SDS-217
func TestStatisticsCountAnEdgeReadingOnce(t *testing.T) {
	n := 2048
	env := make([]uint8, n)
	for b := 0; b < n/2; b++ {
		v := uint8(78)
		if b%104 < 52 {
			v = 178
		}
		env[2*b], env[2*b+1] = v, v
	}
	win := func(ramp int) *engine.EdgeWindow { // a rising edge over ramp samples
		w := make([]uint8, 2048)
		for i := range w {
			switch {
			case i < 1000:
				w[i] = 78
			case i < 1000+ramp:
				w[i] = uint8(78 + 100*(i-1000)/ramp)
			default:
				w[i] = 178
			}
		}
		return &engine.EdgeWindow{C1: w, C2: w, SampleS: 2e-9, Full: true}
	}
	c := &Controller{clock: func() time.Time { return time.Unix(0, 0) }}
	s := &sv{probe: [2]float64{1, 1}, vidx: [2]int{10, 10}, chDisp: [2]bool{true, true},
		meas: []measItem{{0, measIndex("rise")}, {0, measIndex("pkpk")}}}
	first, second := win(10), win(14)
	for seq, w := range []*engine.EdgeWindow{first, first, first, second} {
		read := uint64(1)
		if w == second {
			read = 2
		}
		f := &engine.Frame{Seq: uint64(seq + 1), C1: env, C2: env, Valid: n, WinCols: n, SampleS: 96e-9, PeakDetect: true, Ordered: true,
			Edge: w, EdgeRead: read}
		c.measFrame(&c.ana, s, f)
	}
	rise, pkpk := c.ana.stats[measItem{0, measIndex("rise")}], c.ana.stats[measItem{0, measIndex("pkpk")}]
	if rise == nil || pkpk == nil || rise.N() != 2 || pkpk.N() != 4 {
		t.Fatalf("counted rise %v, pk-pk %v; want 2 readings of the edge, 4 frames", rise, pkpk)
	}
	if !c.ana.allWin[0][measIndex("rise")] {
		t.Fatal("the rise did not come from the edge windows")
	}
}

// The parameters counted once a window read follow what the result took
// from the windows.
// TRLC-LINKS: REQ-SDS-217
func TestWindowParams(t *testing.T) {
	if got := windowParams(&measure.Result{}); got != nil {
		t.Fatalf("nothing from windows: %v", got)
	}
	got := windowParams(&measure.Result{WindowTiming: true, WindowMean: true})
	for _, id := range []string{"freq", "period", "duty", "pwid", "nwid", "mean", "rms", "acrms"} {
		if !got[measIndex(id)] {
			t.Errorf("%s not counted as a window value", id)
		}
	}
	for _, id := range []string{"rise", "pkpk", "max", "top"} {
		if got[measIndex(id)] {
			t.Errorf("%s counted as a window value", id)
		}
	}
	if !windowParams(&measure.Result{EdgeShape: true})[measIndex("prsh")] {
		t.Error("preshoot from the windows not counted as one")
	}
}
