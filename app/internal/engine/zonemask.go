package engine

import (
	"math"
	"sync"
	"time"
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

// Zone is one qualification rectangle. Times are SECONDS RELATIVE TO THE
// TRIGGER EDGE (portable across bands); codes are display codes (0..255).
type Zone struct {
	DtLoS, DtHiS   float64
	CodeLo, CodeHi int
	Avoid          bool // true: the frame must NOT enter the zone
	Ch             int  // 0 = C1, 1 = C2
}

// Mask is a per-display-column envelope: a frame FAILS if any sample in
// column j falls outside [Lo[j], Hi[j]]. Columns are the same edge-anchored
// display window the renderer uses (WinCols samples, edge at PosFrac).
type Mask struct {
	Lo, Hi  []uint8
	WinCols int
	Ch      int
	// identity captured at install time: the mask is only comparable while the
	// band (time geometry) and the channel's vertical mapping are unchanged.
	// WinCols alone is NOT an identity — every ≥200 µs/div band clamps to the
	// same 2048 columns while seconds/column differ 10× (review finding).
	TdivS   float64
	SampleS float64
	VdivKey uint64 // channel V/div bits at install (0 = unknown/don't guard)
	OffKey  uint16 // channel offset DAC shadow at install
}

// zone/mask modes
const (
	ZoneOff     = 0
	ZoneTrigger = 1

	MaskOff      = 0
	MaskTest     = 1
	MaskStopFail = 2

	zoneFallback = 60 // AUTO liveness: publish an unqualified frame every N holds
	maskRingCap  = 8
)

// MaskFail is one captured failing frame (ring entry).
type MaskFail struct {
	C1, C2 []uint8
	Valid  int
	// Seq is the mask CAPTURE ordinal (the Nth tested frame), not the publish
	// sequence: held frames are tested too, and stamping them with the next
	// publish seq gave every hold-window failure the same — and wrong —
	// identity (found by the publish-policy breaker).
	Seq        uint64
	EdgeX      float64
	SampleS    float64
	WinCols    int
	AtNs       int64 // monotonic capture time (engine clock)
	FailCol    int   // first violating display column
	FailCode   int   // the violating sample value
	FailSample int   // raw sample index of the violation
}

type zoneMaskState struct {
	// LOCK ORDER: e.mu is acquired BEFORE zm.mu (Snapshot nests that way).
	// Never take e.mu while holding zm.mu — that is an ABBA deadlock against
	// any status poll (found by the concurrency chaos test).
	mu      sync.Mutex
	zones   []Zone
	mask    *Mask
	ring    []MaskFail
	t0      time.Time // engine-clock epoch for AtNs
	epochOK bool
}

// SetZones installs the qualification zones (nil/empty disables the test but
// not the mode). Copies the slice.
func (e *Engine) SetZones(z []Zone) {
	e.zm.mu.Lock()
	e.zm.zones = append([]Zone(nil), z...)
	e.zm.mu.Unlock()
}

// SetZoneMode switches the zone trigger (ZoneOff/ZoneTrigger).
func (e *Engine) SetZoneMode(m int) {
	if m != ZoneOff && m != ZoneTrigger {
		m = ZoneOff
	}
	e.zoneMode.Store(int32(m))
}

// SetMask installs the envelope mask (nil clears). Copies the envelopes.
// Identity is stamped BEFORE taking zm.mu — e.mu inside zm.mu would invert
// the lock order against Snapshot (e.mu -> zm.mu) and deadlock.
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
		cp.OffKey = e.offCode[m.Ch&1]
		e.mu.Unlock()
	}
	e.zm.mu.Lock()
	e.zm.mask = cp
	e.zm.mu.Unlock()
}

// SetMaskMode switches mask testing (MaskOff/MaskTest/MaskStopFail) and
// resets the running counters when turning on.
func (e *Engine) SetMaskMode(m int) {
	if m < MaskOff || m > MaskStopFail {
		m = MaskOff
	}
	if m != MaskOff && int(e.maskMode.Load()) == MaskOff {
		e.maskPass.Store(0)
		e.maskFail.Store(0)
	}
	if m != MaskStopFail {
		e.maskStopped.Store(false) // leaving stop-on-fail releases the latch
	}
	e.maskMode.Store(int32(m))
}

// ClearMaskFails empties the failure ring and counters.
func (e *Engine) ClearMaskFails() {
	e.zm.mu.Lock()
	e.zm.ring = nil
	e.zm.mu.Unlock()
	e.maskPass.Store(0)
	e.maskFail.Store(0)
	e.maskStopped.Store(false)
}

// MaskFails returns a snapshot of the failure ring (most recent last).
func (e *Engine) MaskFails() []MaskFail {
	e.zm.mu.Lock()
	defer e.zm.mu.Unlock()
	return append([]MaskFail(nil), e.zm.ring...)
}

// Zones returns a copy of the installed zones (render/UI readers).
func (e *Engine) Zones() []Zone {
	e.zm.mu.Lock()
	defer e.zm.mu.Unlock()
	return append([]Zone(nil), e.zm.zones...)
}

