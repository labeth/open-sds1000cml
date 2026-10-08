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
func TestStackStateTileRTL(t *testing.T) {
	rng := rand.New(rand.NewSource(1413))
	var input strings.Builder
	expected := []string{}
	var memory [64]string
	clear := func() {
		for i := range memory {
			memory[i] = "0 0 0 0 0"
		}
	}
	clear()
	emit := func(write bool, bin uint32, ch int, abort int) {
		w := 0
		if write {
			w = 1
		}
		random := func(bits uint) *big.Int { return new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), bits)) }
		data := fmt.Sprintf("%s %s %d %s %d", random(69), random(106), rng.Uint32(), random(69), rng.Uint32())
		fmt.Fprintf(&input, "%d %d %d %s %d %d\n", w, bin, ch, data, len(expected)%8, abort)
		out := "T 0 0 0 0 0 0"
		if abort != 0 {
			clear()
		} else if bin >= 32 {
			out = "T 1 0 0 0 0 0"
		} else if write {
			memory[int(bin)*2+ch] = data
		} else {
			out = "T 0 " + memory[int(bin)*2+ch]
		}
		expected = append(expected, out)
	}
	for bin := uint32(0); bin < 32; bin++ {
		for ch := 0; ch < 2; ch++ {
			emit(false, bin, ch, 0)
			emit(true, bin, ch, 0)
		}
	}
	for i := 0; i < 200; i++ {
		emit(i%3 == 0, uint32(rng.Intn(32)), rng.Intn(2), 0)
	}
	for _, bin := range []uint32{32, 33, 1 << 31, ^uint32(0)} {
		emit(false, bin, 0, 0)
		emit(true, bin, 1, 0)
	}
	for _, delay := range []int{1, 5, 9} {
		emit(true, 12, 1, delay)
		for bin := uint32(0); bin < 32; bin++ {
			emit(false, bin, 0, 0)
			emit(false, bin, 1, 0)
		}
		emit(true, 25, 1, 0)
		emit(false, 25, 1, 0)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(p, []byte(input.String()), 0600); err != nil {
		t.Fatal(err)
	}
	image := compileStackRTL(t, dir, "tb_stack_state_tile", "stack_state_tile.v", "sim/tb_stack_state_tile.v")
	out, err := exec.Command("vvp", image, "+input="+p).CombinedOutput()
	if err != nil {
		t.Fatalf("simulation %v\n%s", err, out)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "T ") {
			continue
		}
		if n >= len(expected) || line != expected[n] {
			t.Fatalf("fixture %d got %s want %s", n, line, expected[n])
		}
		n++
	}
	if n != len(expected) {
		t.Fatalf("results %d want %d", n, len(expected))
	}
	t.Logf("%d state operations: all bins/channels, bank crossings, 308-bit packing, bounds, stalls and reset during writes passed", n)
}
