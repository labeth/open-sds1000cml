// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-dso-core/webserver/wire"
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
	s.sc.NoteCmd(req.Name, req.Value)
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
	case "vdiv1", "vdiv2":
		if s.fe == nil {
			ok, errStr = false, "vertical front end unavailable"
			break
		}
		ch := 0
		if req.Name == "vdiv2" {
			ch = 1
		}
		var refused *hal.Refusal
		if err := s.fe.SetVdivV(ch, req.Value); errors.As(err, &refused) {
			ok, errStr = false, "vdiv not in ladder"
			break
		} else if err != nil {
			ok, errStr = false, err.Error()
			break
		}
		applied = s.fe.VdivV(ch)
	case "offset1", "offset2":
		// Outer sanity guard only; the per-tier offset law clamps to the real
		// ±1.6 V (×1) / ±40 V (×25) authority and readback reports the applied V.
		if req.Value < -40 || req.Value > 40 {
			ok, errStr = false, "offset out of range"
			break
		}
		ch := 0
		if req.Name == "offset2" {
			ch = 1
		}
		// The board stages the DAC; the front end re-anchors it on V/div
		// changes.
		applied = s.lv.SetOffset(ch, req.Value)
	case "probe1", "probe2":
		if s.fe == nil {
			ok, errStr = false, "vertical front end unavailable"
			break
		}
		x := req.Value
		if x != 1 && x != 10 && x != 100 {
			ok, errStr = false, "probe must be 1, 10 or 100"
			break
		}
		ch := 0
		if req.Name == "probe2" {
			ch = 1
		}
		s.fe.SetProbe(ch, x)
		applied = x
	case "coupling1", "coupling2":
		if s.fe == nil {
			ok, errStr = false, "vertical front end unavailable"
			break
		}
		mode := int(req.Value)
		if mode < analog.CplDC || mode > analog.CplGND {
			ok, errStr = false, "coupling must be 0 (DC), 1 (AC) or 2 (GND)"
			break
		}
		ch := 0
		if req.Name == "coupling2" {
			ch = 1
		}
		if err := s.fe.SetCoupling(ch, mode); err != nil {
			ok, errStr = false, err.Error()
			break
		}
		applied = req.Value
	case "bwl1", "bwl2":
		// 20 MHz bandwidth limit: 1 engages the relay, 0 releases it.
		bw, can := s.fe.(interface{ SetBWL(ch int, on bool) error })
		if s.fe == nil || !can {
			ok, errStr = false, "bandwidth limit unavailable"
			break
		}
		ch := 0
		if req.Name == "bwl2" {
			ch = 1
		}
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

// triggers is the engine's zone and protocol triggers.
type triggers struct{ s *Scope }

// SetZones installs the zone-trigger rectangles (seconds from the edge by
// display codes); an empty list clears them.
// TRLC-LINKS: REQ-SDS-162
func (t triggers) SetZones(zs []wire.Zone) {
	out := make([]engine.Zone, 0, len(zs))
	for _, z := range zs {
		out = append(out, engine.Zone{DtLoS: z.DtLoS, DtHiS: z.DtHiS, CodeLo: z.CodeLo, CodeHi: z.CodeHi, Avoid: z.Avoid, Ch: z.Ch})
	}
	t.s.sc.SetZones(out)
}

// SerialConfig is the protocol trigger as armed (on the LCD, a phone or a
// page) and its mode: a page shows it rather than replace it with its own
// empty match.
// TRLC-LINKS: REQ-SDS-162
func (t triggers) SerialConfig() (any, int, bool) {
	sc, ok := t.s.sc.(interface{ SerialConfig() engine.SerialParams })
	if !ok {
		return nil, 0, false
	}
	return sc.SerialConfig(), t.s.sc.Snapshot().SerialMode, true
}

