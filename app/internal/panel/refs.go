// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
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
	has       bool
	show      bool
}

// RefWave is the render-side view of a saved reference.
// TRLC-LINKS: REQ-SDS-138
type RefWave struct {
	C1, C2    []uint8    // RefCols display codes, nil if the channel was absent
	Vdiv, Off [2]float64 // per channel: analog V/div and offset volts at the save
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
	st := c.eng.Snapshot()
	r := refWave{has: true, show: true, vdiv: [2]float64{1, 1}}
	cpl := [2]int{}
	if c.fe != nil {
		idx, _ := c.fe.Snapshot()
		for ch, code := range [2]uint16{st.OffC1, st.OffC2} {
			r.vdiv[ch] = analog.AnalogVdiv(idx[ch])
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
	ok := false
	c.frameFn(func(f *engine.Frame) {
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
		r.c1 = screenColumns(analog.CoupleDisplay(f.C1[:valid], cpl[0]), left, win, f.Interp)
		if len(f.C2) >= valid {
			r.c2 = screenColumns(analog.CoupleDisplay(f.C2[:valid], cpl[1]), left, win, f.Interp)
		}
		ok = true
	})
	if !ok {
		return
	}
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

// RefView returns a copy-free snapshot of both reference slots for the renderer
// (the slices are immutable once captured).
// TRLC-LINKS: REQ-SDS-138
func (c *Controller) RefView() [2]RefWave {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out [2]RefWave
	for i, r := range c.refs {
		if r.has {
			out[i] = RefWave{C1: r.c1, C2: r.c2, Vdiv: r.vdiv, Off: r.off, Show: r.show}
		}
	}
	return out
}
