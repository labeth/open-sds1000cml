// ENGMODEL-OWNER-UNIT: FU-APP-SCPI

package scpi

import (
	"fmt"
	"math"

	"github.com/labeth/open-dso-core/sample"
	core "github.com/labeth/open-dso-core/scpi"
	"github.com/labeth/open-dso-core/scpi/meas"
)

// findEdges is a channel's mid-swing crossings on the 8-bit view.
// TRLC-LINKS: REQ-SDS-024
func findEdges(c []uint8) *meas.Edges { return meas.FindEdges(c) }

// mead answers Ca-Cb:MEAD? <param>: edge delays between the two channels on
// the published frame (PHA in degrees over A's period, SKEW between the
// rising edges nearest the trigger).
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) mead(c *core.Call, a, b int, arg string) error {
	if !meas.IsDelay(arg) {
		return c.Error(core.ErrHeader)
	}
	value, ok := 0.0, false
	h.sc.WithFrame(func(f *sample.Frame) {
		if f == nil || a >= len(f.Traces) || b >= len(f.Traces) {
			return
		}
		// Both channels' edges on the 8-bit view.
		value, ok = meas.Delay(arg, view8(&f.Traces[a]), view8(&f.Traces[b]), f.Len(), f.SampleS, f.TriggerAt)
	})
	unit := "S"
	if arg == "PHA" {
		unit = ""
	}
	v := meas.NoReading
	if ok {
		v = fmt.Sprintf("%.6E%s", value, unit)
	}
	c.Reply(fmt.Sprintf("C%d-C%d:MEAD", a+1, b+1), arg+","+v)
	return nil
}

// cursorReader is a Display whose manual cursors can be read (CRVA?).
type cursorReader interface {
	CursorState() (on bool, typ int, x, y [2]float64)
}

// crva answers Cn:CRVA? HREL|VREL from the panel's manual cursors, in the
// LCD readout's own mapping (10 time divisions, 8 voltage divisions at the
// displayed V/div): HREL = dt, 1/dt and the channel's volts at both time
// cursors; VREL = dV and both cursor levels. OFF when the cursors are off.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) crva(c *core.Call, ch int, arg string) error {
	header := chHeader(ch, "CRVA")
	if arg != "HREL" && arg != "VREL" {
		return c.Error(core.ErrHeader)
	}
	cr, ok := h.disp.(cursorReader)
	if !ok {
		return c.Error(core.ErrDataOutOfRange)
	}
	on, _, x, y := cr.CursorState()
	if !on {
		c.Reply(header, "OFF")
		return nil
	}
	probe := h.probe(ch)
	vdiv, dispVdiv := 1.0, 1.0
	st := h.sc.Snapshot()
	offV := 0.0
	if h.fe != nil {
		vdiv, dispVdiv = h.fe.RangeV(ch&1), h.fe.VdivV(ch&1)
		offV = h.lv.OffsetIn(ch, st)
	}
	if arg == "VREL" {
		level := func(fy float64) float64 { return (0.5-fy)*8*dispVdiv*probe - offV*probe }
		v0, v1 := level(y[0]), level(y[1])
		c.Reply(header, fmt.Sprintf("VREL,%s,%s,%s", sciV(v1-v0), sciV(v0), sciV(v1)))
		return nil
	}
	tdiv, posFrac := st.TdivS, st.TrigPosFrac
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = .5
	}
	dt := (x[1] - x[0]) * 10 * tdiv
	val := [2]string{meas.NoReading, meas.NoReading}
	h.sc.WithFrame(func(f *sample.Frame) {
		if f == nil || f.Len() < 2 || f.WinPoints < 2 || ch >= len(f.Traces) {
			return
		}
		t := &f.Traces[ch]
		sig, cpd := view8(t), t.CodesPerDiv8()
		if len(sig) < f.Len() {
			return
		}
		centre := f.TriggerAt
		if centre < 0 {
			centre = float64(f.Len()) * posFrac
		}
		for k := range val {
			i := int(math.Round(centre + (x[k]-posFrac)*float64(f.WinPoints)))
			if i >= 0 && i < f.Len() {
				val[k] = sciV((float64(sig[i])-sample.Mid8)*vdiv/cpd*probe - offV*probe)
			}
		}
	})
	inv := meas.NoReading
	if dt != 0 {
		inv = fmt.Sprintf("%.2EHz", 1/math.Abs(dt))
	}
	c.Reply(header, fmt.Sprintf("HREL,%s,%s,%s,%s", sciS(dt), inv, val[0], val[1]))
	return nil
}

// view8 is trace t's codes on the 8-bit view: 8-bit codes as they are (their
// whole slice), wider words mapped.
// TRLC-LINKS: REQ-SDS-024
func view8(t *sample.Trace) []uint8 {
	if t.Format.Storage == sample.U16 {
		return t.View8()
	}
	return t.Codes8
}
