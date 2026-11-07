// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"math"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
)

// Zone trigger + mask testing (docs/zonemask-plan.md): capture-path
// qualification that runs in the PUBLISH PATH at the full acquisition rate —
// every locked frame is tested whether or not it is published.
//
//   Zone trigger: up to 4 rectangles in EDGE-ANCHORED time × code space; a
//   frame QUALIFIES by intersecting (or avoiding) each zone. Non-qualifying
//   frames HOLD (NORM strictly; AUTO publishes an unqualified liveness frame
//   every zoneFallback holds, like the flat fallback).
//
//   Mask test: a per-column min/max envelope, edge-anchored like the display
//   window; every locked frame is tested and counted, failures are copied
//   into a ring (with the first violating point + a monotonic timestamp), and
//   stop-on-fail freezes acquisition on the offending frame.

// SetZones installs the qualification zones (nil/empty disables the test but
// not the mode). Copies the slice.
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) SetZones(z []Zone) {
	e.zm.Mu.Lock()
	e.zm.Zones = append([]Zone(nil), z...)
	e.zm.Mu.Unlock()
}

// SetZoneMode switches the zone trigger (ZoneOff/ZoneTrigger).
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) SetZoneMode(m int) {
	if m != ZoneOff && m != ZoneTrigger {
		m = ZoneOff
	}
	e.zoneMode.Store(int32(m))
}

// SetMask installs the envelope mask (nil clears). Copies the envelopes.
// Identity is stamped BEFORE taking zm.mu — e.mu inside zm.mu would invert
// the lock order against Snapshot (e.mu -> zm.mu) and deadlock.
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) SetMask(m *Mask) {
	var cp *Mask
	if m != nil {
		cp = &Mask{Lo: append([]uint8(nil), m.Lo...), Hi: append([]uint8(nil), m.Hi...), WinCols: m.WinCols, Ch: m.Ch}
		// stamp the install-time identity (band + vertical mapping); e.band is
		// written under e.mu at the loop boundary — reading it lock-free here
		// (an API goroutine) could stamp a TORN identity matching neither band
		cp.VdivKey = e.chVdivBits[m.Ch&1].Load()
		e.mu.Lock()
		cp.TdivS = e.band.TdivS
		cp.SampleS = e.band.CaptureIntervalNs() * 1e-9
		if e.sram != nil {
			// The SRAM engine's geometry lives in its frames: the mask was
			// built from the latest ones, so it compares with frames like them.
			id := e.pubIdent
			if m.FrameIdent {
				id = core.FrameIdent{TdivS: m.TdivS, SampleS: m.SampleS, PeakDetect: m.PeakDetect}
			}
			cp.FrameIdent, cp.TdivS, cp.SampleS, cp.PeakDetect = true, id.TdivS, id.SampleS, id.PeakDetect
		}
		cp.OffKey = e.offCode[m.Ch&1]
		e.mu.Unlock()
	}
	e.zm.Mu.Lock()
	e.zm.Mask = cp
	e.zm.MaskGen++
	e.zm.Mu.Unlock()
}

// SetMaskMode switches mask testing (MaskOff/MaskTest/MaskStopFail) and
// resets the running counters when turning on.
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) SetMaskMode(m int) {
	if m < MaskOff || m > MaskStopFail {
		m = MaskOff
	}
	if m != MaskOff && int(e.maskMode.Load()) == MaskOff {
		e.maskPass.Store(0)
		e.maskFail.Store(0)
		e.maskSkip.Store(0) // or a fresh test shows every skip since power-on
	}
	if m != MaskStopFail {
		e.maskStopped.Store(false) // leaving stop-on-fail releases the latch
	}
	e.maskMode.Store(int32(m))
}

// ClearMaskFails empties the failure ring and counters.
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) ClearMaskFails() {
	e.zm.Mu.Lock()
	e.zm.Ring = nil
	e.zm.Mu.Unlock()
	e.maskPass.Store(0)
	e.maskFail.Store(0)
	e.maskSkip.Store(0)
	e.maskStopped.Store(false)
}

// MaskFails returns a snapshot of the failure ring (most recent last).
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) MaskFails() []MaskFail {
	e.zm.Mu.Lock()
	defer e.zm.Mu.Unlock()
	return append([]MaskFail(nil), e.zm.Ring...)
}

// Zones returns a copy of the installed zones (render/UI readers).
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) Zones() []Zone {
	e.zm.Mu.Lock()
	defer e.zm.Mu.Unlock()
	return append([]Zone(nil), e.zm.Zones...)
}

// MaskEnvelope returns a copy of the installed mask (nil if none) for the
// LCD/web renderers.
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) MaskEnvelope() *Mask {
	e.zm.Mu.Lock()
	defer e.zm.Mu.Unlock()
	if e.zm.Mask == nil {
		return nil
	}
	cp := *e.zm.Mask
	cp.Lo = append([]uint8(nil), e.zm.Mask.Lo...)
	cp.Hi = append([]uint8(nil), e.zm.Mask.Hi...)
	return &cp
}

