// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"testing"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
)

// TestRailing pins the semantics the coarsen-on-rail guard (autoset step 3c)
// depends on: a channel counts as "railing" only when MORE than ~5% of its valid
// samples sit at an extreme code (≤1 or ≥254). This is the model-independent
// signal that a DC-heavy trace is parked off-screen on a range where the offset
// DAC can't pull it in, so autoset must coarsen the V/div.
// TRLC-LINKS: REQ-SDS-137
func TestRailing(t *testing.T) {
	c, _, _ := newC(t)

	// helper: build an N-sample channel where `railed` of them are pinned high.
	mk := func(n, railed int, env bool) *engine.Frame {
		s := make([]uint8, n)
		for i := range s {
			s[i] = 128 // mid-screen, well clear of both rails
		}
		for i := 0; i < railed && i < n; i++ {
			s[i] = 255
		}
		return &engine.Frame{C1: s, C2: s, Valid: n, IsEnv: env}
	}
	use := func(f *engine.Frame) { c.SetFrameSource(func(fn func(*engine.Frame)) { fn(f) }) }

	// Clean trace: nothing at the rails → not railing.
	use(mk(1000, 0, false))
	if c.railing(0) {
		t.Errorf("clean trace reported as railing")
	}

	// Just under 5% pinned high (49/1000 = 4.9%) → still not railing.
	use(mk(1000, 49, false))
	if c.railing(0) {
		t.Errorf("4.9%% pinned reported as railing (threshold is >5%%)")
	}

	// Comfortably over 5% (80/1000 = 8%) → railing.
	use(mk(1000, 80, false))
	if !c.railing(0) {
		t.Errorf("8%% pinned NOT reported as railing")
	}

	// Bottom rail counts too (code 0).
	{
		s := make([]uint8, 1000)
		for i := range s {
			s[i] = 128
		}
		for i := 0; i < 80; i++ {
			s[i] = 0
		}
		use(&engine.Frame{C1: s, C2: s, Valid: 1000})
		if !c.railing(0) {
			t.Errorf("bottom-rail (code 0) not reported as railing")
		}
	}

	// Envelope frames are min/max bands, not per-sample rails → never railing,
	// even when the data looks pinned.
	use(mk(1000, 500, true))
	if c.railing(0) {
		t.Errorf("envelope frame reported as railing")
	}

	// Too few samples to judge → not railing.
	use(mk(4, 4, false))
	if c.railing(0) {
		t.Errorf("<8-sample frame reported as railing")
	}

	// Channel selection: rail C2 only (C1 clean) and check ch=1 sees it.
	{
		clean := make([]uint8, 1000)
		railed := make([]uint8, 1000)
		for i := range clean {
			clean[i] = 128
			railed[i] = 128
		}
		for i := 0; i < 80; i++ {
			railed[i] = 255
		}
		use(&engine.Frame{C1: clean, C2: railed, Valid: 1000})
		if c.railing(0) {
			t.Errorf("C1 clean but ch0 reported railing")
		}
		if !c.railing(1) {
			t.Errorf("C2 railed but ch1 did not report railing")
		}
	}
}

// TestOffScreenSaturated covers the front-end-amp saturation case that pure ADC-
// rail detection misses: a large DC on a sensitive range saturates the amp before
// the ADC, so the whole trace pins at the screen EDGE (raw-code midpoint ~251,
// ≈+4.9 div off centre) but NO code reaches 254/255 — railing() reads 0%. The 3c
// coarsen guard keys on offScreen(), which must still report "coarsen" here so the
// signal is walked down to an attenuated range where the offset can centre it.
// TRLC-LINKS: REQ-SDS-137
func TestOffScreenSaturated(t *testing.T) {
	c, _, _ := newC(t)
	use := func(f *engine.Frame) { c.SetFrameSource(func(fn func(*engine.Frame)) { fn(f) }) }

	// Every sample at code 251: pinned at the top edge, but below the 254 rail.
	edge := make([]uint8, 1000)
	for i := range edge {
		edge[i] = 251
	}
	use(&engine.Frame{C1: edge, C2: edge, Valid: 1000})
	if c.railing(0) {
		t.Fatalf("code-251 edge should NOT count as hard ADC-railing (0%% at rails)")
	}
	if !c.offScreen(0) {
		t.Errorf("saturated trace at ~+4.9 div NOT flagged off-screen — guard would not coarsen")
	}

	// A legitimately centred full-screen swing (midpoint at 128) must NOT coarsen.
	centred := make([]uint8, 1000)
	for i := range centred {
		if i%2 == 0 {
			centred[i] = 30 // ~-3.9 div
		} else {
			centred[i] = 226 // ~+3.9 div → midpoint 128
		}
	}
	use(&engine.Frame{C1: centred, C2: centred, Valid: 1000})
	if c.offScreen(0) {
		t.Errorf("centred full-screen signal flagged off-screen — would false-coarsen")
	}
}

