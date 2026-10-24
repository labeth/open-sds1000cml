// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"context"
	"fmt"
	"math"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
)

// Instrument actions: the one setter path each setting has, shared by the
// sheet rows, the knobs and pushes, the workspace tools and the remote API.
// They run on the panel goroutine (or a test) without c.mu held.

// setTrigLevelV puts the trigger level at v volts (probe tip).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setTrigLevelV(v float64) error {
	src := c.eng.Snapshot().TrigSource & 1
	codeF := 31437 - 911*v
	if c.fe != nil {
		codeF = c.fe.TrigCode(v, src)
	}
	code := uint16(min(max(math.Round(codeF), engine.TrigCodeMin), engine.TrigCodeMax))
	c.mu.Lock()
	c.trigCode = code
	c.mu.Unlock()
	c.eng.SetTrigLevelCode(code)
	return nil
}

// stepTrigLevel moves the level n knob steps (positive raises it).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-213
func (c *Controller) stepTrigLevel(n int) error {
	var st engine.Stats
	idx, ok := c.resyncInto(&st)
	step := int(panelTrigStepCodesPerVdiv)
	switch {
	case !ok: // nothing read: the step reads them itself
		step = c.trigStepCodes()
	case c.fe != nil: // trigStepCodes on what resync read
		step = trigStepCodesAt(idx, st.TrigSource&1)
	}
	nc := clampInt(int(c.trigCode)-n*step, engine.TrigCodeMin, engine.TrigCodeMax)
	c.mu.Lock()
	c.trigCode = uint16(nc)
	c.mu.Unlock()
	c.eng.SetTrigLevelCode(uint16(nc))
	return nil
}

// zeroTrigLevel is the Level push: the trigger level to 0 V.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) zeroTrigLevel() error { return c.setTrigLevelV(0) }

// setNorm sets the trigger mode (Normal waits for a trigger).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setNorm(on bool) error {
	c.eng.SetNorm(on)
	c.mu.Lock()
	c.norm = on
	c.mu.Unlock()
	return nil
}

// setTrigType sets Edge, Pulse, Slope, Video or Serial. Serial needs a bus
// in the decoder settings: decode off refuses with a notice.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-013
func (c *Controller) setTrigType(v int) error {
	st := c.eng.Snapshot()
	if v == trigSerial {
		if c.serialTrig() == nil {
			return fmt.Errorf("no serial trigger in this engine")
		}
		return c.armSerial()
	}
	c.setTrigTypeIndex(st, clampInt(v, 0, 3))
	return nil
}

// armSerial switches the trigger to Serial on the bus the decoder settings
// name, as they stand: typed in or filled in by Auto.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-214, REQ-SDS-222
func (c *Controller) armSerial() error {
	c.mu.Lock()
	proto := c.decProto
	c.mu.Unlock()
	if proto == framecalc.ProtoOff {
		c.toast("Serial trigger needs a bus: BUS workspace (F2) finds it, or set Protocol", 2200*time.Millisecond)
		return fmt.Errorf("the serial trigger needs a bus: decode is off")
	}
	if !serialProto(proto) {
		c.toast("The serial trigger needs a bus (decoding "+protoName(proto)+")", 2000*time.Millisecond)
		return fmt.Errorf("the serial trigger needs a bus, not %s", protoName(proto))
	}
	c.setTrigTypeIndex(c.eng.Snapshot(), trigSerial)
	return nil
}

// snapProto reads the decode protocol.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) snapProto() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.decProto
}

// setHoldoff sets the trigger holdoff (0 = off), as the engine clamps it.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setHoldoff(v float64) error {
	c.eng.SetHoldoff(v)
	return nil
}

// force triggers once now.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) force() error {
	if f, ok := c.eng.(interface{ ForceTrigger() }); ok {
		f.ForceTrigger()
	}
	return nil
}

