// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// Settings applies /api/set's controls to the engine and the front end.
// TRLC-LINKS: REQ-SDS-162
func (s *Scope) Settings() webserver.Settings { return controlSetter{s} }

// controlSetter is the scope's /api/set.
type controlSetter struct{ s *Scope }

// Set applies one control (Scope.set).
// TRLC-LINKS: REQ-SDS-162
func (st controlSetter) Set(ctx context.Context, c webserver.Control) webserver.Applied {
	return st.s.set(ctx, c)
}

// set applies one control: logged for the acquisition checker, then staged
// on the engine or the front end; a control the scope cannot take is
// refused with the reason.
// TRLC-LINKS: REQ-SDS-162
func (s *Scope) set(ctx context.Context, req webserver.Control) webserver.Applied { //nolint:gocyclo,funlen // /api/set: one case per control
	// Instrumentation (realtime acquisition checker): log what we did so the
	// acq log can be read against the command that preceded it.
	name := req.Name
	if req.V1 != "" {
		name = req.V1
	}
	s.sc.NoteCmd(name, req.Value)
	if webserver.PerChannel[req.Name] && (req.Ch < 0 || req.Ch > 1) {
		return webserver.Applied{Err: fmt.Sprintf("%s: channel %d is not one of this scope's two", req.Name, req.Ch)}
	}
	ok, applied, errStr := true, req.Value, ""
	switch req.Name {
	case "run":
		s.endWebCapture(ctx)
		s.sc.SetRunning(req.Value != 0)
	case "runtoggle":
		s.endWebCapture(ctx)
		// A single capture can stop between browser status polls. Toggle the
		// current instrument state, not the browser's stale running flag.
		on := !s.sc.Snapshot().Running
		s.sc.SetRunning(on)
		applied = 0
		if on {
			applied = 1
		}
	case "norm":
		s.sc.SetNorm(req.Value != 0)
	case "tdiv":
		if b, found := s.sc.SetTdiv(req.Value); found {
			applied = b.TdivS
		} else {
			ok, errStr = false, "tdiv not in ladder"
		}
	case "triglevelcode":
		// Validate in float space: an out-of-range float→uint16 conversion
		// is implementation-defined (and differs between amd64 and ARMv7).
		if req.Value < 0 || req.Value > 65535 {
			ok, errStr = false, "triglevelcode out of range"
			break
		}
		applied = float64(s.lv.SetTrigLevelCode(uint16(req.Value)))
	case "trigslope":
		s.sc.SetTrigSlope(req.Value != 0)
	case "ets":
		s.sc.SetETS(req.Value != 0)
		// The SRAM fabric has no equivalent-time mode and the engine keeps it
		// off; report that instead of a silent success.
		if req.Value != 0 && !s.sc.Snapshot().ETS {
			ok, errStr = false, "equivalent-time sampling is not available on this acquisition fabric"
			applied = 0
		}
	case "single":
		s.endWebCapture(ctx)
		s.sc.SetSingle()
	case "trigpos":
		s.sc.SetTrigPosFrac(req.Value)
	case "memdepth":
		return webserver.Applied{OK: true, Value: s.sc.SetMemDepth(int(req.Value)), Brief: true}
	case "frameperiod":
		return webserver.Applied{OK: true, Value: s.sc.SetFramePeriod(int(req.Value)), Brief: true}
	case "decodeview":
		// Accepted for older pages: decoding needs no lease now, the live
		// envelope is time-ordered (engine orderEnvelope).
		return webserver.Applied{OK: true, Value: 0, Brief: true}
	case "rollstream":
		// Roll on the stream image (gap-free; an image reload when roll starts
		// or ends) instead of chunked captures.
		if sc, ok := s.sc.(interface{ SetRollStream(bool) }); ok {
			sc.SetRollStream(req.Value != 0)
			return webserver.Applied{OK: true, Value: req.Value != 0, Brief: true}
		}
		return webserver.Applied{Err: "unsupported"}
	case "holdoff":
		return webserver.Applied{OK: true, Value: s.sc.SetHoldoff(req.Value), Brief: true}
	case "stream":
		return webserver.Applied{OK: true, Value: s.sc.SetStreamMode(req.Value != 0), Brief: true}
	case "serialmode":
		s.sc.SetSerialMode(int(req.Value))
	case "zonemode":
		s.sc.SetZoneMode(int(req.Value))
	case "maskmode":
		s.sc.SetMaskMode(int(req.Value))
	case "maskclear":
		s.sc.ClearMaskFails()
	case "bodemode":
		// value: 0 off, else on; ref/dut channels come in via Lo/Hi (0=C1,1=C2)
		s.sc.SetBodeMode(req.Value != 0, int(req.Lo), int(req.Hi))
	case "bodeclear":
		s.sc.ClearBode()
	case "trigtype":
		s.sc.SetTrigType(int(req.Value))
	case "pulseparams":
		s.sc.SetPulseParams(req.Lvl, req.Min, req.Max, req.Cond)
	case "slopeparams":
		s.sc.SetSlopeParams(req.Lo, req.Hi, req.Min, req.Max, req.Cond)
	case "videoparams":
		s.sc.SetVideoParams(req.Std, req.Line, req.Neg)
	case "acqmode":
		s.sc.SetAcqMode(int(req.Value))
	case "precisionrate":
		if sc, supported := s.sc.(interface{ SetPrecisionRate(float64) float64 }); supported {
			applied = sc.SetPrecisionRate(req.Value)
		} else {
			ok = false
			errStr = "precision sample rate unavailable"
		}
	case "avgcount":
		s.sc.SetAvgCount(int(req.Value))
	case "eres":
		s.sc.SetEresLen(int(req.Value))
	case "eresbits":
		s.sc.SetEresLen(engine.EresLenForBits(req.Value))
	case "trigsource":
		ch := 0
		if req.Value == 1 {
			ch = 1
		}
		s.sc.SetTrigSource(ch)
	case "vdiv":
		if s.fe == nil {
			ok, errStr = false, "vertical front end unavailable"
			break
		}
		ch := req.Ch
		var refused *hal.Refusal
		if err := s.fe.SetVdivV(ch, req.Value); errors.As(err, &refused) {
			ok, errStr = false, "vdiv not in ladder"
			break
		} else if err != nil {
			ok, errStr = false, err.Error()
			break
		}
		applied = s.fe.VdivV(ch)
	case "offset":
		// Outer sanity guard only; the per-tier offset law clamps to the real
		// ±1.6 V (×1) / ±40 V (×25) authority and readback reports the applied V.
		if req.Value < -40 || req.Value > 40 {
			ok, errStr = false, "offset out of range"
			break
		}
		ch := req.Ch
		// The board stages the DAC; the front end re-anchors it on V/div
		// changes.
		applied = s.lv.SetOffset(ch, req.Value)
	case "probe":
		if s.fe == nil {
			ok, errStr = false, "vertical front end unavailable"
			break
		}
		x := req.Value
		if x != 1 && x != 10 && x != 100 {
			ok, errStr = false, "probe must be 1, 10 or 100"
			break
		}
		ch := req.Ch
		s.fe.SetProbe(ch, x)
		applied = x
	case "coupling":
		if s.fe == nil {
			ok, errStr = false, "vertical front end unavailable"
			break
		}
		mode := int(req.Value)
		if mode < analog.CplDC || mode > analog.CplGND {
			ok, errStr = false, "coupling must be 0 (DC), 1 (AC) or 2 (GND)"
			break
		}
		ch := req.Ch
		if err := s.fe.SetCoupling(ch, mode); err != nil {
			ok, errStr = false, err.Error()
			break
		}
		applied = req.Value
	case "bwlimit":
		// 20 MHz bandwidth limit: 1 engages the relay, 0 releases it.
		bw, can := s.fe.(interface{ SetBWL(ch int, on bool) error })
		if s.fe == nil || !can {
			ok, errStr = false, "bandwidth limit unavailable"
			break
		}
		ch := req.Ch
		if err := bw.SetBWL(ch, req.Value != 0); err != nil {
			ok, errStr = false, err.Error()
			break
		}
		applied = req.Value
	default:
		ok, errStr = false, "unknown control"
	}
	return webserver.Applied{OK: ok, Value: applied, Err: errStr}
}

