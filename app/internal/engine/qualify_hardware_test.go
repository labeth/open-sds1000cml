// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"strings"
	"testing"

	"open-sds/app/internal/sramcapture"
)

// qualEngine is the packet test engine on the qualifier image at 1 V/div, its
// channels' extents not yet learned.
func qualEngine(t *testing.T) (*Engine, *stackEngineBus, *packetImages) {
	e, b, images := newPacketTestEngine(t)
	for ch := 0; ch < 2; ch++ {
		e.chVdivBits[ch].Store(math.Float64bits(1))
	}
	e.trigPosFrac.Store(math.Float64bits(.5))
	e.trigRising.Store(true)
	e.band = Band{TdivS: 100e-6}
	return e, b, images
}

// square is a 0 V / 3.4 V square at 1 V/div: codes 128 and 213.
func learnSquare(e *Engine) {
	w := make([]uint8, 4000)
	for i := range w {
		w[i] = 128
		if i/100%2 == 1 {
			w[i] = 213
		}
	}
	e.learnQualExtent(&Frame{C1: w, C2: w, Valid: len(w), SampleS: 1e-8}, [2]chScale{{1, 0}, {1, 0}})
}

// Pulse, slope and video triggers are the FPGA's (owner rule: software never
// decides a trigger): the qualifier image is loaded for them, nothing
// triggers until the channel's levels are known, then the qualifier is
// planned at the learned levels, its limits in ticks.
// TRLC-LINKS: REQ-SDS-011
func TestPulseTriggerIsPlannedInHardware(t *testing.T) {
	e, b, _ := qualEngine(t)
	if e.wantImage() != "" {
		t.Fatal("an edge trigger wanted the qualifier image")
	}
	e.SetTrigType(int(TrigPulse))
	e.SetPulseParams(.5, 2000, 3000, CondInside)
	if !e.serviceImage() || b.image != "qualify" || !e.hardwareQualifier {
		t.Fatalf("Pulse: image %s, qualifier %v", b.image, e.hardwareQualifier)
	}
	c, _, _, _, _ := e.sramConfig()
	if c.Qualifier.Enabled || c.Normal {
		t.Fatalf("a qualifier before the levels are known: %+v normal %v", c.Qualifier, c.Normal)
	}
	if _, _, why := e.planQualifier(e.tp, 0, true); !strings.Contains(why, "learning the signal levels on C1") {
		t.Fatalf("why %q", why)
	}
	learnSquare(e)
	c, _, _, _, _ = e.sramConfig()
	q := c.Qualifier
	// 2 us is 500 clocks: at least 499 ticks (the clocks between the edges);
	// 3 us is 750 clocks: over 749 ticks from 751 clocks
	if !q.Enabled || !c.Normal || q.Kind != sramcapture.QualPulse || q.Negative || q.Cond != sramcapture.QualBetween ||
		q.Prescale != 1 || q.MinTicks != 499 || q.MaxTicks != 749 || c.TriggerLevel != 171 {
		t.Fatalf("pulse plan %+v level %d normal %v", q, c.TriggerLevel, c.Normal)
	}
	// a falling trigger slope is a low pulse; a 1 V offset moves the level with the trace
	e.trigRising.Store(false)
	e.trigOffV[0].Store(math.Float64bits(1))
	if c, _, _, _, _ := e.sramConfig(); !c.Qualifier.Negative || c.TriggerLevel != 196 {
		t.Fatalf("low pulse with an offset: %+v level %d", c.Qualifier, c.TriggerLevel)
	}
	// back to edge: the general image
	e.SetTrigType(int(TrigEdge))
	if !e.serviceImage() || b.image != "general" {
		t.Fatalf("edge: image %s", b.image)
	}
}

// The limits in the FPGA's ticks: an interval whose edges are W clocks apart
// counts floor((W-1)/P) ticks of P clocks, so at P 1 a minimum of 10 clocks
// (40 ns) is 9 ticks and a maximum of 10 clocks 9 ticks (over from 11).
// TRLC-LINKS: REQ-SDS-011
func TestQualTicksMatchTheFPGAsCount(t *testing.T) {
	counts := func(w, p int) int { return (w - 1) / p }
	for _, c := range []struct{ minNs, maxNs float64 }{{40, 40}, {40, 400}, {1000, 3000}, {0, 20}, {2e6, 5e6}} {
		p, lo, hi := qualTicks(c.minNs, c.maxNs)
		minW, maxW := int(c.minNs/4), int(c.maxNs/4)
		if p != 1 {
			continue
		}
		// at least the minimum from minW clocks, not before; over the maximum from maxW+1, not at maxW
		if minW > 0 && (counts(minW, 1) < int(lo) || counts(minW-1, 1) >= int(lo)) {
			t.Fatalf("%g ns minimum: %d ticks", c.minNs, lo)
		}
		if counts(maxW, 1) > int(hi) || counts(maxW+1, 1) <= int(hi) {
			t.Fatalf("%g ns maximum: %d ticks", c.maxNs, hi)
		}
	}
	if p, lo, hi := qualTicks(40, 40); p != 1 || lo != 9 || hi != 9 {
		t.Fatalf("40 ns: prescale %d, %d..%d ticks", p, lo, hi)
	}
}

