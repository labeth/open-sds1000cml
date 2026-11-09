// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/hal/prototrig"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// trigState is the trigger side of the board: the loaded image's
// capabilities, the converters' offsets the re-arm band spans, the
// programmed trigger, the decoded-event queue's epoch.
type trigState struct {
	caps       Caps
	offsets    *[2][5]float64 // the converter cores' offsets in codes; nil: none measured
	parts      [2]trigPart    // what Program staged last (parts[cur]) and the next one's buffer
	cur        int
	free       bool // parts[cur] runs free (TriggerSpec.FreeRun)
	norm       bool // parts[cur]'s sweep is not AUTO
	node       hal.TrigNode
	kept       keptSpec // the spec and inputs parts[cur] was planned from
	eventEpoch uint32
	events     []sramcapture.DecodedEvent
	pending    []sramcapture.DecodedEvent // read, not yet handed out
	haveEvents bool
}

// trigPart is a programmed trigger as the trigger fields of the fabric's
// capture configuration, with what it asks of the record: the zone
// trigger fires after the latest window has ended, so the record holds
// that much more before the trigger.
type trigPart struct {
	cfg        sramcapture.Config // the trigger fields only
	zoneDelayS float64
}

// minHysteresis is the fabric trigger's least re-arm band, in codes.
const minHysteresis = 4

// Video line numbers: the first line sync after a field's vertical sync and
// its post-equalizing pulses.
const (
	videoFirstLineNTSC = 10
	videoFirstLinePAL  = 6
)

type triggerFacet Board

// Program stages trigger s for the next Arm: an edge, a pulse, slope or
// video qualifier, the zones that qualify either, a UART, I2C or SPI
// receiver, or any bus the core decodes (prototrig.Bus) on the receiver
// the image has for it. Levels are volts at the probe tip, converted to
// the record's codes at the front end's V/div and offset (levelScale). What
// the fabric cannot do is refused with the reason, never replaced; a
// free-running trigger (FreeRun) stages what the fabric takes of it and is
// never refused for the rest. COMMIT.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-013, REQ-SDS-014
func (t *triggerFacet) Program(s hal.TriggerSpec) error {
	b := (*Board)(t)
	b.stageLevelDAC()
	in := b.trigInputs()
	if b.trig.kept.matches(&s, &in) {
		return nil // staged from the same spec and inputs: the same trigger
	}
	next := 1 - b.trig.cur
	if err := b.trigConfig(&s, &in, &b.trig.parts[next]); err != nil {
		return err
	}
	b.trig.cur, b.trig.free, b.trig.norm = next, s.FreeRun, s.Sweep != hal.Auto
	b.trig.node = s.Stages[0].Node
	b.trig.kept.keep(&s, &in)
	b.restaged()
	return nil
}

// stagedPart is the programmed trigger.
// TRLC-LINKS: REQ-SDS-011
func (ts *trigState) stagedPart() *trigPart { return &ts.parts[ts.cur] }

// levelScale is channel ch's record codes in volts at the probe tip: the
// front end's V/div and offset as the next Arm leaves them, or without a front end (no SPI to it, the
// register path, the engine's tests) a nominal 1 V/div without offset, so
// levels the owner converted the same way come back as the codes they were.
// TRLC-LINKS: REQ-SDS-011
func (b *Board) levelScale(ch int) sample.Scale {
	if b.frontEnd != nil {
		if fe := b.frontEnd.pending(ch & 1); fe.VoltsDiv > 0 {
			if s, ok := b.acScale(ch, fe); ok {
				return s // an AC-coupled channel's codes from its mean (trigger_ac.go)
			}
			return sample.Scale{Zero: sample.Mid8, PerCode: fe.VoltsDiv / CodesPerDiv, OffsetV: fe.OffsetV}
		}
	}
	return sample.Scale{Zero: sample.Mid8, PerCode: 1.0 / CodesPerDiv}
}

