// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-141
func compileStackRTL(t *testing.T, dir, bench string, files ...string) string {
	t.Helper()
	image := filepath.Join(dir, bench+".vvp")
	args := []string{"-g2012", "-s", bench, "-o", image}
	root := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	for _, f := range files {
		args = append(args, filepath.Join(root, f))
	}
	if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile %v: %s", err, out)
	}
	return image
}

// TRLC-LINKS: REQ-SDS-141
func TestStackPositionsRTL(t *testing.T) {
	type fixture struct {
		p, b, k, n   uint32
		d            int64
		abort, delay int
		first        uint32
	}
	fixtures := []fixture{
		{0, 20, 4, 8, -1 << 23, 0, 0, 0}, {0, 20, 4, 8, 1 << 23, 0, 0, 0}, {7, 20, 4, 8, 0, 0, 0, 0},
		{0, 8, 1, 0, 0, 0, 0, 0}, {0, 8, 1, 1, 0, 0, 0, 0},
		{^uint32(0), 20, 1, ^uint32(0), 1 << 23, 0, 0, 0},
		{2, 12, 0, 30, 0, 0, 0, 0}, {2, 0, 4, 30, 0, 0, 0, 0}, {2, 12, 4, 30, 1<<23 + 1, 0, 0, 0},
	}
	for _, k := range []uint32{1, 2, 3, 7, 16, 32, 64, 255, 65535, 1 << 24, 1<<24 + 1, ^uint32(0)} {
		fixtures = append(fixtures, fixture{3, 513, k, 1000, -2359296, 0, 0, 0})
	}
	for _, a := range []int{1, 2} {
		for _, d := range []int{0, 1, 20, 74, 80, 100} {
			fixtures = append(fixtures, fixture{2, 65, 7, 100, 1234567, a, d, 0})
		}
	}
	for _, first := range []uint32{1, 31, 32, 63, 1025, 1<<24 + 3} {
		for _, factor := range []uint32{1, 3, 7, 65535, 1<<24 + 1, ^uint32(0)} {
			fixtures = append(fixtures, fixture{3, 35, factor, ^uint32(0), -2359296, 0, 0, first})
		}
	}
	fixtures = append(fixtures, fixture{0, 1, 1, ^uint32(0), 0, 0, 0, ^uint32(0)}, fixture{0, 2, 1, ^uint32(0), 0, 0, 0, ^uint32(0)}, fixture{0, 3, 7, ^uint32(0), 0, 0, 0, ^uint32(0) - 2})
	for _, delay := range []int{76, 120, 240, 250, 300} {
		fixtures = append(fixtures, fixture{3, 20, 7, 1000, 12345, 2, delay, 33})
	}
	dir := t.TempDir()
	image := compileStackRTL(t, dir, "tb_stack_positions", "stack_positions.v", "sim/tb_stack_positions.v")
	for no, f := range fixtures {
		t.Run(fmt.Sprint(no), func(t *testing.T) {
			file := filepath.Join(dir, "positions.txt")
			input := fmt.Sprintf("%d %d %d %d %d %d %d %d\n", f.p, f.b, f.k, f.n, f.d, f.abort, f.delay, f.first)
			if err := os.WriteFile(file, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("vvp", image, "+input="+file).CombinedOutput()
			if err != nil {
				t.Fatalf("simulate %v: %s", err, out)
			}
			bad := uint64(f.first)+uint64(f.b) > 1<<32 || f.k == 0 || f.b == 0 || f.d > 1<<23 || f.d < -(1<<23)
			count := uint32(0)
			completed := false
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if strings.HasPrefix(line, "D ") {
					expect := 0
					if bad {
						expect = 1
					}
					if line != fmt.Sprintf("D %d", expect) {
						t.Fatalf("completion %q", line)
					}
					completed = true
					continue
				}
				var bin uint32
				var index, frac int64
				var eligible int
				if _, err := fmt.Sscanf(line, "P %d %d %d %d", &bin, &index, &frac, &eligible); err != nil {
					t.Fatal(err)
				}
				if bad {
					t.Fatal("invalid geometry emitted a bin")
				}
				// All terms fit signed 64 bits: uint32 indices shifted by 24 use <=57 bits.
				fixed := (int64(f.p) << 24) + f.d + int64(((uint64(f.first)+uint64(bin))<<24)/uint64(f.k))
				wantIndex, wantFraction := fixed>>24, fixed&((1<<24)-1)
				wantEligible := 0
				if wantIndex >= 0 && wantIndex+1 < int64(f.n) {
					wantEligible = 1
				}
				if bin != count || index != wantIndex || frac != wantFraction || eligible != wantEligible {
					t.Fatalf("bin %d got (%d,%d,%d), want (%d,%d,%d)", bin, index, frac, eligible, wantIndex, wantFraction, wantEligible)
				}
				count++
			}
			if !completed || !bad && count != f.b {
				t.Fatalf("completion=%v bins=%d expected=%d", completed, count, f.b)
			}
		})
	}
	t.Logf("%d position fixtures; general uint32 factors, boundary skips, stalls and aborts", len(fixtures))
}

