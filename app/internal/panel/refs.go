// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// RefCols is a reference's width: one point per screen column.
const RefCols = 800

// refWave is a saved snapshot of both channels as they were drawn, overlaid
// dim for comparison (parity with the web REF A/B). Screen-space: it holds
// the 800 columns on screen at the save, so it lines up while the timebase is
// unchanged — the bench "save a good trace, tweak, compare". It keeps the
// volts it was saved at: Vdiv/Off are each channel's analog V/div and offset
// then, so a later V/div or position change redraws it at the same volts.
// TRLC-LINKS: REQ-SDS-138
type refWave struct {
	c1, c2    []uint8
	vdiv, off [2]float64
	tdiv      float64    // the timebase it was stored at (it lines up only there)
	posFrac   float64    // where the trigger sat across the screen then
	probe     [2]float64 // each channel's probe factor then (1 without a front end)
	has       bool
	show      bool
	setup     cmpSetup // the setup it was stored at, for AUTO in COMPARE
}

// RefExport is a saved reference as the web app takes it (GET /api/refs):
// its screen columns, the volts they were saved at, the timebase and where
// the trigger sat, so a page can overlay the scope's REF A (the master).
type RefExport struct {
	Has     bool       `json:"has"`
	Show    bool       `json:"show"`
	Cols    int        `json:"cols"`
	C1      []int      `json:"c1,omitempty"`
	C2      []int      `json:"c2,omitempty"`
	Vdiv    [2]float64 `json:"vdiv"` // at the probe tip, as the web's volts per code
	Off     [2]float64 `json:"off"`  // at the probe tip
	TdivS   float64    `json:"tdiv_s"`
	PosFrac float64    `json:"pos_frac"`
}

// RefWave is the render-side view of a saved reference.
// TRLC-LINKS: REQ-SDS-138
type RefWave struct {
	C1, C2    []uint8    // RefCols display codes, nil if the channel was absent
	Vdiv, Off [2]float64 // per channel: analog V/div and offset volts at the save
	Probe     [2]float64 // per channel: the probe factor at the save
	Show      bool
}

// captureRef snapshots the current screen into reference slot (0=A, 1=B):
// the same window the LCD draws (trigger at the trigger position, one point
// per column), not the frame's record from its start. Saving a stopped full
// record kept its first samples, which the overlay then drew as a flat line.
// TRLC-LINKS: REQ-SDS-138
func (c *Controller) captureRef(slot int) {
	if c.frameFn == nil || slot < 0 || slot > 1 {
		return
	}
	c.frameFn(func(f *engine.Frame) { c.captureRefFrom(f, slot) })
}

// captureRefFrom stores frame f's screen into reference slot (0=A, 1=B); the
// master stores the averaged frame it is handed, inside the frame lock.
// TRLC-LINKS: REQ-SDS-138, REQ-SDS-218
func (c *Controller) captureRefFrom(f *engine.Frame, slot int) {
	if slot < 0 || slot > 1 {
		return
	}
	st := c.eng.Snapshot()
	r := refWave{has: true, show: true, vdiv: [2]float64{1, 1}, tdiv: st.TdivS, probe: [2]float64{1, 1}}
	cpl := [2]int{}
	if c.fe != nil {
		idx, _ := c.fe.Snapshot()
		for ch, code := range [2]uint16{st.OffC1, st.OffC2} {
			r.vdiv[ch] = analog.AnalogVdiv(idx[ch])
			r.probe[ch] = max(c.fe.ProbeFactor(ch), 1)
			cpl[ch] = c.fe.Coupling(ch)
			if code != 0 && cpl[ch] != analog.CplAC { // as the HUD's offset
				r.off[ch] = c.fe.OffsetVolts(ch, code)
			}
		}
	}
	posFrac := st.TrigPosFrac
	if !(posFrac > 0 && posFrac <= 1) {
		posFrac = .5
	}
	if f == nil || len(f.C1) == 0 || f.IsEnv {
		return
	}
	valid := min(max(f.Valid, 1), len(f.C1))
	win := f.WinCols
	if win <= 0 || win > valid {
		win = valid
	}
	xc := f.EdgeX
	if xc < 0 {
		xc = float64(valid) / 2
	}
	left := xc - float64(win)*posFrac
	r.posFrac = posFrac
	r.c1 = screenColumns(analog.CoupleDisplay(f.C1[:valid], cpl[0]), left, win, f.Interp)
	if len(f.C2) >= valid {
		r.c2 = screenColumns(analog.CoupleDisplay(f.C2[:valid], cpl[1]), left, win, f.Interp)
	}
	r.setup = c.takeSetup()
	c.mu.Lock()
	c.refs[slot] = r
	c.mu.Unlock()
}

// screenColumns samples sig at the LCD trace's column positions (drawTrace):
// the record's nearest end beyond it, linear interpolation when interp.
// TRLC-LINKS: REQ-SDS-138
func screenColumns(sig []uint8, left float64, win int, interp bool) []uint8 {
	n := len(sig)
	out := make([]uint8, RefCols)
	for x := range out {
		pos := left + float64(x)*float64(win)/RefCols
		pos = min(max(pos, 0), float64(n-1))
		i := int(pos)
		v := float64(sig[i])
		if interp && i+1 < n {
			frac := pos - float64(i)
			v = v*(1-frac) + float64(sig[i+1])*frac
		}
		out[x] = uint8(v + .5)
	}
	return out
}