// A failed sweep must preserve calibrated requested offsets.
// TRLC-LINKS: REQ-SDS-137
func TestAutosetNoSignalRestoresRequestedOffsets(t *testing.T) {
	c, eng, fe := newC(t)
	eng.stats.TdivS = 1e-6
	fe.offReqV = [2]float64{-1.35, 0.65}
	before := fe.offReqV
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		fn(&engine.Frame{C1: []uint8{128, 128, 128, 128, 128, 128, 128, 128}, C2: []uint8{128, 128, 128, 128, 128, 128, 128, 128}, Valid: 8, SampleS: 2e-9})
	})
	c.runAutoset(make(chan struct{}))
	if fe.offReqV != before {
		t.Fatalf("offsets after no-signal sweep = %v, want %v", fe.offReqV, before)
	}
}

// TRLC-LINKS: REQ-SDS-137
type autosetSampleEngine struct {
	*fakeEng
	leases []time.Duration
}

// TRLC-LINKS: REQ-SDS-137
func (e *autosetSampleEngine) LeaseSampleView(d time.Duration) { e.leases = append(e.leases, d) }

// TRLC-LINKS: REQ-SDS-137
func TestAutosetLeasesSampleFrames(t *testing.T) {
	c, e, _ := newC(t)
	leaser := &autosetSampleEngine{fakeEng: e}
	c.eng = leaser
	sig := make([]uint8, 200)
	for i := range sig {
		sig[i] = 40
		if i%20 >= 10 {
			sig[i] = 200
		}
	}
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		fn(&engine.Frame{C1: sig, C2: sig, Valid: len(sig), SampleS: 50e-9, IsEnv: len(leaser.leases) == 0})
	})
	stop := make(chan struct{})
	for i := 0; i < 2; i++ {
		if !c.waitFrame(stop) {
			t.Fatal("unexpected cancellation")
		}
		if len(leaser.leases) != i+1 || leaser.leases[i] <= settleMs*time.Millisecond {
			t.Fatalf("autoset must renew chronological samples before settling: %v", leaser.leases)
		}
		m, ok := c.measureChans()
		if !ok || !has(m[0]) || m[0].Freq <= 0 {
			t.Fatal("autoset did not receive a measurable chronological signal")
		}
	}
}

// The sweep's sample lease ends with it: held on, the live view stayed the
// filtered decimated capture for 3 s after AUTO had finished.
// TRLC-LINKS: REQ-SDS-137
func TestAutosetEndsItsSampleLease(t *testing.T) {
	c, e, _ := newC(t)
	leaser := &autosetSampleEngine{fakeEng: e}
	c.eng = leaser
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		fn(&engine.Frame{C1: make([]uint8, 200), C2: make([]uint8, 200), Valid: 200, SampleS: 50e-9})
	})
	c.runAutoset(make(chan struct{}))
	if n := len(leaser.leases); n < 2 || leaser.leases[n-1] != 0 {
		t.Fatalf("leases %v: the last must end the lease (0)", leaser.leases)
	}
}

// AUTO triggers on the stronger channel unless only the other's repetition is
// resolved at the fitted timebase (the slow source's 10 Hz pulse beside its
// 1 kHz square had the trigger on the pulse and the timebase on the square).
// TRLC-LINKS: REQ-SDS-137
func TestAutosetTriggersOnAResolvedChannel(t *testing.T) {
	sq := &measure.Result{Vpp: 3.81, Freq: 1000}
	pulse := &measure.Result{Vpp: 3.91}
	if got := autosetTrigSource([2]*measure.Result{sq, pulse}); got != 0 {
		t.Fatalf("1 kHz on C1, unresolved pulse on C2: triggers on C%d", got+1)
	}
	both := &measure.Result{Vpp: 3.91, Freq: 100e3}
	if got := autosetTrigSource([2]*measure.Result{sq, both}); got != 1 {
		t.Fatalf("both resolved: the stronger C2, got C%d", got+1)
	}
}

