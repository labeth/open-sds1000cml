// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"open-sds/app/internal/sramcapture"
	"open-sds/app/internal/testenv"
)

// The bits image's top level in ring mode (ADR-STREAM-RING,
// tb_top_bits_ring.v; the box averages stand in for the vendor-FIFO
// precision path): the ring read back through the GPMC as recallFrozen
// reads it holds the signal at the ring's pace in every word, its geometry
// and stamp pass softRingPace, and each edge the digital stream shows maps
// through softRingWord to the ring word the bench placed it in, which was
// made within two ring words of the edge.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func TestStreamRingTopRTL(t *testing.T) {
	if testing.Short() {
		t.Skip("full top-level simulation")
	}
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	for _, c := range []struct{ ring, stream, half, run int }{{8, 9, 3000, 300000}, {9, 12, 30000, 600000}} {
		t.Run(fmt.Sprintf("ring2^%d_stream2^%d", c.ring, c.stream), func(t *testing.T) {
			dir := t.TempDir()
			rtl := testenv.RTLDir(t)
			defs := filepath.Join(dir, "defines.v")
			d := "`define STREAM_CAPTURE\n`define STREAM_DRUM\n`define STREAM_BITS\n`define STREAM_RING\n`define INTERLEAVE\n`define BURST_RECALL\n" +
				"`define PRECISION\n`define BOX_DECIM\n`define HOST_READ_FIX\n`define BENCH_MHZ 250\n" +
				fmt.Sprintf("`define RING_LOG_P %d\n`define STREAM_LOG_P %d\n`define HALF_P %d\n`define RUN_P %d\n", c.ring, c.stream, c.half, c.run)
			if err := os.WriteFile(defs, []byte(d), 0o600); err != nil {
				t.Fatal(err)
			}
			image := filepath.Join(dir, "ring.vvp")
			args := []string{"-g2012", "-s", "tb_top_bits_ring", "-o", image, "-I", rtl, defs}
			for _, f := range []string{"top.v", "record.v", "transport.v", "adc_unpack.v", "gpmc_slave.v", "ddio_pair.v", "lane_in.v",
				"sim/ddr_model.v", "sim/tb_top_bits_ring.v", "panel_scan.v", "box_decim.v", "stream_packetizer.v", "stream_banks.v",
				"stream_bits.v", "stream_drum.v", "stream_marks.v", "ring_stamp.v"} {
				args = append(args, filepath.Join(rtl, f))
			}
			if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := exec.Command("vvp", "-N", image).CombinedOutput()
			if err != nil || !regexp.MustCompile(`(?m)^PASS tb_top_bits_ring`).Match(out) {
				t.Fatalf("simulation: %v\n%s", err, out)
			}
			ring := regexp.MustCompile(`ring: length (\d+) start (\d+) trigger (\d+) origin (\d+) stamp (\d+)`).FindSubmatch(out)
			if ring == nil {
				t.Fatalf("no ring line\n%s", out)
			}
			num := func(b []byte) uint64 { v, _ := strconv.ParseUint(string(b), 10, 64); return v }
			m := sramcapture.Metadata{Length: uint32(num(ring[1])), Start: uint32(num(ring[2])), TriggerIndex: uint32(num(ring[3])), Origin: uint32(num(ring[4]))}
			last := uint32(num(ring[5]))
			if err := softRingPace(m, c.ring, last); err != nil {
				t.Fatalf("pace: %v", err)
			}
			edges := regexp.MustCompile(`edge at ordinal (\d+) -> ring word (\d+)`).FindAllSubmatch(out, -1)
			if len(edges) < 20 {
				t.Fatalf("%d edges mapped\n%s", len(edges), out)
			}
			for _, e := range edges {
				w, ok := softRingWord(m, c.ring, last, num(e[1]))
				if !ok || uint64(w) != num(e[2]) {
					t.Fatalf("edge at %s: the app maps it to word %d (%v), the bench to %s", e[1], w, ok, e[2])
				}
			}
			t.Logf("ring of %d words, stamp %d, %d edges placed", m.Length, last, len(edges))
		})
	}
}