// RefsExport is both reference slots for the web app (GET /api/refs).
// TRLC-LINKS: REQ-SDS-138, REQ-SDS-218
func (c *Controller) RefsExport() [2]RefExport {
	ints := func(b []uint8) []int {
		if b == nil {
			return nil
		}
		out := make([]int, len(b))
		for i, v := range b {
			out[i] = int(v)
		}
		return out
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out [2]RefExport
	for i, r := range c.refs {
		if r.has {
			e := RefExport{Has: true, Show: r.show, Cols: RefCols, C1: ints(r.c1), C2: ints(r.c2), TdivS: r.tdiv, PosFrac: r.posFrac}
			for ch := range e.Vdiv {
				p := max(r.probe[ch], 1)
				e.Vdiv[ch], e.Off[ch] = r.vdiv[ch]*p, r.off[ch]*p
			}
			out[i] = e
		}
	}
	return out
}

// RefView returns a copy-free snapshot of both reference slots for the renderer
// (the slices are immutable once captured).
// TRLC-LINKS: REQ-SDS-138
func (c *Controller) RefView() [2]RefWave {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out [2]RefWave
	for i, r := range c.refs {
		if r.has {
			out[i] = RefWave{C1: r.c1, C2: r.c2, Vdiv: r.vdiv, Off: r.off, Probe: r.probe, Show: r.show}
		}
	}
	return out
}

// cmpSetup is the setup a reference or a mask was taken at, so AUTO in
// COMPARE (REQ-SDS-224) can put the live trace back on it: the timebase and
// trigger position, each channel's detent and offset, and the trigger.
type cmpSetup struct {
	has      bool
	tdiv     float64
	posFrac  float64
	vidx     [2]int
	offReq   [2]float64
	trigCode uint16
	trigSrc  int
	rising   bool
	trigType int // a hardware type (edge, pulse, slope, video); a serial trigger is kept as an edge
}

// takeSetup reads the setup in use.
// TRLC-LINKS: REQ-SDS-138, REQ-SDS-224
func (c *Controller) takeSetup() cmpSetup {
	st := c.eng.Snapshot()
	s := cmpSetup{has: true, tdiv: st.TdivSet(), posFrac: st.TrigPosFrac, trigCode: st.TrigCode,
		trigSrc: st.TrigSource & 1, rising: st.TrigRising, trigType: st.TrigType & 3}
	if st.SerialMode != 0 {
		s.trigType = 0
	}
	if c.fe != nil {
		s.vidx, _ = c.fe.Snapshot()
		s.offReq = [2]float64{c.fe.OffsetReqV(0), c.fe.OffsetReqV(1)}
	}
	return s
}

// applySetup puts the scope back on setup s, running in AUTO trigger mode as
// the autoset leaves it.
// TRLC-LINKS: REQ-SDS-224
func (c *Controller) applySetup(s cmpSetup) {
	c.eng.SetNorm(false)
	c.eng.SetRunning(true)
	c.mu.Lock()
	c.norm, c.running = false, true
	c.mu.Unlock()
	if st := c.eng.Snapshot(); st.SerialMode != 0 || st.TrigType != s.trigType {
		c.setTrigTypeIndex(st, s.trigType)
	}
	c.setTdivNearest(s.tdiv)
	if s.posFrac > 0 {
		_ = c.setTrigPos(s.posFrac)
	}
	if c.fe != nil {
		for ch := 0; ch < 2; ch++ {
			c.setVdiv(ch, s.vidx[ch])
			c.fe.SetOffset(ch, s.offReq[ch])
		}
	}
	c.eng.SetTrigSource(s.trigSrc)
	c.eng.SetTrigSlope(s.rising)
	if s.trigCode != 0 {
		c.eng.SetTrigLevelCode(s.trigCode)
		c.mu.Lock()
		c.trigCode = s.trigCode
		c.mu.Unlock()
	}
	c.pushLEDs()
}

// noteMaskSetup keeps the setup a mask was installed at, with the engine's
// mask generation that names it.
// TRLC-LINKS: REQ-SDS-139, REQ-SDS-224
func (c *Controller) noteMaskSetup() {
	s := c.takeSetup()
	gen := c.eng.Snapshot().MaskGen
	c.mu.Lock()
	c.maskSetup, c.maskSetupGen = s, gen
	c.mu.Unlock()
}

// compareAuto is AUTO in COMPARE with something to compare against: the
// setup of the mask built here when it is still the one installed, else of
// REF A, else of REF B, so the live trace sits on it. It says what it took;
// ok is false with none (AUTO then frames as in SCOPE).
// TRLC-LINKS: REQ-SDS-224
func (c *Controller) compareAuto() (string, bool) {
	st := c.eng.Snapshot()
	c.mu.Lock()
	var s cmpSetup
	name := ""
	switch {
	case st.MaskSet && c.maskSetup.has && c.maskSetupGen == st.MaskGen:
		s, name = c.maskSetup, "the mask's"
	case c.refs[0].has && c.refs[0].setup.has:
		s, name = c.refs[0].setup, "REF A's"
	case c.refs[1].has && c.refs[1].setup.has:
		s, name = c.refs[1].setup, "REF B's"
	}
	c.mu.Unlock()
	if !s.has {
		return "", false
	}
	c.applySetup(s)
	note := fmt.Sprintf("AUTO (COMPARE): %s setup, %s/div", name, si(c.eng.Snapshot().TdivSet(), "s"))
	if c.fe != nil {
		note += fmt.Sprintf(", C1 %s/div, C2 %s/div", si(c.vdivAt(0), "V"), si(c.vdivAt(1), "V"))
	}
	return note, true
}
