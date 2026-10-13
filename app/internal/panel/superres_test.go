// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/superres"
)

// An FPGA stack reviews at the session's timebase: one screen of samples with
// the stack's edge at the trigger position (bug hunt 2026-10-10: its 32
// samples were stretched across a 100 ns/div screen).
// TRLC-LINKS: REQ-SDS-141, REQ-SDS-021
func TestFPGAReviewSpanIsTrueTimebase(t *testing.T) {
	if gate, screen := fpgaReviewSpan(32, 16, 100e-9, 2e-9, 0.5); screen != 500 || gate != 250-16 {
		t.Fatalf("gate %d screen %d, want 500 samples with the edge (16 in) at 250", gate, screen)
	}
	if gate, screen := fpgaReviewSpan(32, 16, 10e-9, 2e-9, 0.2); screen != 50 || gate != 10-16 {
		t.Fatalf("10 ns/div at 20%%: gate %d screen %d", gate, screen)
	}
}

// leaseEng counts raw-frame leases.
type leaseEng struct {
	*fakeEng
	n atomic.Int32
}

func (e *leaseEng) LeaseDecodeView(time.Duration) { e.n.Add(1) }

// A final stack (here: the timebase changed under it) stops leasing raw
// frames; the renewed lease held the live view at 1 fps at 200 us/div for as
// long as super-res stayed armed (bug hunt 2026-10-10).
// TRLC-LINKS: REQ-SDS-140
func TestSuperresFinalStackReleasesRawLease(t *testing.T) {
	_, base, fe := newC(t)
	eng := &leaseEng{fakeEng: base}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	var seq uint64
	c.frameFn = func(fn func(*engine.Frame)) {
		seq++
		w := make([]uint8, 256)
		fn(&engine.Frame{C1: w, C2: w, Valid: len(w), SampleS: 2e-9, Seq: seq})
	}
	st := superres.New(16, 4)
	st.SampleS = 1e-9 // stacked at another timebase
	stop := make(chan struct{})
	c.mu.Lock()
	c.srActive, c.srStack, c.srStop = true, st, stop
	c.mu.Unlock()
	go c.srLoop(stop, st, 0)
	defer close(stop)
	time.Sleep(150 * time.Millisecond)
	settled := eng.n.Load()
	time.Sleep(200 * time.Millisecond)
	if more := eng.n.Load() - settled; more > 0 {
		t.Fatalf("final stack renewed the raw lease %d more times", more)
	}
}

// Raw SRAM frames keep their pitch at every timebase: a timebase change
// freezes the stack (and its lease) by TdivS.
// TRLC-LINKS: REQ-SDS-140
func TestSuperresTimebaseChangeFreezesStack(t *testing.T) {
	_, base, fe := newC(t)
	eng := &leaseEng{fakeEng: base}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	var seq uint64
	c.frameFn = func(fn func(*engine.Frame)) {
		seq++
		w := make([]uint8, 256)
		fn(&engine.Frame{C1: w, C2: w, Valid: len(w), SampleS: 2e-9, TdivS: 200e-6, Seq: seq})
	}
	st := superres.New(16, 4)
	st.SampleS = 2e-9 // same pitch: only the timebase moved
	stop := make(chan struct{})
	c.mu.Lock()
	c.srActive, c.srStack, c.srStop = true, st, stop
	c.mu.Unlock()
	go c.srLoop(stop, st, 100e-9)
	defer close(stop)
	time.Sleep(150 * time.Millisecond)
	settled := eng.n.Load()
	time.Sleep(200 * time.Millisecond)
	c.mu.Lock()
	status := c.srStatus
	c.mu.Unlock()
	if more := eng.n.Load() - settled; more > 0 || !strings.Contains(status, "timebase changed") {
		t.Fatalf("status %q, %d more leases after the timebase changed", status, more)
	}
}

