// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Host CPU of the live stream decode: a second of each main protocol's
// stream, at the bucket the stream plan picks (or, for a bus too fast for
// the gap-free stream, the coarsest bucket that leaves a feature 3.5
// samples), pushed in the engine's pushes. CPU time is this thread's
// getrusage, so a busy machine does not distort it. On the PC:
//
//	SDS_STREAM_CPU=1 go test ./internal/decode -run TestStreamCPU -v
//
// and on the scope, cross-compiled (GOOS=linux GOARCH=arm GOARM=7 go test -c):
//
//	SDS_STREAM_CPU=1 ./decode-stream-cpu-arm.test -test.run TestStreamCPU -test.v

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// streamCPUExtra are buses the FPGA decodes today, modelled as if the live
// stream carried them.
var streamCPUExtra = []streamCase{
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "uart", baud: 115200, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		for k := 0; k < 1+i%4; k++ {
			tlUART(l.a, (i*37+k*91+rng.Intn(256))&0xff, 115200, "none", 1)
		}
		l.a.idle((20 + rng.Float64()*40) / 115200)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeUART(a, s, UARTCfg{Baud: 115200, Bits: 8, Parity: "none", Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "i2c", baud: 400000, lines: 2, gen: func(l twoLine, i int, rng *rand.Rand) {
		q := 1 / (4 * 400000.0)
		bit := func(v byte) { l.add(0, v, q); l.add(1, v, 2*q); l.add(0, v, q) }
		byteOut := func(v int) {
			for k := 7; k >= 0; k-- {
				bit(byte(v >> k & 1))
			}
			bit(0) // ACK
		}
		l.add(1, 0, q) // START: SDA falls, SCL high
		l.add(0, 0, q)
		byteOut(0x50<<1 | i&1)
		for k := 0; k < 1+i%3; k++ {
			byteOut((i*29 + rng.Intn(256)) & 0xff)
		}
		l.add(0, 0, q) // STOP
		l.add(1, 0, q)
		l.add(1, 1, (15+rng.Float64()*30)/400000)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeI2C(a, b, s, I2CCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "spi", baud: 1000000, lines: 2, idle: idleOf(0, 0), gen: func(l twoLine, i int, rng *rand.Rand) {
		h := 0.5 / 1e6
		for k := 0; k < 1+i%3; k++ {
			v := (i*53 + rng.Intn(256)) & 0xff
			for j := 7; j >= 0; j-- {
				d := byte(v >> j & 1)
				l.add(0, d, h)
				l.add(1, d, h)
			}
		}
		l.add(0, 0, (20+rng.Float64()*30)*1e-6)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeSPI(a, b, s, SPICfg{MSB: true, Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "can", baud: 500000, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		n := 1 + i%8
		data := make([]int, n)
		for k := range data {
			data[k] = rng.Intn(256)
		}
		_, wire := canStdFrame((i*0x123+7)&0x7ff, n, data)
		for _, b := range wire {
			l.a.add(byte(b), 1/500000.0)
		}
		l.a.idle((14 + rng.Float64()*40) / 500000)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeCANFD(a, s, CANFDCfg{NominalBaud: 500000, DominantLow: true, Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "manchester", baud: 100000, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		h := 0.5 / 100000
		for _, b := range mBits([]int{0xAA, i & 0xff, rng.Intn(256), 0x5C}, true, 8) {
			l.a.add(byte(1-b), h) // IEEE: a 1 rises mid-bit
			l.a.add(byte(b), h)
		}
		l.a.idle((12 + rng.Float64()*30) / 100000)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeManchester(a, s, ManchesterCfg{Bitrate: 100000, IEEE: true, MSB: true, Bits: 8, Threshold: 128, HaveThr: true})
	}},
}

// streamCPUTiming is StreamTiming for streamCPUExtra.
var streamCPUTiming = map[string]func(baud float64) (float64, float64, float64){
	"uart":       func(b float64) (float64, float64, float64) { return 1 / b, 50 / b, 15 / b },
	"i2c":        func(b float64) (float64, float64, float64) { return 0.25 / b, 45 / b, 12 / b },
	"spi":        func(b float64) (float64, float64, float64) { return 0.5 / b, 30 / b, 12 / b },
	"can":        func(b float64) (float64, float64, float64) { return 1 / b, 135 / b, 12 / b },
	"manchester": func(b float64) (float64, float64, float64) { return 0.5 / b, 36 / b, 10 / b },
}

// streamCPUNames are the protocols measured.
var streamCPUNames = []string{"uart", "dmx512", "midi", "can", "i2c", "spi", "manchester", "onewire", "ws281x", "pwm"}

// streamCPUCase is one protocol's generator, timing and stream bucket.
type streamCPUCase struct {
	c                       streamCase
	feature, longest, quiet float64
	lead                    float64
	bucket                  float64 // seconds a peak word, two samples
}

// streamCPUCaseOf finds name's generator and its stream's bucket.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamCPUCaseOf(t testing.TB, name string) streamCPUCase {
	var all []streamCase
	all = append(all, streamCases...)
	all = append(all, moreStreamCases...)
	all = append(all, captureCases...)
	all = append(all, streamCPUExtra...)
	for _, c := range all {
		if c.name != name {
			continue
		}
		tc := streamCPUCase{c: c}
		if f, ok := streamCPUTiming[name]; ok {
			tc.feature, tc.longest, tc.quiet = f(c.baud)
		} else {
			proto := strings.TrimSuffix(name, "-carrier")
			tc.feature, tc.longest, tc.quiet, _ = StreamTiming(proto, c.baud)
			tc.lead = StreamLead(proto, c.baud)
		}
		log, ok := StreamBucketLog(tc.feature)
		if !ok {
			for log = StreamGapFreeMinLog - 1; log > 1 && tc.feature < 3.5*float64(uint64(1)<<log)*2e-9; log-- {
			}
		}
		tc.bucket = float64(uint64(1)<<log) * 2e-9
		return tc
	}
	t.Fatalf("no case %s", name)
	return streamCPUCase{}
}

// streamCPULines is about secs of the case's traffic as the peak stream's
// samples; bursty adds a quiet stretch of 20 frames' length after every
// fourth frame.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamCPULines(tc streamCPUCase, seed int64, secs float64, bursty bool) (a, b []uint8, ss float64) {
	const m = 8
	rng := rand.New(rand.NewSource(seed))
	fine := newTwoLine(m / tc.bucket)
	idle := [2]byte{1, 1}
	if tc.c.name == "j1850" || tc.c.name == "irnec-carrier" {
		idle[0] = 0
	}
	if tc.c.idle != nil {
		idle = *tc.c.idle
	}
	fine.add(idle[0], idle[1], tc.quiet+2e-3)
	for i := 0; fine.a.t < secs; i++ {
		t0 := fine.a.t
		tc.c.gen(fine, i, rng)
		if bursty && i%4 == 3 {
			last := [2]byte{fine.a.bits[len(fine.a.bits)-1], fine.b.bits[len(fine.b.bits)-1]}
			fine.add(last[0], last[1], 20*(fine.a.t-t0))
		}
	}
	fine.add(idle[0], idle[1], 2*tc.quiet+tc.longest)
	a, b = peakStream(fine.a.codes(), m, rng), peakStream(fine.b.codes(), m, rng)
	n := min(len(a), len(b))
	return a[:n], b[:n], tc.bucket / 2
}

// streamCPUIdle is secs of the case's idle line as the peak stream's
// samples: what a quiet bus costs.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamCPUIdle(tc streamCPUCase, secs float64) (a, b []uint8, ss float64) {
	const m = 8
	rng := rand.New(rand.NewSource(1))
	fine := newTwoLine(m / tc.bucket)
	idle := [2]byte{1, 1}
	if tc.c.name == "j1850" || tc.c.name == "irnec-carrier" {
		idle[0] = 0
	}
	if tc.c.idle != nil {
		idle = *tc.c.idle
	}
	fine.add(idle[0], idle[1], secs)
	a, b = peakStream(fine.a.codes(), m, rng), peakStream(fine.b.codes(), m, rng)
	n := min(len(a), len(b))
	return a[:n], b[:n], tc.bucket / 2
}

// streamCPURun pushes the samples through a stream decoder in the engine's
// pushes and returns the frames.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamCPURun(tc streamCPUCase, a, b []uint8, ss float64, dec func(a, b []uint8) Result) []StreamFrame {
	d := NewStreamDecoder(dec, int(tc.quiet/ss), int((tc.longest+tc.quiet)/ss))
	d.SetLead(int(tc.lead / ss))
	d.SetSubframes(tc.c.name == "spdif")
	pushN := int(math.Max(0.2, math.Min(1, 2*tc.quiet)) / ss)
	var out []StreamFrame
	for at := 0; at < len(a); at += pushN {
		e := min(len(a), at+pushN)
		var bl []uint8
		if tc.c.lines == 2 {
			bl = b[at:e]
		}
		out = append(out, d.Push(a[at:e], bl)...)
	}
	return append(out, d.Flush()...)
}

// threadCPU is the process's user+system CPU time in ns.
// TRLC-LINKS: REQ-SDS-018
func threadCPU() int64 {
	var r syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &r) // the collector's threads too
	return r.Utime.Nano() + r.Stime.Nano()
}

// cpuOf is fn's CPU time a call (ns): the least of seven rounds of at least
// 80 ms each, so other work on the machine inflates no round it spares.
// With SDS_STREAM_CPU_CYCLES=1 it is the calling thread's user cycles a
// call instead (perf counters: a loaded PC's time swings by a third), with
// =insn its instructions.
// TRLC-LINKS: REQ-SDS-018
func cpuOf(fn func()) float64 {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	fn()
	if os.Getenv("SDS_STREAM_CPU_CYCLES") != "" {
		if c, ok := cyclesOf(fn); ok {
			return c
		}
	}
	best := math.Inf(1)
	for range 7 {
		runtime.GC()
		t0, n := threadCPU(), 0
		for threadCPU()-t0 < 80e6 || n < 2 {
			fn()
			n++
		}
		best = min(best, float64(threadCPU()-t0)/float64(n))
	}
	return best
}

// cyclesOf is fn's user-mode cycles on this thread a call, the least of
// five rounds; ok false without a perf counter.
// TRLC-LINKS: REQ-SDS-018
func cyclesOf(fn func()) (float64, bool) {
	var attr [128]byte
	binary.LittleEndian.PutUint32(attr[0:], 0)               // PERF_TYPE_HARDWARE
	binary.LittleEndian.PutUint32(attr[4:], 128)             // size
	binary.LittleEndian.PutUint64(attr[8:], 0)               // PERF_COUNT_HW_CPU_CYCLES
	binary.LittleEndian.PutUint64(attr[40:], 1<<0|1<<5|1<<6) // disabled, exclude_kernel, exclude_hv
	fd, _, errno := syscall.Syscall6(syscall.SYS_PERF_EVENT_OPEN, uintptr(unsafe.Pointer(&attr[0])), 0, ^uintptr(0), ^uintptr(0), 0, 0)
	if errno != 0 {
		return 0, false
	}
	defer syscall.Close(int(fd))
	read := func() uint64 {
		var b [8]byte
		_, _ = syscall.Read(int(fd), b[:])
		return binary.LittleEndian.Uint64(b[:])
	}
	const enable, disable = 0x2400, 0x2401 // PERF_EVENT_IOC_ENABLE, _DISABLE
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, enable, 0)
	best := math.Inf(1)
	for range 5 {
		runtime.GC()
		c0, n := read(), 0
		for t0 := threadCPU(); threadCPU()-t0 < 60e6 || n < 2; n++ {
			fn()
		}
		best = min(best, float64(read()-c0)/float64(n))
	}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, disable, 0)
	return best, true
}

