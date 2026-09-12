package sramcapture

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"
)

type fakeBus struct {
	id, mapID, revision, flags            uint16
	length, start, origin, position, base uint32
	staged                                map[uint16]uint16
	buffer                                [512]uint32
	index                                 uint16
	received                              uint32
	writes, readWords, failRead           int
	commands                              []uint16
	corruptBurstHead                      bool
	observed                              chan struct{}
}

func frozenBus() *fakeBus {
	f := &fakeBus{id: FabricID, mapID: QualifiedMapID, revision: 5, flags: 4 | 16 | 32 | 64, length: Words, start: Words - 17, origin: 71, base: 0xfffff123, staged: make(map[uint16]uint16)}
	f.position = (f.origin + f.start) & addressMask
	return f
}
func (f *fakeBus) Read(plane uint8, s uint16) (uint16, error) {
	if plane != 1 {
		return 0, errors.New("backend accessed a plane other than CS1")
	}
	var value uint32
	switch s {
	case 0:
		return f.id, nil
	case 1:
		if f.observed != nil {
			select {
			case f.observed <- struct{}{}:
			default:
			}
		}
		return f.flags, nil
	case 2, 3:
		value = f.length
	case 4, 5:
		value = f.start
	case 6, 7:
		value = f.length / 2
	case 8, 9:
		value = f.position
	case 10, 11:
		value = f.origin
	case 12:
		return uint16(f.received), nil
	case 13:
		return f.revision, nil
	case 14:
		return f.mapID, nil
	case 15:
		return f.staged[15], nil
	case 19:
		return f.staged[19], nil
	case 17:
		f.readWords++
		if f.failRead != 0 && f.readWords == f.failRead {
			return 0, io.ErrUnexpectedEOF
		}
		return uint16(f.buffer[f.index]), nil
	case 18:
		return uint16(f.buffer[f.index] >> 16), nil
	}
	if s&1 != 0 {
		return uint16(value >> 16), nil
	}
	return uint16(value), nil
}
func (f *fakeBus) RawWrite(s, v uint16) error {
	f.writes++
	f.staged[s] = v
	if s == 16 {
		f.index = v
		return nil
	}
	if s != 1 {
		return nil
	}
	f.commands = append(f.commands, v)
	f.flags ^= 512
	n := uint32(f.staged[8]) | uint32(f.staged[9])<<16
	switch v {
	case 1:
		f.length = uint32(f.staged[2]) | uint32(f.staged[3])<<16
		f.length += uint32(f.staged[4]) | uint32(f.staged[5])<<16
		if f.staged[6]&16 != 0 {
			f.flags = (f.flags & 512) | 64 | 8
		} else {
			f.flags = (f.flags & 512) | 64 | 32 | 16 | 4
		}
	case 4, 5:
		f.flags = (f.flags & 512) | 64 | 32 | 16 | 4
	case 3:
		f.position = (f.position + n) & addressMask
		f.flags &^= 1024
	case 2, 7:
		first := f.position
		if v == 7 {
			if f.flags&1024 == 0 {
				return errors.New("invalid continuation")
			}
			first = (first - 1) & addressMask
		}
		if n > 512 {
			return errors.New("buffer overflow")
		}
		for i := uint32(0); i < n; i++ {
			relative := (first + i - f.origin - f.start) & addressMask
			f.buffer[i] = f.base + relative
		}
		if f.corruptBurstHead && n > 1 {
			f.buffer[0] = f.buffer[1]
		}
		f.received = n
		f.position = (f.position + n) & addressMask
		if v == 2 {
			f.position = (f.position + 1) & addressMask
		}
		f.flags |= 1024
	}
	return nil
}
func client(t *testing.T, f *fakeBus) *Capture {
	t.Helper()
	c, e := New(f)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func checkWords(t *testing.T, data []byte, base uint32, n uint32) {
	t.Helper()
	if len(data) != int(n)*4 {
		t.Fatalf("bytes %d want %d", len(data), n*4)
	}
	for i := uint32(0); i < n; i++ {
		if got := binary.LittleEndian.Uint32(data[4*i:]); got != base+i {
			t.Fatalf("word %d: %08x want %08x", i, got, base+i)
		}
	}
}
func TestFullCapacityWrappedAndRepeatedRecall(t *testing.T) {
	f := frozenBus()
	c := client(t, f)
	ctx := context.Background()
	for attempt := 0; attempt < 2; attempt++ {
		var dst bytes.Buffer
		n, e := c.Recall(ctx, 0, Words, &dst)
		if e != nil || n != int64(Bytes) {
			t.Fatalf("recall: n=%d err=%v", n, e)
		}
		checkWords(t, dst.Bytes(), f.base, Words)
	}
	continues := 0
	for _, op := range f.commands {
		if op == 1 {
			t.Fatal("recall rearmed")
		}
		if op == 7 {
			continues++
		}
	}
	if continues != 2047 {
		t.Fatalf("adjacent windows did not continue: %d", continues)
	}
	var dst bytes.Buffer
	_, e := c.Recall(ctx, Words-513, 513, &dst)
	if e != nil {
		t.Fatal(e)
	}
	checkWords(t, dst.Bytes(), f.base+Words-513, 513)
}
func TestLegacyReadFallback(t *testing.T) {
	f := frozenBus()
	f.revision = 4
	c := client(t, f)
	// Revision 4 does not report prefetch availability. Clear it on every read
	// via an adapter, while retaining the same physical flush-position model.
	c.bus = noPrefetch{f}
	var dst bytes.Buffer
	_, e := c.Recall(context.Background(), 17, 1025, &dst)
	if e != nil {
		t.Fatal(e)
	}
	checkWords(t, dst.Bytes(), f.base+17, 1025)
	for _, op := range f.commands {
		if op == 7 {
			t.Fatal("used continuation on legacy fabric")
		}
	}
}

type noPrefetch struct{ *fakeBus }

func (f noPrefetch) Read(p uint8, s uint16) (uint16, error) {
	v, e := f.fakeBus.Read(p, s)
	if s == 1 {
		v &^= 1024
	}
	return v, e
}
func TestBoundsAndIdentityRejectBeforeWrites(t *testing.T) {
	for _, change := range []func(*fakeBus){func(f *fakeBus) { f.id = 0xa2f1 }, func(f *fakeBus) { f.mapID = 0x2611 }, func(f *fakeBus) { f.revision = 3 }} {
		f := frozenBus()
		change(f)
		if _, e := New(f); e == nil {
			t.Fatal("accepted incompatible fabric")
		}
		if f.writes != 0 {
			t.Fatal("identity check wrote registers")
		}
	}
	f := frozenBus()
	c := client(t, f)
	for _, window := range [][2]uint32{{Words, 1}, {1, ^uint32(0)}, {Words + 1, 0}} {
		if _, e := c.Recall(context.Background(), window[0], window[1], io.Discard); e == nil {
			t.Fatal("accepted invalid window")
		}
	}
	if e := c.Arm(context.Background(), Config{PreWords: Words, PostWords: 1}); e == nil {
		t.Fatal("accepted oversized record")
	}
	if f.writes != 0 {
		t.Fatal("invalid request altered capture")
	}
	n, e := c.Recall(context.Background(), Words, 0, io.Discard)
	if e != nil || n != 0 || f.writes != 0 {
		t.Fatal("empty boundary window")
	}
	f.flags &^= 16
	if _, e = c.Recall(context.Background(), 0, 1, io.Discard); e == nil {
		t.Fatal("read live record")
	}
}
func TestErrorsLeaveRecordRecallable(t *testing.T) {
	f := frozenBus()
	c := client(t, f)
	f.failRead = 513
	var dst bytes.Buffer
	n, e := c.Recall(context.Background(), 0, 1024, &dst)
	if !errors.Is(e, io.ErrUnexpectedEOF) || n != 2048 {
		t.Fatalf("partial failure: %d %v", n, e)
	}
	f.failRead = 0
	dst.Reset()
	_, e = c.Recall(context.Background(), 0, 1024, &dst)
	if e != nil {
		t.Fatal(e)
	}
	checkWords(t, dst.Bytes(), f.base, 1024)
	n, e = c.Recall(context.Background(), 0, 1, shortWriter{})
	if !errors.Is(e, io.ErrShortWrite) || n != 3 {
		t.Fatalf("short writer: %d %v", n, e)
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }
func TestWaitAllowsForce(t *testing.T) {
	f := frozenBus()
	c := client(t, f)
	if e := c.Arm(context.Background(), Config{PreWords: Words / 2, PostWords: Words / 2, Normal: true}); e != nil {
		t.Fatal(e)
	}
	if f.staged[3] != 4 || f.staged[5] != 4 {
		t.Fatal("full-depth high count bits truncated")
	}
	f.observed = make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.WaitFrozen(ctx) }()
	<-f.observed
	if e := c.Force(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatalf("wait blocked force: %v", e)
	}
}

func TestInterleaveRateAndFault(t *testing.T) {
	b := frozenBus()
	b.revision = 6
	b.staged[15] = 500
	b.staged[19] = 2
	c, e := New(b)
	if e != nil {
		t.Fatal(e)
	}
	m, e := c.Status()
	if e != nil || !m.Interleaved || m.SampleRateHz != 500000000 || !m.Locked {
		t.Fatalf("metadata %+v %v", m, e)
	}
	if e = c.Arm(context.Background(), Config{Pair: 1, PostWords: 2}); e == nil {
		t.Fatal("accepted single-pair selection")
	}
	b.staged[19] = 3
	var buf bytes.Buffer
	if _, e = c.Recall(context.Background(), 0, 2, &buf); e == nil || buf.Len() != 0 {
		t.Fatal("faulted record exported")
	}
	b.staged[15] = 1000
	if _, e = c.Status(); e == nil {
		t.Fatal("unrecognized rate accepted")
	}
}

func TestInterleaveWarmRecallPreservesFullCapacity(t *testing.T) {
	f := frozenBus()
	f.revision = 7
	f.staged[15] = 500
	f.staged[19] = 2
	f.corruptBurstHead = true
	c := client(t, f)
	for attempt := 0; attempt < 2; attempt++ {
		var dst bytes.Buffer
		n, e := c.Recall(context.Background(), 0, Words, &dst)
		if e != nil || n != int64(Bytes) {
			t.Fatalf("warm recall %d: %d %v", attempt, n, e)
		}
		checkWords(t, dst.Bytes(), f.base, Words)
	}
	var tail bytes.Buffer
	_, e := c.Recall(context.Background(), Words-3, 3, &tail)
	if e != nil {
		t.Fatal(e)
	}
	checkWords(t, tail.Bytes(), f.base+Words-3, 3)
	for _, op := range f.commands {
		if op == 1 || op == 7 {
			t.Fatalf("warm recall used unsafe operation %d", op)
		}
	}
}