// SetSerial installs the protocol-trigger configuration (engine
// SerialParams: {proto,chA,chB,baud,cpol,cpha,msb,addr,rw,bytes}). Arm and
// disarm are separate, via /api/set {control:"serialmode"}. Match values
// retain the protocol's word width. The SPI clock rate, the data delay and
// the receiver stay the scope's: its BUS rows set them (bus.spi_clock,
// bus.spi_delay, bus.receiver, the one path for the LCD, a phone and a
// page), so a page's post, one running an older build's scripts too, never
// replaces them.
// TRLC-LINKS: REQ-SDS-162
func (t triggers) SetSerial(decode func(v any) error) error {
	s := t.s
	var p engine.SerialParams
	if err := decode(&p); err != nil {
		return err
	}
	p.Proto = clampI(p.Proto, 0, engine.SerialProtocols-1) // 0 off … 10 flexray, 11 lin … 30 wiegand … 52 IR multi
	p.ChA, p.ChB = p.ChA&1, p.ChB&1
	p.RW = clampI(p.RW, 0, 2)
	if p.Addr > 127 || p.Addr < -1 {
		p.Addr = -1 // out of 7-bit range → "any" (never a silent never-match)
	}
	if p.Baud < 0 || p.Baud > 64_000_000 { // the fast image's UART receiver: 62.5 MBd
		p.Baud = 0
	}
	if p.Bits != 0 {
		hi := 16
		switch p.Proto {
		case 21, 30: // SIRC's and Wiegand's frame length
			hi = 64
		case 34, 35, 44, 52: // an RC code's bits, Oregon's version, the PJON mode, the IR multi protocol
			hi = 64
		case 41: // a T55xx command's bits
			hi = 70
		}
		p.Bits = clampI(p.Bits, 1, hi)
	}
	if len(p.Bytes) > 64 { // a match pattern longer than a record is pointless
		p.Bytes = p.Bytes[:64]
	}
	maxValue := 255
	switch p.Proto {
	case 1, 4: // UART / Manchester configurable data word
		if p.Bits > 0 {
			maxValue = (1 << p.Bits) - 1
		}
	case 5: // SENT nibble
		maxValue = 15
	case 7: // MIL-STD-1553 word
		maxValue = 65535
	case 8: // ARINC 429 data field
		maxValue = 0x7ffff
	case 29, 30: // MDIO header and data, Wiegand fields
		maxValue = 0xffff
	case 26: // an S/PDIF sample
		maxValue = 0xffffff
	case 28: // an SWD data word's low 31 bits
		maxValue = 0x7fffffff
	case 31, 32: // an encoder's or a stepper's step direction
		maxValue = 1
	case 35: // an Oregon nibble
		maxValue = 15
	case 52: // an IR multi address or command
		maxValue = 0xffff
	case 49: // a cJTAG scan's low 31 bits
		maxValue = 0x7fffffff
	}
	for i := range p.Bytes {
		p.Bytes[i] = clampI(p.Bytes[i], 0, maxValue)
	}
	if sc, ok := s.sc.(interface{ SerialConfig() engine.SerialParams }); ok {
		cur := sc.SerialConfig()
		p.SPIClockHz, p.SPIDelayNs, p.Fine = cur.SPIClockHz, cur.SPIDelayNs, cur.Fine
	}
	s.sc.SetSerialParams(p)
	return nil
}

// SerialProtocols lists the protocols the trigger's images take.
// TRLC-LINKS: REQ-SDS-162
func (t triggers) SerialProtocols() any { return engine.SerialDecoderList() }

// TRLC-LINKS: REQ-SDS-162
func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// masks is the engine's mask test.
type masks struct{ s *Scope }

// SetMask installs a mask built on the page (display-window columns, win =
// the engine's WinCols at build time); with the frame's timebase and
// sample interval it compares only with frames of that identity.
// TRLC-LINKS: REQ-SDS-162
func (m masks) SetMask(mk wire.Mask) {
	m.s.sc.SetMask(&engine.Mask{
		Lo: mk.Lo, Hi: mk.Hi, WinCols: mk.WinCols, Ch: mk.Ch,
		FrameIdent: mk.TdivS > 0 && mk.SampleS > 0, TdivS: mk.TdivS, SampleS: mk.SampleS, PeakDetect: mk.PeakDetect,
	})
}

// ClearMask removes the mask with its test and counts (clearMask).
// TRLC-LINKS: REQ-SDS-014, REQ-SDS-139
func (m masks) ClearMask() { m.s.clearMask() }

// MaskGen names the mask installed (Stats.MaskGen).
// TRLC-LINKS: REQ-SDS-162
func (m masks) MaskGen() uint64 { return m.s.sc.Snapshot().MaskGen }

// MaskFails is the ring of captured failing frames, most recent last.
// TRLC-LINKS: REQ-SDS-162
func (m masks) MaskFails() []wire.MaskFail {
	ring := m.s.sc.MaskFails()
	out := make([]wire.MaskFail, len(ring))
	for i, f := range ring {
		out[i] = wire.MaskFail{
			C1: f.C1, C2: f.C2, Valid: f.Valid, Seq: f.Seq, EdgeX: f.EdgeX, SampleS: f.SampleS, WinCols: f.WinCols,
			AtNs: f.AtNs, FailCol: f.FailCol, FailCode: f.FailCode, FailSample: f.FailSample,
		}
	}
	return out
}

// VertScales are the channels' offset volts and volts per code now
// (vertScales).
// TRLC-LINKS: REQ-SDS-164
func (m masks) VertScales() (off, vpc [2]float64) { return m.s.vertScales() }

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
