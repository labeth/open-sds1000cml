package engine

import (
	"testing"

	"open-sds/app/internal/combine"
	"open-sds/app/internal/superres"
)

// combineTestGrid builds a known mean-only combine grid (3072 words, 12/bin,
// bin-major, LSW-first) plus the per-bin align codes it encodes, so a consumer can
// assert the crunched mean equals the code exactly (a misframe by even one word —
// e.g. a spurious prime read — corrupts every bin).
func combineTestGrid(gridL, k int) (words []uint16, codeA []int) {
	nb := gridL * k
	words = make([]uint16, nb*combine.WordsPerBin)
	codeA = make([]int, nb)
	const cnt = 100 // every bin populated → Fill == 1, cnt≥4 lets the crunch run
	for b := 0; b < nb; b++ {
		ca := 50 + b%100 // 8-bit code, distinct enough to catch a one-word shift
		cb := 30 + b%120
		codeA[b] = ca
		w := words[b*combine.WordsPerBin:]
		w[0] = uint16(cnt)                 // align.cnt
		w[1] = uint16((ca * cnt) & 0xffff) // align.sum lo
		w[2] = uint16((ca * cnt) >> 16)    // align.sum hi
		// w[3..8] = sum2/cntA/sumA: mean-only → 0
		w[9] = uint16(cnt)                  // other.cnt
		w[10] = uint16((cb * cnt) & 0xffff) // other.sum lo
		w[11] = uint16((cb * cnt) >> 16)    // other.sum hi
	}
	return
}

// TestCombineArmCarriesSiggen proves the ARM CHAIN: with the fabric FAST-SIGNAL
// GENERATOR enabled (SetSiggen), doCombineDrain's SEL_RUN arm write carries RUN[6]/[7]
// (siggen enable/shape) IN ADDITION to combine_en (RUN[5]) — so the device-combine
// accumulate integrates the SAME triangle the host reference-lock engages on. With
// siggen OFF the arm word is byte-for-byte the combine-only word (no siggen leak).
func TestCombineArmCarriesSiggen(t *testing.T) {
	const gridL, k = 64, 4
	words, _ := combineTestGrid(gridL, k)
	n := combine.DrainWords(gridL, k)

	armRunVal := func(fb *fakeBus) (uint16, bool) {
		// The arm write is the SEL_RUN write with combine_en (bit 5) set; the restore
		// write clears it. Take the last SEL_RUN write that has bit 5 set = the arm.
		var val uint16
		var found bool
		for _, w := range fb.snapWrites() {
			if w.sel == selRun && w.val&(1<<combine.RunCombineEnBit) != 0 {
				val, found = w.val, true
			}
		}
		return val, found
	}

	// siggen OFF (default): arm word = combine_en only, no siggen bits.
	fbOff := newFakeBus()
	fbOff.combineWords, fbOff.combineRemain = words, uint16(n)
	eOff, _ := newTestEngine(t, fbOff)
	if _, ok := eOff.doCombineDrain(CombineReq{GridL: gridL, K: k, DwellMs: 20}); !ok {
		t.Fatalf("siggen-off drain ok=false")
	}
	offArm, ok := armRunVal(fbOff)
	if !ok {
		t.Fatalf("no combine arm write seen (siggen off)")
	}
	if offArm&(1<<combine.RunSiggenEnBit) != 0 || offArm&(1<<combine.RunSiggenShapeBit) != 0 {
		t.Fatalf("siggen-off combine arm %#04x leaked a siggen bit", offArm)
	}

	// siggen ON (ramp): arm word carries combine_en AND RUN[6] AND RUN[7].
	fbOn := newFakeBus()
	fbOn.combineWords, fbOn.combineRemain = words, uint16(n)
	eOn, _ := newTestEngine(t, fbOn)
	eOn.SetSiggen(true, true) // enable + ramp shape
	if _, ok := eOn.doCombineDrain(CombineReq{GridL: gridL, K: k, DwellMs: 20}); !ok {
		t.Fatalf("siggen-on drain ok=false")
	}
	onArm, ok := armRunVal(fbOn)
	if !ok {
		t.Fatalf("no combine arm write seen (siggen on)")
	}
	wantMask := uint16((1 << combine.RunCombineEnBit) | (1 << combine.RunSiggenEnBit) | (1 << combine.RunSiggenShapeBit))
	if onArm&wantMask != wantMask {
		t.Fatalf("siggen-on combine arm %#04x missing bits (want mask %#04x set: combine_en|siggen_en|shape)", onArm, wantMask)
	}
	// The ONLY difference vs the off arm must be the two siggen bits (additive).
	if onArm != offArm|(1<<combine.RunSiggenEnBit)|(1<<combine.RunSiggenShapeBit) {
		t.Fatalf("siggen-on arm %#04x is not off-arm %#04x + siggen bits (non-additive)", onArm, offArm)
	}
}

