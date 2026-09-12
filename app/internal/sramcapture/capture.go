// Package sramcapture owns the full-depth acquisition probe's CS1 protocol.
// It does not load a fabric, open/close a device, or access CS3. The caller
// supplies the already inherited bus and must give this backend exclusive
// ownership while it is in use; its register ABI differs from default.v.
package sramcapture

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const (
	Words             = uint32(1 << 19)
	Bytes             = Words * 4
	SamplesPerChannel = Words * 2
	FabricID          = uint16(0x5a52)
	// QualifiedMapID comes from lanecal-2026-09-11 and lanemap_seed.vh.
	QualifiedMapID = uint16(0xe192)
	addressMask    = Words - 1
)

// Bus is the subset of bus.Dev used here. RawWrite bypasses the OLD default
// fabric's selector schema, never the plane guard: all accesses here are CS1.
type Bus interface {
	Read(plane uint8, selector uint16) (uint16, error)
	RawWrite(selector, value uint16) error
}

type Capture struct {
	mu    sync.Mutex
	bus   Bus
	beats atomic.Uint64
}

type Source uint8

const (
	ADC Source = iota
	Counter
)

// Config counts 32-bit SRAM words. Each ADC word contains two consecutive
// sample pairs, byte order CH1[n], CH2[n], CH1[n+1], CH2[n+1]. PreWords excludes
// the triggering word; PostWords includes it. Pair and TriggerChannel are
// zero based. Cross-core analog timing is not implied by this API.
type Config struct {
	Source         Source `json:"source"`
	Pair           uint8  `json:"pair"`
	PreWords       uint32 `json:"pre_words"`
	PostWords      uint32 `json:"post_words"`
	Normal         bool   `json:"normal"`
	Falling        bool   `json:"falling"`
	TriggerChannel uint8  `json:"trigger_channel"`
	TriggerLevel   uint8  `json:"trigger_level"`
}

// Metadata counters are stable for recall only when Ready and Frozen are true.
// TriggerIndex is a WORD index, not a sample-half or interpolated timestamp.
type Metadata struct {
	Locked       bool   `json:"locked"`
	Ready        bool   `json:"ready"`
	Running      bool   `json:"running"`
	Frozen       bool   `json:"frozen"`
	Triggered    bool   `json:"triggered"`
	Prefetched   bool   `json:"prefetched"`
	Length       uint32 `json:"words"`
	Start        uint32 `json:"start"`
	TriggerIndex uint32 `json:"trigger_index"`
	Position     uint32 `json:"position"`
	Origin       uint32 `json:"origin"`
	Revision     uint16 `json:"revision"`
	MapID        uint16 `json:"map_id"`
	SampleRateHz uint32 `json:"sample_rate_hz"`
	Interleaved  bool   `json:"interleaved"`
	DataFault    bool   `json:"data_fault"`
}

func New(b Bus) (*Capture, error) {
	c := &Capture{bus: b}
	id, err := c.read(0)
	if err != nil {
		return nil, err
	}
	if id != FabricID {
		return nil, fmt.Errorf("sramcapture: fabric %04x, expected %04x (do not use the default fabric ABI)", id, FabricID)
	}
	rev, err := c.read(13)
	if err != nil {
		return nil, err
	}
	if rev < 4 {
		return nil, fmt.Errorf("sramcapture: revision %d lacks the corrected ADC map", rev)
	}
	m, err := c.read(14)
	if err != nil {
		return nil, err
	}
	if m != QualifiedMapID {
		return nil, fmt.Errorf("sramcapture: unqualified ADC map %04x, expected %04x", m, QualifiedMapID)
	}
	return c, nil
}
func (c *Capture) read(s uint16) (uint16, error) {
	v, err := c.bus.Read(1, s)
	if err == nil {
		c.beats.Add(1)
	}
	return v, err
}

// Beats advances on successful bus activity, including a long record recall.
// A supervisor can observe progress without taking ownership from a transfer.
func (c *Capture) Beats() uint64           { return c.beats.Load() }
func (c *Capture) write(s, v uint16) error { return c.bus.RawWrite(s, v) }
func (c *Capture) read32(s uint16) (uint32, error) {
	lo, e := c.read(s)
	if e != nil {
		return 0, e
	}
	hi, e := c.read(s + 1)
	return uint32(lo) | uint32(hi)<<16, e
}
func (c *Capture) write32(s uint16, v uint32) error {
	if e := c.write(s, uint16(v)); e != nil {
		return e
	}
	return c.write(s+1, uint16(v>>16))
}

