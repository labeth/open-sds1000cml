// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// takeEng is soundEng with the engine's takes: STOPped (mode) it shows a
// take of the samples held, a trigger two seconds in.
// TRLC-LINKS: REQ-SDS-275
type takeEng struct {
	*soundEng
	mode      string
	take      *engine.SoundTake
	pre, post float64
	gate      *engine.SoundMarkConfig
}

// TRLC-LINKS: REQ-SDS-275
func (e *takeEng) SoundInfo() engine.SoundInfo {
	in := e.soundEng.SoundInfo()
	in.Mode = e.mode
	if e.take != nil {
		in.Take = e.take.ID
	}
	if e.mode == "stopped" {
		in.Active = false
	}
	return in
}

// TRLC-LINKS: REQ-SDS-275
func (e *takeEng) SoundTake() *engine.SoundTake { return e.take }

// TRLC-LINKS: REQ-SDS-275
func (e *takeEng) SetSoundTakeTimes(pre, post float64) { e.pre, e.post = pre, post }

// TRLC-LINKS: REQ-SDS-275
func (e *takeEng) SetSoundTrigGate(cfg engine.SoundMarkConfig, on bool) {
	e.gate = nil
	if on {
		e.gate = &cfg
	}
}

// A take on the LCD: STOPped, the readouts read the take's window (the
// tuner the tone in it, the meters over the window), the plot shows both
// channels with the trigger, S/DIV zooms about the centre, Position
// scrolls, its push brings the trigger to the centre; armed, Position moves
// the trigger within the take; the take's settings reach the engine.
// TRLC-LINKS: REQ-SDS-275
func TestSoundTakeView(t *testing.T) {
	f1 := 146.83 // D3, from 2 s on
	se := newSoundEng([2]func(float64) float64{
		func(t float64) float64 {
			if t < 2 {
				return 0.01 * math.Sin(2*math.Pi*50*t)
			}
			return 1.5 * math.Sin(2*math.Pi*f1*t)
		},
		func(t float64) float64 { return 0.3 * math.Sin(2*math.Pi*1000*t) },
	}, 5)
	e := &takeEng{soundEng: se, mode: "live"}
	c := New(e, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	press(t, c, "f3", "f3", "f3", "f3")
	now := time.Now()
	se.run(c, 5*time.Second, &now)
	if e.pre != 1 || e.post != 4 {
		t.Fatalf("take times %v %v", e.pre, e.post)
	}
	// STOP: the take, a trigger at the bass's onset.
	b := se.SoundRead(0, se.next, nil, nil)
	e.take = &engine.SoundTake{SoundBlock: b, ID: 1, Why: "single", HasTrig: true, Trig: uint64(2 * se.fs), TrigRising: true, TrigLevel: 140}
	e.mode = "stopped"
	se.run(c, 500*time.Millisecond, &now)
	p := c.View().Sound
	if p.Take == nil || !strings.HasPrefix(p.Take.Title, "SINGLE take 5.0 s") || math.Abs(p.Take.TrigX-0.4) > 0.01 {
		t.Fatalf("take pane %+v", p.Take)
	}
	if !strings.Contains(p.Caption, "STOP") || len(p.Take.Lo[0]) != soundTakeWidth || p.Take.VHi[0] < 1.5 || p.Take.VHi[1] > 0.5 {
		t.Fatalf("caption %q, columns %d, ranges %v %v", p.Caption, len(p.Take.Lo[0]), p.Take.VLo, p.Take.VHi)
	}
	// Zoom in about the centre (2.5 s: the bass) and read it.
	for i := 0; i < 8; i++ {
		turn(c, "tdiv", 1)
	}
	se.run(c, 300*time.Millisecond, &now)
	p = c.View().Sound
	if p.Note != "D3" || p.Take.From < 0.45 || p.Take.To > 0.55 {
		t.Fatalf("zoomed: note %q, window %.3f..%.3f", p.Note, p.Take.From, p.Take.To)
	}
	// Position scrolls a tenth of the window a detent.
	from := p.Take.From
	turn(c, "horizpos", 5)
	se.run(c, 300*time.Millisecond, &now)
	if p = c.View().Sound; math.Abs((p.Take.From-from)-(p.Take.To-p.Take.From)/2) > 0.002 {
		t.Fatalf("scroll %.4f -> %.4f", from, p.Take.From)
	}
	// Its push: the trigger to the centre; again: all of the take.
	press(t, c, "horizpospush")
	if p = c.View().Sound; math.Abs(p.Take.TrigX-0.5) > 0.01 {
		t.Fatalf("trigger at %.3f", p.Take.TrigX)
	}
	press(t, c, "horizpospush")
	if p = c.View().Sound; p.Take.From != 0 || p.Take.To != 1 {
		t.Fatalf("all: %.3f..%.3f", p.Take.From, p.Take.To)
	}
	// Armed: Position moves the trigger within the take, the sum kept.
	e.mode, e.take = "armed", nil
	se.run(c, 300*time.Millisecond, &now)
	turn(c, "horizpos", 2)
	if s := c.SoundSettings(); math.Abs(s.PreS-1.5) > 1e-9 || math.Abs(s.PostS-3.5) > 1e-9 || e.pre != 1.5 {
		t.Fatalf("pre %v post %v (engine %v)", s.PreS, s.PostS, e.pre)
	}
	// The gate as the trigger reaches the engine with its level.
	c.SetSoundSettings(SoundSettings{A4: 440, Ref: "dbu", Cycles: 2, Gate: 1, GateV: 0.2, PreS: 1, PostS: 4, TrigGate: true})
	se.run(c, 300*time.Millisecond, &now)
	if e.gate == nil || e.gate.Ch != 1 || !e.gate.Rising || absI(int(e.gate.Level)-133) > 1 {
		t.Fatalf("gate %+v", e.gate)
	}
}
