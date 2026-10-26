// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	image := compileQualifyTop(t, dir, "qualify", qualifyImageDefines)
	// decimated records (the box averages stand in for the vendor-FIFO
	// precision path, as in TestQualifierDecimatedTopRTL)
	decimImage := compileQualifyTop(t, dir, "qualify-decim", qualifyImageDefines+"`define PRECISION\n`define BOX_DECIM\n", "box_decim.v")
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
		t.Run(r.name, func(t *testing.T) { runSymbolTop(t, dir, r.image, r.name, tc.p, tc.wave(), r.log, r.pre) })
	}
}

// compileQualifyTop builds tb_top_qualify around the qualify image's top
// level with defines (and the extra sources they need).
// TRLC-LINKS: REQ-SDS-013
func compileQualifyTop(t *testing.T, dir, name, defines string, extra ...string) string {
	t.Helper()
	rtl := testenv.RTLDir(t)
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

// runSymbolTop runs the qualify image's top level (image) with the symbol
// trigger planned for p on wave w (C1 line A, C2 line B), the record
// decimated 2^log (0: the full rate) with pre's history: the block's matches
// against the Go model's, the fire on the first, the record taking it within
// a decimated word, and the host's match count. extra adds the bench's
// switches and register 18's bits above the decimation; it returns the
// bench's output.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func runSymbolTop(t *testing.T, dir, image, name string, p SerialParams, w *symWave, log int, pre string, extra ...string) string {
	t.Helper()
	name = strings.NewReplacer("/", "-", " ", "-").Replace(name)
	c := planHardwareSymbol(&p)
	if !c.Enabled {
		t.Fatal("unplannable")
	}
	want := symMatches(t, p, w)
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
	words := filepath.Join(dir, name+"-words.mem")
	if err := os.WriteFile(words, []byte(in.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	reg18 := uint16(log)
	for _, x := range extra {
		if x == "+pickalt" && log != 0 {
			reg18 |= 1 << 5 // picked samples instead of the filter
		}
	}
	regs := [][2]uint16{{7, 128 | 4<<8}, {59, 128}, {18, reg18}, {99, 0}}
	for _, v := range c.Table {
		regs = append(regs, [2]uint16{100, uint16(v)})
	}
	regs = append(regs, [2]uint16{98, 1})
	regs = append(regs, sramcapture.SymbolRegisters(c)...)
	var rb strings.Builder
	for _, r := range regs {
		fmt.Fprintf(&rb, "%x\n%x\n", r[0], r[1])
	}
	rp := filepath.Join(dir, name+"-regs.mem")
	if err := os.WriteFile(rp, []byte(rb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-N", image, "+input=" + words, fmt.Sprintf("+length=%d", 2*len(w.a)), "+regs=" + rp, fmt.Sprintf("+nregs=%d", len(regs)),
		pre, "+post=2000"}, extra...)
	out, err := exec.Command("vvp", args...).CombinedOutput()
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
	if d := recorded - trigger; d < 0 || d > 20+1<<log {
		t.Fatalf("record's trigger at word %d, the block fired at %d", recorded, trigger)
	}
	if count != len(ms) {
		t.Fatalf("host count %d, matches %d", count, len(ms))
	}
	t.Logf("matches %v (model clocks %v), trigger word %d, record's %d", ms, want, trigger, recorded)
	return string(out)
}

// symLeadMax and symTailAfter bound a wave to the bench's 4M words (two a
// receiver clock): the idle before the first frame at most 8 ms - the bench
// streams the wave while it still arms the record over the host bus, and a
// 2 ms lead let the first frame pass unarmed - and 20000 clocks after the
// model's first match (the bench then holds the last word, the line's idle).
const (
	symLeadMax   = 1000000
	symTailAfter = 20000
	// symLeadMin pads a short lead (MDIO's first frame 32 us in) to 0.4 ms:
	// a record decimated /256 is still arming there
	symLeadMin = 50000
)

// trimSymWave is w with its idle lead cut to symLeadMax clocks (or padded to
// symLeadMin) and its end to
// symTailAfter after the model's first match for p (a Wiegand reader's 248 ms
// wave over the bench's words); nil when the model finds no match on it.
// TRLC-LINKS: REQ-SDS-013
func trimSymWave(t *testing.T, p SerialParams, w *symWave) *symWave {
	t.Helper()
	lead := 0
	for lead < len(w.a) && w.a[lead] == w.a[0] && w.b[lead] == w.b[0] {
		lead++
	}
	from := max(0, lead-symLeadMax)
	out := &symWave{a: w.a[from:], b: w.b[from:]}
	if pad := symLeadMin - lead; pad > 0 { // the lead's own level, longer
		a, b := make([]bool, pad, pad+len(out.a)), make([]bool, pad, pad+len(out.b))
		for i := range a {
			a[i], b[i] = w.a[0], w.b[0]
		}
		out.a, out.b = append(a, out.a...), append(b, out.b...)
	}
	m := symMatches(t, p, out)
	if len(m) == 0 {
		return nil
	}
	if end := m[0] + symTailAfter; end < len(out.a) {
		out.a, out.b = out.a[:end], out.b[:end]
	}
	return out
}

// Every two-line symbol bus (PS/2, SWD, MDIO, Wiegand, the quadrature
// encoder, stepper, SD card, PDI, cJTAG, Maple and the caliper) on a record
// decimated /16: the block matches where the Go model does and the record,
// written a word in eight, takes its fire within a decimated word (a fire
// taken only on a written word was lost there).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-040
func TestSymbolTwoLineDecimatedTopRTL(t *testing.T) {
	if testing.Short() {
		t.Skip("full top-level simulation")
	}
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	dir := t.TempDir()
	image := compileQualifyTop(t, dir, "qualify-decim", qualifyImageDefines+"`define PRECISION\n`define BOX_DECIM\n", "box_decim.v")
	two := []string{"PS/2", "SWD", "MDIO", "Wiegand", "Quadrature", "Stepper", "SD card", "PDI", "cJTAG", "Maple", "Caliper"}
	// the slow buses' waves take some six minutes each: with SDS_LONG_RTL=1
	slow := []string{"Wiegand", "Caliper"}
	seen := 0
	for _, tc := range symbolCases() {
		if !slices.Contains(two, tc.name) {
			continue
		}
		seen++
		t.Run(tc.name, func(t *testing.T) {
			if slices.Contains(slow, tc.name) && os.Getenv("SDS_LONG_RTL") == "" {
				t.Skip("a slow bus's wave: SDS_LONG_RTL=1")
			}
			p := tc.p
			for _, r := range tc.reqs { // the first request that matches a frame
				if r.want > 0 {
					p.Pattern = r.pattern
					break
				}
			}
			w := trimSymWave(t, p, tc.wave())
			if w == nil {
				t.Fatal("the model finds no match on the trimmed wave")
			}
			runSymbolTop(t, dir, image, tc.name+"-decimated-16", p, w, 4, "+pre=2")
		})
	}
	if seen != len(two) {
		t.Fatalf("%d of the %d two-line buses in symbolCases", seen, len(two))
	}
}

// precisionStub stands in for the vendor-FIFO precision filter in the
// qualify image's simulation: a word that no picked sample reads (fraction
// 0x34), one per decimated sample's time.
const precisionStub = "`timescale 1ns/1ps\n" +
	"module adc_precision #(parameter SHARED_TAIL=0)(input core,packclk,clk100,enable,input [4:0] decim_request,input [31:0] raw,input raw_valid,\n" +
	" output [31:0] data,output reg valid=0,output fault);\n" +
	" assign data=32'h12341234;assign fault=1'b0;\n" +
	" reg [3:0] n=0;\n" +
	" always @(posedge core)begin valid<=0;if(!enable)n<=0;else if(raw_valid)begin n<=n+1'b1;valid<=n==0;end end\n" +
	"endmodule\n"

// The qualify image with the precision filter and, on asking (register 18
// bit 5, register 30 reading 0x5041), every 2^n-th sample (PICK_ALT): a
// clocked two-wire bus (SWD) on a record decimated /16 takes the filter's
// words, or picked ones - the line's codes, the fraction 0 - and the
// trigger either way.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-040
func TestQualifyPickAltTopRTL(t *testing.T) {
	if testing.Short() {
		t.Skip("full top-level simulation")
	}
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "precision_stub.v")
	if err := os.WriteFile(stub, []byte(precisionStub), 0o600); err != nil {
		t.Fatal(err)
	}
	rtl := testenv.RTLDir(t)
	rel, err := filepath.Rel(rtl, stub)
	if err != nil {
		t.Fatal(err)
	}
	image := compileQualifyTop(t, dir, "qualify-pickalt", qualifyImageDefines+"`define PRECISION\n`define PICK_ALT\n", "pick_decim.v", rel)
	var tc symCase
	for _, c := range symbolCases() {
		if c.name == "SWD" {
			tc = c
		}
	}
	p := tc.p
	p.Pattern = tc.reqs[0].pattern
	for _, pick := range []bool{false, true} {
		extra := []string{"+pickalt", "+words"}
		name := "pick"
		if !pick {
			extra, name = []string{"+words"}, "filter"
		}
		t.Run(name, func(t *testing.T) {
			out := runSymbolTop(t, dir, image, "SWD-"+name, p, tc.wave(), 4, "+pre=2", extra...)
			words := 0
			for _, line := range strings.Split(out, "\n") {
				var n int
				var v uint32
				if _, err := fmt.Sscanf(line, "P %d %x", &n, &v); err != nil {
					continue
				}
				words++
				c1, c2 := v>>8&0xff, v>>24&0xff
				switch {
				case !pick && v != 0x12341234:
					t.Fatalf("word %d: %08x, not the filter's", words, v)
				case pick && (v&0x00ff00ff != 0 || (c1 != 200 && c1 != 50) || (c2 != 200 && c2 != 50)):
					t.Fatalf("word %d: %08x, not a picked sample of the lines", words, v)
				}
			}
			if words < 100 {
				t.Fatalf("%d decimated words", words)
			}
		})
	}
}
