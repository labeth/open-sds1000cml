// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"math"
)

// fpgaQ converts an unsigned little-endian 128-bit limb pair scaled by 2^-shift.
// TRLC-LINKS: REQ-SDS-141
func fpgaQ(v [2]uint64, shift int) float64 {
	return math.Ldexp(float64(v[0])+math.Ldexp(float64(v[1]), 64), -shift)
}

// FPGAStack loads edge-locked FPGA moments (ADR-STACKING-IMAGE-SPLIT) into a
// Stack so Result reports the same mean, split-stack noise and bits gained as
// the software stacker. moments is bin-major with two channels per bin, on a
// grid of k fine bins per input sample; values are ADC codes. align is the
// channel the hardware detected edges on. Hits counts committed hits.
// TRLC-LINKS: REQ-SDS-141
func FPGAStack(moments []FPGAStackMoments, k, align, hits, frames int, sampleS float64) (*Stack, error) {
	if k < 1 || len(moments) == 0 || len(moments)%(2*k) != 0 {
		return nil, fmt.Errorf("FPGA stack: %d moments do not form whole samples at %d bins per sample", len(moments), k)
	}
	if align != 0 && align != 1 {
		return nil, fmt.Errorf("FPGA stack: align channel %d", align)
	}
	st := New(len(moments)/2/k, k)
	for b := 0; b < st.Nbins; b++ {
		for ch := 0; ch < 2; ch++ {
			m := moments[2*b+ch]
			if m.CountOdd > m.Count {
				return nil, fmt.Errorf("FPGA stack: bin %d channel %d odd count %d exceeds %d", b, ch, m.CountOdd, m.Count)
			}
			c := &st.C[ch]
			c.sum[b] = fpgaQ(m.Sum, 24)
			c.sum2[b] = fpgaQ(m.SumSquares, 48)
			c.cnt[b] = float64(m.Count)
			c.sumA[b] = fpgaQ(m.SumOdd, 24)
			c.cntA[b] = float64(m.CountOdd)
		}
	}
	st.Align, st.Hits, st.Frames, st.SampleS = align, hits, frames, sampleS
	return st, nil
}
