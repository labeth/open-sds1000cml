// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/hal/prototrig"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// trigState is the trigger side of the board: the loaded image's
// capabilities, the decoded-event queue's epoch.
type trigState struct {
	caps       Caps
	eventEpoch uint32
	events     []sramcapture.DecodedEvent
	pending    []sramcapture.DecodedEvent // read, not yet handed out
	haveEvents bool
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

// Program stages trigger s in the capture configuration the next Arm
// commits: an edge, a pulse, slope or video qualifier, or a UART, I2C or
// SPI receiver. Levels are volts at the probe tip, converted to the record's
// codes at the front end's applied V/div and offset. What the fabric cannot
// do is refused with the reason, never replaced.
// TRLC-LINKS: REQ-SDS-011
func (t *triggerFacet) Program(s hal.TriggerSpec) error {
	b := (*Board)(t)
	var c sramcapture.Config
	if err := b.trigConfig(s, &c); err != nil {
		return err
	}
	st := &b.staged
	st.Qualifier, st.Zones, st.Symbol, st.Sequence, st.Packet = c.Qualifier, c.Zones, c.Symbol, c.Sequence, c.Packet
	st.Manchester, st.USBLS, st.MIL1553, st.SENT = c.Manchester, c.USBLS, c.MIL1553, c.SENT
	st.SPI, st.I2C, st.UART = c.SPI, c.I2C, c.UART
	st.Normal, st.Falling, st.TriggerChannel, st.TriggerLevel = c.Normal, c.Falling, c.TriggerChannel, c.TriggerLevel
	st.TriggerHysteresis, st.SecondLevel = c.TriggerHysteresis, c.SecondLevel
	return nil
}

// code is volts v at channel ch's tip as a record code.
// TRLC-LINKS: REQ-SDS-011
func (b *Board) code(ch int, v float64) (uint8, error) {
	if b.frontEnd == nil {
		return 0, hal.Refuse("trigger level", "no front end to convert volts")
	}
	fe := b.frontEnd.Get(ch)
	if !(fe.VoltsDiv > 0) {
		return 0, hal.Refuse("trigger level", "the channel's V/div is not set")
	}
	return uint8(math.Round(math.Max(0, math.Min(255, 128+(v+fe.OffsetV)*CodesPerDiv/fe.VoltsDiv)))), nil
}

// codes is a band of volts dv on channel ch in codes, at least minimum.
// TRLC-LINKS: REQ-SDS-011
func (b *Board) codes(ch int, dv float64, minimum uint8) uint8 {
	if b.frontEnd == nil || dv <= 0 {
		return minimum
	}
	fe := b.frontEnd.Get(ch)
	if !(fe.VoltsDiv > 0) {
		return minimum
	}
	return uint8(math.Min(255, math.Max(float64(minimum), math.Ceil(dv*CodesPerDiv/fe.VoltsDiv))))
}

// trigConfig is s as the fabric's trigger fields of c.
// TRLC-LINKS: REQ-SDS-011
func (b *Board) trigConfig(s hal.TriggerSpec, c *sramcapture.Config) error { //nolint:gocyclo // one case a node
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
	g := s.Stages[0]
	if g.Source.Kind != hal.Analog || g.Source.Index < 0 || g.Source.Index > 1 {
		return hal.Refuse("trigger source", "the fabric triggers on C1 or C2")
	}
	ch := g.Source.Index
	caps := b.trig.caps
	c.TriggerChannel = uint8(ch) //nolint:gosec // G115: 0 or 1 above
	c.Normal = true
	c.Falling = g.Levels.Slope == hal.Falling
	var err error
	if c.TriggerLevel, err = b.code(ch, g.Levels.High); err != nil {
		return err
	}
	c.TriggerHysteresis = b.codes(ch, g.Levels.Hysteresis, minHysteresis)
	switch g.Node {
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
		return b.qualConfig(g, ch, c)
	case hal.NodeProtocol:
		return b.protoConfig(g, ch, c)
	}
	return hal.Refuse(g.Node.String()+" trigger", "not programmable through the HAL on this scope")
}

// qualConfig is a pulse, slope or video stage as the qualifier.
// TRLC-LINKS: REQ-SDS-011
func (b *Board) qualConfig(g hal.TrigStage, ch int, c *sramcapture.Config) error {
	q := sramcapture.QualifierConfig{Enabled: true, Channel: c.TriggerChannel, Negative: c.Falling}
	var minNs, maxNs float64
	if g.Qual != nil {
		switch g.Qual.Op {
		case hal.Shorter:
			q.Cond, maxNs = core.CondLess, g.Qual.MaxS*1e9
		case hal.Longer:
			q.Cond, minNs = core.CondGreater, g.Qual.MinS*1e9
		case hal.Inside:
			q.Cond, minNs, maxNs = core.CondInside, g.Qual.MinS*1e9, g.Qual.MaxS*1e9
		default:
			return hal.Refuse("time qualifier outside", "the qualifier takes shorter, longer or inside")
		}
	}
	switch g.Node {
	case hal.NodePulse:
		q.Kind = sramcapture.QualPulse
	case hal.NodeSlope:
		q.Kind = sramcapture.QualSlope
		lo, err := b.code(ch, g.Levels.Low)
		if err != nil {
			return err
		}
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
		q.Kind, q.Cond = sramcapture.QualVideo, sramcapture.QualAny
		if !g.Video.AnyLine && g.Video.Line > 0 {
			q.Line = uint16(max(1, min(1023, g.Video.Line-first+1))) //nolint:gosec // G115: clamped
		}
		minNs, maxNs = 0, 0
	}
	q.Prescale, q.MinTicks, q.MaxTicks = QualTicks(minNs, maxNs)
	c.Qualifier = q
	return nil
}

// protoConfig is a protocol stage as the image's receiver.
// TRLC-LINKS: REQ-SDS-011
func (b *Board) protoConfig(g hal.TrigStage, ch int, c *sramcapture.Config) error {
	caps := b.trig.caps
	p := core.SerialParams{ChA: ch, ChB: 1 - ch, Threshold: float64(c.TriggerLevel), HaveThr: true, Addr: -1, RW: 2}
	switch pt := g.Proto.(type) {
	case prototrig.UART:
		if !caps.UART && !caps.SampleUART {
			return hal.Refuse("UART trigger", "this FPGA image has no UART receiver")
		}
		p.Proto, p.Baud, p.Bits, p.Inverted = core.SerUART, int(math.Round(pt.Baud)), pt.Bits, !pt.IdleHigh
		switch pt.Parity {
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
		if sda, err := b.code(p.ChB, pt.SDA.Level); err == nil {
			p.ThresholdB, p.HaveThrB = float64(sda), true
		}
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
		if d, err := b.code(p.ChB, pt.Data.Level); err == nil {
			p.ThresholdB, p.HaveThrB = float64(d), true
		}
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
		centre = -b.frontEnd.Get(src.Index).OffsetV
	}
	st := hal.TrigStage{Node: node, Source: src, Levels: hal.Levels{High: centre, Slope: hal.Rising}}
	switch node {
	case hal.NodeEdge:
	case hal.NodePulse:
		st.Qual = &hal.Qualifier{Op: hal.Inside, MinS: 1e-6, MaxS: 5e-6}
	case hal.NodeSlope:
		st.Qual = &hal.Qualifier{Op: hal.Shorter, MaxS: 100e-9}
		if b.frontEnd != nil {
			vd := b.frontEnd.Get(src.Index).VoltsDiv
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
