// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import (
	"sync"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/hal"
	ui "github.com/labeth/open-dso-core/uimodel/panel"
)

// idle is the matrix at rest: every key up, no knob magnitude.
// TRLC-LINKS: REQ-SDS-135
func idle() [5]uint16 {
	return [5]uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF, 0}
}

// seeded is a decoder with the matrix at rest as its baseline.
// TRLC-LINKS: REQ-SDS-135
func seeded() *decoder {
	d := &decoder{}
	d.decode(idle(), true)
	return d
}

// A key press is its 1→0 edge: held it does not repeat, and released and
// pressed again it is a second press.
// TRLC-LINKS: REQ-SDS-135
func TestKeyEdges(t *testing.T) {
	d := seeded()
	m := idle()
	m[1] &^= 1 << 2 // RUN/STOP is sel 0x65 (idx 1) bit 2
	if evs := d.decode(m, true); len(evs) != 1 || evs[0].Kind != hal.KeyEvent || evs[0].Key != ui.KeyRunStop || !evs[0].Down {
		t.Fatalf("run/stop press: %+v", evs)
	}
	if evs := d.decode(m, true); len(evs) != 0 {
		t.Fatalf("held key repeated: %+v", evs)
	}
	d.decode(idle(), true)
	if evs := d.decode(m, true); len(evs) != 1 || evs[0].Key != ui.KeyRunStop {
		t.Fatalf("second press: %+v", evs)
	}
}

// The first snapshot is the baseline: nothing is fabricated from it.
// TRLC-LINKS: REQ-SDS-135
func TestFirstSnapshotIsTheBaseline(t *testing.T) {
	var d decoder
	m := idle()
	m[1] &^= 1 << 2
	if evs := d.decode(m, true); len(evs) != 0 {
		t.Fatalf("a first snapshot made events: %+v", evs)
	}
}

// Every wired position decodes to its key's name, the UI model knows every
// one, and an unwired position is a press without a name (it only wakes
// the screen).
// TRLC-LINKS: REQ-SDS-135
func TestKeyCodesAreTheModelsKeys(t *testing.T) {
	for code, name := range keyCodes {
		if code>>8&^3 != 0 || (1<<(code&0xFF))&knobPhaseMask != 0 {
			t.Errorf("code %#x: not a key position", code)
		}
		if n, ok := ui.KeyName(name); !ok || n != name {
			t.Errorf("code %#x: %q is not a key of the UI model", code, name)
		}
		d := seeded()
		m := idle()
		m[code>>8] &^= 1 << (code & 0xFF)
		if evs := d.decode(m, true); len(evs) != 1 || evs[0].Key != name {
			t.Errorf("code %#x: %+v, want %q", code, evs, name)
		}
	}
	if len(keyCodes) != 34 {
		t.Fatalf("%d wired keys", len(keyCodes))
	}
	d := seeded()
	m := idle()
	m[0] &^= 1 << 0 // a position no key is wired to
	if evs := d.decode(m, true); len(evs) != 1 || evs[0].Key != "" || !evs[0].Down {
		t.Fatalf("an unwired position: %+v", evs)
	}
}

// Exactly one knob is serviced per snapshot, the first in the FPGA's
// priority order: HORIZ POSITION wins over TIME/DIV.
// TRLC-LINKS: REQ-SDS-135
func TestKnobPriorityOneRowPerEvent(t *testing.T) {
	d := seeded()
	m := idle()
	m[3] &^= 1 << 14 // horizpos CW
	m[2] &^= 1 << 14 // tdiv CW
	m[4] = 1
	evs := d.decode(m, true)
	if len(evs) != 1 || evs[0].Kind != hal.KnobEvent || evs[0].Knob != ui.KnobHorizPos || evs[0].Steps != 1 {
		t.Fatalf("one knob, horizpos: %+v", evs)
	}
}

// A stepped knob moves one detent whatever the magnitude, and a sustained
// rotation (the bit stays low) steps again: resting-bit decode, not edge
// decode. The low bit says the direction.
// TRLC-LINKS: REQ-SDS-135
func TestSteppedKnob(t *testing.T) {
	d := seeded()
	m := idle()
	m[2] &^= 1 << 14 // TIME/DIV CW
	m[4] = 37
	if evs := d.decode(m, true); len(evs) != 1 || evs[0].Knob != ui.KnobTdiv || evs[0].Steps != 1 {
		t.Fatalf("tdiv CW: %+v", evs)
	}
	if evs := d.decode(m, true); len(evs) != 1 || evs[0].Steps != 1 {
		t.Fatalf("sustained rotation missed: %+v", evs)
	}
	m = idle()
	m[1] &^= 1 << 15 // CH1 V/DIV CCW
	m[4] = 1
	if evs := d.decode(m, true); len(evs) != 1 || evs[0].Knob != ui.KnobCh1Vdiv || evs[0].Steps != -1 {
		t.Fatalf("ch1 vdiv CCW: %+v", evs)
	}
}

