package engine

import (
	"testing"

	"open-sds/app/internal/sramcapture"
)

// Frames captured on the instrument on 2026-09-27 (ADR-PANEL-SERIAL-SCAN).
var panelIdle = [4]uint16{0xfdfc, 0xffff, 0xfcff, 0xffff}

// TRLC-LINKS: REQ-SDS-022
func withBitLow(w [4]uint16, bit int) [4]uint16 {
	w[bit/16] &^= 1 << uint(bit%16)
	return w
}

// TRLC-LINKS: REQ-SDS-022
func TestPanelMatrixButtons(t *testing.T) {
	var k panelKnobs
	if m, _ := panelMatrix(sramcapture.PanelFrame{Words: panelIdle}, &k); m != [5]uint16{0xffff, 0xffff, 0xffff, 0xffff, 0} {
		t.Fatalf("idle frame gave %04x", m)
	}
	for _, c := range []struct {
		name      string
		frameBit  int
		word, bit int
	}{
		{"RUN/STOP", 45, 1, 2}, // 0x65:2
		{"AUTO", 5, 3, 10},     // 0x67:10
		{"SINGLE", 37, 1, 10},  // 0x65:10
	} {
		m, _ := panelMatrix(sramcapture.PanelFrame{Words: withBitLow(panelIdle, c.frameBit)}, &k)
		want := [5]uint16{0xffff, 0xffff, 0xffff, 0xffff, 0}
		want[c.word] &^= 1 << uint(c.bit)
		if m != want {
			t.Errorf("%s: got %04x want %04x", c.name, m, want)
		}
	}
}

// TRLC-LINKS: REQ-SDS-022
func TestPanelMatrixKnobs(t *testing.T) {
	var k panelKnobs
	frame := sramcapture.PanelFrame{Words: panelIdle}
	panelMatrix(frame, &k)
	// Row 7 (trigger level, 0x64 high byte) three counts up; row 3 (T/div,
	// stepped) two counts down.
	// Four quadrature counts per detent.
	frame.Knobs[3] = 3 * panelCountsPerDetent
	frame.Knobs[1] = uint16(uint8(256 - 2*panelCountsPerDetent))
	// The decoder's priority serves T/div (row 3) first, one step a read,
	// then the trigger level's whole count.
	var m [5]uint16
	for i := 0; i < 2; i++ {
		m, _ = panelMatrix(frame, &k)
		if m[2] != 0xffff&^(1<<(8+7)) || m[0] != 0xffff || m[4] != 1 {
			t.Fatalf("T/div read %d: %04x, want one counter-clockwise step", i, m)
		}
	}
	m, _ = panelMatrix(frame, &k)
	if m[2] != 0xffff || m[0] != 0xffff&^(1<<(8+6)) || m[4] != 3 {
		t.Fatalf("trigger level read %04x: want clockwise, magnitude 3", m)
	}
	if m, _ = panelMatrix(frame, &k); m[4] != 0 {
		t.Fatalf("steps left over: %04x", m)
	}
}

// Reads no hand can make - every key down, or a knob jumping more than six
// detents between reads - yield nothing and re-prime the knob counts.
// TRLC-LINKS: REQ-SDS-022
func TestPanelMatrixRejectsGarbage(t *testing.T) {
	var k panelKnobs
	idle := sramcapture.PanelFrame{Words: panelIdle}
	panelMatrix(idle, &k)
	if _, ok := panelMatrix(sramcapture.PanelFrame{}, &k); ok {
		t.Fatal("all-zero read (48 keys down) accepted")
	}
	// After the garbage the counts re-prime: a counter now far away is the
	// new baseline, not a 100-count turn.
	far := idle
	far.Knobs[3] = 100
	if m, ok := panelMatrix(far, &k); !ok || m[4] != 0 {
		t.Fatalf("re-prime read: ok %v m %04x", ok, m)
	}
	jump := far
	jump.Knobs[3] = 100 + 40 // 10 detents in one read
	if _, ok := panelMatrix(jump, &k); ok {
		t.Fatal("10-detent jump accepted")
	}
	two := sramcapture.PanelFrame{Words: withBitLow(withBitLow(panelIdle, 45), 5)}
	if _, ok := panelMatrix(two, &k); !ok {
		t.Fatal("two keys down refused")
	}
}
