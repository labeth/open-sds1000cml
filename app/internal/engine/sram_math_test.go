package engine

import "testing"

func TestERESQ8(t *testing.T) {
	for _, n := range []int{1, 2, 9, 100} {
		src := make([]uint16, n)
		want := make([]uint16, n)
		for i := range src {
			src[i] = uint16((i*173 + 257) % 65536)
		}
		for _, l := range []int{3, 15, 63} {
			in := append([]uint16(nil), src...)
			for i := range src {
				sum, count := uint32(0), uint32(0)
				for j := i - l/2; j <= i+l/2; j++ {
					if j >= 0 && j < n {
						sum += uint32(src[j])
						count++
					}
				}
				want[i] = uint16((sum + count/2) / count)
			}
			eresQ8(in, make([]uint16, n), l)
			for i := range in {
				if in[i] != want[i] {
					t.Fatalf("n=%d l=%d i=%d got=%d want=%d", n, l, i, in[i], want[i])
				}
			}
		}
	}
}

func TestSRAMBlockAverageFractionsAndReset(t *testing.T) {
	var a sramAverage
	for i, want := range []uint16{25600, 25728, 25856, 26368} {
		q := uint16(25600 + i*256)
		f := Frame{Valid: 1, C1: make([]byte, 1), C2: make([]byte, 1), Q1: []uint16{q}, Q2: []uint16{65535}}
		a.push(&f, 3)
		if f.Q1[0] != want || f.Q2[0] != 65535 {
			t.Fatalf("step %d: %v %v", i, f.Q1, f.Q2)
		}
	}
	a.reset()
	f := Frame{Valid: 1, C1: make([]byte, 1), C2: make([]byte, 1), Q1: []uint16{1}, Q2: []uint16{2}}
	if a.push(&f, 256) != 1 || f.Q1[0] != 1 {
		t.Fatal("reset retained prior capture")
	}
	for i := 1; i < 256; i++ {
		f.Q1[0] = 65535
		f.Q2[0] = 65535
		a.push(&f, 256)
	}
	if f.Q1[0] != 65279 {
		t.Fatalf("256-frame sum: %d", f.Q1[0])
	}
}

func TestSRAMAverageAlignsShiftedRecordWithoutWrapping(t *testing.T) {
	var a sramAverage
	for k, shift := range []int{0, 3, -2} {
		f := Frame{Valid: 100, EdgeX: float64(50 + shift), C1: make([]byte, 100), C2: make([]byte, 100), Q1: make([]uint16, 100), Q2: make([]uint16, 100)}
		for i := range f.Q1 {
			f.Q1[i] = uint16(10000 + (i-shift)*128)
			f.Q2[i] = uint16(20000 - (i-shift)*64)
		}
		a.push(&f, 3)
		for i := 0; i < f.Valid; i++ {
			if f.Q1[i] != uint16(10000+(i+a.lo)*128) || f.Q2[i] != uint16(20000-(i+a.lo)*64) {
				t.Fatalf("frame %d sample %d alignment failed", k, i)
			}
		}
		if k == 2 && (f.Valid != 95 || f.EdgeX != 48) {
			t.Fatalf("invalid common region: %d %g", f.Valid, f.EdgeX)
		}
	}
}