// A bursty bus shows its burst at the sweep's band but not in every capture
// after it (the bench FlexRay: two frames each 240 us, idle high between):
// the vertical fit and the trigger keep the widest swing seen, so a flat idle
// capture does not fit a sensitive V/div that pushes the signal off the
// screen (it fitted 100 mV/div to a 3.3 V bus).
// TRLC-LINKS: REQ-SDS-137
func TestAutosetKeepsABurstsSwing(t *testing.T) {
	c, e, fe := newC(t)
	e.stats.TdivS = 1e-6
	burst := make([]uint8, 200)
	idle := make([]uint8, 200)
	for i := range burst {
		burst[i] = 200
		if i%20 >= 10 {
			burst[i] = 40
		}
		idle[i] = 200 + uint8(i%2)*2
	}
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		sig := idle
		if e.stats.TdivS <= 1e-6 {
			sig = burst
		}
		fn(&engine.Frame{C1: sig, C2: sig, Valid: len(sig), SampleS: 50e-9})
	})
	c.runAutoset(make(chan struct{}))
	vidx, _ := fe.Snapshot()
	got := fe.offReqV
	want := detentFor(160*analog.AnalogVdiv(nearestDetent(2.0))/25, goalScope.fill) // the burst read at the sweep's 2 V/div
	// the midpoint the burst reads at that scale: the offset centres it (an
	// idle-only capture's midpoint, its rail, must not re-centre it)
	for ch := 0; ch < 2; ch++ {
		c.setVdiv(ch, nearestDetent(2.0))
	}
	e.stats.TdivS = 1e-6
	m, _ := c.measureChans()
	for ch := 0; ch < 2; ch++ {
		if vidx[ch] < want {
			t.Fatalf("C%d fitted detent %d, want %d or coarser for the burst's swing", ch+1, vidx[ch], want)
		}
		if mid := (m[ch].Vmax + m[ch].Vmin) / 2; math.Abs(got[ch]+mid) > 0.05*m[ch].Vpp {
			t.Fatalf("C%d offset %.3f V, want %.3f V (the burst's midpoint centred)", ch+1, got[ch], -mid)
		}
	}
}

// AUTO finds a 3.3 V logic square through 10x probes and fits it as with
// 1x: the sweep's coarse 2 V/div is at the probe tip (at the BNC it was
// 20 V/div at the tip, the swing four codes, and AUTO said no signal). With
// the factor left at 1x on a 10x probe (Default setup), a second pass at
// 200 mV/div finds it.
// TRLC-LINKS: REQ-SDS-137
func TestAutosetFindsALogicSignalThroughA10xProbe(t *testing.T) {
	for _, pr := range []struct{ set, physical float64 }{{1, 1}, {10, 10}, {1, 10}} {
		c, e, fe := newC(t)
		e.stats.TdivS = 1e-6
		fe.SetProbe(0, pr.set)
		fe.SetProbe(1, pr.set)
		c.SetFrameSource(func(fn func(*engine.Frame)) {
			idx, _ := fe.Snapshot()
			var codes [2][]uint8
			for ch := 0; ch < 2; ch++ {
				for i := 0; i < 400; i++ {
					tip := 0.0
					if i%20 >= 10 {
						tip = 3.3
					}
					// centred on the screen: the fake engine keeps no offset
					code := 128 + (tip-1.65)/pr.physical/analog.AnalogVdiv(idx[ch])*25 + float64(i%3) - 1
					codes[ch] = append(codes[ch], uint8(math.Max(0, math.Min(255, math.Round(code)))))
				}
			}
			fn(&engine.Frame{C1: codes[0], C2: codes[1], Valid: 400, SampleS: 50e-9})
		})
		c.runAutoset(make(chan struct{}))
		c.mu.Lock()
		msg := c.autosetMsg
		c.mu.Unlock()
		if msg == "AUTOSET: no signal" {
			t.Fatalf("%+v: no signal", pr)
		}
		vidx, _ := fe.Snapshot()
		for ch := 0; ch < 2; ch++ {
			// the 3.3 V swing fitted to 500 mV or 1 V a division at the tip
			if tip := analog.Detents[vidx[ch]].VdivV * pr.physical; tip < 0.45 || tip > 1.05 {
				t.Fatalf("%+v: C%d fitted %g V/div at the BNC, %g at the tip", pr, ch+1, analog.Detents[vidx[ch]].VdivV, tip)
			}
		}
	}
}

// A 3.3 V logic line through a 10x probe set as 1x switches four codes at the
// sweep's 2 V/div: the coarse pass gives way to the fine one at once
// (smallActive). A quiet line's two codes of noise, or a wider signal beside
// the small one, keep the coarse pass going.
// TRLC-LINKS: REQ-SDS-137
func TestSmallActiveLine(t *testing.T) {
	c, _, _ := newC(t)
	// TRLC-LINKS: REQ-SDS-137
	line := func(swing int) []uint8 {
		s := make([]uint8, 1000)
		for i := range s {
			v := 130 + i%3 - 1 // two codes of noise
			if swing > 0 && i%100 >= 50 {
				v += swing
			}
			s[i] = uint8(v)
		}
		return s
	}
	for _, tc := range []struct {
		c1, c2 int
		want   bool
	}{{4, 0, true}, {0, 4, true}, {6, 5, true}, {0, 0, false}, {4, 30, false}, {12, 0, false}} {
		f := &engine.Frame{C1: line(tc.c1), C2: line(tc.c2), Valid: 1000}
		c.SetFrameSource(func(fn func(*engine.Frame)) { fn(f) })
		if got := c.smallActive(); got != tc.want {
			t.Errorf("swings %d/%d codes: smallActive %v, want %v", tc.c1, tc.c2, got, tc.want)
		}
	}
}
