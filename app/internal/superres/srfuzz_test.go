package superres

import (
	"math"
	"math/rand"
	"testing"
)

// Super-res fuzz: the device stacker consumes real frames (arbitrary edgeX,
// lengths, gates) and does dense sub-sample index math. It must never panic —
// a panic in the panel's stacker goroutine takes down the app. (Correctness
// of the stack is covered by the 50-family breaker; this is crash-safety.)
func TestSuperresFuzzNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5151))
	sig := func(n int) []uint8 {
		s := make([]uint8, n)
		for i := range s {
			s[i] = uint8(rng.Intn(256))
		}
		return s
	}
	for i := 0; i < 3000; i++ {
		n := 1 + rng.Intn(4000)
		K := []int{1, 8, 16, 32, 64}[rng.Intn(5)]
		st := New(n, K)
		edge := []float64{-1, 0, float64(n) / 2, float64(n) * 2, math.NaN(), 1e9}[rng.Intn(6)]
		gLo := rng.Intn(n+200) - 100
		gHi := rng.Intn(n+200) - 100
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("iter %d (n=%d K=%d edge=%v gate=[%d,%d]) panicked: %v", i, n, K, edge, gLo, gHi, r)
				}
			}()
			// vary the seeding path: plain ref, gated ref, or none
			switch rng.Intn(3) {
			case 0:
				st.SeedRef(sig(n), sig(n), edge)
			case 1:
				st.SeedRefGate(sig(n), sig(n), edge, gLo, gHi)
			}
			// feed a handful of frames of assorted lengths (mismatch is realistic:
			// band jitter changes Valid frame-to-frame)
			for k := 0; k < 4; k++ {
				m := 1 + rng.Intn(4000)
				st.Feed(sig(m), sig(m), []float64{-1, float64(m) / 2, math.NaN()}[rng.Intn(3)])
			}
			st.Result(rng.Intn(2) == 0, 1+rng.Intn(4))
			st.ResetKeepRef()
		}()
	}
}
