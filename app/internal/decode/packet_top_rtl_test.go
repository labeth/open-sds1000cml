// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"open-sds/app/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The protocol images' decoder lists (ADR-PROTOCOL-PACKET-IMAGE).
const (
	protocolImageFoundation = "`define UART_TRIGGER\n`define PROTOCOL_SET\n`define INTERLEAVE\n`define BURST_RECALL\n`define HOST_READ_FIX\n`define BENCH_MHZ 250\n"
	packetImageDefines      = "`define TRIG_ARINC\n`define TRIG_CAN\n`define TRIG_FLEXRAY\n`define TRIG_MIL\n`define TRIG_SENT\n`define SEQUENCE_ONLY\n" + protocolImageFoundation
	manchesterImageDefines  = "`define TRIG_MAN\n`define TRIG_USB\n`define SEQUENCE_ONLY\n" + protocolImageFoundation
	// The general image also decimates; the simulation omits the vendor-FIFO precision path.
	generalImageDefines = "`define TRIG_UART\n`define TRIG_I2C\n`define TRIG_SPI\n" + protocolImageFoundation
)

// sequenceRegs writes a sequence trigger (ADR-PROTOCOL-SEQUENCE-TRIGGER) into
// a register list for tb_top_packet: each element's index, value, mask, then
// kind (which stores it), then the control word.
// TRLC-LINKS: REQ-SDS-013
func sequenceRegs(dir, name string, elements []seqElem, qualify, endBad bool, extra ...[2]uint32) string {
	var regs [][2]uint32
	regs = append(regs, extra...)
	for k, e := range elements {
		regs = append(regs, [2]uint32{119, uint32(k)}, [2]uint32{120, e.value & 0xffff}, [2]uint32{121, e.value >> 16},
			[2]uint32{122, e.mask & 0xffff}, [2]uint32{123, e.mask >> 16}, [2]uint32{124, uint32(e.kind)})
	}
	control := uint32(len(elements))
	if qualify {
		control |= 64
	}
	if endBad {
		control |= 128
	}
	regs = append(regs, [2]uint32{118, control})
	var b strings.Builder
	for _, r := range regs {
		fmt.Fprintf(&b, "%x\n%x\n", r[0], r[1])
	}
	path := filepath.Join(dir, name+"-regs.mem")
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		panic(err)
	}
	return fmt.Sprintf("+regs=%s", path) + " " + fmt.Sprintf("+nregs=%d", len(regs))
}

// TRLC-LINKS: REQ-SDS-013
func dataElements(bytes []int) []seqElem {
	out := make([]seqElem, len(bytes))
	for i, b := range bytes {
		out[i] = seqElem{2, 0xff, uint32(b)}
	}
	return out
}

type packetTopRun struct {
	events  [][4]uint64 // protocol, kind, value, ADC sample
	trigger int64
}

// Simulate the packet image's top level on one CH1 waveform. Each entry
// lasts unit ADC samples at 500 MS/s.
// TRLC-LINKS: REQ-SDS-013
func runPacketTop(t *testing.T, dir, image string, entries []uint8, unit int, control, ticks, aux uint32, pattern uint64, extra ...string) packetTopRun {
	t.Helper()
	var in strings.Builder
	for _, v := range entries {
		fmt.Fprintf(&in, "%02x\n", v)
	}
	path := filepath.Join(dir, fmt.Sprintf("wave-%04x.mem", control))
	if err := os.WriteFile(path, []byte(in.String()), 0600); err != nil {
		t.Fatal(err)
	}
	argv := append([]string{image, "+input=" + path, fmt.Sprintf("+length=%d", len(entries)), fmt.Sprintf("+unit=%d", unit),
		fmt.Sprintf("+control=%x", control), fmt.Sprintf("+ticks=%d", ticks), fmt.Sprintf("+aux=%x", aux), fmt.Sprintf("+pattern=%x", pattern)}, extra...)
	out, err := exec.Command("vvp", argv...).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "PASS packet top") {
		t.Fatalf("simulate: %v\n%s", err, out)
	}
	r := packetTopRun{trigger: -1}
	for _, line := range strings.Split(string(out), "\n") {
		var e [4]uint64
		if _, err := fmt.Sscanf(line, "E %d %d %x %d", &e[0], &e[1], &e[2], &e[3]); err == nil {
			r.events = append(r.events, e)
		}
		fmt.Sscanf(line, "T %d", &r.trigger)
	}
	return r
}

