package lcd

import (
	"math/rand"
	"testing"

	"open-sds/app/internal/engine"
)

// Render fuzz: the LCD renderer runs on the device 20x a second — a panic on
// ANY frame/HUD combination kills the whole app. Storm it with degenerate
// frames (nil, empty, short C2, envelope lies, zero valid) and hostile HUD
// state (huge zoom, out-of-range cursors/menus, mismatched mask geometry).
func TestRenderFuzzNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(31337))
	sf := NewMemSurface()
	persist := NewMemSurface()

	mkFrame := func() *engine.Frame {
		switch rng.Intn(8) {
		case 0:
			return nil
		case 1:
			return &engine.Frame{} // all zero
		case 2: // envelope metadata lies
			return &engine.Frame{
				C1: make([]uint8, 100), C2: make([]uint8, 100), Valid: 100,
				IsEnv: true, EnvCols: 800, // env arrays nil!
				WinCols: 2048, EdgeX: 50,
			}
		case 3: // short C2
			return &engine.Frame{
				C1: make([]uint8, 2048), C2: make([]uint8, 3), Valid: 2048,
				WinCols: 2048, EdgeX: 1024, SampleS: 800e-9,
			}
		case 4: // valid > len
			return &engine.Frame{
				C1: make([]uint8, 64), C2: make([]uint8, 64), Valid: 5000,
				WinCols: 2048, EdgeX: -1,
			}
		default:
			n := 1 + rng.Intn(6000)
			f := &engine.Frame{
				C1: make([]uint8, n), C2: make([]uint8, n), Valid: n,
				WinCols: 1 + rng.Intn(4096), EdgeX: float64(rng.Intn(2*n)) - float64(n)/2,
				SampleS: []float64{0, 800e-9, 2e-9}[rng.Intn(3)],
				IsEnv:   rng.Intn(6) == 0, EnvCols: rng.Intn(900),
				Ptp: rng.Intn(255), Trigd: rng.Intn(2) == 0, Interp: rng.Intn(2) == 0,
			}
			if f.IsEnv {
				f.EnvMin = make([]uint8, f.EnvCols)
				f.EnvMax = make([]uint8, f.EnvCols)
				f.EnvMin2 = f.EnvMin
				f.EnvMax2 = f.EnvMax
			}
			for i := range f.C1 {
				f.C1[i] = uint8(rng.Intn(256))
				f.C2[i] = uint8(rng.Intn(256))
			}
			return f
		}
	}

	for i := 0; i < 400; i++ {
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
			ViewMode: rng.Intn(4), MathMode: rng.Intn(6),
			Zoom: []int{0, 1, 2, 50, 9999}[rng.Intn(5)], ZoomOff: rng.Float64()*4 - 2,
			Persist:  rng.Intn(2) == 0,
			DecProto: rng.Intn(6), DecBaud: []int{0, 115200}[rng.Intn(2)],
			DecChA: rng.Intn(3), DecChB: rng.Intn(3), DecFormat: rng.Intn(4),
			CurOn: rng.Intn(2) == 0, CurType: rng.Intn(3), CurSel: rng.Intn(3),
			CurX:     [2]float64{rng.Float64()*3 - 1, rng.Float64()*3 - 1},
			CurY:     [2]float64{rng.Float64()*3 - 1, rng.Float64()*3 - 1},
			MenuOpen: rng.Intn(2) == 0, MenuTitle: "FUZZ", MenuSel: rng.Intn(9) - 2,
			MenuItems: make([]MenuItem, rng.Intn(9)),
			SRActive:  rng.Intn(3) == 0, SRFocus: rng.Intn(5),
			SRMean: make([]float32, rng.Intn(3)*512), SRk: rng.Intn(65),
			SRMean2: make([]float32, rng.Intn(3)*512), SRAlign: rng.Intn(2),
			SRSampleS: []float64{0, -1, 2e-9, 1e-6}[rng.Intn(4)],
			SRGateLo: rng.Intn(4096) - 100, SRGateHi: rng.Intn(4096) - 100,
			SRWinLo: rng.Intn(4096) - 100, SRWinHi: rng.Intn(4096) - 100, SRPeriod: rng.Intn(300) - 10,
			ZoneMode: rng.Intn(2), MaskMode: rng.Intn(3),
			Zones: []engine.Zone{{DtLoS: rng.Float64()*2e-4 - 1e-4, DtHiS: rng.Float64()*2e-4 - 1e-4,
				CodeLo: rng.Intn(400) - 100, CodeHi: rng.Intn(400) - 100, Ch: rng.Intn(2)}},
			MaskLo: mlo, MaskHi: mhi, MaskWin: maskWin,
			MaskPass: int64(rng.Intn(1000)), MaskFail: int64(rng.Intn(10)), MaskSkip: int64(rng.Intn(10)),
			MaskStopped: rng.Intn(4) == 0, MaskMsg: []string{"", "MASK: building 9/32"}[rng.Intn(2)],
			RefShow:     [2]bool{rng.Intn(2) == 0, false},
			AutosetBusy: rng.Intn(6) == 0, AutosetMsg: "sweep",
		}
		if rng.Intn(3) == 0 && f != nil && len(f.C1) > 0 {
			hud.RefC1[0] = f.C1 // ref same as live
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
