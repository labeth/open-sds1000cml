// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"fmt"
	"open-sds/app/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// The stream and bits images' top levels stream the paced counter ramp
// through the drum (tb_top_stream_drum.v), drained over GPMC as DrainStream
// does: every word in order, no fault. The bits image also runs in ring mode
// (the drum on chip beside the record's ring), where the record owns the
// transport and its traffic is not the drum's.
// TRLC-LINKS: REQ-SDS-035
func TestStreamDrumTopRTL(t *testing.T) {
	if testing.Short() {
		t.Skip("full top-level simulation")
	}
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	for _, c := range []struct {
		image             string
		mode, clocks, run int
	}{{"stream", 3, 125, 12000}, {"bits", 3, 125, 12000}, {"bits", 35, 250, 6000}} {
		t.Run(fmt.Sprintf("%s_mode%d_%dclocks", c.image, c.mode, c.clocks), func(t *testing.T) {
			dir := t.TempDir()
			rtl := testenv.RTLDir(t)
			d := "`define STREAM_CAPTURE\n`define STREAM_DRUM\n`define INTERLEAVE\n`define BURST_RECALL\n" +
				"`define PRECISION\n`define BOX_DECIM\n`define HOST_READ_FIX\n`define BENCH_MHZ 250\n"
			files := []string{"stream_peak.v"}
			if c.image == "bits" {
				d += "`define STREAM_BITS\n`define STREAM_RING\n"
				files = []string{"stream_bits.v", "ring_stamp.v"}
			}
			d += fmt.Sprintf("`define MODE_P %d\n`define RAMP_CLOCKS_P %d\n`define WORDS_P %d\n", c.mode, c.clocks, c.run)
			defs := filepath.Join(dir, "defines.v")
			if err := os.WriteFile(defs, []byte(d), 0o600); err != nil {
				t.Fatal(err)
			}
			image := filepath.Join(dir, "drum.vvp")
			args := []string{"-g2012", "-s", "tb_top_stream_drum", "-o", image, "-I", rtl, defs}
			for _, f := range append([]string{
				"top.v", "record.v", "transport.v", "adc_unpack.v", "gpmc_slave.v", "ddio_pair.v", "lane_in.v",
				"sim/ddr_model.v", "sim/tb_top_stream_drum.v", "panel_scan.v", "box_decim.v", "stream_packetizer.v", "stream_banks.v",
				"stream_drum.v", "stream_marks.v",
			}, files...) {
				args = append(args, filepath.Join(rtl, f))
			}
			if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := exec.Command("vvp", "-N", image).CombinedOutput()
			if err != nil || !regexp.MustCompile(`(?m)^PASS tb_top_stream_drum`).Match(out) {
				t.Fatalf("simulation: %v\n%s", err, out)
			}
		})
	}
}
