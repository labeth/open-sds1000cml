package sramcapture

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type streamFake struct {
	*fakeBus
	flags                              uint16
	first                              [2]uint64
	counts                             [2]uint16
	releases                           []uint16
	badPointer, faultOnPop, tokenOnPop bool
}

type streamDMAFake struct{ *streamFake }

func (s *streamDMAFake) PopWordsChecked(port uint16, dst []uint16) error {
	for i := range dst {
		v, e := s.Read(1, port)
		if e != nil {
			return e
		}
		dst[i] = v
	}
	return nil
}
func streamBus() *streamFake {
	f := frozenBus()
	f.revision = 11
	return &streamFake{fakeBus: f, flags: 256 | 3 | 8 | 16, first: [2]uint64{2048, 0}, counts: [2]uint16{2, 1024}}
}
func (s *streamFake) Read(p uint8, r uint16) (uint16, error) {
	if p != 1 {
		return 0, errors.New("wrong plane")
	}
	switch {
	case r == 32:
		return s.flags, nil
	case r == 33 || r == 34:
		return s.counts[r-33], nil
	case r >= 35 && r <= 42:
		return uint16(s.first[(r-35)/4] >> (16 * ((r - 35) % 4))), nil
	case r == 25:
		if s.faultOnPop {
			s.flags |= 64
		}
		if s.tokenOnPop {
			s.flags ^= 12
			s.tokenOnPop = false
		}
	case r == 27 && s.badPointer:
		return 1, nil
	}
	return s.fakeBus.Read(p, r)
}
func (s *streamFake) RawWrite(r, v uint16) error {
	if r == 20 {
		s.releases = append(s.releases, v)
		s.flags &^= 1 << (v & 1)
		return nil
	}
	return s.fakeBus.RawWrite(r, v)
}

type streamWriter func([]byte) (int, error)

func (f streamWriter) Write(b []byte) (int, error) { return f(b) }

func TestStreamDrainOrdersBanksAndKeepsOddTail(t *testing.T) {
	b := streamBus()
	c, e := New(&streamDMAFake{b})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2048; i++ {
		b.buffer[i] = uint32(i + 2048)
		b.buffer[2048+i] = uint32(i)
	}
	var out bytes.Buffer
	writer := streamWriter(func(p []byte) (int, error) {
		if len(b.releases) != out.Len()/8192 {
			t.Fatal("bank released before copy")
		}
		return out.Write(p)
	})
	block, e := c.DrainStream(context.Background(), 0, writer)
	if e != nil || block.Bank != 1 || block.Words != 2048 {
		t.Fatalf("%+v %v", block, e)
	}
	block, e = c.DrainStream(context.Background(), 2048, writer)
	if e != nil || block.Bank != 0 || block.Words != 3 {
		t.Fatalf("%+v %v", block, e)
	}
	checkWords(t, out.Bytes(), 0, 2051)
	if len(b.releases) != 2 || b.releases[0] != 3 || b.releases[1] != 0 {
		t.Fatal(b.releases)
	}
	if _, e = c.DrainStream(context.Background(), 2051, io.Discard); !errors.Is(e, ErrNoStreamBlock) {
		t.Fatal(e)
	}
}
func TestStreamDrainHighSequenceAndFractionBytes(t *testing.T) {
	b := streamBus()
	b.flags = 256 | 1 | 16
	b.first[0] = 1<<48 | 123
	b.counts[0] = 1
	b.buffer[0] = 0x12ff80ab
	c, _ := New(b)
	var out bytes.Buffer
	block, e := c.DrainStream(context.Background(), b.first[0], &out)
	if e != nil || block.FirstWord != b.first[0] || block.Words != 1 || binary.LittleEndian.Uint32(out.Bytes()) != 0x12ff80ab {
		t.Fatalf("%+v %x %v", block, out.Bytes(), e)
	}
}
func TestStreamDrainFailuresRetainOwnership(t *testing.T) {
	for _, name := range []string{"disabled", "fault", "sequence", "count-zero", "count-big", "pointer", "fault-during", "token-during", "short-write", "sink-error", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			b := streamBus()
			b.flags = 256 | 1
			b.first[0] = 0
			b.counts[0] = 2
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var sink io.Writer = io.Discard
			switch name {
			case "disabled":
				b.flags = 1
			case "fault":
				b.flags |= 64
			case "sequence":
				b.first[0] = 5
			case "count-zero":
				b.counts[0] = 0
			case "count-big":
				b.counts[0] = 1025
			case "pointer":
				b.badPointer = true
			case "fault-during":
				b.faultOnPop = true
			case "token-during":
				b.tokenOnPop = true
			case "short-write":
				sink = shortWriter{}
			case "sink-error":
				sink = streamWriter(func([]byte) (int, error) { return 0, io.ErrClosedPipe })
			case "cancelled":
				cancel()
			}
			c, _ := New(b)
			if _, e := c.DrainStream(ctx, 0, sink); e == nil {
				t.Fatal("failure accepted")
			}
			if len(b.releases) != 0 || b.flags&1 == 0 {
				t.Fatal("failed block released")
			}
		})
	}
}
