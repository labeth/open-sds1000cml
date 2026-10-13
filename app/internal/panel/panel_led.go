// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"time"

	"open-sds/app/internal/panel/framecalc"
)

// Lamps (spec 08 §8.2): only the confirmed lamps, each with one meaning,
// sheet open or not: the key's function is in use. Measure: readouts on.
// Acquire: acquisition not Normal, or super-res on. Display: a view other
// than Y-T, or persistence. Utility: decoding. Save/Recall: the settings are
// unchanged since the last save or recall. 1 and 2: the channel is shown.
// Single: armed. Run/Stop: green running, amber waiting for a trigger
// (Normal or Single), red stopped. Math: a math trace is on. Ref: REF A or
// REF B is shown. Cursors: the cursors are on. INTENSITY stays dark (no
// firmware has lit it, so its latch bit is not confirmed).

// lampWord is the latch word for state s.
// TRLC-LINKS: REQ-SDS-135, REQ-SDS-211
func (c *Controller) lampWord(s *sv) uint16 {
	var w uint16
	if s.chDisp[0] {
		w |= ledCH1
	}
	if s.chDisp[1] {
		w |= ledCH2
	}
	if s.mathMode != 0 {
		w |= ledMath
	}
	if s.refHas[0] && s.refShow[0] || s.refHas[1] && s.refShow[1] {
		w |= ledRef
	}
	if s.curOn {
		w |= ledCursors
	}
	switch {
	case !s.st.Running:
		w |= ledStop
	case s.st.Single || c.waitingForTrigger(s):
		w |= ledRun | ledStop // both elements: amber
	default:
		w |= ledRun
	}
	if s.st.Single && s.st.Running {
		w |= ledSingle
	}
	if s.showMeas && len(s.meas) > 0 {
		w |= ledMeasure
	}
	if s.st.AcqMode != 0 || s.srActive {
		w |= ledAcquire
	}
	if s.viewMode != viewYT || s.persist {
		w |= ledDisplay
	}
	if s.decProto != framecalc.ProtoOff {
		w |= ledUtility
	}
	if c.sameAsSavedSetup(s) {
		w |= ledSaveRec
	}
	return w
}

// waitingForTrigger reports Normal mode, running, with nothing published
// lately: the held frame's trigger state no longer describes now.
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) waitingForTrigger(s *sv) bool {
	sp, ok := c.eng.(interface{ SincePublish() time.Duration })
	if !ok || !s.st.Norm || !s.st.Running {
		return false
	}
	return sp.SincePublish() > time.Duration(math.Max(1, 25*s.st.TdivS)*float64(time.Second))
}

// setupPrint is a fingerprint of the settings a setup holds (for the
// Save/Recall lamp).
// TRLC-LINKS: REQ-SDS-136
func setupPrint(s *sv) string {
	return fmt.Sprint(s.st.TdivSet(), s.st.TrigSource, s.st.TrigRising, s.st.TrigCode, s.st.TrigType, s.st.Norm, s.st.HoldoffS,
		s.st.TrigPosFrac, s.st.AcqMode, s.st.AvgCount, s.st.EresLen, s.vidx, s.offV, s.cpl, s.probe, s.bwl,
		s.chDisp, s.showMeas, s.persist, s.viewMode, s.mathMode, s.decProto, s.decBaud, s.decChA, s.decChB,
		s.decCPOL, s.decCPHA, s.decFormat, s.meas, s.fftSrc, s.ui.ws)
}

// SyncLEDs refreshes the lamps from the engine and the panel state and
// re-latches only when the word changed, so the lamps follow a single-shot
// self-stopping or the web/SCPI toggling run without a key press; it also
// publishes the panel snapshot for the remote's long-poll, at most every
// syncPublishEvery (an input publishes at once).
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) SyncLEDs() {
	st := c.eng.Snapshot()
	c.mu.Lock()
	c.running, c.norm, c.single = st.Running, st.Norm, st.Single
	c.mu.Unlock()
	c.pushLEDs()
	c.rmu.Lock()
	at := c.remote.pubAt
	c.rmu.Unlock()
	if now := c.now(); at.IsZero() || now.Sub(at) >= syncPublishEvery || now.Before(at) {
		c.publish()
	}
}

// syncPublishEvery bounds the periodic publish: hashing every sheet row and
// encoding the snapshot 45 times a second took a tenth of the CPU (bug hunt
// #3, B3).
const syncPublishEvery = 150 * time.Millisecond

// pushLEDs latches the lamp word when it changed.
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) pushLEDs() {
	w := c.lampWord(c.snap())
	c.mu.Lock()
	changed := !c.ledSet || w != c.ledWord
	c.ledWord, c.ledSet = w, true
	c.mu.Unlock()
	if changed {
		c.eng.SetLEDs(w)
	}
}

// TRLC-LINKS: REQ-SDS-135
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
