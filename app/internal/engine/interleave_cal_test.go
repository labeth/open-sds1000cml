// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TRLC-LINKS: REQ-SDS-016
func TestInterleaveCalibrationRotationAndGuards(t *testing.T) {
	c := InterleaveCalibration{Vdiv: [2]float64{2, 2}, Offset: [2][5]float64{{3, -6, -7, 5, 5}, {-2, -1, 0, 1, 2}}}
	for r := 0; r < 5; r++ {
		n := 50000
		a := make([]uint8, n)
		b := make([]uint8, n)
		q1 := make([]uint16, n)
		q2 := make([]uint16, n)
		for i := range a {
			base := 100 + (i/5)%50
			a[i] = uint8(base + int(c.Offset[0][(i+r)%5]))
			b[i] = uint8(base + int(c.Offset[1][(i+r)%5]))
		}
		if !c.apply(a, b, q1, q2, [2]float64{2, 2}, 2e-9) {
			t.Fatalf("rotation %d rejected", r)
		}
		for i := range a {
			want := uint16(100+(i/5)%50) * 256
			if q1[i] != want || q2[i] != want {
				t.Fatalf("r%d i%d got %d/%d want%d", r, i, q1[i], q2[i], want)
			}
		}
	}
	n := 50000
	for _, kind := range []string{"scale", "highfrequency", "clipping", "ambiguous"} {
		a := make([]uint8, n)
		b := make([]uint8, n)
		q1 := make([]uint16, n)
		q2 := make([]uint16, n)
		scale := [2]float64{2, 2}
		for i := range a {
			a[i] = uint8(128 + int(c.Offset[0][i%5]))
			b[i] = uint8(128 + int(c.Offset[1][i%5]))
			if kind == "highfrequency" {
				a[i] = uint8(float64(a[i]) + 20*math.Sin(float64(i%5)*2*math.Pi/5))
			}
			if kind == "ambiguous" {
				a[i] = 128
				b[i] = 128
			}
		}
		if kind == "scale" {
			scale[0] = 1
		}
		if kind == "clipping" {
			a[123] = 255
		}
		before := append([]uint8(nil), a...)
		if c.apply(a, b, q1, q2, scale, 2e-9) {
			t.Fatalf("%s accepted", kind)
		}
		for i := range a {
			if a[i] != before[i] || q1[i] != 0 {
				t.Fatal("bypass modified data")
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-016
func TestVerifiedInterleaveRanges(t *testing.T) {
	c := InterleaveCalibration{Vdiv: [2]float64{2, 2}, VerifiedVdiv: [2][]float64{{1, 5, 10}, {1, 5, 10}}}
	for _, scale := range [][2]float64{{1, 2}, {2, 1}, {5, 10}, {10, 1}} {
		if !c.supportsScale(scale) {
			t.Fatal(scale)
		}
	}
	if c.supportsScale([2]float64{.5, 2}) {
		t.Fatal("unmeasured range accepted")
	}
}

// TRLC-LINKS: REQ-SDS-016
func TestInterleaveGainOnlyAtMeasuredScale(t *testing.T) {
	c := InterleaveCalibration{Vdiv: [2]float64{2, 2}, VerifiedVdiv: [2][]float64{{1}, {1}}, Offset: [2][5]float64{{3, -6, -7, 5, 5}, {-2, -1, 0, 1, 2}}, GainVdiv: [2]float64{1, 1}, Gain: [2][5]float64{{1.02, .98, 1, 1, 1}, {.99, 1.01, 1, 1, 1}}, GainOffset: [2][5]float64{{.1, -.1, 0, 0, 0}, {0, 0, .1, -.1, 0}}}
	for _, scale := range [][2]float64{{1, 1}, {2, 2}, {1, 2}} {
		for rotation := 0; rotation < 5; rotation++ {
			a, b := make([]uint8, 5000), make([]uint8, 5000)
			q1, q2 := make([]uint16, 5000), make([]uint16, 5000)
			for ch, x := range [][]uint8{a, b} {
				for i := range x {
					x[i] = uint8(100 + (i/5)%50 + int(c.Offset[ch][(i+rotation)%5]))
				}
			}
			if !c.apply(a, b, q1, q2, scale, 2e-9) {
				t.Fatal("calibration rejected")
			}
			for ch, q := range [][]uint16{q1, q2} {
				for i, v := range q {
					phase := (i + rotation) % 5
					want := float64(100 + (i/5)%50)
					if scale[ch] == 1 {
						want = 128 + (want-128-c.GainOffset[ch][phase])/c.Gain[ch][phase]
					}
					if v != uint16(math.Round(want*256)) {
						t.Fatalf("scale %v ch%d phase%d: got %d want %g", scale, ch, phase, v, want*256)
					}
				}
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-016
func TestApertureCorrectionAgainstKnownSampleTimes(t *testing.T) {
	delays := [5]float64{.1, -.2, .15, -.1, .05}
	for _, hz := range []float64{1e6, 5e6, 25e6, 31.25e6, 50e6} {
		for rotation := 0; rotation < 5; rotation++ {
			q := make([]uint16, 4000)
			codes := make([]uint8, len(q))
			truth := make([]float64, len(q))
			before := 0.0
			for i := range q {
				truth[i] = 128 + 40*math.Sin(2*math.Pi*hz*float64(i)*2e-9)
				v := 128 + 40*math.Sin(2*math.Pi*hz*(float64(i)*2e-9-delays[(i+rotation)%5]*1e-9))
				q[i] = uint16(math.Round(v * 256))
				codes[i] = roundQ8(q[i])
				if i >= 2 && i < len(q)-2 {
					before += math.Pow(float64(q[i])/256-truth[i], 2)
				}
			}
			first, last := q[0], q[len(q)-1]
			correctAperture(q, codes, delays, rotation)
			after := 0.0
			for i := 2; i < len(q)-2; i++ {
				after += math.Pow(float64(q[i])/256-truth[i], 2)
				if codes[i] != roundQ8(q[i]) {
					t.Fatal("preview differs")
				}
			}
			if after >= before*.05 {
				t.Fatalf("%g Hz rotation %d: before %g after %g", hz, rotation, before, after)
			}
			if q[0] != first || q[len(q)-1] != last {
				t.Fatal("boundary changed")
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-016
func TestTimingCalibrationValidationAndEligibility(t *testing.T) {
	c := InterleaveCalibration{Vdiv: [2]float64{1, 1}, GainVdiv: [2]float64{1, 1}, Gain: [2][5]float64{{1, 1, 1, 1, 1}, {1, 1, 1, 1, 1}}, TimingVdiv: [2]float64{1, 1}, TimingNs: [2][5]float64{{.1, -.1, 0, 0, 0}, {0, 0, .1, -.1, 0}}}
	path := filepath.Join(t.TempDir(), "cal.json")
	for _, kind := range []string{"valid", "delay", "scale", "common-delay"} {
		test := c
		switch kind {
		case "delay":
			test.TimingNs[0][0] = 1
		case "scale":
			test.TimingVdiv[0] = 2
		case "common-delay":
			test.TimingNs[0][0] = .2
		}
		b, _ := json.Marshal(test)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadInterleaveCalibration(path)
		if (err == nil) != (kind == "valid") {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	if !c.hasTiming([2]float64{1, 1}, 2e-9) || c.hasTiming([2]float64{2, 2}, 2e-9) || c.hasTiming([2]float64{1, 1}, 32e-9) {
		t.Fatal("timing eligibility")
	}
}
