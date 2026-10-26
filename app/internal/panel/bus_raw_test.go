// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// BUS trades the frame rate for the decode: a live envelope too coarse for
// the bus leases sampled frames, renewed by each frame (a sampled one too)
// while the bus's settings stay; they changing, or BUS left, end the lease.
// TRLC-LINKS: REQ-SDS-214
func TestBusLeasesSampledFramesToDecode(t *testing.T) {
	eng := &namedLeaseEng{fakeEng: newEng()}
	c := New(eng, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	c.mu.Lock()
	c.decProto = framecalc.ProtoUART
	c.mu.Unlock()
	c.switchWS(wsview.Bus)
	eng.take()
	s := c.snap()
	s.st.Running = true
	env := &engine.Frame{PeakDetect: true, Ordered: true}
	raw := &engine.Frame{}
	a := &anaState{}
	a.res = decode.Result{Proto: "uart", Error: "UART" + " not resolved at this timebase - S/DIV faster, or SINGLE"}
	if !framecalc.Unresolved(a.res) {
		t.Fatal("the envelope's refusal not recognised")
	}
	c.busRawFrames(a, s, env)
	// the timebase (500us/div) records 32 ns at the finest: that record, as samples
	if got := eng.take(); len(got) != 1 || got[0] != "samples 3.2e-08 10s" {
		t.Fatalf("coarse envelope: leases %v", got)
	}
	if !strings.Contains(a.res.Error, "sampled records") {
		t.Fatalf("notice %q", a.res.Error)
	}
	a.res = decode.Result{Proto: "uart", OK: true}
	c.busRawFrames(a, s, raw)
	if got := eng.take(); len(got) != 1 || got[0] != "samples 3.2e-08 10s" {
		t.Fatalf("sampled frame decoded: leases %v, want renewed", got)
	}
	s.st.TdivS *= 2 // the timebase changed: its envelope is tried first
	c.busRawFrames(a, s, raw)
	if got := eng.take(); len(got) != 1 || got[0] != "samples 0 0s" {
		t.Fatalf("settings changed: leases %v, want ended", got)
	}
	s.st.TdivS /= 2
	a.res = decode.Result{Proto: "uart", Error: "UART" + " not resolved at this timebase - S/DIV faster, or SINGLE"}
	c.busRawFrames(a, s, env)
	eng.take()
	// a roll view with too few samples a bit leases them too; a sampled
	// record as coarse ends the lease for those settings
	s.st.Roll = true
	s.st.TdivS = 10e-3
	a.res = decode.Result{Proto: "uart", Error: "0.3 samples/bit; need >= 3"}
	c.busRawFrames(a, s, raw)
	if got := eng.take(); len(got) != 1 || got[0] != "samples 2.56e-07 10s" {
		t.Fatalf("coarse roll view: leases %v", got)
	}
	s.st.Roll = false
	a.res = decode.Result{Proto: "uart", Error: "2.1 samples/bit; need >= 3"}
	c.busRawFrames(a, s, raw)
	if got := eng.take(); len(got) != 1 || got[0] != "samples 0 0s" {
		t.Fatalf("sampled record too coarse: leases %v, want ended", got)
	}
	s.st.Roll = true
	a.res = decode.Result{Proto: "uart", Error: "0.3 samples/bit; need >= 3"}
	c.busRawFrames(a, s, raw)
	if got := eng.take(); len(got) != 0 {
		t.Fatalf("settings whose records were too coarse: leases %v, want none", got)
	}
	s.st.Roll, s.st.TdivS = false, 200e-6
	a.res = decode.Result{Proto: "uart", Error: "UART" + " not resolved at this timebase - S/DIV faster, or SINGLE"}
	c.busRawFrames(a, s, env)
	eng.take()
	c.switchWS(wsview.Measure)
	c.View()
	if got := eng.take(); len(got) != 1 || got[0] != rawForBus+" 0s" {
		t.Fatalf("BUS left: leases %v, want ended", got)
	}
}

// TRLC-LINKS: REQ-SDS-214
func (e *namedLeaseEng) LeaseBusSamples(sampleS float64, d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.leases = append(e.leases, fmt.Sprintf("samples %g %v", sampleS, d))
}

// A bus whose last decode measured its shortest feature leases records
// sampled at a quarter of it at the coarsest (the fewest words that decode it); records that
// still hold too few samples a bit take the full rate.
// TRLC-LINKS: REQ-SDS-214
func TestBusLeasesRecordsSampledForTheBus(t *testing.T) {
	eng := &namedLeaseEng{fakeEng: newEng()}
	c := New(eng, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	c.mu.Lock()
	c.decProto = framecalc.ProtoUART
	c.mu.Unlock()
	c.switchWS(wsview.Bus)
	eng.take()
	s := c.snap()
	s.st.Running = true
	a := &anaState{}
	// decoded at 200us/div: a bit of 8.7 us, 35 samples of 0.25 us
	a.res = decode.Result{Proto: "uart", OK: true, SPB: 34.8}
	c.busRawFrames(a, s, &engine.Frame{SampleS: 0.25e-6})
	eng.take()
	s.st.TdivS, s.st.Roll = 10e-3, true
	a.res = decode.Result{Proto: "uart", Error: "0.3 samples/bit; need >= 3"}
	c.busRawFrames(a, s, &engine.Frame{SampleS: 30e-6})
	if got := eng.take(); len(got) != 1 || got[0] != "samples 2.175e-06 10s" {
		t.Fatalf("coarse roll view: leases %v, want records every 8.7/4 us at the coarsest", got)
	}
	s.st.Roll = false
	a.res = decode.Result{Proto: "uart", Error: "2.1 samples/bit; need >= 3"}
	c.busRawFrames(a, s, &engine.Frame{SampleS: 4e-6, Decimation: 2048})
	// the full rate is 10ms/div's own record (256 ns), as samples: a raw
	// lease read it whole and bucketed
	if got := eng.take(); len(got) != 1 || got[0] != "samples 2.56e-07 10s" {
		t.Fatalf("decimated records too coarse: leases %v, want the timebase's own record", got)
	}
	// a decimated record read under the lease before: the own record stays
	c.busRawFrames(a, s, &engine.Frame{SampleS: 4e-6, Decimation: 2048})
	if got := eng.take(); len(got) != 1 || got[0] != "samples 2.56e-07 10s" {
		t.Fatalf("a late decimated record: leases %v, want the own record kept", got)
	}
	// the timebase's own record (10ms/div: /128) too coarse ends it
	c.busRawFrames(a, s, &engine.Frame{SampleS: 256e-9, Decimation: 128})
	if got := eng.take(); len(got) != 1 || got[0] != "samples 0 0s" {
		t.Fatalf("the timebase's record too coarse: leases %v, want ended", got)
	}
}

// A bus whose feature wants samples finer than the finest decimation (a
// 10 Mbit/s FlexRay's 100 ns bit) leases full-rate records at once.
// TRLC-LINKS: REQ-SDS-214
func TestBusLeasesTheFullRateForAFastBus(t *testing.T) {
	eng := &namedLeaseEng{fakeEng: newEng()}
	c := New(eng, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	c.mu.Lock()
	c.decProto = framecalc.ProtoFlexRay
	c.mu.Unlock()
	c.switchWS(wsview.Bus)
	eng.take()
	s := c.snap()
	s.st.Running = true
	a := &anaState{}
	a.res = decode.Result{Proto: "flexray", OK: true, SPB: 50}
	c.busRawFrames(a, s, &engine.Frame{SampleS: 2e-9})
	eng.take()
	s.st.TdivS = 20e-6
	a.res = decode.Result{Proto: "flexray", Error: "1.8 samples/bit; need >= 3"}
	c.busRawFrames(a, s, &engine.Frame{SampleS: 61e-9, PeakDetect: true, Ordered: true})
	if got := eng.take(); len(got) != 1 || got[0] != rawForBus+" 10s" {
		t.Fatalf("FlexRay at 20us/div: leases %v, want full-rate records", got)
	}
}

// A live frame that does not decode and whose points are too far apart for
// the bus as its last decode measured it leases sampled records whatever the
// decoder's error (SENT at 2ms/div: "no SENT frame").
// TRLC-LINKS: REQ-SDS-214
func TestBusLeasesWhereTheViewIsTooCoarseForTheBus(t *testing.T) {
	eng := &namedLeaseEng{fakeEng: newEng()}
	c := New(eng, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	c.mu.Lock()
	c.decProto = framecalc.ProtoSENT
	c.mu.Unlock()
	c.switchWS(wsview.Bus)
	eng.take()
	s := c.snap()
	s.st.Running = true
	a := &anaState{}
	// decoded at 200us/div: a 3 us tick, the feature four of them (12 us)
	a.res = decode.Result{Proto: "sent", OK: true, SPB: 12}
	c.busRawFrames(a, s, &engine.Frame{SampleS: 0.25e-6})
	eng.take()
	s.st.TdivS = 2e-3
	a.res = decode.Result{Proto: "sent", Error: "no SENT frame"}
	c.busRawFrames(a, s, &engine.Frame{SampleS: 12e-6, PeakDetect: true, Ordered: true})
	if got := eng.take(); len(got) != 1 || got[0] != "samples 3e-06 10s" {
		t.Fatalf("SENT at 2ms/div: leases %v, want records every 3 us", got)
	}
	// a fine view that does not decode is no resolution matter
	s.st.TdivS = 100e-6
	a.res = decode.Result{Proto: "sent", Error: "no SENT frame"}
	c.busRawFrames(a, s, &engine.Frame{SampleS: 0.6e-6, PeakDetect: true, Ordered: true})
	if got := eng.take(); len(got) != 0 && got[len(got)-1] != "samples 0 0s" {
		t.Fatalf("a fine view: leases %v", got)
	}
}

// A live envelope whose decode fails reads sampled records while the bus's
// shortest feature spans under coarseFeature of its points (Sony SIRC at
// 20ms/div: 5.6 points a unit), not when it decodes, not past that, and not
// for a bus never decoded at these settings.
// TRLC-LINKS: REQ-SDS-214
func TestCoarseEnvelopeFailureReadsSampledRecords(t *testing.T) {
	f := &engine.Frame{SampleS: 107e-6}
	a := &anaState{featKey: "sirc", featEnv: 600e-6, res: decode.Result{Proto: "irsirc", Error: "no complete SIRC frame"}}
	if !coarseFor(a, "sirc", f) {
		t.Fatal("5.6 points a unit, failing: the live view")
	}
	if a.res.OK = true; coarseFor(a, "sirc", f) {
		t.Fatal("a decode that holds took sampled records")
	}
	a.res.OK = false
	if coarseFor(a, "other", f) {
		t.Fatal("a bus never decoded at these settings took sampled records")
	}
	if a.featEnv = 1.2e-3; coarseFor(a, "sirc", f) {
		t.Fatal("11 points a unit: the decoder's own failure, not the view's")
	}
}

// The packet list and BUS's leases belong to SPI's data delay as set: Auto's,
// re-measured on every full-rate record, keeps the key; a delay set by hand
// is the key's own.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-230
func TestBusKeyHoldsThroughAutoDelay(t *testing.T) {
	s := &sv{decProto: framecalc.ProtoSPI, decDelayAuto: true}
	s.spiEye = decode.SPIEye{PeriodS: 100e-9, DelayS: 12.4e-9}
	k := busListKey(s)
	s.spiEye.DelayS = 12.9e-9
	if busListKey(s) != k {
		t.Fatal("a re-measured Auto delay changed the key")
	}
	s.decDelayAuto, s.decDelayNs = false, 5
	k = busListKey(s)
	if s.decDelayNs = 7; busListKey(s) == k {
		t.Fatal("a delay set by hand kept the key")
	}
}

// A decode refused for its record's sampling, in either wording: the line
// codes' and the clocked buses'.
// TRLC-LINKS: REQ-SDS-214
func TestTooFewSamplesBothWordings(t *testing.T) {
	for _, e := range []string{"1.9 samples/bit; need >= 3", "4.0 cols/clock; too few samples/bit"} {
		if !tooFewSamples(decode.Result{Error: e}) {
			t.Fatalf("%q not read as too few samples", e)
		}
	}
	if tooFewSamples(decode.Result{Error: "no SPI frame"}) {
		t.Fatal("a missing frame read as too few samples")
	}
}

// A clocked bus whose decode reports a clock over twice as slow as the one
// measured for its settings read an alias (a 10 MHz SPI bus on 32 ns
// samples): refused as too coarse; at the measured clock, or on a bus that
// is not clocked, the decode stands.
// TRLC-LINKS: REQ-SDS-214
func TestAliasedClockIsRefused(t *testing.T) {
	f := &engine.Frame{SampleS: 32e-9}
	a := &anaState{featKey: "spi", featS: 50e-9, res: decode.Result{Proto: "spi", OK: true, SPB: 9}} // a 288 ns clock half
	if !aliasedClock(a, "spi", f) {
		t.Fatal("a clock 5.8 times slower than measured stood")
	}
	if a.res.SPB = 3; aliasedClock(a, "spi", f) {
		t.Fatal("a decode at the measured clock (a 48 ns half) refused")
	}
	a.res = decode.Result{Proto: "pwm", OK: true, Feature: 40}
	if aliasedClock(a, "spi", f) {
		t.Fatal("a PWM line's run, which follows its duty, refused")
	}
}
