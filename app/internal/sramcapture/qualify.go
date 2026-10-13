// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "fmt"

// Qualifier kinds (ADR-HARDWARE-QUALIFIERS).
const (
	QualPulse = 0 // the time between a pulse's edges at the trigger level
	QualSlope = 1 // the time from the first slope level to the second
	QualVideo = 2 // line N after the vertical sync, or every line
)

// Qualifier conditions on the measured time.
const (
	QualAny     = 0
	QualUnder   = 1 // under the minimum
	QualOver    = 2 // over the maximum
	QualBetween = 3 // from the minimum to the maximum
)

// QualTickNs is the qualifier's clock (the 250 MHz acquisition clock).
const QualTickNs = 4

// QualMaxTicks is the most ticks a limit holds (16 bits).
const QualMaxTicks = 1<<16 - 1

// QualMaxPrescale is the most clocks a tick holds (a power of two): 65535
// ticks of it are 17 s, past the longest limit a trigger takes (10 s).
const QualMaxPrescale = 1 << 16

// ZoneClockNs is the zones' clock (the 125 MHz half clock): buckets are a
// power of two of it.
const ZoneClockNs = 8

// ZoneDecideClocks is how many zone clocks after the wait the zone trigger
// fires: it fires (Wait*Bucket + ZoneDecideClocks) zone clocks and
// ZoneFireNs after the edge, whatever the zones (fpga/images/qualify/zone_qual.v).
const ZoneDecideClocks = 48

// ZoneFireNs is the rest of the zone trigger's delay after an edge
// trigger's: the hand-over between the two clocks and the fire's registers
// (the testbench's 5 or 6 acquisition clocks, one for the zones' copy of the
// samples and one to the trigger), 28 or 32 ns.
const ZoneFireNs = 30

// QualFireNs is how long after an edge trigger's the qualifier's pulse or
// video trigger fires on the same closing edge: three acquisition clocks (its
// copy of the samples, its registered edges and its decision).
const QualFireNs = 12

// QualSlopeFireNs is the same for a slope, whose levels compare sums of five
// samples: three clocks of sums, one less of comparison, and the sum's two
// samples of delay on an edge.
const QualSlopeFireNs = 24

// QualifierConfig is the qualifier image's hardware trigger: pulse width,
// slope time or video line, decided in the FPGA (ADR-HARDWARE-QUALIFIERS).
// Pulse and video fire on the trigger level's edges (TriggerLevel on
// Channel); a slope crosses LevelA (low) and LevelB (high). Times are in
// ticks of Prescale clocks of QualTickNs.
// TRLC-LINKS: REQ-SDS-011
type QualifierConfig struct {
	Enabled  bool   `json:"enabled"`
	Kind     uint8  `json:"kind"`
	Channel  uint8  `json:"channel"`
	Negative bool   `json:"negative"` // a low pulse, a falling slope, a negative sync
	Cond     uint8  `json:"cond"`
	MinTicks uint32 `json:"min_ticks"`
	MaxTicks uint32 `json:"max_ticks"`
	Prescale uint32 `json:"prescale"` // clocks a tick, a power of two 1..QualMaxPrescale
	LevelA   uint8  `json:"level_a"`
	LevelB   uint8  `json:"level_b"`
	Line     uint16 `json:"line"` // video: lines after the vertical sync (0 every line)
}

// TRLC-LINKS: REQ-SDS-011
func (p QualifierConfig) validate() error {
	if !p.Enabled {
		return nil
	}
	if p.Kind > QualVideo || p.Channel > 1 || p.Cond > QualBetween || p.MinTicks > QualMaxTicks || p.MaxTicks > QualMaxTicks ||
		p.Prescale < 1 || p.Prescale > QualMaxPrescale || p.Prescale&(p.Prescale-1) != 0 || p.Line > 1023 {
		return fmt.Errorf("sramcapture: invalid hardware qualifier configuration")
	}
	return nil
}

// SupportsQualifier reports the qualifier block (the qualifier image).
// TRLC-LINKS: REQ-SDS-011
func (c *Capture) SupportsQualifier() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.read(qualCapability)
	return v == 0x5154 && err == nil, err
}

