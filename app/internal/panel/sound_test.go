// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// soundEng is the panel's fake engine with an audio stream: two channels of
// samples (1 V/div, 40 mV a code) that grow as the test advances it, and
// the FPGA's edge marks computed from them at the level asked for (the
// comparator's re-arm below level - hysteresis, its fire at level).
// TRLC-LINKS: REQ-SDS-273
type soundEng struct {
	*fakeEng
	fs     float64
	q      [2][]uint16
	next   int
	lease  time.Time
	mark   engine.SoundMarkConfig
	marked bool
}

// TRLC-LINKS: REQ-SDS-273
func newSoundEng(fn [2]func(t float64) float64, seconds float64) *soundEng {
	e := &soundEng{fakeEng: newEng(), fs: 500e6 / 8192}
	n := int(seconds * e.fs)
	for ch := 0; ch < 2; ch++ {
		e.q[ch] = make([]uint16, n)
		for i := range e.q[ch] {
			v := fn[ch](float64(i) / e.fs)
			e.q[ch][i] = uint16(math.Max(0, math.Min(65535, math.Round(v/0.04*256)+32768)))
		}
	}
	return e
}

// TRLC-LINKS: REQ-SDS-273
func (e *soundEng) LeaseSound(d time.Duration) { e.lease = time.Now().Add(d) }

// TRLC-LINKS: REQ-SDS-273
func (e *soundEng) SoundInfo() engine.SoundInfo {
	return engine.SoundInfo{State: "streaming", Active: true, RateHz: e.fs, Next: uint64(e.next), Marks: true, MarkCfg: e.mark}
}

// TRLC-LINKS: REQ-SDS-273
func (e *soundEng) SetSoundMarks(c engine.SoundMarkConfig) { e.mark, e.marked = c, true }

// marks are the FPGA's marks on [from, to).
// TRLC-LINKS: REQ-SDS-273
func (e *soundEng) marks(from, to int) []engine.SoundMark {
	if !e.marked {
		return nil
	}
	var out []engine.SoundMark
	q := e.q[e.mark.Ch]
	lvl, low, high := int(e.mark.Level)<<8, (int(e.mark.Level)-int(e.mark.Hyst))<<8, (int(e.mark.Level)+int(e.mark.Hyst))<<8
	armR, armF := false, false
	for i := max(1, from-2000); i < to; i++ {
		v := int(q[i])
		if e.mark.Rising && armR && v >= lvl {
			if i >= from {
				out = append(out, engine.SoundMark{At: uint64(i), Ch: e.mark.Ch, Rising: true})
			}
			armR = false
		}
		if e.mark.Falling && armF && v <= lvl {
			if i >= from {
				out = append(out, engine.SoundMark{At: uint64(i), Ch: e.mark.Ch, Rising: false})
			}
			armF = false
		}
		if v < low {
			armR = true
		}
		if v > high {
			armF = true
		}
	}
	return out
}

// TRLC-LINKS: REQ-SDS-273
func (e *soundEng) SoundRead(from uint64, n int, q1, q2 []uint16) engine.SoundBlock {
	a := min(int(from), e.next)
	b := min(a+n, e.next)
	return engine.SoundBlock{First: uint64(a), N: b - a, Q1: append(q1[:0], e.q[0][a:b]...), Q2: append(q2[:0], e.q[1][a:b]...),
		RateHz: e.fs, Scales: []engine.SoundScale{{VdivV: [2]float64{1, 1}}}, Marks: e.marks(a, b), Next: uint64(e.next)}
}

// TRLC-LINKS: REQ-SDS-273
func (e *soundEng) SoundLatest(n int, q1, q2 []uint16) engine.SoundBlock {
	return e.SoundRead(uint64(max(0, e.next-n)), n, q1, q2)
}

// TRLC-LINKS: REQ-SDS-273
func (e *soundEng) SoundMarksSince(from uint64) []engine.SoundMark { return e.marks(int(from), e.next) }

// run advances the stream by d in service ticks.
// TRLC-LINKS: REQ-SDS-273
func (e *soundEng) run(c *Controller, d time.Duration, now *time.Time) {
	for t := time.Duration(0); t < d; t += soundTick {
		e.next = min(len(e.q[0]), e.next+int(soundTick.Seconds()*e.fs))
		*now = now.Add(soundTick)
		c.snd.step(*now)
	}
}

