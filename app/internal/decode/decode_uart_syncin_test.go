// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"strings"
	"testing"
)

// A record that opens inside back-to-back frames decodes without errors: the
// bytes cut by the window are gaps (bug hunt #2: every capture of the bench
// UART began "!0B !AA !A9", so the default error watch caught every frame).
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