// zoneMaskUncomparable counts an env/roll publish that the zone trigger and
// mask CANNOT test (no edge anchor, no per-sample record). The frame still
// publishes — at those bands unqualifiability is structural, not transient,
// and holding would blank the display — but the bypass is COUNTED so the UI
// can say "zone/mask inactive at this timebase" instead of the feature
// silently wearing a clean run's signature (same principle as MaskSkip).
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) zoneMaskUncomparable() {
	if e.zoneMode.Load() == ZoneTrigger {
		e.zoneSkip.Add(1)
	}
	if e.maskMode.Load() != MaskOff {
		e.maskSkip.Add(1)
	}
}

// zonesQualify tests a locked frame against the installed zones. Runs on the
// engine goroutine; f is the producer slot (safe to read). All zones must
// pass (intersect zones must be hit, avoid zones must be missed).
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) zonesQualify(f *sample.Frame, valid int, edgeX, sampleS float64) bool {
	e.zm.Mu.Lock()
	zones := e.zm.Zones
	e.zm.Mu.Unlock()
	if len(zones) == 0 {
		return true
	}
	for i := range zones {
		z := &zones[i]
		sig := f.Codes8(0)
		if z.Ch == 1 {
			sig = f.Codes8(1)
		}
		if len(sig) < valid {
			return false
		}
		lo := int(math.Round(edgeX + z.DtLoS/sampleS))
		hi := int(math.Round(edgeX + z.DtHiS/sampleS))
		if lo > hi {
			lo, hi = hi, lo
		}
		if lo < 0 {
			lo = 0
		}
		if hi >= valid {
			hi = valid - 1
		}
		hit := false
		for s := lo; s <= hi; s++ {
			v := int(sig[s])
			if v >= z.CodeLo && v <= z.CodeHi {
				hit = true
				break
			}
		}
		if hit == z.Avoid { // intersect zone missed, or avoid zone hit
			return false
		}
	}
	return true
}

// maskEval tests a locked frame against the envelope mask, updates counters,
// captures failures into the ring, and reports whether acquisition should
// stop (stop-on-fail). Runs on the engine goroutine.
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) maskEval(f *sample.Frame, valid, liveDepth int, edgeX, sampleS, posFrac float64) (fail, stop bool) {
	mode := int(e.maskMode.Load())
	if mode == MaskOff {
		return false, false
	}
	e.zm.Mu.Lock()
	m := e.zm.Mask
	e.zm.Mu.Unlock()
	if m == nil || m.WinCols <= 0 {
		return false, false
	}
	// Identity guards: a silently-skipping test wears the success signature of a
	// clean run, so every non-comparable frame COUNTS as skipped (Stats.MaskSkip)
	// and the UI can say "mask invalid".
	win := m.WinCols
	frameWin := win // the frame's window, in its own samples
	var mapLo, mapHi []uint8
	if m.FrameIdent {
		var ok bool
		if frameWin, mapLo, mapHi, ok = e.maskFrameWindow(m, f, valid, posFrac); !ok {
			e.maskSkip.Add(1)
			return false, false
		}
	} else if e.band.WinCols() != win ||
		m.TdivS != e.band.TdivS || m.SampleS != e.band.CaptureIntervalNs()*1e-9 {
		e.maskSkip.Add(1)
		return false, false // band changed since the mask was built: not comparable
	}
	if m.VdivKey != 0 && m.VdivKey != e.chVdivBits[m.Ch&1].Load() {
		e.maskSkip.Add(1)
		return false, false // vertical scale changed: code-space mask is stale
	}
	e.mu.Lock()
	off := e.offCode[m.Ch&1]
	e.mu.Unlock()
	if m.OffKey != off {
		e.maskSkip.Add(1)
		return false, false // vertical offset moved: codes shifted under the mask
	}
	if int(e.acqMode.Load()) == AcqAverage {
		// AVERAGE rewrites the published samples AFTER this test point — the
		// verdict would judge data the user never sees. Not comparable.
		e.maskSkip.Add(1)
		return false, false
	}
	sig := f.Codes8(0)
	if m.Ch == 1 {
		sig = f.Codes8(1)
	}
	if len(sig) < valid {
		e.maskSkip.Add(1)
		return false, false
	}
	seq := e.maskCap.Add(1) // this frame IS comparable: it gets a capture ordinal
	// deep drains go dead past validDepth (repeated last sample) — testing the
	// dead tail would judge garbage, so cap the testable range at the live part
	if liveDepth > 0 && liveDepth < valid {
		valid = liveDepth
	}
	// display window mapping (window() semantics): column j reads the raw
	// sample left+j where left anchors the edge at posFrac of the window.
	lo, hi := m.Lo, m.Hi
	if mapLo != nil {
		lo, hi = mapLo, mapHi // entry j bounds the frame's window sample j
	}
	left := int(math.Round(edgeX - float64(frameWin)*posFrac))
	failCol, failCode, failSample := -1, 0, 0
	tested := 0
	for j := range frameWin {
		s := left + j
		if s < 0 || s >= valid {
			continue // off-record columns are not testable (rail-extend on display)
		}
		if lo[j] > hi[j] {
			continue // no mask column covers this sample's time
		}
		tested++
		v := sig[s]
		if v < lo[j] || v > hi[j] {
			failCol, failCode, failSample = j, int(v), s
			break
		}
	}
	if failCol < 0 {
		if tested == 0 {
			// the whole window fell off the record / into the dead tail: a
			// zero-column "pass" wears a clean run's signature — count a skip
			e.maskSkip.Add(1)
			return false, false
		}
		e.maskPass.Add(1)
		return false, false
	}
	e.maskFail.Add(1)
	// capture the failing frame into the ring (copy NOW — the arena slot is
	// reused by the next drain)
	e.zm.Mu.Lock()
	if !e.zm.EpochOK {
		e.zm.T0 = e.clk.Now()
		e.zm.EpochOK = true
	}
	mf := MaskFail{
		C1: append([]uint8(nil), f.Codes8(0)[:valid]...), Valid: valid,
		Seq: seq, EdgeX: edgeX, SampleS: sampleS, WinCols: frameWin,
		AtNs:    int64(e.clk.Now().Sub(e.zm.T0)),
		FailCol: failCol, FailCode: failCode, FailSample: failSample,
	}
	if len(f.Codes8(1)) >= valid {
		mf.C2 = append([]uint8(nil), f.Codes8(1)[:valid]...)
	}
	e.zm.Ring = append(e.zm.Ring, mf)
	if len(e.zm.Ring) > core.MaskRingCap {
		e.zm.Ring = e.zm.Ring[len(e.zm.Ring)-core.MaskRingCap:]
	}
	e.zm.Mu.Unlock()
	return true, mode == MaskStopFail
}