func (c *Capture) Status() (Metadata, error) { c.mu.Lock(); defer c.mu.Unlock(); return c.status() }
func (c *Capture) status() (m Metadata, err error) {
	s, err := c.read(1)
	if err != nil {
		return m, err
	}
	m.Ready = s&4 != 0
	m.Locked = s&64 != 0
	m.Running = s&8 != 0
	m.Frozen = s&16 != 0
	m.Triggered = s&32 != 0
	m.Prefetched = s&1024 != 0
	for _, f := range []struct {
		sel uint16
		v   *uint32
	}{{2, &m.Length}, {4, &m.Start}, {6, &m.TriggerIndex}, {8, &m.Position}, {10, &m.Origin}} {
		*f.v, err = c.read32(f.sel)
		if err != nil {
			return m, err
		}
	}
	m.Revision, err = c.read(13)
	if err != nil {
		return m, err
	}
	m.MapID, err = c.read(14)
	if err != nil {
		return m, err
	}
	m.SampleRateHz = 100000000
	if m.Revision >= 6 {
		rate, e := c.read(15)
		if e != nil {
			return m, e
		}
		if rate != 500 {
			return m, fmt.Errorf("sramcapture: unknown interleave rate %d", rate)
		}
		flags, e := c.read(19)
		if e != nil {
			return m, e
		}
		m.SampleRateHz = uint32(rate) * 1000000
		m.Interleaved = true
		m.DataFault = flags&1 != 0
		m.Locked = m.Locked && flags&2 != 0
	}
	return m, nil
}

