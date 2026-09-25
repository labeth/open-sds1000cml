// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type manchesterCandidate struct{ Error, Value, Cell int }

// Exercise the actual quarter-cell RTL against the existing receiver oracle.
// Both phases must match, not merely the phase that gives the expected bytes.
// This is receiver-component evidence, not whole-image or live qualification.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestManchesterReceiveRTL(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	dir := t.TempDir()
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	image := filepath.Join(dir, "receiver.vvp")
	if out, err := exec.Command("iverilog", "-g2012", "-s", "tb_manchester_receive", "-o", image, filepath.Join(rtl, "manchester_receive.v"), filepath.Join(rtl, "sim", "tb_manchester_receive.v")).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	for _, period := range []int{8, 9, 12, 21, 40} {
		for _, width := range []int{1, 7, 8, 16} {
			for _, ieee := range []bool{false, true} {
				for _, msb := range []bool{false, true} {
					for _, broken := range []bool{false, true} {
						name := fmt.Sprintf("T%d/width%d/IEEE%v/MSB%v/broken%v", period, width, ieee, msb, broken)
						t.Run(name, func(t *testing.T) {
							words := []int{0xaaaa, 0x0000, 0xffff, 0xb32c, 0x47, 0x55}
							mask := (1 << width) - 1
							for i := range words {
								words[i] &= mask
							}
							bits := mBits(words, msb, width)
							wave := manchesterWave(bits, ieee, period)
							if broken {
								cell := len(bits) / 2
								begin := period*6 + cell*period
								for j := period / 2; j < period; j++ {
									wave[begin+j] = wave[begin]
								}
							}
							s := sliceChannel(wave, 125, true)
							if !s.ok || len(s.edges) < 2 {
								t.Fatal("fixture has no edges")
							}
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
							boolInt := func(v bool) int {
								if v {
									return 1
								}
								return 0
							}
							args := []string{image, "+input=" + path, fmt.Sprintf("+length=%d", len(wave)), fmt.Sprintf("+first=%d", s.edges[0].i), fmt.Sprintf("+period=%d", period), fmt.Sprintf("+width=%d", width), fmt.Sprintf("+ieee=%d", boolInt(ieee)), fmt.Sprintf("+msb=%d", boolInt(msb))}
							if period == 9 {
								args = append(args, "+stall=1")
							}
							output, err := exec.Command("vvp", args...).CombinedOutput()
							if err != nil {
								t.Fatalf("simulate: %v\n%s", err, output)
							}
							var actual [2][]manchesterCandidate
							var scores, goods [2]int
							seenScore := false
							for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
								if strings.HasPrefix(line, "E ") {
									var phase int
									var e manchesterCandidate
									if _, err := fmt.Sscanf(line, "E %d %d %d %d", &phase, &e.Error, &e.Value, &e.Cell); err != nil {
										t.Fatal(err)
									}
									actual[phase] = append(actual[phase], e)
								} else if strings.HasPrefix(line, "S ") {
									if _, err := fmt.Sscanf(line, "S %d %d %d %d", &scores[0], &scores[1], &goods[0], &goods[1]); err != nil {
										t.Fatal(err)
									}
									seenScore = true
								} else {
									t.Fatalf("unexpected simulator output %q", line)
								}
							}
							if !seenScore {
								t.Fatal("missing final scores")
							}
							for phase := 0; phase < 2; phase++ {
								start := float64(s.edges[0].i) - float64(phase*period)/2
								cells, good, viol := recoverManchester(s, start, float64(period), ieee, float64(s.edges[len(s.edges)-1].i))
								var want []manchesterCandidate
								value, used, last := 0, 0, 0
								for i, c := range cells {
									if c.Bit < 0 {
										want = append(want, manchesterCandidate{Error: 1, Cell: i})
										value, used = 0, 0
										continue
									}
									if msb {
										value = value<<1 | c.Bit
									} else {
										value |= c.Bit << used
									}
									used++
									last = i
									if used == width {
										want = append(want, manchesterCandidate{Value: value, Cell: i})
										value, used = 0, 0
									}
								}
								if used != 0 {
									want = append(want, manchesterCandidate{Error: 1, Cell: last})
								}
								if scores[phase] != good-4*viol || goods[phase] != good || !reflect.DeepEqual(actual[phase], want) {
									t.Fatalf("phase %d: score/good %d/%d want %d/%d\nevents %v\nwant   %v", phase, scores[phase], goods[phase], good-4*viol, good, actual[phase], want)
								}
							}
						})
					}
				}
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestManchesterReceiveRTLBoundaries(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	image := filepath.Join(t.TempDir(), "boundaries.vvp")
	if out, err := exec.Command("iverilog", "-g2012", "-s", "tb_manchester_boundaries", "-o", image, filepath.Join(rtl, "manchester_receive.v"), filepath.Join(rtl, "sim", "tb_manchester_boundaries.v")).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.Command("vvp", image).CombinedOutput(); err != nil {
		t.Fatalf("boundaries: %v\n%s", err, out)
	}
}
