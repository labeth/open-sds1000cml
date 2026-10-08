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
func TestStackAccumulateRTL(t *testing.T) {
	type fixture struct {
		enabled, odd    bool
		value           uint64
		sum, sum2, sumA *big.Int
		count, countA   uint32
		abort, delay    int
	}
	max := func(n uint) *big.Int { return new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), n), big.NewInt(1)) }
	base := func(v uint64, odd bool) fixture {
		return fixture{true, odd, v, new(big.Int), new(big.Int), new(big.Int), 0, 0, 0, 0}
	}
	fixtures := []fixture{}
	for _, v := range []uint64{0, 1, 1 << 24, 255 << 24, (1 << 37) - 1} {
		for _, odd := range []bool{false, true} {
			fixtures = append(fixtures, base(v, odd))
		}
	}
	for k := 0; k < 5; k++ {
		f := base((1<<37)-1, true)
		switch k {
		case 0:
			f.sum = max(69)
		case 1:
			f.sum2 = max(106)
		case 2:
			f.sumA = max(69)
		case 3:
			f.count = ^uint32(0)
		case 4:
			f.countA = ^uint32(0)
		}
		fixtures = append(fixtures, f)
		f.enabled = false
		fixtures = append(fixtures, f)
	}
	f := base(7, false)
	f.countA = ^uint32(0)
	f.sumA = max(69)
	fixtures = append(fixtures, f)
	for _, kind := range []int{1, 2} {
		for _, delay := range []int{0, 1, 37, 100, 199, 205} {
			f := base((1<<37)-1, true)
			f.abort = kind
			f.delay = delay
			fixtures = append(fixtures, f)
		}
	}
	rng := rand.New(rand.NewSource(141))
	limit69 := new(big.Int).Lsh(big.NewInt(1), 69)
	limit106 := new(big.Int).Lsh(big.NewInt(1), 106)
	for i := 0; i < 160; i++ {
		f := base(rng.Uint64()&((1<<37)-1), i%2 != 0)
		f.sum = new(big.Int).Rand(rng, limit69)
		f.sum2 = new(big.Int).Rand(rng, limit106)
		f.sumA = new(big.Int).Rand(rng, limit69)
		f.count = rng.Uint32()
		f.countA = rng.Uint32()
		fixtures = append(fixtures, f)
	}
	s, s2, sa := new(big.Int), new(big.Int), new(big.Int)
	var n, na uint32
	for i := 0; i < 80; i++ {
		f := base(rng.Uint64()&((1<<37)-1), i%2 != 0)
		f.sum.Set(s)
		f.sum2.Set(s2)
		f.sumA.Set(sa)
		f.count = n
		f.countA = na
		fixtures = append(fixtures, f)
		v := new(big.Int).SetUint64(f.value)
		s.Add(s, v)
		s2.Add(s2, new(big.Int).Mul(v, v))
		n++
		if f.odd {
			sa.Add(sa, v)
			na++
		}
	}
	var input strings.Builder
	expected := []string{}
	for i, f := range fixtures {
		en, odd := 0, 0
		if f.enabled {
			en = 1
		}
		if f.odd {
			odd = 1
		}
		fmt.Fprintf(&input, "%d %d %d %s %s %d %s %d %d %d %d\n", en, odd, f.value, f.sum, f.sum2, f.count, f.sumA, f.countA, i%9, f.abort, f.delay)
		a, b, c := new(big.Int).Set(f.sum), new(big.Int).Set(f.sum2), new(big.Int).Set(f.sumA)
		n, na := uint64(f.count), uint64(f.countA)
		bad := 0
		if f.enabled {
			v := new(big.Int).SetUint64(f.value)
			a.Add(a, v)
			b.Add(b, new(big.Int).Mul(v, v))
			n++
			if f.odd {
				c.Add(c, v)
				na++
			}
			if a.BitLen() > 69 || b.BitLen() > 106 || c.BitLen() > 69 || n > uint64(^uint32(0)) || na > uint64(^uint32(0)) {
				bad = 1
				a.Set(f.sum)
				b.Set(f.sum2)
				c.Set(f.sumA)
				n = uint64(f.count)
				na = uint64(f.countA)
			}
		}
		expected = append(expected, fmt.Sprintf("A %d %s %s %d %s %d", bad, a, b, n, c, na))
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fixtures.txt")
	if err := os.WriteFile(path, []byte(input.String()), 0600); err != nil {
		t.Fatal(err)
	}
	image := compileStackRTL(t, dir, "tb_stack_accumulate", "stack_accumulate.v", "sim/tb_stack_accumulate.v")
	out, err := exec.Command("vvp", image, "+input="+path).CombinedOutput()
	if err != nil {
		t.Fatalf("simulation: %v\n%s", err, out)
	}
	lines := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "A ") {
			lines = append(lines, line)
		}
	}
	if len(lines) != len(expected) {
		t.Fatalf("results %d want %d\n%s", len(lines), len(expected), out)
	}
	maxCycles := 0
	for i, line := range lines {
		fields := strings.Fields(line)
		got := strings.Join(fields[:len(fields)-1], " ")
		if got != expected[i] {
			t.Fatalf("fixture %d:\ngot  %s\nwant %s", i, got, expected[i])
		}
		var cycles int
		fmt.Sscan(fields[len(fields)-1], &cycles)
		if cycles > maxCycles {
			maxCycles = cycles
		}
	}
	t.Logf("%d exact-moment fixtures; maximum %d clocks; overflow, mask, stalls, reset and replacing starts passed", len(fixtures), maxCycles)
}
