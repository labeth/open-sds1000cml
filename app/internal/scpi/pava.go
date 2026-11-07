// ENGMODEL-OWNER-UNIT: FU-APP-SCPI

package scpi

import (
	"strings"

	"github.com/labeth/open-dso-core/measure"
	"github.com/labeth/open-dso-core/sample"
	core "github.com/labeth/open-dso-core/scpi"
	"github.com/labeth/open-dso-core/scpi/meas"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
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

// pavaOrder is PAVA? ALL's parameter order: the core's legacy order.
var pavaOrder = func() []string {
	names := make([]string, len(meas.Params))
	for i, p := range meas.Params {
		names[i] = p.Name
	}
	return names
}()

// pava answers Cn:PAVA? <param> from the published frame with the same
// measurement code, scale, offset, probe and coupling as the screen. A value
// the record cannot support reads "****", as the vendor firmware does.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-024
func (h *Handler) pava(c *core.Call, ch int, arg string) error {
	param, known := meas.Lookup(arg)
	if !known && arg != "ALL" {
		return c.Error(core.ErrHeader)
	}
	r := h.measure(ch)
	header := chHeader(ch, "PAVA")
	if arg != "ALL" {
		c.Reply(header, arg+","+param.Legacy(r))
		return nil
	}
	// Every parameter from one measurement, in one buffer.
	var out strings.Builder
	out.Grow(400)
	out.WriteString("ALL")
	for i := range meas.Params {
		p := &meas.Params[i]
		out.WriteString(",")
		out.WriteString(p.Name)
		out.WriteString(",")
		out.WriteString(p.Legacy(r))
	}
	c.Reply(header, out.String())
	return nil
}

// measure measures channel ch on the published frame as the screen does;
// nil when there is nothing to measure.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-024
func (h *Handler) measure(ch int) *measure.Result {
	vdiv, offV := 1.0, 0.0
	if h.fe != nil {
		vdiv = h.fe.RangeV(ch & 1) // codes are on the analog range
		offV = h.lv.OffsetIn(ch, h.sc.Snapshot())
	}
	probe := h.probe(ch)
	cpl := h.coupling(ch)
	var r *measure.Result
	h.sc.WithFrame(func(f *sample.Frame) {
		if f == nil || f.Len() == 0 || ch >= len(f.Traces) {
			return
		}
		// The measurement reads the trace on the 8-bit view at its own codes
		// a division; a wider converter's words are measured as Q8.8 there.
		t := &f.Traces[ch]
		sig, q := t.Codes8, t.Precision
		if t.Format.Storage == sample.U16 {
			sig, q = t.View8(), nil
		}
		vpc := vdiv / t.CodesPerDiv8() * probe
		valid := min(f.Len(), len(sig))
		if len(q) >= valid {
			q = q[:valid]
		} else {
			q = nil
		}
		from := 0
		if a, b := engine.MeasureRange(f); b-a < valid { // a roll view's captured stretch
			from, valid = a, b
			if q != nil {
				q = q[a:b]
			}
		}
		if t.Format.Storage == sample.U16 {
			r = measure.ComputeTrace(t, from, valid, vpc, offV*probe, f.SampleS, cpl, f.Cond.Guard)
		} else {
			r = measure.ComputeAcquisition(sig[from:valid], q, vpc, offV*probe, f.SampleS, cpl, f.Cond.Guard)
		}
		if r != nil && f.Content == sample.PeakPairs {
			r = r.WithEnvelopeTiming(sig[:valid], vpc, offV*probe, f.SampleS).WithEdgeDetail(vpc, offV*probe, engine.EdgeWindows(f, ch)...)
		}
	})
	if r != nil && h.inverted(ch) {
		r = r.Inverted() // measure what the inverted trace shows
	}
	return r
}

// pavaValue formats one parameter of a measurement; "****" when the record
// cannot support it.
// TRLC-LINKS: REQ-SDS-024
func pavaValue(arg string, r *measure.Result) string {
	p, ok := meas.Lookup(arg)
	if !ok {
		return meas.NoReading
	}
	return p.Legacy(r)
}