// CodeVolts is record code (fractional for a learned level) on channel ch
// in volts at the probe tip, the inverse of Program's conversion: a level
// the owner holds in codes reaches Program as these volts and comes back
// as the same code, half codes too (sample.Scale.Word).
// TRLC-LINKS: REQ-SDS-011
func (b *Board) CodeVolts(ch int, code float64) float64 { return b.levelScale(ch).Volts(code) }

// trigInputs is what the board plans a trigger spec at besides the spec:
// both channels' scales (the front end's V/div and offset), the loaded
// image's capabilities and the converters' offsets the re-arm band spans.
// It is comparable: a spec programmed again at the same inputs plans the
// same trigger.
type trigInputs struct {
	scale       [2]sample.Scale
	caps        Caps
	offsets     [2][5]float64
	haveOffsets bool
}

// trigInputs is the board's inputs now.
// TRLC-LINKS: REQ-SDS-011
func (b *Board) trigInputs() trigInputs {
	in := trigInputs{scale: [2]sample.Scale{b.levelScale(0), b.levelScale(1)}, caps: b.trig.caps}
	if b.trig.offsets != nil {
		in.offsets, in.haveOffsets = *b.trig.offsets, true
	}
	return in
}

// code is volts v at channel ch's tip as a record code.
// TRLC-LINKS: REQ-SDS-011
func (in *trigInputs) code(ch int, v float64) uint8 {
	return uint8(math.Round(math.Max(0, math.Min(255, in.scale[ch&1].Word(v)))))
}

// codes is a band of volts dv on channel ch in codes, at least minimum.
// TRLC-LINKS: REQ-SDS-011
func (in *trigInputs) codes(ch int, dv float64, minimum uint8) uint8 {
	if dv <= 0 {
		return minimum
	}
	return uint8(math.Min(255, math.Max(float64(minimum), math.Ceil(dv/in.scale[ch&1].PerCode))))
}

// nanos is a time in seconds as the nanoseconds the fabric's counters are
// planned in. A time that was a nanosecond count with up to three decimals
// comes back as exactly that count: the owner's limits are set in
// nanoseconds, and a count one float step off would round to the other
// tick at a tick's half.
// TRLC-LINKS: REQ-SDS-011
func nanos(s float64) float64 {
	ns := s * 1e9
	if q := math.Round(ns*1e3) / 1e3; math.Abs(ns-q) <= 1e-12*math.Abs(ns) {
		return q
	}
	return ns
}

// trigConfig plans s into tp as the fabric's trigger fields, at the
// board's inputs in (trigInputs). tp is left in an unspecified state when
// s is refused.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-014
func (b *Board) trigConfig(s *hal.TriggerSpec, in *trigInputs, tp *trigPart) error {
	switch {
	case len(s.Stages) != 1:
		return hal.Refuse("trigger sequence", "the fabric's trigger is one stage")
	case s.Reset != nil:
		return hal.Refuse("trigger reset", "the fabric's trigger is one stage")
	case s.HoldoffEv != 0:
		return hal.Refuse("holdoff by events", "the holdoff is a time the owner paces")
	case s.Coupling != hal.TrigDC:
		return hal.Refuse("trigger coupling", "the fabric compares the record's DC codes")
	case s.NoiseReject:
		return hal.Refuse("noise reject", "the fabric's band is the converters' offsets")
	}
	g := &s.Stages[0]
	if g.Source.Kind != hal.Analog || g.Source.Index < 0 || g.Source.Index > 1 {
		return hal.Refuse("trigger source", "the fabric triggers on C1 or C2")
	}
	ch := g.Source.Index
	*tp = trigPart{}
	c := &tp.cfg
	c.TriggerChannel = uint8(ch)
	c.Normal = true
	c.Falling = g.Levels.Slope == hal.Falling
	c.TriggerLevel = in.code(ch, g.Levels.High)
	if err := b.nodeConfig(g, in, ch, c, s.FreeRun); err != nil {
		return err
	}
	if len(g.Zones) != 0 {
		z, delayS, why := b.planZones(g.Zones, in)
		if why != "" && !s.FreeRun {
			return hal.Refuse("zone trigger", why)
		}
		if why == "" {
			c.Zones, tp.zoneDelayS = z, delayS
		}
	}
	if s.FreeRun {
		c.Normal = false
	}
	b.planHysteresis(c, in.codes(ch, g.Levels.Hysteresis, minHysteresis))
	return nil
}

