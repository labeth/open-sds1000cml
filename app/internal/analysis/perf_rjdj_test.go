// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"math/rand"
	"sort"
	"testing"
)

// medianInPlace is the sorted copy's middle for every length, with ties.
// TRLC-LINKS: REQ-SDS-199
func TestMedianInPlaceIsTheSortedMiddle(t *testing.T) {
	r := rand.New(rand.NewSource(8))
	for n := 1; n < 300; n++ {
		a := make([]float64, n)
		for i := range a {
			a[i] = float64(r.Intn(20))
		}
		s := append([]float64(nil), a...)
		sort.Float64s(s)
		if got := medianInPlace(a); got != s[n>>1] {
			t.Fatalf("n %d: %v, want %v", n, got, s[n>>1])
		}
	}
}

// TRLC-LINKS: REQ-SDS-199
func BenchmarkRJDJ(b *testing.B) {
	r := rand.New(rand.NewSource(3))
	tie := make([]float64, 20000)
	for i := range tie {
		tie[i] = r.NormFloat64() + float64(i%2)*6
	}
	for b.Loop() {
		RJDJ(tie)
	}
}

// rjdjRef is RJDJ as first written: the oracle and the baseline.
// TRLC-LINKS: REQ-SDS-199
func rjdjRef(tie []float64) (rj, dj float64, ok bool) {
	if len(tie) < 200 {
		return 0, 0, false
	}
	s := append([]float64(nil), tie...)
	sort.Float64s(s)
	med := s[len(s)>>1]
	var left, right []float64
	for _, v := range s {
		if v <= med {
			left = append(left, v)
		}
	}
	for _, v := range s {
		if v > med {
			right = append(right, v)
		}
	}
	mL, mR := median(left), median(right)
	sep := mR - mL
	mid := (mL + mR) / 2
	bandLo, bandHi := mid-0.25*sep, mid+0.25*sep
	central := 0
	for _, v := range s {
		if v > bandLo && v < bandHi {
			central++
		}
	}
	if !(sep > 0) || float64(central)/float64(len(s)) > 0.08 {
		return 1.4826 * median(absDev(s, med)), 0, true
	}
	madL, madR := median(absDev(left, mL)), median(absDev(right, mR))
	return 1.4826 * ((madL + madR) / 2), sep, true
}

// TRLC-LINKS: REQ-SDS-199
func BenchmarkRJDJRef(b *testing.B) {
	r := rand.New(rand.NewSource(3))
	tie := make([]float64, 20000)
	for i := range tie {
		tie[i] = r.NormFloat64() + float64(i%2)*6
	}
	for b.Loop() {
		rjdjRef(tie)
	}
}

// RJDJ reads as the first version did, uni- and bimodal, with ties.
// TRLC-LINKS: REQ-SDS-199
func TestRJDJIsTheReference(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for _, sep := range []float64{0, 0.5, 3, 8} {
		for _, n := range []int{200, 201, 999, 5000} {
			tie := make([]float64, n)
			for i := range tie {
				tie[i] = float64(int(r.NormFloat64()*10)) / 10
				if i%2 == 1 {
					tie[i] += sep
				}
			}
			a1, b1, c1 := RJDJ(tie)
			a2, b2, c2 := rjdjRef(tie)
			if a1 != a2 || b1 != b2 || c1 != c2 {
				t.Fatalf("sep %v n %d: %v %v %v, want %v %v %v", sep, n, a1, b1, c1, a2, b2, c2)
			}
		}
	}
}
