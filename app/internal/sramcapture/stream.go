// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrNoStreamBlock is not EOF; observe Finished and drain remaining banks.
var ErrNoStreamBlock = errors.New("sramcapture: no stream block ready")

// ErrStreamDisabled is a drain while no stream is enabled: before a stream
// starts it is not yet a loss (a pump started ahead of its stream).
var ErrStreamDisabled = errors.New("sramcapture: streaming is disabled")

// TRLC-LINKS: REQ-SDS-035
type StreamStatus struct {
	Ready      uint8 `json:"ready"`
	Tokens     uint8 `json:"tokens"`
	LastSingle uint8 `json:"last_single"`
	Fault      bool  `json:"fault"`
	Finished   bool  `json:"finished"`
	Enabled    bool  `json:"enabled"`
	// The drum images say which fault fired and whether the SRAM ring holds a
	// backlog (the pump should then look again soon).
	BankOverrun  bool `json:"bank_overrun,omitempty"`
	InputOverrun bool `json:"input_overrun,omitempty"`
	ReadOverrun  bool `json:"read_overrun,omitempty"`
	FIFOFull     bool `json:"fifo_full,omitempty"`
	Backlog      bool `json:"backlog,omitempty"`
}

// TRLC-LINKS: REQ-SDS-035
func decodeStreamStatus(v uint16) StreamStatus {
	return StreamStatus{uint8(v & 3), uint8((v >> 2) & 3), uint8((v >> 4) & 3), v&64 != 0, v&128 != 0, v&256 != 0,
		v&512 != 0, v&1024 != 0, v&2048 != 0, v&4096 != 0, v&8192 != 0}
}

// TRLC-LINKS: REQ-SDS-035
func (c *Capture) StreamStatus() (StreamStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var v [2]uint16 // the revision and the status in one batch
	if e := c.readMany([]uint16{13, 32}, v[:]); e != nil {
		return StreamStatus{}, e
	}
	if v[0] != 11 {
		return StreamStatus{}, fmt.Errorf("sramcapture: stream requires experimental revision 11")
	}
	return decodeStreamStatus(v[1]), nil
}

// PrepareStream allocates and touches the host buffers before acquisition starts.
// Call while idle so first-block allocation cannot consume the bank deadline.
// TRLC-LINKS: REQ-SDS-035
func (c *Capture) PrepareStream() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	rev, err := c.read(13)
	if err != nil {
		return err
	}
	if rev != 11 {
		return fmt.Errorf("sramcapture: stream requires experimental revision 11")
	}
	return c.prepareStreamBuffers()
}