// SOUND on the LCD: F3 again to the fourth view, the tuner reads the tone
// to a fraction of a cent, the meters read both channels, the cycle trace
// comes from the FPGA's marks, Adjust turns the pages and V/oct takes a
// point; the settings survive a save and restore.
// TRLC-LINKS: REQ-SDS-273, REQ-SDS-267, REQ-SDS-266, REQ-SDS-272
func TestSoundView(t *testing.T) {
	f0 := 440 * math.Pow(2, 3.0/1200)
	e := newSoundEng([2]func(float64) float64{
		func(t float64) float64 { return 2 * math.Sin(2*math.Pi*f0*t) },
		func(t float64) float64 { return 0.5*math.Sin(2*math.Pi*1000*t) + 0.2 },
	}, 6)
	c := New(e, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	press(t, c, "f3", "f3", "f3", "f3")
	if c.viewMode != viewSound {
		t.Fatalf("F3 x4 reached view %d", c.viewMode)
	}
	v := c.View()
	if v.Sound == nil || v.Layout.PlotTop != soundPlotTop {
		t.Fatalf("no SOUND pane: %+v", v.Layout)
	}
	now := time.Now()
	e.run(c, 2*time.Second, &now)
	p := c.View().Sound
	if !p.HasPitch || p.Note != "A4" || math.Abs(p.Cents-3) > 0.3 {
		t.Fatalf("tuner %q %+.2f c (%s)", p.Note, p.Cents, p.Hz)
	}
	if !p.Meter[0].On || !p.Meter[1].On || !strings.HasPrefix(p.Meter[0].Lines[3], "Vpp 4") {
		t.Fatalf("meters %+v", p.Meter)
	}
	// 2 V peak = 1.414 V RMS = +5.2 dBu
	if math.Abs(p.Meter[0].DB-5.23) > 0.1 {
		t.Fatalf("C1 %.2f dBu", p.Meter[0].DB)
	}
	if len(p.Traces) == 0 || p.TraceWhy != "" || math.Abs(float64(p.Traces[0][0])) > 0.1 {
		t.Fatalf("cycle trace: %d traces, why %q", len(p.Traces), p.TraceWhy)
	}
	if !e.marked || e.mark.Ch != 0 || !e.mark.Rising || absI(int(e.mark.Level)-128) > 3 {
		t.Fatalf("marks %+v", e.mark)
	}
	// Adjust: the readout pages; on harmonics the tone's H1.
	turn(c, "adjust", 3)
	e.run(c, time.Second, &now)
	p = c.View().Sound
	if p.Page != "HARMONICS" || len(p.Lines) == 0 || !strings.Contains(p.Lines[0].Text, "H1 44") {
		t.Fatalf("page %s %+v", p.Page, p.Lines)
	}
	// V/oct: the push takes a point (CV on C2: its mean 0.2 V).
	turn(c, "adjust", 1)
	press(t, c, "adjustpush")
	e.run(c, 300*time.Millisecond, &now)
	if r := c.SoundReport(false, false); len(r.VOct) != 1 || math.Abs(r.VOct[0].V-0.2) > 0.01 || math.Abs(Cents(r.VOct[0].Hz, f0)) > 1 {
		t.Fatalf("V/oct %+v", r.VOct)
	}
	// Position: the cycles drawn; its push the tuned channel.
	turn(c, "horizpos", 1)
	press(t, c, "horizpospush")
	if s := c.SoundSettings(); s.Cycles != 4 || s.Ch != 1 {
		t.Fatalf("settings %+v", s)
	}
	// The settings round-trip through the settings file.
	c.SetSoundSettings(SoundSettings{A4: 432, Ch: 1, Ref: "euro", Page: 2, Cycles: 1, Gate: 0, GateV: 1.5})
	sv := c.SettingsView()
	c2, _, _ := newC(t)
	c2.ApplySettingsView(sv)
	if got := c2.SoundSettings(); got.A4 != 432 || got.Ref != "euro" || got.Page != 2 || got.Cycles != 1 || got.Gate != 0 || got.GateV != 1.5 {
		t.Fatalf("restored %+v", got)
	}
}

// Cents is the interval a/b in cents (test helper).
// TRLC-LINKS: REQ-SDS-267
func Cents(a, b float64) float64 { return 1200 * math.Log2(a/b) }

// The envelope capture: armed, the FPGA's gate edge starts it, its fall
// ends the gate, and the reading follows once the sound has died away.
// TRLC-LINKS: REQ-SDS-268
func TestSoundEnvelopeCapture(t *testing.T) {
	const on, a, d, s, off, r = 1.0, 0.05, 0.08, 0.5, 1.6, 0.15
	gate := func(t float64) float64 {
		if t >= on && t < off {
			return 5
		}
		return 0
	}
	env := func(t float64) float64 {
		switch {
		case t < on:
			return 0
		case t < on+a:
			return (t - on) / a
		case t < off:
			return s + (1-s)*math.Exp(-(t-on-a)/d)
		}
		return (s + (1-s)*math.Exp(-(off-on-a)/d)) * math.Exp(-(t-off)/r)
	}
	e := newSoundEng([2]func(float64) float64{
		func(t float64) float64 { return 3 * env(t) * math.Sin(2*math.Pi*330*t) },
		gate,
	}, 6)
	c := New(e, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.SetSoundSettings(SoundSettings{A4: 440, Ref: "dbu", Cycles: 2, Gate: 1, GateV: 1.5, Page: soundPageADSR})
	now := time.Now()
	e.run(c, 300*time.Millisecond, &now)
	if err := c.SoundAction("adsr_arm"); err != nil {
		t.Fatal(err)
	}
	e.run(c, 5*time.Second, &now)
	rep := c.SoundReport(false, false)
	if rep.ADSR != adsrDone || !rep.ADSRRes.OK {
		t.Fatalf("capture %s %s %+v", rep.ADSR, rep.ADSRWhy, rep.ADSRRes)
	}
	res := rep.ADSRRes
	if math.Abs(res.Attack1090-0.8*a) > 0.006 || math.Abs(res.SustainPct-50) > 4 || math.Abs(res.Release-r*math.Log(10)) > 0.04 {
		t.Fatalf("reading %+v", res)
	}
	if e.mark.Ch != 1 || !e.mark.Rising || !e.mark.Falling {
		t.Fatalf("gate marks %+v", e.mark)
	}
}