func (c *Capture) command(ctx context.Context, op uint16) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s, e := c.read(1)
	if e != nil {
		return e
	}
	ack := s & 512
	if e = c.write(1, op); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for {
		if e = ctx.Err(); e != nil {
			return fmt.Errorf("sramcapture: command %d: %w", op, e)
		}
		s, e = c.read(1)
		if e != nil {
			return e
		}
		if s&512 != ack {
			if s&384 != 0 {
				return fmt.Errorf("sramcapture: command %d rejected (status %04x)", op, s)
			}
			return nil
		}
	}
}
func (c *Capture) wait(ctx context.Context, mask uint16) error {
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		s, e := c.read(1)
		if e != nil {
			return e
		}
		if s&mask == mask {
			return nil
		}
		timer := time.NewTimer(100 * time.Microsecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (c *Capture) ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return c.wait(ctx, 4)
}

func (c *Capture) Arm(ctx context.Context, cfg Config) error {
	if cfg.Source > Counter || cfg.Pair > 4 || cfg.TriggerChannel > 1 || cfg.PostWords == 0 || uint64(cfg.PreWords)+uint64(cfg.PostWords) > uint64(Words) {
		return fmt.Errorf("sramcapture: invalid source, channel, or record geometry")
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, e := c.read(1)
	if e != nil {
		return e
	}
	if s&4 == 0 || s&8 != 0 {
		return fmt.Errorf("sramcapture: halt the current acquisition before rearming")
	}
	revision, e := c.read(13)
	if e != nil {
		return e
	}
	if revision >= 6 && cfg.Pair != 0 {
		return fmt.Errorf("sramcapture: interleaved image uses all five pairs; pair must be zero")
	}
	word := uint16(cfg.Pair) << 1
	if cfg.Source == ADC {
		word |= 1
	}
	if cfg.Normal {
		word |= 16
	}
	if cfg.Falling {
		word |= 32
	}
	if cfg.TriggerChannel == 1 {
		word |= 64
	}
	if e = c.write32(2, cfg.PreWords); e != nil {
		return e
	}
	if e = c.write32(4, cfg.PostWords); e != nil {
		return e
	}
	if e = c.write(6, word); e != nil {
		return e
	}
	if e = c.write(7, uint16(cfg.TriggerLevel)); e != nil {
		return e
	}
	return c.command(ctx, 1)
}
func (c *Capture) Force(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.command(ctx, 4)
}
func (c *Capture) Halt(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.command(ctx, 5); e != nil {
		return e
	}
	return c.ready(ctx)
}
func (c *Capture) WaitFrozen(ctx context.Context) error {
	// Do not hold ownership while waiting for an external trigger: another
	// caller must remain able to Force or Halt this acquisition.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		s, err := c.read(1)
		c.mu.Unlock()
		if err != nil {
			return err
		}
		if s&20 == 20 {
			return nil
		}
		timer := time.NewTimer(100 * time.Microsecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (c *Capture) Snapshot(ctx context.Context) (out [10]uint8, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err = c.command(ctx, 6); err != nil {
		return out, err
	}
	for {
		flags, e := c.read(1)
		if e != nil {
			return out, e
		}
		if flags&2048 == 0 {
			break
		}
		if e = ctx.Err(); e != nil {
			return out, e
		}
	}
	for i := 0; i < 5; i++ {
		v, e := c.read(uint16(20 + i))
		if e != nil {
			return out, e
		}
		out[2*i] = uint8(v)
		out[2*i+1] = uint8(v >> 8)
	}
	return out, nil
}

// Recall streams an immutable frozen window. It never rearms or writes SRAM.
// A failed/partial host write leaves the record intact for another recall.
// Zero count means an empty window, not full depth. Byte layout is documented
// on Config; the caller can use 2*i and 2*i+1 to split channels.
func (c *Capture) Recall(ctx context.Context, offset, count uint32, dst io.Writer) (written int64, err error) {
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, err := c.status()
	if err != nil {
		return 0, err
	}
	if m.DataFault {
		return 0, fmt.Errorf("sramcapture: interleave FIFO fault; record has missing samples")
	}
	if !m.Ready || !m.Frozen || m.Running {
		return 0, fmt.Errorf("sramcapture: record is not frozen")
	}
	if m.Length > Words || uint64(offset)+uint64(count) > uint64(m.Length) {
		return 0, fmt.Errorf("sramcapture: recall window exceeds record")
	}
	// Revision 7 qualifies fresh bursts with a discarded SRAM warm-up prefix.
	// These reads wrap physically but never add samples to the returned window.
	prefix := uint32(0)
	if m.Revision >= 7 {
		prefix = 16
	}
	var data [512 * 4]byte
	for copied := uint32(0); copied < count; {
		if err = ctx.Err(); err != nil {
			return written, err
		}
		n := count - copied
		if n > 512-prefix {
			n = 512 - prefix
		}
		target := (m.Origin + m.Start + offset + copied - prefix) & addressMask
		position, e := c.read32(8)
		if e != nil {
			return written, e
		}
		flags, e := c.read(1)
		if e != nil {
			return written, e
		}
		op := uint16(2)
		if prefix == 0 && flags&1024 != 0 && target == (position-1)&addressMask {
			op = 7
		} else {
			skip := (target - position) & addressMask
			if skip != 0 {
				if e = c.write32(8, skip); e != nil {
					return written, e
				}
				if e = c.command(ctx, 3); e != nil {
					return written, e
				}
				if e = c.ready(ctx); e != nil {
					return written, e
				}
			}
		}
		if e = c.write32(8, n+prefix); e != nil {
			return written, e
		}
		if e = c.command(ctx, op); e != nil {
			return written, e
		}
		if e = c.ready(ctx); e != nil {
			return written, e
		}
		got, e := c.read(12)
		if e != nil {
			return written, e
		}
		if uint32(got) != n+prefix {
			return written, fmt.Errorf("sramcapture: short read buffer: got %d want %d", got, n+prefix)
		}
		for i := uint32(0); i < n; i++ {
			if e = c.write(16, uint16(i+prefix)); e != nil {
				return written, e
			}
			v, e := c.read32(17)
			if e != nil {
				return written, e
			}
			binary.LittleEndian.PutUint32(data[4*i:], v)
		}
		nn, e := dst.Write(data[:4*n])
		written += int64(nn)
		if e != nil {
			return written, e
		}
		if nn != int(4*n) {
			return written, io.ErrShortWrite
		}
		copied += n
	}
	return written, nil
}
