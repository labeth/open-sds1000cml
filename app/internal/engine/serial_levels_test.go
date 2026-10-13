// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"strings"
	"testing"

	"open-sds/app/internal/sramcapture"
)

// levelsEngine is a bare engine whose image has the hardware UART matcher,
// at 1 V/div with no offset, the trigger centred, a request without a
// threshold armed.
func levelsEngine(p SerialParams) *Engine {
	e := &Engine{hardwareUART: true, hardwareSPI: true, hardwareSequence: true, band: Band{TdivS: 100e-6}}
	for ch := 0; ch < 2; ch++ {
		e.chVdivBits[ch].Store(math.Float64bits(1))
	}
	e.trigPosFrac.Store(math.Float64bits(.5))
	e.SetSerialParams(p)
	e.SetSerialMode(SerialTrigger)
	return e
}

// The serial trigger is the FPGA's: until a capture has shown the line's two
// levels nothing triggers, and the status says so; then the matcher slices at
// the decoder's threshold, re-expressed at a new V/div or offset, and kept
// through a capture whose level moved under two codes (ADR-SERIAL-HARDWARE-TRIGGER).
// TRLC-LINKS: REQ-SDS-013
func TestLearnedLevelPlansTheHardwareMatcher(t *testing.T) {
	e := levelsEngine(SerialParams{Proto: serUART, ChA: 0, ChB: 1, Baud: 115200, Bits: 8, Parity: "none", Bytes: []int{0x48, 0x69}})
	if c, _, _, _, _ := e.sramConfig(); c.UART.Enabled || c.Normal {
		t.Fatalf("a matcher before the line's levels are known: %+v", c.UART)
	}
	if why := e.serialNoHardware(); !strings.Contains(why, "learning the line levels on C1") {
		t.Fatalf("the reason: %q", why)
	}
	w := uartWave([]int{0x48, 0x69, 0x20}, 20) // rails 40 and 210: threshold 125
	f := &Frame{C1: w, C2: w, Valid: len(w), SampleS: 1 / (115200.0 * 20)}
	scale := [2]chScale{{1, 0}, {1, 0}}
	e.learnSerialLevels(f, scale)
	c, _, _, _, _ := e.sramConfig()
	if !c.UART.Enabled || !c.Normal || c.TriggerChannel != 0 || c.TriggerLevel != 125 || c.UART.Pattern != 0x4869 {
		t.Fatalf("the hardware plan at the learned level: %+v, level %d", c.UART, c.TriggerLevel)
	}
	// 2 V/div: the level's -0.12 V is 1.5 codes from the centre, not 3
	e.chVdivBits[0].Store(math.Float64bits(2))
	if c, _, _, _, _ := e.sramConfig(); c.TriggerLevel != 127 {
		t.Fatalf("at 2 V/div the level is code %d, want 127", c.TriggerLevel)
	}
	// a 1 V offset moves the trace, and the level with it
	e.trigOffV[0].Store(math.Float64bits(1))
	if c, _, _, _, _ := e.sramConfig(); c.TriggerLevel != 139 {
		t.Fatalf("with a 1 V offset the level is code %d, want 139", c.TriggerLevel)
	}
	// a capture whose threshold moved one code keeps the plan
	for i := range w {
		if w[i] == 210 {
			w[i] = 212
		}
	}
	e.learnSerialLevels(f, scale)
	if v := math.Float64frombits(e.serLv.volts[0].Load()); math.Abs(v-(-3.0/25)) > 1e-9 {
		t.Fatalf("a one-code move re-planned: %v V", v)
	}
}

