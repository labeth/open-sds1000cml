// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
)

// linFrameRecord is a 20 ms record of LIN frames at 19200 bit/s on C1 (C2
// flat): break, sync, protected identifier, two data bytes and the enhanced
// checksum, every 4 ms.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-223, REQ-SDS-238
func linFrameRecord() *engine.Frame {
	const n, sampleS, baud = 20000, 1e-6, 19200.0
	c1, c2 := make([]uint8, n), make([]uint8, n)
	for i := range c1 {
		c1[i], c2[i] = 210, 128
	}
	spb := 1 / baud / sampleS
	t := 300.0
	put := func(level uint8, bits float64) {
		for i := max(int(t), 0); i < min(int(t+bits*spb), n); i++ {
			c1[i] = level
		}
		t += bits * spb
	}
	char := func(v int) {
		put(40, 1)
		for b := 0; b < 8; b++ {
			put(map[bool]uint8{true: 210, false: 40}[v>>b&1 == 1], 1)
		}
		put(210, 1)
	}
	id := 0x12
	bit := func(k int) int { return id >> k & 1 }
	pid := id | (bit(0)^bit(1)^bit(2)^bit(4))<<6 | (1-(bit(1)^bit(3)^bit(4)^bit(5)))<<7
	data := []int{0xA5, 0x5A}
	sum := pid
	for _, d := range data {
		if sum += d; sum > 0xff {
			sum -= 0xff
		}
	}
	for t < n-4000 {
		start := t
		put(40, 13)
		put(210, 1)
		char(0x55)
		char(pid)
		put(210, 2)
		for _, d := range data {
			char(d)
		}
		char(0xff - sum)
		t = start + 4000
	}
	return &engine.Frame{C1: c1, C2: c2, Valid: n, WinCols: n, EdgeX: n / 2, Ptp: 170,
		TdivS: n * sampleS / 10, SampleS: sampleS, Trigd: true, FullRate: true}
}

// BUS entry finds a LIN bus and arms its frame start (the hardware's break
// and sync, then the identifier) with the protocol, line and rate filled in.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-223, REQ-SDS-238
func TestBusEntryFramesALINBus(t *testing.T) {
	r := newViewRig(t)
	r.show(linFrameRecord())
	press(t, r.c, "f2")
	r.show(linFrameRecord())
	s := r.c.snap()
	if s.decProto != framecalc.ProtoLIN || s.decBaud != 19200 || s.decChA != 0 {
		t.Fatalf("detected protocol %d at %d on C%d, want LIN 19200 on C1 (%q)", s.decProto, s.decBaud, s.decChA+1, s.ui.busNote)
	}
	p := r.eng.SerialConfig()
	if r.eng.stats.SerialMode == 0 || p.Proto != 11 || p.Baud != 19200 || strings.Join(tokensOf(p), " ") != "SOF ??" ||
		!strings.Contains(s.ui.busNote, "TRIG: LIN frame start") {
		t.Fatalf("armed %d on %+v: %q", r.eng.stats.SerialMode, p, s.ui.busNote)
	}
	if !strings.HasPrefix(s.ui.busNote, "LIN 19200 on C1") {
		t.Fatalf("notice %q", s.ui.busNote)
	}
}
