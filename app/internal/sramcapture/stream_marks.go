// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"fmt"
)

// Stream edge marks (ADR-SOUND): the stream image's edge comparator, on the
// decimated words the stream carries, marks the stream word a level
// crossing falls in while a continuous stream runs. Register 19's bits 3
// and 4 ask for rising and falling marks; the channel and level are the
// trigger's (registers 6 and 7), latched live while the drum streams.
// Reads: 54 the capability (0x4D01), 55 the rising marks' count (wrapping),
// 56/57 the newest rising mark's word ordinal, 58 the falling count, 59/60
// the newest falling mark's word. A count read equal before and after its
// word belongs to it.

// streamMarkCapability is register 54 of an image with stream marks.
const streamMarkCapability = 0x4D01

// StreamMarkMode chooses the directions marked.
type StreamMarkMode struct {
	Rise, Fall bool
}

// bits is the mode's register 19 bits.
// TRLC-LINKS: REQ-SDS-272
func (m StreamMarkMode) bits() uint16 {
	v := uint16(0)
	if m.Rise {
		v |= 8
	}
	if m.Fall {
		v |= 16
	}
	return v
}

// StreamMarkRead is the image's newest marks.
type StreamMarkRead struct {
	RiseCount, FallCount uint16
	RiseWord, FallWord   uint32
}

// StreamMarksSupported reports an image with stream marks.
// TRLC-LINKS: REQ-SDS-272
func (c *Capture) StreamMarksSupported() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	rev, err := c.read(13)
	if err != nil || rev != 11 {
		return false
	}
	v, err := c.read(54)
	return err == nil && v == streamMarkCapability
}

// StartStreamMarked starts a continuous stream of decimated words (cfg's
// decimation, no trigger) with the marks asked for.
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-265, REQ-SDS-035
func (c *Capture) StartStreamMarked(ctx context.Context, cfg Config, m StreamMarkMode) error {
	if cfg.DecimationLog2 < 8 || cfg.DecimationLog2 > 20 {
		return fmt.Errorf("sramcapture: stream decimation %d, need 8..20", cfg.DecimationLog2)
	}
	c.mu.Lock()
	rev, err := c.read(13)
	if err == nil && rev != 11 {
		err = fmt.Errorf("sramcapture: stream requires revision 11")
	}
	if err == nil {
		err = c.prepareStreamBuffers()
	}
	if err == nil {
		err = c.write(19, 1|2|m.bits()) // stream, continuous, the marks
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if err = c.Arm(ctx, cfg); err != nil {
		c.mu.Lock()
		c.write(19, 0)
		c.mu.Unlock()
	}
	return err
}

// SetStreamMarks changes the marks' channel, level, hysteresis and
// directions on the running stream.
// TRLC-LINKS: REQ-SDS-272
func (c *Capture) SetStreamMarks(ch int, level, hyst uint8, m StreamMarkMode) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	word := uint16(1 | 16) // the ADC source, normal
	if ch&1 == 1 {
		word |= 64
	}
	if err := c.write(6, word); err != nil {
		return err
	}
	if err := c.write(7, uint16(level)|uint16(hyst)<<8); err != nil {
		return err
	}
	return c.write(19, 1|2|m.bits())
}

// StreamMarks reads the newest marks: each count is read before and after
// its word, and the pair read again (up to three times) when a mark came in
// between.
// TRLC-LINKS: REQ-SDS-272
func (c *Capture) StreamMarks() (StreamMarkRead, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var r StreamMarkRead
	for k, regs := range [2][3]uint16{{55, 56, 57}, {58, 59, 60}} {
		var count uint16
		var word uint32
		ok := false
		for try := 0; try < 3 && !ok; try++ {
			// count, word, count again in one batch (in this order)
			var v [4]uint16
			if err := c.readMany([]uint16{regs[0], regs[1], regs[2], regs[0]}, v[:]); err != nil {
				return r, err
			}
			count, word, ok = v[3], uint32(v[1])|uint32(v[2])<<16, v[0] == v[3]
		}
		if !ok {
			return r, fmt.Errorf("sramcapture: stream marks kept changing")
		}
		if k == 0 {
			r.RiseCount, r.RiseWord = count, word
		} else {
			r.FallCount, r.FallWord = count, word
		}
	}
	return r, nil
}
