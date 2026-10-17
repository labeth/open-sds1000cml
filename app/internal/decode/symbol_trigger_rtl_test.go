// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math/rand"
	"open-sds/app/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// symbolRTL runs tb_symbol_trigger on per-clock lines with configuration c
// and returns the clocks after which match was high.
// TRLC-LINKS: REQ-SDS-013
func symbolRTL(t *testing.T, image, dir string, c SymbolCfg, a, b []bool, resets int) []int {
	t.Helper()
	var in strings.Builder
	for i := range a {
		x, y := '0', '0'
		if a[i] {
			x = '1'
		}
		if b[i] {
			y = '1'
		}
		in.WriteRune(x)
		in.WriteRune(y)
		in.WriteByte('\n')
	}
	var tb strings.Builder
	for _, v := range c.Table {
		fmt.Fprintf(&tb, "%02x\n", v)
	}
	inPath, tblPath := filepath.Join(dir, "lines.mem"), filepath.Join(dir, "table.mem")
	if err := os.WriteFile(inPath, []byte(in.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tblPath, []byte(tb.String()), 0600); err != nil {
		t.Fatal(err)
	}
	bi := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	out, err := exec.Command("vvp", "-N", image, "+input="+inPath, "+table="+tblPath, fmt.Sprintf("+length=%d", len(a)), fmt.Sprintf("+resets=%d", resets),
		fmt.Sprintf("+inv_a=%d", bi(c.InvA)), fmt.Sprintf("+inv_b=%d", bi(c.InvB)), fmt.Sprintf("+and_ab=%d", bi(c.AndAB)), fmt.Sprintf("+clk_fall=%d", bi(c.ClockFall)),
		fmt.Sprintf("+mode=%d", c.Mode), fmt.Sprintf("+lead_len=%d", c.LeadLen), fmt.Sprintf("+prescale=%d", c.Prescale), fmt.Sprintf("+unit_q8=%d", c.UnitQ8),
		fmt.Sprintf("+frame_units=%d", c.FrameUnits), fmt.Sprintf("+period=%d", c.Period), fmt.Sprintf("+phase0=%d", c.Phase0), fmt.Sprintf("+k=%d", c.K),
		fmt.Sprintf("+pattern=%x", c.Pattern), fmt.Sprintf("+mask=%x", c.Mask)).CombinedOutput()
	if err != nil {
		t.Fatalf("simulation %v\n%s", err, out)
	}
	var got []int
	done := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var v int
		switch {
		case strings.HasPrefix(line, "M "):
			fmt.Sscanf(line, "M %d", &v)
			got = append(got, v)
		case strings.HasPrefix(line, "F "):
			done = true
		case strings.Contains(line, "$finish") || line == "":
		default:
			t.Fatalf("unexpected simulator output %q", line)
		}
	}
	if !done {
		t.Fatalf("simulation incomplete\n%s", out)
	}
	return got
}

// symbolRTLImage compiles the testbench once.
// TRLC-LINKS: REQ-SDS-013
func symbolRTLImage(t *testing.T) (string, string) {
	t.Helper()
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	dir := t.TempDir()
	rtl := testenv.RTLDir(t)
	image := filepath.Join(dir, "symbol.vvp")
	args := []string{"-g2012", "-s", "tb_symbol_trigger", "-o", image, filepath.Join(rtl, "symbol_trigger.v"), filepath.Join(rtl, "sim", "tb_symbol_trigger.v")}
	if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile %v\n%s", err, out)
	}
	return image, dir
}

// symbolRuns appends a line of runs (alternating levels from first) to dst.
// TRLC-LINKS: REQ-SDS-013
func symbolRuns(dst []bool, first bool, runs ...int) []bool {
	l := first
	for _, w := range runs {
		for i := 0; i < w; i++ {
			dst = append(dst, l)
		}
		l = !l
	}
	return dst
}

