package decodestream

import (
	"testing"

	"open-sds/app/internal/iface"
)

// fakeFab is a faithful model of the in-fabric wide-frame drain port: a FIFO of
// entries, STATUS reporting occupancy + sticky overflow, and DrainDecodeWords that
// pops one entry per 3 words in {w0,w1,w2} layout — exactly what the acq.v patch
// presents (verified byte-exact by fpga sim tb_decstream.v). A STATUS read snapshots
// the count the fabric would return (the wide phase re-anchor is implicit here).
type fakeFab struct {
	q        []Entry
	overflow bool
	wide     bool
	spbHi    uint8
	drains   int // count of DrainDecodeWords calls
	statuses int // count of STATUS reads
}

func (f *fakeFab) Read(plane iface.Plane, sel uint16) (uint16, error) {
	if plane != iface.CS1 || sel != selDecSTATUS {
		return 0, nil
	}
	f.statuses++
	st := uint16(len(f.q)) & fillMask
	if len(f.q) > 0 {
		st |= 0x2000 // busy
	}
	if f.overflow {
		st |= statusOverflow
	}
	return st, nil
}

func (f *fakeFab) WriteSpare(sel, val uint16) error {
	if sel == selDecSPBHi {
		f.wide = val&wideEnableBit != 0
		f.spbHi = uint8(val)
	}
	return nil
}

func (f *fakeFab) DrainDecodeWords(dst []uint16, n int) {
	f.drains++
	if !f.wide {
		panic("DrainDecodeWords called while not in wide-frame mode")
	}
	if n%wordsPerEntry != 0 {
		panic("wide-frame drain must request a multiple of 3 words")
	}
	k := n / wordsPerEntry
	for i := 0; i < k; i++ {
		if len(f.q) == 0 { // over-read would replay a stale head; the streamer must never do this
			panic("over-read: drained more entries than STATUS reported")
		}
		e := f.q[0]
		f.q = f.q[1:]
		dst[i*wordsPerEntry+0] = uint16(e.Flags)<<8 | uint16(e.Data)
		dst[i*wordsPerEntry+1] = uint16(e.Idx & 0xFFFF)
		dst[i*wordsPerEntry+2] = uint16((e.Idx >> 16) & 0xFF)
	}
}

func TestEnableDisableWide(t *testing.T) {
	f := &fakeFab{}
	s := New(f)
	if err := s.EnableWide(0x00); err != nil {
		t.Fatalf("EnableWide: %v", err)
	}
	if !f.wide {
		t.Fatal("EnableWide did not set SPB_HI[10]")
	}
	// The SPB high byte must survive the enable (wide bit is [8], timing is [7:0]).
	if err := s.EnableWide(0x56); err != nil {
		t.Fatalf("EnableWide(0x56): %v", err)
	}
	if f.spbHi != 0x56 || !f.wide {
		t.Fatalf("spbHi=%#02x wide=%v, want 0x56 + wide", f.spbHi, f.wide)
	}
	if err := s.DisableWide(0x56); err != nil {
		t.Fatalf("DisableWide: %v", err)
	}
	if f.wide {
		t.Fatal("DisableWide left wide set")
	}
	if f.spbHi != 0x56 {
		t.Fatalf("DisableWide clobbered spbHi=%#02x, want 0x56", f.spbHi)
	}
}

