package decode

import (
	"fmt"
	"testing"
)

// uartWave synthesizes an 8N1 LSB-first UART waveform: idle-high, one start bit,
// 8 data bits, one stop bit, at spb samples/bit, with lead/trail idle.
func uartWave(bytes []int, spb int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	var w []uint8
	push := func(bit int, k int) {
		v := lo
		if bit == 1 {
			v = hi
		}
		for j := 0; j < k; j++ {
			w = append(w, v)
		}
	}
	push(1, spb*8) // lead idle
	for _, b := range bytes {
		push(0, spb) // start
		for c := 0; c < 8; c++ {
			push((b>>c)&1, spb) // LSB first
		}
		push(1, spb) // stop
		push(1, spb) // inter-byte idle
	}
	push(1, spb*8) // trail idle
	return w
}

func TestDecodeUARTRoundTrip(t *testing.T) {
	want := []int{0x48, 0x69, 0x20, 0x55, 0xAA, 0x0F, 0xF0, 0x0A} // "Hi " + patterns
	spb := 40
	w := uartWave(want, spb)
	colTimeS := 1e-6 // arbitrary; baud is derived: 1/(spb*colTimeS)
	baud := int(1.0 / (float64(spb) * colTimeS))

	// explicit baud
	r := DecodeUART(w, colTimeS, UARTCfg{Baud: baud})
	if !r.OK {
		t.Fatalf("explicit-baud decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", want) {
		t.Errorf("explicit baud: got %v want %v", r.Bytes, want)
	}

	// auto baud
	ra := DecodeUART(w, colTimeS, UARTCfg{})
	if !ra.OK {
		t.Fatalf("auto-baud decode failed: %s", ra.Error)
	}
	if fmt.Sprintf("%v", ra.Bytes) != fmt.Sprintf("%v", want) {
		t.Errorf("auto baud: got %v want %v", ra.Bytes, want)
	}
	if ra.SPB < 30 || ra.SPB > 50 {
		t.Errorf("auto SPB %.1f not near %d", ra.SPB, spb)
	}
}

// spiWave synthesizes an SPI Mode-0 (CPOL=0,CPHA=0) MSB-first waveform:
// SCLK idle low, data set up while SCLK low and sampled on the rising edge; a
// long idle gap between message repeats so the gap-reset re-frames. Mirrors the
// FPGA spi.v ground truth. Returns parallel (clk, data) code slices.
func spiWave(bytes []int, h int) (clk, data []uint8) {
	lo, hi := uint8(40), uint8(210)
	seg := func(c, d uint8, n int) {
		for i := 0; i < n; i++ {
			clk = append(clk, c)
			data = append(data, d)
		}
	}
	seg(lo, lo, h*20) // lead idle (SCLK low)
	for _, b := range bytes {
		for k := 7; k >= 0; k-- { // MSB first
			bit := lo
			if (b>>k)&1 == 1 {
				bit = hi
			}
			seg(lo, bit, h) // SCLK low: set up data
			seg(hi, bit, h) // SCLK high: sampled on the rising edge
		}
	}
	seg(lo, lo, h*20) // trail idle
	return clk, data
}

func TestDecodeSPIRoundTrip(t *testing.T) {
	want := []int{0x48, 0x69, 0x20, 0x55, 0xAA, 0x0F, 0xF0, 0x0A}
	clk, data := spiWave(want, 20)
	r := DecodeSPI(clk, data, 2e-7, SPICfg{CPOL: false, CPHA: false, MSB: true})
	if !r.OK {
		t.Fatalf("SPI decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", want) {
		t.Errorf("SPI: got %v want %v", r.Bytes, want)
	}
}

// i2cWave synthesizes a full I2C transaction: START, addr+RW, ACK, data bytes
// (each ACKed), STOP. SDA changes while SCL is low and is sampled on SCL rising.
func i2cWave(addr7, rw int, data []int, h int) (scl, sda []uint8) {
	lo, hi := uint8(40), uint8(210)
	seg := func(c, d uint8, n int) {
		for i := 0; i < n; i++ {
			scl = append(scl, c)
			sda = append(sda, d)
		}
	}
	pushByte := func(v int) {
		for k := 7; k >= 0; k-- {
			b := lo
			if (v>>k)&1 == 1 {
				b = hi
			}
			seg(lo, b, h)
			seg(hi, b, h)
		}
		seg(lo, lo, h) // ACK=0 on the 9th clock
		seg(hi, lo, h)
	}
	seg(hi, hi, h*4) // idle
	seg(hi, lo, h)   // START: SDA falls while SCL high
	pushByte(addr7<<1 | (rw & 1))
	for _, d := range data {
		pushByte(d)
	}
	seg(lo, lo, h)   // STOP: bring SDA low while SCL low...
	seg(hi, lo, h/2) // ...SCL high...
	seg(hi, hi, h*2) // ...SDA rises while SCL high = STOP
	seg(hi, hi, h*4) // idle
	return scl, sda
}

func TestDecodeI2CRoundTrip(t *testing.T) {
	scl, sda := i2cWave(0x24, 0 /*W*/, []int{0x55, 0xAA}, 20)
	r := DecodeI2C(scl, sda, 2e-7, I2CCfg{})
	if !r.OK {
		t.Fatalf("I2C decode failed: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != "[85 170]" { // 0x55 0xAA
		t.Errorf("I2C data bytes: got %v want [85 170]", r.Bytes)
	}
	// The span stream must carry START, addr 0x24, W, ACK, data, STOP.
	var kinds string
	for _, s := range r.Spans {
		kinds += s.Kind + " "
	}
	for _, need := range []string{"start", "addr", "rw", "ack", "data", "stop"} {
		if !containsWord(kinds, need) {
			t.Errorf("I2C spans missing %q; got %s", need, kinds)
		}
	}
}

func containsWord(s, w string) bool {
	for i := 0; i+len(w) <= len(s); i++ {
		if s[i:i+len(w)] == w {
			return true
		}
	}
	return false
}

func TestAutodetect(t *testing.T) {
	msg := []int{0x48, 0x69, 0x55, 0xAA}
	// UART on C1 only, no second channel.
	uw := uartWave(msg, 40)
	if r := Autodetect(uw, nil, 1e-6, "hex"); r.Proto != "uart" {
		t.Errorf("UART-only: got %q (%d bytes)", r.Proto, len(r.Bytes))
	}
	// SPI: clock on C1, data on C2 — must beat the UART/I2C hypotheses.
	clk, dat := spiWave(msg, 20)
	if r := Autodetect(clk, dat, 2e-7, "hex"); r.Proto != "spi" {
		t.Errorf("SPI pair: got %q (%d bytes)", r.Proto, len(r.Bytes))
	}
	// I2C: SCL on C1, SDA on C2 — START/STOP framing must win over the swap.
	scl, sda := i2cWave(0x24, 0, []int{0x55, 0xAA}, 20)
	if r := Autodetect(scl, sda, 2e-7, "hex"); r.Proto != "i2c" {
		t.Errorf("I2C pair: got %q (%d bytes)", r.Proto, len(r.Bytes))
	}
	// Nothing but a flat line -> off.
	flat := make([]uint8, 2000)
	for i := range flat {
		flat[i] = 128
	}
	if r := Autodetect(flat, nil, 1e-6, "hex"); r.Proto != "off" {
		t.Errorf("flat: got %q, want off", r.Proto)
	}
}

// TestAutodetectAllProtocols covers the seven added single-wire protocols: each
// one's own synthetic signal must be claimed as ITSELF (not a lookalike — a
// 1553 word IS Manchester, a CAN frame is UART-shaped), on either channel, and
// a bare clock must stay "off" rather than decode as constant-bit Manchester.
func TestAutodetectAllProtocols(t *testing.T) {
	type tc struct {
		name string
		want string
		c1   []uint8
		ct   float64
	}
	var cases []tc

	manW, manCT := bkBuild([]int{0xAA, 0xB3, 0x2C, 0x47, 0x99}, true, true, 8, 20, 100000, 0, 0)
	cases = append(cases, tc{"manchester", "manchester", manW, manCT})

	milW := mil1553Wave([]int{0x1234, 0xAAAA}, []bool{true, false},
		[]int{mil1553OddParity(0x1234), mil1553OddParity(0xAAAA)}, 20)
	cases = append(cases, tc{"mil1553", "mil1553", milW, 1.0 / (20.0 * 1e6)})

	nibs := []int{0x1, 0xA, 0x5, 0xF, 0x0, 0xC, 0x3, 0}
	nibs[7] = sentCRC4(nibs[1:7])
	cases = append(cases, tc{"sent", "sent", sentWave([][]int{nibs}, 6, 0, 0), 1e-6})

	_, cw := canStdFrame(0x123, 3, []int{0xDE, 0xAD, 0xBE})
	cases = append(cases, tc{"canfd", "canfd", canRender(cw, 20, true, 160, 160), 1.0 / (20.0 * 500000.0)})

	var arW []uint8
	arincIdle(&arW, 240)
	arincAppendWord(&arW, arincMakeWord(0o107, 1, 0x5A5A, 2), 40)
	arincIdle(&arW, 240)
	cases = append(cases, tc{"arinc429", "arinc429", arW, 2.5e-7})

	usbW := usbWave([]usbPkt{{pid: 0xD /*SETUP*/, data: []int{0x12, 0x00}}, {pid: 0x2 /*ACK*/}}, 20)
	cases = append(cases, tc{"usbls", "usbls", usbW, 1.0 / (20.0 * 1.5e6)})

	flxW := brFlexFrame(brFlexFixCRC([]int{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE}), 20, 8, 8, 8)
	cases = append(cases, tc{"flexray", "flexray", flxW, ctForExact(10_000_000, 20)})

	for _, c := range cases {
		if r := Autodetect(c.c1, nil, c.ct, "hex"); r.Proto != c.want {
			t.Errorf("%s on C1: got %q (%d bytes, err=%q)", c.name, r.Proto, len(r.Bytes), r.Error)
		}
		if r := Autodetect(nil, c.c1, c.ct, "hex"); r.Proto != c.want { // channel-swapped
			t.Errorf("%s on C2: got %q (%d bytes, err=%q)", c.name, r.Proto, len(r.Bytes), r.Error)
		}
	}

	// A bare clock (pure square, >40 edges) must NOT be claimed as constant-bit
	// Manchester (or 0x55 UART) — there is no data on a clock, so Auto says off.
	var sq []uint8
	for i := 0; i < 60*40; i++ {
		if (i/20)%2 == 0 {
			sq = append(sq, 210)
		} else {
			sq = append(sq, 40)
		}
	}
	if r := Autodetect(sq, nil, 1e-6, "hex"); r.Proto != "off" {
		t.Errorf("bare clock: got %q (%d bytes), want off", r.Proto, len(r.Bytes))
	}
}

func TestFmtByte(t *testing.T) {
	if got := FmtByte(0x48, "both"); got != "48'H" {
		t.Errorf("both 0x48 = %q, want 48'H", got)
	}
	if got := FmtByte(0x0A, "both"); got != "0A" { // non-printable -> hex only
		t.Errorf("both 0x0A = %q, want 0A", got)
	}
	if got := FmtByte(0x48, "ascii"); got != "H" {
		t.Errorf("ascii 0x48 = %q, want H", got)
	}
}

func TestDecodeUARTFrameError(t *testing.T) {
	// A byte with the stop bit corrupted -> frame-error span with "!" prefix.
	spb := 30
	lo, hi := uint8(40), uint8(210)
	var w []uint8
	push := func(bit, k int) {
		v := lo
		if bit == 1 {
			v = hi
		}
		for j := 0; j < k; j++ {
			w = append(w, v)
		}
	}
	push(1, spb*8)
	push(0, spb)             // start
	for c := 0; c < 8; c++ { // data 0x55
		push((0x55>>c)&1, spb)
	}
	push(0, spb) // BAD stop (should be high)
	push(1, spb*8)
	r := DecodeUART(w, 1e-6, UARTCfg{Baud: int(1.0 / (float64(spb) * 1e-6))})
	if !r.OK || len(r.Spans) == 0 {
		t.Fatalf("decode failed: %s", r.Error)
	}
	if r.Spans[0].Kind != "frame-error" {
		t.Errorf("expected frame-error, got kind=%s text=%q", r.Spans[0].Kind, r.Spans[0].Text)
	}
}