// nodeConfig is stage g's node as the fabric's trigger fields of c; free
// keeps what the fabric takes of a receiver it cannot plan whole.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-013
func (b *Board) nodeConfig(g *hal.TrigStage, in *trigInputs, ch int, c *sramcapture.Config, free bool) error {
	caps := b.trig.caps
	switch g.Node { //nolint:exhaustive // the nodes the fabric programs; every other node is refused below
	case hal.NodeEdge:
		if g.Levels.Slope == hal.Either {
			return hal.Refuse("edge on either slope", "the fabric's edge trigger takes one slope")
		}
		if g.Gate != nil || g.Qual != nil || g.Count > 1 {
			return hal.Refuse("qualified edge", "the fabric's edge trigger has no gate, interval or count")
		}
		return nil
	case hal.NodePulse, hal.NodeSlope, hal.NodeVideo:
		if !caps.Qualifier {
			return hal.Refuse(g.Node.String()+" trigger", "this FPGA image has no pulse, slope or video trigger")
		}
		return b.qualConfig(g, in, ch, c)
	case hal.NodeProtocol, hal.NodeSymbol, hal.NodeSequence, hal.NodeFrameStart:
		if bus, ok := busOf(g.Proto); ok {
			return b.busConfig(bus, in, c, free)
		}
		if g.Node != hal.NodeProtocol {
			return hal.Refuse(g.Node.String()+" trigger", "the fabric's generic receivers take a bus request")
		}
		return b.protoConfig(g, in, ch, c)
	}
	return hal.Refuse(g.Node.String()+" trigger", "not programmable through the HAL on this scope")
}

// busOf is p as a bus request, by value or by pointer.
// TRLC-LINKS: REQ-SDS-013
func busOf(p hal.ProtoTrigger) (*prototrig.Bus, bool) {
	switch b := p.(type) {
	case prototrig.Bus:
		return &b, true
	case *prototrig.Bus:
		return b, b != nil
	}
	return nil, false
}

// busConfig is bus request bus on the image's receiver for it: its levels
// as record codes on its lines, then the serial planner. A request no
// receiver takes is refused, or with free kept as far as the planner went.
// TRLC-LINKS: REQ-SDS-013
func (b *Board) busConfig(bus *prototrig.Bus, in *trigInputs, c *sramcapture.Config, free bool) error {
	if !b.trig.caps.Serial() {
		return hal.Refuse("serial trigger", "this FPGA image has no matcher for any bus")
	}
	// the levels are the request's thresholds whatever it named: a
	// threshold left to the decoder is the one the owner learned
	p := bus.Request
	p.Threshold, p.HaveThr = in.scale[p.ChA&1].Word(bus.Levels[0]), true
	p.ThresholdB, p.HaveThrB = in.scale[p.ChB&1].Word(bus.Levels[1]), true
	var arinc uint32
	if len(bus.ARINCSlicer) == 4 {
		for _, v := range bus.ARINCSlicer {
			arinc = arinc<<8 | uint32(in.code(p.ChA, v))
		}
	}
	c.Normal = false
	if arinc != 0 {
		b.arincAux.Store(arinc)
	}
	b.PlanSerial(c, p, false, arinc)
	if !c.Normal && !free {
		return hal.Refuse("serial trigger", "no receiver of this FPGA image takes the request")
	}
	return nil
}