// SINGLE then UTILITY: the stopped full record is far too deep to stack in
// software, so the arm resumes and seeds from a live raw frame (bug hunt #2:
// it always failed with "window too deep" on the SRAM band).
// TRLC-LINKS: REQ-SDS-140
func TestSuperresArmFromStoppedDeepRecord(t *testing.T) {
	c, eng, _ := newC(t)
	eng.SetRunning(false)
	wave := func(n int) []uint8 {
		w := make([]uint8, n)
		for i := range w {
			w[i] = 60
			if (i/64)%2 == 1 {
				w[i] = 190
			}
		}
		return w
	}
	deep, live := wave(1<<16), wave(1024)
	c.frameFn = func(fn func(*engine.Frame)) {
		if !eng.Snapshot().Running {
			fn(&engine.Frame{C1: deep, C2: deep, Valid: len(deep), EdgeX: 32768, SampleS: 2e-9, Seq: 1})
			return
		}
		fn(&engine.Frame{C1: live, C2: live, Valid: len(live), EdgeX: 512, SampleS: 2e-9, Seq: 2})
	}
	c.srArm()
	defer c.srCancel("")
	c.mu.Lock()
	active, status, n := c.srActive, c.srStatus, 0
	if c.srStack != nil {
		n = c.srStack.N
	}
	c.mu.Unlock()
	if !active || n != len(live) {
		t.Fatalf("arm from a stopped deep record: active=%v N=%d status %q", active, n, status)
	}
}

// With the panel loop running, Super-res seeds off it: On returns at once with
// the mode reading On, the stack lands when the seed is done, and Off while it
// seeds discards it (bug hunt #3: On held every key 2-4 s on the scope, each
// gate edge's knob detent 1-2 s).
// TRLC-LINKS: REQ-SDS-140
func TestSuperresSeedsOffThePanelLoop(t *testing.T) {
	c, eng, _ := newC(t)
	eng.SetRunning(true)
	fr := smallFrame()
	fr.SampleS = 1e-9
	hold := make(chan struct{})
	var calls atomic.Int32
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		if calls.Add(1) == 2 { // the seed's first look waits here
			<-hold
		}
		fn(fr)
	})
	c.looping.Store(true)
	defer c.looping.Store(false)
	idle := func() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.srSeederOn }
	wait := func(ok func() bool) {
		for deadline := time.Now().Add(3 * time.Second); !ok(); {
			if time.Now().After(deadline) {
				t.Fatal("the seeder did not finish")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	start := time.Now()
	if err := c.srArm(); err != nil || time.Since(start) > 200*time.Millisecond || rowText(c, "acq.superres") != "On" {
		t.Fatalf("On: %v after %v, the row %q", err, time.Since(start), rowText(c, "acq.superres"))
	}
	if s := c.SuperresView().Status; s != "finding the reference..." { // not the last session's status
		t.Fatalf("status while the seed runs: %q", s)
	}
	c.srCancel("cancelled") // Off while the seed waits
	close(hold)
	wait(idle)
	if c.SuperresView().Active || rowText(c, "acq.superres") != "Off" {
		t.Fatalf("a seed cancelled in flight armed super-res: active %v row %q", c.SuperresView().Active, rowText(c, "acq.superres"))
	}
	if err := c.srArm(); err != nil {
		t.Fatal(err)
	}
	defer c.srCancel("")
	wait(func() bool { return idle() && c.SuperresView().Active })
}

// A Stop pressed while a seed runs off the panel goroutine stands: the seed's
// end resumed RUN over it (bug hunt #3: the ACQ Run row set to Stop just
// after Super-res On read Run again); with no press meanwhile it resumes.
// TRLC-LINKS: REQ-SDS-140
func TestSuperresSeedKeepsAStopPressedMeanwhile(t *testing.T) {
	for _, press := range []bool{true, false} {
		c, eng, _ := newC(t)
		eng.SetRunning(true)
		fr := smallFrame()
		fr.SampleS = 1e-9
		hold := make(chan struct{})
		var calls atomic.Int32
		c.SetFrameSource(func(fn func(*engine.Frame)) {
			if calls.Add(1) == 2 { // the seed's first look waits here
				<-hold
			}
			fn(fr)
		})
		c.looping.Store(true)
		if err := c.srArm(); err != nil {
			t.Fatal(err)
		}
		if press {
			if err := c.setRun(1); err != nil { // Stop while the seed runs
				t.Fatal(err)
			}
		} else {
			eng.SetRunning(false) // stopped, but not by a press since On
		}
		close(hold)
		for deadline := time.Now().Add(3 * time.Second); !c.SuperresView().Active; {
			if time.Now().After(deadline) {
				t.Fatal("the seed did not finish")
			}
			time.Sleep(5 * time.Millisecond)
		}
		if running := eng.Snapshot().Running; running == press {
			t.Fatalf("Stop pressed meanwhile %v: running %v after the seed", press, running)
		}
		c.srCancel("")
		c.looping.Store(false)
	}
}
