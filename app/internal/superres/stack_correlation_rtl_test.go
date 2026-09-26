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
	"reflect"
	"strings"
	"testing"
)

type correlationResult struct {
	Defined, Invalid int
	Score            int64
}

// Independent arbitrary-precision arithmetic oracle for the fixed-point score.
// No intermediate machine-word products are allowed to overflow here.
// TRLC-LINKS: REQ-SDS-141
func exactCorrelation(m windowMoments) correlationResult {
	if m.N == 0 {
		return correlationResult{}
	}
	product := func(a, b uint64) *big.Int {
		return new(big.Int).Mul(new(big.Int).SetUint64(a), new(big.Int).SetUint64(b))
	}
	c := new(big.Int).Sub(product(m.N, m.XY), product(m.X, m.Y))
	ex := new(big.Int).Sub(product(m.N, m.XX), product(m.X, m.X))
	ey := new(big.Int).Sub(product(m.N, m.YY), product(m.Y, m.Y))
	if ex.Sign() < 0 || ey.Sign() < 0 {
		return correlationResult{Invalid: 1}
	}
	if ex.Sign() == 0 || ey.Sign() == 0 {
		return correlationResult{}
	}
	rad := new(big.Int).Lsh(new(big.Int).Mul(ex, ey), 96)
	root := new(big.Int).Sqrt(rad)
	num := new(big.Int).Lsh(new(big.Int).Abs(c), 96)
	q := new(big.Int).Quo(num, root)
	if q.Cmp(new(big.Int).Lsh(big.NewInt(1), 48)) > 0 {
		return correlationResult{Invalid: 1}
	}
	if c.Sign() < 0 {
		q.Neg(q)
	}
	return correlationResult{Defined: 1, Score: q.Int64()}
}