// The block's RTL against its Go model, clock for clock: every mode on
// random tables, configurations and run sequences (short glitches, unit-like
// runs, long idles), plus a structured pulse-width frame whose matches are
// counted.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestSymbolTriggerRTL(t *testing.T) {
	image, dir := symbolRTLImage(t)
	rng := rand.New(rand.NewSource(7))
	matched := 0
	defer func() {
		t.Logf("random trials: %d matches", matched)
		if matched < 100 {
			t.Errorf("random trials matched %d times: too few to compare", matched)
		}
	}()
	for trial := 0; trial < 24; trial++ {
		t.Run(fmt.Sprintf("random%d", trial), func(t *testing.T) {
			var c SymbolCfg
			c.Mode = trial % 4
			c.InvA, c.InvB = rng.Intn(2) == 0, rng.Intn(2) == 0
			c.AndAB, c.ClockFall = rng.Intn(4) == 0, rng.Intn(2) == 0
			c.LeadLen = rng.Intn(5)
			if trial%3 != 0 {
				c.LeadLen = rng.Intn(3)
			}
			c.Prescale = rng.Intn(3)
			c.UnitQ8 = uint32(2048 + rng.Intn(40*256))
			if rng.Intn(2) == 0 {
				c.FrameUnits = 1 + rng.Intn(12)
			}
			if rng.Intn(2) == 0 {
				c.Period = 1 + rng.Intn(11)
				c.Phase0 = rng.Intn(c.Period)
			}
			c.K = rng.Intn(20)
			c.Pattern = rng.Uint64()
			c.Mask = rng.Uint64() & (1<<uint(rng.Intn(7)) - 1)
			// Tables of a few windows per level, as protocols use them.
			for lvl := 0; lvl < 2; lvl++ {
				lo := 0
				for lo < 1024 {
					w := 1 + rng.Intn(200)
					code := uint8(rng.Intn(4))<<4 | uint8(1+rng.Intn(4))
					if rng.Intn(3) == 0 {
						code = uint8(rng.Intn(3))<<4 | uint8(rng.Intn(4))
					}
					for i := lo; i < lo+w && i < 1024; i++ {
						c.Table[lvl<<10|i] = code
					}
					lo += w
				}
			}
			var a, b []bool
			la, lb := true, true
			for len(a) < 40000 {
				var w int
				switch rng.Intn(6) {
				case 0:
					w = 1 + rng.Intn(4)
				case 1, 2:
					w = 5 + rng.Intn(40)
				case 3, 4:
					w = 40 + rng.Intn(400)
				default:
					w = 400 + rng.Intn(4000)
				}
				for i := 0; i < w; i++ {
					a = append(a, la)
					b = append(b, lb)
					if rng.Intn(30) == 0 {
						lb = !lb
					}
				}
				la = !la
			}
			want := SymbolMatches(c, a, b, 8)
			got := symbolRTL(t, image, dir, c, a, b, 8)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("config %+v\nRTL %v\nmodel %v", c, symHead(got), symHead(want))
			}
			matched += len(want)
		})
	}
	t.Run("pulse frames", func(t *testing.T) {
		// A two-step leader (100 high, 50 low clocks), then bits as high
		// pulses (10 = 0, 20 = 1) separated by 10-clock lows; 8 bits,
		// MSB first; the trigger asks for 0xA5 at bit 8.
		var c SymbolCfg
		c.Mode, c.LeadLen, c.K, c.Pattern, c.Mask = SymbolClass, 2, 8, 0xa5, 0xff
		set := func(level bool, lo, hi int, code uint8) {
			for n := lo; n <= hi; n++ {
				c.Table[SymbolIndex(level, uint32(n))] = code
			}
		}
		set(true, 90, 110, SymLeadStep<<4|SymDataRestart)
		set(false, 45, 55, (SymLeadStep+1)<<4|SymDataEnd)
		set(false, 7, 12, SymLeadSep<<4|SymDataSep)
		set(true, 7, 12, SymDataBit0)
		set(true, 17, 23, SymDataBit1)
		frame := func(dst []bool, v int) []bool {
			runs := []int{100, 50}
			for i := 7; i >= 0; i-- {
				w := 10
				if v>>i&1 != 0 {
					w = 20
				}
				runs = append(runs, w, 10)
			}
			runs[len(runs)-1] += 300
			return symbolRuns(dst, true, runs...)
		}
		a := symbolRuns(nil, false, 200)
		for _, v := range []int{0xa5, 0x5a, 0xa5, 0xa4, 0xa5} {
			a = frame(a, v)
		}
		b := make([]bool, len(a))
		want := SymbolMatches(c, a, b, 8)
		if len(want) != 3 {
			s := NewSymbolTrigger(c)
			for i := range a {
				s.Step(a[i], b[i], i < 8)
				if s.v4 || s.hitA {
					t.Logf("%d v4 %v q4 %02x in %v got %d n %d sh %x hitA %v", i, s.v4, s.q4, s.inFrame, s.got, s.n, s.sh, s.hitA)
				}
			}
			t.Fatalf("model matches %v, want three frames", want)
		}
		got := symbolRTL(t, image, dir, c, a, b, 8)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("RTL %v model %v", got, want)
		}
	})
}

// TRLC-LINKS: REQ-SDS-013
func symHead(v []int) []int {
	if len(v) > 40 {
		return v[:40]
	}
	return v
}
