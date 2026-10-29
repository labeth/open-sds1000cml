// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// The qualify image's top level with a pulse qualifier on full-rate and
// decimated records (/16, /32, /256; the box averages stand in for the
// vendor-FIFO precision path): a UART-like line, 40 us idle before each
// burst of 1 Mbit/s characters, triggered on an idle over 20 us. The
// qualifier fires one clock a few after the closing edge and the record
// takes a trigger only with a word written (one clock in many decimated):
// unheld, no decimated record ever triggered. Each record triggers within a
// few decimated samples of a burst's start.
// TRLC-LINKS: REQ-SDS-011
func TestQualifierDecimatedTopRTL(t *testing.T) {
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
	defs := filepath.Join(dir, "defines.v")
	if err := os.WriteFile(defs, []byte(qualifyImageDefines+"`define PRECISION\n`define BOX_DECIM\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(dir, "qualify-decim.vvp")
	args := []string{"-g2012", "-s", "tb_top_qualify_decim", "-o", image, "-I", rtl, defs}
	for _, f := range []string{"top.v", "record.v", "transport.v", "adc_unpack.v", "sample_timeline.v", "decoded_event_queue.v",
		"decoded_event_bridge.v", "decoded_event_reader.v", "decoded_event_transport.v", "decoded_event_port.v",
		"gpmc_slave.v", "ddio_pair.v", "lane_in.v", "sim/ddr_model.v", "sim/tb_top_qualify_decim.v", "event_sequence.v", "envelope_reduce.v", "panel_scan.v",
		"qual_trigger.v", "zone_qual.v", "sum_edge_pair.v", "symbol_trigger.v", "box_decim.v"} {
		args = append(args, filepath.Join(rtl, f))
	}
	if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	// the line: 2 ns samples, two a word; C2 held mid-scale
	const sampleS, idle, bit = 2e-9, 40e-6, 1e-6
	var line []bool
	hold := func(v bool, s float64) {
		for i := 0; i < int(s/sampleS+0.5); i++ {
			line = append(line, v)
		}
	}
	var starts []int // each burst's start bit, in words
	for f := 0; f < 4; f++ {
		hold(true, idle)
		starts = append(starts, len(line)/2)
		for _, c := range []int{0x48, 0x69, 0x55, 0xAA} {
			hold(false, bit)
			for i := 0; i < 8; i++ {
				hold(c>>i&1 == 1, bit)
			}
			hold(true, bit)
		}
		hold(true, 3*bit)
	}
	var in strings.Builder
	code := func(v bool) uint32 {
		if v {
			return 200
		}
		return 50
	}
	for i := 0; i+1 < len(line); i += 2 {
		fmt.Fprintf(&in, "%08x\n", 128<<24|code(line[i+1])<<16|128<<8|code(line[i]))
	}
	words := filepath.Join(dir, "words.mem")
	if err := os.WriteFile(words, []byte(in.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	// a high pulse over 20 us (at least 10), as AUTO's UART frame start asks
	prescale, minTicks, maxTicks := qualTicks(10e3, 20e3)
	for _, log := range []int{0, 4, 5, 8} {
		regs := [][2]uint32{{7, 125 | 4<<8}, {18, uint32(log)}, {22, minTicks - 1}, {24, maxTicks}, {26, 0x8080}, {27, 0},
			{28, prescale - 1}, {21, 1 | 2<<4}}
		var rb strings.Builder
		for _, r := range regs {
			fmt.Fprintf(&rb, "%x\n%x\n", r[0], r[1])
		}
		rp := filepath.Join(dir, fmt.Sprintf("regs%d.mem", log))
		if err := os.WriteFile(rp, []byte(rb.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("vvp", "-N", image, "+input="+words, fmt.Sprintf("+length=%d", len(line)/2), "+regs="+rp,
			fmt.Sprintf("+nregs=%d", len(regs)), "+pre=64", "+post=2000").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "PASS qualify decimated") {
			t.Fatalf("/%d: %v\n%s", 1<<log, err, out)
		}
		trigger := -1
		for _, l := range strings.Split(string(out), "\n") {
			fmt.Sscanf(l, "T %d", &trigger)
		}
		// within a few decimated samples (two a word at the full rate) of a start
		near := false
		for _, s := range starts {
			if d := trigger - s; d >= 0 && d <= 16+3*(1<<log)/2 {
				near = true
			}
		}
		if !near {
			t.Fatalf("/%d: trigger at word %d, bursts start at %v", 1<<log, trigger, starts)
		}
		t.Logf("/%d: trigger at word %d (bursts at %v)", 1<<log, trigger, starts)
	}
}

// The edge trigger on decimated records (/16, /32, /256: box averages) of a
// signal faster than one decimated sample: a line low for 40 us, then a
// 62.5 MHz square for 4 us. The averages of the burst sit at mid-scale,
// under the level; the comparators read the raw samples, so each record
// triggers at a burst's start (ADR-TRIGGER-ANY-TIMEBASE): on the filter's
// words no decimated record ever triggered (a 1 MHz square at 50 ms/div).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-011
func TestEdgeDecimatedTopRTL(t *testing.T) {
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
	defs := filepath.Join(dir, "defines.v")
	if err := os.WriteFile(defs, []byte(qualifyImageDefines+"`define PRECISION\n`define BOX_DECIM\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(dir, "edge-decim.vvp")
	args := []string{"-g2012", "-s", "tb_top_qualify_decim", "-o", image, "-I", rtl, defs}
	for _, f := range []string{"top.v", "record.v", "transport.v", "adc_unpack.v", "sample_timeline.v", "decoded_event_queue.v",
		"decoded_event_bridge.v", "decoded_event_reader.v", "decoded_event_transport.v", "decoded_event_port.v",
		"gpmc_slave.v", "ddio_pair.v", "lane_in.v", "sim/ddr_model.v", "sim/tb_top_qualify_decim.v", "event_sequence.v", "envelope_reduce.v", "panel_scan.v",
		"qual_trigger.v", "zone_qual.v", "sum_edge_pair.v", "symbol_trigger.v", "box_decim.v"} {
		args = append(args, filepath.Join(rtl, f))
	}
	if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	const sampleS = 2e-9
	var line []uint32
	var starts []int // each burst's start, in words
	for b := 0; b < 4; b++ {
		for i := 0; i < int(40e-6/sampleS); i++ {
			line = append(line, 50)
		}
		starts = append(starts, len(line)/2)
		for i := 0; i < int(4e-6/sampleS); i++ {
			line = append(line, map[bool]uint32{true: 200, false: 50}[i/4%2 == 0])
		}
	}
	var in strings.Builder
	for i := 0; i+1 < len(line); i += 2 {
		fmt.Fprintf(&in, "%08x\n", 128<<24|line[i+1]<<16|128<<8|line[i])
	}
	words := filepath.Join(dir, "words.mem")
	if err := os.WriteFile(words, []byte(in.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, log := range []int{0, 4, 5, 8} {
		// C1 rising through 160 (the burst's averages sit at 125); no qualifier
		regs := [][2]uint32{{7, 160 | 4<<8}, {18, uint32(log)}, {21, 0}}
		var rb strings.Builder
		for _, r := range regs {
			fmt.Fprintf(&rb, "%x\n%x\n", r[0], r[1])
		}
		rp := filepath.Join(dir, fmt.Sprintf("regs%d.mem", log))
		if err := os.WriteFile(rp, []byte(rb.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("vvp", "-N", image, "+input="+words, fmt.Sprintf("+length=%d", len(line)/2), "+regs="+rp,
			fmt.Sprintf("+nregs=%d", len(regs)), "+pre=64", "+post=2000").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "PASS qualify decimated") {
			t.Fatalf("/%d: %v\n%s", 1<<log, err, out)
		}
		trigger := -1
		for _, l := range strings.Split(string(out), "\n") {
			fmt.Sscanf(l, "T %d", &trigger)
		}
		near := false
		for _, s := range starts {
			if d := trigger - s; d >= 0 && d <= 16+3*(1<<log)/2 {
				near = true
			}
		}
		if !near {
			t.Fatalf("/%d: trigger at word %d, bursts start at %v", 1<<log, trigger, starts)
		}
		t.Logf("/%d: trigger at word %d (bursts at %v)", 1<<log, trigger, starts)
	}
}