// editPulse changes the pulse qualifier, keeping min < max.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) editPulse(fn func(), maxEdited bool) error {
	st := c.eng.Snapshot()
	c.mu.Lock()
	c.adoptQual(&st)
	fn()
	c.pulseLvl = clampF(c.pulseLvl, 0.05, 0.95)
	c.pulseMin, c.pulseMax = orderNs(c.pulseMin, c.pulseMax, maxEdited)
	pl, pmn, pmx, pc := c.pulseLvl, c.pulseMin, c.pulseMax, c.pulseCond
	c.mu.Unlock()
	c.eng.SetPulseParams(pl, pmn, pmx, pc)
	return nil
}

// editSlope changes the slope qualifier, keeping low < high and min < max.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) editSlope(fn func(), hiEdited, maxEdited bool) error {
	st := c.eng.Snapshot()
	c.mu.Lock()
	c.adoptQual(&st)
	fn()
	c.slopeLo, c.slopeHi = clampF(c.slopeLo, 0.05, 0.95), clampF(c.slopeHi, 0.05, 0.95)
	c.slopeLo, c.slopeHi = orderFrac(c.slopeLo, c.slopeHi, hiEdited)
	c.slopeMin, c.slopeMax = orderNs(c.slopeMin, c.slopeMax, maxEdited)
	lo, hi, tmn, tmx, sc := c.slopeLo, c.slopeHi, c.slopeMin, c.slopeMax, c.slopeCond
	c.mu.Unlock()
	c.eng.SetSlopeParams(lo, hi, tmn, tmx, sc)
	return nil
}

// editVideo changes the video qualifier.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) editVideo(fn func()) error {
	st := c.eng.Snapshot()
	c.mu.Lock()
	c.adoptQual(&st)
	fn()
	c.videoLine = clampInt(c.videoLine, 0, 625)
	std, ln, neg := c.videoStd, c.videoLine, c.videoNeg
	c.mu.Unlock()
	c.eng.SetVideoParams(std, ln, neg)
	return nil
}

// ---- vertical ----

// setVdivIdx puts a channel on a V/div detent.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setVdivIdx(ch, idx int) error {
	if c.fe == nil {
		return fmt.Errorf("no analog front end")
	}
	idx = clampInt(idx, 0, len(analog.Detents)-1)
	if err := c.fe.SetVdiv(ch, idx); err != nil {
		return err
	}
	c.mu.Lock()
	c.vIdx[ch] = idx
	c.mu.Unlock()
	return nil
}

// stepVdiv moves a channel n detents (positive = larger V/div).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) stepVdiv(ch, n int) error {
	if c.fe == nil {
		return fmt.Errorf("no analog front end")
	}
	idx, _ := c.fe.Snapshot()
	return c.setVdivIdx(ch, idx[ch]+n)
}

// offsetRangeV is the offset's input-referred range on a detent: the coarse
// attenuator sets it (analog offsetTierRangeV: ±1.6 V sensitive, ±40 V
// attenuated).
// TRLC-LINKS: REQ-SDS-212
func offsetRangeV(idx int) float64 {
	if idx >= 0 && idx < len(analog.Detents) && analog.Detents[idx].Atten {
		return 40
	}
	return 1.6
}

// setOffsetTip puts a channel's offset at v volts (probe tip).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setOffsetTip(ch int, v float64) error {
	if c.fe == nil {
		return fmt.Errorf("no analog front end")
	}
	c.fe.SetOffset(ch, v/c.fe.ProbeFactor(ch))
	return nil
}

// stepOffset moves a channel's offset n knob steps (0.2 div each).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-213
func (c *Controller) stepOffset(ch, n int) error {
	if c.fe == nil {
		return nil
	}
	idx, _ := c.fe.Snapshot()
	v := c.fe.OffsetReqV(ch) + float64(n)*panelOffsetStepDiv*analog.Detents[idx[ch]].VdivV
	c.fe.SetOffset(ch, v)
	return nil
}