// TRLC-LINKS: REQ-SDS-013
func bitsEntries(wire []int, hi, lo uint8) []uint8 {
	out := make([]uint8, len(wire))
	for i, b := range wire {
		out[i] = lo
		if b == 1 {
			out[i] = hi
		}
	}
	return out
}

// The packet image's top level triggers normal records from ARINC 429, CAN
// and FlexRay through the shared packet block, the Manchester image from
// Manchester, and both publish their events.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
func TestPacketImageTopRTL(t *testing.T) {
	if testing.Short() {
		t.Skip("full top-level simulation")
	}
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required RTL tool %s: %v", tool, err)
		}
	}
	dir := t.TempDir()
	rtl := testenv.RTLDir(t)
	compile := func(name, defines string, decoders ...string) string {
		defs := filepath.Join(dir, name+"-defines.v")
		if err := os.WriteFile(defs, []byte(defines), 0600); err != nil {
			t.Fatal(err)
		}
		image := filepath.Join(dir, name+".vvp")
		args := []string{"-g2012", "-s", "tb_top_packet", "-o", image, "-I", rtl, defs}
		for _, f := range append([]string{"top.v", "record.v", "transport.v", "adc_unpack.v", "sample_timeline.v", "decoded_event_queue.v",
			"decoded_event_bridge.v", "decoded_event_reader.v", "decoded_event_transport.v", "decoded_event_port.v",
			"gpmc_slave.v", "ddio_pair.v", "lane_in.v", "sim/ddr_model.v", "sim/tb_top_packet.v", "event_sequence.v", "envelope_reduce.v", "panel_scan.v"}, decoders...) {
			args = append(args, filepath.Join(rtl, f))
		}
		if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
			t.Fatalf("compile %s: %v\n%s", name, err, out)
		}
		return image
	}
	image := compile("packet", packetImageDefines, "arinc429_trigger.v", "can_trigger.v", "flexray_trigger.v", "mil1553_trigger.v", "sent_trigger.v")
	manchesterImage := compile("line", manchesterImageDefines, "manchester_receive.v", "manchester_trigger.v", "usbls_trigger.v", "protocol_scratch.v")
	generalImage := compile("general", generalImageDefines, "uart_trigger.v", "i2c_trigger.v", "spi_trigger.v")
	seqArgs := func(name string, elements []seqElem, qualify, endBad bool, extra ...[2]uint32) []string {
		return strings.Fields(sequenceRegs(dir, name, elements, qualify, endBad, extra...))
	}
	t.Run("can-long-sequence", func(t *testing.T) {
		// A 20-byte FD payload matched whole; the qualified hit fires when the
		// frame ends, never on the earlier classic frame the native "any"
		// predicate would take.
		payload := make([]int, 20)
		for i := range payload {
			payload[i] = (i*29 + 3) & 0xff
		}
		_, a := canStdFrame(0x100, 2, []int{0x11, 0x22})
		fd := canFDStdFrame(0x2A5, 11, payload)
		wire := append(append(append(ones(20), a...), fd...), ones(10)...)
		r := runPacketTop(t, dir, image, bitsEntries(wire, 210, 40), 500, 1|6<<8, 32000, 32000, 0, seqArgs("can-seq", dataElements(payload), true, false)...)
		dataEnd := int64(20+len(a)+len(fd)-24) * 500
		if r.trigger < dataEnd-500 || r.trigger > dataEnd+2000 {
			t.Fatalf("trigger at %d, FD data ends at %d", r.trigger, dataEnd)
		}
	})
	t.Run("can-classic-qualified", func(t *testing.T) {
		// The fixture's frames: a bad-CRC frame, then a good one with the same
		// data; only the good frame's END may fire "55 ?? 0F".
		in, _ := canStdFrame(0x124, 4, []int{0x55, 0xAA, 0x0F, 0xF0})
		crc := canCRC15(in) ^ 0x10
		bad, rv, rl := canStuffCore(append(append([]int{}, in...), canBitsMSB(crc, 15)...))
		if rl == 5 {
			bad = append(bad, 1-rv)
		}
		bad = append(bad, 1, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1)
		_, good := canStdFrame(0x123, 4, []int{0x55, 0xAA, 0x0F, 0xF0})
		wire := append(append(append(ones(20), bad...), good...), ones(10)...)
		r := runPacketTop(t, dir, image, bitsEntries(wire, 210, 40), 500, 1|6<<8, 32000, 32000, 0,
			seqArgs("can-classic", []seqElem{{2, 0xff, 0x55}, {2, 0, 0}, {2, 0xff, 0x0f}}, true, false)...)
		crcEnd := int64(20+len(bad)+len(good)-13) * 500
		if r.trigger < crcEnd-1000 || r.trigger > crcEnd+2000 {
			t.Fatalf("qualified trigger at %d, good frame's CRC ends at %d (events %v)", r.trigger, crcEnd, r.events)
		}
	})
	t.Run("envelope-recall", func(t *testing.T) {
		// After a frozen CAN record, recall 32 buckets of 256 words (beyond the
		// 4096-word buffer) as an
		// envelope and check each bucket's extremes against the SRAM words.
		_, w := canStdFrame(0x123, 4, []int{0x55, 0xAA, 0x0F, 0xF0})
		wire := append(append(ones(20), w...), ones(10)...)
		out, err := exec.Command("vvp", []string{image, "+input=" + writeEntries(t, dir, bitsEntries(wire, 210, 40)),
			fmt.Sprintf("+length=%d", len(wire)), "+unit=500", "+control=601", "+ticks=32000", "+aux=7d00", "+pattern=0", "+envelope=256"}...).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "PASS packet top") {
			t.Fatalf("simulate: %v\n%s", err, out)
		}
		var sram, buffer []uint32
		received := -1
		for _, line := range strings.Split(string(out), "\n") {
			var v uint32
			if _, err := fmt.Sscanf(line, "S %x", &v); err == nil {
				sram = append(sram, v)
			}
			if _, err := fmt.Sscanf(line, "B %x", &v); err == nil {
				buffer = append(buffer, v)
			}
			fmt.Sscanf(line, "N %d", &received)
		}
		want := envelopeRef(sram, 16, 256)
		if received != 32 || fmt.Sprint(buffer) != fmt.Sprint(want) {
			t.Fatalf("received %d\n got %x\nwant %x", received, buffer, want)
		}
	})
	t.Run("can-error", func(t *testing.T) {
		in, _ := canStdFrame(0x123, 4, []int{0x55, 0xAA, 0x0F, 0xF0})
		crc := canCRC15(in) ^ 0x10
		bad, rv, rl := canStuffCore(append(append([]int{}, in...), canBitsMSB(crc, 15)...))
		if rl == 5 {
			bad = append(bad, 1-rv)
		}
		bad = append(bad, 1, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1)
		_, good := canStdFrame(0x100, 2, []int{0x11, 0x22})
		wire := append(append(append(ones(20), good...), bad...), ones(10)...)
		r := runPacketTop(t, dir, image, bitsEntries(wire, 210, 40), 500, 1|6<<8, 32000, 32000, 0, seqArgs("can-err", []seqElem{{4, 0, 0}}, false, false)...)
		crcEnd := int64(20+len(good)+len(bad)-13) * 500
		if r.trigger < crcEnd-1000 || r.trigger > crcEnd+2000 {
			t.Fatalf("error trigger at %d, bad CRC ends at %d", r.trigger, crcEnd)
		}
	})
	uart := func(bytes []int, badStop int) []int {
		wire := ones(20)
		for i, b := range bytes {
			wire = append(wire, 0)
			for k := 0; k < 8; k++ {
				wire = append(wire, b>>k&1)
			}
			if i == badStop {
				wire = append(wire, 0, 1, 1)
			} else {
				wire = append(wire, 1, 1)
			}
		}
		return append(wire, ones(20)...)
	}
	// UART at 1 Mbaud: 500 ADC samples and 125 receiver clocks per bit;
	// control 48 enables channel 0, with ticks at 49/50.
	uartRegs := [][2]uint32{{49, 125}, {50, 0}, {48, 1}}
	t.Run("general-uart-long-sequence", func(t *testing.T) {
		msg := []int{}
		for _, c := range "Hello, sequence!" {
			msg = append(msg, int(c))
		}
		wire := uart(append([]int{0x55, 0x55}, msg...), -1)
		r := runPacketTop(t, dir, generalImage, bitsEntries(wire, 210, 40), 500, 0, 0, 0, 0,
			append([]string{"+serial"}, seqArgs("uart-seq", dataElements(msg), false, false, uartRegs...)...)...)
		end := int64(20+18*11) * 500
		if r.trigger < end-3000 || r.trigger > end+1000 {
			t.Fatalf("trigger at %d, message ends near %d", r.trigger, end)
		}
	})
	t.Run("general-uart-error", func(t *testing.T) {
		wire := uart([]int{0x41, 0x42, 0x43, 0x44}, 2)
		r := runPacketTop(t, dir, generalImage, bitsEntries(wire, 210, 40), 500, 0, 0, 0, 0,
			append([]string{"+serial"}, seqArgs("uart-err", []seqElem{{4, 0, 0}}, false, false, uartRegs...)...)...)
		stop := int64(20+2*11+9) * 500
		if r.trigger < stop || r.trigger > stop+3000 {
			t.Fatalf("framing-error trigger at %d, bad stop bit at %d", r.trigger, stop)
		}
	})
	expectFrames := func(t *testing.T, r packetTopRun, protocol uint64, frames [][]int, ends []uint64) {
		t.Helper()
		i := 0
		for fi, data := range frames {
			if i+len(data)+2 > len(r.events) {
				t.Fatalf("frame %d: events %v", fi, r.events)
			}
			if e := r.events[i]; e[0] != protocol || e[1] != 1 {
				t.Fatalf("frame %d START %v", fi, e)
			}
			for k, d := range data {
				if e := r.events[i+1+k]; e[0] != protocol || e[1] != 2 || e[2] != uint64(d) {
					t.Fatalf("frame %d byte %d %v want %02x", fi, k, e, d)
				}
			}
			if e := r.events[i+1+len(data)]; e[0] != protocol || e[1] != ends[fi] {
				t.Fatalf("frame %d end %v want kind %d", fi, e, ends[fi])
			}
			i += len(data) + 2
		}
	}
	t.Run("can", func(t *testing.T) {
		// 1 Mbit/s: 500 ADC samples per bit, 32000/256 receiver clocks.
		_, a := canStdFrame(0x100, 2, []int{0x11, 0x22})
		_, b := canStdFrame(0x123, 4, []int{0x55, 0xAA, 0x0F, 0xF0})
		wire := append(append(append(make([]int, 0), ones(20)...), a...), b...)
		wire = append(wire, ones(10)...)
		r := runPacketTop(t, dir, image, bitsEntries(wire, 210, 40), 500, 1|6<<8, 32000, 32000, 0,
			seqArgs("can-native", dataElements([]int{0xAA, 0x0F}), true, false)...)
		expectFrames(t, r, 6, [][]int{{0x11, 0x22}, {0x55, 0xAA, 0x0F, 0xF0}}, []uint64{3, 3})
		crcEnd := int64(20+len(a)+len(b)-13) * 500
		if r.trigger < crcEnd-500 || r.trigger > crcEnd+1500 {
			t.Fatalf("trigger at sample %d, second frame's CRC ends at %d", r.trigger, crcEnd)
		}
	})
	t.Run("flexray", func(t *testing.T) {
		// 10 Mbit/s: 50 ADC samples per bit, 3200/256 receiver clocks.
		first := flexFrameBytes(3, 1, false, false, []int{1, 2, 3, 4})
		second := flexFrameBytes(9, 2, false, false, []int{0x48, 0x69, 0x55, 0xAA})
		var wire []int
		frame := func(bytes []int) {
			wire = append(wire, zeros(6)...)
			wire = append(wire, 1)
			for _, v := range bytes {
				wire = append(wire, 1, 0)
				for d := 7; d >= 0; d-- {
					wire = append(wire, v>>d&1)
				}
			}
			wire = append(wire, 0, 1)
			wire = append(wire, ones(11)...)
		}
		wire = ones(20)
		frame(first)
		frame(second)
		end := int64(len(wire)-13) * 50
		r := runPacketTop(t, dir, image, bitsEntries(wire, 210, 40), 50, 1|10<<8, 3200, 0, 0,
			seqArgs("flexray-native", dataElements([]int{0x55, 0xAA}), true, false)...)
		expectFrames(t, r, 10, [][]int{first, second}, []uint64{3, 3})
		if r.trigger < end || r.trigger > end+150 {
			t.Fatalf("trigger at sample %d, second frame ends at %d", r.trigger, end)
		}
	})
	t.Run("manchester", func(t *testing.T) {
		// 1 Mbit/s IEEE, MSB-first 16-bit words: 20 entries of 25 samples per bit.
		wave := manchesterWave(mBits([]int{0x1234, 0x5678}, true, 16), true, 20)
		wave = append(wave, manchesterWave(mBits([]int{0xA55A, 0x0FF0}, true, 16), true, 20)...)
		r := runPacketTop(t, dir, manchesterImage, wave, 25, 1|1<<3|1<<4|16<<5, 125, 0, 0,
			append([]string{"+manchester"}, seqArgs("manchester-native", []seqElem{{2, 0xffff, 0xA55A}}, true, true)...)...)
		var words []uint64
		for _, e := range r.events {
			if e[0] != 4 {
				t.Fatalf("Manchester event protocol %v", e)
			}
			if e[1] == 2 {
				words = append(words, e[2])
			}
		}
		if fmt.Sprint(words) != fmt.Sprint([]uint64{0x1234, 0x5678, 0xA55A, 0x0FF0}) || r.trigger < int64(len(wave)/2)*25 {
			t.Fatalf("Manchester words %x trigger %d events %v", words, r.trigger, r.events)
		}
	})
	t.Run("arinc", func(t *testing.T) {
		// 100 kbit/s: two entries per bit, 2500 ADC samples each; 1250 receiver clocks per bit.
		var w []uint8
		arincIdle(&w, 8)
		arincAppendWord(&w, arincMakeWord(0312, 1, 0x2abcd, 3), 2)
		arincIdle(&w, 8)
		arincAppendWord(&w, arincMakeWord(0107, 2, 0x15a3f, 1), 2)
		arincIdle(&w, 16)
		levels := uint32(141)<<24 | uint32(114)<<16 | uint32(161)<<8 | 92
		r := runPacketTop(t, dir, image, w, 2500, 1|8<<8, 1250, levels, 0,
			seqArgs("arinc-native", []seqElem{{2, 0x7ffff << 10, 0x15a3f << 10}}, true, true)...)
		if len(r.events) != 6 || r.events[1][1] != 2 || r.events[4][2]>>10&0x7ffff != 0x15a3f || r.events[5][1] != 3 {
			t.Fatalf("ARINC events %v", r.events)
		}
		for _, e := range r.events {
			if e[0] != 8 {
				t.Fatalf("ARINC event protocol %v", e)
			}
		}
		// The second word ends at its last pulse; publication follows the 2.5-bit gap.
		second := int64(8+64+8) * 2500
		if r.trigger < second+31*5000 || r.trigger > second+36*5000 {
			t.Fatalf("trigger at sample %d, second word starts at %d", r.trigger, second)
		}
	})
}

