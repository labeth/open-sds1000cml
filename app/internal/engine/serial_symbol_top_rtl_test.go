// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
	"open-sds/app/internal/testenv"
)

// The qualify image's defines for the top-level simulation (the vendor-FIFO
// precision path left out, as the general image's simulation does).
const qualifyImageDefines = "`define QUAL_TRIGGER\n`define SYMBOL_TRIGGER\n`define INTERLEAVE\n`define BURST_RECALL\n`define HOST_READ_FIX\n`define BENCH_MHZ 250\n"

// The qualify image's top level with the symbol trigger, planned by the
// engine for a pulse-width bus (WS281x), UART characters after a break
// (DMX512) and a clocked two-wire bus (SWD) on both channels: registers
// over GPMC, the table, the block's matches against the Go model's, the
// host's match count and the record's trigger on the first match.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestSymbolTopRTL(t *testing.T) {
	if testing.Short() {
		t.Skip("full top-level simulation")
	}
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	dir := t.TempDir()
	rtl := testenv.RTLDir(t)
	compile := func(name, defines string, extra ...string) string {
		defs := filepath.Join(dir, name+"-defines.v")
		if err := os.WriteFile(defs, []byte(defines), 0o600); err != nil {
			t.Fatal(err)
		}
		image := filepath.Join(dir, name+".vvp")
		args := []string{"-g2012", "-s", "tb_top_qualify", "-o", image, "-I", rtl, defs}
		for _, f := range append([]string{"top.v", "record.v", "transport.v", "adc_unpack.v", "sample_timeline.v", "decoded_event_queue.v",
			"decoded_event_bridge.v", "decoded_event_reader.v", "decoded_event_transport.v", "decoded_event_port.v",
			"gpmc_slave.v", "ddio_pair.v", "lane_in.v", "sim/ddr_model.v", "sim/tb_top_qualify.v", "event_sequence.v", "envelope_reduce.v", "panel_scan.v",
			"qual_trigger.v", "zone_qual.v", "sum_edge_pair.v", "symbol_trigger.v"}, extra...) {
			args = append(args, filepath.Join(rtl, f))
		}
		if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		return image
	}
	image := compile("qualify", qualifyImageDefines)
	// decimated records (the box averages stand in for the vendor-FIFO
	// precision path, as in TestQualifierDecimatedTopRTL)
	decimImage := compile("qualify-decim", qualifyImageDefines+"`define PRECISION\n`define BOX_DECIM\n", "box_decim.v")
	us := 1e-6
	cases := []struct {
		name string
		p    SerialParams
		wave func() *symWave
	}{
		{"WS281x", func() SerialParams {
			p := symParams(serWS281x, 800000)
			p.Pattern = []PatternElem{symStart, symData(0x11, 0xff)}
			return p
		}(), func() *symWave {
			w := &symWave{}
			w.hold(false, true, 20*us)
			for f := 0; f < 3; f++ {
				for _, v := range []int{0x10 + f, 0x80, 0x5a} {
					for i := 7; i >= 0; i-- {
						if v>>i&1 != 0 {
							w.runs(true, 0.8*us, 0.45*us)
						} else {
							w.runs(true, 0.4*us, 0.85*us)
						}
					}
				}
				w.hold(false, true, 60*us)
			}
			return w
		}},
		{"DMX", func() SerialParams {
			p := symParams(serDMX, 250000)
			p.Pattern = []PatternElem{symStart, symData(0, 0xff)}
			return p
		}(), func() *symWave {
			w := &symWave{}
			w.hold(true, true, 20*us)
			for f := 0; f < 3; f++ {
				w.hold(false, true, 100*us)
				w.hold(true, true, 12*us)
				for s, v := range []int{f % 2, 0x21, 0x43} {
					w.uart(v, 8, "none", 2, 4*us)
					if s == 1 {
						w.hold(true, true, 9*us)
					}
				}
				w.hold(true, true, 30*us)
			}
			return w
		}},
		{"SWD", func() SerialParams {
			p := symParams(serSWD, 5000000)
			p.Pattern = []PatternElem{symStart, symData(0xa5, 0xff)}
			return p
		}(), func() *symWave {
			w := &symWave{}
			T := 0.2 * us
			bits := func(v, n int) {
				for i := 0; i < n; i++ {
					b := v>>i&1 != 0
					w.hold(false, b, T/2)
					w.hold(true, b, T/2)
				}
			}
			bits(0, 8)
			for _, req := range []int{0x8d, 0xa5, 0x8d, 0xa5} {
				bits(req, 8)
				bits(0, 1)
				bits(1, 3)
				bits(0xffff, 32)
				bits(0, 1)
				bits(0, 9)
			}
			return w
		}},
	}
	// each bus on full-rate records, and the clocked two-wire bus (SWD) on
	// records decimated /16 and /256 (2 words of history: 512 raw words at
	// /256, before its first match), which take a trigger only with a word
	// written - one clock in many
	type run struct {
		name, image string
		log         int
		pre         string
		tc          int
	}
	var runs []run
	for i, tc := range cases {
		runs = append(runs, run{tc.name, image, 0, "+pre=64", i})
		if tc.name == "SWD" {
			for _, log := range []int{4, 8} {
				runs = append(runs, run{fmt.Sprintf("SWD-decimated-%d", 1<<log), decimImage, log, "+pre=2", i})
			}
		}
	}
	for _, r := range runs {
		tc := cases[r.tc]
		t.Run(r.name, func(t *testing.T) {
			w := tc.wave()
			c := planHardwareSymbol(&tc.p)
			if !c.Enabled {
				t.Fatal("unplannable")
			}
			want := symMatches(t, tc.p, w)
			if len(want) == 0 {
				t.Fatal("the model finds no match")
			}
			// two acquisition words a receiver clock; C1 line A, C2 line B
			code := func(v bool) uint32 {
				if v {
					return 200
				}
				return 50
			}
			var in strings.Builder
			for i := range w.a {
				c1, c2 := code(w.a[i]), code(w.b[i])
				word := c2<<24 | c1<<16 | c2<<8 | c1
				fmt.Fprintf(&in, "%08x\n%08x\n", word, word)
			}
			words := filepath.Join(dir, r.name+"-words.mem")
			if err := os.WriteFile(words, []byte(in.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			regs := [][2]uint16{{7, 128 | 4<<8}, {59, 128}, {18, uint16(r.log)}, {99, 0}}
			for _, v := range c.Table {
				regs = append(regs, [2]uint16{100, uint16(v)})
			}
			regs = append(regs, [2]uint16{98, 1})
			regs = append(regs, sramcapture.SymbolRegisters(c)...)
			var rb strings.Builder
			for _, r := range regs {
				fmt.Fprintf(&rb, "%x\n%x\n", r[0], r[1])
			}
			rp := filepath.Join(dir, r.name+"-regs.mem")
			if err := os.WriteFile(rp, []byte(rb.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("vvp", "-N", r.image, "+input="+words, fmt.Sprintf("+length=%d", 2*len(w.a)), "+regs="+rp, fmt.Sprintf("+nregs=%d", len(regs)),
				r.pre, "+post=2000").CombinedOutput()
			if err != nil || !strings.Contains(string(out), "PASS qualify top") {
				t.Fatalf("simulate: %v\n%s", err, out)
			}
			var ms []int
			trigger, recorded, count := -1, -1, -1
			for _, line := range strings.Split(string(out), "\n") {
				var n int
				if _, err := fmt.Sscanf(line, "M %d", &n); err == nil {
					ms = append(ms, n)
				}
				fmt.Sscanf(line, "T %d", &trigger)
				fmt.Sscanf(line, "R %d", &recorded)
				fmt.Sscanf(line, "C %d", &count)
			}
			// The model's matches are receiver clocks of the wave (two words
			// each); the image's come a fixed pipeline later in words.
			if len(ms) == 0 || trigger < 0 {
				t.Fatalf("no match or trigger\n%s", out)
			}
			for i, m := range ms {
				if i >= len(want) {
					t.Fatalf("image matches %v, model %v", ms, want)
				}
				d := m - 2*want[i]
				if d < 0 || d > 2*decode.SymbolLatency+40 {
					t.Fatalf("match %d at word %d, model clock %d (word %d)", i, m, want[i], 2*want[i])
				}
			}
			if d := trigger - ms[0]; d < 0 || d > 20 {
				t.Fatalf("trigger at word %d, first match at %d", trigger, ms[0])
			}
			// the record takes it within a decimated word (2^(log-1) raw words)
			if d := recorded - trigger; d < 0 || d > 20+1<<r.log {
				t.Fatalf("record's trigger at word %d, the block fired at %d", recorded, trigger)
			}
			if count != len(ms) {
				t.Fatalf("host count %d, matches %d", count, len(ms))
			}
			t.Logf("matches %v (model clocks %v), trigger word %d", ms, want, trigger)
		})
	}
}