// SPI's matcher ends a word after 1.5 clock periods of silence: the rate is
// measured on the clock line when the request names none.
// TRLC-LINKS: REQ-SDS-013
func TestSPIClockRateFromTheClockLine(t *testing.T) {
	clk, data := spiWave([]int{0x55, 0xaa, 0x0f}, 10) // 20-sample clock periods
	if hz := clockRate(clk, 1e-8); math.Abs(hz-5e6) > 1 {
		t.Fatalf("clock %v Hz, want 5 MHz", hz)
	}
	e := levelsEngine(SerialParams{Proto: serSPI, ChA: 0, ChB: 1, MSB: true, Bytes: []int{0x55}})
	f := &Frame{C1: clk, C2: data, Valid: len(clk), SampleS: 1e-8}
	if _, why := e.serialPlanParams(e.ser.params); why == "" {
		t.Fatal("an SPI plan before its clock was measured")
	}
	e.learnSerialLevels(f, [2]chScale{{1, 0}, {1, 0}})
	p, why := e.serialPlanParams(e.ser.params)
	if why != "" || p.SPIClockHz != 5000000 || !p.HaveThr {
		t.Fatalf("SPI plan params %+v (%q)", p, why)
	}
	if c, _, _, _, _ := e.sramConfig(); !c.SPI.Enabled || !c.Normal {
		t.Fatalf("no SPI matcher: %+v", c.SPI)
	}
}

// The FPGA triggers where the match ended; the trigger point moves to where
// it began, so the matched bytes sit at the trigger position. A point the
// decode finds no match before stays the hardware's.
// TRLC-LINKS: REQ-SDS-013
func TestHardwareMatchStartPlacesTheTrigger(t *testing.T) {
	const spb = 20
	e := levelsEngine(SerialParams{Proto: serUART, ChA: 0, ChB: 1, Baud: 115200, Bits: 8, Parity: "none", Bytes: []int{0x48, 0x69}})
	w := uartWave([]int{0x10, 0x48, 0x69, 0x20}, spb)
	f := &Frame{C1: w, C2: w, Valid: len(w), SampleS: 1 / (115200.0 * spb)}
	start := 8*spb + 11*spb          // 0x48's start bit
	end := 8*spb + 2*11*spb + 10*spb // the end of 0x69's stop bit
	got := e.serialMatchStart(f, float64(end))
	if math.Abs(got-float64(start)) > 2 {
		t.Fatalf("the match starts at %v, want %d", got, start)
	}
	if got := e.serialMatchStart(f, float64(start-spb)); got >= 0 {
		t.Fatalf("a point before the match moved to %v", got)
	}
}

// A UART request with the decoder's auto baud (the web's: baud 0) takes the
// bit rate measured on its line, snapped to a standard rate, so the FPGA
// matcher can count its bits (it planned nothing, and nothing triggered).
// TRLC-LINKS: REQ-SDS-013
func TestUARTAutoBaudPlansTheHardwareMatcher(t *testing.T) {
	const spb = 20
	w := uartWave([]int{0x48, 0x69, 0x20, 0x55, 0xaa, 0x0f, 0xf0, 0x0a}, spb)
	sampleS := 1 / (115200.0 * spb) * 1.01 // a 1 % fast clock still reads 115200
	if b := uartBaud(w, sampleS); b != 115200 {
		t.Fatalf("measured %v baud", b)
	}
	e := levelsEngine(SerialParams{Proto: serUART, ChA: 0, ChB: 0, Baud: 0, Bits: 8, Parity: "none", Bytes: []int{0xaa}})
	if _, why := e.serialPlanParams(e.ser.params); !strings.Contains(why, "learning the line levels") {
		t.Fatalf("before any capture: %q", why)
	}
	e.serLv.volts[0].Store(math.Float64bits(-.12)) // the level known, the rate not yet
	e.serLv.have[0].Store(true)
	if _, why := e.serialPlanParams(e.ser.params); why != "measuring the UART bit rate" {
		t.Fatalf("before the rate was measured: %q", why)
	}
	e.learnSerialLevels(&Frame{C1: w, C2: w, Valid: len(w), SampleS: sampleS}, [2]chScale{{1, 0}, {1, 0}})
	p, why := e.serialPlanParams(e.ser.params)
	if why != "" || p.Baud != 115200 {
		t.Fatalf("plan params %+v (%q)", p, why)
	}
	if c, _, _, _, _ := e.sramConfig(); !c.UART.Enabled || c.UART.Pattern != 0xaa {
		t.Fatalf("no UART matcher: %+v", c.UART)
	}
}

