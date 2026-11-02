// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
)

// codesOf returns a channel's valid samples as codes (Q8.8 when the frame
// carries them), with the display coupling applied.
// TRLC-LINKS: REQ-SDS-215
func codesOf(f *engine.Frame, ch, lo, hi, cpl int) []float64 {
	valid := framecalc.Valid(f)
	lo, hi = max(lo, 0), min(hi, valid)
	if hi <= lo {
		return nil
	}
	sig := framecalc.Channel(f, ch, valid)
	if cpl != analog.CplDC {
		sig = analog.CoupleDisplay(sig, cpl)
	}
	q := f.Q1
	if ch == 1 {
		q = f.Q2
	}
	out := make([]float64, hi-lo)
	if cpl == analog.CplDC && len(q) >= valid {
		for i, v := range q[lo:hi] {
			out[i] = float64(v) / 256
		}
		return out
	}
	for i, v := range sig[lo:hi] {
		out[i] = float64(v)
	}
	return out
}
