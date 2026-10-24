// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"testing"

	"open-sds/app/internal/decode"
)

// The engine's per-capture work on the scope's single ARM core: these time
// the pieces a capture, a serial-trigger check or a view runs through.
// go test ./internal/engine -run - -bench .

// packetSpans is a decode of n packets: a start of frame, then size bytes.
// TRLC-LINKS: REQ-SDS-013
func packetSpans(n, size int) []decode.Span {
	var sp []decode.Span
	x := 0
	for p := 0; p < n; p++ {
		sp = append(sp, decode.Span{I0: x, I1: x, Text: "SOF", Kind: "sof"})
		for b := 0; b < size; b++ {
			sp = append(sp, decode.Span{I0: x + 10*b, I1: x + 10*b + 9, Text: "41", Kind: "data", Val: (p*7 + b) & 0xff})
		}
		x += 10*size + 40
	}
	return sp
}

// TRLC-LINKS: REQ-SDS-013
func BenchmarkMatchPackets(b *testing.B) {
	sp := append(packetSpans(150, 2), packetSpans(50, 8)...)
	want := []int{0x55, 0xAA, 0x01}
	for b.Loop() {
		matchPackets(sp, want)
	}
}

// TRLC-LINKS: REQ-SDS-013
func BenchmarkMatchBytes(b *testing.B) {
	sp := packetSpans(1, 12)
	want := []int{0x03, 0x04}
	for b.Loop() {
		matchBytes(sp, want)
	}
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func BenchmarkPlanHardwareUART(b *testing.B) {
	p := SerialParams{Proto: serUART, Baud: 115200, Bits: 8, Parity: "none", HaveThr: true, Threshold: 128, Bytes: []int{0x55, 0xAA}}
	q := p
	q.Parity, q.Baud = "even", 1000000
	for b.Loop() {
		planHardwareUART(&p, false)
		planHardwareUART(&q, true)
	}
}

// planParams are serial settings each hardware planner accepts.
// TRLC-LINKS: REQ-SDS-013
func planParams() map[string]SerialParams {
	base := SerialParams{HaveThr: true, Threshold: 128, ChA: 0, ChB: 1, Bytes: []int{0x55, 0x0A}, Inverted: false}
	m := map[string]SerialParams{}
	p := base
	p.Proto, p.SPIClockHz, p.MSB = serSPI, 10000000, true
	m["spi"] = p
	p = base
	p.Proto, p.Addr, p.RW = serI2C, 0x50, 2
	m["i2c"] = p
	p = base
	p.Proto, p.TickNs, p.Nibbles, p.Bytes = serSENT, 3000, 8, []int{5, 10}
	m["sent"] = p
	p = base
	p.Proto, p.Baud, p.Bytes = serMIL1553, 1000000, []int{0x1234}
	m["mil1553"] = p
	p = base
	p.Proto, p.Baud = serUSB, 1500000
	m["usbls"] = p
	return m
}

// planByValue calls each planner on a copy of p, as the planners took
// their settings before (the reference).
//
//go:noinline
func planByValue(name string, p SerialParams) bool {
	switch name {
	case "spi":
		return planHardwareSPI(&p, true).Enabled
	case "i2c":
		return planHardwareI2C(&p, true).Enabled
	case "sent":
		return planHardwareSENT(&p).Enabled
	case "mil1553":
		return planHardwareMIL1553(&p).Enabled
	}
	return planHardwareUSBLS(&p).Enabled
}

// planByPointer calls each planner on p itself.
//
//go:noinline
func planByPointer(name string, p *SerialParams) bool {
	switch name {
	case "spi":
		return planHardwareSPI(p, true).Enabled
	case "i2c":
		return planHardwareI2C(p, true).Enabled
	case "sent":
		return planHardwareSENT(p).Enabled
	case "mil1553":
		return planHardwareMIL1553(p).Enabled
	}
	return planHardwareUSBLS(p).Enabled
}

// TRLC-LINKS: REQ-SDS-013
func BenchmarkPlan(b *testing.B) {
	for name, p := range planParams() {
		b.Run(name, func(b *testing.B) {
			if !planByPointer(name, &p) {
				b.Fatal("not planned")
			}
			for b.Loop() {
				planByPointer(name, &p)
			}
		})
		b.Run(name+"Ref", func(b *testing.B) {
			for b.Loop() {
				planByValue(name, p)
			}
		})
	}
}

// TRLC-LINKS: REQ-SDS-001
func BenchmarkOneFrame(b *testing.B) {
	fb := newFakeBus()
	e, _ := newTestEngine(&testing.T{}, fb)
	e.bringUp()
	for b.Loop() {
		e.oneFrame(false)
	}
}

// TRLC-LINKS: REQ-SDS-126
func BenchmarkUniRingStats(b *testing.B) {
	var u uniRing
	u.reset()
	disc := make([]uint8, 4096)
	for k := 0; k < uniDepth; k++ {
		for i := range disc {
			disc[i] = uint8(128 + 60*((i/97)%2*2-1) + (i*k)%7)
		}
		u.push(disc, 4096, 2048)
	}
	for b.Loop() {
		u.stats()
	}
}

// uniRing.stats reads as the column-by-column float sums did, bit for bit.
// TRLC-LINKS: REQ-SDS-126
func TestUniRingStatsExact(t *testing.T) {
	ref := func(u *uniRing) (std, stdRaw, worst float64) {
		colStd := func(ring [][]uint8, col int) float64 {
			var s, s2 float64
			for k := 0; k < u.cnt; k++ {
				v := float64(ring[k][col])
				s += v
				s2 += v * v
			}
			m := s / float64(u.cnt)
			v := s2/float64(u.cnt) - m*m
			if v < 0 {
				v = 0
			}
			return math.Sqrt(v)
		}
		var sumC, sumR float64
		for c := 0; c < uniCols; c++ {
			sc := colStd(u.centred, c)
			sumC += sc
			sumR += colStd(u.raw, c)
			if sc > worst {
				worst = sc
			}
		}
		return sumC / uniCols, sumR / uniCols, worst
	}
	var u uniRing
	u.reset()
	disc := make([]uint8, 3000)
	for k := 0; k < uniDepth+3; k++ {
		for i := range disc {
			disc[i] = uint8(128 + 60*((i/97)%2*2-1) + (i*k*31)%17)
		}
		u.push(disc, 3000, float64(1400+k))
		a1, b1, c1 := u.stats()
		a2, b2, c2 := ref(&u)
		if a1 != a2 || b1 != b2 || c1 != c2 {
			t.Fatalf("after %d pushes: %v %v %v, want %v %v %v", k+1, a1, b1, c1, a2, b2, c2)
		}
	}
}
