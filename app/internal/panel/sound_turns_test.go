// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// countEng counts the stream reads of each length the service asks for.
type countEng struct {
	*soundEng
	reads map[int]int
}

// TRLC-LINKS: REQ-SDS-273
func (e *countEng) SoundLatest(n int, q1, q2 []uint16) engine.SoundBlock {
	e.reads[n]++
	return e.soundEng.SoundLatest(n, q1, q2)
}

// The LCD's SOUND view reads the tuner and the meters on alternate service
// ticks, each four times a second: both at every tick kept the core so busy
// that a Listening page's audio ran dry.
// TRLC-LINKS: REQ-SDS-273, REQ-SDS-267, REQ-SDS-266
func TestSoundTunerAndMetersTakeTurns(t *testing.T) {
	se := newSoundEng([2]func(float64) float64{
		func(t float64) float64 { return 2 * math.Sin(2*math.Pi*440*t) },
		func(t float64) float64 { return 0.5 * math.Sin(2*math.Pi*1000*t) },
	}, 4)
	e := &countEng{soundEng: se, reads: map[int]int{}}
	c := New(e, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.snd.setLCD(true)
	now := time.Now()
	se.next = int(se.fs)
	for i := 0; i < 8; i++ {
		se.next += int(soundTick.Seconds() * se.fs)
		now = now.Add(soundTick)
		c.snd.step(now)
	}
	tuner := 0
	for n, k := range e.reads {
		if n >= 4096 && n <= 32768 && n&(n-1) == 0 {
			tuner += k
		}
	}
	if tuner != 4 {
		t.Fatalf("8 ticks read the tuner's window %d times, want 4 (%v)", tuner, e.reads)
	}
}

// The modulation page reads its four-second window once a window: every
// two seconds it took a tenth of the core beside a Listening page.
// TRLC-LINKS: REQ-SDS-269, REQ-SDS-273
func TestSoundModulationOnceAWindow(t *testing.T) {
	se := newSoundEng([2]func(float64) float64{
		func(t float64) float64 { return 2 * math.Sin(2*math.Pi*440*t) * (1 + 0.2*math.Sin(2*math.Pi*5*t)) },
		func(t float64) float64 { return 0 },
	}, 14)
	e := &countEng{soundEng: se, reads: map[int]int{}}
	c := New(e, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.snd.setLCD(true)
	c.SetSoundSettings(SoundSettings{A4: 440, Page: soundPageMod, Cycles: 2, Gate: -1})
	now := time.Now()
	se.next = int(4.5 * se.fs)
	for d := time.Duration(0); d < 8*time.Second; d += soundTick {
		se.next += int(soundTick.Seconds() * se.fs)
		now = now.Add(soundTick)
		c.snd.step(now)
	}
	if n := e.reads[int(4*se.fs)]; n != 2 {
		t.Fatalf("8 s read the modulation window %d times, want 2", n)
	}
}
