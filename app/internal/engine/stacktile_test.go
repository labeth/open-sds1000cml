package engine

import (
	"math/rand"
	"testing"

	"open-sds/app/internal/superres"
)

// TRLC-LINKS: REQ-SDS-141
func tileMomentsFixture(n int) (a, b []superres.FPGAStackMoments) {
	r := rand.New(rand.NewSource(3))
	a = make([]superres.FPGAStackMoments, n)
	b = make([]superres.FPGAStackMoments, n)
	for i := range a {
		for _, m := range []*superres.FPGAStackMoments{&a[i], &b[i]} {
			m.Sum = [2]uint64{r.Uint64(), r.Uint64() >> 20}
			m.SumSquares = [2]uint64{r.Uint64(), r.Uint64() >> 20}
			m.SumOdd = [2]uint64{r.Uint64(), r.Uint64() >> 20}
			m.Count, m.CountOdd = r.Uint32()>>4, r.Uint32()>>4
		}
	}
	return a, b
}

// TRLC-LINKS: REQ-SDS-141
func TestAddTileMomentsMatchesAdd(t *testing.T) {
	a, b := tileMomentsFixture(512)
	want := append([]superres.FPGAStackMoments(nil), a...)
	for i := range want {
		superres.AddFPGAMoments(&want[i], b[i])
	}
	addTileMoments(a, b)
	for i := range a {
		if a[i] != want[i] {
			t.Fatalf("bin %d: %+v want %+v", i, a[i], want[i])
		}
	}
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkAddTileMoments(b *testing.B) {
	s, p := tileMomentsFixture(2048)
	for b.Loop() {
		addTileMoments(s, p)
	}
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkAddTileMomentsRef(b *testing.B) {
	s, p := tileMomentsFixture(2048)
	for b.Loop() {
		for i := range s {
			superres.AddFPGAMoments(&s[i], p[i])
		}
	}
}