// maskFrameWindow is the window of frame f a frame-identified mask m tests,
// in f's own samples, and the mask read through trigger-relative time when f
// samples the screen otherwise (mapLo nil when it does not). ok is false
// when f is not comparable with m.
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) maskFrameWindow(m *Mask, f *sample.Frame, valid int, posFrac float64) (w int, mapLo, mapHi []uint8, ok bool) {
	w = f.WinPoints
	if w <= 0 || w > valid {
		w = valid
	}
	if m.TdivS != f.Snapshot.TdivS {
		return w, nil, nil, false // another timebase: not comparable
	}
	if w == m.WinCols && m.SampleS == f.SampleS && m.PeakDetect == (f.Content == sample.PeakPairs) {
		return w, nil, nil, true
	}
	// The same screen sampled otherwise - a full-rate record while a view
	// holds a lease, the live envelope after one: the mask is read through
	// trigger-relative time, as the web draws it. Skipped, a mask built
	// during a lease would test nothing once the lease ends.
	if mapLo, mapHi = e.maskThroughTime(m, w, f.SampleS, f.Content == sample.PeakPairs, posFrac); mapLo == nil {
		return w, nil, nil, false // a window of another span: not comparable
	}
	return w, mapLo, mapHi, true
}

// maskThroughTime reads mask m for a frame window of w samples at sampleS
// (peak: min/max pairs) through trigger-relative time: entry i bounds the
// window's sample i by the mask columns its time can fall in - the rounding of
// both windows' left edges, and for a min or max anywhere in its pair's
// bucket. An entry no column covers is inverted (lo > hi): untestable. It
// returns nil for a window spanning another time than the mask's.
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) maskThroughTime(m *Mask, w int, sampleS float64, peak bool, posFrac float64) (lo, hi []uint8) {
	cols := min(m.WinCols, len(m.Lo), len(m.Hi))
	if w <= 0 || cols <= 0 || !(sampleS > 0) || !(m.SampleS > 0) {
		return nil, nil
	}
	// The same screen in both: windows round to whole samples (an envelope's to
	// half a bucket).
	span := float64(m.WinCols) * m.SampleS
	if math.Abs(float64(w)*sampleS-span) > 0.01*span+0.5*math.Max(sampleS, m.SampleS) {
		return nil, nil
	}
	key := core.MaskMapKey{Mask: m, W: w, SampleS: sampleS, Peak: peak, PosFrac: posFrac}
	if e.maskMap.Key == key {
		return e.maskMap.Lo, e.maskMap.Hi
	}
	ratio := sampleS / m.SampleS // mask columns per frame sample
	reach := 1.0                 // half a sample of extent and half of left-edge rounding
	if peak {
		reach = 2.0
	}
	lo, hi = make([]uint8, w), make([]uint8, w)
	for i := range w {
		jc := (float64(i)-float64(w)*posFrac)*ratio + float64(m.WinCols)*posFrac
		a := max(int(math.Floor(jc-reach*ratio-0.5)), 0)
		b := min(int(math.Ceil(jc+reach*ratio+0.5)), cols-1)
		l, h := uint8(255), uint8(0)
		for j := a; j <= b; j++ {
			l, h = min(l, m.Lo[j]), max(h, m.Hi[j])
		}
		lo[i], hi[i] = l, h
	}
	e.maskMap = core.MaskMapState{Key: key, Lo: lo, Hi: hi}
	return lo, hi
}
