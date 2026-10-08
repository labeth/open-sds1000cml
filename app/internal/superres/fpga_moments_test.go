// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"math/big"
	"math/rand"
	"testing"
)

// TRLC-LINKS: REQ-SDS-141
func TestFPGAMomentsWire(t *testing.T) {
	rng := rand.New(rand.NewSource(1415))
	for i := 0; i < 1024; i++ {
		m := FPGAStackMoments{Sum: [2]uint64{rng.Uint64(), rng.Uint64() & 31}, SumSquares: [2]uint64{rng.Uint64(), rng.Uint64() & ((1 << 42) - 1)}, SumOdd: [2]uint64{rng.Uint64(), rng.Uint64() & 31}, Count: rng.Uint32(), CountOdd: rng.Uint32()}
		words, err := EncodeFPGAMoments(m)
		if err != nil {
			t.Fatal(err)
		}
		// Independent arbitrary-precision packing checks offsets and field widths,
		// not only whether the encoder and decoder reverse each other's mistakes.
		want := new(big.Int)
		for _, f := range []struct {
			offset uint
			v      [2]uint64
		}{{0, m.Sum}, {69, m.SumSquares}, {175, [2]uint64{uint64(m.Count), 0}}, {207, m.SumOdd}, {276, [2]uint64{uint64(m.CountOdd), 0}}} {
			v := new(big.Int).SetUint64(f.v[1])
			v.Lsh(v, 64)
			v.Or(v, new(big.Int).SetUint64(f.v[0]))
			v.Lsh(v, f.offset)
			want.Or(want, v)
		}
		got := new(big.Int)
		for j := len(words) - 1; j >= 0; j-- {
			got.Lsh(got, 16)
			got.Or(got, new(big.Int).SetUint64(uint64(words[j])))
		}
		if got.Cmp(want) != 0 {
			t.Fatalf("wire mismatch fixture %d", i)
		}
		decoded, err := DecodeFPGAMoments(words[:])
		if err != nil || decoded != m {
			t.Fatalf("round trip fixture %d: %v", i, err)
		}
	}
	for _, m := range []FPGAStackMoments{{Sum: [2]uint64{0, 32}}, {SumOdd: [2]uint64{0, 32}}, {SumSquares: [2]uint64{0, 1 << 42}}} {
		if _, err := EncodeFPGAMoments(m); err == nil {
			t.Fatal("accepted overflow")
		}
	}
	for _, n := range []int{0, 19, 21} {
		if _, err := DecodeFPGAMoments(make([]uint16, n)); err == nil {
			t.Fatal("accepted wrong length")
		}
	}
	for bit := 4; bit < 16; bit++ {
		words := make([]uint16, 20)
		words[19] = 1 << bit
		if _, err := DecodeFPGAMoments(words); err == nil {
			t.Fatal("accepted padding")
		}
	}
}