// endWebCapture ends a web page's decoded capture before Run, Run/Stop or
// Single from a page, as the scope's run-control keys do: they always get the
// acquisition back, also from a page closed mid-session.
// TRLC-LINKS: REQ-SDS-013
func (s *Scope) endWebCapture(ctx context.Context) {
	e, ok := s.sc.(interface {
		EndWebDecodedCapture(context.Context) (bool, error)
	})
	if !ok || !s.sc.Snapshot().DecodedWeb {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := e.EndWebDecodedCapture(ctx); err != nil {
		log.Printf("web: ending the web page's decoded capture: %v", err)
	}
}

// serialTrigger is the engine's protocol trigger as the web scope takes it.
type serialTrigger struct{ s *Scope }

// SerialConfig is the configuration as armed, where the engine reads it
// back.
// TRLC-LINKS: REQ-SDS-162
func (t serialTrigger) SerialConfig() (engine.SerialParams, bool) {
	sc, ok := t.s.sc.(interface{ SerialConfig() engine.SerialParams })
	if !ok {
		return engine.SerialParams{}, false
	}
	return sc.SerialConfig(), true
}

// TRLC-LINKS: REQ-SDS-162
func (t serialTrigger) SetSerialParams(p engine.SerialParams) { t.s.sc.SetSerialParams(p) }

// TRLC-LINKS: REQ-SDS-162
func (t serialTrigger) SerialMode() int { return t.s.sc.Snapshot().SerialMode }

// maskTest is the engine's mask test as the web scope takes it.
type maskTest struct{ s *Scope }

// TRLC-LINKS: REQ-SDS-162
func (m maskTest) SetMask(mk *engine.Mask) { m.s.sc.SetMask(mk) }

// ClearMask removes the mask with its test and counts (clearMask).
// TRLC-LINKS: REQ-SDS-014, REQ-SDS-139
func (m maskTest) ClearMask() { m.s.clearMask() }

// MaskGen names the mask installed (Stats.MaskGen).
// TRLC-LINKS: REQ-SDS-162
func (m maskTest) MaskGen() uint64 { return m.s.sc.Snapshot().MaskGen }

// TRLC-LINKS: REQ-SDS-162
func (m maskTest) MaskFails() []engine.MaskFail { return m.s.sc.MaskFails() }

// clearMask removes the mask with its test and counts: through the panel
// where there is one, which also stops a mask build or master stack under
// way on the LCD, else in the engine.
// TRLC-LINKS: REQ-SDS-014, REQ-SDS-139
func (s *Scope) clearMask() {
	if p, ok := s.panel.(interface{ ClearMask() }); ok {
		p.ClearMask()
		return
	}
	s.sc.SetMaskMode(0)
	s.sc.SetMask(nil)
	s.sc.ClearMaskFails()
}
