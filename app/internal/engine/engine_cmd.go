// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"open-sds/app/internal/iface"
	"time"
)

// serviceCommands flushes staged work at the frame boundary — the engine is
// armed+filling here, never inside a halt window. Snapshot+clear under the
// mutex; bus writes with it released. Order: diagnostic Exec requests, the
// LED latch, the offset DACs, then the trigger words.
// TRLC-LINKS: REQ-SDS-001
func (e *Engine) serviceCommands() {
	stage := time.Now()
	e.serviceExec()
	if e.decodedSession != nil {
		e.decodedService.maxExec = max(e.decodedService.maxExec, time.Since(stage))
	}
	if e.sram != nil {
		stage = time.Now()
		e.serviceDecodedRequests()
		if e.decodedSession != nil {
			e.decodedService.maxRequests = max(e.decodedService.maxRequests, time.Since(stage))
		}
		e.pumpDecodedEvents()
	}

	stage = time.Now()
	e.mu.Lock()
	trigDirty, code := e.trigDirty, e.trigCode
	offDirty, offCode := e.offDirty, e.offCode
	ledDirty, ledWord := e.ledDirty, e.ledWord
	e.trigDirty = false
	e.offDirty = [2]bool{}
	e.ledDirty = false
	e.mu.Unlock()

	// LED latch strobe (MAX V CS3 0x09..0x0b): one indivisible 4-write burst,
	// never interleaved with any other CS3 write.
	if ledDirty {
		e.w3(0x0b, 0)
		e.w3(0x0a, ledWord>>8)
		e.w3(0x09, ledWord&0xff)
		e.w3(0x0b, 1)
	}

	// Vertical offset (MAX V CS3 DACs): low byte, then self-latching high byte.
	if offDirty[0] {
		e.w3(cs3OffC1Lo, offCode[0]&0xff)
		e.w3(cs3OffC1Hi, offCode[0]>>8)
	}
	if offDirty[1] {
		e.w3(cs3OffC2Lo, offCode[1]&0xff)
		e.w3(cs3OffC2Hi, offCode[1]>>8)
	}

	// Trigger: the MAX V comparator DAC quad (both lanes the same code, high
	// bytes self-latch) keeps the A12 comparator threshold current for
	// TRIG_LEVEL.HW_SEL; the fabric's own words (ACQ_CTRL source/slope,
	// TRIG_LEVEL in sample codes) follow on any change of level, source,
	// slope, V/div or offset — compare-on-change — and a change re-arms so
	// the running capture picks it up.
	if trigDirty {
		lo, hi := code&0xff, code>>8
		e.w3(cs3LevelALo, lo)
		e.w3(cs3LevelAHi, hi)
		e.w3(cs3LevelBLo, lo)
		e.w3(cs3LevelBHi, hi)
	}
	if e.sram != nil {
		if e.decodedSession != nil {
			e.decodedService.maxControl = max(e.decodedService.maxControl, time.Since(stage))
		}
		return
	}
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
