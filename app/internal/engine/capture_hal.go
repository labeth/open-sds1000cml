// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"fmt"
	"math"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/hal/prototrig"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The capture in HAL terms: the engine's settings (trigger levels in record
// codes, learned levels and extents, the serial request, the zones) become
// one hal.TriggerSpec in volts and one hal.Plan, which the board programs
// (Trigger.Program) and configures (Acquirer.Configure) into its fabric
// configuration. What the settings ask that the board cannot serve yet runs
// free (TriggerSpec.FreeRun): nothing triggers, nothing is substituted.

// captureTarget takes a capture's trigger and plan: the board's facets for
// the capture the owner arms, a board draft for a look at the settings.
type captureTarget interface {
	Program(s hal.TriggerSpec) error
	Configure(p hal.Plan) error
	// Trigger is the programmed trigger's fields of the configuration.
	Trigger() *sramcapture.Config
}

// boardTarget stages on the board's facets.
type boardTarget struct{ e *Engine }

// TRLC-LINKS: REQ-SDS-011
func (t boardTarget) Program(s hal.TriggerSpec) error { return t.e.trigger().Program(s) }

// TRLC-LINKS: REQ-SDS-008
func (t boardTarget) Configure(p hal.Plan) error { return t.e.acquirer().Configure(p) }

// TRLC-LINKS: REQ-SDS-011
func (t boardTarget) Trigger() *sramcapture.Config { return t.e.hw().StagedTrigger() }

// halTrigger is the engine's scratch for one trigger in HAL terms, reused
// at every look at the settings (owner only).
type halTrigger struct {
	stages [1]hal.TrigStage
	qual   hal.Qualifier
	video  hal.Video
	bus    prototrig.Bus
	slicer [4]float64
	zones  []hal.Zone
	zm     []Zone
}

// bothChannels is the channels every SRAM record holds.
var bothChannels = []int{0, 1}

// planCapture plans the capture the settings ask for on t: the trigger
// first (whether a serial receiver takes it decides picking), then the
// plan.
// TRLC-LINKS: REQ-SDS-009, REQ-SDS-010, REQ-SDS-011, REQ-SDS-013, REQ-SDS-014
func (e *Engine) planCapture(t captureTarget) (SRAMPlan, float64, bool, core.TrigParams) {
	p, tdiv, norm, tp, block := e.capturePlan()
	perWord := 2 - acq.BoolInt(p.Log != 0)
	words := uint32(p.Samples / perWord) //nolint:gosec // G115: the plan's samples fit the SRAM
	frac := math.Float64frombits(e.trigPosFrac.Load())
	room := 0
	if e.serialMode.Load() == SerialTrigger && p.SampleS > 0 {
		room = int(e.serialLeadWords(p, perWord, words, uint32(float64(words)*frac))) * perWord
	}
	e.programTrigger(t, tp, norm)
	// BUS's sampled records pick samples where the image can, while a serial
	// trigger reads the raw samples (as the edge and the qualifier do; the
	// zones judge the filter's words): the decode reads edges, which the
	// precision filter's band smooths away
	pick := e.caps().PickAlt && block == blockBus && p.Log != 0 && acq.HardwareSerialName(t.Trigger()) != ""
	_ = t.Configure(hal.Plan{
		Request: hal.Request{Channels: bothChannels, TimePerDivS: tdiv, PretrigFrac: frac, PretrigRoom: room, Mode: e.halMode()},
		Mode:    e.halMode(), SampleS: p.SampleS, Depth: p.Samples, AcqDecim: 1 << p.Log, Pick: pick,
		ETSFactor: 1, Segments: 1, WinSamples: p.Screen,
	})
	return p, tdiv, norm, tp
}

// halMode is the acquisition mode in HAL terms.
// TRLC-LINKS: REQ-SDS-012
func (e *Engine) halMode() hal.AcqMode {
	switch e.acqMode.Load() {
	case AcqAverage:
		return hal.Average
	case AcqEres, AcqPrecision:
		return hal.HighRes
	case AcqPeak:
		return hal.PeakDetect
	}
	return hal.Normal
}

// programTrigger programs the trigger the settings ask for on t: the
// serial trigger on the image's receiver, else a pulse, slope or video
// qualifier or the edge, qualified by the zones. A trigger the hardware
// cannot serve yet (its levels still being learned, no image for it) runs
// free: captures complete untriggered and software never decides a trigger.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-013, REQ-SDS-014
func (e *Engine) programTrigger(t captureTarget, tp core.TrigParams, norm bool) {
	ch, rising := int(e.trigChannel()), e.trigRising.Load()
	base := e.edgeStage(ch, rising)
	if e.serialMode.Load() == SerialTrigger {
		e.programSerial(t, base, norm)
		return
	}
	st, armed := base, tp.Typ == TrigEdge
	if !armed {
		// The FPGA's qualifier decides a pulse, slope or video trigger; until
		// it can (no image, the levels still unknown) captures run free
		// and nothing triggers.
		if q, why := e.qualStage(tp, base); why == "" {
			st, armed = q, true
		}
	}
	if e.zonesOn() {
		// The FPGA's zones qualify the trigger; until they can, nothing
		// triggers.
		if armed {
			st.Zones = e.halZones()
			if e.program(t, st, norm, false) == nil {
				return
			}
			st.Zones = nil
		}
		armed = false
	}
	if e.program(t, st, norm, !armed) != nil {
		_ = e.program(t, base, norm, true)
	}
}

