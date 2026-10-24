// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"testing"
	"time"

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

// discernSig is a capture's codes: a noisy square wave.
// TRLC-LINKS: REQ-SDS-011
func discernSig(n int) []uint8 {
	sig := make([]uint8, n)
	for i := range sig {
		sig[i] = uint8(128 + 70*((i/301)%2*2-1) + (i*7919)%5 - 2)
	}
	return sig
}

// TRLC-LINKS: REQ-SDS-011
func BenchmarkNoiseFloor(b *testing.B) {
	sig := discernSig(20000)
	for b.Loop() {
		noiseFloor(sig)
	}
}

// TRLC-LINKS: REQ-SDS-009, REQ-SDS-011
func BenchmarkPtp(b *testing.B) {
	sig := discernSig(20000)
	for b.Loop() {
		ptp(sig)
	}
}

// TRLC-LINKS: REQ-SDS-011
func BenchmarkNearerCrossHint(b *testing.B) {
	sig := discernSig(20000)
	for b.Loop() {
		nearerCrossHint(sig, 128, 9000)
	}
}

// TRLC-LINKS: REQ-SDS-001, REQ-SDS-011
func BenchmarkOneFrameSmall(b *testing.B) {
	fb := newFakeBus()
	fb.wave = func(i int) (uint8, uint8) { // a small signal: its presence is judged against the noise
		v := uint8(128 + 10*((i/128)%2*2-1) + i%3)
		return v, v
	}
	e, _ := newTestEngine(&testing.T{}, fb)
	e.bringUp()
	for b.Loop() {
		e.oneFrame(false)
	}
}

// TRLC-LINKS: REQ-SDS-012
func BenchmarkEresQ8(b *testing.B) {
	src := make([]uint16, 20000)
	for i := range src {
		src[i] = uint16(32768 + (i*7919)%3000)
	}
	work, scratch := make([]uint16, len(src)), make([]uint16, len(src))
	for b.Loop() {
		copy(work, src)
		eresQ8(work, scratch, 15)
	}
}

// TRLC-LINKS: REQ-SDS-012
func BenchmarkSramAverage(b *testing.B) {
	const n = 20000
	f := &Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, EdgeX: 9000.25, WinCols: n}
	var a sramAverage
	for b.Loop() {
		f.Valid, f.EdgeX, f.WinCols = n, 9000.25, n
		f.Q1, f.Q2 = qBuffer(f.Q1, n), qBuffer(f.Q2, n)
		for i := range f.Q1[:n] {
			f.Q1[i], f.Q2[i] = uint16(32768+i), uint16(30000+i)
		}
		f.C1, f.C2 = f.C1[:n], f.C2[:n]
		a.push(f, 16)
	}
}

// TRLC-LINKS: REQ-SDS-010
func BenchmarkRollPush(b *testing.B) {
	var r rollView
	r.reset(rollPlan{screen: 2000, group: 1}, 0.1, [2]chScale{{vdiv: 0.5, off: 0.1}, {vdiv: 2}})
	q1, q2 := make([]uint16, 200), make([]uint16, 200)
	for i := range q1 {
		q1[i], q2[i] = uint16(32768+i*37), uint16(30000+i*11)
	}
	for b.Loop() {
		r.push(0, q1, q2, nil)
	}
}

// TRLC-LINKS: REQ-SDS-012
func BenchmarkEnvReduce(b *testing.B) {
	e, _ := newTestEngine(&testing.T{}, newFakeBus())
	f := &Frame{C1: make([]uint8, 3000), C2: make([]uint8, 3000), EnvMin: make([]uint8, envDisplayCols), EnvMax: make([]uint8, envDisplayCols),
		EnvMin2: make([]uint8, envDisplayCols), EnvMax2: make([]uint8, envDisplayCols)}
	for k := 0; k < envRingN; k++ {
		for i := range f.C1 {
			f.C1[i], f.C2[i] = uint8(i*7+k), uint8(i*3)
		}
		e.envPush(f, 0, 2900+k)
	}
	for b.Loop() {
		e.envReduce(f)
	}
}

