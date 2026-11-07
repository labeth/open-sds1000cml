// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"time"

	"github.com/labeth/open-sds1000cml/app/internal/iface"
)

// flushLEDs writes a staged lamp word (the board's panel plane). Besides
// the frame boundary it runs between SRAM recall bursts, so a key press
// lights its lamp at once instead of after a multi-second deep recall.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-135
func (e *Engine) flushLEDs() { e.hw().FlushLamps() }

// serviceCommands flushes staged work at the frame boundary — the engine is
// armed+filling here, never inside a halt window: the decoded stream's
// requests and events, then the board's housekeeping (its panel plane: the
// LED latch, the offset DACs, the trigger level DAC), then on the register
// path the trigger words.
// TRLC-LINKS: REQ-SDS-001
func (e *Engine) serviceCommands() {
	// The stages are timed for a decoded session's service figures only:
	// without one a frame reads no clock here.
	var stage time.Time
	if e.sram != nil {
		if e.decodedSession != nil {
			stage = time.Now()
		}
		e.serviceDecodedRequests()
		if e.decodedSession != nil {
			e.decodedService.maxRequests = max(e.decodedService.maxRequests, time.Since(stage))
		}
		e.pumpDecodedEvents()
		e.expireDecoded()
	}

	if e.decodedSession != nil {
		stage = time.Now()
	}
	// The panel plane's staged words: the lamp latch, the offset DACs, the
	// trigger level DAC (the board's commit-point housekeeping).
	if e.sram != nil {
		e.platform().Service()
		if e.decodedSession != nil {
			e.decodedService.maxControl = max(e.decodedService.maxControl, time.Since(stage))
		}
		return
	}
	trigDirty, code := e.hw().FlushPanel()
	if e.flushTrigWords(false) && e.running.Load() {
		e.armEngine()
		if trigDirty {
			e.logf("engine: trigger recommitted, code=%#04x level=%d acq=%#04x", code,
				e.trigShadow&iface.TrigLevelLevelMask, e.acqShadow)
		}
	}
}

// TRLC-LINKS: REQ-SDS-009, REQ-SDS-010
func (e *Engine) syncBandStatsLocked() {
	e.stats.TdivS = e.band.TdivS
	e.stats.DisplayedS = e.band.DisplayedSdivS()
	switch e.band.Kind() {
	case KindNativeFast:
		e.stats.BandKind = "native-fast"
	case KindDecimated:
		e.stats.BandKind = "decimated"
	case KindEnvelope:
		e.stats.BandKind = "envelope"
	case KindRoll:
		e.stats.BandKind = "roll"
	}
	if e.band.Kind() == KindRoll {
		e.stats.HaltMode = "stream"
	} else {
		e.stats.HaltMode = "capture-halt"
	}
}
