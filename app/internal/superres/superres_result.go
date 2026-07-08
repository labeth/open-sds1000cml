package superres

import "math"

// Result is the crunch output: the super-resolved mean trace (nil if statsOnly)
// plus the measured resolution figures.
type Result struct {
	Mean                    []float32 // align-channel mean; nil if statsOnly
	Mean2                   []float32 // the OTHER channel, same fine grid; nil if statsOnly
	Frames, Rejected, Hits  int
	Fill                    float64
	SigmaSingle, SigmaStack float64
	SigmaMeasured           bool
	BitsGained, EffBits     float64
	FineDtS, EffRateSa      float64
}

// Result crunches the stack: builds the mean trace and measures σ_single→σ_stack
// (odd/even half-stack) → bitsGained. stride subsamples the O(nbins) stats.
func (st *Stack) Result(statsOnly bool, stride int) Result {
	nb := st.Nbins
	if stride < 1 {
		stride = 1
	}
	A := &st.C[st.Align]
	const EPS = 0.05
	var mean, mean2 []float32
	if !statsOnly {
		// Align channel (Mean) plus the OTHER channel (Mean2), both on the same
		// L·K fine grid so a stacked X-Y / dual-spectrum can pair them index-for-
		// index. The other channel is drizzled at the align channel's positions,
		// so an unlocked C1↔C2 phase shows up as honest smear (matches the web).
		B := &st.C[1-st.Align]
		mean = make([]float32, nb)
		mean2 = make([]float32, nb)
		for b := 0; b < nb; b++ {
			if c := A.cnt[b]; c < EPS {
				mean[b] = -1
			} else {
				mean[b] = float32(A.sum[b] / c)
			}
			if c := B.cnt[b]; c < EPS {
				mean2[b] = -1
			} else {
				mean2[b] = float32(B.sum[b] / c)
			}
		}
	}
	statLo, statHi := st.StatLo*st.K, st.StatHi*st.K
	span := statHi - statLo
	if span < 1 {
		span = 1
	}
	statStride := stride
	if s := int(math.Ceil(float64(span) / 4096)); s > statStride {
		statStride = s
	}
	var sigSingles, halves, cnts []float64
	filled, scanned := 0, 0
	for b := statLo; b < statHi; b += statStride {
		c := A.cnt[b]
		scanned++
		if c < EPS {
			continue
		}
		filled++
		cnts = append(cnts, c)
		mv := A.sum[b] / c
		if c >= 4 {
			v := A.sum2[b]/c - mv*mv
			if v < 0 {
				v = 0
			}
			sigSingles = append(sigSingles, math.Sqrt(v))
			ca := A.cntA[b]
			cb := c - ca
			if ca >= 2 && cb >= 2 {
				ma := A.sumA[b] / ca
				mb := (A.sum[b] - A.sumA[b]) / cb
				halves = append(halves, (ma-mb)/2)
			}
		}
	}
	sigmaSingle := median(sigSingles)
	sigmaStack, sigmaMeasured := 0.0, true
	if len(halves) >= 16 {
		absh := make([]float64, len(halves))
		for i, v := range halves {
			absh[i] = math.Abs(v)
		}
		sigmaStack = 1.4826 * median(absh)
	}
	sigmaStackTheory := 0.0
	if sigmaSingle > 0 && len(cnts) > 0 {
		sigmaStackTheory = sigmaSingle / math.Sqrt(median(cnts))
	}
	if sigmaStack == 0 && sigmaStackTheory > 0 {
		sigmaStack, sigmaMeasured = sigmaStackTheory, false
	}
	bitsGained := 0.0
	if sigmaStack > 0 && sigmaSingle > 0 {
		bitsGained = math.Log2(sigmaSingle / sigmaStack)
	}
	fineDt, effRate := 0.0, 0.0
	if st.SampleS > 0 {
		fineDt = st.SampleS / float64(st.K)
		effRate = float64(st.K) / st.SampleS
	}
	fill := 0.0
	if scanned > 0 {
		fill = float64(filled) / float64(scanned)
	}
	return Result{
		Mean: mean, Mean2: mean2, Frames: st.Frames, Rejected: st.Rejected, Hits: st.Hits, Fill: fill,
		SigmaSingle: sigmaSingle, SigmaStack: sigmaStack, SigmaMeasured: sigmaMeasured,
		BitsGained: bitsGained, EffBits: 8 + bitsGained,
		FineDtS: fineDt, EffRateSa: effRate,
	}
}
