// Package combine is the host-side consumer of the in-fabric ETS super-res COMBINE
// engine (fpga/standard/sr_accum.v). The fabric accumulates, at the fast ENCODE,
// trigger-referenced INTEGER per-bin accumulators; the drain ships them over the
// SOLVED gapless BURST/EDMA path as raw little-endian uint16 words (LSW first). This
// package reassembles those words into a superres.BinGrid so superres.Stack.Result
// crunches them UNCHANGED (Mean / SigmaSingle / BitsGained are computed host-side by
// the same code the host drizzle uses). It is the device analogue of decodestream.
//
// Additive / app-only: NO regs.vh / regmux.vh / schema / codegen change. The combine
// enable lives on previously-FREE register bits (see the Arm* helpers) so the fabric
// build-ID stays 0xc2f6eb5f and, with combine disabled, the fabric + drain are
// byte-for-byte identical to today.
//
// CEILING (stated plainly): this is algorithm-EQUIVALENT ETS super-res, NOT
// superres.js golden-vector identical — the float reference-lock / gain-fit / interp
// brain stays host-side; the fabric does the integer trigger-referenced accumulate.
// The SHIPPING fabric fits the single free M9K as MEAN-ONLY (24-bit {sum,cnt} cell):
// call Unpack with full=false, sum2/sumA/cntA come back nil, and Result yields the
// Mean trace with BitsGained=0. full=true is the external-SRAM config (byte-exact
// BitsGained), proven by fpga/standard/sim/tb_sr_accum.v.
package combine

import (
	"errors"
	"fmt"

	"open-sds/app/internal/superres"
)

// WordsPerBin is the fabric drain width per fine bin (sr_accum.v WPB): always 12
// 16-bit words in bin-major order, even in the mean-only fabric (the sum2/sumA/cntA
// words then read 0). LSW first within every multi-word field.
const WordsPerBin = 12

// Register-bit map for arming combine (previously-FREE bits of EXISTING selectors;
// reconciled in fpga plan sr_build.md §0). combine_en = RUN[5] & interleave_en.
const (
	RunCombineEnBit    = 5 // SEL_RUN bit 5: combine_en (resets 0; free-bit precedent = stream_on[3]/test_ramp[4])
	XformInterleaveBit = 2 // SEL_XFORM_CTRL bit 2: interleave_en (phased fast ENCODE; required for combine)
	XformTrigEnBit     = 9 // SEL_XFORM_CTRL bit 9: trigger-referenced pass anchor (il trig_en, reused)
)

// DrainWords is the number of uint16 words one combine drain yields for a gridL*K grid.
func DrainWords(gridL, k int) int { return gridL * k * WordsPerBin }

// ArmRun returns the SEL_RUN word with combine_en set (bit 5). The caller keeps its
// existing run_en / mode / stream bits; this only OR-sets the additive combine bit.
func ArmRun(run uint16) uint16 { return run | (1 << RunCombineEnBit) }

// ArmXform returns the SEL_XFORM_CTRL word with interleave_en (bit 2, brings up the
// phased fast ENCODE) and the trigger-referenced anchor (bit 9) set. When combine is
// cancelled the caller clears RUN[5] (ArmRun's bit); with RUN[5]=0 the fabric is
// byte-for-byte today regardless of these XFORM bits.
func ArmXform(xform uint16) uint16 {
	return xform | (1 << XformInterleaveBit) | (1 << XformTrigEnBit)
}

// Unpack reassembles a drained combine grid (raw little-endian uint16 words, LSW
// first, bin-major, WordsPerBin per bin) into a superres.BinGrid ready for
// Stack.InjectBins. gridL*k must equal the fabric NBINS (default 256). align is the
// physical channel the align accumulators came from (0=C1, 1=C2). When full is false
// (the shipping mean-only fabric) the sum2/sumA/cntA arrays are left nil so Result
// yields the Mean trace with BitsGained=0; when true they are reassembled for the
// byte-exact BitsGained path. sampleS/hits/frames are host metadata attached verbatim.
func Unpack(words []uint16, gridL, k, align int, full bool, sampleS float64, hits, frames int) (superres.BinGrid, error) {
	if gridL < 1 || k < 1 {
		return superres.BinGrid{}, errors.New("combine: Unpack bad grid dims")
	}
	if align < 0 || align > 1 {
		return superres.BinGrid{}, errors.New("combine: Unpack bad align")
	}
	nb := gridL * k
	if len(words) != nb*WordsPerBin {
		return superres.BinGrid{}, fmt.Errorf("combine: Unpack want %d words, got %d", nb*WordsPerBin, len(words))
	}

	g := superres.BinGrid{
		GridL: gridL, K: k, Align: align, SampleS: sampleS,
		Hits: hits, Frames: frames,
		ASum: make([]uint64, nb), ACnt: make([]uint64, nb),
		BSum: make([]uint64, nb), BCnt: make([]uint64, nb),
	}
	if full {
		g.ASum2 = make([]uint64, nb)
		g.ASumA = make([]uint64, nb)
		g.ACntA = make([]uint64, nb)
	}

	for b := 0; b < nb; b++ {
		w := words[b*WordsPerBin : b*WordsPerBin+WordsPerBin]
		g.ACnt[b] = uint64(w[0])
		g.ASum[b] = uint64(w[1]) | uint64(w[2])<<16
		if full {
			g.ASum2[b] = uint64(w[3]) | uint64(w[4])<<16 | uint64(w[5])<<32
			g.ACntA[b] = uint64(w[6])
			g.ASumA[b] = uint64(w[7]) | uint64(w[8])<<16
		}
		g.BCnt[b] = uint64(w[9])
		g.BSum[b] = uint64(w[10]) | uint64(w[11])<<16
	}
	return g, nil
}