// TRLC-LINKS: REQ-SDS-141
func TestStackInterpolateRTL(t *testing.T) {
	type fixture struct {
		v                  [4]uint64
		w                  uint32
		mask, abort, delay int
	}
	var fixtures []fixture
	const max = (uint64(1) << 37) - 1
	for _, w := range []uint32{0, 1, 1 << 23, (1 << 24) - 1} {
		for _, mask := range []int{0, 1, 2, 3} {
			fixtures = append(fixtures, fixture{[4]uint64{0, max, max, 0}, w, mask, 0, 0})
		}
	}
	rng := rand.New(rand.NewSource(141028))
	for i := 0; i < 300; i++ {
		var v [4]uint64
		for k := range v {
			v[k] = uint64(rng.Int63n(1 << 37))
		}
		fixtures = append(fixtures, fixture{v, uint32(rng.Intn(1 << 24)), rng.Intn(4), 0, 0})
	}
	for _, a := range []int{1, 2} {
		for _, d := range []int{0, 1, 2, 5, 15, 26, 40, 52} {
			fixtures = append(fixtures, fixture{[4]uint64{40 << 24, 200 << 24, 180 << 24, 60 << 24}, 1234567, 3, a, d})
		}
	}
	var input strings.Builder
	for _, f := range fixtures {
		fmt.Fprintf(&input, "%d %d %d %d %d %d %d %d\n", f.v[0], f.v[1], f.v[2], f.v[3], f.w, f.mask, f.abort, f.delay)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "interpolate.txt")
	if err := os.WriteFile(file, []byte(input.String()), 0600); err != nil {
		t.Fatal(err)
	}
	image := compileStackRTL(t, dir, "tb_stack_interpolate", "stack_interpolate.v", "sim/tb_stack_interpolate.v")
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
		var got [2]uint64
		var mask, cycles int
		if _, err := fmt.Sscanf(line, "I %d %d %d %d", &got[0], &got[1], &mask, &cycles); err != nil {
			t.Fatal(err)
		}
		f := fixtures[k]
		if mask != f.mask {
			t.Fatal("mask changed")
		}
		if cycles > maxCycles {
			maxCycles = cycles
		}
		for ch := 0; ch < 2; ch++ {
			want := uint64(0)
			if mask&(1<<ch) != 0 {
				a, b := f.v[ch*2], f.v[ch*2+1]
				if b >= a {
					want = a + ((b - a) * uint64(f.w) >> 24)
				} else {
					want = a - ((a - b) * uint64(f.w) >> 24)
				}
				weight := float64(f.w) / (1 << 24)
				expected := float64(a)/(1<<24)*(1-weight) + float64(b)/(1<<24)*weight
				if math.Abs(float64(got[ch])/(1<<24)-expected) > 1.0/(1<<24)+1e-10 {
					t.Fatalf("float interpolation differs at %d ch%d", k, ch)
				}
			}
			if got[ch] != want {
				t.Fatalf("case %d channel%d got=%d want=%d", k, ch, got[ch], want)
			}
		}
	}
	t.Logf("%d interpolation fixtures; maximum latency %d clocks; error below one Q24 unit per channel", len(fixtures), maxCycles)
}

