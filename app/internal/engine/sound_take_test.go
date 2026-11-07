// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"testing"
	"time"
)

// takeWords packs n stream words: CH1 a square of period per samples between
// codes 100 and 160 starting low at ordinal start, CH2 mid-scale.
// TRLC-LINKS: REQ-SDS-275
func takeWords(start uint64, n, per int) []byte {
	b := make([]byte, 4*n)
	for i := range n {
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
		s.ring.PushWords(takeWords(s.ring.Next, n, per), SoundScale{VdivV: [2]float64{1, 1}}, false, now)
	}
	push(200)
	e.soundTrigStep(s) // arms: marks from 200, a trigger from 610 (the pre-trigger time since the start)
	if !s.armed || s.armAt != 610 {
		t.Fatalf("armed %v at %d", s.armed, s.armAt)
	}
	push(1000)
	// The image read three crossings since the last read and kept the
	// newest (1050); the first after the arm is at 650.
	s.ring.AddMarks([]SoundMark{{At: 500, Ch: 0, Rising: true}, {At: 1050, Ch: 0, Rising: true, N: 3}})
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
	at := s.ring.Next - 50
	s.ring.AddMarks([]SoundMark{{At: at, Ch: 0, Rising: true, N: 1}})
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
	s.ring.PushWords(takeWords(0, 5000, 100), SoundScale{VdivV: [2]float64{1, 1}}, false, time.Unix(1, 0))
	e.soundFreeze(s)
	tk := e.SoundTake()
	if tk == nil || tk.N != 5000 || tk.HasTrig || tk.Why != "stop" || !s.frozen {
		t.Fatalf("take %+v", tk)
	}
	if in := e.SoundInfo(); in.Take != tk.ID || in.Oldest != in.Next {
		t.Fatalf("info %+v", in)
	}
}