// A slope's two levels, and long limits on a coarser tick.
// TRLC-LINKS: REQ-SDS-011
func TestSlopeAndLongLimitsInHardware(t *testing.T) {
	e, _, _ := qualEngine(t)
	e.SetTrigType(int(TrigSlope))
	e.SetSlopeParams(.2, .8, 0, 20, CondLess)
	e.serviceImage()
	learnSquare(e)
	c, _, _, _, _ := e.sramConfig()
	q := c.Qualifier
	if !q.Enabled || q.Kind != sramcapture.QualSlope || q.LevelA != 145 || q.LevelB != 196 || q.Cond != sramcapture.QualUnder || q.MinTicks != 0 {
		t.Fatalf("slope plan %+v", q)
	}
	if p, lo, hi := qualTicks(2e9, 5e9); p != 32768 || lo != 15259 || hi != 38146 { // 2 s and 5 s in ticks of 131 us
		t.Fatalf("5 s limits: prescale %d, %d..%d ticks", p, lo, hi)
	}
	if p, _, hi := qualTicks(0, 10e9); p != 65536 || hi != 38146 { // the longest limit, 10 s
		t.Fatalf("10 s limit: prescale %d, %d ticks", p, hi)
	}
}

// A video line counts from the field's vertical sync: NTSC's first line
// sync after it is line 10, PAL's line 6; line 0 is every line.
// TRLC-LINKS: REQ-SDS-011
func TestVideoLineInHardware(t *testing.T) {
	e, _, _ := qualEngine(t)
	e.SetTrigType(int(TrigVideo))
	e.serviceImage()
	learnSquare(e)
	for _, c := range []struct {
		std, line int
		want      uint16
	}{{1, 20, 11}, {1, 10, 1}, {1, 3, 1}, {0, 6, 1}, {0, 23, 18}, {0, 0, 0}} {
		e.SetVideoParams(c.std, c.line, true)
		cfg, _, _, _, _ := e.sramConfig()
		if !cfg.Qualifier.Enabled || cfg.Qualifier.Kind != sramcapture.QualVideo || cfg.Qualifier.Line != c.want || !cfg.Qualifier.Negative || cfg.TriggerLevel != 154 {
			t.Fatalf("std %d line %d: %+v level %d", c.std, c.line, cfg.Qualifier, cfg.TriggerLevel)
		}
	}
}

// The FPGA fires a video line at its sync's end; the trigger point moves
// to the sync's leading edge. A pulse's point is its closing crossing.
// TRLC-LINKS: REQ-SDS-011
func TestQualifiedTriggerIsPlacedOnItsEvent(t *testing.T) {
	const n = 2000
	sig := make([]uint8, n)
	for i := range sig {
		sig[i] = 200
		if i >= 800 && i < 1270 { // a 4.7 us sync at 10 ns
			sig[i] = 40
		}
	}
	f := &Frame{C1: sig, Valid: n, SampleS: 1e-8, EdgeX: 1272, Trigd: true}
	q := sramcapture.QualifierConfig{Enabled: true, Kind: sramcapture.QualVideo, Negative: true}
	if x := placeQualified(f, sig, nil, q, 88); math.Abs(x-800) > 1 {
		t.Fatalf("video placed at %.1f, want the sync's leading edge 800", x)
	}
	q = sramcapture.QualifierConfig{Enabled: true, Kind: sramcapture.QualPulse, Negative: true} // a low pulse closes rising
	if x := placeQualified(f, sig, nil, q, 120); math.Abs(x-1270) > 1 {
		t.Fatalf("pulse placed at %.1f, want 1270", x)
	}
}

