// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"testing"
	"time"
)

// takeWords packs n stream words: CH1 a square of period per samples between
// codes 100 and 160 starting low at ordinal start, CH2 mid-scale.
// TRLC-LINKS: REQ-SDS-275
func takeWords(start uint64, n, per int) []byte {
	b := make([]byte, 4*n)
	for i := 0; i < n; i++ {
		c := uint16(100)
		if (int(start)+i)%per >= per/2 {
			c = 160
		}
		v := c << 8
		b[4*i], b[4*i+1] = byte(v), byte(v>>8)
		b[4*i+2], b[4*i+3] = 0, 128
	}
	return b
}

// The take times stay inside the ring: pre at most 10 s, post at least a
// tenth, the two at most 16 s.
// TRLC-LINKS: REQ-SDS-275
func TestSoundTakeTimes(t *testing.T) {
	for _, c := range []struct{ pre, post, wp, wq float64 }{
		{1, 4, 1, 4}, {-1, 0, 1, 4}, {math.NaN(), math.Inf(1), 1, 4}, {12, 3, 10, 3}, {8, 15, 1, 15}, {0, 0.01, 0, 4},
	} {
		if p, q := SoundTakeTimes(c.pre, c.post); p != c.wp || q != c.wq {
			t.Errorf("%v %v: %v %v, want %v %v", c.pre, c.post, p, q, c.wp, c.wq)
		}
	}
}

// The comparator's first crossing: armed at or below the level less the
// hysteresis, firing at or above it; falling mirrored; a crossing before
// start disarms as in the fabric.
// TRLC-LINKS: REQ-SDS-275
func TestFirstCrossing(t *testing.T) {
	codes := []int{130, 130, 120, 125, 129, 131, 140, 120, 135, 150, 110}
	q := make([]uint16, len(codes))
	for i, c := range codes {
		q[i] = uint16(c << 8)
	}
	if i, ok := firstCrossingFrom(q, 130, 4, true, 0); !ok || i != 5 {
		t.Errorf("rising: %d %v", i, ok)
	}
	if i, ok := firstCrossingFrom(q, 130, 4, true, 6); !ok || i != 8 {
		t.Errorf("rising from 6: %d %v", i, ok)
	}
	if i, ok := firstCrossingFrom(q, 130, 4, false, 0); !ok || i != 7 {
		t.Errorf("falling: %d %v", i, ok)
	}
	if _, ok := firstCrossingFrom(q[:2], 130, 4, true, 0); ok {
		t.Error("a crossing without arming")
	}
}

// A NORMAL trigger: armed once the pre-trigger time has streamed, the
// FPGA's mark that came with others placed on the first crossing after the
// arm, the take published once the post-trigger time has streamed with the
// trigger at the pre-trigger time, and armed again after it. SINGLE stops.
// TRLC-LINKS: REQ-SDS-275
func TestSoundTrigger(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	s := e.sound()
	e.SetSoundTakeTimes(0.01, 0.1) // 610 and 6104 samples
	s.streaming, s.marksOn, s.trigMode = true, true, true
	cfg := SoundMarkConfig{Ch: 0, Level: 130, Rising: true}
	s.applied = cfg
	now := time.Unix(5, 0)
	const per = 100
	push := func(n int) {
		s.ring.pushWords(takeWords(s.ring.next, n, per), SoundScale{VdivV: [2]float64{1, 1}}, false, now)
	}
	push(200)
	e.soundTrigStep(s) // arms: marks from 200, a trigger from 610 (the pre-trigger time since the start)
	if !s.armed || s.armAt != 610 {
		t.Fatalf("armed %v at %d", s.armed, s.armAt)
	}
	push(1000)
	// The image read three crossings since the last read and kept the
	// newest (1050); the first after the arm is at 650.
	s.ring.addMarks([]SoundMark{{At: 500, Ch: 0, Rising: true}, {At: 1050, Ch: 0, Rising: true, N: 3}})
	e.soundTrigStep(s)
	if !s.pending || s.trigAt != 650 {
		t.Fatalf("pending %v at %d, want 650", s.pending, s.trigAt)
	}
	push(600)
	e.soundTrigStep(s)
	if e.SoundTake() != nil {
		t.Fatal("published before the post-trigger time")
	}
	push(5000)
	e.soundTrigStep(s)
	tk := e.SoundTake()
	if tk == nil || !tk.HasTrig || tk.Trig != 650 || tk.First != 40 || tk.N != 610+6104 || tk.Why != "normal" {
		t.Fatalf("take %+v", tk)
	}
	if s.pending || s.nextArm != 650+6104 {
		t.Fatalf("after: pending %v next arm %d", s.pending, s.nextArm)
	}
	// SINGLE: the next trigger publishes and stops.
	e.singleArmed.Store(true)
	e.running.Store(true)
	e.soundTrigStep(s)
	push(1000)
	at := s.ring.next - 50
	s.ring.addMarks([]SoundMark{{At: at, Ch: 0, Rising: true, N: 1}})
	e.soundTrigStep(s)
	push(6200)
	e.soundTrigStep(s)
	if tk := e.SoundTake(); tk == nil || tk.Trig != at || tk.Why != "single" || e.running.Load() || e.singleArmed.Load() {
		t.Fatalf("single: take %+v running %v", tk, e.running.Load())
	}
}

// STOP freezes the newest seconds as the take (the ring then goes); a
// triggered take is kept.
// TRLC-LINKS: REQ-SDS-275
func TestSoundFreeze(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	s := e.sound()
	s.ring.pushWords(takeWords(0, 5000, 100), SoundScale{VdivV: [2]float64{1, 1}}, false, time.Unix(1, 0))
	e.soundFreeze(s)
	tk := e.SoundTake()
	if tk == nil || tk.N != 5000 || tk.HasTrig || tk.Why != "stop" || !s.frozen {
		t.Fatalf("take %+v", tk)
	}
	if in := e.SoundInfo(); in.Take != tk.ID || in.Oldest != in.Next {
		t.Fatalf("info %+v", in)
	}
}