// TRLC-LINKS: REQ-SDS-141
func TestStackResampleRTL(t *testing.T) {
	type fixture struct {
		p, b, k, n              int
		d                       int64
		mask, odd, abort, fault int
	}
	fixtures := []fixture{
		{0, 64, 4, 16, -1 << 23, 3, 1, 0, 0}, {14, 32, 4, 16, 1 << 23, 3, 0, 0, 0},
		{0, 32, 1, 32, 0, 3, 1, 0, 0}, {2, 80, 3, 32, 1234567, 3, 0, 0, 0},
		{2, 80, 7, 32, -1234567, 3, 1, 0, 0}, {3, 96, 32, 32, 1234567, 1, 1, 0, 0},
		{3, 96, 64, 32, -1234567, 2, 0, 0, 0}, {3, 16, 4, 32, 0, 0, 1, 0, 0},
		{0, 8, 4, 0, 0, 3, 0, 0, 0}, {0, 8, 4, 1, 0, 3, 0, 0, 0},
		{2, 32, 4, 32, 1234567, 3, 1, 1, 0}, {2, 32, 4, 32, 1234567, 3, 1, 2, 0},
		{2, 32, 4, 32, 1234567, 3, 1, 3, 0}, {2, 32, 4, 32, 1234567, 3, 1, 4, 1},
		{2, 32, 4, 32, 0, 3, 0, 0, 1}, {2, 32, 0, 32, 0, 3, 0, 0, 0},
		{2, 0, 4, 32, 0, 3, 0, 0, 0}, {2, 32, 4, 32, 1<<23 + 1, 3, 0, 0, 0},
	}
	dir := t.TempDir()
	image := compileStackRTL(t, dir, "tb_stack_resample", "stack_positions.v", "stack_interpolate.v", "stack_resample.v", "sim/tb_stack_resample.v")
	rng := rand.New(rand.NewSource(141029))
	for no, f := range fixtures {
		t.Run(fmt.Sprint(no), func(t *testing.T) {
			var sig [2][]float32
			for ch := 0; ch < 2; ch++ {
				sig[ch] = make([]float32, f.n)
				for k := range sig[ch] {
					sig[ch][k] = float32(rng.Intn(11000)) / 4
				}
			}
			var input strings.Builder
			fmt.Fprintf(&input, "%d %d %d %d %d %d %d %d %d\n", f.p, f.b, f.k, f.n, f.d, f.mask, f.odd, f.abort, f.fault)
			for k := 0; k < f.n; k++ {
				fmt.Fprintf(&input, "%d %d\n", uint64(float64(sig[0][k])*(1<<24)), uint64(float64(sig[1][k])*(1<<24)))
			}
			file := filepath.Join(dir, "resample.txt")
			if err := os.WriteFile(file, []byte(input.String()), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("vvp", image, "+input="+file).CombinedOutput()
			if err != nil {
				t.Fatalf("simulate %v: %s", err, out)
			}
			bad := f.b == 0 || f.k == 0 || f.d > 1<<23 || f.d < -(1<<23) || f.fault != 0 && f.abort != 4
			st := Stack{N: f.n, K: f.k, Nbins: f.b}
			if !bad {
				for ch := 0; ch < 2; ch++ {
					st.C[ch] = chanState{sum: make([]float64, f.b), sum2: make([]float64, f.b), cnt: make([]float64, f.b), sumA: make([]float64, f.b), cntA: make([]float64, f.b)}
					if f.mask&(1<<ch) != 0 {
						st.drizzleHit(ch, sig[ch], float64(f.p)+float64(f.d)/(1<<24), f.odd != 0)
					}
				}
			}
			count, expectedReads := 0, 0
			completed := false
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if line == "R" {
					count = 0
					expectedReads = 0
					continue
				}
				if strings.HasPrefix(line, "D ") {
					var invalid, reads, cycles int
					if _, err := fmt.Sscanf(line, "D %d %d %d", &invalid, &reads, &cycles); err != nil {
						t.Fatal(err)
					}
					if (invalid != 0) != bad {
						t.Fatalf("invalid=%d expected %v", invalid, bad)
					}
					if !bad && reads != expectedReads {
						t.Fatalf("memory reads=%d expected=%d", reads, expectedReads)
					}
					completed = true
					continue
				}
				var bin, mask, odd int
				var v [2]uint64
				if _, err := fmt.Sscanf(line, "B %d %d %d %d %d", &bin, &v[0], &v[1], &mask, &odd); err != nil {
					t.Fatal(err)
				}
				if bad {
					t.Fatal("invalid resampling emitted output")
				}
				if bin != count || odd != f.odd {
					t.Fatalf("output order/parity bin=%d want=%d odd=%d", bin, count, odd)
				}
				fixed := (int64(f.p) << 24) + f.d + int64((uint64(bin)<<24)/uint64(f.k))
				idx, w := fixed>>24, uint64(fixed&((1<<24)-1))
				wantMask := 0
				if idx >= 0 && idx+1 < int64(f.n) {
					wantMask = f.mask
				}
				if mask != wantMask {
					t.Fatalf("mask=%d expected=%d", mask, wantMask)
				}
				if mask != 0 {
					expectedReads++
				}
				for ch := 0; ch < 2; ch++ {
					want := uint64(0)
					if mask&(1<<ch) != 0 {
						a, b := uint64(float64(sig[ch][idx])*(1<<24)), uint64(float64(sig[ch][idx+1])*(1<<24))
						if b >= a {
							want = a + ((b - a) * w >> 24)
						} else {
							want = a - ((a - b) * w >> 24)
						}
						// Coordinate truncation adds at most one Q24 phase unit times slope.
						bound := (math.Abs(float64(sig[ch][idx+1]-sig[ch][idx]))+1)/(1<<24) + 1e-10
						if st.C[ch].cnt[bin] != 1 || math.Abs(float64(v[ch])/(1<<24)-st.C[ch].sum[bin]) > bound {
							t.Fatalf("drizzleHit differs bin%d channel%d", bin, ch)
						}
					} else if st.C[ch].cnt[bin] != 0 {
						t.Fatal("software boundary differs")
					}
					if v[ch] != want {
						t.Fatalf("bin%d channel%d got=%d expected=%d", bin, ch, v[ch], want)
					}
				}
				count++
			}
			if !completed || !bad && count != f.b {
				t.Fatalf("completion=%v bins=%d expected=%d", completed, count, f.b)
			}
		})
	}
	t.Logf("%d memory-to-resampled-bin fixtures compared with drizzleHit", len(fixtures))
}
