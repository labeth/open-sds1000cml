// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "testing"

// TestAutodetectMIDIBurstIsNoPWM: a full-rate record of 2 ms holding one MIDI
// message (a Control Change, B0 07 64: three bytes at 31250 Bd) between idle
// stretches. Its bits give six steady periods, which read as PWM once the
// idle was cut from the record (AUTO's search named PWM at 500 ns/div on the
// bench's MIDI source, one search in ten). A PWM output runs on: AUTO names
// MIDI.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func TestAutodetectMIDIBurstIsNoPWM(t *testing.T) {
	const sr, bit = 5e8, 32e-6
	w := newTimeline(sr)
	w.idle(430e-6)
	for _, b := range []int{0xB0, 0x07, 0x64} {
		w.add(0, bit)
		for k := 0; k < 8; k++ {
			w.add(byte(b>>k&1), bit)
		}
		w.add(1, bit)
	}
	w.idle(float64(1<<20)/sr - w.t)
	c := w.codes()
	r, _ := AutodetectFast(c, idleLike(c), 1/sr, "hex")
	if r.Proto != "midi" {
		t.Fatalf("named %q (%s) %q, want MIDI", r.Proto, r.Src, r.Text)
	}
}
