// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"strings"
	"testing"
)

// A record that opens inside back-to-back frames decodes without errors: the
// bytes cut by the window are gaps (not "!0B !AA !A9" at the start of every
// capture of the bench UART, which the default error watch catches).
// TRLC-LINKS: REQ-SDS-018
func TestUARTSyncInIsNotAnError(t *testing.T) {
	const spb = 10
	var line []uint8
	bit := func(b int) {
		for k := 0; k < spb; k++ {
			line = append(line, uint8(40+160*b))
		}
	}
	for f := 0; f < 40; f++ {
		v := []int{0x55, 0xAA, 0x0F, 0xF0}[f%4]
		bit(0)
		for c := 0; c < 8; c++ {
			bit(v >> c & 1)
		}
		bit(1)
	}
	for _, cut := range []int{13, 37, 61, 88} {
		r := DecodeUART(line[cut:], 1.0/(115200*spb), UARTCfg{Baud: 115200, Format: "hex", Threshold: 120, HaveThr: true})
		if !r.OK || strings.Contains(r.Text, "!") {
			t.Fatalf("cut %d: %q", cut, r.Text)
		}
		if len(r.Bytes) < 30 {
			t.Fatalf("cut %d: only %d bytes", cut, len(r.Bytes))
		}
	}
	// A line that never frames keeps its errors.
	r := DecodeUART(line, 1.0/(57600*spb), UARTCfg{Baud: 115200, Format: "hex", Threshold: 120, HaveThr: true})
	if !strings.Contains(r.Text, "!") {
		t.Fatalf("wrong baud decoded clean: %q", r.Text)
	}
}

// A record that opens idle is in sync from its first byte: a break after it
// stays an error (the sigrok oracle's break-condition stream, 41 break 42 43;
// the first sync-in rule turned 41 and the break into gaps).
// TRLC-LINKS: REQ-SDS-018
func TestUARTSyncInKeepsBreakAfterIdleStart(t *testing.T) {
	const spb = 10
	var line []uint8
	level := func(b, bits int) {
		for k := 0; k < bits*spb; k++ {
			line = append(line, uint8(40+160*b))
		}
	}
	emit := func(v int) {
		level(0, 1)
		for c := 0; c < 8; c++ {
			level(v>>c&1, 1)
		}
		level(1, 2)
	}
	level(1, 8)
	emit(0x41)
	level(0, 15) // break
	level(1, 2)
	emit(0x42)
	emit(0x43)
	level(1, 8)
	r := DecodeUART(line, 1.0/(115200*spb), UARTCfg{Baud: 115200, Format: "hex", Threshold: 120, HaveThr: true})
	if r.Text != "41 !00 42 43" {
		t.Fatalf("break stream: %q, want 41 !00 42 43", r.Text)
	}
}

// benchUARTLine is the bench's UART source: "Hi U" AA 0F F0 0A, each byte 8N1
// with an idle bit after its stop bit, and 27 idle bits after each message.
func benchUARTLine(spb, messages int) ([]uint8, []int) {
	msg := []int{0x48, 0x69, 0x20, 0x55, 0xAA, 0x0F, 0xF0, 0x0A}
	var line []uint8
	level := func(b, bits int) {
		for k := 0; k < bits*spb; k++ {
			line = append(line, uint8(40+160*b))
		}
	}
	for m := 0; m < messages; m++ {
		for _, v := range msg {
			level(0, 1)
			for c := 0; c < 8; c++ {
				level(v>>c&1, 1)
			}
			level(1, 2)
		}
		level(1, 27)
	}
	return line, msg
}

// Every capture of the bench line decodes its bytes and nothing else, wherever
// the window opens, not "2A !0B !AA !A9" or a byte read from a cut one's last
// bits ("F8" for 0A), on the LCD and on the web alike: the first edge 1.5 bits
// in must not count as opening idle, and four frames are too few for the
// sync-in.
// TRLC-LINKS: REQ-SDS-018
func TestUARTSyncInBenchLine(t *testing.T) {
	const spb = 12
	line, msg := benchUARTLine(spb, 4)
	period := (8*11 + 27) * spb
	ct := 1.0 / (115200 * spb)
	bad := 0
	for cut := 0; cut < period; cut++ {
		rec := line[cut : cut+2*period]
		r := DecodeUART(rec, ct, UARTCfg{Baud: 115200, Format: "hex", Threshold: 120, HaveThr: true})
		if !r.OK || strings.Contains(r.Text, "!") || len(r.Bytes) < len(msg) {
			bad++
			if bad <= 5 {
				t.Errorf("cut %d: %q", cut, r.Text)
			}
			continue
		}
		k0 := -1
		for k, v := range msg {
			if v == r.Bytes[0] {
				k0 = k
			}
		}
		for j, v := range r.Bytes {
			if k0 < 0 || v != msg[(k0+j)%len(msg)] {
				bad++
				if bad <= 5 {
					t.Errorf("cut %d: %q is not the line's bytes", cut, r.Text)
				}
				break
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d windows decoded wrong", bad, period)
	}
}

// A parity error after a short idle is the line's, not the sync-in's: 8 bits
// of idle open a record (a parity bit's edge can follow a word of ones, but
// the rule counts the data bits), so the first frames' errors stay.
// TRLC-LINKS: REQ-SDS-018
func TestUARTSyncInKeepsAParityErrorAfterIdle(t *testing.T) {
	const sr, baud = 1_000_000, 62500
	for _, bad := range []int{0, 1} {
		fs := frames(0x11, 0x22, 0x33, 0x44, 0x55, 0x66)
		fs[bad].flipParity = true
		bits := oracleUARTBits(sr, baud, 8, "even", fs)
		r := DecodeUART(bitsToCodes(bits), 1.0/sr, UARTCfg{Baud: baud, Parity: "even"})
		want := []string{"!11 22 33 44 55 66", "11 !22 33 44 55 66"}[bad]
		if r.Text != want {
			t.Fatalf("parity error on frame %d: %q, want %q", bad, r.Text, want)
		}
	}
}
