// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES

package superres

// accumCh resamples one aligned frame at every fine bin (interp kernel): bin b
// reads the frame at raw index b/K + shift, linearly interpolated. Odd frames
// also land in the A half-stack (odd/even split for the measured σ).
// TRLC-LINKS: REQ-SDS-141
func (st *Stack) accumCh(ch int, sig []float32, shift float64) {
	K, nb, n := st.K, st.Nbins, st.N
	C := &st.C[ch]
	odd := (st.Frames & 1) == 1
	invK := 1.0 / float64(K)
	sum, sum2, cnt := C.sum[:nb], C.sum2[:nb], C.cnt[:nb]
	sumA, cntA := C.sumA[:nb], C.cntA[:nb]
	for bi := range sum {
		t := float64(bi)*invK + shift
		if t < 0 { // before the frame (math.Floor's negative case)
			continue
		}
		i0 := int(t) // the floor of t >= 0
		if i0+1 >= n {
			continue
		}
		w := t - float64(i0)
		a := float64(sig[i0]) * (1 - w)
		bb := float64(sig[i0+1]) * w
		v := a + bb
		p := v * v
		sum[bi] += v
		sum2[bi] += p
		cnt[bi]++
		if odd {
			sumA[bi] += v
			cntA[bi]++
		}
	}
}
