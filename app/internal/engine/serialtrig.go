// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

// Serial / protocol trigger: publish only frames whose decoded UART/I2C/SPI
// stream contains an operator-specified byte or address pattern, anchored on the
// match. It is a publish-gate (like the zone trigger, zonemask.go) so it composes
// with whatever edge/qualifier trigger is active, and it runs in the engine — so
// it works headless (LCD-only) and for SINGLE/NORM, not just in the browser.
//
// The decoder (internal/decode) is pure — it takes raw uint8 codes + the
// per-sample interval — so the engine can call it directly (no import cycle; only
// panel imports engine). The trigger carries its OWN decode config, independent
// of the LCD decode strip's config (which lives on the panel).

import (
	"math"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
)

// SetSerialParams installs the match config (copies Bytes).
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) SetSerialParams(p SerialParams) {
	p.Bytes = append([]int(nil), p.Bytes...)
	p.Pattern = append([]PatternElem(nil), p.Pattern...)
	e.ser.Mu.Lock()
	e.ser.Params = p
	e.ser.Mu.Unlock()
	e.arincAux.Store(0)
	e.serLv.ShiftS.Store(0)
}

// SerialConfig is the serial trigger's current decode settings and match
// (a copy), so the panel edits what is armed, whoever armed it.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) SerialConfig() SerialParams {
	e.ser.Mu.Lock()
	p := e.ser.Params
	e.ser.Mu.Unlock()
	p.Bytes = append([]int(nil), p.Bytes...)
	p.Pattern = append([]PatternElem(nil), p.Pattern...)
	return p
}

// SetSerialMode arms/disarms the serial trigger (SerialOff/SerialTrigger).
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) SetSerialMode(m int) {
	if m != SerialOff && m != SerialTrigger {
		m = SerialOff
	}
	if m != SerialOff && int(e.serialMode.Load()) == SerialOff {
		e.serialMatches.Store(0) // reset the running count on the off→on edge
	}
	e.serialMode.Store(int32(m))
}

// autoGateLiveness is AUTO's rule for a capture the armed serial or zone gate
// rejected, in the SRAM loop (engine_loop.go applies the same rule): hold
// it, but let a liveness frame through (autoLivenessDue). With no gate armed
// every capture publishes. Engine goroutine only.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) autoGateLiveness() bool {
	switch {
	case e.serialMode.Load() == SerialTrigger:
		return e.autoLivenessDue(&e.serialHeld, core.SerialFallback)
	case e.zoneMode.Load() == ZoneTrigger:
		return e.autoLivenessDue(&e.zoneHeld, core.ZoneFallback)
	}
	return true
}

// autoLivenessDue counts one capture a gate held in AUTO (held its counter)
// and reports a liveness frame due: every fallback holds, or autoLiveMax
// after the last frame published (none yet: at once). Engine goroutine only.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) autoLivenessDue(held *int, fallback int) bool {
	*held++
	if *held < fallback {
		e.mu.Lock()
		last := e.lastPublish
		e.mu.Unlock()
		if !last.IsZero() && e.clk.Now().Sub(last) < core.AutoLiveMax {
			return false
		}
	}
	*held = 0
	return true
}

// serialQualify decodes the captured record with the armed serial config and
// reports whether the target pattern is present, plus the record SAMPLE index to
// anchor the display on (-1 = don't re-anchor). Engine goroutine; f is the
// producer slot. It runs on EVERY publish candidate while armed — async UART in
// AUTO never edge-locks, so (unlike the zone gate) it must NOT be gated on lock.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) serialQualify(f *sample.Frame, valid int, sampleS float64) (bool, int) {
	return e.serialQualifyAt(f, valid, sampleS, core.AnchorSpan{Target: -1, Lo: math.MinInt, Hi: math.MaxInt})
}

// serialQualifyAt is serialQualify choosing among the record's matches by at.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) serialQualifyAt(f *sample.Frame, valid int, sampleS float64, at core.AnchorSpan) (bool, int) {
	e.ser.Mu.Lock()
	p := e.ser.Params
	e.ser.Mu.Unlock()
	if p.Empty() {
		return true, -1 // armed but unconfigured → pass through, don't re-anchor
	}
	if valid < 8 || !(sampleS > 0) { // !(>0) also rejects NaN (every NaN compare is false)
		return false, -1
	}
	chA, chB := f.Codes8(0), f.Codes8(0)
	if p.ChA == 1 {
		chA = f.Codes8(1)
	}
	if p.ChB == 1 {
		chB = f.Codes8(1)
	}
	if len(chA) < valid || len(chB) < valid {
		return false, -1
	}
	return core.SerialMatchIn(chA[:valid], chB[:valid], sampleS, p, at)
}