// TestDrainRampExactness is the host-side gap-count proof: a strictly +1 idx ramp
// must reconstruct with no skip (drop) and no repeat (dup), bytes/flags exact.
func TestDrainRampExactness(t *testing.T) {
	const K = 20
	f := &fakeFab{wide: true}
	for i := 0; i < K; i++ {
		f.q = append(f.q, Entry{Flags: uint8(0x80 | i), Idx: uint32(1000 + i), Data: uint8(i)})
	}
	s := New(f)

	entries, overflow, err := s.Drain(0)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if overflow {
		t.Fatal("unexpected overflow")
	}
	if len(entries) != K {
		t.Fatalf("drained %d, want %d", len(entries), K)
	}
	for i, e := range entries {
		if e.Data != uint8(i) {
			t.Errorf("entry[%d].Data=%#02x want %#02x", i, e.Data, i)
		}
		if e.Flags != uint8(0x80|i) {
			t.Errorf("entry[%d].Flags=%#02x want %#02x", i, e.Flags, 0x80|i)
		}
		if e.Idx != uint32(1000+i) { // any gap/dup shows here
			t.Errorf("entry[%d].Idx=%d want %d (gap/dup!)", i, e.Idx, 1000+i)
		}
	}
	// exactly one STATUS + one drain; FIFO fully emptied.
	if f.statuses != 1 || f.drains != 1 {
		t.Errorf("statuses=%d drains=%d, want 1/1", f.statuses, f.drains)
	}
	if len(f.q) != 0 {
		t.Errorf("FIFO not emptied: %d left", len(f.q))
	}
}

// TestDrainCapLeavesRemainder proves the cap pops EXACTLY maxEntries and leaves the
// rest queued (no early pop, no loss) — the "entries pushed during a drain stay
// queued" property, exercised deterministically via the cap.
func TestDrainCapLeavesRemainder(t *testing.T) {
	f := &fakeFab{wide: true}
	for i := 0; i < 10; i++ {
		f.q = append(f.q, Entry{Idx: uint32(i), Data: uint8(i)})
	}
	s := New(f)

	first, _, err := s.Drain(4)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(first) != 4 {
		t.Fatalf("first drain=%d, want 4", len(first))
	}
	for i, e := range first {
		if e.Data != uint8(i) || e.Idx != uint32(i) {
			t.Errorf("first[%d]=%+v, want data/idx=%d", i, e, i)
		}
	}
	// The remaining 6 must be intact and in order on the next drain.
	rest, _, err := s.Drain(0)
	if err != nil {
		t.Fatalf("Drain rest: %v", err)
	}
	if len(rest) != 6 {
		t.Fatalf("rest=%d, want 6", len(rest))
	}
	for i, e := range rest {
		if e.Data != uint8(i+4) || e.Idx != uint32(i+4) {
			t.Errorf("rest[%d]=%+v, want data/idx=%d", i, e, i+4)
		}
	}
}

func TestDrainEmpty(t *testing.T) {
	f := &fakeFab{wide: true}
	s := New(f)
	entries, overflow, err := s.Drain(0)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(entries) != 0 || overflow {
		t.Fatalf("empty drain: entries=%d overflow=%v", len(entries), overflow)
	}
	// STATUS was read once; no drain launched on an empty FIFO (never over-reads).
	if f.statuses != 1 || f.drains != 0 {
		t.Errorf("statuses=%d drains=%d, want 1/0", f.statuses, f.drains)
	}
}

func TestDrainSurfacesOverflow(t *testing.T) {
	f := &fakeFab{wide: true, overflow: true}
	f.q = append(f.q, Entry{Idx: 7, Data: 0xAA})
	s := New(f)
	entries, overflow, err := s.Drain(0)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if !overflow {
		t.Error("overflow not surfaced (host must see in-fabric loss)")
	}
	if len(entries) != 1 || entries[0].Data != 0xAA || entries[0].Idx != 7 {
		t.Errorf("entries=%+v, want one {0xAA, idx 7}", entries)
	}
}

func TestWideFramePacking(t *testing.T) {
	// idx that exercises all 24 bits and both idx words + high flags.
	f := &fakeFab{wide: true}
	f.q = append(f.q, Entry{Flags: 0xC3, Idx: 0xABCDEF, Data: 0x5A})
	s := New(f)
	entries, _, err := s.Drain(0)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d, want 1", len(entries))
	}
	e := entries[0]
	if e.Flags != 0xC3 || e.Data != 0x5A || e.Idx != 0xABCDEF {
		t.Errorf("entry=%+v, want {0xC3, 0xABCDEF, 0x5A}", e)
	}
}
