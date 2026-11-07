// ENGMODEL-OWNER-UNIT: FU-APP-LCD

package lcd

import (
	"math/rand"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// Render fuzz: the LCD renderer runs on the device 20x a second — a panic on
// ANY frame/HUD combination kills the whole app. Storm it with degenerate
// frames (nil, empty, short C2, envelope lies, zero valid) and hostile HUD
// state (huge zoom, out-of-range cursors/menus, mismatched mask geometry).
// TRLC-LINKS: REQ-SDS-021
func TestRenderFuzzNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(31337))
	sf := NewMemSurface()
	persist := NewMemSurface()

	mkFrame := func() *sample.Frame {
		switch rng.Intn(8) {
		case 0:
			return nil
		case 1:
			return &sample.Frame{Traces: []sample.Trace{{}, {Ch: 1}}} // all zero
		case 2: // envelope metadata lies
			return &sample.Frame{
				WinPoints: 2048,
				TriggerAt: 50,
				Flags:     sample.Envelope,
				Traces:    []sample.Trace{{Codes8: make([]uint8, 100), Valid: 100}, {Ch: 1, Codes8: make([]uint8, 100), Valid: 100}},
			}
		case 3: // short C2
			return &sample.Frame{
				WinPoints: 2048,
				TriggerAt: 1024,
				SampleS:   800e-9,
				Traces:    []sample.Trace{{Codes8: make([]uint8, 2048), Valid: 2048}, {Ch: 1, Codes8: make([]uint8, 3), Valid: 2048}},
			}
		case 4: // valid > len
			return &sample.Frame{
				WinPoints: 2048,
				TriggerAt: -1,
				Traces:    []sample.Trace{{Codes8: make([]uint8, 64), Valid: 5000}, {Ch: 1, Codes8: make([]uint8, 64), Valid: 5000}},
			}
		default:
			n := 1 + rng.Intn(6000)
			f := &sample.Frame{
				Traces:    []sample.Trace{{Codes8: make([]uint8, n), Valid: n}, {Ch: 1, Codes8: make([]uint8, n), Valid: n}},
				WinPoints: 1 + rng.Intn(4096), TriggerAt: float64(rng.Intn(2*n)) - float64(n)/2,
				SampleS: []float64{0, 800e-9, 2e-9}[rng.Intn(3)],
			}
			env, cols := rng.Intn(6) == 0, rng.Intn(900)
			f.SourcePtp, f.Status.Triggered = rng.Intn(255), rng.Intn(2) == 0
			f.Flags.Set(sample.Envelope, env)
			f.Flags.Set(sample.Interp, rng.Intn(2) == 0)
			if env {
				f.Traces[0].Min8 = make([]uint8, cols)
				f.Traces[0].Max8 = make([]uint8, cols)
				f.Traces[1].Min8 = f.Min8(0)
				f.Traces[1].Max8 = f.Max8(0)
			}
			for i := range f.Codes8(0) {
				f.Codes8(0)[i] = uint8(rng.Intn(256))
				f.Codes8(1)[i] = uint8(rng.Intn(256))
			}
			return f
		}
	}

	for i := range 400 {
		f := mkFrame()
		maskWin := []int{0, 100, 2048, 4096}[rng.Intn(4)]
		var mlo, mhi []uint8
		if maskWin > 0 && rng.Intn(3) > 0 {
			mlo = make([]uint8, maskWin)
			mhi = make([]uint8, maskWin)
		}
		hud := HUD{
			C1VdivV: []float64{0, 1, 1e-3}[rng.Intn(3)], C2VdivV: 1,
			TdivS: []float64{0, 500e-6}[rng.Intn(2)], SampleS: 800e-9,
			Running: rng.Intn(2) == 0, Norm: rng.Intn(2) == 0, Trigd: rng.Intn(2) == 0,
			TrigPosFrac: []float64{-3, 0, 0.5, 1, 9}[rng.Intn(5)],
			TwoChan:     rng.Intn(2) == 0, ShowC1: rng.Intn(2) == 0, ShowC2: rng.Intn(2) == 0,
			ShowMeas: rng.Intn(2) == 0,
			ViewMode: rng.Intn(5), MathMode: rng.Intn(6), // 0..4 incl. spectrogram
			Zoom: []int{0, 1, 2, 50, 9999}[rng.Intn(5)], ZoomOff: rng.Float64()*4 - 2,
			Persist:  rng.Intn(2) == 0,
			DecProto: rng.Intn(6), DecBaud: []int{0, 115200}[rng.Intn(2)],
			DecChA: rng.Intn(3), DecChB: rng.Intn(3), DecFormat: rng.Intn(4),
			CurOn: rng.Intn(2) == 0, CurType: rng.Intn(3), CurSel: rng.Intn(3),
			CurX:     [2]float64{rng.Float64()*3 - 1, rng.Float64()*3 - 1},
			CurY:     [2]float64{rng.Float64()*3 - 1, rng.Float64()*3 - 1},
			UI:       fuzzView(rng),
			Match:    MatchView{Open: rng.Intn(6) == 0, Keys: [][]string{{"0"}, {}}, Row: rng.Intn(4) - 1, Col: rng.Intn(4) - 1, Toks: []string{"55", "ERR"}, Cur: rng.Intn(5) - 1},
			SRActive: rng.Intn(3) == 0, SRFocus: rng.Intn(5),
			SRMean: make([]float32, rng.Intn(3)*512), SRk: rng.Intn(65),
			SRMean2: make([]float32, rng.Intn(3)*512), SRAlign: rng.Intn(2),
			SRSampleS: []float64{0, -1, 2e-9, 1e-6}[rng.Intn(4)],
			SRGateLo:  rng.Intn(4096) - 100, SRGateHi: rng.Intn(4096) - 100,
			SRWinLo: rng.Intn(4096) - 100, SRWinHi: rng.Intn(4096) - 100, SRPeriod: rng.Intn(300) - 10,
			ZoneMode: rng.Intn(2), MaskMode: rng.Intn(3),
			Zones: []engine.Zone{{
				DtLoS: rng.Float64()*2e-4 - 1e-4, DtHiS: rng.Float64()*2e-4 - 1e-4,
				CodeLo: rng.Intn(400) - 100, CodeHi: rng.Intn(400) - 100, Ch: rng.Intn(2),
			}},
			MaskLo: mlo, MaskHi: mhi, MaskWin: maskWin,
			MaskPass: int64(rng.Intn(1000)), MaskFail: int64(rng.Intn(10)), MaskSkip: int64(rng.Intn(10)),
			MaskStopped: rng.Intn(4) == 0, MaskMsg: []string{"", "MASK: building 9/32"}[rng.Intn(2)],
			RefShow:     [2]bool{rng.Intn(2) == 0, false},
			AutosetBusy: rng.Intn(6) == 0, AutosetMsg: "sweep",
		}
		if rng.Intn(3) == 0 && f != nil && len(f.Codes8(0)) > 0 {
			hud.RefC1[0] = f.Codes8(0) // ref same as live
			hud.RefShow[0] = true
		}
		// Fill the super-res means with real values + occasional -1 gaps so the
		// stacked FFT/X-Y resample+transform paths are exercised (not just their
		// nil/zero guards) under the hostile K/window/period values above.
		fillSR := func(m []float32) {
			for j := range m {
				if rng.Intn(8) == 0 {
					m[j] = -1 // gap
				} else {
					m[j] = float32(rng.Intn(256))
				}
			}
		}
		fillSR(hud.SRMean)
		fillSR(hud.SRMean2)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("iteration %d panicked: %v (frame=%+v)", i, r, f)
				}
			}()
			Render(sf, f, hud, rng.Intn(2) == 0, persist)
		}()
	}
}
