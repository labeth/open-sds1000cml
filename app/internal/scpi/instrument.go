// ENGMODEL-OWNER-UNIT: FU-APP-SCPI

package scpi

import (
	"errors"
	"math"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	core "github.com/labeth/open-dso-core/scpi"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
)

// instrument is the core Instrument over this scope's engine, front end,
// levels and panel: what the shared commands read and stage. The legacy
// dialect's own setters call the engine directly, in the order and number
// the engine's command queue has always seen.
type instrument struct{ h *Handler }

var _ core.Instrument = instrument{}

// halCoupling maps the front end's coupling modes.
var halCoupling = map[int]hal.Coupling{analog.CplDC: hal.DC, analog.CplAC: hal.AC, analog.CplGND: hal.GND}

// State is the instrument's settings at the probe tip.
// TRLC-LINKS: REQ-SDS-024
func (in instrument) State() core.State {
	h := in.h
	st := h.sc.Snapshot()
	s := core.State{
		TimeDivS: st.TdivS,
		DelayS:   (st.TrigPosFrac - .5) * 10 * st.TdivS,
		Running:  st.Running,
		Mode:     [...]hal.AcqMode{hal.Normal, hal.Average, hal.HighRes, hal.PeakDetect, hal.HighRes}[min(max(st.AcqMode, 0), 4)],
	}
	switch {
	case st.Single:
		s.Sweep = hal.Single
	case st.Norm:
		s.Sweep = hal.NormalSweep
	}
	// The shared commands see this scope's trigger as the edge trigger: its
	// source, slope and level serve every type the engine runs.
	src := h.trigSource()
	edge := hal.TrigStage{Node: hal.NodeEdge, Source: hal.Source{Kind: hal.Analog, Index: src}, Levels: hal.Levels{Slope: hal.Falling}}
	if st.TrigRising {
		edge.Levels.Slope = hal.Rising
	}
	if v, ok := h.lv.TrigLevel(src); ok {
		edge.Levels.High = v
	}
	s.Trigger.Configs[core.TrigEdge] = core.TrigConfig{Stages: []hal.TrigStage{edge}}
	for ch := range 2 {
		p := h.probe(ch)
		c := hal.Channel{
			On: h.tra[ch], Invert: h.inverted(ch), VoltsDiv: p, Probe: p, Impedance: 1e6,
			Coupling: halCoupling[h.coupling(ch)], SkewS: h.skew[ch],
		}
		if d, ok := h.disp.(traceDisplay); ok {
			c.On = d.TraceOn(ch)
		}
		if h.fe != nil {
			c.VoltsDiv = h.fe.VdivV(ch) * p
		}
		c.OffsetV = h.lv.OffsetIn(ch, st) * p
		if bw, ok := h.fe.(interface{ BWL(ch int) bool }); ok && bw.BWL(ch) {
			c.BWLimit = 20e6
		}
		s.Channels = append(s.Channels, c)
	}
	return s
}

// errFixed is a change this adapter cannot stage.
var errFixed = errors.New("scpi: not a setting of this scope")

// Change stages what fn changed, through the same setters the panel uses.
// TRLC-LINKS: REQ-SDS-024
func (in instrument) Change(fn func(*core.State) error) error {
	h := in.h
	old := in.State()
	s := old.Clone()
	if err := fn(&s); err != nil {
		return err
	}
	t, ot := s.Trigger.Active(), old.Trigger.Active()
	if len(s.Channels) != len(old.Channels) || s.Mode != old.Mode || t.Coupling != ot.Coupling ||
		t.NoiseReject != ot.NoiseReject || t.HoldoffS != ot.HoldoffS || s.Depth != old.Depth {
		return errFixed
	}
	if s.TimeDivS != old.TimeDivS {
		b, ok := h.sc.SetTdiv(s.TimeDivS)
		if !ok {
			return errFixed
		}
		h.pendTdiv = b.TdivS
	}
	if s.DelayS != old.DelayS {
		h.sc.SetTrigPosFrac(math.Min(1, math.Max(0, .5+s.DelayS/(10*h.sc.Snapshot().TdivS))))
	}
	if src := s.Trigger.Source(); src != old.Trigger.Source() {
		if src.Kind != hal.Analog || src.Index < 0 || src.Index > 1 {
			return errFixed
		}
		h.sc.SetTrigSource(src.Index)
	}
	if sl := s.Trigger.Slope(); sl != old.Trigger.Slope() {
		h.sc.SetTrigSlope(sl == hal.Rising)
	}
	if lv := s.Trigger.LevelV(); lv != old.Trigger.LevelV() {
		h.trlvV = h.lv.SetTrigLevel(lv, s.Trigger.Source().Index)
	}
	if s.Sweep != old.Sweep || s.Running != old.Running {
		switch {
		case s.Sweep == hal.Single && s.Running:
			h.sc.SetSingle()
		case !s.Running:
			h.sc.SetRunning(false)
		default:
			h.sc.SetNorm(s.Sweep == hal.NormalSweep)
			h.sc.SetRunning(true)
		}
	}
	for ch, c := range s.Channels {
		o := old.Channels[ch]
		if c.Probe != o.Probe && h.fe != nil {
			h.attn[ch] = c.Probe
			h.fe.SetProbe(ch, c.Probe)
		}
		p := h.probe(ch)
		if c.VoltsDiv != o.VoltsDiv && h.fe != nil {
			if err := h.fe.SetVdivV(ch, c.VoltsDiv/p); err != nil {
				return err
			}
		}
		if c.OffsetV != o.OffsetV {
			h.lv.SetOffset(ch, c.OffsetV/p)
		}
		if c.Coupling != o.Coupling && h.fe != nil {
			for m, hc := range halCoupling {
				if hc == c.Coupling {
					_ = h.fe.SetCoupling(ch, m)
				}
			}
		}
		if c.Invert != o.Invert {
			h.SetInverted(ch, c.Invert)
		}
		if c.On != o.On {
			h.tra[ch] = c.On
			if d, ok := h.disp.(traceDisplay); ok {
				d.SetTraceOn(ch, c.On)
			}
		}
		if c.SkewS != o.SkewS {
			h.skew[ch] = c.SkewS
		}
	}
	return nil
}

// WithFrame reads the last published frame.
// TRLC-LINKS: REQ-SDS-024
func (in instrument) WithFrame(fn func(*sample.Frame)) { in.h.sc.WithFrame(fn) }

// Acquisition is Stop, Ready (NORMAL/SINGLE armed, nothing triggered
// lately) or Trig'd: the same WAIT rule the LCD and web status use.
// TRLC-LINKS: REQ-SDS-024
func (in instrument) Acquisition() core.RunState {
	st := in.h.sc.Snapshot()
	if !st.Running {
		return core.Stopped
	}
	if sp, ok := in.h.sc.(interface{ SincePublish() time.Duration }); ok && st.Norm &&
		sp.SincePublish() > time.Duration(math.Max(1, 25*st.TdivS)*float64(time.Second)) {
		return core.Ready
	}
	return core.Triggered
}

// Force captures now, even while NORMAL or SINGLE waits; an engine without
// a force runs.
// TRLC-LINKS: REQ-SDS-024
func (in instrument) Force() error {
	if sc, ok := in.h.sc.(interface{ ForceTrigger() }); ok {
		sc.ForceTrigger()
		return nil
	}
	in.h.sc.SetRunning(true)
	return nil
}