// Check standalone score arithmetic at full moment widths, then the actual
// sample-pair -> moments -> score RTL path, including aborts during arithmetic.
// TRLC-LINKS: REQ-SDS-141
func TestStackCorrelationRTL(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required RTL tool %s: %v", tool, err)
		}
	}
	dir := t.TempDir()
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	run := func(t *testing.T, fromSamples, countBits int, input string, length int, want []correlationResult) {
		t.Helper()
		image := filepath.Join(dir, fmt.Sprintf("score-%d.vvp", fromSamples))
		file := filepath.Join(dir, "input.mem")
		args := []string{"-g2012", "-s", "tb_stack_correlation", fmt.Sprintf("-Ptb_stack_correlation.FROM_SAMPLES=%d", fromSamples), fmt.Sprintf("-Ptb_stack_correlation.COUNT_BITS=%d", countBits), "-o", image}
		for _, f := range []string{"stack_moments.v", "stack_correlation.v", "stack_match_score.v", "sim/tb_stack_correlation.v"} {
			args = append(args, filepath.Join(rtl, f))
		}
		if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		if err := os.WriteFile(file, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("vvp", image, "+input="+file, fmt.Sprintf("+length=%d", length)).CombinedOutput()
		if err != nil {
			t.Fatalf("simulate: %v\n%s", err, out)
		}
		var got []correlationResult
		maxCycles := 0
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			var v correlationResult
			var cycles int
			if _, err := fmt.Sscanf(line, "C %d %d %d %d", &v.Defined, &v.Invalid, &v.Score, &cycles); err != nil {
				t.Fatalf("unexpected %q: %v", line, err)
			}
			got = append(got, v)
			if cycles > maxCycles {
				maxCycles = cycles
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("scores got=%+v want=%+v", got, want)
		}
		t.Logf("%d results; maximum start-to-result latency %d clocks", len(got), maxCycles)
	}
	const valid = uint32(1 << 16)
	const last = uint32(1 << 17)
	const start = uint32(1 << 18)
	const reset = uint32(1 << 19)
	var moments []windowMoments
	var expected []correlationResult
	var commands []uint32
	idle := func(n int) { commands = append(commands, make([]uint32, n)...) }
	// Cancel nonflat windows while multiplying, square-rooting and dividing.
	for _, abort := range []uint32{reset, start} {
		delays := []int{30, 100, 200, 400, 650, 1050, 1400, 1800, 2000}
		// Cover both 21- and 32-bit covariance capture neighborhoods, where a
		// speculative old write must never escape an abort as a valid score.
		for d := 240; d <= 260; d++ {
			delays = append(delays, d)
		}
		for d := 325; d <= 345; d++ {
			delays = append(delays, d)
		}
		// Root-to-division handoff overwrites the shared shift/remainder storage.
		// Sweep the transition neighborhood for both supported physical and
		// full-width modes under reset and replacement-start cancellation.
		for _, base := range []int{1350, 1765} {
			for d := base; d <= base+30; d++ {
				delays = append(delays, d)
			}
		}
		for _, delay := range delays {
			commands = append(commands, start, valid, valid|0x0a0a, valid|0xe6e6, valid|last|0xffff)
			idle(delay)
			commands = append(commands, abort)
			idle(2800)
		}
	}
	rng := rand.New(rand.NewSource(480141))
	for _, length := range []int{1, 4, 9, 24, 127, 256, 1024, 4096, 65535} {
		for _, family := range []string{"random", "same", "inverse", "flat", "small-ripple"} {
			if length == 65535 && family != "random" {
				continue
			}
			commands = append(commands, start)
			var m windowMoments
			x := make([]float32, length)
			y := make([]uint8, length)
			for i := 0; i < length; i++ {
				a, b := uint8(rng.Intn(256)), uint8(rng.Intn(256))
				switch family {
				case "same":
					b = a
				case "inverse":
					b = 255 - a
				case "flat":
					a, b = 255, 255
				case "small-ripple":
					a, b = 128+uint8(i%2), 128+uint8(i%3)
				}
				x[i], y[i] = float32(a), b
				m.N++
				m.X += uint64(a)
				m.Y += uint64(b)
				m.XX += uint64(a) * uint64(a)
				m.YY += uint64(b) * uint64(b)
				m.XY += uint64(a) * uint64(b)
				v := valid | uint32(a) | uint32(b)<<8
				if i == length-1 {
					v |= last
				}
				commands = append(commands, v)
				if i%17 == 0 {
					commands = append(commands, 0xffff, 0xffff)
				}
			}
			idle(2800)
			moments = append(moments, m)
			q := exactCorrelation(m)
			expected = append(expected, q)
			if length < 4 || q.Defined == 0 {
				continue
			}
			tpl := gateTemplate(x, 0, length)
			mean := float64(m.Y) / float64(m.N)
			dot, energy := 0.0, 0.0
			for i := range y {
				v := float64(y[i]) - mean
				dot += tpl.data[i] * v
				energy += v * v
			}
			ref := dot / (tpl.norm * math.Sqrt(energy))
			actual := float64(q.Score) / (1 << 48)
			if math.Abs(actual-ref) > 2e-12 {
				t.Fatalf("N=%d %s Q48 %.17g reference %.17g", length, family, actual, ref)
			}
		}
	}
	t.Run("sample-pairs-through-score-and-aborts", func(t *testing.T) {
		var b strings.Builder
		for _, v := range commands {
			fmt.Fprintf(&b, "%05x\n", v)
		}
		run(t, 1, 32, b.String(), len(commands), expected)
		run(t, 1, 21, b.String(), len(commands), expected)
	})
	t.Run("full-physical-record-through-bounded-moments", func(t *testing.T) {
		const n = 1 << 20
		var b strings.Builder
		fmt.Fprintf(&b, "%05x\n", start)
		for i := 0; i < n; i++ {
			v := valid | uint32(127+2*(i&1))*0x101
			if i == n-1 {
				v |= last
			}
			fmt.Fprintf(&b, "%05x\n", v)
		}
		for i := 0; i < 2800; i++ {
			b.WriteString("00000\n")
		}
		run(t, 1, 21, b.String(), n+2801, []correlationResult{{Defined: 1, Score: 1 << 48}})
	})
	t.Run("bounded-moments-and-truncation-rejection", func(t *testing.T) {
		for _, bits := range []int{1, 2, 20, 21, 22, 31} {
			t.Run(fmt.Sprint(bits), func(t *testing.T) {
				limit := uint64(1)<<bits - 1
				var cases []windowMoments
				for _, m := range moments {
					if m.N > limit {
						continue
					}
					k := limit / m.N
					cases = append(cases, m, windowMoments{m.N * k, m.X * k, m.Y * k, m.XX * k, m.YY * k, m.XY * k})
				}
				// Full physical SRAM record, with positive and negative correlation.
				if bits >= 21 {
					n := uint64(1) << 20
					cases = append(cases, windowMoments{n, n * 128, n * 128, n * 16385, n * 16385, n * 16385},
						windowMoments{n, n * 128, n * 128, n * 16385, n * 16385, n * 16383})
				}
				// Near-maximal energies exercise the root's highest bit; adjacent
				// cross moments must retain Q48 distinctions close to unity.
				maxSquare := uint64(1)<<(bits+16) - 1
				for _, cross := range []uint64{maxSquare, maxSquare - 1, maxSquare / 2} {
					cases = append(cases, windowMoments{N: limit, XX: maxSquare, YY: maxSquare, XY: cross})
				}
				cases = append(cases, windowMoments{N: 1, XX: 1, YY: 2, XY: 1})
				// Unconstrained representable moments exercise subtraction signs,
				// carries and invalid covariance, independently of waveform fixtures.
				random := rand.New(rand.NewSource(int64(bits)))
				for i := 0; i < 128; i++ {
					cases = append(cases, windowMoments{random.Uint64() & limit,
						random.Uint64() & (uint64(1)<<(bits+8) - 1), random.Uint64() & (uint64(1)<<(bits+8) - 1),
						random.Uint64() & (uint64(1)<<(bits+16) - 1), random.Uint64() & (uint64(1)<<(bits+16) - 1),
						random.Uint64() & (uint64(1)<<(bits+16) - 1)})
				}
				cases = append(cases, windowMoments{}, windowMoments{N: 1, X: 1}, windowMoments{N: 1, XX: 1, YY: 1, XY: 2})
				var b strings.Builder
				var want []correlationResult
				for _, m := range cases {
					fmt.Fprintf(&b, "%08x%010x%010x%012x%012x%012x\n", m.N, m.X, m.Y, m.XX, m.YY, m.XY)
					want = append(want, exactCorrelation(m))
				}
				// Each public field must reject its first unrepresentable bit.
				for field := 0; field < 6; field++ {
					m := windowMoments{N: 1, XX: 1, YY: 1}
					switch field {
					case 0:
						m.N = uint64(1) << bits
					case 1:
						m.X = uint64(1) << (bits + 8)
					case 2:
						m.Y = uint64(1) << (bits + 8)
					case 3:
						m.XX = uint64(1) << (bits + 16)
					case 4:
						m.YY = uint64(1) << (bits + 16)
					case 5:
						m.XY = uint64(1) << (bits + 16)
					}
					fmt.Fprintf(&b, "%08x%010x%010x%012x%012x%012x\n", m.N, m.X, m.Y, m.XX, m.YY, m.XY)
					want = append(want, correlationResult{Invalid: 1})
				}
				run(t, 0, bits, b.String(), len(want), want)
			})
		}
	})
	t.Run("full-width-moments-and-invalid-input", func(t *testing.T) {
		base := append([]windowMoments(nil), moments...)
		base = append(base, windowMoments{3, 382, 383, 81154, 81409, 16256})
		for _, m := range base {
			k := uint64(0xffffffff) / m.N
			moments = append(moments, windowMoments{m.N * k, m.X * k, m.Y * k, m.XX * k, m.YY * k, m.XY * k})
		}
		moments = append(moments, windowMoments{}, windowMoments{N: 4, X: 4}, windowMoments{N: 4, XX: 1, YY: 1, XY: 2})
		var b strings.Builder
		var want []correlationResult
		for _, m := range moments {
			fmt.Fprintf(&b, "%08x%010x%010x%012x%012x%012x\n", m.N, m.X, m.Y, m.XX, m.YY, m.XY)
			want = append(want, exactCorrelation(m))
		}
		run(t, 0, 32, b.String(), len(moments), want)
		t.Logf("%d full-width moment cases; maximum count reaches 2^32-1", len(moments))
	})
}
