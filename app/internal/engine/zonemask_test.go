package engine

import "testing"

// mkFrame builds a synthetic frame: baseline 100, with a pulse of `code` over
// sample range [pLo,pHi).
func zmFrame(n int, code uint8, pLo, pHi int) *Frame {
	f := &Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n}
	for i := 0; i < n; i++ {
		f.C1[i] = 100
		if i >= pLo && i < pHi {
			f.C1[i] = code
		}
		f.C2[i] = 100
	}
	return f
}

func TestZonesQualify(t *testing.T) {
	e := &Engine{}
	const n = 2048
	const edgeX = 1000.0
	const sampleS = 2e-9
	// pulse at samples 1100..1150 (i.e. +200ns..+300ns after the edge), code 200
	f := zmFrame(n, 200, 1100, 1150)

	cases := []struct {
		name string
		z    Zone
		want bool
	}{
		{"intersect hit", Zone{DtLoS: 150e-9, DtHiS: 350e-9, CodeLo: 180, CodeHi: 255}, true},
		{"intersect miss (wrong codes)", Zone{DtLoS: 150e-9, DtHiS: 350e-9, CodeLo: 220, CodeHi: 255}, false},
		{"intersect miss (wrong time)", Zone{DtLoS: 500e-9, DtHiS: 700e-9, CodeLo: 180, CodeHi: 255}, false},
		{"avoid ok (empty region)", Zone{DtLoS: 500e-9, DtHiS: 700e-9, CodeLo: 180, CodeHi: 255, Avoid: true}, true},
		{"avoid violated (pulse present)", Zone{DtLoS: 150e-9, DtHiS: 350e-9, CodeLo: 180, CodeHi: 255, Avoid: true}, false},
		{"negative dt (before edge, baseline hit)", Zone{DtLoS: -500e-9, DtHiS: -100e-9, CodeLo: 90, CodeHi: 110}, true},
	}
	for _, c := range cases {
		e.SetZones([]Zone{c.z})
		if got := e.zonesQualify(f, n, edgeX, sampleS); got != c.want {
			t.Errorf("%s: qualify=%v want %v", c.name, got, c.want)
		}
	}
	// multiple zones AND together
	e.SetZones([]Zone{
		{DtLoS: 150e-9, DtHiS: 350e-9, CodeLo: 180, CodeHi: 255},              // hit ok
		{DtLoS: 500e-9, DtHiS: 700e-9, CodeLo: 180, CodeHi: 255, Avoid: true}, // avoid ok
	})
	if !e.zonesQualify(f, n, edgeX, sampleS) {
		t.Error("AND of passing zones should qualify")
	}
	e.SetZones([]Zone{
		{DtLoS: 150e-9, DtHiS: 350e-9, CodeLo: 180, CodeHi: 255},
		{DtLoS: 150e-9, DtHiS: 350e-9, CodeLo: 180, CodeHi: 255, Avoid: true}, // contradiction
	})
	if e.zonesQualify(f, n, edgeX, sampleS) {
		t.Error("contradictory zones must not qualify")
	}
	// no zones = qualify
	e.SetZones(nil)
	if !e.zonesQualify(f, n, edgeX, sampleS) {
		t.Error("no zones should qualify")
	}
}

func TestMaskEvalAndRing(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	const n = 2048
	// band-independent test: call maskEval directly with matching WinCols
	win := e.band.WinCols()
	// golden envelope: flat 100 ± 5 everywhere
	lo := make([]uint8, win)
	hi := make([]uint8, win)
	for i := range lo {
		lo[i], hi[i] = 95, 105
	}
	e.SetMask(&Mask{Lo: lo, Hi: hi, WinCols: win, Ch: 0})
	e.SetMaskMode(MaskTest)

	edgeX := float64(n) / 2
	// passing frame: flat 100
	f := zmFrame(n, 100, 0, 0)
	if fail, _ := e.maskEval(f, n, 0, edgeX, 2e-9, 0.5); fail {
		t.Fatal("flat frame inside the envelope must pass")
	}
	// failing frame: a spike to 200 at the window centre
	f2 := zmFrame(n, 200, n/2-4, n/2+4)
	fail, stop := e.maskEval(f2, n, 0, edgeX, 2e-9, 0.5)
	if !fail || stop {
		t.Fatalf("spike must fail without stopping in MaskTest (fail=%v stop=%v)", fail, stop)
	}
	if e.maskFail.Load() != 1 || e.maskPass.Load() != 1 {
		t.Fatalf("counters: pass=%d fail=%d", e.maskPass.Load(), e.maskFail.Load())
	}
	ring := e.MaskFails()
	if len(ring) != 1 || ring[0].Seq != 2 || ring[0].FailCode != 200 {
		t.Fatalf("ring: %+v", ring)
	}
	// stop-on-fail
	e.SetMaskMode(MaskStopFail)
	if _, stop := e.maskEval(f2, n, 0, edgeX, 2e-9, 0.5); !stop {
		t.Fatal("MaskStopFail must request a stop")
	}
	// ring caps at maskRingCap
	for i := 0; i < maskRingCap+4; i++ {
		e.maskEval(f2, n, 0, edgeX, 2e-9, 0.5)
	}
	if len(e.MaskFails()) != maskRingCap {
		t.Fatalf("ring cap: %d", len(e.MaskFails()))
	}
	// WinCols mismatch → not comparable, no counting
	e.ClearMaskFails()
	e.SetMask(&Mask{Lo: lo[:win-1], Hi: hi[:win-1], WinCols: win - 1, Ch: 0})
	if fail, _ := e.maskEval(f2, n, 0, edgeX, 2e-9, 0.5); fail {
		t.Fatal("mismatched WinCols must skip, not fail")
	}
}

func TestBuildMaskFromEnvelope(t *testing.T) {
	win := 100
	lo := make([]uint8, win)
	hi := make([]uint8, win)
	for i := range lo {
		lo[i], hi[i] = 100, 120
	}
	// a step at column 50
	for i := 50; i < win; i++ {
		lo[i], hi[i] = 180, 200
	}
	m := BuildMaskFromEnvelope(lo, hi, win, 2, 5, 0)
	if m == nil {
		t.Fatal("nil mask")
	}
	if m.Lo[10] != 95 || m.Hi[10] != 125 {
		t.Errorf("flat region dilation: [%d,%d]", m.Lo[10], m.Hi[10])
	}
	// near the step, horizontal dilation must open the envelope across it
	if m.Lo[49] != 95 || m.Hi[49] != 205 {
		t.Errorf("step-adjacent dilation: [%d,%d]", m.Lo[49], m.Hi[49])
	}
	if BuildMaskFromEnvelope(lo, hi, win+1, 1, 1, 0) != nil {
		t.Error("length mismatch must return nil")
	}
}
