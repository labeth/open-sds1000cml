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

type StreamStatus struct {
	Ready      uint8 `json:"ready"`
	Tokens     uint8 `json:"tokens"`
	LastSingle uint8 `json:"last_single"`
	Fault      bool  `json:"fault"`
	Finished   bool  `json:"finished"`
	Enabled    bool  `json:"enabled"`
}

func decodeStreamStatus(v uint16) StreamStatus {
	return StreamStatus{uint8(v & 3), uint8((v >> 2) & 3), uint8((v >> 4) & 3), v&64 != 0, v&128 != 0, v&256 != 0}
}
func (c *Capture) StreamStatus() (StreamStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rev, e := c.read(13)
	if e != nil {
		return StreamStatus{}, e
	}
	if rev != 11 {
		return StreamStatus{}, fmt.Errorf("sramcapture: stream requires experimental revision 11")
	}
	v, e := c.read(32)
	return decodeStreamStatus(v), e
}

// PrepareStream allocates and touches the host buffers before acquisition starts.
// Call while idle so first-block allocation cannot consume the bank deadline.
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
	c.prepareStreamBuffers()
	return nil
}

func (c *Capture) prepareStreamBuffers() {
	if cap(c.streamHalves) < 4096 {
		c.streamHalves = make([]uint16, 4096)
		c.streamBytes = make([]byte, 8192)
	}
	// Touch each page, including already allocated buffers, before arm.
	for i := 0; i < len(c.streamHalves); i += 1024 {
		c.streamHalves[i] = 0
	}
	for i := 0; i < len(c.streamBytes); i += 2048 {
		c.streamBytes[i] = 0
	}
}

type StreamBlock struct {
	FirstWord uint64 `json:"first_word"`
	Words     uint32 `json:"words"`
	Bank      uint8  `json:"bank"`
}

// DrainStream copies an immutable bank, checks sequence, then releases it.
// It never rearms or changes timing/mode. A failed read retains bank ownership;
// callers must discard any partial sink write before retrying. A release-write
// error is ambiguous and requires abort/restart, not a blind retry. Expensive FIR
// work belongs after release. The inherited bus must have one owner.
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
	if s.Fault {
		return block, fmt.Errorf("sramcapture: stream overrun or packetizer fault")
	}
	if !s.Enabled {
		return block, fmt.Errorf("sramcapture: streaming is disabled")
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
	count, e := c.read(33 + uint16(block.Bank))
	if e != nil {
		return block, e
	}
	if count == 0 || count > 1024 {
		return block, fmt.Errorf("sramcapture: invalid stream packet count %d", count)
	}
	block.Words = uint32(count)*2 - uint32((s.LastSingle>>block.Bank)&1)
	base := uint16(block.Bank) * 2048
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
	if cap(c.streamHalves) < 4096 {
		c.prepareStreamBuffers()
	}
	bytes := c.streamBytes[:block.Words*4]
	halves := c.streamHalves[:block.Words*2]
	bytePop, direct := c.bus.(interface{ PopBytesChecked(uint16, []byte) error })
	if direct {
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
	if uint32(index) != (uint32(base)*2+block.Words*2)%8192 {
		return block, fmt.Errorf("sramcapture: stream burst pointer %d", index)
	}
	raw, e = c.read(32)
	if e != nil {
		return block, e
	}
	after := decodeStreamStatus(raw)
	mask := uint8(1 << block.Bank)
	if after.Fault || !after.Enabled || after.Ready&mask == 0 || (after.Tokens^s.Tokens)&mask != 0 {
		return block, fmt.Errorf("sramcapture: stream changed or faulted during DMA")
	}
	if e = ctx.Err(); e != nil {
		return block, e
	}
	if !direct {
		for i, v := range halves {
			binary.LittleEndian.PutUint16(bytes[2*i:], v)
		}
	}
	n, e := dst.Write(bytes)
	if e != nil {
		return block, e
	}
	if n != len(bytes) {
		return block, io.ErrShortWrite
	}
	e = c.write(20, uint16(block.Bank)|uint16((s.Tokens>>block.Bank)&1)<<1)
	return block, e
}