// setCoupling sets DC, AC or GND.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setCoupling(ch, mode int) error {
	if c.fe == nil {
		return fmt.Errorf("no analog front end")
	}
	return c.fe.SetCoupling(ch, mode)
}

// setProbe sets the probe factor.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setProbe(ch, x int) error {
	if c.fe == nil {
		return fmt.Errorf("no analog front end")
	}
	c.fe.SetProbe(ch, float64(x))
	return nil
}

// setBWL sets the 20 MHz bandwidth limit.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setBWL(ch int, on bool) error {
	b, ok := c.fe.(interface{ SetBWL(ch int, on bool) error })
	if !ok {
		return fmt.Errorf("no bandwidth limit on this front end")
	}
	return b.SetBWL(ch, on)
}

// setInvert sets the display-level trace invert (SCPI Cn:INVS's state).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setInvert(ch int, on bool) error {
	if c.inverter == nil {
		return fmt.Errorf("invert unavailable")
	}
	c.inverter.SetInverted(ch, on)
	return nil
}

// setChanShown shows or hides a channel's trace.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setChanShown(ch int, on bool) error {
	c.mu.Lock()
	c.chDisp[ch&1] = on
	c.mu.Unlock()
	return nil
}

// ---- horizontal ----

// setTdivIdx puts the timebase on a ladder index.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setTdivIdx(i int) error {
	if len(c.tdivs) == 0 {
		return fmt.Errorf("no timebase ladder")
	}
	i = clampInt(i, 0, len(c.tdivs)-1)
	// A packet or line image (a CAN, SENT, Manchester ... trigger) records
	// 2.1 ms: no slower screen shows its record whole.
	if st := c.eng.Snapshot(); st.TdivMaxS > 0 && c.tdivs[i] > st.TdivMaxS*(1+1e-6) {
		for i > 0 && c.tdivs[i] > st.TdivMaxS*(1+1e-6) {
			i--
		}
		c.toast(fmt.Sprintf("%s/div at most on this trigger: its image records %s", si(c.tdivs[i], "s"), si(max(st.RecordS, 10*c.tdivs[i]), "s")), 2500*time.Millisecond)
	}
	c.mu.Lock()
	c.tdivIdx = i
	c.mu.Unlock()
	c.eng.SetTdiv(c.tdivs[i])
	return nil
}

// stepTdiv moves the timebase n ladder steps (positive = slower).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) stepTdiv(n int) error {
	c.resync()
	return c.setTdivIdx(c.tdivIdx + n)
}

// setTrigPos puts the trigger at a fraction of the screen.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setTrigPos(frac float64) error {
	c.eng.SetTrigPosFrac(clampF(frac, 0.02, 1))
	return nil
}

// centreTrigger is the Position push: trigger to the centre, zoom pan to 0.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) centreTrigger() error {
	c.mu.Lock()
	c.zoomOff = 0
	c.mu.Unlock()
	c.eng.SetTrigPosFrac(0.5)
	return nil
}

// zoomFactors are the horizontal zoom steps.
var zoomFactors = []int{1, 2, 5, 10, 20, 50}

// setZoom sets the horizontal zoom (1 = off); off resets the pan.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setZoom(z int) error {
	ok := false
	for _, f := range zoomFactors {
		ok = ok || f == z
	}
	if !ok {
		return fmt.Errorf("zoom %dx is not offered", z)
	}
	c.mu.Lock()
	if c.zoom > 1 {
		c.ui.zoomLast = c.zoom
	}
	c.zoom = z
	if z <= 1 {
		c.zoomOff = 0
	} else {
		c.ui.zoomLast = z
	}
	c.mu.Unlock()
	return nil
}

// setZoomOff pans the zoom window (fraction of the record, -0.5..0.5).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setZoomOff(f float64) error {
	c.mu.Lock()
	c.zoomOff = clampF(f, -0.5, 0.5)
	c.mu.Unlock()
	return nil
}

