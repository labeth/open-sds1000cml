// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"
	"time"
)

// A frame slot's Q8.8 buffer from a deep stopped record is let go under
// live frames again (it is four times what they need): the slots kept a
// 1 M-sample record's codes for good, and the heap sat at the memory
// limit. A buffer that fits is reused.
// TRLC-LINKS: REQ-SDS-040
func TestQBufferLetsGoOfADeepRecordsBuffer(t *testing.T) {
	deep := make([]uint16, 1<<20)
	if q := qBuffer(deep, 4096); cap(q) >= 1<<20 {
		t.Fatalf("a live frame kept the deep buffer (cap %d)", cap(q))
	}
	if q := qBuffer(deep, 1<<20-5); &q[0] != &deep[0] {
		t.Fatal("a buffer that fits was not reused")
	}
	small := make([]uint16, 8192)
	if q := qBuffer(small, 1000); &q[0] != &small[0] {
		t.Fatal("a small buffer was not reused")
	}
}

// The audio stream's ring lets its samples go (4 MB) once nothing listens,
// and takes them again with the next samples; the ordinals go on and no
// sample from before the release is read back.
// TRLC-LINKS: REQ-SDS-265
func TestSoundRingReleasesItsSamples(t *testing.T) {
	r := newSoundRing(12)
	now := time.Unix(1, 0)
	r.pushWords(soundWords(0, 3000), SoundScale{}, false, now)
	r.release()
	if len(r.q[0]) != 0 {
		t.Fatal("the samples were kept")
	}
	if b := r.read(0, 5000, nil, nil, 1); b.N != 0 || b.First != 3000 {
		t.Fatalf("read after the release: first %d n %d", b.First, b.N)
	}
	r.pushWords(soundWords(3000, 100), SoundScale{}, true, now)
	b := r.read(0, 5000, nil, nil, 1)
	if b.First != 3000 || b.N != 100 || b.Q1[0] != 3000 || b.Next != 3100 {
		t.Fatalf("after the release: first %d n %d q %d next %d", b.First, b.N, b.Q1[0], b.Next)
	}
}