// TestCombineDrainNoPrimeRead drives the engine's owner-goroutine COMBINE primitive
// against a fake bus that ships a known bin-major grid, and proves the drain does NOT
// issue a leading prime read: the returned words equal the scripted grid verbatim
// (exactly n pops), and Unpack→InjectBins→Result crunches a byte-exact mean (Fill=1,
// 256 bins) whose per-bin value equals the encoded code — which a one-word misframe
// would break.
func TestCombineDrainNoPrimeRead(t *testing.T) {
	const gridL, k = 64, 4
	words, codeA := combineTestGrid(gridL, k)
	n := combine.DrainWords(gridL, k)
	if n != len(words) {
		t.Fatalf("grid size %d != DrainWords %d", len(words), n)
	}

	fb := newFakeBus()
	fb.combineWords = words
	fb.combineRemain = uint16(n) // BURST_REMAIN reports the full grid ready
	e, _ := newTestEngine(t, fb)

	out, ok := e.doCombineDrain(CombineReq{GridL: gridL, K: k, DwellMs: 20})
	if !ok {
		t.Fatalf("doCombineDrain returned ok=false")
	}
	if len(out.Words) != n {
		t.Fatalf("drained %d words, want %d", len(out.Words), n)
	}
	if fb.wordReads != n {
		t.Fatalf("BURST popped %d words, want exactly %d (a prime read would over-pop)", fb.wordReads, n)
	}
	for i := range words { // byte-exact: no word dropped, none inserted
		if out.Words[i] != words[i] {
			t.Fatalf("word[%d] = %#04x, want %#04x (grid misframed)", i, out.Words[i], words[i])
		}
	}

	// The engine drain must restore baseline: XFORM_CTRL back to 0, combine_en cleared,
	// OP_RESET issued — so the fabric is byte-for-byte today after the drain.
	sawXform0, sawReset := false, false
	for _, w := range fb.snapWrites() {
		if w.sel == selXformCtrl && w.val == 0 {
			sawXform0 = true
		}
		if w.sel == selOpcode && w.val == opReset {
			sawReset = true
		}
	}
	if !sawXform0 || !sawReset {
		t.Fatalf("combine drain did not restore baseline (xform0=%v reset=%v)", sawXform0, sawReset)
	}

	// Full consumer crunch: Unpack → InjectBins → Result must be byte-exact.
	grid, err := combine.Unpack(out.Words, gridL, k, 0, false, out.SampleS, 100, out.Frames)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	st := superres.New(gridL*k, k)
	if err := st.InjectBins(grid); err != nil {
		t.Fatalf("InjectBins: %v", err)
	}
	res := st.Result(false, 1)
	if res.Fill != 1 {
		t.Fatalf("Fill = %v, want 1 (every bin populated)", res.Fill)
	}
	if len(res.Mean) != gridL*k {
		t.Fatalf("Mean len = %d, want %d", len(res.Mean), gridL*k)
	}
	for b := range res.Mean {
		if res.Mean[b] != float32(codeA[b]) {
			t.Fatalf("Mean[%d] = %v, want %v (crunch not byte-exact / misframed)", b, res.Mean[b], codeA[b])
		}
	}
}