// TRLC-LINKS: REQ-SDS-013
func ones(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = 1
	}
	return out
}

// TRLC-LINKS: REQ-SDS-013
func zeros(n int) []int { return make([]int, n) }

// envelopeRef is the raw envelope contract: per bucket {max2,max1,min2,min1}.
// TRLC-LINKS: REQ-SDS-013
func envelopeRef(words []uint32, skip, bucket int) []uint32 {
	words = words[skip:]
	var out []uint32
	for b := 0; b+bucket <= len(words); b += bucket {
		mn, mx := [2]uint32{0xff, 0xff}, [2]uint32{}
		for _, w := range words[b : b+bucket] {
			for _, x := range [][2]uint32{{w & 0xff, w >> 8 & 0xff}, {w >> 16 & 0xff, w >> 24}} {
				for ch := 0; ch < 2; ch++ {
					mn[ch], mx[ch] = min(mn[ch], x[ch]), max(mx[ch], x[ch])
				}
			}
		}
		out = append(out, mx[1]<<24|mx[0]<<16|mn[1]<<8|mn[0])
	}
	return out
}

// writeEntries stores waveform entries for tb_top_packet.
// TRLC-LINKS: REQ-SDS-013
func writeEntries(t *testing.T, dir string, entries []uint8) string {
	t.Helper()
	var in strings.Builder
	for _, v := range entries {
		fmt.Fprintf(&in, "%02x\n", v)
	}
	path := filepath.Join(dir, "envelope-wave.mem")
	if err := os.WriteFile(path, []byte(in.String()), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