// TRLC-LINKS: REQ-SDS-035
func (c *Capture) prepareStreamBuffers() error {
	words, err := c.read(26)
	if err != nil {
		return err
	}
	// Two banks of words/2 each: 5120 is the drum images' (banks of 2560
	// words, more than a ring lap brings at decimation 256).
	if words != 4096 && words != 5120 && words != 8192 {
		return fmt.Errorf("sramcapture: unknown stream buffer %d", words)
	}
	c.streamWords = words
	if cap(c.streamHalves) < int(words) {
		c.streamHalves = make([]uint16, words)
		c.streamBytes = make([]byte, int(words)*2)
	}
	for i := 0; i < len(c.streamHalves); i += 1024 {
		c.streamHalves[i] = 0
	}
	for i := 0; i < len(c.streamBytes); i += 2048 {
		c.streamBytes[i] = 0
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-035
type StreamBlock struct {
	FirstWord uint64 `json:"first_word"`
	Words     uint32 `json:"words"`
	Bank      uint8  `json:"bank"`
}

// StreamReserver is a DrainStream sink the bank's words are popped straight
// into: Reserve hands out room for n bytes (two pieces where the room wraps),
// Commit makes the reserved bytes the sink's after every check passed; a
// drain that fails commits nothing. The worker's DMA-mapped ring is one: the
// bank lands where its reader takes it, with no copy in between.
// TRLC-LINKS: REQ-SDS-035
type StreamReserver interface {
	Reserve(n int) (a, b []byte, err error)
	Commit(n int)
}

// StreamBacklog reports whether the drum held a backlog at the last
// DrainStream: it then refills the host's banks only as its ring comes
// round, and a reader must be at each refill to catch up.
// TRLC-LINKS: REQ-SDS-035
func (c *Capture) StreamBacklog() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streamBacklog
}

// DrainStream copies an immutable bank, checks sequence, then releases it.
// It never rearms or changes timing/mode. A failed read retains bank ownership;
// callers must discard any partial sink write before retrying. A release-write
// error is ambiguous and requires abort/restart, not a blind retry. Expensive FIR
// work belongs after release. The inherited bus must have one owner.
// TRLC-LINKS: REQ-SDS-035
func (c *Capture) DrainStream(ctx context.Context, expected uint64, dst io.Writer) (StreamBlock, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var block StreamBlock
	if e := ctx.Err(); e != nil {
		return block, e
	}
	rev, e := c.read(13)
	if e != nil {
		return block, e
	}
	if rev != 11 {
		return block, fmt.Errorf("sramcapture: stream requires experimental revision 11")
	}
	raw, e := c.read(32)
	if e != nil {
		return block, e
	}
	s := decodeStreamStatus(raw)
	c.streamBacklog = s.Backlog
	if s.Fault {
		return block, fmt.Errorf("sramcapture: stream overrun or packetizer fault")
	}
	if !s.Enabled {
		return block, ErrStreamDisabled
	}
	if s.Ready == 0 {
		return block, ErrNoStreamBlock
	}
	found := false
	for bank := uint8(0); bank < 2; bank++ {
		if s.Ready&(1<<bank) == 0 {
			continue
		}
		var first uint64
		for part := uint16(0); part < 4; part++ {
			v, e := c.read(35 + uint16(bank)*4 + part)
			if e != nil {
				return block, e
			}
			first |= uint64(v) << (16 * part)
		}
		if first == expected {
			block.Bank = bank
			block.FirstWord = first
			found = true
			break
		}
	}
	if !found {
		return block, fmt.Errorf("sramcapture: missing stream word %d", expected)
	}
	if c.streamWords == 0 {
		if e = c.prepareStreamBuffers(); e != nil {
			return block, e
		}
	}
	count, e := c.read(33 + uint16(block.Bank))
	if e != nil {
		return block, e
	}
	if count == 0 || count > c.streamWords/4 {
		return block, fmt.Errorf("sramcapture: invalid stream packet count %d", count)
	}
	block.Words = uint32(count)*2 - uint32((s.LastSingle>>block.Bank)&1)
	base := uint16(block.Bank) * (c.streamWords / 2)
	// Prime the non-burst output to the same first word before DMA starts.
	if e = c.write(16, base); e != nil {
		return block, e
	}
	if _, e = c.read(17); e != nil {
		return block, e
	}
	if e = c.write(17, base*2); e != nil {
		return block, e
	}
	bytes := c.streamBytes[:block.Words*4]
	halves := c.streamHalves[:block.Words*2]
	bytePop, direct := c.bus.(interface{ PopBytesChecked(uint16, []byte) error })
	reserver, reserved := dst.(StreamReserver)
	reserved = reserved && direct
	if reserved {
		// The pop port's pointer runs on across the two pieces of a wrap.
		ra, rb, e := reserver.Reserve(len(bytes))
		if e != nil {
			return block, e
		}
		if e = bytePop.PopBytesChecked(25, ra); e != nil {
			return block, e
		}
		if len(rb) > 0 {
			if e = bytePop.PopBytesChecked(25, rb); e != nil {
				return block, e
			}
		}
	} else if direct {
		if e = bytePop.PopBytesChecked(25, bytes); e != nil {
			return block, e
		}
	} else if pop, ok := c.bus.(interface{ PopWordsChecked(uint16, []uint16) error }); ok {
		if e = pop.PopWordsChecked(25, halves); e != nil {
			return block, e
		}
	} else {
		for i := range halves {
			v, e := c.read(25)
			if e != nil {
				return block, e
			}
			halves[i] = v
		}
	}
	index, e := c.read(27)
	if e != nil {
		return block, e
	}
	if uint32(index) != (uint32(base)*2+block.Words*2)%(uint32(c.streamWords)*2) {
		return block, fmt.Errorf("sramcapture: stream burst pointer %d", index)
	}
	raw, e = c.read(32)
	if e != nil {
		return block, e
	}
	after := decodeStreamStatus(raw)
	mask := uint8(1 << block.Bank)
	changed := func(st StreamStatus) bool {
		return st.Fault || !st.Enabled || st.Ready&mask == 0 || (st.Tokens^s.Tokens)&mask != 0
	}
	if changed(after) {
		// A bank the host holds is not republished before its release, so
		// the status is read again before the stream ends: about one stream
		// start in four hundred ended here with no fault, overrun or backlog
		// set. A real change (a token or a fault) reads the same twice.
		if raw, e = c.read(32); e != nil {
			return block, e
		}
		if after = decodeStreamStatus(raw); changed(after) {
			return block, fmt.Errorf("sramcapture: stream changed or faulted during DMA (bank %d of %d words; before %+v, after %+v)", block.Bank, block.Words, s, after)
		}
	}
	if e = ctx.Err(); e != nil {
		return block, e
	}
	if !direct {
		for i, v := range halves {
			binary.LittleEndian.PutUint16(bytes[2*i:], v)
		}
	}
	if reserved {
		reserver.Commit(len(bytes))
	} else {
		n, e := dst.Write(bytes)
		if e != nil {
			return block, e
		}
		if n != len(bytes) {
			return block, io.ErrShortWrite
		}
	}
	e = c.write(20, uint16(block.Bank)|uint16((s.Tokens>>block.Bank)&1)<<1)
	return block, e
}

// StreamPace sets a stream's word rate to any whole number of raw words (4
// ns, two samples each) instead of the decimation's power of two, so a
// stream runs as close to what the host bus drains as its plan wants
// (ADR-STREAM-IMAGE). Words is the peak stream's bucket (even: whole pairs,
// 2 to 2^19) or the digital stream's period (2 to 32768, sixteen periods a
// word); 0 keeps 2^DecimationLog2 samples a word. RampClocks paces the
// counter source's ramp at a word every that many 250 MHz clocks (2 or
// more; 0 at the decimator's words), for measuring the bus's ceiling with
// every word checked.
type StreamPace struct {
	Words      uint32
	RampClocks uint16
}

// streamPaceCapability is register 65's word in an image that takes the
// pace (registers 29 and 30, 46 the ramp's), "RA".
const streamPaceCapability = 0x5241

// setStreamPace writes p and, for a pace other than the decimation's, reads
// it back with the image's capability: an image without the pace would
// stream at the power of two and every sample's time would be wrong. The
// caller holds c.mu.
// TRLC-LINKS: REQ-SDS-035
func (c *Capture) setStreamPace(p StreamPace) error {
	if p.Words == 1 || p.Words > 1<<19 {
		return fmt.Errorf("sramcapture: stream pace %d raw words, need 2..%d", p.Words, 1<<19)
	}
	if p.RampClocks == 1 {
		return fmt.Errorf("sramcapture: ramp pace %d clocks, need 2 or more", p.RampClocks)
	}
	if err := c.writeMany([2]uint16{29, uint16(p.Words)}, [2]uint16{30, uint16(p.Words >> 16)}, [2]uint16{46, p.RampClocks}); err != nil {
		return err
	}
	if p == (StreamPace{}) {
		return nil
	}
	var v [4]uint16
	if err := c.readMany([]uint16{65, 66, 67, 68}, v[:]); err != nil {
		return err
	}
	if v[0] != streamPaceCapability {
		return fmt.Errorf("sramcapture: this FPGA image streams at powers of two only")
	}
	if uint32(v[1])|uint32(v[2]&15)<<16 != p.Words || v[3] != p.RampClocks {
		return fmt.Errorf("sramcapture: stream pace read back %d words, %d clocks", uint32(v[1])|uint32(v[2]&15)<<16, v[3])
	}
	return nil
}

// StartStream enables the stream banks and arms cfg: every word the record
// writes is also published to the host through DrainStream, starting at
// ordinal 0. continuous never triggers, so the stream runs until StopStream;
// otherwise the record's own trigger and post-trigger end it. peak streams
// each bucket's extremes (the envelope recall's raw word format) in place of
// the record's averaged words (the digital stream image: its digital words),
// one word per 2^DecimationLog2 samples or at pace. The fabric streams
// decimated records only (DecimationLog2 8 or more); a pace's word rate is
// the caller's to keep within what the host drain sustains.
// TRLC-LINKS: REQ-SDS-035
func (c *Capture) StartStream(ctx context.Context, cfg Config, continuous, peak bool, pace StreamPace) error {
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
		// Latched by the fabric while idle, as the mode.
		err = c.setStreamPace(pace)
	}
	if err == nil {
		mode := uint16(1)
		if continuous {
			mode |= 2
		}
		if peak {
			mode |= 4 // bucket extremes of the raw words, {max2,max1,min2,min1}
		}
		// Latched by the fabric while idle, so before the arm.
		err = c.write(19, mode)
		c.ringStream = false
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

// StopStream halts the acquisition and disables the stream banks; blocks not
// yet drained are discarded.
// TRLC-LINKS: REQ-SDS-035
func (c *Capture) StopStream(ctx context.Context) error {
	c.mu.Lock()
	ring := c.ringStream
	c.mu.Unlock()
	err := c.Halt(ctx)
	if ring && err == nil {
		// The first stop froze a running ring; this one ends the stream.
		err = c.Halt(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ringStream = false
	if werr := c.write(19, 0); err == nil {
		err = werr
	}
	return err
}
