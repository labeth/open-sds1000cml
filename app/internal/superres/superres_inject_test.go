package superres

import (
	"math"
	"testing"
)

// buildFabricBins synthesizes a device-combine grid the way sr_accum.v would:
// for each fine bin it accumulates a small stack of 8-bit codes (varied so the
// measured-sigma path is exercised), splitting odd/even passes into the A half.
func buildFabricBins(gridL, K, align int, depth int) BinGrid {
	nb := gridL * K
	g := BinGrid{
		GridL: gridL, K: K, Align: align, SampleS: 1e-9,
		ASum: make([]uint64, nb), ACnt: make([]uint64, nb),
		ASum2: make([]uint64, nb), ASumA: make([]uint64, nb), ACntA: make([]uint64, nb),
		BSum: make([]uint64, nb), BCnt: make([]uint64, nb),
	}
	for b := 0; b < nb; b++ {
		base := 40 + (b*211)%150 // per-bin mean spread across code space
		for d := 0; d < depth; d++ {
			// deterministic pseudo-noise, kept in [0,255]
			n := ((b*7 + d*53 + 11) % 17) - 8
			v := base + n
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			uv := uint64(v)
			g.ASum[b] += uv
			g.ASum2[b] += uv * uv
			g.ACnt[b]++
			if d&1 == 1 { // odd pass -> A half
				g.ASumA[b] += uv
				g.ACntA[b]++
			}
			// other channel: sum+cnt only
			bv := uint64((base + 20 + (d*13)%9) & 0xFF)
			g.BSum[b] += bv
			g.BCnt[b]++
		}
	}
	g.Hits = depth
	g.Frames = 1
	return g
}

// referenceStack loads the same integer bins by directly populating the chanState
// float slices + geometry (the "existing path" a host-drizzled stack ends up in),
// bypassing InjectBins. Result on this stack is the golden the InjectBins path
// must reproduce byte-exact.
func referenceStack(g BinGrid) *Stack {
	nb := g.GridL * g.K
	st := New(g.GridL, g.K)
	st.Nbins = nb
	st.Align = g.Align
	st.SampleS = g.SampleS
	st.StatLo, st.StatHi = 0, g.GridL
	st.Gated = true
	st.Hits = g.Hits
	st.Frames = g.Frames
	a := g.Align
	b := 1 - g.Align
	A := &st.C[a]
	B := &st.C[b]
	A.sum = make([]float64, nb)
	A.sum2 = make([]float64, nb)
	A.cnt = make([]float64, nb)
	A.sumA = make([]float64, nb)
	A.cntA = make([]float64, nb)
	B.sum = make([]float64, nb)
	B.cnt = make([]float64, nb)
	for i := 0; i < nb; i++ {
		A.sum[i] = float64(g.ASum[i])
		A.sum2[i] = float64(g.ASum2[i])
		A.cnt[i] = float64(g.ACnt[i])
		A.sumA[i] = float64(g.ASumA[i])
		A.cntA[i] = float64(g.ACntA[i])
		B.sum[i] = float64(g.BSum[i])
		B.cnt[i] = float64(g.BCnt[i])
	}
	return st
}

func floatsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestInjectBinsByteExact proves the CPU crunch runs byte-exact on device-combined
// integer bins: InjectBins -> Result must equal Result on the same bins loaded by
// the existing (direct float-slice) path — Mean, Mean2, BitsGained, and the sigmas.
func TestInjectBinsByteExact(t *testing.T) {
	// GridL=64, K=4 -> 256 bins; depth 32 -> cnt>=4 and >=16 halves => measured sigma.
	g := buildFabricBins(64, 4, 1, 32)

	stInj := New(1, 1) // deliberately mis-sized: InjectBins must resize
	if err := stInj.InjectBins(g); err != nil {
		t.Fatalf("InjectBins error: %v", err)
	}
	rInj := stInj.Result(false, 1)

	rRef := referenceStack(g).Result(false, 1)

	if !floatsEqual(rInj.Mean, rRef.Mean) {
		t.Errorf("Mean differs between InjectBins and reference path")
	}
	if !floatsEqual(rInj.Mean2, rRef.Mean2) {
		t.Errorf("Mean2 differs between InjectBins and reference path")
	}
	if rInj.BitsGained != rRef.BitsGained {
		t.Errorf("BitsGained differs: inject=%v ref=%v", rInj.BitsGained, rRef.BitsGained)
	}
	if rInj.SigmaSingle != rRef.SigmaSingle || rInj.SigmaStack != rRef.SigmaStack {
		t.Errorf("Sigma differs: inject=(%v,%v) ref=(%v,%v)",
			rInj.SigmaSingle, rInj.SigmaStack, rRef.SigmaSingle, rRef.SigmaStack)
	}
	if rInj.SigmaMeasured != rRef.SigmaMeasured {
		t.Errorf("SigmaMeasured differs: inject=%v ref=%v", rInj.SigmaMeasured, rRef.SigmaMeasured)
	}
	if rInj.Hits != g.Hits || rInj.Frames != g.Frames {
		t.Errorf("counters not propagated: Hits=%d Frames=%d", rInj.Hits, rInj.Frames)
	}

	// The test must actually exercise the measured-sigma / bits path, else it is
	// vacuous: assert a real BitsGained was produced and sigma was measured.
	if !(rInj.BitsGained > 0) {
		t.Errorf("expected BitsGained>0 (measured-sigma path), got %v", rInj.BitsGained)
	}
	if !rInj.SigmaMeasured {
		t.Errorf("expected measured sigma (>=16 half bins), got theory fallback")
	}
	// And the mean must match sum/cnt exactly on a spot bin.
	A := &referenceStack(g).C[g.Align]
	want := float32(A.sum[10] / A.cnt[10])
	if rInj.Mean[10] != want {
		t.Errorf("Mean[10]=%v want %v", rInj.Mean[10], want)
	}
	_ = math.Sqrt
}

// TestInjectBinsMeanOnly: nil optional arrays => mean trace valid, BitsGained 0.
func TestInjectBinsMeanOnly(t *testing.T) {
	full := buildFabricBins(32, 4, 0, 20)
	g := BinGrid{
		GridL: full.GridL, K: full.K, Align: full.Align, SampleS: full.SampleS,
		Hits: full.Hits, Frames: full.Frames,
		ASum: full.ASum, ACnt: full.ACnt, // no ASum2/ASumA/ACntA
		BSum: full.BSum, BCnt: full.BCnt,
	}
	st := New(1, 1)
	if err := st.InjectBins(g); err != nil {
		t.Fatalf("InjectBins mean-only error: %v", err)
	}
	r := st.Result(false, 1)
	if r.BitsGained != 0 {
		t.Errorf("mean-only BitsGained should be 0, got %v", r.BitsGained)
	}
	nb := g.GridL * g.K
	if len(r.Mean) != nb {
		t.Fatalf("Mean length %d != %d", len(r.Mean), nb)
	}
	want := float32(float64(g.ASum[5]) / float64(g.ACnt[5]))
	if r.Mean[5] != want {
		t.Errorf("mean-only Mean[5]=%v want %v", r.Mean[5], want)
	}
}

// TestInjectBinsReject: length mismatch must error, not crunch garbage.
func TestInjectBinsReject(t *testing.T) {
	g := buildFabricBins(16, 4, 0, 8)
	g.ACnt = g.ACnt[:len(g.ACnt)-1] // short
	st := New(1, 1)
	if err := st.InjectBins(g); err == nil {
		t.Errorf("expected error on short array, got nil")
	}
}