// sentLine is a SENT line at tick samples a tick: each frame a SYNC of 56
// ticks and its nibbles, every pulse five ticks low and high for the rest.
func sentLine(frames [][]int, tick int) []uint8 {
	var w []uint8
	push := func(v uint8, k int) {
		for j := 0; j < k; j++ {
			w = append(w, v)
		}
	}
	pulse := func(ticks int) { push(40, 5*tick); push(210, (ticks-5)*tick) }
	push(210, 8*tick)
	for _, f := range frames {
		pulse(56)
		for _, v := range f {
			pulse(12 + v)
		}
	}
	push(40, 5*tick)
	push(210, 8*tick)
	return w
}

// The scope's requests name no threshold for any bus: once a capture has
// shown the line, a CAN match is planned on the packet image, SENT's with the
// tick and the nibbles measured on its line, and a MIL-1553 word typed as two
// bytes is one word: every bus, not UART, I2C and SPI only.
// TRLC-LINKS: REQ-SDS-013
func TestEveryBusPlansFromTheScope(t *testing.T) {
	e, b, _ := newPacketTestEngine(t)
	for ch := 0; ch < 2; ch++ {
		e.chVdivBits[ch].Store(math.Float64bits(1))
	}
	e.trigPosFrac.Store(math.Float64bits(.5))
	e.band = Band{TdivS: 100e-6}
	e.SetSerialParams(SerialParams{Proto: serCAN, Baud: 500000, Bytes: []int{0x55, 0xAA}})
	e.SetSerialMode(SerialTrigger)
	if e.serviceImage() {
		t.Fatal("an image loaded before the line's levels are known")
	}
	w := uartWave([]int{0x55, 0xAA}, 20) // rails 40 and 210: threshold 125
	e.learnSerialLevels(&Frame{C1: w, C2: w, Valid: len(w), SampleS: 1e-7}, [2]chScale{{1, 0}, {1, 0}})
	if !e.serviceImage() || b.image != "packet" {
		t.Fatalf("CAN with learned levels: image %s", b.image)
	}
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.Packet.Enabled || cfg.Packet.Protocol != serCAN || cfg.TriggerLevel != 125 {
		t.Fatalf("CAN plan %+v level %d", cfg.Packet, cfg.TriggerLevel)
	}

	e.SetSerialParams(SerialParams{Proto: serSENT, Baud: 115200, Bytes: []int{1, 2}})
	if cfg, _, _, _, _ := e.sramConfig(); cfg.SENT.Enabled || !strings.Contains(e.serialNoHardware(), "measuring the SENT tick") {
		t.Fatalf("SENT before its tick: %+v %q", cfg.SENT, e.serialNoHardware())
	}
	frame := []int{0, 1, 2, 5, 10, 3, 4, 1}
	line := sentLine([][]int{frame, frame, frame, frame}, 30) // 3 us ticks at 100 ns
	e.learnSerialLevels(&Frame{C1: line, C2: line, Valid: len(line), SampleS: 1e-7}, [2]chScale{{1, 0}, {1, 0}})
	// the packet image matches with the sequence trigger
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.SENT.Enabled || cfg.SENT.TickTicks != 375 || cfg.SENT.Nibbles != 8 || cfg.Sequence.Length != 2 {
		t.Fatalf("SENT plan %+v sequence %d (want 375 ticks of 8 ns, 8 nibbles, 2 units)", cfg.SENT, cfg.Sequence.Length)
	}

	e.SetSerialParams(SerialParams{Proto: serMIL1553, Baud: 1000000, Bytes: []int{0xA5, 0x5A}})
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.MIL1553.Enabled || cfg.Sequence.Length != 1 || cfg.Sequence.Elements[0].Value != 0xA55A {
		t.Fatalf("MIL-1553 plan %+v sequence %+v", cfg.MIL1553, cfg.Sequence)
	}
	// an ARINC 429 data field as the BUS table's bytes 00 AB CD
	if p, _ := e.serialPlanParams(SerialParams{Proto: serARINC, Baud: 100000, Bytes: []int{0x00, 0xAB, 0xCD}}); len(p.Bytes) != 1 || p.Bytes[0] != 0xABCD {
		t.Fatalf("ARINC 429 field %v", p.Bytes)
	}
	if p, _ := e.serialPlanParams(SerialParams{Proto: serARINC, Baud: 100000, Bytes: []int{0x12, 0x34, 0x56}}); len(p.Bytes) != 3 {
		t.Fatalf("an ARINC 429 field over 19 bits joined: %v", p.Bytes)
	}
}