// A continuous knob's steps are the accelerated magnitude.
// TRLC-LINKS: REQ-SDS-135
func TestContinuousKnobAccel(t *testing.T) {
	d := seeded()
	m := idle()
	m[0] &^= 1 << 6 // CH1 POSITION CW
	m[4] = 25
	if evs := d.decode(m, true); len(evs) != 1 || evs[0].Knob != ui.KnobCh1Pos || evs[0].Steps != 100 {
		t.Fatalf("ch1 pos accel: %+v", evs)
	}
}

// TRLC-LINKS: REQ-SDS-135
func TestAccelMap(t *testing.T) {
	cases := map[uint16]int{0: 0, 5: 5, 9: 9, 10: 50, 19: 50, 20: 100, 150: 100, 1000: 100}
	for raw, want := range cases {
		if got := accel(raw); got != want {
			t.Errorf("accel(%d) = %d, want %d", raw, got, want)
		}
	}
}

// A phase bit low with a zero magnitude is a plain key interrupt: no knob.
// TRLC-LINKS: REQ-SDS-135
func TestKnobGateOnZeroMagnitude(t *testing.T) {
	d := seeded()
	m := idle()
	m[2] &^= 1 << 14
	if evs := d.decode(m, true); len(evs) != 0 {
		t.Fatalf("knob moved with zero magnitude: %+v", evs)
	}
}

// A keys-only read (the 40 ms tick) decodes no knob.
// TRLC-LINKS: REQ-SDS-135
func TestResyncButtonsOnly(t *testing.T) {
	d := seeded()
	m := idle()
	m[2] &^= 1 << 14
	m[4] = 3
	if evs := d.decode(m, false); len(evs) != 0 {
		t.Fatalf("knob decoded on the re-sync tick: %+v", evs)
	}
}

// matrixEng is the engine's matrix and lamp latch.
type matrixEng struct {
	mu   sync.Mutex
	m    [5]uint16
	leds []uint16
}

// TRLC-LINKS: REQ-SDS-135
func (e *matrixEng) ReadMatrix() ([5]uint16, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.m, true
}

// TRLC-LINKS: REQ-SDS-135
func (e *matrixEng) SetLEDs(w uint16) {
	e.mu.Lock()
	e.leds = append(e.leds, w)
	e.mu.Unlock()
}

// The lamps latch as the UI model's lamp word, once per change; a lamp the
// latch has no bit for (the trigger mode's) lights nothing.
// TRLC-LINKS: REQ-SDS-135
func TestLampsLatchTheWord(t *testing.T) {
	e := &matrixEng{m: idle()}
	in := NewInput(e, -1, t.Logf)
	_ = in.SetLamps([]string{"ch1", "run", "auto"})
	_ = in.SetLamps([]string{"ch1", "run", "normal"})
	_ = in.SetLamps([]string{"ch1", "stop", "single"})
	if len(e.leds) != 2 || e.leds[0] != 0x2010 || e.leds[1] != 0xC010 {
		t.Fatalf("latched %#04x", e.leds)
	}
}

// Without SIGIO the matrix is polled on the tick, knobs included: a press
// and a turn reach Events.
// TRLC-LINKS: REQ-SDS-135
func TestPolledInputReachesEvents(t *testing.T) {
	e := &matrixEng{m: idle()}
	in := NewInput(e, -1, t.Logf)
	stop := make(chan struct{})
	defer close(stop)
	go in.Run(stop)
	time.Sleep(60 * time.Millisecond) // the baseline
	e.mu.Lock()
	e.m[1] &^= 1 << 10 // SINGLE
	e.m[0] &^= 1 << 14 // TRIG LEVEL CW
	e.m[4] = 2
	e.mu.Unlock()
	var got []hal.InputEvent
	for len(got) < 2 {
		select {
		case ev := <-in.Events():
			got = append(got, ev)
		case <-time.After(2 * time.Second):
			t.Fatalf("events %+v", got)
		}
	}
	if got[0].Key != ui.KeySingle || got[1].Knob != ui.KnobTrigLevel || got[1].Steps != 2 {
		t.Fatalf("events %+v", got)
	}
}