// qualConfig is a pulse, slope or video stage as the qualifier. The
// counter is planned from both limits whatever the condition: the one it
// compares is the fabric's minimum for Shorter (under the minimum), its
// maximum for Longer (over the maximum).
// TRLC-LINKS: REQ-SDS-011
func (b *Board) qualConfig(g *hal.TrigStage, in *trigInputs, ch int, c *sramcapture.Config) error {
	q := sramcapture.QualifierConfig{Enabled: true, Channel: c.TriggerChannel, Negative: c.Falling}
	var minNs, maxNs float64
	if g.Qual != nil {
		lo, hi := nanos(g.Qual.MinS), nanos(g.Qual.MaxS)
		switch g.Qual.Op {
		case 0:
			q.Cond, minNs, maxNs = sramcapture.QualAny, lo, hi
		case hal.Shorter:
			q.Cond, minNs, maxNs = sramcapture.QualUnder, hi, lo
		case hal.Longer:
			q.Cond, minNs, maxNs = sramcapture.QualOver, hi, lo
		case hal.Inside:
			q.Cond, minNs, maxNs = sramcapture.QualBetween, lo, hi
		case hal.Outside:
			return hal.Refuse("time qualifier outside", "the qualifier takes shorter, longer or inside")
		default:
			return hal.Refuse("time qualifier", "not a time condition")
		}
	}
	switch g.Node { //nolint:exhaustive // trigConfig sends only pulse, slope and video stages here
	case hal.NodePulse:
		q.Kind = sramcapture.QualPulse
	case hal.NodeSlope:
		q.Kind = sramcapture.QualSlope
		lo := in.code(ch, g.Levels.Low)
		q.LevelA, q.LevelB = lo, c.TriggerLevel
		c.TriggerLevel = lo
	case hal.NodeVideo:
		if g.Video == nil {
			return hal.Refuse("video trigger", "no video standard")
		}
		first := 0
		switch g.Video.Standard {
		case "pal":
			first = videoFirstLinePAL
		case "ntsc":
			first = videoFirstLineNTSC
		default:
			return hal.Refuse("video "+g.Video.Standard, "the qualifier counts PAL and NTSC lines")
		}
		q.Kind, q.Cond, q.Negative = sramcapture.QualVideo, sramcapture.QualAny, !g.Video.Positive
		if !g.Video.AnyLine && g.Video.Line > 0 {
			q.Line = uint16(max(1, min(1023, g.Video.Line-first+1)))
		}
		minNs, maxNs = 0, 0
	}
	q.Prescale, q.MinTicks, q.MaxTicks = QualTicks(minNs, maxNs)
	c.Qualifier = q
	return nil
}