// The live window holds the room the trigger point may move back before the
// hardware serial trigger, all of it before: the match and two units more
// (an ARINC 429 field fires the FPGA ~0.44 ms after it begins, past a quarter
// screen at 200 us/div, which would pad the screen's left edge).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-010
func TestSerialPreRoomHoldsTheMatchStart(t *testing.T) {
	e := levelsEngine(SerialParams{Proto: serARINC, ChA: 0, Baud: 100000, Bytes: []int{0x00, 0xAB, 0xCD}})
	w := uartWave([]int{0x55}, 20)
	e.learnSerialLevels(&Frame{C1: w, C2: w, Valid: len(w), SampleS: 1e-7}, [2]chScale{{1, 0}, {1, 0}})
	const sampleS, screen = 2e-9, 1000000                            // 200 us/div
	if room := e.serialPreRoom(sampleS, screen); room != 3*36*5000 { // one field and two words of 36 bits, 5000 samples a bit
		t.Fatalf("ARINC room %d samples, want %d", room, 3*36*5000)
	}
	e.SetSerialParams(SerialParams{Proto: serI2C, ChA: 0, ChB: 1, Bytes: []int{0x55}}) // no unit known: a quarter screen
	if room := e.serialPreRoom(sampleS, screen); room != screen/4 {
		t.Fatalf("I2C room %d", room)
	}
	m := sramcapture.Metadata{Length: 524288, SamplesPerWord: 2, TriggerIndex: 262144}
	off, n := liveSRAMWindowPre(m, 10000, .5, 4000)
	if pre := int(m.TriggerIndex-off) * 2; n != (10000+512+4000)/2 || pre != 5000+256+4000 {
		t.Fatalf("window %d words, %d samples before the trigger", n, pre)
	}
}

// The record holds, before the FPGA's serial trigger, the way the trigger
// point last moved back to the match's start: at 200 us/div the record is one
// screen, and the moved point would leave the screen's left edge without
// samples (an ARINC 429 field begins 0.44 ms before the trigger).
// TRLC-LINKS: REQ-SDS-013
func TestRecordHoldsTheMatchBeforeTheTrigger(t *testing.T) {
	e := levelsEngine(SerialParams{Proto: serUART, ChA: 0, ChB: 1, Baud: 115200, Bits: 8, Parity: "none", Bytes: []int{0x48}})
	e.band = Band{TdivS: 200e-6}
	c0, plan, _, _, _ := e.sramConfig()
	e.serLv.shiftS.Store(math.Float64bits(440e-6))
	c1, _, _, _, _ := e.sramConfig()
	want := c0.PreWords + uint32(440e-6/plan.SampleS)/uint32(2-boolInt(plan.Log != 0)) // samples a word
	if d := int(c1.PreWords) - int(want); d < -1 || d > 1 || c1.PreWords+c1.PostWords != c0.PreWords+c0.PostWords {
		t.Fatalf("pre %d -> %d words, want %d (record %d words)", c0.PreWords, c1.PreWords, want, c0.PreWords+c0.PostWords)
	}
	e.SetSerialParams(SerialParams{Proto: serUART, ChA: 0, Baud: 9600, Bytes: []int{0x48}}) // another request: measured anew
	if c2, _, _, _, _ := e.sramConfig(); c2.PreWords != c0.PreWords {
		t.Fatalf("a new request kept the old shift: pre %d", c2.PreWords)
	}
}