// setRollStream picks how roll runs (chunked or the gap-free stream image).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setRollStream(on bool) error {
	e, ok := c.eng.(interface{ SetRollStream(bool) })
	if !ok {
		return fmt.Errorf("no roll stream in this engine")
	}
	e.SetRollStream(on)
	return nil
}

// ---- acquisition ----

// setAcqMode sets Normal, Average, ERes, Peak or Precision.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setAcqMode(m int) error {
	c.eng.SetAcqMode(m)
	return nil
}

// setRun is the Acquisition row: 0 run, 1 stop, 2 single.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setRun(v int) error {
	c.endWebCapture()
	switch v {
	case 2:
		c.single1()
	default:
		r := v == 0
		c.runReq.Add(1)
		c.mu.Lock()
		c.running, c.single = r, false
		c.mu.Unlock()
		c.eng.SetRunning(r)
	}
	return nil
}

// endWebCapture ends a decoded capture a web page holds before a run-control
// key or DEFAULT SETUP runs: the scope's keys always get the acquisition back,
// also from a page closed mid-session. The scope's own stream is its RUN/STOP's
// to stop and restart.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-013
func (c *Controller) endWebCapture() {
	e, ok := c.eng.(interface {
		EndWebDecodedCapture(context.Context) (bool, error)
	})
	if !ok || !c.eng.Snapshot().DecodedWeb {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ended, err := e.EndWebDecodedCapture(ctx)
	switch {
	case err != nil:
		c.logf("panel: ending the web page's decoded capture: %v", err)
	case ended:
		c.toast("Ended the web page's decoded capture", 1800*time.Millisecond)
	}
}

// single1 is SINGLE: Normal mode, one triggered capture, then stop.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) single1() {
	c.endWebCapture()
	c.runReq.Add(1)
	c.mu.Lock()
	c.norm, c.running, c.single = true, true, true
	c.mu.Unlock()
	c.eng.SetSingle()
}

// ---- display ----

// setView sets the display view; the active workspace remembers it.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-211
func (c *Controller) setView(v int) error {
	if v < 0 || v >= numViews {
		return fmt.Errorf("no view %d", v)
	}
	c.mu.Lock()
	c.viewMode = v
	c.ui.views[c.ui.ws] = v
	c.mu.Unlock()
	return nil
}

// setMath sets the math function (0 off).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setMath(m int) error {
	c.mu.Lock()
	c.mathMode = mod5(m)
	c.mu.Unlock()
	return nil
}

// ---- references ----

// clearRefs deletes both references.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-138
func (c *Controller) clearRefs() error {
	c.mu.Lock()
	c.refs = [2]refWave{}
	c.ui.hasMaster = false // the master was REF A
	c.mu.Unlock()
	return nil
}

// clearRef deletes one reference (REF A is the master).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-227
func (c *Controller) clearRef(i int) error {
	c.mu.Lock()
	c.refs[i&1] = refWave{}
	if i&1 == 0 {
		c.ui.hasMaster = false
	}
	c.mu.Unlock()
	return nil
}

// showRef shows or hides a stored reference.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-138
func (c *Controller) showRef(i int, on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.refs[i&1].has {
		return fmt.Errorf("reference %c is empty", 'A'+i&1)
	}
	c.refs[i&1].show = on
	return nil
}

// storeRef snapshots the screen into a reference slot.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-138
func (c *Controller) storeRef(i int) error {
	c.captureRef(i & 1)
	c.mu.Lock()
	ok := c.refs[i&1].has
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("no frame to store")
	}
	return nil
}

// ---- mask ----

// setMaskMode sets Off, Test or Stop on fail.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-139
func (c *Controller) setMaskMode(m int) error {
	c.eng.SetMaskMode(clampInt(m, 0, 2))
	c.maskSetMsg("")
	return nil
}

// resetMaskCounters zeroes the pass/fail counts.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-139
func (c *Controller) resetMaskCounters() error {
	c.eng.ClearMaskFails()
	c.maskSetMsg("")
	return nil
}
