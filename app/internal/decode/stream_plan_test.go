// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math"
	"math/rand"
	"testing"
)

// The digital stream's plan takes any whole period: six periods a feature
// where the budget allows, else the finest the budget and the bus allow
// while three and a half fit; never past the bus's words a second, never
// coarser than the coarsest decimation's.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestStreamBitsPlan(t *testing.T) {
	if StreamBitsMinPeriod != 7 || StreamBitsWordRate(StreamBitsMinPeriod) > StreamBusWords || StreamBitsWordRate(StreamBitsMinPeriod-1) <= StreamBusWords {
		t.Fatalf("finest period %d raw words: %.4g words a second", StreamBitsMinPeriod, StreamBitsWordRate(StreamBitsMinPeriod))
	}
	for _, c := range []struct {
		feature, rate float64
		period        int
		ok            bool
	}{
		{4e-6, 4e6, 166, true},        // DMX: six periods, 1.5 M samples a second
		{2.17e-6, 4e6, 90, true},      // Modbus at 460.8 kBd: six, 2.8 M
		{2e-6, 2e6, 125, true},        // a clocked bus at 250 kHz: the budget's 2 M, four periods
		{1.6e-6, 2e6, 0, false},       // three and a half do not fit the budget
		{0.5e-6, 0, 20, true},         // the bus alone: six periods of 83 ns
		{0.1e-6, 0, 7, true},          // the bus's finest, 3.57 periods
		{0.09e-6, 0, 0, false},        // finer than the bus carries
		{24e-9 * 1000, 0, 1000, true}, // a whole number of periods keeps them
		{1, 4e6, StreamBitsMaxPeriod, true},
	} {
		p, ok := StreamBitsPlan(c.feature, c.rate)
		if p != c.period || ok != c.ok {
			t.Errorf("feature %.3g s at %.3g a second: period %d %v, want %d %v", c.feature, c.rate, p, ok, c.period, c.ok)
		}
	}
	rng := rand.New(rand.NewSource(1))
	powers := 0
	for i := 0; i < 100000; i++ {
		feature := math.Exp(rng.Float64()*math.Log(1e5)) * 50e-9 // 50 ns to 5 ms
		rate := []float64{0, 1e6, 2e6, 4e6}[rng.Intn(4)]
		p, ok := StreamBitsPlan(feature, rate)
		if !ok {
			// even the finest allowed leaves under three and a half
			fine := float64(StreamBitsMinPeriod)
			if rate > 0 {
				fine = math.Max(fine, math.Ceil(1/(rate*StreamRawWord)))
			}
			if 3.5*fine*StreamRawWord <= feature*(1-1e-9) {
				t.Fatalf("feature %.4g s at %.3g: refused, %v raw words keep 3.5", feature, rate, fine)
			}
			continue
		}
		n := feature / StreamBitsPeriod(p)
		switch {
		case p < StreamBitsMinPeriod || p > StreamBitsMaxPeriod || StreamBitsWordRate(p) > StreamBusWords:
			t.Fatalf("feature %.4g s: period %d past the bus or the coarsest", feature, p)
		case n < 3.5*(1-1e-9):
			t.Fatalf("feature %.4g s: period %d keeps %.3f", feature, p, n)
		case rate > 0 && 1/StreamBitsPeriod(p) > rate*(1+1e-9):
			t.Fatalf("feature %.4g s: period %d past %.3g a second", feature, p, rate)
		case p < StreamBitsMaxPeriod && feature/StreamBitsPeriod(p+1) >= 6 && (rate == 0 || 1/StreamBitsPeriod(p+1) <= rate):
			t.Fatalf("feature %.4g s: period %d, but %d keeps six", feature, p, p+1)
		case p > StreamBitsMinPeriod && n < 6*(1-1e-9) && (rate == 0 || 1/StreamBitsPeriod(p-1) <= rate*(1-1e-9)):
			t.Fatalf("feature %.4g s: period %d keeps %.3f, but %d is allowed", feature, p, n, p-1)
		}
		if p&(p-1) == 0 && p != StreamBitsMaxPeriod {
			powers++
		}
	}
	if powers > 1000 {
		t.Fatalf("%d plans a power of two", powers)
	}
}