// configureQualifier writes the qualifier block, or disables it; a fabric
// without the block refuses an enabled qualifier.
// TRLC-LINKS: REQ-SDS-011
func (c *Capture) configureQualifier(revision uint16, p QualifierConfig) error {
	var capability uint16
	if revision >= 10 {
		v, err := c.read(qualCapability)
		if err != nil {
			return err
		}
		capability = v
	}
	if capability != 0x5154 {
		if p.Enabled {
			return fmt.Errorf("sramcapture: fabric lacks hardware trigger qualifiers")
		}
		return nil
	}
	var control uint16
	if p.Enabled {
		minM1, minZero := uint32(0), p.MinTicks == 0
		if !minZero {
			minM1 = p.MinTicks - 1
		}
		control = 1 | uint16(p.Kind)<<1 | uint16(p.Cond)<<4 | uint16(p.Channel)<<6
		if p.Negative {
			control |= 8
		}
		if minZero {
			control |= 0x80
		}
		for _, r := range []struct{ sel, value uint16 }{
			{22, uint16(minM1)}, {24, uint16(p.MaxTicks)},
			{26, uint16(p.LevelB)<<8 | uint16(p.LevelA)}, {27, p.Line}, {28, uint16(p.Prescale - 1)},
		} {
			if err := c.write(r.sel, r.value); err != nil {
				return err
			}
		}
	}
	return c.write(21, control)
}

// The qualifier block's registers (free in every image: the host bus has
// seven address bits): read 74 the block, 75 its kinds; write 21, 22, 24
// and 26..28 the qualifier, 83..92 and 69..73 the zones.
const qualCapability = 74

// Zones is the zone trigger in the FPGA (ADR-HARDWARE-QUALIFIERS): up to four
// rectangles, each a window in buckets around the trigger edge and a range of
// codes on one channel; an edge qualifies when every intersect zone was
// entered in its window and every avoid zone was not. The FPGA fires Wait
// buckets after the edge's, when the latest window has ended.
// TRLC-LINKS: REQ-SDS-014
type ZoneConfig struct {
	Zones  [4]ZoneWindow `json:"zones"`
	Wait   uint16        `json:"wait"`         // buckets from the edge's to the decision, 1..1023
	Bucket uint16        `json:"bucket_clock"` // half clocks a bucket, a power of two 1..4096
}

// ZoneWindow is one zone: Start..End buckets from the edge's (signed),
// codes Lo..Hi on Channel.
// TRLC-LINKS: REQ-SDS-014
type ZoneWindow struct {
	Enabled bool  `json:"enabled"`
	Channel uint8 `json:"channel"`
	Lo      uint8 `json:"lo"`
	Hi      uint8 `json:"hi"`
	Start   int16 `json:"start"`
	End     int16 `json:"end"`
	Avoid   bool  `json:"avoid"`
}

// Enabled reports zones to test.
// TRLC-LINKS: REQ-SDS-014
func (z ZoneConfig) Enabled() bool {
	for _, w := range z.Zones {
		if w.Enabled {
			return true
		}
	}
	return false
}

// TRLC-LINKS: REQ-SDS-014
func (z ZoneConfig) validate() error {
	if !z.Enabled() {
		return nil
	}
	if z.Wait < 1 || z.Wait > 1023 || z.Bucket < 1 || z.Bucket > 4096 || z.Bucket&(z.Bucket-1) != 0 {
		return fmt.Errorf("sramcapture: invalid hardware zone configuration")
	}
	for _, w := range z.Zones {
		// the oldest count read must still be in the 1024-bucket history
		if w.Enabled && (w.Channel > 1 || w.Start > w.End || w.Start < -1000 || w.End > 1000 || int(z.Wait)-int(w.Start)+1 > 1023 || int(w.End)+1 > int(z.Wait)) {
			return fmt.Errorf("sramcapture: zone window outside the hardware history")
		}
	}
	return nil
}

// configureZones writes the zone block, or disables it.
// TRLC-LINKS: REQ-SDS-014
func (c *Capture) configureZones(revision uint16, z ZoneConfig) error {
	var kinds uint16
	if revision >= 10 {
		if v, err := c.read(qualCapability); err != nil {
			return err
		} else if v == 0x5154 {
			if kinds, err = c.read(qualCapability + 1); err != nil {
				return err
			}
		}
	}
	if kinds&8 == 0 {
		if z.Enabled() {
			return fmt.Errorf("sramcapture: fabric lacks the hardware zone trigger")
		}
		return nil
	}
	var control uint16
	if z.Enabled() {
		starts, ends := []uint16{90, 91, 92, 69}, []uint16{70, 71, 72, 73}
		regs := []struct{ sel, value uint16 }{{84, z.Wait}, {85, z.Bucket - 1}}
		for i, w := range z.Zones {
			if !w.Enabled {
				continue
			}
			control |= 1 << i
			control |= uint16(w.Channel) << (4 + i)
			if w.Avoid {
				control |= 1 << (8 + i)
			}
			regs = append(regs, struct{ sel, value uint16 }{uint16(86 + i), uint16(w.Hi)<<8 | uint16(w.Lo)},
				struct{ sel, value uint16 }{starts[i], uint16(w.Start) & 0x7ff}, struct{ sel, value uint16 }{ends[i], uint16(w.End) & 0x7ff})
		}
		for _, r := range regs {
			if err := c.write(r.sel, r.value); err != nil {
				return err
			}
		}
	}
	return c.write(83, control)
}
