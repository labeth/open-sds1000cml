package engine

import (
	"math"
	"testing"
)

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
		if !c.apply(a, b, q1, q2, [2]float64{2, 2}) {
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
		if c.apply(a, b, q1, q2, scale) {
			t.Fatalf("%s accepted", kind)
		}
		for i := range a {
			if a[i] != before[i] || q1[i] != 0 {
				t.Fatal("bypass modified data")
			}
		}
	}
}

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
