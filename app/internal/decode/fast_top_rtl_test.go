// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"open-sds/app/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The fast image's decoder list (ADR-SAMPLE-DOMAIN-SPI).
const fastImageDefines = "`define TRIG_UART\n`define TRIG_I2C\n`define TRIG_SPI\n`define SAMPLE_BUS\n" + protocolImageFoundation

// fastTopEvent is an event as tb_top_fast.v prints it: the receiver's
// protocol, kind, value and I2C metadata.
type fastTopEvent struct {
	Protocol, Kind int
	Value          uint32
	Meta           int
}

// TRLC-LINKS: REQ-SDS-229
func runFastTop(t *testing.T, dir, image string, wide []uint64, regs [][2]uint32) (events []fastTopEvent, eventWords []int, trigger int) {
	t.Helper()
	var in strings.Builder
	for _, w := range wide {
		fmt.Fprintf(&in, "%08x\n%08x\n", uint32(w), uint32(w>>32))
	}
	words := filepath.Join(dir, "words.mem")
	if err := os.WriteFile(words, []byte(in.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	var rb strings.Builder
	for _, r := range regs {
		fmt.Fprintf(&rb, "%x\n%x\n", r[0], r[1])
	}
	rp := filepath.Join(dir, "regs.mem")
	if err := os.WriteFile(rp, []byte(rb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("vvp", image, "+input="+words, fmt.Sprintf("+length=%d", 2*len(wide)), "+regs="+rp, fmt.Sprintf("+nregs=%d", len(regs)),
		"+pre=64", "+post=40000").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "PASS fast top") {
		t.Fatalf("simulate: %v\n%s", err, out)
	}
	trigger = -1
	for _, line := range strings.Split(string(out), "\n") {
		var e fastTopEvent
		var n int
		if _, err := fmt.Sscanf(line, "E %d %d %x %d %d", &e.Protocol, &e.Kind, &e.Value, &e.Meta, &n); err == nil {
			events, eventWords = append(events, e), append(eventWords, n)
		}
		fmt.Sscanf(line, "T %d", &trigger)
	}
	return
}

// The fast image's top level: SPI, UART and I2C configured over the host
// bus decode the model's events from both channels and trigger a record on
// the model's match (the trigger follows the match by the receiver's and
// the trigger path's latency).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func TestFastImageTopRTL(t *testing.T) {
	if testing.Short() {
		t.Skip("full top-level simulation")
	}
	needIverilog(t)
	dir := t.TempDir()
	rtl := testenv.RTLDir(t)
	defs := filepath.Join(dir, "fast-defines.v")
	if err := os.WriteFile(defs, []byte(fastImageDefines), 0o600); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(dir, "fast.vvp")
	args := []string{"-g2012", "-s", "tb_top_fast", "-o", image, "-I", rtl, defs}
	for _, f := range []string{"top.v", "record.v", "transport.v", "adc_unpack.v", "sample_timeline.v", "decoded_event_queue.v",
		"decoded_event_bridge.v", "decoded_event_reader.v", "decoded_event_transport.v", "decoded_event_port.v",
		"gpmc_slave.v", "ddio_pair.v", "lane_in.v", "sim/ddr_model.v", "sim/tb_top_fast.v", "event_sequence.v", "envelope_reduce.v", "panel_scan.v",
		"sample_bus.v", "sample_edges.v", "spi_sample.v", "uart_sample.v", "i2c_sample.v"} {
		args = append(args, filepath.Join(rtl, f))
	}
	if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	level := [2]uint32{7, 120 | 13<<8}
	// The trigger within 24 acquisition words (96 ns) after the receiver
	// clock of the model's match.
	checkTrigger := func(t *testing.T, trigger, matchClock int) {
		t.Helper()
		t.Logf("trigger at word %d, the model's match at word %d", trigger, 2*matchClock)
		if trigger < 2*matchClock || trigger > 2*matchClock+24 {
			t.Fatalf("trigger at word %d, the model's match at word %d", trigger, 2*matchClock)
		}
	}
	t.Run("spi-104MHz", func(t *testing.T) {
		var p spiBus
		for _, c := range synthCases() {
			if c.rateHz == 104e6 && c.cpha && !c.cpol { // mode 1, LSB first, clock on C1
				p = c
			}
		}
		c := p.config()
		c.Pattern, c.Length = 0xffa55a, 3
		wide := p.words()
		model, _ := SampleSPI(wide, c)
		control := uint32(1 | c.ClockCh<<1 | int(b2u8(c.Inverted))<<2 | int(b2u8(c.CPOL))<<3 | int(b2u8(c.CPHA))<<4 | int(b2u8(c.MSB))<<5 | c.Length<<6)
		ev, _, trigger := runFastTop(t, dir, image, wide, [][2]uint32{level, {58, uint32(c.Delay) & 0xff},
			{65, uint32(c.GapClocks) & 0xffff}, {66, uint32(c.GapClocks) >> 16}, {67, c.Pattern & 0xffff}, {68, c.Pattern >> 16}, {64, control}})
		var want []fastTopEvent
		match := -1
		for _, e := range model {
			want = append(want, fastTopEvent{Protocol: 3, Kind: int(e.Kind), Value: uint32(e.Data)})
			if e.Match && match < 0 {
				match = e.Clock
			}
		}
		t.Logf("%d of the model's %d events", len(ev), len(want))
		if !reflect.DeepEqual(ev, want[:min(len(want), len(ev))]) || len(ev) < 10 {
			t.Fatalf("events\n%v\nwant\n%v", ev, want)
		}
		checkTrigger(t, trigger, match)
	})
	t.Run("uart-62.5MBd-9O1-inverted", func(t *testing.T) {
		u := uartBus{baud: 62.5e6, err: 0.01, bits: 9, parity: 2, stop: 1, inverted: true, ch: 1, rise: 3e-9, noise: 1, seed: 5}
		u.frames = uartFrames(9, 5)
		c := u.config()
		c.Pattern, c.Length = 0x55aa, 2
		wide := u.lines().words()
		model := SampleUART(wide, c)
		control := uint32(1 | c.Ch<<1 | int(b2u8(c.Inverted))<<2 | c.Length<<3 | c.Bits<<6 | c.Parity<<10 | int(b2u8(c.Resync))<<12)
		ev, _, trigger := runFastTop(t, dir, image, wide, [][2]uint32{level, {49, uint32(c.Period16) & 0xffff}, {50, uint32(c.Period16) >> 16},
			{51, c.Pattern & 0xffff}, {52, c.Pattern >> 16}, {48, control}})
		var want []fastTopEvent
		match := -1
		for _, e := range model {
			want = append(want, fastTopEvent{Protocol: 1, Kind: int(e.Kind), Value: uint32(e.Data)})
			if e.Match && match < 0 {
				match = e.Clock
			}
		}
		t.Logf("%d of the model's %d events", len(ev), len(want))
		if !reflect.DeepEqual(ev, want[:min(len(want), len(ev))]) || len(ev) < 10 {
			t.Fatalf("events\n%v\nwant\n%v", ev, want)
		}
		checkTrigger(t, trigger, match)
	})
	t.Run("i2c-3.4MHz", func(t *testing.T) {
		b := i2cCases()[6]
		c := b.config()
		c.Address, c.Direction, c.Pattern, c.Length = 0x50, 0, 0x0010, 2
		wide := b.lines().words()
		model, matches := SampleI2C(wide, c)
		control := uint32(1 | c.ClockCh<<1 | int(b2u8(c.Inverted))<<2 | c.Direction<<3 | c.Length<<6)
		ev, _, trigger := runFastTop(t, dir, image, wide, [][2]uint32{level, {58, uint32(c.Delay) & 0xff}, {54, uint32(c.Address)},
			{55, c.Pattern & 0xffff}, {56, c.Pattern >> 16}, {53, control}})
		var want []fastTopEvent
		for _, e := range model {
			want = append(want, fastTopEvent{Protocol: 2, Kind: int(e.Kind), Value: uint32(e.Data), Meta: int(e.Meta)})
		}
		t.Logf("%d of the model's %d events", len(ev), len(want))
		if !reflect.DeepEqual(ev, want[:min(len(want), len(ev))]) || len(ev) < 10 {
			t.Fatalf("events\n%v\nwant\n%v", ev, want)
		}
		checkTrigger(t, trigger, matches[0])
	})
}
