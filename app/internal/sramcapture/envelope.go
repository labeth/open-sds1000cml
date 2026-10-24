// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"fmt"
	"io"
)

// EnvelopeOutputWords is the reducer's output limit: a request beyond it
// produces nothing (2048 Q8.8 buckets, 4096 words, give 0).
const EnvelopeOutputWords = 2048

// EnvelopeBuckets is the envelope recall's output length for display: raw
// records give two samples (minimum, maximum) per bucket per channel, one word
// a bucket; 4096 points keep decoding a live frame meaningful. Q8.8 records
// take two words a bucket, so they get EnvelopeBucketsQ8.
const (
	EnvelopeBuckets   = EnvelopeOutputWords
	EnvelopeBucketsQ8 = EnvelopeOutputWords / 2
)

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
	if err != nil {
		return false, err
	}
	return c.envelopeCapabilityOf(revision)
}

// envelopeCapabilityOf is envelopeCapability on a fabric of the revision read.
// TRLC-LINKS: REQ-SDS-010
func (c *Capture) envelopeCapabilityOf(revision uint16) (bool, error) {
	if revision != 10 {
		return false, nil
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
	return m, c.recallEnvelope(ctx, m, offset, bucket, buckets, dst)
}

// recallEnvelope reduces the window of the record m, read just now, into dst.
// Called with c.mu held.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-033
func (c *Capture) recallEnvelope(ctx context.Context, m Metadata, offset, bucket, buckets uint32, dst io.Writer) error {
	if present, err := c.envelopeCapabilityOf(m.Revision); err != nil || !present {
		return fmt.Errorf("sramcapture: fabric lacks the envelope recall: %v", err)
	}
	if m.DataFault {
		return fmt.Errorf("sramcapture: interleave FIFO fault; record has missing samples")
	}
	if !m.Ready || !m.Frozen || m.Running {
		return fmt.Errorf("sramcapture: record is not frozen")
	}
	q8 := m.FractionBits == 8
	perBucket := uint32(1)
	if q8 {
		perBucket = 2
	}
	words := bucket * buckets
	// The FPGA reduces two-word packets: buckets are an even number of words.
	if bucket < 2 || bucket%2 != 0 || bucket > 0xffffe || buckets == 0 || buckets*perBucket > EnvelopeOutputWords || (!q8 && buckets%2 != 0) ||
		uint64(offset)+uint64(words) > uint64(m.Length) || words+16 > Words {
		return fmt.Errorf("sramcapture: invalid envelope window")
	}
	const prefix = 16
	target := (m.Origin + m.Start + offset - prefix) & addressMask
	// The position's two words in one batch, as the writes below: this
	// runs for every live frame.
	var pw [2]uint16
	if err := c.readMany([]uint16{8, 9}, pw[:]); err != nil {
		return err
	}
	position := uint32(pw[0]) | uint32(pw[1])<<16
	var err error
	if skip := (target - position) & addressMask; skip != 0 {
		if err = c.writeMany([2]uint16{8, uint16(skip)}, [2]uint16{9, uint16(skip >> 16)}); err != nil {
			return err
		}
		if err = c.command(ctx, 3); err != nil {
			return err
		}
		if err = c.ready(ctx); err != nil {
			return err
		}
	}
	control := uint16(0x8000) | uint16(bucket>>16)
	if q8 {
		control |= 0x4000
	}
	c.envelopeOn = true
	defer func() {
		// Leave normal recalls unaffected even when the reduction failed.
		if c.envelopeOn && c.write(44, 0) == nil {
			c.envelopeOn = false
		}
	}()
	total := words + prefix
	if err = c.writeMany([2]uint16{43, uint16(bucket)}, [2]uint16{44, control}, [2]uint16{45, prefix},
		[2]uint16{8, uint16(total)}, [2]uint16{9, uint16(total >> 16)}); err != nil {
		return err
	}
	if err = c.command(ctx, 2); err != nil {
		// Diagnostic: record what the transport reported, then retry once.
		s, _ := c.read(1)
		pos, _ := c.read32(8)
		c.envelopeRejects++
		c.envelopeLast = fmt.Sprintf("op2 rejected: status %04x position %d target %d words %d: %v", s, pos, target, words, err)
		if err = c.ready(ctx); err != nil {
			return err
		}
		if err = c.command(ctx, 2); err != nil {
			return fmt.Errorf("%s; retry: %w", c.envelopeLast, err)
		}
	}
	if err = c.ready(ctx); err != nil {
		return err
	}
	out := buckets * perBucket
	// The 125 MHz reducer finishes a few clocks after the transport.
	got, err := c.read(46)
	if err == nil && uint32(got) != out {
		got, err = c.read(46)
	}
	if err != nil {
		return err
	}
	if uint32(got) != out {
		return fmt.Errorf("sramcapture: envelope produced %d words, want %d", got, out)
	}
	// Leave normal recalls unaffected.
	if err = c.write(44, 0); err != nil {
		return err
	}
	c.envelopeOn = false
	if cap(c.recallHalves) < int(out*2) {
		c.recallHalves = make([]uint16, out*2)
	}
	halves := c.recallHalves[:out*2]
	if err = c.write(16, 0); err != nil {
		return err
	}
	if _, err = c.read(17); err != nil {
		return err
	}
	if err = c.write(17, 0); err != nil {
		return err
	}
	if pop, ok := c.bus.(interface{ PopWordsChecked(uint16, []uint16) error }); ok {
		if err = pop.PopWordsChecked(25, halves); err != nil {
			return err
		}
	} else {
		for i := range halves {
			if halves[i], err = c.read(25); err != nil {
				return err
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
	return err
}
