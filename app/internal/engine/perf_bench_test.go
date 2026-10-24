// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
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

// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func BenchmarkViewWindow(b *testing.B) {
	e, _ := newTestEngine(&testing.T{}, newFakeBus())
	cal := &InterleaveCalibration{}
	for k := range cal.Offset[0] {
		cal.Offset[0][k], cal.Offset[1][k] = 0.3*float64(k)-0.5, -0.2*float64(k)
	}
	e.interleaveCalibration = cal
	m := sramcapture.Metadata{SampleRateHz: 500e6, SamplesPerWord: 2, Decimation: 1}
	const n = 2048
	src := discernSig(n)
	f := &Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n}
	r := &viewRead{p: viewPlan{bucket: 4, buckets: n / 2, length: 2 * n, n: n}, f: f}
	r.w.n = n
	for b.Loop() {
		copy(f.C1, src)
		copy(f.C2, src)
		e.window(m, r, [2]chScale{{vdiv: 1}, {vdiv: 1}}, envelopeValue(0, 4))
	}
}

//go:noinline
func planPacketByValue(p SerialParams) bool { return planHardwarePacket(&p, 0xffff, 0).Enabled }

//go:noinline
func planPacketByPointer(p *SerialParams) bool { return planHardwarePacket(p, 0xffff, 0).Enabled }

//go:noinline
func planSymbolByValue(p SerialParams) bool { return planHardwareSymbol(&p).Enabled }

//go:noinline
func planSymbolByPointer(p *SerialParams) bool { return planHardwareSymbol(p).Enabled }

// TRLC-LINKS: REQ-SDS-013
func BenchmarkPlanPacketSymbol(b *testing.B) {
	pk := SerialParams{Proto: serCAN, Baud: 500000, HaveThr: true, Threshold: 128, Bytes: []int{0x12, 0x34}}
	sy := SerialParams{Proto: serLIN, Baud: 19200, HaveThr: true, Threshold: 128, Bytes: []int{0x55}}
	b.Run("packet", func(b *testing.B) {
		for b.Loop() {
			planPacketByPointer(&pk)
		}
	})
	b.Run("packetRef", func(b *testing.B) {
		for b.Loop() {
			planPacketByValue(pk)
		}
	})
	b.Run("symbol", func(b *testing.B) {
		for b.Loop() {
			planSymbolByPointer(&sy)
		}
	})
	b.Run("symbolRef", func(b *testing.B) {
		for b.Loop() {
			planSymbolByValue(sy)
		}
	})
}