// programSerial programs the serial trigger on the image's receiver for
// it, as far as the receivers take it while none takes it whole.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) programSerial(t captureTarget, base hal.TrigStage, norm bool) {
	if !e.hasSerialHardware() {
		_ = e.program(t, base, norm, true)
		return
	}
	e.ser.Mu.Lock()
	serial := e.ser.Params
	e.ser.Mu.Unlock()
	e.spiQuietCheck(serial)
	// The matcher slices at the learned level when the request names none.
	serial, why := e.serialPlanParams(serial)
	if why == core.ArincOneWord {
		_ = e.program(t, base, norm, true)
		return
	}
	st := base
	st.Node, st.Proto = hal.NodeProtocol, e.busTrigger(serial)
	if e.program(t, st, norm, false) != nil {
		_ = e.program(t, st, norm, true)
	}
}

// program programs stage st alone on t.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) program(t captureTarget, st hal.TrigStage, norm, free bool) error {
	sweep := hal.Auto
	if norm {
		sweep = hal.NormalSweep
	}
	e.halTrig.stages[0] = st
	return t.Program(hal.TriggerSpec{Stages: e.halTrig.stages[:], Sweep: sweep, FreeRun: free})
}

// levelVolts is record code on channel ch as volts at the probe tip, as the
// board converts them back.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) levelVolts(ch int, code float64) float64 { return e.hw().CodeVolts(ch, code) }

// edgeStage is the edge trigger on channel ch at the trigger level.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) edgeStage(ch int, rising bool) hal.TrigStage {
	st := sourceStage(ch, rising)
	st.Levels.High = e.levelVolts(ch, float64(e.trigLevelCode()))
	return st
}

// sourceStage is an edge on channel ch of the trigger slope, at no level
// yet.
// TRLC-LINKS: REQ-SDS-011
func sourceStage(ch int, rising bool) hal.TrigStage {
	slope := hal.Rising
	if !rising {
		slope = hal.Falling
	}
	return hal.TrigStage{Node: hal.NodeEdge, Source: hal.Source{Kind: hal.Analog, Index: ch}, Levels: hal.Levels{Slope: slope}}
}

// busTrigger is serial request serial as the board's bus trigger: its
// thresholds and ARINC 429's learned slicer in volts.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) busTrigger(serial SerialParams) *prototrig.Bus {
	b := &e.halTrig.bus
	*b = prototrig.Bus{Request: serial}
	b.Levels[0] = e.levelVolts(serial.ChA, serial.Threshold)
	b.Levels[1] = e.levelVolts(serial.ChB, serial.ThresholdB)
	if aux := e.arincAux.Load(); aux != 0 {
		for i := range e.halTrig.slicer {
			e.halTrig.slicer[i] = e.levelVolts(serial.ChA, float64(uint8(aux>>(24-8*i)))) //nolint:gosec // G115: one byte of the packed levels
		}
		b.ARINCSlicer = e.halTrig.slicer[:]
	}
	return b
}