// protoConfig is a protocol stage as the image's receiver.
// TRLC-LINKS: REQ-SDS-011
func (b *Board) protoConfig(g *hal.TrigStage, in *trigInputs, ch int, c *sramcapture.Config) error {
	caps := b.trig.caps
	p := core.SerialParams{ChA: ch, ChB: 1 - ch, Threshold: float64(c.TriggerLevel), HaveThr: true, Addr: -1, RW: 2}
	switch pt := g.Proto.(type) {
	case prototrig.UART:
		if !caps.UART && !caps.SampleUART {
			return hal.Refuse("UART trigger", "this FPGA image has no UART receiver")
		}
		p.Proto, p.Baud, p.Bits, p.Inverted = core.SerUART, int(math.Round(pt.Baud)), pt.Bits, !pt.IdleHigh
		switch pt.Parity { //nolint:exhaustive // default: the receiver has no mark or space parity
		case prototrig.NoParity:
			p.Parity = core.ParityNone
		case prototrig.Even:
			p.Parity = "even"
		case prototrig.Odd:
			p.Parity = "odd"
		default:
			return hal.Refuse("UART mark or space parity", "the receiver checks even or odd parity")
		}
		if pt.Condition != prototrig.UARTData || pt.Compare != prototrig.Equal || pt.MSBFirst || pt.StopBits > 1 {
			return hal.Refuse("UART trigger", "the receiver matches equal data bytes of an LSB-first frame with one stop bit")
		}
		if pt.Value >= 0 {
			p.Bytes = []int{pt.Value}
		}
		c.UART = PlanHardwareUART(&p, caps.SampleUART)
		if !c.UART.Enabled {
			return hal.Refuse("UART trigger", "the receiver does not take this rate and frame")
		}
		c.TriggerChannel = c.UART.Channel
		return nil
	case prototrig.I2C:
		if !caps.I2C && !caps.SampleI2C {
			return hal.Refuse("I2C trigger", "this FPGA image has no I2C receiver")
		}
		if pt.Condition != prototrig.I2CAddr7Data || pt.SCL.Source.Index != ch {
			return hal.Refuse("I2C trigger", "the receiver matches a 7-bit address and data, clocked on the trigger source")
		}
		p.Proto, p.ChB, p.Addr = core.SerI2C, pt.SDA.Source.Index, pt.Address
		p.RW = map[prototrig.RW]int{prototrig.Write: 0, prototrig.Read: 1, prototrig.AnyRW: 2}[pt.RW]
		for _, d := range pt.Data {
			p.Bytes = append(p.Bytes, int(d))
		}
		p.ThresholdB, p.HaveThrB = float64(in.code(p.ChB, pt.SDA.Level)), true
		c.I2C = PlanHardwareI2C(&p, caps.SampleI2C)
		if !c.I2C.Enabled {
			return hal.Refuse("I2C trigger", "the receiver does not take this match")
		}
		c.TriggerChannel, c.SecondLevel = c.I2C.ClockChannel, SecondLevel(p)
		return nil
	case prototrig.SPI:
		if !caps.SPI && !caps.SampleSPI {
			return hal.Refuse("SPI trigger", "this FPGA image has no SPI receiver")
		}
		if pt.SelectMode != prototrig.SelectTimeout || pt.Clock.Source.Index != ch || len(pt.Bits)%8 != 0 {
			return hal.Refuse("SPI trigger", "the receiver frames on clock idle and matches whole bytes, clocked on the trigger source")
		}
		p.Proto, p.ChB, p.MSB, p.CPOL = core.SerSPI, pt.Data.Source.Index, pt.MSBFirst, pt.LatchFalling
		for i := 0; i < len(pt.Bits); i += 8 {
			v := 0
			for k := range 8 {
				bit := pt.Bits[i+k]
				if bit == hal.DontCare {
					return hal.Refuse("SPI don't-care bits", "the receiver matches whole bytes")
				}
				v = v<<1 | map[bool]int{true: 1}[bit == hal.High]
			}
			p.Bytes = append(p.Bytes, v)
		}
		p.ThresholdB, p.HaveThrB = float64(in.code(p.ChB, pt.Data.Level)), true
		c.SPI = PlanHardwareSPI(&p, caps.SampleSPI)
		if !c.SPI.Enabled {
			return hal.Refuse("SPI trigger", "the receiver does not take this match")
		}
		c.TriggerChannel, c.SecondLevel = c.SPI.ClockChannel, SecondLevel(p)
		return nil
	case nil:
		return hal.Refuse("protocol trigger", "no protocol configuration")
	}
	return hal.Refuse(g.Proto.Protocol()+" trigger", "not programmable through the HAL on this scope")
}

// Caps is the loaded image's trigger nodes.
// TRLC-LINKS: REQ-SDS-011
func (t *triggerFacet) Caps() hal.TrigCaps { return (*Board)(t).trigCaps() }

// LevelGrid is one step of the trigger comparator DAC at the source's
// V/div, as the composition's levels compute it.
// TRLC-LINKS: REQ-SDS-011
func (t *triggerFacet) LevelGrid(src hal.Source) hal.Grid {
	b := (*Board)(t)
	if b.levelGrid == nil || src.Kind != hal.Analog {
		return hal.Grid{}
	}
	return b.levelGrid(src.Index)
}

