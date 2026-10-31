// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"fmt"
)

// The history ring (ADR-STREAM-RING): on an image with it (the bits image),
// register 19 bit 5 runs the digital stream on chip - its slack the drum's
// FIFO and the two host banks - while the same arm runs the record in the
// SRAM as a ring of precision words, at the record's decimation, until a
// stop freezes it (the first stop; the second ends the stream). Register 21
// holds the stream's own decimation, coarser than the ring's. Reads: 61 the
// capability (0x5247), 62/63 the frozen ring's newest word's time in raw
// words (4 ns, two samples a line) from the stream's start - the origin the
// stream's own words count from - wrapping at 2^32.

// streamRingCapability is register 61 of an image with the history ring.
const streamRingCapability = 0x5247

// regStreamLog is the stream's decimation in ring mode.
const regStreamLog = 21

// StreamRingSupported reports an image with the history ring.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func (c *Capture) StreamRingSupported() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	rev, err := c.read(13)
	if err != nil || rev != 11 {
		return false
	}
	v, err := c.read(61)
	return err == nil && v == streamRingCapability
}

// StartStreamRing starts the continuous digital stream at decimation
// streamLog on chip and arms cfg as the history ring (cfg's decimation is
// the ring's; nothing triggers it). Every word reaches DrainStream from
// ordinal 0 as with StartStream; FreezeRing stops the ring.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func (c *Capture) StartStreamRing(ctx context.Context, cfg Config, streamLog uint8) error {
	if cfg.DecimationLog2 < 8 || cfg.DecimationLog2 > 20 {
		return fmt.Errorf("sramcapture: ring decimation %d, need 8..20", cfg.DecimationLog2)
	}
	if streamLog < 8 || streamLog > 20 {
		return fmt.Errorf("sramcapture: stream decimation %d, need 8..20", streamLog)
	}
	c.mu.Lock()
	rev, err := c.read(13)
	if err == nil && rev != 11 {
		err = fmt.Errorf("sramcapture: stream requires revision 11")
	}
	if err == nil {
		var v uint16
		if v, err = c.read(61); err == nil && v != streamRingCapability {
			err = fmt.Errorf("sramcapture: this image has no history ring")
		}
	}
	if err == nil {
		err = c.prepareStreamBuffers()
	}
	if err == nil {
		// Both latched by the fabric while idle, so before the arm.
		err = c.write(regStreamLog, uint16(streamLog))
	}
	if err == nil {
		err = c.write(19, 1|2|4|32) // stream, continuous, digital words, ring
	}
	c.ringStream = err == nil
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if err = c.Arm(ctx, cfg); err != nil {
		c.mu.Lock()
		c.write(19, 0)
		c.ringStream = false
		c.mu.Unlock()
	}
	return err
}

// FreezeRing stops the history ring and reads it: the frozen record (a
// halted ring: its trigger index is its end) and the newest word's time in
// raw words. The stream runs on until StopStream.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func (c *Capture) FreezeRing(ctx context.Context) (Metadata, uint32, error) {
	c.mu.Lock()
	ring := c.ringStream
	c.mu.Unlock()
	if !ring {
		return Metadata{}, 0, fmt.Errorf("sramcapture: no history ring runs")
	}
	if err := c.Halt(ctx); err != nil {
		return Metadata{}, 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, err := c.status()
	if err != nil {
		return m, 0, err
	}
	if !m.Frozen || !m.Ready || m.Length == 0 {
		return m, 0, fmt.Errorf("sramcapture: the history ring did not freeze")
	}
	last, err := c.read32(62)
	return m, last, err
}
