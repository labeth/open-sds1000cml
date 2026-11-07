// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The engine's per-capture work on the scope's single ARM core: these time
// the pieces a capture, a serial-trigger check or a view runs through.
// go test ./internal/engine -run - -bench .

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func BenchmarkPlanHardwareUART(b *testing.B) {
	p := SerialParams{Proto: core.SerUART, Baud: 115200, Bits: 8, Parity: "none", HaveThr: true, Threshold: 128, Bytes: []int{0x55, 0xAA}}
	q := p
	q.Parity, q.Baud = "even", 1000000
	for b.Loop() {
		acq.PlanHardwareUART(&p, false)
		acq.PlanHardwareUART(&q, true)
	}
}

// planParams are serial settings each hardware planner accepts.
// TRLC-LINKS: REQ-SDS-013
func planParams() map[string]SerialParams {
	base := SerialParams{HaveThr: true, Threshold: 128, ChA: 0, ChB: 1, Bytes: []int{0x55, 0x0A}, Inverted: false}
	m := map[string]SerialParams{}
	p := base
	p.Proto, p.SPIClockHz, p.MSB = core.SerSPI, 10000000, true
	m["spi"] = p
	p = base
	p.Proto, p.Addr, p.RW = core.SerI2C, 0x50, 2
	m["i2c"] = p
	p = base
	p.Proto, p.TickNs, p.Nibbles, p.Bytes = core.SerSENT, 3000, 8, []int{5, 10}
	m["sent"] = p
	p = base
	p.Proto, p.Baud, p.Bytes = core.SerMIL1553, 1000000, []int{0x1234}
	m["mil1553"] = p
	p = base
	p.Proto, p.Baud = core.SerUSB, 1500000
	m["usbls"] = p
	return m
}

// planByValue calls each planner on a copy of p, as the planners took
// their settings before (the reference).
// TRLC-LINKS: REQ-SDS-013
//
//go:noinline
func planByValue(name string, p SerialParams) bool {
	switch name {
	case "spi":
		return acq.PlanHardwareSPI(&p, true).Enabled
	case "i2c":
		return acq.PlanHardwareI2C(&p, true).Enabled
	case "sent":
		return acq.PlanHardwareSENT(&p).Enabled
	case "mil1553":
		return acq.PlanHardwareMIL1553(&p).Enabled
	}
	return acq.PlanHardwareUSBLS(&p).Enabled
}

// planByPointer calls each planner on p itself.
// TRLC-LINKS: REQ-SDS-013
//
//go:noinline
func planByPointer(name string, p *SerialParams) bool {
	switch name {
	case "spi":
		return acq.PlanHardwareSPI(p, true).Enabled
	case "i2c":
		return acq.PlanHardwareI2C(p, true).Enabled
	case "sent":
		return acq.PlanHardwareSENT(p).Enabled
	case "mil1553":
		return acq.PlanHardwareMIL1553(p).Enabled
	}
	return acq.PlanHardwareUSBLS(p).Enabled
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
		core.NoiseFloor(sig)
	}
}

