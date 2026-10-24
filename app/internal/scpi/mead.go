// ENGMODEL-OWNER-UNIT: FU-APP-SCPI
package scpi

import (
	"fmt"
	"math"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// edges holds a channel's interpolated 50 % crossings (sample positions).
type edges struct{ rise, fall []float64 }

// findEdges slices a channel at mid-swing with 10 % hysteresis, so converter
// noise on a flat level is no edge; nil when the channel has no swing.
// TRLC-LINKS: REQ-SDS-024
func findEdges(c []uint8) *edges {
	if len(c) < 4 {
		return nil
	}
	lo, hi := 255, 0
	for _, v := range c {
		lo, hi = min(lo, int(v)), max(hi, int(v))
	}
	if hi-lo < 8 {
		return nil
	}
	mid := float64(lo+hi) / 2
	hy := float64(hi-lo) / 10
	// The bands as whole codes: a code is past mid+hy exactly when it is at
	// least its ceiling, so the loop compares bytes, not floats.
	up, down := int(math.Ceil(mid+hy)), int(math.Floor(mid-hy))
	e := &edges{}
	state := 0 // unknown until the first excursion past a hysteresis band
	for i := 1; i < len(c); i++ {
		v := int(c[i])
		switch {
		case v >= up && state <= 0:
			if state < 0 {
				e.rise = append(e.rise, crossing(c, i, mid))
			}
			state = 1
		case v <= down && state >= 0:
			if state > 0 {
				e.fall = append(e.fall, crossing(c, i, mid))
			}
			state = -1
		}
	}
	return e
}

// crossing walks back from i to the sample pair straddling level and
// interpolates the crossing position.
// TRLC-LINKS: REQ-SDS-024
func crossing(c []uint8, i int, level float64) float64 {
	for j := i; j > 0; j-- {
		a, b := float64(c[j-1]), float64(c[j])
		if (a-level)*(b-level) <= 0 && a != b {
			return float64(j-1) + (level-a)/(b-a)
		}
	}
	return float64(i)
}

// meadParams are the C1-C2:MEAD? parameters: source A edge -> source B edge.
var meadParams = map[string]struct {
	aRise, bRise, last bool
}{
	"FRR": {true, true, false}, "FRF": {true, false, false}, "FFR": {false, true, false}, "FFF": {false, false, false},
	"LRR": {true, true, true}, "LRF": {true, false, true}, "LFR": {false, true, true}, "LFF": {false, false, true},
}

// mead answers Ca-Cb:MEAD? <param>: edge delays between the two channels on
// the published frame (PHA in degrees over A's period, SKEW between the
// rising edges nearest the trigger).
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) mead(a, b int, arg string) []byte {
	header := fmt.Sprintf("C%d-C%d:MEAD", a+1, b+1)
	p, known := meadParams[arg]
	if !known && arg != "PHA" && arg != "SKEW" {
		return errTok(errHeader)
	}
	value, unit := math.NaN(), "S"
	h.sc.WithFrame(func(f *engine.Frame) {
		if f == nil || f.Valid < 4 || len(f.C1) < f.Valid || len(f.C2) < f.Valid || !(f.SampleS > 0) {
			return
		}
		ch := [2][]uint8{f.C1[:f.Valid], f.C2[:f.Valid]}
		ea, eb := findEdges(ch[a]), findEdges(ch[b])
		if ea == nil || eb == nil {
			return
		}
		pick := func(xs []float64, last bool) (float64, bool) {
			if len(xs) == 0 {
				return 0, false
			}
			if last {
				return xs[len(xs)-1], true
			}
			return xs[0], true
		}
		switch arg {
		case "PHA":
			unit = ""
			if len(ea.rise) < 2 || len(eb.rise) == 0 {
				return
			}
			period := (ea.rise[len(ea.rise)-1] - ea.rise[0]) / float64(len(ea.rise)-1)
			ta := ea.rise[0]
			for _, tb := range eb.rise {
				if tb >= ta-period/2 {
					ph := math.Mod((tb-ta)/period*360, 360)
					if ph > 180 {
						ph -= 360
					} else if ph <= -180 {
						ph += 360
					}
					value = ph
					return
				}
			}
		case "SKEW":
			centre := f.EdgeX
			if centre < 0 {
				centre = float64(f.Valid) / 2
			}
			nearest := func(xs []float64) (float64, bool) {
				best, ok := 0.0, false
				for _, x := range xs {
					if !ok || math.Abs(x-centre) < math.Abs(best-centre) {
						best, ok = x, true
					}
				}
				return best, ok
			}
			ta, okA := nearest(ea.rise)
			tb, okB := nearest(eb.rise)
			if okA && okB {
				value = (tb - ta) * f.SampleS
			}
		default:
			xa, xb := ea.fall, eb.fall
			if p.aRise {
				xa = ea.rise
			}
			if p.bRise {
				xb = eb.rise
			}
			ta, okA := pick(xa, p.last)
			tb, okB := pick(xb, p.last)
			if okA && okB {
				value = (tb - ta) * f.SampleS
			}
		}
	})
	v := "****"
	if !math.IsNaN(value) {
		v = fmt.Sprintf("%.6E%s", value, unit)
	}
	return h.reply(header, arg+","+v)
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
func (h *Handler) crva(ch int, arg string) []byte {
	header := fmt.Sprintf("C%d:CRVA", ch+1)
	if arg != "HREL" && arg != "VREL" {
		return errTok(errHeader)
	}
	cr, ok := h.disp.(cursorReader)
	if !ok {
		return errTok(errOutOfRange)
	}
	on, _, x, y := cr.CursorState()
	if !on {
		return h.reply(header, "OFF")
	}
	probe := h.probe(ch)
	vdiv, dispVdiv := 1.0, 1.0
	if h.fe != nil {
		idx, _ := h.fe.Snapshot()
		i := idx[ch&1]
		vdiv = analog.AnalogVdiv(i)
		if i >= 0 && i < len(analog.Detents) {
			dispVdiv = analog.Detents[i].VdivV
		}
	}
	st := h.sc.Snapshot()
	offCode := st.OffC1
	if ch == 1 {
		offCode = st.OffC2
	}
	offV := 0.0
	if offCode != 0 && h.fe != nil {
		offV = h.fe.OffsetVolts(ch, offCode)
	}
	if arg == "VREL" {
		level := func(fy float64) float64 { return (0.5-fy)*8*dispVdiv*probe - offV*probe }
		v0, v1 := level(y[0]), level(y[1])
		return h.reply(header, fmt.Sprintf("VREL,%s,%s,%s", sciV(v1-v0), sciV(v0), sciV(v1)))
	}
	tdiv, posFrac := st.TdivS, st.TrigPosFrac
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = .5
	}
	dt := (x[1] - x[0]) * 10 * tdiv
	val := [2]string{"****", "****"}
	h.sc.WithFrame(func(f *engine.Frame) {
		if f == nil || f.Valid < 2 || f.WinCols < 2 {
			return
		}
		sig := f.C1
		if ch == 1 {
			sig = f.C2
		}
		if len(sig) < f.Valid {
			return
		}
		centre := f.EdgeX
		if centre < 0 {
			centre = float64(f.Valid) * posFrac
		}
		for k := range val {
			i := int(math.Round(centre + (x[k]-posFrac)*float64(f.WinCols)))
			if i >= 0 && i < f.Valid {
				val[k] = sciV((float64(sig[i])-128)*vdiv/25*probe - offV*probe)
			}
		}
	})
	inv := "****"
	if dt != 0 {
		inv = fmt.Sprintf("%.2EHz", 1/math.Abs(dt))
	}
	return h.reply(header, fmt.Sprintf("HREL,%s,%s,%s,%s", sciS(dt), inv, val[0], val[1]))
}
