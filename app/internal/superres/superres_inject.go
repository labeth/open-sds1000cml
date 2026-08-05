package superres

import "errors"

// BinGrid is one drained device-combine grid: integer accumulators per fine bin,
// align + other channel, in fabric wire order (bin-major). Sum2/SumA/CntA nil =>
// mean-only (Result yields the traces with BitsGained=0). Lengths must == GridL*K.
//
// This is the DEVICE analogue of the host drizzle: the fabric (sr_accum.v) fills
// these integer accumulators at the fast rate; the CPU crunch Stack.Result reads
// them UNCHANGED. Every accumulator width here is <=48 bits, so uint64->float64
// is lossless (<2^53) and the crunch is byte-exact on the injected bins.
type BinGrid struct {
	GridL, K int     // fine grid = GridL*K bins (== Nbins)
	Align    int     // physical channel the align accumulators are (0=C1,1=C2)
	SampleS  float64 // coarse seconds/sample (fine dt = SampleS/K), from the frame
	Hits     int     // fabric accumulation count (occurrences stacked); -> st.Hits
	Frames   int     // device combine passes drained (>=1); -> st.Frames

	// align channel (C[Align]) — full set for byte-exact bits:
	ASum, ACnt   []uint64 // required (mean trace + fill)
	ASum2        []uint64 // optional (SigmaSingle); nil => mean-only
	ASumA, ACntA []uint64 // optional (measured SigmaStack); nil => theory fallback

	// other channel (C[1-Align]) — for Mean2 only:
	BSum, BCnt []uint64 // required
}

// InjectBins loads a device-combined grid so Result(false,1) crunches it byte-exact.
// It (re)sizes the stack to a GridL*K grid, converts each integer accumulator to the
// float64 the crunch reads, and sets the geometry Result + the stop logic need. It is
// the DEVICE analogue of gateInstall+Feed: after it returns, st.Result / st.Hits /
// st.Frames are valid exactly as if the host had drizzled. No reference/template is
// built (the host already seeded it at arm; InjectBins only refreshes the numbers).
func (st *Stack) InjectBins(g BinGrid) error {
	if g.GridL < 1 || g.K < 1 {
		return errors.New("superres: InjectBins bad grid dims")
	}
	if g.Align < 0 || g.Align > 1 {
		return errors.New("superres: InjectBins bad Align")
	}
	nb := g.GridL * g.K
	if len(g.ASum) != nb || len(g.ACnt) != nb || len(g.BSum) != nb || len(g.BCnt) != nb {
		return errors.New("superres: InjectBins required array length != GridL*K")
	}
	full := g.ASum2 != nil || g.ASumA != nil || g.ACntA != nil
	if full {
		// full-bits path requires the whole optional set at the right length
		if len(g.ASum2) != nb || len(g.ASumA) != nb || len(g.ACntA) != nb {
			return errors.New("superres: InjectBins optional array length != GridL*K")
		}
	}

	// geometry (mirrors gateInstall: StatLo/StatHi are COARSE samples, Result
	// multiplies by K, so 0..GridL selects the whole fine grid).
	st.K = g.K
	st.GridL = g.GridL
	st.N = g.GridL
	st.Nbins = nb
	st.Align = g.Align
	st.SampleS = g.SampleS
	st.StatLo, st.StatHi = 0, g.GridL
	st.Gated = true

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
	// the other channel's stats arrays are never read by Result for 1-Align, but
	// keep them allocated & zeroed so any incidental access is safe.
	B.sum2 = make([]float64, nb)
	B.sumA = make([]float64, nb)
	B.cntA = make([]float64, nb)

	for i := 0; i < nb; i++ {
		A.sum[i] = float64(g.ASum[i])
		A.cnt[i] = float64(g.ACnt[i])
		B.sum[i] = float64(g.BSum[i])
		B.cnt[i] = float64(g.BCnt[i])
		if full {
			A.sum2[i] = float64(g.ASum2[i])
			A.sumA[i] = float64(g.ASumA[i])
			A.cntA[i] = float64(g.ACntA[i])
		}
	}

	st.Hits = g.Hits
	st.Frames = g.Frames
	st.Rejected = 0
	return nil
}
