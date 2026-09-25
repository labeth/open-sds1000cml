// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type windowMoments struct{ N, X, Y, XX, YY, XY uint64 }

// Exercise integer moment accumulation against direct sample sums and the
// centered floating-point correlation used by gateFind. Peak decisions and
// complete FPGA matching/stacking are outside this component's scope.
// TRLC-LINKS: REQ-SDS-141
func TestStackMomentsRTL(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required RTL tool %s: %v", tool, err)
		}
	}
	const valid = uint32(1 << 16)
	const last = uint32(1 << 17)
	const start = uint32(1 << 18)
	const reset = uint32(1 << 19)
	dir := t.TempDir()
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	run := func(t *testing.T, width int, commands []uint32, want []windowMoments, wantOverflow int) {
		t.Helper()
		image := filepath.Join(dir, fmt.Sprintf("moments-%d.vvp", width))
		input := filepath.Join(dir, "commands.mem")
		args := []string{"-g2012", "-s", "tb_stack_moments", fmt.Sprintf("-Ptb_stack_moments.COUNT_BITS=%d", width), "-o", image,
			filepath.Join(rtl, "stack_moments.v"), filepath.Join(rtl, "sim", "tb_stack_moments.v")}
		if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		var b strings.Builder
		for _, v := range commands {
			fmt.Fprintf(&b, "%05x\n", v)
		}
		if err := os.WriteFile(input, []byte(b.String()), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("vvp", image, "+input="+input, fmt.Sprintf("+length=%d", len(commands))).CombinedOutput()
		if err != nil {
			t.Fatalf("simulate: %v\n%s", err, out)
		}
		var got []windowMoments
		overflows := 0
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "O" {
				overflows++
				continue
			}
			if line == "" {
				continue
			}
			var m windowMoments
			if _, err := fmt.Sscanf(line, "D %d %d %d %d %d %d", &m.N, &m.X, &m.Y, &m.XX, &m.YY, &m.XY); err != nil {
				t.Fatalf("unexpected %q: %v", line, err)
			}
			got = append(got, m)
		}
		if !reflect.DeepEqual(got, want) || overflows != wantOverflow {
			t.Fatalf("results got=%+v want=%+v; overflow=%d want=%d", got, want, overflows, wantOverflow)
		}
	}
	t.Run("exact-moments-and-reference-correlation", func(t *testing.T) {
		var commands []uint32
		var want []windowMoments
		rng := rand.New(rand.NewSource(429141))
		for _, length := range []int{1, 4, 9, 24, 127, 256, 1024, 4096, 65535} {
			for _, family := range []string{"random", "same", "inverse", "flat", "small-ripple"} {
				if length == 65535 && family != "random" {
					continue
				}
				commands = append(commands, start)
				x := make([]float32, length)
				y := make([]uint8, length)
				var m windowMoments
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
					} // ignored values while stalled
				}
				commands = append(commands, 0, 0, 0)
				want = append(want, m)
				if length < 4 {
					continue
				}
				tpl := gateTemplate(x, 0, length)
				if tpl == nil {
					continue
				}
				mean := float64(m.Y) / float64(m.N)
				dot, energy := 0.0, 0.0
				for i := range y {
					v := float64(y[i]) - mean
					dot += tpl.data[i] * v
					energy += v * v
				}
				if energy == 0 {
					continue
				}
				ref := dot / (tpl.norm * math.Sqrt(energy))
				// These fixture lengths keep the exact integer products below
				// int64. The final hardware normalizer needs wider products for
				// the full 32-bit count range; it is not implemented by this test.
				numerator := int64(m.N*m.XY) - int64(m.X*m.Y)
				xEnergy := m.N*m.XX - m.X*m.X
				yEnergy := m.N*m.YY - m.Y*m.Y
				score := float64(numerator) / math.Sqrt(float64(xEnergy)*float64(yEnergy))
				if math.Abs(score-ref) > 2e-12 {
					t.Fatalf("N=%d %s moment correlation %.17g reference %.17g", length, family, score, ref)
				}
			}
		}
		run(t, 32, commands, want, 0)
		t.Logf("%d windows matched exactly, including a 65535-pair window", len(want))
	})
	t.Run("abort-reset-overflow-recovery", func(t *testing.T) {
		commands := []uint32{valid | last | 0xffff, start, valid | 0xffff, valid | 0xffff, start, valid | last | 0xffff, reset, 0, 0}
		// Reset cancels a pending last pair; restart cancels a partial window.
		// Sixteen accepted pairs exceed the deliberately reduced four-bit count.
		commands = append(commands, start)
		for i := 0; i < 16; i++ {
			v := valid | 0xffff
			if i == 15 {
				v |= last
			}
			commands = append(commands, v)
		}
		commands = append(commands, 0, 0, 0, valid|last|0xffff, 0, start)
		for i := 0; i < 15; i++ {
			v := valid | 0xffff
			if i == 14 {
				v |= last
			}
			commands = append(commands, v)
		}
		commands = append(commands, 0, 0, 0, start, valid|last|0x0302, 0, 0, 0)
		run(t, 4, commands, []windowMoments{{15, 3825, 3825, 975375, 975375, 975375}, {1, 2, 3, 4, 9, 6}}, 1)
	})
}
