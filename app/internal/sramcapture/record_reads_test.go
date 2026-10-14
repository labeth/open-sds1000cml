// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// identityBus is a frozen revision 10 record with an identity.
// TRLC-LINKS: REQ-SDS-033
func identityBus() *fakeBus {
	f := frozenBus()
	f.revision = 10
	f.staged[15] = 500
	f.staged[19] = 2
	f.identity, f.recordID, f.epoch = true, 0x12345, 7
	return f
}

// A further read of a record whose status was read checks its flags and
// identity, not its whole status again (each register read a round trip to
// the bus worker), reads in order, and refuses once another record is frozen.
// TRLC-LINKS: REQ-SDS-033
func TestRecallReadsOfChecksTheRecordNotItsStatus(t *testing.T) {
	f := identityBus()
	c := client(t, f)
	m, err := c.Status()
	if err != nil || !m.HasRecordIdentity {
		t.Fatalf("status %+v %v", m, err)
	}
	f.selReads = map[uint16]int{}
	var a, b bytes.Buffer
	err = c.RecallReadsOf(context.Background(), m, RecordRead{Offset: 4000, Count: 100, Dst: &a}, RecordRead{Offset: 9000, Count: 33, Dst: &b})
	if err != nil {
		t.Fatal(err)
	}
	checkWords(t, a.Bytes(), f.base+4000, 100)
	checkWords(t, b.Bytes(), f.base+9000, 33)
	if f.selReads[13] != 0 || f.selReads[2] != 0 {
		t.Fatalf("the status was read again: revision %d, length %d times", f.selReads[13], f.selReads[2])
	}
	if f.selReads[69] == 0 || f.selReads[71] == 0 {
		t.Fatal("the record's identity was not checked")
	}
	f.recordID++
	if err := c.RecallReadsOf(context.Background(), m, RecordRead{Offset: 0, Count: 4, Dst: &a}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("another record read as the old one: %v", err)
	}
	f.recordID--
	f.flags &^= 16
	if err := c.RecallReadsOf(context.Background(), m, RecordRead{Offset: 0, Count: 4, Dst: &a}); err == nil {
		t.Fatal("a record no longer frozen was read")
	}
}

// RecallRecord (the bus worker's side) reads a record named by its identity
// and refuses another.
// TRLC-LINKS: REQ-SDS-033
func TestRecallRecordNamesItsRecord(t *testing.T) {
	f := identityBus()
	c := client(t, f)
	var a bytes.Buffer
	if err := c.RecallRecord(context.Background(), RecordIdentity{Epoch: 7, Record: 0x12345}, []RecordRead{{Offset: 100, Count: 50, Dst: &a}}); err != nil {
		t.Fatal(err)
	}
	checkWords(t, a.Bytes(), f.base+100, 50)
	if err := c.RecallRecord(context.Background(), RecordIdentity{Epoch: 8, Record: 0x12345}, []RecordRead{{Offset: 100, Count: 50, Dst: &a}}); err == nil {
		t.Fatal("a read named another record and got this one")
	}
}

// isolatedBus is a bus a worker owns: it records the whole reads asked of it.
// TRLC-LINKS: REQ-SDS-033
type isolatedBus struct {
	*fakeBus
	calls      int
	epoch, rec uint32
	windows    []uint32
	owned      bool
	size       int
}

// TRLC-LINKS: REQ-SDS-033
func (b *isolatedBus) RecallIsolated(epoch, record uint32, windows []uint32, dst []byte) (bool, error) {
	if !b.owned {
		return false, nil
	}
	b.calls++
	b.epoch, b.rec, b.windows, b.size = epoch, record, append([]uint32(nil), windows...), len(dst)
	for i := range dst {
		dst[i] = byte(i)
	}
	return true, nil
}

// With a bus worker the reads of a frame go in one round trip, each read's
// share of the answer to its writer (a Q8.8 envelope bucket is two words), and
// nothing touches a register here; without one they are read here.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-033
func TestRecallReadsOfOneRoundTripOnAWorker(t *testing.T) {
	f := identityBus()
	b := &isolatedBus{fakeBus: f, owned: true}
	c := client(t, f)
	c.bus = b
	m, err := c.Status()
	if err != nil {
		t.Fatal(err)
	}
	m.FractionBits = 8
	writes, reads := f.writes, len(f.commands)
	var screen, view bytes.Buffer
	err = c.RecallReadsOf(context.Background(), m, RecordRead{Offset: 10, Bucket: 4, Count: 6, Dst: &screen}, RecordRead{Offset: 500, Count: 5, Dst: &view})
	if err != nil {
		t.Fatal(err)
	}
	if b.calls != 1 || b.epoch != 7 || b.rec != 0x12345 || b.size != 6*8+5*4 {
		t.Fatalf("worker asked %d times for record %d/%#x, %d bytes", b.calls, b.epoch, b.rec, b.size)
	}
	if want := []uint32{10, 4, 6, 500, 0, 5}; len(b.windows) != len(want) || b.windows[0] != 10 || b.windows[3] != 500 || b.windows[5] != 5 {
		t.Fatalf("windows %v, want %v", b.windows, want)
	}
	if screen.Len() != 48 || view.Len() != 20 || screen.Bytes()[47] != 47 || view.Bytes()[0] != 48 {
		t.Fatalf("answer split %d + %d bytes", screen.Len(), view.Len())
	}
	if f.writes != writes || len(f.commands) != reads {
		t.Fatal("a register was touched beside the worker's read")
	}
	b.owned = false
	view.Reset()
	if err := c.RecallReadsOf(context.Background(), m, RecordRead{Offset: 500, Count: 5, Dst: &view}); err != nil {
		t.Fatal(err)
	}
	checkWords(t, view.Bytes(), f.base+500, 5)
}