// qualStage is the trigger type's hardware qualifier on base's channel at
// the current V/div and offset: its levels placed on the channel's extent
// learned from the captures, its time limits. why says why there is none
// ("" when there is one).
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) qualStage(tp core.TrigParams, base hal.TrigStage) (hal.TrigStage, string) {
	ch := base.Source.Index & 1
	if !e.caps().Qualifier {
		if e.images != nil && e.images.HasQualify() && e.protocolImage != acq.ImageQualify {
			return base, "loading the qualifier FPGA image"
		}
		return base, "this FPGA image has no pulse, slope or video trigger"
	}
	if !e.qualExt.Have[ch].Load() {
		return base, fmt.Sprintf("learning the signal levels on C%d", ch+1)
	}
	off := math.Float64frombits(e.trigOffV[ch].Load())
	vdiv := e.chVdivV(ch)
	code := func(v float64) float64 { return 128 + (v+off)*25/vdiv }
	lo := code(math.Float64frombits(e.qualExt.Lo[ch].Load()))
	hi := code(math.Float64frombits(e.qualExt.Hi[ch].Load()))
	span := hi - lo
	if span < core.FlatRejectSpan {
		return base, fmt.Sprintf("no signal swing on C%d", ch+1)
	}
	clamp := func(v float64) float64 { return math.Round(math.Max(1, math.Min(254, v))) }
	st := base
	level := clamp(lo + tp.PulseLvlFrac*span)
	minNs, maxNs, cond := tp.PulseWMinNs, tp.PulseWMaxNs, tp.PulseCond
	switch tp.Typ {
	case TrigPulse:
		st.Node = hal.NodePulse
	case TrigSlope:
		st.Node = hal.NodeSlope
		level = clamp(lo + tp.SlopeLoFrac*span)
		st.Levels.Low, st.Levels.High = e.levelVolts(ch, level), e.levelVolts(ch, clamp(lo+tp.SlopeHiFrac*span))
		minNs, maxNs, cond = tp.SlopeTMinNs, tp.SlopeTMaxNs, tp.SlopeCond
	case TrigVideo:
		st.Node = hal.NodeVideo
		level = clamp(lo + 0.3*span) // the sync slice, 30 % from the sync tip
		if !tp.VideoNeg {
			level = clamp(hi - 0.3*span)
		}
		e.halTrig.video = hal.Video{Standard: "pal", Line: tp.VideoLine, AnyLine: tp.VideoLine == 0, Positive: !tp.VideoNeg}
		if tp.VideoStd == 1 {
			e.halTrig.video.Standard = "ntsc"
		}
		st.Video = &e.halTrig.video
	case TrigEdge:
		return base, "an edge trigger has no qualifier"
	default:
		return base, "the trigger type has no qualifier"
	}
	if tp.Typ != TrigSlope {
		st.Levels.High = e.levelVolts(ch, level)
	}
	if tp.Typ != TrigVideo {
		e.halTrig.qual = qualifierOf(cond, minNs, maxNs)
		st.Qual = &e.halTrig.qual
	}
	return st, ""
}

// qualifierOf is a qualifier condition (Cond*) on limits minNs and maxNs
// in HAL terms: less than the minimum is Shorter than it, more than the
// maximum Longer than it, the other limit kept for the counter's range.
// TRLC-LINKS: REQ-SDS-011
func qualifierOf(cond int, minNs, maxNs float64) hal.Qualifier {
	lo, hi := minNs/1e9, maxNs/1e9
	switch cond {
	case CondLess:
		return hal.Qualifier{Op: hal.Shorter, MinS: hi, MaxS: lo}
	case CondGreater:
		return hal.Qualifier{Op: hal.Longer, MinS: hi, MaxS: lo}
	case CondInside:
		return hal.Qualifier{Op: hal.Inside, MinS: lo, MaxS: hi}
	}
	return hal.Qualifier{MinS: lo, MaxS: hi}
}

// halZones is the zone trigger's zones in HAL terms: their windows around
// the edge, their codes in volts on their channels.
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) halZones() []hal.Zone {
	e.zm.Mu.Lock()
	e.halTrig.zm = append(e.halTrig.zm[:0], e.zm.Zones...)
	e.zm.Mu.Unlock()
	zs := e.halTrig.zones[:0]
	for _, z := range e.halTrig.zm {
		ch := z.Ch & 1
		zs = append(zs, hal.Zone{
			Source: hal.Source{Kind: hal.Analog, Index: ch}, FromS: z.DtLoS, ToS: z.DtHiS,
			Low: e.levelVolts(ch, float64(z.CodeLo)), High: e.levelVolts(ch, float64(z.CodeHi)), Avoid: z.Avoid,
		})
	}
	e.halTrig.zones = zs
	return zs
}

// planQualifier is the trigger type's hardware qualifier on channel ch as
// the board plans it, the trigger level its edges fire at, and why it has
// none ("" when it does). rising is the trigger slope: a high pulse, a
// rising slope. It may run under e.mu.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) planQualifier(tp core.TrigParams, ch int, rising bool) (sramcapture.QualifierConfig, uint8, string) {
	st, why := e.qualStage(tp, sourceStage(ch&1, rising))
	if why != "" {
		return sramcapture.QualifierConfig{}, 0, why
	}
	d := e.qualDraft()
	if err := e.program(d, st, true, false); err != nil {
		return sramcapture.QualifierConfig{}, 0, err.Error()
	}
	c := d.Trigger()
	return c.Qualifier, c.TriggerLevel, ""
}

// planZones is the zone trigger as the board plans it (PlanZones). delayS
// is how long after an edge trigger's the FPGA fires; why says why there
// is no plan ("" when there is one).
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) planZones() (z sramcapture.ZoneConfig, delayS float64, why string) {
	if !e.caps().Qualifier {
		if e.images != nil && e.images.HasQualify() && e.protocolImage != acq.ImageQualify {
			return z, 0, "loading the qualifier FPGA image"
		}
		return z, 0, "this FPGA image has no zone trigger"
	}
	return e.hw().PlanZones(e.halZones())
}
