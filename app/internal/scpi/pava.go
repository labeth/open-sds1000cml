// ENGMODEL-OWNER-UNIT: FU-APP-SCPI
package scpi

import (
	"fmt"
	"math"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
)

// liveFront is the front end's live probe and coupling, when it offers them:
// the panel and web UI change both without touching this handler's shadows.
type liveFront interface {
	ProbeFactor(ch int) float64
	Coupling(ch int) int
}

// probe returns a channel's probe factor, live when the front end has it.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) probe(ch int) float64 {
	if lf, ok := h.fe.(liveFront); ok {
		return lf.ProbeFactor(ch)
	}
	if h.attn[ch&1] > 0 {
		return h.attn[ch&1]
	}
	return 1
}

// coupling returns a channel's coupling mode, live when the front end has it.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) coupling(ch int) int {
	if lf, ok := h.fe.(liveFront); ok {
		return lf.Coupling(ch)
	}
	switch h.cpl[ch&1] {
	case "A1M", "A50":
		return analog.CplAC
	case "GND":
		return analog.CplGND
	}
	return analog.CplDC
}

// pavaParam maps a Cn:PAVA? parameter to its value and unit. timing marks
// the parameters that need a resolved edge; edge, those that need its shape,
// which a peak-detect envelope's bucket hides.
var pavaParam = map[string]struct {
	unit   string
	timing bool
	get    func(r *measure.Result) float64
	edge   bool
}{
	"PKPK":  {"V", false, func(r *measure.Result) float64 { return r.Vpp }, false},
	"MAX":   {"V", false, func(r *measure.Result) float64 { return r.Vmax }, false},
	"MIN":   {"V", false, func(r *measure.Result) float64 { return r.Vmin }, false},
	"AMPL":  {"V", false, func(r *measure.Result) float64 { return r.Vampl }, false},
	"TOP":   {"V", false, func(r *measure.Result) float64 { return r.Vtop }, false},
	"BASE":  {"V", false, func(r *measure.Result) float64 { return r.Vbase }, false},
	"MEAN":  {"V", false, func(r *measure.Result) float64 { return r.Vmean }, false},
	"RMS":   {"V", false, func(r *measure.Result) float64 { return r.Vrms }, false},
	"OVSP":  {"%", true, func(r *measure.Result) float64 { return r.Overshoot }, true},
	"RPRE":  {"%", true, func(r *measure.Result) float64 { return r.Preshoot }, true},
	"PER":   {"s", true, func(r *measure.Result) float64 { return r.Period }, false},
	"FREQ":  {"Hz", true, func(r *measure.Result) float64 { return r.Freq }, false},
	"PWID":  {"s", true, func(r *measure.Result) float64 { return r.PosWidthS }, false},
	"NWID":  {"s", true, func(r *measure.Result) float64 { return r.NegWidthS }, false},
	"RISE":  {"s", true, func(r *measure.Result) float64 { return r.RiseS }, true},
	"FALL":  {"s", true, func(r *measure.Result) float64 { return r.FallS }, true},
	"DUTY":  {"%", true, func(r *measure.Result) float64 { return r.Duty }, false},
	"NDUTY": {"%", true, func(r *measure.Result) float64 { return 100 - r.Duty }, false},
}

// pava answers Cn:PAVA? <param> from the published frame with the same
// measurement code, scale, offset, probe and coupling as the screen. A value
// the record cannot support reads "****", as the vendor firmware does.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-024
func (h *Handler) pava(ch int, arg string) []byte {
	p, ok := pavaParam[arg]
	if !ok {
		return errTok(errHeader)
	}
	vdiv := 1.0
	if h.fe != nil {
		idx, _ := h.fe.Snapshot()
		vdiv = analog.AnalogVdiv(idx[ch&1]) // codes are on the analog range
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
	probe := h.probe(ch)
	cpl := h.coupling(ch)
	var r *measure.Result
	h.sc.WithFrame(func(f *engine.Frame) {
		if f == nil || f.Valid == 0 {
			return
		}
		sig, q := f.C1, f.Q1
		if ch == 1 {
			sig, q = f.C2, f.Q2
		}
		valid := min(f.Valid, len(sig))
		if len(q) >= valid {
			q = q[:valid]
		} else {
			q = nil
		}
		r = measure.ComputeAcquisition(sig[:valid], q, vdiv/25*probe, offV*probe, f.SampleS, cpl, f.FilterGuard)
		if r != nil && f.PeakDetect {
			r = r.WithEnvelopeTiming(sig[:valid], vdiv/25*probe, offV*probe, f.SampleS)
		}
	})
	value := "****"
	if r != nil && (!p.timing || r.HasTiming) && !(p.edge && r.EnvTiming) {
		if v := p.get(r); !math.IsNaN(v) && !math.IsInf(v, 0) {
			value = fmt.Sprintf("%.6E%s", v, p.unit)
		}
	}
	return h.reply(fmt.Sprintf("C%d:PAVA", ch+1), arg+","+value)
}
