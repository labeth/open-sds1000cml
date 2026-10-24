// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"math/rand"
	"testing"
)

// benchStack is a stack seeded and fed with the parity frames (gate as
// given; -1 for the auto gate).
// TRLC-LINKS: REQ-SDS-141
func benchStack(gateLo, gateHi int) (*Stack, [][2][]uint8) {
	N, K, align, frames := genFrames()
	u8 := func(a []int16) []uint8 {
		b := make([]uint8, len(a))
		for i, v := range a {
			b[i] = uint8(v)
		}
		return b
	}
	var fr [][2][]uint8
	for _, f := range frames {
		fr = append(fr, [2][]uint8{u8(f.C1), u8(f.C2)})
	}
	st := New(N, K)
	st.Align = align
	st.SeedRefGate(fr[0][0], fr[0][1], frames[0].EdgeX, gateLo, gateHi)
	for i := 1; i < len(frames); i++ {
		st.Feed(fr[i][0], fr[i][1], frames[i].EdgeX)
	}
	return st, fr
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkMedian(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	a := make([]float64, 100000)
	for i := range a {
		a[i] = r.Float64()
	}
	for b.Loop() {
		median(a)
	}
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkDecodeFPGAMoments(b *testing.B) {
	w, _ := EncodeFPGAMoments(FPGAStackMoments{Sum: [2]uint64{0x123456789abcdef, 0x1f}, SumSquares: [2]uint64{0xfedcba987654321, 0x3ffff}, Count: 77, SumOdd: [2]uint64{0x55555, 3}, CountOdd: 30})
	for b.Loop() {
		if _, err := DecodeFPGAMoments(w[:]); err != nil {
			b.Fatal(err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkFPGAStack(b *testing.B) {
	m := make([]FPGAStackMoments, 2*16*512)
	for i := range m {
		m[i] = FPGAStackMoments{Sum: [2]uint64{uint64(i) << 24, 0}, SumSquares: [2]uint64{uint64(i) << 30, 1}, Count: 9, SumOdd: [2]uint64{uint64(i) << 23, 0}, CountOdd: 4}
	}
	for b.Loop() {
		if _, err := FPGAStack(m, 16, 0, 9, 9, 2e-9); err != nil {
			b.Fatal(err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkBuildFPGATemplate(b *testing.B) {
	sig := make([]float64, 5000)
	for i := range sig {
		sig[i] = 60
		if i >= 2600 {
			sig[i] = 190
		}
	}
	for b.Loop() {
		if _, err := BuildFPGATemplate(sig, 2e-9, 2000, 125, false, 64, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkResult(b *testing.B) {
	st, _ := benchStack(-1, -1)
	for b.Loop() {
		st.Result(false, 1)
	}
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkGateFind(b *testing.B) {
	st, fr := benchStack(40, 160)
	for b.Loop() {
		st.gateFind(fr[1][0], 0, 0)
	}
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkBuildTemplate(b *testing.B) {
	st, _ := benchStack(-1, -1)
	ref := st.C[st.Align].ref
	for b.Loop() {
		buildTemplate(ref, st.N, st.RefEdgeX, st.N)
	}
}

// TRLC-LINKS: REQ-SDS-141
func BenchmarkAccumCh(b *testing.B) {
	st, fr := benchStack(-1, -1)
	sig := make([]float32, len(fr[1][0]))
	for i, v := range fr[1][0] {
		sig[i] = float32(v)
	}
	for b.Loop() {
		st.accumCh(0, sig, 3.25)
		st.drizzleHit(1, sig, 2.5, true)
	}
}

// TRLC-LINKS: REQ-SDS-142
func BenchmarkCompensate(b *testing.B) {
	m := make([]float32, 30000)
	for i := range m {
		m[i] = float32(100 + 50*((i/700)%2))
		if i%97 == 0 {
			m[i] = -1
		}
	}
	for b.Loop() {
		Compensate(m, 125e-12, CompOpts{})
	}
}
