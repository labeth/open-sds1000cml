// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"
	"time"
)

// words packs n stream words whose CH1 code is the sample's ordinal from
// start (low 16 bits) and CH2 its complement.
// TRLC-LINKS: REQ-SDS-265
func soundWords(start uint64, n int) []byte {
	b := make([]byte, 4*n)
	for i := 0; i < n; i++ {
		v := uint16(start + uint64(i))
		b[4*i], b[4*i+1] = byte(v), byte(v>>8)
		b[4*i+2], b[4*i+3] = byte(^v), byte(^v>>8)
	}
	return b
}

// The ring keeps the newest samples in order across its wrap, with the
// scale in force, the gaps and the marks inside what a reader copies.
// TRLC-LINKS: REQ-SDS-265, REQ-SDS-272
func TestSoundRing(t *testing.T) {
	r := newSoundRing(12) // 4096 samples
	now := time.Unix(10, 0)
	sc := SoundScale{VdivV: [2]float64{1, 0.5}}
	at := uint64(0)
	for k := 0; k < 40; k++ {
		n := 37 + k*13
		s := sc
		if k >= 20 {
			s.VdivV[0] = 2 // a V/div change
		}
		r.pushWords(soundWords(at, n), s, k == 36, now)
		if k == 33 {
			r.addMarks([]SoundMark{{At: at + 5, Rising: true}})
		}
		at += uint64(n)
	}
	b := r.latest(4096, nil, nil, 61035)
	if b.N != 4096 || b.First != at-4096 || b.Next != at {
		t.Fatalf("latest: first %d n %d next %d (wrote %d)", b.First, b.N, b.Next, at)
	}
	for i := 0; i < b.N; i++ {
		want := uint16(b.First + uint64(i))
		if b.Q1[i] != want || b.Q2[i] != ^want {
			t.Fatalf("sample %d: %d %d, want %d", i, b.Q1[i], b.Q2[i], want)
		}
	}
	if len(b.Scales) != 1 || b.Scales[0].VdivV[0] != 2 {
		t.Fatalf("scales %+v", b.Scales)
	}
	if len(b.Gaps) != 1 || len(b.Marks) != 1 || !b.Marks[0].Rising {
		t.Fatalf("gaps %v marks %+v", b.Gaps, b.Marks)
	}
	// A read from before the oldest starts at the oldest; from the end gives
	// nothing.
	if c := r.read(0, 10, nil, nil, 1); c.First != at-4096 || c.N != 10 {
		t.Fatalf("old read %d %d", c.First, c.N)
	}
	if c := r.read(at+5, 10, nil, nil, 1); c.N != 0 || c.First != at {
		t.Fatalf("future read %d %d", c.First, c.N)
	}
	// wait wakes on new samples and times out without.
	done := make(chan uint64)
	go func() { done <- r.wait(at, 2*time.Second) }()
	time.Sleep(20 * time.Millisecond)
	r.pushWords(soundWords(at, 3), sc, false, now)
	if got := <-done; got != at+3 {
		t.Fatalf("woke at %d", got)
	}
	start := time.Now()
	if got := r.wait(at+3, 30*time.Millisecond); got != at+3 || time.Since(start) < 25*time.Millisecond {
		t.Fatal("wait did not time out")
	}
}

// Mark words are 32-bit counts: they extend to the stream's ordinal near
// the words drained, before and after a wrap.
// TRLC-LINKS: REQ-SDS-272
func TestUnwrapWord(t *testing.T) {
	cases := []struct {
		w    uint32
		ref  uint64
		want uint64
	}{{100, 90, 100}, {100, 5000, 100}, {0xfffffff0, 1<<32 + 10, 0xfffffff0}, {5, 1<<32 - 3, 1<<32 + 5}, {7, 3<<32 + 2, 3<<32 + 7}}
	for _, c := range cases {
		if got := unwrapWord(c.w, c.ref); got != c.want {
			t.Errorf("unwrap(%#x, %#x) = %#x, want %#x", c.w, c.ref, got, c.want)
		}
	}
}

// A lease holds the stream for its time; the info starts off.
// TRLC-LINKS: REQ-SDS-265
func TestSoundLease(t *testing.T) {
	fb := newFakeBus()
	e, clk := newTestEngine(t, fb)
	if e.soundWanted() {
		t.Fatal("wanted before a lease")
	}
	e.LeaseSound(2 * time.Second)
	if !e.soundWanted() {
		t.Fatal("lease not held")
	}
	clk.clock().Sleep(3 * time.Second)
	if e.soundWanted() {
		t.Fatal("lease outlived its time")
	}
	e.LeaseSound(time.Minute)
	clk.clock().Sleep(11 * time.Second)
	if e.soundWanted() {
		t.Fatal("a lease longer than the bound held")
	}
	if in := e.SoundInfo(); in.State != "off" || in.RateHz < 61035 || in.RateHz > 61036 {
		t.Fatalf("info %+v", in)
	}
	e.SetSoundMarks(SoundMarkConfig{Ch: 3, Level: 130, Rising: true})
	if in := e.SoundInfo(); in.MarkCfg.Ch != 1 || in.MarkCfg.Level != 130 {
		t.Fatalf("mark config %+v", in.MarkCfg)
	}
}