// TRLC-LINKS: REQ-SDS-013
func BenchmarkPlanSymbolPhys(b *testing.B) {
	ps := []SerialParams{
		{Proto: serPWM, Baud: 1000, HaveThr: true, Threshold: 128},
		{Proto: serDCF77, Baud: 1, HaveThr: true, Threshold: 128},
		{Proto: serIRMP, Baud: 1000, Bits: 10, HaveThr: true, Threshold: 128},
	}
	for b.Loop() {
		for i := range ps {
			planHardwareSymbol(&ps[i])
		}
	}
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func BenchmarkClockRate(b *testing.B) {
	codes := make([]uint8, 20000)
	for i := range codes {
		codes[i] = uint8(60 + 140*((i/5)%2)) // a clock of ten samples
	}
	for b.Loop() {
		clockRate(codes, 2e-9, false)
	}
}

// TRLC-LINKS: REQ-SDS-013
func BenchmarkMatchSequence(b *testing.B) {
	var sp []decode.Span
	for t := 0; t < 100; t++ { // I2C transactions: start, address, direction, two bytes, stop
		x := t * 100
		sp = append(sp, decode.Span{I0: x, Kind: "start"}, decode.Span{I0: x + 1, Kind: "addr", Val: 0x50 + t%3},
			decode.Span{I0: x + 9, Kind: "rw", Text: "W"}, decode.Span{I0: x + 10, Kind: "data", Val: t & 0xff},
			decode.Span{I0: x + 20, Kind: "data", Val: 0x11}, decode.Span{I0: x + 30, Kind: "stop"})
	}
	p := SerialParams{Proto: serI2C, Addr: 0x52, RW: 0, Pattern: []PatternElem{{Kind: patternData, Value: 0x99, Mask: 0xff}, {Kind: patternData, Value: 0x11, Mask: 0xff}}}
	for b.Loop() {
		matchSequence(sp, p)
	}
}

// TRLC-LINKS: REQ-SDS-013
func BenchmarkSerialDecoder(b *testing.B) {
	for b.Loop() {
		serialDecoder(serUART)
		serialDecoder(serI2C)
	}
}

// TRLC-LINKS: REQ-SDS-231
func BenchmarkRecordStuck(b *testing.B) {
	c1, c2 := discernSig(100003), discernSig(100003)
	for b.Loop() {
		recordStuck(c1, c2)
	}
}

// The stepped snapshot index is j·periods/m for every record length.
// TRLC-LINKS: REQ-SDS-231
func TestRecordStuckIndex(t *testing.T) {
	for periods := 1; periods < 3000; periods += 7 {
		m := min(periods, 512)
		at, rem := 0, 0
		step, frac := periods/m, periods%m
		for j := 0; j < m; j++ {
			if at != j*periods/m {
				t.Fatalf("periods %d j %d: %d, want %d", periods, j, at, j*periods/m)
			}
			at += step
			if rem += frac; rem >= m {
				rem -= m
				at++
			}
		}
	}
}

// stuckCores by histogram names the cores the sorting version did.
// TRLC-LINKS: REQ-SDS-231
func TestStuckCoresSame(t *testing.T) {
	ref := func(snaps [][10]uint8) uint16 {
		n := len(snaps)
		if n < adcSnaps/2 {
			return 0
		}
		var stuck uint16
		vals := make([]int, n)
		for ch := 0; ch < 2; ch++ {
			var held [5]bool
			moving := false
			for p := 0; p < 5; p++ {
				for i, s := range snaps {
					vals[i] = int(s[2*p+ch])
				}
				first, same := vals[0], true
				for _, v := range vals {
					same = same && v == first
				}
				held[p] = same
				if same {
					continue
				}
				slices.Sort(vals)
				med, away := vals[n/2], 0
				for _, v := range vals {
					if v-med >= adcMoving || med-v >= adcMoving {
						away++
					}
				}
				if away >= n/8 {
					moving = true
				}
			}
			for p, h := range held {
				if h && moving {
					stuck |= 1 << (2*p + ch)
				}
			}
		}
		return stuck
	}
	for seed := 0; seed < 300; seed++ {
		n := adcSnaps/2 + seed%400
		snaps := make([][10]uint8, n)
		for j := range snaps {
			for k := 0; k < 10; k++ {
				switch (seed + k) % 4 {
				case 0: // held
					snaps[j][k] = uint8(seed)
				case 1: // moving widely
					snaps[j][k] = uint8((j*7919 + k*31 + seed) % 256)
				case 2: // a little noise
					snaps[j][k] = uint8(128 + (j*seed+k)%3)
				default: // mostly one code, sometimes away
					snaps[j][k] = 100
					if (j+seed)%7 == 0 {
						snaps[j][k] = 200
					}
				}
			}
		}
		if got, want := stuckCores(snaps), ref(snaps); got != want {
			t.Fatalf("seed %d: %#x, want %#x", seed, got, want)
		}
	}
}

// TRLC-LINKS: REQ-SDS-011
func BenchmarkQualifyPulse(b *testing.B) {
	sig := discernSig(20000)
	p := defaultTrigParams()
	for b.Loop() {
		qualifyPulse(sig, 2, p, true)
		qualifyPulse(sig, 2, p, false)
	}
}

// TRLC-LINKS: REQ-SDS-040
func BenchmarkSRAMFrameWrite(b *testing.B) {
	const words = 8192
	payload := make([]byte, 4*words)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	for _, fb := range []uint8{0, 8} {
		spw := uint8(2)
		if fb == 8 {
			spw = 1
		}
		n := words * int(spw)
		f := &Frame{C1: make([]uint8, n), C2: make([]uint8, n), Q1: make([]uint16, n), Q2: make([]uint16, n), Valid: n}
		b.Run(map[uint8]string{0: "codes", 8: "q8"}[fb], func(b *testing.B) {
			for b.Loop() {
				w := sramFrameWriter{f: f, m: sramcapture.Metadata{SamplesPerWord: spw, FractionBits: fb}}
				w.Write(payload)
			}
		})
	}
}

// TRLC-LINKS: REQ-SDS-007
func BenchmarkArenaConsume(b *testing.B) {
	a := newArena(16)
	for b.Loop() {
		a.Publish()
		a.Consume()
		a.Consume()
	}
}

// TRLC-LINKS: REQ-SDS-010
func BenchmarkBandInterval(b *testing.B) {
	var bs []Band
	for _, t := range []float64{2e-9, 1e-6, 100e-6, 10e-3, 200e-3} {
		band, _ := PlanTdiv(t)
		bs = append(bs, band)
	}
	for b.Loop() {
		for _, band := range bs {
			band.CaptureIntervalNs()
		}
	}
}

// Every ladder row keeps its intervals and its kind.
// TRLC-LINKS: REQ-SDS-010
func TestBandIntervalsByKind(t *testing.T) {
	for _, b := range bands {
		want := 0.0
		switch b.Kind() {
		case KindEnvelope:
			_, d := b.EnvPlan()
			want = float64(d) * 10
		case KindRoll:
			want = rollDivisor * 10
		default:
			switch b.Class {
			case 0x20:
				want = 2
			case 0x01:
				want = 4
			default:
				want = float64(b.Divisor()) * 10
			}
		}
		if got := b.nominalIntervalNs(); got != want {
			t.Fatalf("%v: %v, want %v", b.TdivS, got, want)
		}
	}
}

// Decim by the tick's reciprocal is the quotient's rounding for every whole
// interval the ladder can name.
// TRLC-LINKS: REQ-SDS-010
func TestDecimReciprocal(t *testing.T) {
	for x := 0.0; x < 1e7; x++ {
		if math.Round(x*(1/baseTickNs)) != math.Round(x/baseTickNs) {
			t.Fatalf("interval %v ns", x)
		}
	}
}

// etsBenchEngine has ETS scratch and sums for nCols columns.
// TRLC-LINKS: REQ-SDS-019
func etsBenchEngine(nCols int) *Engine {
	e := &Engine{etsSum1: make([]int64, nCols), etsSum2: make([]int64, nCols), etsCnt: make([]int32, nCols)}
	e.etsScratch1, e.etsScratch2 = discernSig(etsDrainCols), discernSig(etsDrainCols)
	return e
}

// TRLC-LINKS: REQ-SDS-019
func BenchmarkETSFold(b *testing.B) {
	const nCols = 500
	e := etsBenchEngine(nCols)
	W := float64(nCols) * etsTs * 0.4
	for b.Loop() {
		e.etsFold(600.3, 100, 1100, nCols, W, W/nCols)
	}
}

// TRLC-LINKS: REQ-SDS-019
func BenchmarkETSFoldRef(b *testing.B) {
	const nCols = 500
	e := etsBenchEngine(nCols)
	s1, s2 := make([]float64, nCols), make([]float64, nCols) // the float sums it replaced
	W := float64(nCols) * etsTs * 0.4
	etColTime := W / nCols
	for b.Loop() {
		xref := 600.3
		for k := 100; k <= 1100; k++ {
			col := int(math.Round(((float64(k)-xref)*etsTs + W/2) / etColTime))
			if col < 0 || col >= nCols {
				continue
			}
			s1[col] += float64(e.etsScratch1[k])
			s2[col] += float64(e.etsScratch2[k])
			e.etsCnt[col]++
		}
	}
}

// etsFold's narrowed range sums what the whole range did.
// TRLC-LINKS: REQ-SDS-019
func TestETSFoldRange(t *testing.T) {
	for _, nCols := range []int{50, 500, 2000} {
		for _, frac := range []float64{0.05, 0.4, 1, 3} {
			for _, xref := range []float64{0.2, 600.3, 1500.9} {
				W := float64(nCols) * etsTs * frac
				e := etsBenchEngine(nCols)
				e.etsFold(xref, 0, etsDrainCols-1, nCols, W, W/float64(nCols))
				r := etsBenchEngine(nCols)
				for k := 0; k <= etsDrainCols-1; k++ {
					col := int(math.Round(((float64(k)-xref)*etsTs + W/2) / (W / float64(nCols))))
					if col >= 0 && col < nCols {
						r.etsSum1[col] += int64(r.etsScratch1[k])
						r.etsSum2[col] += int64(r.etsScratch2[k])
						r.etsCnt[col]++
					}
				}
				if fmt.Sprint(e.etsSum1, e.etsSum2, e.etsCnt) != fmt.Sprint(r.etsSum1, r.etsSum2, r.etsCnt) {
					t.Fatalf("nCols %d frac %v xref %v: sums differ", nCols, frac, xref)
				}
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-022
func BenchmarkPanelMatrix(b *testing.B) {
	var k panelKnobs
	f := sramcapture.PanelFrame{Words: [4]uint16{0xffff, 0xfffb, 0xffff, 0xffff}, Knobs: [4]uint16{0x0102, 0x0304, 0x0506, 0x0708}}
	for b.Loop() {
		f.Knobs[0]++
		panelMatrix(f, &k)
	}
}

// fundamentalHzRef is the float-compare reading fundamentalHz replaced.
// TRLC-LINKS: REQ-SDS-066
func fundamentalHzRef(sig []uint8, sampleS float64) float64 {
	n := len(sig)
	if n < 8 {
		return 0
	}
	var sum float64
	for _, v := range sig {
		sum += float64(v)
	}
	mean := sum / float64(n)
	const hyst = 2.0
	firstX, lastX, crossings, armed := -1.0, -1.0, 0, false
	for i := 1; i < n; i++ {
		a, b := float64(sig[i-1]), float64(sig[i])
		if !armed && b < mean-hyst {
			armed = true
		}
		if armed && a < mean && b >= mean {
			x := float64(i-1) + (mean-a)/(b-a)
			if firstX < 0 {
				firstX = x
			}
			lastX = x
			crossings++
			armed = false
		}
	}
	if crossings < 2 || lastX <= firstX {
		return 0
	}
	p := (lastX - firstX) / float64(crossings-1)
	if p <= 1 {
		return 0
	}
	return 1.0 / (p * sampleS)
}

// TRLC-LINKS: REQ-SDS-066
func TestFundamentalHzSame(t *testing.T) {
	for seed := 0; seed < 200; seed++ {
		n := 50 + seed*37
		sig := make([]uint8, n)
		for i := range sig {
			sig[i] = uint8(128 + 60*math.Sin(2*math.Pi*float64(i)*(0.01+float64(seed)/5000)) + float64((i*seed)%5))
		}
		if got, want := fundamentalHz(sig, 1e-6), fundamentalHzRef(sig, 1e-6); got != want {
			t.Fatalf("seed %d: %v, want %v", seed, got, want)
		}
	}
}

// TRLC-LINKS: REQ-SDS-066
func BenchmarkFundamentalHz(b *testing.B) {
	sig := discernSig(20000)
	for b.Loop() {
		fundamentalHz(sig, 2e-9)
	}
}

// TRLC-LINKS: REQ-SDS-011
func BenchmarkQualName(b *testing.B) {
	for b.Loop() {
		qualName(sramcapture.QualPulse)
		qualName(sramcapture.QualVideo)
	}
}

// TRLC-LINKS: REQ-SDS-265
func BenchmarkSoundRingRead(b *testing.B) {
	r := newSoundRing(16)
	raw := make([]byte, 4*40000)
	for i := range raw {
		raw[i] = byte(i)
	}
	now := time.Unix(0, 0)
	r.pushWords(raw, SoundScale{}, false, now)
	r.pushWords(raw, SoundScale{}, false, now) // past the end: the read wraps
	var q1, q2 []uint16
	for b.Loop() {
		blk := r.read(r.next-30000, 30000, q1, q2, 61e3)
		q1, q2 = blk.Q1, blk.Q2
	}
}

// The ring reads back what was pushed, across its wrap.
// TRLC-LINKS: REQ-SDS-265
func TestSoundRingReadWraps(t *testing.T) {
	r := newSoundRing(8)
	for k := 0; k < 5; k++ {
		raw := make([]byte, 4*97)
		for i := 0; i < 97; i++ {
			v := uint16(k*1000 + i)
			raw[4*i], raw[4*i+1], raw[4*i+2], raw[4*i+3] = byte(v), byte(v>>8), byte(^v), byte(^v>>8)
		}
		r.pushWords(raw, SoundScale{}, false, time.Unix(0, 0))
	}
	blk := r.read(r.next-200, 200, nil, nil, 1)
	for i := 0; i < blk.N; i++ {
		o := blk.First + uint64(i)
		want := uint16(int(o/97)*1000 + int(o%97))
		if blk.Q1[i] != want || blk.Q2[i] != ^want {
			t.Fatalf("sample %d: %d %d, want %d %d", o, blk.Q1[i], blk.Q2[i], want, ^want)
		}
	}
}

// firstCrossingFromRef is the negating loop firstCrossingFrom replaced.
// TRLC-LINKS: REQ-SDS-275
func firstCrossingFromRef(q []uint16, level, hyst int, rising bool, start int) (int, bool) {
	armed := false
	for i, w := range q {
		c, l := int(w>>8), level
		if !rising {
			c, l = -c, -l
		}
		switch {
		case c <= l-hyst:
			armed = true
		case armed && c >= l:
			if i >= start {
				return i, true
			}
			armed = false
		}
	}
	return 0, false
}

// TRLC-LINKS: REQ-SDS-275
func TestFirstCrossingFromSame(t *testing.T) {
	q := make([]uint16, 3000)
	for i := range q {
		q[i] = uint16(128+int(100*math.Sin(float64(i)/37))) << 8
	}
	for _, rising := range []bool{true, false} {
		for _, level := range []int{0, 60, 128, 200, 255} {
			for _, start := range []int{0, 500, 2999, 4000} {
				for _, hyst := range []int{0, 4, 30} {
					a, b := firstCrossingFrom(q, level, hyst, rising, start)
					c, d := firstCrossingFromRef(q, level, hyst, rising, start)
					if a != c || b != d {
						t.Fatalf("rising %v level %d start %d hyst %d: %d %v, want %d %v", rising, level, start, hyst, a, b, c, d)
					}
				}
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-275
func BenchmarkFirstCrossing(b *testing.B) {
	q := make([]uint16, 30000)
	for i := range q {
		q[i] = uint16(128+int(100*math.Sin(float64(i)/37))) << 8
	}
	for b.Loop() {
		firstCrossingFrom(q, 128, 4, false, 29000)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func BenchmarkSoftStreamMode(b *testing.B) {
	for b.Loop() {
		SoftStreamMode(serDMX, 250000, false)
		SoftStreamMode(serUART, 115200, false)
	}
}

// TRLC-LINKS: REQ-SDS-013
func BenchmarkBufferDecoded(b *testing.B) {
	e, _ := newTestEngine(&testing.T{}, newFakeBus())
	batch := make([]sramcapture.DecodedEvent, 1024)
	for b.Loop() {
		for i := range batch {
			batch[i].Sequence = e.decodedSequence + uint32(i)
		}
		e.bufferDecoded(batch)
		e.decodedHead, e.decodedCount = (e.decodedHead+e.decodedCount)%decodedBufferLimit, 0 // a consumer took them
	}
}

// TRLC-LINKS: REQ-SDS-013
func BenchmarkBufferDecodedRef(b *testing.B) {
	e, _ := newTestEngine(&testing.T{}, newFakeBus())
	batch := make([]sramcapture.DecodedEvent, 1024)
	s := &e.decodedService
	for b.Loop() {
		for i := range batch {
			batch[i].Sequence = e.decodedSequence + uint32(i)
		}
		for _, v := range batch { // the per-event loop it replaced
			if v.Kind == sramcapture.EventLoss {
				s.lost += uint64(v.Count)
			}
			if v.Sequence != e.decodedSequence || e.decodedCount >= decodedBufferLimit {
				break
			}
			e.decodedSequence++
			if len(e.decodedBuffer) != decodedBufferLimit {
				e.decodedBuffer = make([]sramcapture.DecodedEvent, decodedBufferLimit)
			}
			e.decodedBuffer[(e.decodedHead+e.decodedCount)%decodedBufferLimit] = v
			e.decodedCount++
			s.maxBuffered = max(s.maxBuffered, e.decodedCount)
		}
		e.decodedHead, e.decodedCount = (e.decodedHead+e.decodedCount)%decodedBufferLimit, 0
	}
}

// bufferDecoded keeps the events, sequence and errors the per-event loop
// did: a clean batch, a sequence gap mid-batch, a buffer filling, a wrap.
// TRLC-LINKS: REQ-SDS-013
func TestBufferDecodedSame(t *testing.T) {
	run := func(head, count int, seq uint32, batch []sramcapture.DecodedEvent, ref bool) (string, error) {
		e, _ := newTestEngine(t, newFakeBus())
		e.decodedBuffer = make([]sramcapture.DecodedEvent, decodedBufferLimit)
		e.decodedHead, e.decodedCount, e.decodedSequence = head, count, seq
		var err error
		if ref {
			s := &e.decodedService
			for _, v := range batch {
				if v.Kind == sramcapture.EventLoss {
					s.lost += uint64(v.Count)
				}
				if v.Sequence != e.decodedSequence {
					err = fmt.Errorf("decoded event sequence discontinuity")
					break
				}
				if e.decodedCount >= decodedBufferLimit {
					err = fmt.Errorf("decoded host buffer overflow; transcript incomplete")
					break
				}
				e.decodedSequence++
				e.decodedBuffer[(e.decodedHead+e.decodedCount)%decodedBufferLimit] = v
				e.decodedCount++
				s.maxBuffered = max(s.maxBuffered, e.decodedCount)
			}
		} else {
			err = e.bufferDecoded(batch)
		}
		var tail []sramcapture.DecodedEvent
		for i := 0; i < e.decodedCount && i < 50; i++ {
			tail = append(tail, e.decodedBuffer[(e.decodedHead+e.decodedCount-1-i)%decodedBufferLimit])
		}
		return fmt.Sprint(e.decodedCount, e.decodedSequence, e.decodedService.lost, e.decodedService.maxBuffered, tail), err
	}
	mk := func(seq uint32, n, gapAt int) []sramcapture.DecodedEvent {
		b := make([]sramcapture.DecodedEvent, n)
		for i := range b {
			b[i].Sequence, b[i].Kind, b[i].Count = seq+uint32(i), uint8(i%3), uint32(i)
			if gapAt >= 0 && i >= gapAt {
				b[i].Sequence++
			}
		}
		return b
	}
	for _, c := range []struct{ head, count, n, gap int }{{0, 0, 40, -1}, {5, 3, 40, 17}, {decodedBufferLimit - 10, 0, 40, -1}, {0, decodedBufferLimit - 7, 40, -1}, {100, 5, 40, 0}} {
		for _, kind := range []uint8{0} {
			_ = kind
			b := mk(1000, c.n, c.gap)
			got, gerr := run(c.head, c.count, 1000, b, false)
			want, werr := run(c.head, c.count, 1000, b, true)
			if got != want || fmt.Sprint(gerr) != fmt.Sprint(werr) {
				t.Fatalf("%+v: %s %v, want %s %v", c, got, gerr, want, werr)
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-001
func BenchmarkServiceCommands(b *testing.B) {
	e, _ := newTestEngine(&testing.T{}, newFakeBus())
	for b.Loop() {
		e.serviceCommands()
	}
}

// TRLC-LINKS: REQ-SDS-011
func BenchmarkTrigDispLevelAC(b *testing.B) {
	e, _ := newTestEngine(&testing.T{}, newFakeBus())
	e.SetTrigLevelCode(30000)
	e.chCoupling[0].Store(couplingAC)
	e.chMeanCode[0].Store(math.Float64bits(131.5))
	for b.Loop() {
		e.trigDispLevel(0)
	}
}