// TestStreamCPU reports each protocol's decode CPU a second of stream, and
// how many samples reach the decoder and its slicing a pushed sample.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestStreamCPU(t *testing.T) {
	if os.Getenv("SDS_STREAM_CPU") == "" {
		t.Skip("SDS_STREAM_CPU=1 to measure")
	}
	var sb strings.Builder
	p := func(f string, a ...any) { fmt.Fprintf(&sb, f, a...) }
	unit := "CPU ms a second of stream (% of one core)"
	if os.Getenv("SDS_STREAM_CPU_CYCLES") != "" {
		unit = "user Mcycles a second of stream"
		if os.Getenv("SDS_STREAM_CPU_CYCLES") == "insn" {
			unit = "user M instructions a second of stream"
		}
	}
	p("%s/%s; %s, decoded samples a pushed one\n", runtime.GOOS, runtime.GOARCH, unit)
	p("%-11s %8s %9s | %9s %7s | %9s %7s | %7s | %5s %7s\n", "protocol", "bucket", "samples/s", "dense ms", "redo", "bursty ms", "redo", "idle ms", "frames", "slice%")
	for _, name := range streamCPUNames {
		tc := streamCPUCaseOf(t, name)
		var row [2][2]float64
		var frames int
		var slicePct float64
		for k, bursty := range []bool{false, true} {
			a, b, ss := streamCPULines(tc, 7, 0.5, bursty)
			secs := float64(len(a)) * ss
			decoded := 0
			dec := func(x, y []uint8) Result { decoded += len(x); return tc.c.dec(x, y, ss) }
			fr := streamCPURun(tc, a, b, ss, dec)
			if k == 0 {
				frames = len(fr)
			}
			redo := float64(decoded) / float64(len(a))
			ns := cpuOf(func() { streamCPURun(tc, a, b, ss, dec) })
			row[k] = [2]float64{ns / secs / 1e6, redo}
			if k == 0 {
				sl := cpuOf(func() {
					sliceChannel(a, 128, true)
					if tc.c.lines == 2 {
						sliceChannel(b, 128, true)
					}
				})
				slicePct = 100 * sl * redo / ns
			}
		}
		ia, ib, iss := streamCPUIdle(tc, 0.25)
		idleDec := func(x, y []uint8) Result { return tc.c.dec(x, y, iss) }
		idleMS := cpuOf(func() { streamCPURun(tc, ia, ib, iss, idleDec) }) / (float64(len(ia)) * iss) / 1e6
		p("%-11s %6.3gus %9.3g | %9.2f %7.2f | %9.2f %7.2f | %7.2f | %5d %6.0f%%\n", name, tc.bucket*1e6, 2/tc.bucket,
			row[0][0], row[0][1], row[1][0], row[1][1], idleMS, frames, slicePct)
	}
	t.Log("\n" + sb.String())
	if path := os.Getenv("SDS_STREAM_CPU_OUT"); path != "" {
		_ = os.WriteFile(path, []byte(sb.String()), 0o644)
	}
}

// streamCPUData is a benchmark's stream, made once.
type streamCPUData struct {
	a, b []uint8
	ss   float64
}

var streamCPUBench = map[string]streamCPUData{}

// BenchmarkStreamCPU is TestStreamCPU's dense streams for a profile
// (SDS_STREAM_BURSTY=1: the bursty ones).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func BenchmarkStreamCPU(b *testing.B) {
	for _, name := range streamCPUNames {
		b.Run(name, func(b *testing.B) {
			tc := streamCPUCaseOf(b, name)
			key := fmt.Sprint(name, os.Getenv("SDS_STREAM_BURSTY"))
			if _, ok := streamCPUBench[key]; !ok {
				a, l2, ss := streamCPULines(tc, 7, 0.5, os.Getenv("SDS_STREAM_BURSTY") != "")
				streamCPUBench[key] = streamCPUData{a, l2, ss}
			}
			in := streamCPUBench[key]
			a, l2, ss := in.a, in.b, in.ss
			dec := func(x, y []uint8) Result { return tc.c.dec(x, y, ss) }
			b.ResetTimer()
			for range b.N {
				streamCPURun(tc, a, l2, ss, dec)
			}
		})
	}
}