// TRLC-LINKS: REQ-SDS-009, REQ-SDS-011
func BenchmarkPtp(b *testing.B) {
	sig := discernSig(20000)
	for b.Loop() {
		core.Ptp(sig)
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

// TRLC-LINKS: REQ-SDS-010
func BenchmarkRollPush(b *testing.B) {
	var r rollView
	r.reset(rollPlan{screen: 2000, group: 1}, 0.1, [2]core.ChScale{{Vdiv: 0.5, Off: 0.1}, {Vdiv: 2}})
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
	f := &sample.Frame{
		Traces: []sample.Trace{{Codes8: make([]uint8, 3000), Min8: make([]uint8, envDisplayCols)[:0], Max8: make([]uint8, envDisplayCols)[:0]}, {Ch: 1, Codes8: make([]uint8, 3000), Min8: make([]uint8, envDisplayCols)[:0], Max8: make([]uint8, envDisplayCols)[:0]}},
	}
	for k := range envRingN {
		for i := range f.Codes8(0) {
			f.Codes8(0)[i], f.Codes8(1)[i] = uint8(i*7+k), uint8(i*3)
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
		for i := range n {
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
	for range 30 {
		e.notePublish(e.clk.Now())
		clk.t = clk.t.Add(10 * time.Millisecond)
	}
	if fps := e.Snapshot().FPS; fps != 30 {
		t.Fatalf("FPS %v after 30 publishes in 0.3 s, want 30", fps)
	}
	for range 200 {
		e.notePublish(e.clk.Now())
		clk.t = clk.t.Add(10 * time.Millisecond)
	}
	if fps := e.Snapshot().FPS; fps != 64 { // the window holds 64
		t.Fatalf("FPS %v, want 64", fps)
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
		core.RefineTriggerEdge(sig, q, len(sig), 9030.4, 128, true)
		core.RefineTriggerEdge(sig, nil, len(sig), 9630.4, 128, false)
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
	e.setInterleaveCalibration(cal)
	m := sramcapture.Metadata{SampleRateHz: 500e6, SamplesPerWord: 2, Decimation: 1}
	const n = 2048
	src := discernSig(n)
	f := &sample.Frame{Traces: []sample.Trace{{Codes8: make([]uint8, n), Valid: n}, {Ch: 1, Codes8: make([]uint8, n), Valid: n}}}
	r := &viewRead{p: core.ViewPlan{Bucket: 4, Buckets: n / 2, Length: 2 * n, N: n}, f: f}
	r.got = n
	for b.Loop() {
		copy(f.Codes8(0), src)
		copy(f.Codes8(1), src)
		e.window(m, r, [2]core.ChScale{{Vdiv: 1}, {Vdiv: 1}}, core.EnvelopeValue(0, 4))
	}
}

// TRLC-LINKS: REQ-SDS-013
//
//go:noinline
func planPacketByValue(p SerialParams) bool { return acq.PlanHardwarePacket(&p, 0xffff, 0).Enabled }

// TRLC-LINKS: REQ-SDS-013
//
//go:noinline
func planPacketByPointer(p *SerialParams) bool { return acq.PlanHardwarePacket(p, 0xffff, 0).Enabled }

// TRLC-LINKS: REQ-SDS-013
//
//go:noinline
func planSymbolByValue(p SerialParams) bool { return acq.PlanHardwareSymbol(&p).Enabled }

// TRLC-LINKS: REQ-SDS-013
//
//go:noinline
func planSymbolByPointer(p *SerialParams) bool { return acq.PlanHardwareSymbol(p).Enabled }

// TRLC-LINKS: REQ-SDS-013
func BenchmarkPlanPacketSymbol(b *testing.B) {
	pk := SerialParams{Proto: core.SerCAN, Baud: 500000, HaveThr: true, Threshold: 128, Bytes: []int{0x12, 0x34}}
	sy := SerialParams{Proto: core.SerLIN, Baud: 19200, HaveThr: true, Threshold: 128, Bytes: []int{0x55}}
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
		{Proto: core.SerPWM, Baud: 1000, HaveThr: true, Threshold: 128},
		{Proto: core.SerDCF77, Baud: 1, HaveThr: true, Threshold: 128},
		{Proto: core.SerIRMP, Baud: 1000, Bits: 10, HaveThr: true, Threshold: 128},
	}
	for b.Loop() {
		for i := range ps {
			acq.PlanHardwareSymbol(&ps[i])
		}
	}
}

// TRLC-LINKS: REQ-SDS-231
func BenchmarkRecordStuck(b *testing.B) {
	c1, c2 := discernSig(100003), discernSig(100003)
	for b.Loop() {
		recordStuck(c1, c2)
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
		for ch := range 2 {
			var held [5]bool
			moving := false
			for p := range 5 {
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
	for seed := range 300 {
		n := adcSnaps/2 + seed%400
		snaps := make([][10]uint8, n)
		for j := range snaps {
			for k := range 10 {
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
	p := core.DefaultTrigParams()
	for b.Loop() {
		core.QualifyPulse(sig, 2, p, true)
		core.QualifyPulse(sig, 2, p, false)
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
		f := &sample.Frame{Traces: []sample.Trace{{Codes8: make([]uint8, n), Precision: make([]uint16, n), Valid: n}, {Ch: 1, Codes8: make([]uint8, n), Precision: make([]uint16, n), Valid: n}}}
		b.Run(map[uint8]string{0: "codes", 8: "q8"}[fb], func(b *testing.B) {
			for b.Loop() {
				w := acq.FrameWriter{F: f, M: sramcapture.Metadata{SamplesPerWord: spw, FractionBits: fb}}
				if _, err := w.Write(payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TRLC-LINKS: REQ-SDS-007
func BenchmarkArenaConsume(b *testing.B) {
	a := newFrameArena(16)
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
		case KindNativeFast, KindDecimated:
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

// TRLC-LINKS: REQ-SDS-066
func BenchmarkFundamentalHz(b *testing.B) {
	sig := discernSig(20000)
	for b.Loop() {
		core.FundamentalHz(sig, 2e-9)
	}
}

// TRLC-LINKS: REQ-SDS-011
func BenchmarkQualName(b *testing.B) {
	for b.Loop() {
		acq.QualName(sramcapture.QualPulse)
		acq.QualName(sramcapture.QualVideo)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func BenchmarkSoftStreamMode(b *testing.B) {
	for b.Loop() {
		SoftStreamMode(core.SerDMX, 250000, false)
		SoftStreamMode(core.SerUART, 115200, false)
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
		if err := e.bufferDecoded(batch); err != nil {
			b.Fatal(err)
		}
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
					err = errors.New("decoded event sequence discontinuity")
					break
				}
				if e.decodedCount >= decodedBufferLimit {
					err = errors.New("decoded host buffer overflow; transcript incomplete")
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

// TRLC-LINKS: REQ-SDS-010
func BenchmarkRunWord(b *testing.B) {
	e, _ := newTestEngine(&testing.T{}, newFakeBus())
	bands := []Band{}
	for _, t := range []float64{1e-6, 10e-3, 200e-3} {
		band, _ := PlanTdiv(t)
		bands = append(bands, band)
	}
	for b.Loop() {
		for _, band := range bands {
			e.band = band
			e.runWord()
		}
	}
}

var lineSink []byte

// TRLC-LINKS: REQ-SDS-018
func BenchmarkLineBuf(b *testing.B) {
	for b.Loop() {
		bp := getLineBuf()
		lineSink = (*bp)[:16]
		lineBufs.Put(bp)
	}
}

// TRLC-LINKS: REQ-SDS-018
func BenchmarkLineBufRef(b *testing.B) {
	for b.Loop() {
		lineSink = make([]byte, 64*1024)[:16] // a fresh buffer a poll, as before
	}
}

// TRLC-LINKS: REQ-SDS-009
func BenchmarkSnapshot(b *testing.B) {
	e, clk := newTestEngine(&testing.T{}, newFakeBus())
	for range 100 {
		e.notePublish(e.clk.Now())
		clk.t = clk.t.Add(20 * time.Millisecond)
	}
	for b.Loop() {
		e.Snapshot()
	}
}

// Every full-rate frame is offset-corrected: two 14000-sample channels.
// TRLC-LINKS: REQ-SDS-016
func BenchmarkInterleaveApply(b *testing.B) {
	c := InterleaveCalibration{Vdiv: [2]float64{2, 2}, Offset: [2][5]float64{{3, -6, -7, 5, 5}, {-2, -1, 0, 1, 2}}}
	n := 14000
	a0, b0 := make([]uint8, n), make([]uint8, n)
	for i := range a0 {
		base := 100 + (i/5)%50
		a0[i] = uint8(base + int(c.Offset[0][(i+2)%5]))
		b0[i] = uint8(base + int(c.Offset[1][(i+2)%5]))
	}
	a0[77], b0[91] = 255, 0
	x, y := make([]uint8, n), make([]uint8, n)
	q1, q2 := make([]uint16, n), make([]uint16, n)
	for b.Loop() {
		copy(x, a0)
		copy(y, b0)
		if !c.apply(x, y, q1, q2, [2]float64{2, 2}, 2e-9) {
			b.Fatal("rejected")
		}
	}
}

// The capture loop rebuilds its config every status poll to see a change.
// TRLC-LINKS: REQ-SDS-009
func BenchmarkSramConfig(b *testing.B) {
	e, _ := newTestEngine(&testing.T{}, newFakeBus())
	cfg, _, _, _, _ := e.sramConfig()
	for b.Loop() {
		if next, _, _, _, _ := e.sramConfig(); next != cfg {
			b.Fatal("changed")
		}
	}
}
