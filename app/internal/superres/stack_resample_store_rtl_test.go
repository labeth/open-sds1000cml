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
func TestStackResampleStoreRTL(t *testing.T) {
	type moments struct {
		s, q, a *big.Int
		n, na   int
	}
	var want [16]moments
	for i := range want {
		want[i] = moments{new(big.Int), new(big.Int), new(big.Int), 0, 0}
	}
	var input strings.Builder
	fixtures := 0
	for _, p := range []int64{0, 4, 29, 31} {
		for _, delta := range []int64{-1 << 23, 0, 1 << 22} {
			for _, factor := range []int64{1, 3, 8} {
				for mask := 0; mask < 4; mask++ {
					odd := fixtures % 2
					reads, ops := 0, 0
					for bin := int64(0); bin < 8; bin++ {
						q := (p << 24) + delta + (bin<<24)/factor
						if q < 0 || (q>>24)+1 >= 32 || mask == 0 {
							continue
						}
						reads++
						idx, frac := q>>24, q&((1<<24)-1)
						for ch := 0; ch < 2; ch++ {
							if mask&(1<<ch) == 0 {
								continue
							}
							ops += 2
							slope, offset := int64(37), int64(5)
							if ch == 1 {
								slope = 19
								offset = 3
							}
							value := ((idx*slope + offset) << 20) + ((slope << 20) * frac >> 24)
							m := &want[int(bin)*2+ch]
							v := big.NewInt(value)
							m.s.Add(m.s, v)
							m.q.Add(m.q, new(big.Int).Mul(v, v))
							m.n++
							if odd != 0 {
								m.a.Add(m.a, v)
								m.na++
							}
						}
					}
					fmt.Fprintf(&input, "%d %d %d %d %d %d %d\n", p, delta, factor, mask, odd, reads, ops)
					fixtures++
				}
			}
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(path, []byte(input.String()), 0600); err != nil {
		t.Fatal(err)
	}
	image := compileStackRTL(t, dir, "tb_stack_resample_store", "stack_positions.v", "stack_interpolate.v", "stack_resample.v", "stack_accumulate.v", "stack_bin_writer.v", "stack_resample_store.v", "sim/tb_stack_resample_store.v")
	out, err := exec.Command("vvp", image, "+input="+path).CombinedOutput()
	if err != nil {
		t.Fatalf("simulation %v\n%s", err, out)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "B ") {
			continue
		}
		m := want[n]
		expected := fmt.Sprintf("B %d %s %s %d %s %d", n, m.s, m.q, m.n, m.a, m.na)
		if line != expected {
			t.Fatalf("got %s want %s", line, expected)
		}
		n++
	}
	if n != 16 || !strings.Contains(string(out), "PASS connected") {
		t.Fatalf("incomplete results\n%s", out)
	}
	t.Logf("%d connected hits: exact persisted moments, boundaries, masks, busy starts, adapter faults and reset recovery passed", fixtures)
}
