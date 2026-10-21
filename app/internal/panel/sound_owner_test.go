// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/settings"
)

// lcdPress runs keys as the LCD's own (a physical press or /api/panel).
// TRLC-LINKS: REQ-SDS-277
func lcdPress(t *testing.T, c *Controller, names ...string) {
	t.Helper()
	for _, n := range names {
		code, ok := nameCode(n)
		if !ok {
			t.Fatalf("no button %q", n)
		}
		c.lcdButton(code)
	}
}

// leaseActive reports the fake stream's lease running now.
// TRLC-LINKS: REQ-SDS-277
func (e *soundEng) leaseActive() bool { return time.Now().Before(e.lease) }

// The drift log ends when SOUND is left on the LCD and on every page, and a
// saved setup never brings it back on: on, it held the audio stream from
// every workspace, so the scope captured nothing even after a restart.
// TRLC-LINKS: REQ-SDS-276
func TestSoundDriftEndsWithSound(t *testing.T) {
	tone := func(t float64) float64 { return 0 }
	e := newSoundEng([2]func(float64) float64{tone, tone}, 30)
	c := New(e, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)

	// Power-on with a setup that kept Drift on: it comes back off.
	sv := c.SettingsView()
	sv.Sound.Drift = true
	c.ApplySettingsView(sv)
	if c.SoundSettings().Drift {
		t.Fatal("a saved Drift came back on")
	}
	if got := soundSettingsIn(settings.Sound{Set: true, A4: 440, Drift: true}); got.Drift {
		t.Fatal("soundSettingsIn kept Drift on")
	}

	// On in the LCD's SOUND view, it runs (and leases) while the view shows.
	press(t, c, "f3", "f3", "f3", "f3")
	c.View()
	c.SetSoundSettings(SoundSettings{A4: 440, Ref: "dbu", Cycles: 2, Gate: -1, GateV: 0.1, Drift: true})
	now := time.Now()
	e.run(c, time.Second, &now)
	if !c.SoundSettings().Drift || !e.leaseActive() {
		t.Fatalf("Drift on in SOUND: on %v, leased %v", c.SoundSettings().Drift, e.leaseActive())
	}
	// SOUND left (F3 again: the FFT view): Drift ends, nothing leases.
	lcdPress(t, c, "f3")
	c.View()
	e.run(c, time.Second, &now)
	if c.SoundSettings().Drift {
		t.Fatal("Drift stayed on after the LCD left SOUND")
	}
	if e.leaseActive() {
		t.Fatal("the stream stayed leased after SOUND was left")
	}

	// On from a page: it runs while the page shows SOUND, ends once the
	// page goes.
	c.SoundRemoteLease("", nil)
	c.SetSoundSettings(SoundSettings{A4: 440, Ref: "dbu", Cycles: 2, Gate: -1, GateV: 0.1, Drift: true})
	c.snd.step(time.Now())
	if !c.SoundSettings().Drift {
		t.Fatal("Drift ended while a page shows SOUND")
	}
	c.snd.step(time.Now().Add(soundPageFor + time.Second))
	if c.SoundSettings().Drift {
		t.Fatal("Drift stayed on after the page left SOUND")
	}
}

