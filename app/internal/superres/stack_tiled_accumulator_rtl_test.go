// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-141
func TestStackTiledAccumulatorRTL(t *testing.T) {
	type moment struct {
		s, q, a *big.Int
		n, na   uint32
	}
	limbs := func(x *big.Int) [2]uint64 {
		return [2]uint64{x.Uint64(), new(big.Int).Rsh(new(big.Int).Set(x), 64).Uint64()}
	}
	state := func(m moment) FPGAStackMoments {
		return FPGAStackMoments{Sum: limbs(m.s), SumSquares: limbs(m.q), SumOdd: limbs(m.a), Count: m.n, CountOdd: m.na}
	}
	var input strings.Builder
	expected := []FPGAStackMoments{}
	hits, uploads := 0, 0
	for _, factor := range []int64{3, 7} {
		for first := int64(0); first < 24; first += 8 {
			var moments [16]moment
			for i := range moments {
				v := big.NewInt((first*2 + int64(i) + 10) << 24)
				m := moment{new(big.Int).Mul(v, big.NewInt(3)), new(big.Int).Mul(new(big.Int).Mul(v, v), big.NewInt(3)), new(big.Int).Set(v), 3, 1}
				moments[i] = m
				words, err := EncodeFPGAMoments(state(m))
				if err != nil {
					t.Fatal(err)
				}
				packed := new(big.Int)
				for w := 19; w >= 0; w-- {
					packed.Lsh(packed, 16)
					packed.Or(packed, new(big.Int).SetUint64(uint64(words[w])))
				}
				fmt.Fprintf(&input, "0 %d %d %080x\n", i/2, i%2, packed)
				uploads++
			}
			for h, p := range []int64{0, 1, 3, 28, 30, 31} {
				delta := []int64{-1 << 22, 0, 1 << 23, 12345}[h%4]
				mask := []int{3, 1, 2, 0, 3, 3}[h]
				odd := h % 2
				fmt.Fprintf(&input, "2 %d %d %d %d %d %d 8\n", p, first, delta, factor, mask, odd)
				hits++
				for b := int64(0); b < 8; b++ {
					pos := (p << 24) + delta + ((first+b)<<24)/factor
					if pos < 0 || pos>>24 >= 31 {
						continue
					}
					idx, frac := pos>>24, pos&((1<<24)-1)
					for ch := 0; ch < 2; ch++ {
						if mask&(1<<ch) == 0 {
							continue
						}
						slope, offset := int64(37), int64(5)
						if ch == 1 {
							slope = 19
							offset = 3
						}
						v := big.NewInt(((idx*slope + offset) << 20) + ((slope << 20) * frac >> 24))
						m := &moments[int(b)*2+ch]
						m.s.Add(m.s, v)
						m.q.Add(m.q, new(big.Int).Mul(v, v))
						m.n++
						if odd != 0 {
							m.a.Add(m.a, v)
							m.na++
						}
					}
				}
			}
			for i, m := range moments {
				fmt.Fprintf(&input, "1 %d %d\n", i/2, i%2)
				expected = append(expected, state(m))
			}
		}
	}
	for ch := 0; ch < 2; ch++ {
		fmt.Fprintf(&input, "1 31 %d\n", ch)
		expected = append(expected, FPGAStackMoments{})
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(p, []byte(input.String()), 0600); err != nil {
		t.Fatal(err)
	}
	image := compileStackRTL(t, dir, "tb_stack_tiled_accumulator", "stack_tiled_accumulator.v", "stack_tile_access.v", "stack_tile_transfer.v", "stack_state_tile.v", "stack_resample_store.v", "stack_resample.v", "stack_positions.v", "stack_interpolate.v", "stack_accumulate.v", "stack_bin_writer.v", "sim/tb_stack_tiled_accumulator.v")
	out, err := exec.Command("vvp", image, "+input="+p).CombinedOutput()
	if err != nil {
		t.Fatalf("simulation %v\n%s", err, out)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "S ") {
			continue
		}
		packed, ok := new(big.Int).SetString(strings.TrimPrefix(line, "S "), 16)
		if !ok {
			t.Fatal(line)
		}
		var words [20]uint16
		for i := range words {
			words[i] = uint16(packed.Uint64())
			packed.Rsh(packed, 16)
		}
		got, err := DecodeFPGAMoments(words[:])
		if n >= len(expected) || err != nil || got != expected[n] {
			t.Fatalf("state %d mismatch: %v, got %+v", n, err, got)
		}
		n++
	}
	if n != len(expected) || !strings.Contains(string(out), "PASS host-restored") {
		t.Fatalf("incomplete simulation\n%s", out)
	}
	t.Logf("%d host uploads, %d accepted hits and %d exact Go-decoded state downloads passed, plus ownership, sample faults, overflow and reset recovery", uploads, hits, n)
}
