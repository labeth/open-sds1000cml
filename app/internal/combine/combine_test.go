package combine

import (
	"testing"

	"open-sds/app/internal/superres"
)

// pack builds the little-endian 12-word/bin drain buffer the fabric produces, so the
// test exercises the exact wire contract Unpack must invert.
func pack(nb int, cnt, sum, sum2, cntA, sumA, bcnt, bsum []uint64) []uint16 {
	w := make([]uint16, nb*WordsPerBin)
	for b := 0; b < nb; b++ {
		o := b * WordsPerBin
		w[o+0] = uint16(cnt[b])
		w[o+1] = uint16(sum[b])
		w[o+2] = uint16(sum[b] >> 16)
		w[o+3] = uint16(sum2[b])
		w[o+4] = uint16(sum2[b] >> 16)
		w[o+5] = uint16(sum2[b] >> 32)
		w[o+6] = uint16(cntA[b])
		w[o+7] = uint16(sumA[b])
		w[o+8] = uint16(sumA[b] >> 16)
		w[o+9] = uint16(bcnt[b])
		w[o+10] = uint16(bsum[b])
		w[o+11] = uint16(bsum[b] >> 16)
	}
	return w
}

func TestUnpackFullRoundTrip(t *testing.T) {
	const gridL, k = 4, 4
	nb := gridL * k
	cnt := make([]uint64, nb)
	sum := make([]uint64, nb)
	sum2 := make([]uint64, nb)
	cntA := make([]uint64, nb)
	sumA := make([]uint64, nb)
	bcnt := make([]uint64, nb)
	bsum := make([]uint64, nb)
	for b := 0; b < nb; b++ {
		cnt[b] = uint64(200 + b)                   // <=255
		sum[b] = uint64(200+b) * uint64(100+b)     // fits 32b
		sum2[b] = uint64(200+b) * uint64(65025)    // exercises the 48b field (>32b)
		cntA[b] = uint64(100 + b)
		sumA[b] = uint64(100+b) * uint64(120+b)
		bcnt[b] = uint64(150 + b)
		bsum[b] = uint64(150+b) * uint64(77+b)
	}
	words := pack(nb, cnt, sum, sum2, cntA, sumA, bcnt, bsum)

	g, err := Unpack(words, gridL, k, 1, true, 2.5e-9, 1234, 3)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if g.GridL != gridL || g.K != k || g.Align != 1 || g.Hits != 1234 || g.Frames != 3 || g.SampleS != 2.5e-9 {
		t.Fatalf("metadata mismatch: %+v", g)
	}
	for b := 0; b < nb; b++ {
		if g.ACnt[b] != cnt[b] || g.ASum[b] != sum[b] || g.ASum2[b] != sum2[b] ||
			g.ACntA[b] != cntA[b] || g.ASumA[b] != sumA[b] ||
			g.BCnt[b] != bcnt[b] || g.BSum[b] != bsum[b] {
			t.Fatalf("bin %d field mismatch: got cnt=%d sum=%d sum2=%d cntA=%d sumA=%d bcnt=%d bsum=%d",
				b, g.ACnt[b], g.ASum[b], g.ASum2[b], g.ACntA[b], g.ASumA[b], g.BCnt[b], g.BSum[b])
		}
	}

	// The reassembled grid must crunch through the unchanged Result path.
	var st superres.Stack
	if err := st.InjectBins(g); err != nil {
		t.Fatalf("InjectBins(full): %v", err)
	}
}

func TestUnpackMeanOnly(t *testing.T) {
	// Mirrors the SHIPPING fabric (FULLSTATS=0): words 3..8 and 9..11 are all zero;
	// Unpack with full=false must leave sum2/sumA/cntA nil so Result is mean-only.
	const gridL, k = 8, 2
	nb := gridL * k
	cnt := make([]uint64, nb)
	sum := make([]uint64, nb)
	zero := make([]uint64, nb)
	for b := 0; b < nb; b++ {
		cnt[b] = uint64(10 + b)
		sum[b] = uint64(10+b) * 130
	}
	words := pack(nb, cnt, sum, zero, zero, zero, zero, zero)

	g, err := Unpack(words, gridL, k, 0, false, 1e-9, 42, 1)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if g.ASum2 != nil || g.ASumA != nil || g.ACntA != nil {
		t.Fatalf("mean-only must leave optional arrays nil")
	}
	for b := 0; b < nb; b++ {
		if g.ACnt[b] != cnt[b] || g.ASum[b] != sum[b] {
			t.Fatalf("bin %d mean mismatch: cnt=%d sum=%d", b, g.ACnt[b], g.ASum[b])
		}
	}
	var st superres.Stack
	if err := st.InjectBins(g); err != nil {
		t.Fatalf("InjectBins(mean): %v", err)
	}
}

func TestUnpackLengthMismatch(t *testing.T) {
	if _, err := Unpack(make([]uint16, 10), 4, 4, 0, false, 0, 0, 1); err == nil {
		t.Fatalf("expected length-mismatch error")
	}
	if _, err := Unpack(nil, 0, 4, 0, false, 0, 0, 1); err == nil {
		t.Fatalf("expected bad-dims error")
	}
}

func TestArmBits(t *testing.T) {
	if got := ArmRun(0x0005); got != 0x0025 {
		t.Fatalf("ArmRun: got %#x want 0x0025", got)
	}
	if got := ArmXform(0x0003); got != 0x0207 {
		t.Fatalf("ArmXform: got %#x want 0x0207", got)
	}
	if DrainWords(64, 4) != 256*WordsPerBin {
		t.Fatalf("DrainWords wrong")
	}
}