// MaskEnvelope returns a copy of the installed mask (nil if none) for the
// LCD/web renderers.
func (e *Engine) MaskEnvelope() *Mask {
	e.zm.mu.Lock()
	defer e.zm.mu.Unlock()
	if e.zm.mask == nil {
		return nil
	}
	cp := *e.zm.mask
	cp.Lo = append([]uint8(nil), e.zm.mask.Lo...)
	cp.Hi = append([]uint8(nil), e.zm.mask.Hi...)
	return &cp
}

// zoneMaskUncomparable counts an env/roll publish that the zone trigger and
// mask CANNOT test (no edge anchor, no per-sample record). The frame still
// publishes — at those bands unqualifiability is structural, not transient,
// and holding would blank the display — but the bypass is COUNTED so the UI
// can say "zone/mask inactive at this timebase" instead of the feature
// silently wearing a clean run's signature (same principle as MaskSkip).
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
func (e *Engine) zonesQualify(f *Frame, valid int, edgeX, sampleS float64) bool {
	e.zm.mu.Lock()
	zones := e.zm.zones
	e.zm.mu.Unlock()
	if len(zones) == 0 {
		return true
	}
	for i := range zones {
		z := &zones[i]
		sig := f.C1
		if z.Ch == 1 {
			sig = f.C2
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
func (e *Engine) maskEval(f *Frame, valid, liveDepth int, edgeX, sampleS, posFrac float64) (fail, stop bool) {
	mode := int(e.maskMode.Load())
	if mode == MaskOff {
		return false, false
	}
	e.zm.mu.Lock()
	m := e.zm.mask
	e.zm.mu.Unlock()
	if m == nil || m.WinCols <= 0 {
		return false, false
	}
	// Identity guards: a silently-skipping test wears the success signature of a
	// clean run, so every non-comparable frame COUNTS as skipped (Stats.MaskSkip)
	// and the UI can say "mask invalid".
	win := m.WinCols
	if e.band.WinCols() != win ||
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
	sig := f.C1
	if m.Ch == 1 {
		sig = f.C2
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
	left := int(math.Round(edgeX - float64(win)*posFrac))
	failCol, failCode, failSample := -1, 0, 0
	tested := 0
	for j := 0; j < win; j++ {
		s := left + j
		if s < 0 || s >= valid {
			continue // off-record columns are not testable (rail-extend on display)
		}
		tested++
		v := sig[s]
		if v < m.Lo[j] || v > m.Hi[j] {
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
	e.zm.mu.Lock()
	if !e.zm.epochOK {
		e.zm.t0 = e.clk.Now()
		e.zm.epochOK = true
	}
	mf := MaskFail{
		C1: append([]uint8(nil), f.C1[:valid]...), Valid: valid,
		Seq: seq, EdgeX: edgeX, SampleS: sampleS, WinCols: win,
		AtNs:    int64(e.clk.Now().Sub(e.zm.t0)),
		FailCol: failCol, FailCode: failCode, FailSample: failSample,
	}
	if len(f.C2) >= valid {
		mf.C2 = append([]uint8(nil), f.C2[:valid]...)
	}
	e.zm.ring = append(e.zm.ring, mf)
	if len(e.zm.ring) > maskRingCap {
		e.zm.ring = e.zm.ring[len(e.zm.ring)-maskRingCap:]
	}
	e.zm.mu.Unlock()
	return true, mode == MaskStopFail
}

// BuildMaskFromEnvelope dilates a per-column [lo,hi] envelope by ±tolCols
// horizontally and ±tolCodes vertically — the standard mask morphology. The
// input envelopes must be winCols long.
func BuildMaskFromEnvelope(lo, hi []uint8, winCols, tolCols, tolCodes, ch int) *Mask {
	if len(lo) != winCols || len(hi) != winCols || winCols <= 0 {
		return nil
	}
	// Columns NEVER OBSERVED during the build (per-frame edge position moves the
	// window; its fringes may go uncovered) still carry the accumulator's
	// initial lo>hi. Left alone, dilation turns them into inverted-garbage
	// bounds that fail every sample (found by the 50-wave breaker: env [247,8]).
	// An unobserved column is UNTESTABLE: normalize to the always-pass [0,255]
	// before dilating.
	nLo := make([]uint8, winCols)
	nHi := make([]uint8, winCols)
	for j := 0; j < winCols; j++ {
		if lo[j] > hi[j] {
			nLo[j], nHi[j] = 0, 255
		} else {
			nLo[j], nHi[j] = lo[j], hi[j]
		}
	}
	lo, hi = nLo, nHi
	outLo := make([]uint8, winCols)
	outHi := make([]uint8, winCols)
	for j := 0; j < winCols; j++ {
		mn, mx := 255, 0
		a, b := j-tolCols, j+tolCols
		if a < 0 {
			a = 0
		}
		if b >= winCols {
			b = winCols - 1
		}
		for k := a; k <= b; k++ {
			if int(lo[k]) < mn {
				mn = int(lo[k])
			}
			if int(hi[k]) > mx {
				mx = int(hi[k])
			}
		}
		mn -= tolCodes
		mx += tolCodes
		if mn < 0 {
			mn = 0
		}
		if mx > 255 {
			mx = 255
		}
		outLo[j] = uint8(mn)
		outHi[j] = uint8(mx)
	}
	return &Mask{Lo: outLo, Hi: outHi, WinCols: winCols, Ch: ch}
}
