// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/settings"
)

// fastUART is the rig's bus: a 460800 Bd UART; otherBus a 115200 Bd one the
// probes moved to.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func fastUART() *engine.Frame {
	return uartFrame([]string{"Hi!\r\n", "OK\r\n"}, 460800, 100e-6, 100e-9, 20000)
}

// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func otherBus() *engine.Frame {
	return uartFrame([]string{"slower bus\r\n"}, 115200, 60e-6, 100e-9, 20000)
}

// autoRig is a view rig whose decoder settings Auto filled in from fastUART.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func autoRig(t *testing.T) *viewRig {
	t.Helper()
	r := newViewRig(t)
	r.show(fastUART())
	set(t, r.c, "bus.protocol", "Auto")
	if s := r.c.snap(); s.decProto != framecalc.ProtoUART || s.decBaud != 460800 || !s.decAuto {
		t.Fatalf("Auto filled in protocol %d baud %d (filled %v)", s.decProto, s.decBaud, s.decAuto)
	}
	if !r.eng.Snapshot().Running {
		t.Fatal("the rig's scope is stopped: no capture would be judged")
	}
	return r
}

// waitBaud waits for the decoder settings' rate to become want.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func waitBaud(t *testing.T, c *Controller, want int) bool {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if s := c.snap(); s.decBaud == want && !s.ui.busDetecting {
			return true
		}
	}
	return false
}

// When the bus Auto filled in stops decoding for about two seconds of
// captures (the probes moved to another bus), Auto detects again by itself,
// once, and fills in the new bus.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func TestAutoDetectsAgainWhenTheBusIsLost(t *testing.T) {
	r := autoRig(t)
	r.show(fastUART()) // decodes: the watch is armed
	for i := 0; i < autoLostCaptures; i++ {
		r.clk.advance(autoLostAfter)
		r.show(otherBus())
	}
	if !waitBaud(t, r.c, 115200) {
		s := r.c.snap()
		t.Fatalf("after the bus was lost: protocol %d baud %d, notice %q", s.decProto, s.decBaud, r.c.View().Notice)
	}
	if s := r.c.snap(); s.decProto != framecalc.ProtoUART || !s.decAuto {
		t.Fatalf("the new bus: protocol %d filled %v", s.decProto, s.decAuto)
	}
}

// One bad capture, however long it stands, does not move the settings: the
// next capture that decodes holds them.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func TestAutoKeepsTheBusOverABadCapture(t *testing.T) {
	r := autoRig(t)
	r.show(fastUART())
	r.clk.advance(10 * time.Second)
	r.show(otherBus())
	r.clk.advance(time.Second)
	r.show(fastUART())
	for i := 0; i < autoLostCaptures-1; i++ {
		r.clk.advance(autoLostAfter)
		r.show(otherBus())
	}
	time.Sleep(100 * time.Millisecond) // a detection would run off the frame path
	if s := r.c.snap(); s.decBaud != 460800 || s.ui.busDetecting || strings.Contains(r.c.View().Notice, "detecting the bus again") {
		t.Fatalf("a bad capture moved the bus: baud %d, notice %q", s.decBaud, r.c.View().Notice)
	}
}

// Settings set by hand are used as they are: losing their bus detects
// nothing.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func TestHandSetSettingsAreNotRedetected(t *testing.T) {
	r := autoRig(t)
	set(t, r.c, "bus.baud", "460800")
	if r.c.snap().decAuto {
		t.Fatal("a rate set by hand still reads as Auto's")
	}
	r.show(fastUART())
	for i := 0; i < 2*autoLostCaptures; i++ {
		r.clk.advance(autoLostAfter)
		r.show(otherBus())
	}
	time.Sleep(100 * time.Millisecond)
	if s := r.c.snap(); s.decBaud != 460800 || s.ui.busDetecting {
		t.Fatalf("hand-set settings were replaced: baud %d", s.decBaud)
	}
}

// A setup saved while Auto was a decoding mode holds Protocol = Auto: it
// loads as one detection that fills in the settings, never an error, and
// is saved again with the protocol found.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-214
func TestSavedAutoLoadsAsOneDetection(t *testing.T) {
	r := newViewRig(t)
	r.show(fastUART())
	r.c.ApplySettingsView(settings.ViewState{Decode: settings.Decode{Proto: framecalc.ProtoAuto, Baud: 115200, ChB: 1}})
	s := r.c.snap()
	if s.decProto != framecalc.ProtoUART || s.decBaud != 460800 || !s.decAuto {
		t.Fatalf("loaded Auto: protocol %d baud %d filled %v", s.decProto, s.decBaud, s.decAuto)
	}
	if d := r.c.SettingsView().Decode; d.Proto != framecalc.ProtoUART || d.Baud != 460800 {
		t.Fatalf("saved again as %+v", d)
	}
}

// Auto again forces a new detection, and its F-key never stays lit: the
// row reads the protocol found.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func TestAutoAgainDetectsAgain(t *testing.T) {
	r := autoRig(t)
	r.show(otherBus())
	set(t, r.c, "bus.protocol", "Auto")
	if s := r.c.snap(); s.decBaud != 115200 || rowText(r.c, "bus.protocol") != "UART" {
		t.Fatalf("Auto again: baud %d row %q", s.decBaud, rowText(r.c, "bus.protocol"))
	}
}

// A bit rate a detection measured a little off fills in as the rate the bus
// runs at (Manchester at 998 kb/s on one record is 1 Mb/s); one off every
// standard rate stands.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func TestFilledInBitRateIsTheStandardOne(t *testing.T) {
	for in, want := range map[int]int{998000: 1000000, 1004000: 1000000, 509000: 500000, 10002156: 10000000, 1490000: 1500000, 777000: 777000} {
		if got := standardRate(in); got != want {
			t.Fatalf("%d filled in as %d, want %d", in, got, want)
		}
	}
}

// Lines with no edges (a probe off, a pause in the traffic) say nothing of
// the bus: however long they last, nothing is detected, and the bus that
// comes after them is detected once it fails to decode.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func TestIdleLinesDoNotDetect(t *testing.T) {
	r := autoRig(t)
	r.show(fastUART())
	flat := make([]uint8, 20000)
	for i := range flat {
		flat[i] = 210
	}
	for i := 0; i < 2*autoLostCaptures; i++ {
		r.clk.advance(autoLostAfter)
		r.show(&engine.Frame{C1: flat, C2: flat, Valid: len(flat), WinCols: len(flat), SampleS: 100e-9})
	}
	time.Sleep(100 * time.Millisecond)
	if s := r.c.snap(); s.decBaud != 460800 || s.ui.busDetecting || strings.Contains(r.c.View().Notice, "detecting the bus again") {
		t.Fatalf("idle lines started a detection: baud %d, notice %q", s.decBaud, r.c.View().Notice)
	}
	for i := 0; i < autoLostCaptures; i++ {
		r.clk.advance(time.Second)
		r.show(otherBus())
	}
	if !waitBaud(t, r.c, 115200) {
		t.Fatalf("the bus after the idle lines: baud %d, notice %q", r.c.snap().decBaud, r.c.View().Notice)
	}
}