// The zone trigger is the FPGA's: its windows in power-of-two buckets of the
// 8 ns half clock, the finest that keeps every window and the decision's
// wait in the 1024-bucket history; the record holds the decision's delay
// before the trigger; with zones nothing triggers until the hardware can.
// TRLC-LINKS: REQ-SDS-014
func TestZoneTriggerIsPlannedInHardware(t *testing.T) {
	e, b, _ := qualEngine(t)
	e.SetZones([]Zone{{DtLoS: -40e-6, DtHiS: -10e-6, CodeLo: 180, CodeHi: 220}, {DtLoS: 5e-6, DtHiS: 20e-6, CodeLo: 0, CodeHi: 60, Avoid: true, Ch: 1}})
	e.SetZoneMode(ZoneTrigger)
	if !e.serviceImage() || b.image != "qualify" {
		t.Fatalf("zones: image %s", b.image)
	}
	z, delayS, why := e.planZones()
	// windows from 40 us before to 20 us after: the finest bucket that holds them is 8 half clocks (64 ns);
	// the FPGA fires the wait and its 48 half clocks of decision (and 30 ns of registers) after the edge
	if why != "" || z.Bucket != 8 || z.Zones[0].Start != -625 || z.Zones[0].End != -156 || z.Zones[1].Start != 78 ||
		z.Zones[1].End != 313 || !z.Zones[1].Avoid || z.Zones[1].Channel != 1 || z.Wait != 314 || math.Abs(delayS-(314*64e-9+48*8e-9+30e-9)) > 1e-12 {
		t.Fatalf("zone plan %+v delay %g why %q", z, delayS, why)
	}
	c0, plan, _, _, _ := e.sramConfig()
	if !c0.Zones.Enabled() || !c0.Normal {
		t.Fatalf("config %+v normal %v", c0.Zones, c0.Normal)
	}
	e.SetZoneMode(ZoneOff)
	c1, _, _, _, _ := e.sramConfig()
	if c1.Zones.Enabled() || c0.PreWords != c1.PreWords+uint32(delayS/plan.SampleS)/uint32(2-boolInt(plan.Log != 0)) {
		t.Fatalf("the record's room for the decision: %d words with zones, %d without", c0.PreWords, c1.PreWords)
	}
	// windows wider than the history at the coarsest bucket: no plan, nothing triggers
	e.SetZones([]Zone{{DtLoS: -1, DtHiS: 1, CodeLo: 0, CodeHi: 255}})
	e.SetZoneMode(ZoneTrigger)
	if _, _, why := e.planZones(); !strings.Contains(why, "history") {
		t.Fatalf("wide zones: %q", why)
	}
	if c, _, _, _, _ := e.sramConfig(); c.Zones.Enabled() || c.Normal {
		t.Fatalf("wide zones planned: %+v normal %v", c.Zones, c.Normal)
	}
}

// The TRIG status says the qualifier image is loading from the moment the
// load starts, not from the first capture after it (the TRIG tab named
// nothing for the seconds a load takes; bug hunt #3); an edge drops the last
// type's status at once.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-014
func TestTriggerStatusSaysTheImageLoads(t *testing.T) {
	e, _, _ := qualEngine(t)
	e.SetZones([]Zone{{DtLoS: 1e-6, DtHiS: 2e-6, CodeLo: 0, CodeHi: 255}})
	e.SetZoneMode(ZoneTrigger)
	e.SetTrigType(int(TrigSlope))
	if !e.serviceImage() {
		t.Fatal("no image switch")
	}
	if st := e.Snapshot(); st.TriggerBackend != "none: loading the qualifier FPGA image" || st.ZoneBackend != "none: loading the qualifier FPGA image" {
		t.Fatalf("during the load: trigger %q zones %q", st.TriggerBackend, st.ZoneBackend)
	}
	e.mu.Lock()
	e.stats.TriggerBackend = "hardware-slope"
	e.mu.Unlock()
	e.SetTrigType(int(TrigEdge))
	if st := e.Snapshot(); st.TriggerBackend != "" {
		t.Fatalf("an edge kept %q", st.TriggerBackend)
	}
}

// Levels are learned only from a capture with no front-end write since the
// previous capture was armed: the one spanning a V/div change (its relay's
// transient) is skipped, the next one learns (bug hunt #3).
// TRLC-LINKS: REQ-SDS-011
func TestLevelsAreNotLearnedAcrossAFrontEndWrite(t *testing.T) {
	e, _, _ := qualEngine(t)
	square := func(lo, hi uint8) *Frame {
		w := make([]uint8, 4000)
		for i := range w {
			w[i] = lo
			if i/100%2 == 1 {
				w[i] = hi
			}
		}
		return &Frame{C1: w, C2: w, Valid: len(w), SampleS: 1e-8}
	}
	sc := [2]chScale{{1, 0}, {1, 0}}
	extent := func() (float64, float64) {
		return math.Float64frombits(e.qualExt.lo[0].Load()), math.Float64frombits(e.qualExt.hi[0].Load())
	}
	e.learnLevels(square(128, 213), sc, e.feGen.Load()) // steady: learns 0..3.4 V
	if lo, hi := extent(); !e.qualExt.have[0].Load() || lo != 0 || hi != 3.4 {
		t.Fatalf("a steady capture: %g..%g", lo, hi)
	}
	e.NoteFrontEndWrite()                              // a relay switch...
	e.learnLevels(square(20, 250), sc, e.feGen.Load()) // ...whose capture holds its transient
	if lo, hi := extent(); lo != 0 || hi != 3.4 {
		t.Fatalf("the capture after a front-end write was learned: %g..%g", lo, hi)
	}
	e.learnLevels(square(128, 140), sc, e.feGen.Load()) // the next, steady again
	if lo, hi := extent(); lo != 0 || math.Abs(hi-0.48) > 1e-9 {
		t.Fatalf("the steady capture after it: %g..%g", lo, hi)
	}
}
