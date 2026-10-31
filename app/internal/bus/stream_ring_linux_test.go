package bus

import (
	"encoding/binary"
	"testing"
)

// TRLC-LINKS: REQ-SDS-035
func TestStreamRingReserveCommitPeek(t *testing.T) {
	w := &acquisitionWorker{sOn: true, sShared: true, sring: make([]byte, 64)}
	r := streamRingWriter{w}
	a, b, err := r.Reserve(48)
	if err != nil || len(a) != 48 || len(b) != 0 {
		t.Fatalf("reserve 48: %d %d %v", len(a), len(b), err)
	}
	var reply [16]byte
	peek := func(release uint32) (first uint64, at, count int, err error) {
		n, err := w.streamPeek(release, reply[:])
		if err != nil {
			return 0, 0, 0, err
		}
		if n != 16 {
			t.Fatalf("peek reply %d bytes", n)
		}
		return binary.LittleEndian.Uint64(reply[:]), int(binary.LittleEndian.Uint32(reply[8:])), int(binary.LittleEndian.Uint32(reply[12:])), nil
	}
	// Reserved but not committed: nothing to read.
	if _, _, count, err := peek(0); err != nil || count != 0 {
		t.Fatalf("uncommitted peek: %d %v", count, err)
	}
	r.Commit(48)
	if first, at, count, err := peek(0); err != nil || first != 0 || at != 0 || count != 48 {
		t.Fatalf("peek: %d %d %d %v", first, at, count, err)
	}
	if first, at, count, err := peek(40); err != nil || first != 10 || at != 40 || count != 8 {
		t.Fatalf("peek after release: %d %d %d %v", first, at, count, err)
	}
	// The room wraps: 16 bytes at the end, the rest at the start.
	a, b, err = r.Reserve(40)
	if err != nil || len(a) != 16 || len(b) != 24 || &a[0] != &w.sring[48] || &b[0] != &w.sring[0] {
		t.Fatalf("wrapped reserve: %d %d %v", len(a), len(b), err)
	}
	r.Commit(40)
	if _, _, err := r.Reserve(20); err == nil {
		t.Fatal("reserve past the free room did not fail")
	}
	if _, _, _, err := peek(6); err == nil {
		t.Fatal("a release that is no whole word did not fail")
	}
	if _, _, _, err := peek(52); err == nil {
		t.Fatal("a release past the ready bytes did not fail")
	}
	if first, at, count, err := peek(8); err != nil || first != 12 || at != 48 || count != 40 {
		t.Fatalf("wrapped peek: %d %d %d %v", first, at, count, err)
	}
	w.sShared = false
	if _, _, _, err := peek(0); err != ErrNoSharedRing {
		t.Fatalf("unshared ring: %v", err)
	}
}