// The last one to act wins: a page's SOUND holds the stream (the LCD names
// it), a key on the LCD outside SOUND takes the acquisition back (the lease
// ends, the page's next requests are refused and the page reads paused),
// the page's "take_scope" holds it again; on the LCD's own SOUND view the
// page shares the stream.
// TRLC-LINKS: REQ-SDS-277
func TestSoundLCDTakesTheScopeBack(t *testing.T) {
	quiet := func(float64) float64 { return 0 }
	e := newSoundEng([2]func(float64) float64{quiet, quiet}, 1)
	c := New(e, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	lease := func() { e.LeaseSound(3 * time.Second) }

	if !c.SoundRemoteLease("", lease) || !e.leaseActive() {
		t.Fatal("a page's SOUND did not hold the stream")
	}
	if n := c.View().Notice; n != soundRemoteNotice {
		t.Fatalf("the LCD's notice while a web page streams: %q", n)
	}
	c.SoundRemoteLease("phone", lease)
	if n := c.View().Notice; n != soundPhoneNotice {
		t.Fatalf("the LCD's notice while the phone streams: %q", n)
	}

	lcdPress(t, c, "set50")
	if e.leaseActive() {
		t.Fatal("an LCD key left the page's lease running")
	}
	if c.SoundRemoteLease("", lease) || e.leaseActive() {
		t.Fatal("a page re-leased the stream after the LCD took it back")
	}
	if r := c.SoundReport(false, false); !r.Paused || r.PausedWhy != SoundPausedText {
		t.Fatalf("the page's report: paused %v %q", r.Paused, r.PausedWhy)
	}
	if n := c.View().Notice; n == soundRemoteNotice {
		t.Fatal("the LCD still says a page holds the scope")
	}
	// A page's wants do not run the service while paused.
	c.SoundWant(true, true)
	c.snd.step(time.Now())
	if e.leaseActive() {
		t.Fatal("a paused page's readouts leased the stream")
	}

	if err := c.SoundAction("take_scope"); err != nil {
		t.Fatal(err)
	}
	if !c.SoundRemoteLease("", lease) || !e.leaseActive() || c.SoundReport(false, false).Paused {
		t.Fatal("take_scope did not give the page the stream back")
	}
	// A knob turned on the LCD takes it back as a key does.
	c.lcdKnob("tdiv", 1, 1)
	if e.leaseActive() || !c.SoundPaused() {
		t.Fatal("an LCD knob left the page's lease running")
	}

	// On the LCD's own SOUND view the stream runs for it: a page shares it.
	lcdPress(t, c, "f3", "f3", "f3", "f3")
	c.View()
	if !c.SoundRemoteLease("", lease) || c.SoundPaused() {
		t.Fatalf("a page was refused while the LCD shows SOUND (ws %v view %d)", c.ui.ws, c.viewMode)
	}
	if n := c.View().Notice; n == soundRemoteNotice {
		t.Fatal("the notice shows on the LCD's own SOUND view")
	}
	// Leaving SOUND on the LCD takes the acquisition with it.
	lcdPress(t, c, "f3")
	c.View()
	if e.leaseActive() || c.SoundRemoteLease("", lease) {
		t.Fatal("leaving SOUND on the LCD left the page streaming")
	}
}

// acqSoundEng is an engine whose captures stop while the audio stream is
// leased (the stream takes the fabric): SoundInfo reads active then.
type acqSoundEng struct {
	*fakeEng
	mu    sync.Mutex
	until time.Time
}

// TRLC-LINKS: REQ-SDS-277
func (e *acqSoundEng) LeaseSound(d time.Duration) {
	e.mu.Lock()
	e.until = time.Now().Add(d)
	e.mu.Unlock()
}

// TRLC-LINKS: REQ-SDS-277
func (e *acqSoundEng) leased() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return time.Now().Before(e.until)
}

// TRLC-LINKS: REQ-SDS-277
func (e *acqSoundEng) SoundInfo() engine.SoundInfo {
	return engine.SoundInfo{Active: e.leased(), State: "streaming"}
}

// AUTO on the LCD while a page streams SOUND takes the acquisition back
// first: the lease ends before the sweep, a page polling on is refused, and
// the sweep reads real captures (it searched an empty stream for 25 s and
// said "no signal").
// TRLC-LINKS: REQ-SDS-277, REQ-SDS-137
func TestSoundAutoTakesTheScopeFirst(t *testing.T) {
	c, fe0, _ := newC(t)
	e := &acqSoundEng{fakeEng: fe0}
	c.eng = e
	sig := make([]uint8, 200)
	for i := range sig {
		sig[i] = 40
		if i%20 >= 10 {
			sig[i] = 200
		}
	}
	var mu sync.Mutex
	blind, seen := 0, 0
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		mu.Lock()
		if e.leased() {
			blind++
			mu.Unlock()
			fn(nil) // the stream holds the fabric: no capture
			return
		}
		seen++
		mu.Unlock()
		fn(&engine.Frame{C1: sig, C2: sig, Valid: len(sig), SampleS: 50e-9})
	})
	lease := func() { e.LeaseSound(3 * time.Second) }
	if !c.SoundRemoteLease("", lease) {
		t.Fatal("the page did not hold the stream")
	}
	// The page polls on through the AUTO.
	stop := make(chan struct{})
	refused := make(chan int, 1)
	go func() {
		n := 0
		for {
			select {
			case <-stop:
				refused <- n
				return
			case <-time.After(20 * time.Millisecond):
				if !c.SoundRemoteLease("", lease) {
					n++
				}
			}
		}
	}()
	lcdPress(t, c, "auto")
	end := time.Now().Add(60 * time.Second)
	for c.AutosetBusy() && time.Now().Before(end) {
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	n := <-refused
	mu.Lock()
	defer mu.Unlock()
	if blind != 0 || seen == 0 {
		t.Fatalf("the sweep read %d captures while the stream held the fabric, %d real ones", blind, seen)
	}
	if n == 0 || e.leased() {
		t.Fatalf("the page's polls were not refused (%d refused, leased %v)", n, e.leased())
	}
}
