// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type arincRTLEvent struct {
	kind   int
	value  uint32
	sample uint64
	hit    int
}

// Compare the actual three-level FPGA receiver with the repository decoder.
// This is component evidence, not a fitted acquisition image or live support.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestARINC429TriggerRTL(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required RTL tool %s: %v", tool, err)
		}
	}
	dir := t.TempDir()
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	image := filepath.Join(dir, "arinc.vvp")
	if out, err := exec.Command("iverilog", "-g2012", "-s", "tb_arinc429_trigger", "-o", image,
		filepath.Join(rtl, "arinc429_trigger.v"), filepath.Join(rtl, "sim", "tb_arinc429_trigger.v")).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	run := func(t *testing.T, wave []uint8, period int, pattern, mask uint32, args ...string) []arincRTLEvent {
		t.Helper()
		var input strings.Builder
		for _, v := range wave {
			fmt.Fprintf(&input, "%02x\n", v)
		}
		path := filepath.Join(dir, "samples.mem")
		if err := os.WriteFile(path, []byte(input.String()), 0600); err != nil {
			t.Fatal(err)
		}
		argv := []string{image, "+input=" + path, fmt.Sprintf("+length=%d", len(wave)), fmt.Sprintf("+period=%d", period),
			fmt.Sprintf("+pattern=%08x", pattern), fmt.Sprintf("+mask=%08x", mask)}
		argv = append(argv, args...)
		output, err := exec.Command("vvp", argv...).CombinedOutput()
		if err != nil {
			t.Fatalf("simulate: %v\n%s", err, output)
		}
		var events []arincRTLEvent
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			if line == "" {
				continue
			}
			var e arincRTLEvent
			if _, err := fmt.Sscanf(line, "E %d %x %x %d", &e.kind, &e.value, &e.sample, &e.hit); err != nil {
				t.Fatalf("unexpected output %q: %v", line, err)
			}
			events = append(events, e)
		}
		return events
	}
	check := func(t *testing.T, wave []uint8, period int, pattern, mask uint32, base uint64, events []arincRTLEvent) {
		t.Helper()
		ref := DecodeARINC429(wave, 1/float64(period), ARINC429Cfg{Bitrate: 1, Threshold: 128, HaveThr: true})
		if len(events) != len(ref.Bytes)/4*3 {
			t.Fatalf("events=%d oracle words=%d (%s)", len(events), len(ref.Bytes)/4, ref.Text)
		}
		var starts []int
		for _, s := range ref.Spans {
			if s.Kind == "addr" {
				starts = append(starts, s.I0)
			}
		}
		for w := 0; w < len(ref.Bytes)/4; w++ {
			value := uint32(ref.Bytes[4*w]) | uint32(ref.Bytes[4*w+1])<<8 | uint32(ref.Bytes[4*w+2])<<16 | uint32(ref.Bytes[4*w+3])<<24
			good := popcount(int(value))%2 == 1
			kind, hit, endValue := 2, 0, uint32(0)
			if !good {
				kind, endValue = 4, 1
			} else if value&mask == pattern&mask {
				hit = 1
			}
			a, b, c := events[w*3], events[w*3+1], events[w*3+2]
			start := base + uint64(starts[w])*4
			if a != (arincRTLEvent{1, 0, start, 0}) || b != (arincRTLEvent{kind, value, start, 0}) || c.kind != 3 || c.value != endValue || c.hit != hit {
				t.Fatalf("word %d value=%08x good=%v: got %+v %+v %+v", w, value, good, a, b, c)
			}
			wantLast := start + uint64(31*period)*4
			if c.sample != wantLast {
				t.Fatalf("word %d END timestamp=%x want=%x", w, c.sample, wantLast)
			}
		}
	}
	for _, period := range []int{4, 5, 9, 20, 1250, 10000} {
		for _, mode := range []string{"clean", "parity", "partial", "missing", "duplicates", "dense", "hysteresis", "direct-polarity", "jitter", "reject", "mask", "stall"} {
			if period > 20 && mode != "clean" && mode != "stall" {
				continue
			}
			if period < 9 && (mode == "duplicates" || mode == "dense" || mode == "jitter") {
				continue
			}
			t.Run(fmt.Sprintf("T%d/%s", period, mode), func(t *testing.T) {
				var wave []uint8
				arincIdle(&wave, 4*period)
				words := [][]int{arincMakeWord(0312, 1, 0x2abcd, 3), arincMakeWord(0107, 2, 0x15a3f, 1), arincMakeWord(0, 0, 0, 0)}
				if mode == "parity" {
					words[1][31] ^= 1
				}
				if mode == "partial" {
					words[0] = words[0][:10]
					words[2] = words[2][:17]
				}
				for wi, bits := range words {
					start := len(wave)
					arincAppendWord(&wave, bits, period)
					if wi == 1 {
						switch mode {
						case "missing":
							for i := start + 7*period; i < start+8*period; i++ {
								wave[i] = 128
							}
						case "duplicates", "dense":
							n := 2
							if mode == "dense" {
								n = 3
							}
							for c := 0; c < n; c++ {
								wave[start+(c+4)*period+1] = 128
							}
						case "hysteresis":
							for c := 0; c < 32; c++ { // excursions inside hysteresis must not make a second pulse
								if bits[c] == 1 {
									wave[start+c*period+1] = 150
								} else {
									wave[start+c*period+1] = 100
								}
							}
						case "direct-polarity":
							for c := 0; c < 31; c++ {
								if bits[c] != bits[c+1] {
									for j := period / 2; j < period; j++ {
										wave[start+c*period+j] = wave[start+c*period]
									}
									break
								}
							}
						case "jitter":
							// Preserve the first/last pulse timestamps; vary interior
							// pulse positions independently within their rounded cells.
							for c := 1; c < 31; c++ {
								v := wave[start+c*period]
								if c%2 == 0 {
									wave[start+c*period-1] = v
								} else {
									wave[start+c*period] = 128
								}
							}
						}
					}
					arincIdle(&wave, 4*period)
				}
				pattern, mask := uint32(0), uint32(0)
				if mode == "reject" {
					pattern, mask = 0x12345678, 0xffffffff
				}
				if mode == "mask" {
					pattern, mask = 0312, 0xff
				}
				var args []string
				base := uint64(0xfffffff0)
				if mode == "stall" {
					args = append(args, "+stall=1", "+base=fffffffffffffff0")
					base = 0xfffffffffffffff0
				}
				check(t, wave, period, pattern, mask, base, run(t, wave, period, pattern, mask, args...))
			})
		}
	}
	for _, period := range []int{4, 5, 9, 20} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("gap-boundary/T%d/extra%d", period, extra), func(t *testing.T) {
				var wave []uint8
				arincIdle(&wave, 4*period)
				arincAppendWord(&wave, arincMakeWord(0312, 1, 0x2abcd, 3), period)
				// The pulse-start spacing is exactly floor(2.5T), or one
				// accepted sample longer. Only the latter starts a new word.
				arincIdle(&wave, period*3/2+extra)
				arincAppendWord(&wave, arincMakeWord(0107, 2, 0x15a3f, 1), period)
				arincIdle(&wave, 4*period)
				check(t, wave, period, 0, 0, 0xfffffff0, run(t, wave, period, 0, 0))
			})
		}
	}
	for _, mode := range []string{"reset", "disable", "close-reset", "publication-reset", "invalid", "badperiod"} {
		t.Run(mode, func(t *testing.T) {
			const period = 20
			var wave []uint8
			arincIdle(&wave, 4*period)
			first := len(wave)
			arincAppendWord(&wave, arincMakeWord(0312, 1, 0x2abcd, 3), period)
			end := len(wave)
			arincIdle(&wave, 4*period)
			arincAppendWord(&wave, arincMakeWord(0107, 2, 0x15a3f, 1), period)
			arincIdle(&wave, 4*period)
			args := []string{}
			p := period
			switch mode {
			case "reset":
				args = append(args, fmt.Sprintf("+reset_at=%d", first+15*period))
			case "close-reset":
				args = append(args, fmt.Sprintf("+reset_at=%d", first+31*period+period*5/2+1))
			case "publication-reset":
				args = append(args, fmt.Sprintf("+reset_at=%d", first+31*period+period*5/2+2))
			case "disable":
				args = append(args, fmt.Sprintf("+disable_at=%d", first+15*period))
			case "invalid":
				args = append(args, "+invalid=1")
			case "badperiod":
				p = 3
			}
			events := run(t, wave, p, 0, 0, args...)
			if mode == "publication-reset" {
				if len(events) != 4 || events[0] != (arincRTLEvent{1, 0, 0xfffffff0 + uint64(first)*4, 0}) {
					t.Fatalf("reset must cancel pending DATA and matching END: %+v", events)
				}
				events = events[1:]
			}
			if mode == "invalid" || mode == "badperiod" {
				if len(events) != 0 {
					t.Fatal("invalid configuration published events")
				}
				return
			}
			for i := first; i < end; i++ {
				wave[i] = 128
			}
			check(t, wave, period, 0, 0, 0xfffffff0, events)
		})
	}
}
