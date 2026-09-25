// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Compare qualified FPGA frames with the repository's public decoder. This
// includes whole-frame rejection after matching bytes and 32-bit timestamp wrap.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
func TestManchesterTriggerRTL(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	dir := t.TempDir()
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	image := filepath.Join(dir, "trigger.vvp")
	args := []string{"-g2012", "-s", "tb_manchester_trigger", "-o", image, filepath.Join(rtl, "manchester_receive.v"), filepath.Join(rtl, "manchester_trigger.v"), filepath.Join(rtl, "protocol_scratch.v"), filepath.Join(rtl, "sim", "tb_manchester_trigger.v")}
	if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile %v\n%s", err, out)
	}
	for _, period := range []int{8, 9, 20, 21} {
		for _, width := range []int{1, 8, 16} {
			for _, ieee := range []bool{false, true} {
				for _, msb := range []bool{false, true} {
					t.Run(fmt.Sprintf("T%d/W%d/IEEE%v/MSB%v", period, width, ieee, msb), func(t *testing.T) {
						mask := (1 << width) - 1
						frames := [][]int{{0xaaaa & mask, 0xb3 & mask, 0x2c & mask, 0x47 & mask}, {0, 0, 0, 0}, {mask, mask, mask, mask}, {0xaaaa & mask, 0xb3 & mask, 0x2c & mask, 0x47 & mask}, {0xaaaa & mask, 0x55 & mask, 0x33 & mask, 0x69 & mask}}
						// The finite-record oracle requires a preamble on its final
						// segment even when the live receiver observed its leading gap.
						if width == 1 {
							frames[4] = []int{0, 1, 0, 1}
						}
						var wave []uint8
						for frame, values := range frames {
							w := manchesterWave(mBits(values, msb, width), ieee, period)
							// A late violation must invalidate an earlier payload predicate.
							if frame == 3 {
								begin := period*6 + (len(values)*width-2)*period
								for j := period / 2; j < period; j++ {
									w[begin+j] = w[begin]
								}
							}
							payload := w[6*period : len(w)-6*period]
							if frame == 0 {
								wave = append(wave, w[:6*period]...)
							} else {
								wave = append(wave, 210)
								last := len(wave) - 1
								for last > 0 && wave[last] == wave[last-1] {
									last--
								}
								first := 0
								for first < len(payload) && payload[first] == 210 {
									first++
								}
								gap := 5*period/2 + 1
								if frame == 1 {
									gap = 7 * period
								}
								if frame == 3 {
									gap = 9 * period
								}
								padding := last + gap - len(wave) - first
								if padding < 0 {
									t.Fatal("invalid frame gap fixture")
								}
								for i := 0; i < padding; i++ {
									wave = append(wave, 210)
								}
							}
							wave = append(wave, payload...)
						}
						// The final idle is long enough to resolve ties without another frame.
						for i := 0; i < 20*period; i++ {
							wave = append(wave, 210)
						}
						cfg := ManchesterCfg{Bitrate: 1000000 / period, IEEE: ieee, MSB: msb, Bits: width, Threshold: 125, HaveThr: true}
						// Choose sample duration so the configured rate gives exactly period ticks.
						result := DecodeManchester(wave, 1/(float64(cfg.Bitrate)*float64(period)), cfg)
						if !result.OK {
							t.Fatal(result.Error)
						}
						type event struct {
							kind, value int
							sample      uint64
							hit         int
						}
						var want []event
						var packet []int
						bad := false
						matches := 0
						finish := func() {
							if !bad {
								for i := 1; i < len(packet); i++ {
									if packet[i-1] == frames[0][0] && packet[i] == frames[0][1] {
										matches++
										break
									}
								}
							}
							packet = nil
							bad = false
						}
						for _, s := range result.Spans {
							switch s.Kind {
							case "start", "gap":
								finish()
							case "data":
								want = append(want, event{kind: 2, value: s.Val, sample: uint64(math.Round((float64(s.I1)+1-float64(period)/4)*4)) + 0xfffffff0})
								packet = append(packet, s.Val)
							case "frame-error":
								want = append(want, event{kind: 4, sample: uint64(math.Round((float64(s.I1)+1-float64(period)/4)*4)) + 0xfffffff0})
								bad = true
							}
						}
						finish()
						var input strings.Builder
						for _, v := range wave {
							if v >= 125 {
								input.WriteString("1\n")
							} else {
								input.WriteString("0\n")
							}
						}
						path := filepath.Join(dir, "samples.mem")
						if err := os.WriteFile(path, []byte(input.String()), 0600); err != nil {
							t.Fatal(err)
						}
						b := func(v bool) int {
							if v {
								return 1
							}
							return 0
						}
						out, err := exec.Command("vvp", image, "+input="+path, fmt.Sprintf("+length=%d", len(wave)), fmt.Sprintf("+period=%d", period), fmt.Sprintf("+width=%d", width), fmt.Sprintf("+ieee=%d", b(ieee)), fmt.Sprintf("+msb=%d", b(msb)), fmt.Sprintf("+invert=%d", b(!msb)), fmt.Sprintf("+pattern=%x", uint64(frames[0][0])<<16|uint64(frames[0][1]))).CombinedOutput()
						if err != nil {
							t.Fatalf("simulation %v\n%s", err, out)
						}
						var got []event
						hits, starts, ends := 0, 0, 0
						complete := false
						for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
							if strings.HasPrefix(line, "E ") {
								var e event
								if _, err := fmt.Sscanf(line, "E %d %d %d %d", &e.kind, &e.value, &e.sample, &e.hit); err != nil {
									t.Fatal(err)
								}
								hits += e.hit
								switch e.kind {
								case 1:
									starts++
								case 3:
									ends++
								case 2, 4:
									got = append(got, e)
								default:
									t.Fatalf("unexpected event %v", e)
								}
							} else if line == "F 0" {
								complete = true
							} else {
								t.Fatalf("unexpected simulator output %q", line)
							}
						}
						if !complete || hits != matches || starts != ends || len(got) != len(want) {
							t.Fatalf("frames %d/%d hits %d want %d events %d want %d\n%s\nreference %v", starts, ends, hits, matches, len(got), len(want), out, want)
						}
						for i, e := range got {
							delta := int64(e.sample) - int64(want[i].sample)
							if e.kind != want[i].kind || e.value != want[i].value || delta < -8 || delta > 8 {
								t.Fatalf("event %d got %+v want %+v (delta %d)", i, e, want[i], delta)
							}
						}
					})
				}
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
func TestManchesterTriggerRTLBoundaries(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	for _, bench := range []string{"tb_manchester_frame_bounds", "tb_manchester_replay_rate"} {
		t.Run(bench, func(t *testing.T) {
			image := filepath.Join(t.TempDir(), "frames.vvp")
			if out, err := exec.Command("iverilog", "-g2012", "-s", bench, "-o", image, filepath.Join(rtl, "manchester_trigger.v"), filepath.Join(rtl, "protocol_scratch.v"), filepath.Join(rtl, "sim", bench+".v")).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			if out, err := exec.Command("vvp", image).CombinedOutput(); err != nil {
				t.Fatalf("frame boundaries: %v\n%s", err, out)
			}
		})
	}
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
func TestProtocolScratchOwnershipRTL(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	image := filepath.Join(t.TempDir(), "scratch.vvp")
	if out, err := exec.Command("iverilog", "-g2012", "-s", "tb_protocol_scratch", "-o", image, filepath.Join(rtl, "protocol_scratch.v"), filepath.Join(rtl, "sim", "tb_protocol_scratch.v")).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.Command("vvp", image).CombinedOutput(); err != nil {
		t.Fatalf("scratch ownership: %v\n%s", err, out)
	}
}
