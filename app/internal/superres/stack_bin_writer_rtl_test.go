// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-141
func TestStackBinWriterRTL(t *testing.T) {
	type moments struct {
		s, q, a *big.Int
		n, na   int
	}
	var want [16]moments
	for i := range want {
		want[i] = moments{new(big.Int), new(big.Int), new(big.Int), 0, 0}
	}
	rng := rand.New(rand.NewSource(1412))
	var input strings.Builder
	for i := 0; i < 256; i++ {
		bin, mask, odd := rng.Intn(8), i%4, i%3 == 0
		v := [2]uint64{rng.Uint64() & ((1 << 37) - 1), rng.Uint64() & ((1 << 37) - 1)}
		o := 0
		if odd {
			o = 1
		}
		fmt.Fprintf(&input, "%d %d %d %d %d\n", bin, mask, o, v[0], v[1])
		for ch := 0; ch < 2; ch++ {
			if mask&(1<<ch) == 0 {
				continue
			}
			m := &want[bin*2+ch]
			x := new(big.Int).SetUint64(v[ch])
			m.s.Add(m.s, x)
			m.q.Add(m.q, new(big.Int).Mul(x, x))
			m.n++
			if odd {
				m.a.Add(m.a, x)
				m.na++
			}
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(path, []byte(input.String()), 0600); err != nil {
		t.Fatal(err)
	}
	image := compileStackRTL(t, dir, "tb_stack_bin_writer", "stack_accumulate.v", "stack_bin_writer.v", "sim/tb_stack_bin_writer.v")
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
	if n != 16 || !strings.Contains(string(out), "PASS fault and recovery checks") {
		t.Fatalf("incomplete results\n%s", out)
	}
	t.Log("256 two-channel updates across eight bins passed; delayed/stalled memory, masked channels, read/write errors, overflow and reset recovery checked")
}