// The stepped column is i·cols/n for every sample and length.
// TRLC-LINKS: REQ-SDS-012
func TestEnvColumnStep(t *testing.T) {
	for n := 1; n < 5000; n += 7 {
		c, num, next := 0, 0, n
		for i := 0; i < n; i++ {
			for num >= next {
				c++
				next += n
			}
			num += envDisplayCols
			if c != i*envDisplayCols/n {
				t.Fatalf("n %d i %d: column %d, want %d", n, i, c, i*envDisplayCols/n)
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-009
func BenchmarkNotePublish(b *testing.B) {
	e, _ := newTestEngine(&testing.T{}, newFakeBus())
	t := e.clk.Now()
	for b.Loop() {
		e.notePublish(t)
	}
}

// TRLC-LINKS: REQ-SDS-009
func BenchmarkNotePublishRef(b *testing.B) {
	e, _ := newTestEngine(&testing.T{}, newFakeBus())
	t := e.clk.Now()
	var pub []time.Time // the append-and-reslice window it replaced
	for b.Loop() {
		pub = append(pub, t)
		if len(pub) > 64 {
			pub = pub[len(pub)-64:]
		}
	}
	_ = pub
}

// The FPS window counts the publishes of the last second, the ring full or not.
// TRLC-LINKS: REQ-SDS-009
func TestNotePublishFPS(t *testing.T) {
	e, clk := newTestEngine(t, newFakeBus())
	for i := 0; i < 30; i++ {
		e.notePublish(e.clk.Now())
		clk.t = clk.t.Add(10 * time.Millisecond)
	}
	if fps := e.Snapshot().FPS; fps != 30 {
		t.Fatalf("FPS %v after 30 publishes in 0.3 s, want 30", fps)
	}
	for i := 0; i < 200; i++ {
		e.notePublish(e.clk.Now())
		clk.t = clk.t.Add(10 * time.Millisecond)
	}
	if fps := e.Snapshot().FPS; fps != 64 { // the window holds 64
		t.Fatalf("FPS %v, want 64", fps)
	}
}

// buildMaskRef is the column-by-column dilation BuildMaskFromEnvelope replaced.
// TRLC-LINKS: REQ-SDS-014
func buildMaskRef(lo, hi []uint8, winCols, tolCols, tolCodes int) (outLo, outHi []uint8) {
	nLo, nHi := make([]uint8, winCols), make([]uint8, winCols)
	for j := 0; j < winCols; j++ {
		if lo[j] > hi[j] {
			nLo[j], nHi[j] = 0, 255
		} else {
			nLo[j], nHi[j] = lo[j], hi[j]
		}
	}
	outLo, outHi = make([]uint8, winCols), make([]uint8, winCols)
	for j := 0; j < winCols; j++ {
		mn, mx := 255, 0
		for k := max(j-tolCols, 0); k <= min(j+tolCols, winCols-1); k++ {
			mn, mx = min(mn, int(nLo[k])), max(mx, int(nHi[k]))
		}
		outLo[j], outHi[j] = uint8(max(mn-tolCodes, 0)), uint8(min(mx+tolCodes, 255))
	}
	return outLo, outHi
}

// The running dilation builds the mask the column-by-column one did.
// TRLC-LINKS: REQ-SDS-014
func TestBuildMaskDilation(t *testing.T) {
	for _, w := range []int{1, 2, 7, 50, 333} {
		lo, hi := make([]uint8, w), make([]uint8, w)
		for j := range lo {
			lo[j], hi[j] = uint8((j*7919)%200), uint8(50+(j*104729)%200)
		}
		for _, tol := range []int{0, 1, 2, 5, 13, 400} {
			for _, codes := range []int{0, 8, 300} {
				m := BuildMaskFromEnvelope(lo, hi, w, tol, codes, 0)
				rl, rh := buildMaskRef(lo, hi, w, tol, codes)
				if string(m.Lo) != string(rl) || string(m.Hi) != string(rh) {
					t.Fatalf("w %d tol %d codes %d: dilation differs", w, tol, codes)
				}
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-014
func BenchmarkBuildMask(b *testing.B) {
	lo, hi := make([]uint8, 12000), make([]uint8, 12000)
	for j := range lo {
		lo[j], hi[j] = uint8(60+(j/97)%2*100), uint8(90+(j/97)%2*100)
	}
	for b.Loop() {
		BuildMaskFromEnvelope(lo, hi, len(lo), 5, 8, 0)
		BuildMaskFromEnvelope(lo, hi, len(lo), 20, 24, 0)
	}
}

// TRLC-LINKS: REQ-SDS-011
func BenchmarkRefineTriggerEdge(b *testing.B) {
	sig := discernSig(20000)
	q := make([]uint16, len(sig))
	for i, v := range sig {
		q[i] = uint16(v)<<8 | uint16(i*37&0xff)
	}
	for b.Loop() {
		refineTriggerEdge(sig, q, len(sig), 9030.4, 128, true)
		refineTriggerEdge(sig, nil, len(sig), 9630.4, 128, false)
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkEdgeWindows(b *testing.B) {
	f := &Frame{Edge: &EdgeWindow{C1: make([]uint8, 1024), C2: make([]uint8, 1024), SampleS: 2e-9},
		EdgeOpp: &EdgeWindow{C1: make([]uint8, 1024), C2: make([]uint8, 1024), SampleS: 2e-9}}
	for b.Loop() {
		f.EdgeWindows(0)
		f.EdgeWindows(1)
	}
}

// TRLC-LINKS: REQ-SDS-016, REQ-SDS-010
func BenchmarkDebiasEnvelope(b *testing.B) {
	var c InterleaveCalibration
	for k := range c.Offset[0] {
		c.Offset[0][k], c.Offset[1][k] = 0.3*float64(k)-0.5, -0.2*float64(k)
	}
	src := discernSig(20000)
	sig, q := make([]uint8, len(src)), make([]uint16, len(src))
	for b.Loop() {
		copy(sig, src)
		c.debiasEnvelope(0, sig, q, false)
		c.debiasEnvelope(1, sig, q, true)
	}
}