// Default is a node's starting stage on src: the level at the screen's
// centre, rising; a pulse inside 1-5 us; a slope from 20 to 80 % in under
// 100 ns; PAL video on any line.
// TRLC-LINKS: REQ-SDS-011
func (t *triggerFacet) Default(node hal.TrigNode, src hal.Source) (hal.TrigStage, error) {
	b := (*Board)(t)
	if src.Kind != hal.Analog || src.Index < 0 || src.Index > 1 {
		return hal.TrigStage{}, hal.Refuse("trigger source", "the fabric triggers on C1 or C2")
	}
	centre := 0.0
	if b.frontEnd != nil {
		centre = -b.frontEnd.pending(src.Index).OffsetV
	}
	st := hal.TrigStage{Node: node, Source: src, Levels: hal.Levels{High: centre, Slope: hal.Rising}}
	switch node { //nolint:exhaustive // default: the other nodes have no single-source default here
	case hal.NodeEdge:
	case hal.NodePulse:
		st.Qual = &hal.Qualifier{Op: hal.Inside, MinS: 1e-6, MaxS: 5e-6}
	case hal.NodeSlope:
		st.Qual = &hal.Qualifier{Op: hal.Shorter, MaxS: 100e-9}
		if b.frontEnd != nil {
			vd := b.frontEnd.pending(src.Index).VoltsDiv
			st.Levels.Low, st.Levels.High = centre-1.5*vd, centre+1.5*vd
		}
	case hal.NodeVideo:
		st.Video = &hal.Video{Standard: "pal", AnyLine: true}
		st.Levels.Slope = hal.Falling
	default:
		return hal.TrigStage{}, hal.Refuse(node.String()+" trigger", "no single-source default")
	}
	return st, nil
}

// SetEventEpoch names the decoded-event queue's epoch for Events (the
// running decoded capture's); 0 ends it. OWNER.
// TRLC-LINKS: REQ-SDS-011
func (b *Board) SetEventEpoch(epoch uint32) {
	b.trig.eventEpoch, b.trig.haveEvents, b.trig.pending = epoch, epoch != 0, nil
}

// Events reads hardware-decoded events of the running decoded capture.
// OWNER.
// TRLC-LINKS: REQ-SDS-011
func (t *triggerFacet) Events(dst []hal.Event) (int, error) {
	b := (*Board)(t)
	if !b.trig.haveEvents {
		return 0, hal.Refuse("decoded events", "no decoded capture is running")
	}
	var err error
	if len(b.trig.pending) == 0 {
		b.trig.events, err = b.mem.ReadDecodedEventsInto(b.trig.eventEpoch, b.trig.events[:0])
		b.trig.pending = b.trig.events
	}
	n := 0
	for n < len(dst) && n < len(b.trig.pending) {
		ev := b.trig.pending[n]
		dst[n] = hal.Event{At: ev.Sample, Kind: ev.Kind, Value: ev.Value}
		n++
	}
	b.trig.pending = b.trig.pending[n:]
	return n, err
}

var _ hal.MatchEndTrigger = (*triggerFacet)(nil)

// FiresAtMatchEnd is true: the fabric's receivers fire once the match's
// last unit is in, and the owner places the trigger at its start.
// TRLC-LINKS: REQ-SDS-013
func (t *triggerFacet) FiresAtMatchEnd() bool { return true }

// Matches is the symbol trigger's match counter.
// TRLC-LINKS: REQ-SDS-011
func (t *triggerFacet) Matches() (hal.MatchStats, bool) {
	b := (*Board)(t)
	if !b.trig.caps.Symbol {
		return hal.MatchStats{}, false
	}
	n, err := b.mem.SymbolMatches()
	if err != nil {
		return hal.MatchStats{}, false
	}
	return hal.MatchStats{Matches: uint64(n)}, true
}
