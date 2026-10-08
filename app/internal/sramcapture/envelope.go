// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"fmt"
	"io"
)

// EnvelopeBuckets is the envelope recall's output length for display: raw
// records give two samples (minimum, maximum) per bucket per channel.
const EnvelopeBuckets = 1024

// EnvelopeDiagnostics reports rejected envelope commands and the last one.
// TRLC-LINKS: REQ-SDS-010
func (c *Capture) EnvelopeDiagnostics() (int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.envelopeRejects, c.envelopeLast
}

// SupportsEnvelope reports the envelope recall (ADR-IMAGE-REGROUP-DECIMATION).
// TRLC-LINKS: REQ-SDS-010
func (c *Capture) SupportsEnvelope() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.envelopeCapability()
}

// TRLC-LINKS: REQ-SDS-010
func (c *Capture) envelopeCapability() (bool, error) {
	revision, err := c.read(13)
	if err != nil || revision != 10 {
		return false, err
	}
	v, err := c.read(43)
	return v == 0x454e, err
}

// RecallEnvelope reduces the frozen record window [offset, offset+buckets*bucket)
// words to buckets entries in the FPGA: per bucket and channel the minimum and
// maximum, in the record's own word format. Raw records give one word per
// bucket, {max2,max1,min2,min1} — two raw samples (min, max) per channel —
// and need an even bucket count. Q8.8 records give two words, the minima then
// the maxima. The deep record stays in SRAM for full recall.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-033
func (c *Capture) RecallEnvelope(ctx context.Context, offset, bucket, buckets uint32, dst io.Writer) (Metadata, error) {
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, err := c.status()
	if err != nil {
		return m, err
	}
	if present, err := c.envelopeCapability(); err != nil || !present {
		return m, fmt.Errorf("sramcapture: fabric lacks the envelope recall: %v", err)
	}
	if m.DataFault {
		return m, fmt.Errorf("sramcapture: interleave FIFO fault; record has missing samples")
	}
	if !m.Ready || !m.Frozen || m.Running {
		return m, fmt.Errorf("sramcapture: record is not frozen")
	}
	q8 := m.FractionBits == 8
	perBucket := uint32(1)
	if q8 {
		perBucket = 2
	}
	words := bucket * buckets
	// The FPGA reduces two-word packets: buckets are an even number of words.
	if bucket < 2 || bucket%2 != 0 || bucket > 0xffffe || buckets == 0 || buckets*perBucket > 4096 || (!q8 && buckets%2 != 0) ||
		uint64(offset)+uint64(words) > uint64(m.Length) || words+16 > Words {
		return m, fmt.Errorf("sramcapture: invalid envelope window")
	}
	const prefix = 16
	target := (m.Origin + m.Start + offset - prefix) & addressMask
	position, err := c.read32(8)
	if err != nil {
		return m, err
	}
	if skip := (target - position) & addressMask; skip != 0 {
		if err = c.write32(8, skip); err != nil {
			return m, err
		}
		if err = c.command(ctx, 3); err != nil {
			return m, err
		}
		if err = c.ready(ctx); err != nil {
			return m, err
		}
	}
	control := uint16(0x8000) | uint16(bucket>>16)
	if q8 {
		control |= 0x4000
	}
	c.envelopeOn = true
	for _, r := range []struct{ sel, value uint16 }{{43, uint16(bucket)}, {44, control}, {45, prefix}} {
		if err = c.write(r.sel, r.value); err != nil {
			return m, err
		}
	}
	if err = c.write32(8, words+prefix); err != nil {
		return m, err
	}
	if err = c.command(ctx, 2); err != nil {
		// Diagnostic: record what the transport reported, then retry once.
		s, _ := c.read(1)
		pos, _ := c.read32(8)
		c.envelopeRejects++
		c.envelopeLast = fmt.Sprintf("op2 rejected: status %04x position %d target %d words %d: %v", s, pos, target, words, err)
		if err = c.ready(ctx); err != nil {
			return m, err
		}
		if err = c.command(ctx, 2); err != nil {
			return m, fmt.Errorf("%s; retry: %w", c.envelopeLast, err)
		}
	}
	if err = c.ready(ctx); err != nil {
		return m, err
	}
	out := buckets * perBucket
	// The 125 MHz reducer finishes a few clocks after the transport.
	got, err := c.read(46)
	if err == nil && uint32(got) != out {
		got, err = c.read(46)
	}
	if err != nil {
		return m, err
	}
	if uint32(got) != out {
		return m, fmt.Errorf("sramcapture: envelope produced %d words, want %d", got, out)
	}
	// Leave normal recalls unaffected.
	if err = c.write(44, 0); err != nil {
		return m, err
	}
	c.envelopeOn = false
	if cap(c.recallHalves) < int(out*2) {
		c.recallHalves = make([]uint16, out*2)
	}
	halves := c.recallHalves[:out*2]
	if err = c.write(16, 0); err != nil {
		return m, err
	}
	if _, err = c.read(17); err != nil {
		return m, err
	}
	if err = c.write(17, 0); err != nil {
		return m, err
	}
	if pop, ok := c.bus.(interface{ PopWordsChecked(uint16, []uint16) error }); ok {
		if err = pop.PopWordsChecked(25, halves); err != nil {
			return m, err
		}
	} else {
		for i := range halves {
			if halves[i], err = c.read(25); err != nil {
				return m, err
			}
		}
	}
	data := make([]byte, out*4)
	for i, h := range halves {
		data[2*i], data[2*i+1] = byte(h), byte(h>>8)
	}
	n, err := dst.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return m, err
}
