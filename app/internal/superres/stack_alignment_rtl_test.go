// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Integer oracle for Q48 scores -> Q24 parabolic offset, independently using
// arbitrary precision. Clipping and truncation are part of the hardware ABI.
// TRLC-LINKS: REQ-SDS-141
func exactAlignment(l, c, r int64, lp, rp bool) (int64, bool) {
	const unit = int64(1) << 48
	if c < -unit || c > unit || lp && (l < -unit || l > unit) || rp && (r < -unit || r > unit) {
		return 0, true
	}
	if !lp || !rp {
		return 0, false
	}
	denominator := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(c), 1), new(big.Int).Add(big.NewInt(l), big.NewInt(r)))
	if denominator.Sign() <= 0 {
		return 0, false
	}
	denominator.Lsh(denominator, 1)
	numerator := new(big.Int).Lsh(new(big.Int).Sub(big.NewInt(r), big.NewInt(l)), 24)
	q := new(big.Int).Quo(numerator, denominator)
	bound := big.NewInt(1 << 23)
	if q.Cmp(bound) > 0 {
		return 1 << 23, false
	}
	if q.Cmp(new(big.Int).Neg(bound)) < 0 {
		return -(1 << 23), false
	}
	return q.Int64(), false
}

// TRLC-LINKS: REQ-SDS-141
func TestStackAlignmentRTL(t *testing.T) {
	const unit = int64(1) << 48
	type fixture struct {
		l, c, r              int64
		lp, rp, abort, delay int
	}
	fixtures := []fixture{
		{0, unit, 0, 1, 1, 0, 0}, {0, unit, unit / 2, 1, 1, 0, 0}, {unit / 2, unit, 0, 1, 1, 0, 0},
		{unit, unit, unit, 1, 1, 0, 0}, {unit, 0, unit, 1, 1, 0, 0},
		{0, unit, unit, 1, 1, 0, 0}, {unit, unit, 0, 1, 1, 0, 0},
		{-unit, unit, unit, 1, 1, 0, 0}, {unit, unit, -unit, 1, 1, 0, 0},
		{unit - 2, unit - 1, unit - 1, 1, 1, 0, 0},
		{-unit, 0, unit - 1, 1, 1, 0, 0}, {unit - 1, 0, -unit, 1, 1, 0, 0},
		{0, unit, unit / 2, 0, 1, 0, 0}, {0, unit, unit / 2, 1, 0, 0, 0},
		{unit + 1, unit, 0, 1, 1, 0, 0}, {0, unit + 1, 0, 1, 1, 0, 0}, {0, 0, -unit - 1, 1, 1, 0, 0},
		{unit + 1, unit, 0, 0, 1, 0, 0},
	}
	rng := rand.New(rand.NewSource(141027))
	for k := 0; k < 400; k++ {
		fixtures = append(fixtures, fixture{rng.Int63n(2*unit+1) - unit, rng.Int63n(2*unit+1) - unit, rng.Int63n(2*unit+1) - unit, 1, 1, 0, 0})
	}
	for _, a := range []int{1, 2} {
		for _, d := range []int{0, 1, 2, 3, 10, 75, 150, 220} {
			fixtures = append(fixtures, fixture{unit / 2, unit, 0, 1, 1, a, d})
		}
	}
	var input strings.Builder
	for _, f := range fixtures {
		fmt.Fprintf(&input, "%d %d %d %d %d %d %d\n", f.l, f.c, f.r, f.lp, f.rp, f.abort, f.delay)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "input.txt")
	image := filepath.Join(dir, "alignment.vvp")
	if err := os.WriteFile(file, []byte(input.String()), 0600); err != nil {
		t.Fatal(err)
	}
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	if out, err := exec.Command("iverilog", "-g2012", "-s", "tb_stack_alignment", "-o", image, filepath.Join(rtl, "stack_alignment.v"), filepath.Join(rtl, "sim/tb_stack_alignment.v")).CombinedOutput(); err != nil {
		t.Fatalf("compile %v: %s", err, out)
	}
	out, err := exec.Command("vvp", image, "+input="+file).CombinedOutput()
	if err != nil {
		t.Fatalf("simulate %v: %s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(fixtures) {
		t.Fatalf("results %d expected %d: %s", len(lines), len(fixtures), out)
	}
	maxCycles := 0
	for k, line := range lines {
		var invalid, cycles int
		var delta int64
		if _, err := fmt.Sscanf(line, "A %d %d %d", &invalid, &delta, &cycles); err != nil {
			t.Fatal(err)
		}
		f := fixtures[k]
		want, bad := exactAlignment(f.l, f.c, f.r, f.lp != 0, f.rp != 0)
		if invalid != 0 != bad || delta != want {
			t.Fatalf("fixture %d: got (%d,%d), want (%d,%v)", k, delta, invalid, want, bad)
		}
		if cycles > maxCycles {
			maxCycles = cycles
		}
		if !bad && f.lp != 0 && f.rp != 0 {
			l, c, r := float64(f.l)/float64(unit), float64(f.c)/float64(unit), float64(f.r)/float64(unit)
			den := l - 2*c + r
			wantFloat := 0.0
			if den < 0 {
				wantFloat = math.Max(-.5, math.Min(.5, .5*(l-r)/den))
			}
			if math.Abs(float64(delta)/(1<<24)-wantFloat) > 1.0/(1<<24)+1e-15 {
				t.Fatalf("float alignment differs: %g vs %g", float64(delta)/(1<<24), wantFloat)
			}
		}
	}
	t.Logf("%d alignment fixtures; maximum latency %d clocks; error at most one Q24 unit", len(fixtures), maxCycles)
}
