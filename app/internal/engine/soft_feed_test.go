// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math/rand"
	"os"
	"runtime"
	"slices"
	"syscall"
	"testing"
)

// feedPeakRef is feedPeak's pair ordering a sample at a time with branches,
// as it was: the reference the branch-free loop must match.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func feedPeakRef(s *softStream, raw []byte, cal *InterleaveCalibration) {
	n := len(raw) / 4
	lines := 1
	if clockedTwoLine(s.p.Proto) {
		lines = 2
	}
	for k := 0; k < lines; k++ {
		ch := s.p.ChA & 1
		dst := &s.pendA
		if k == 1 {
			ch, dst = 1-ch, &s.pendB
		}
		var off [2]int
		if cal != nil {
			off = cal.envelopeOffsets(ch)
		}
		var q [2][256]int32
		var r [2][256]uint8
		for k := range 2 {
			for b := range 256 {
				x := b << 8
				if cal != nil {
					x = debiasQ(x, off[k])
				}
				q[k][b], r[k][b] = int32(x), roundQ8(uint16(x))
			}
		}
		at := len(*dst)
		out := slices.Grow(*dst, 2*n)[:at+2*n]
		prev := int32(s.last[ch])
		if !s.haveLast && n > 0 {
			prev = (q[0][raw[ch]] + q[1][raw[2+ch]]) / 2
		}
		o := out[at:]
		for i := 0; i < n; i++ {
			bl, bh := raw[4*i+ch], raw[4*i+2+ch]
			lo, hi := q[0][bl], q[1][bh]
			dl, dh := lo-prev, hi-prev
			if dl < 0 {
				dl = -dl
			}
			if dh < 0 {
				dh = -dh
			}
			if dh < dl {
				o[2*i], o[2*i+1] = r[1][bh], r[0][bl]
				prev = lo
			} else {
				o[2*i], o[2*i+1] = r[0][bl], r[1][bh]
				prev = hi
			}
		}
		s.last[ch] = uint16(prev)
		*dst = out
		if k == 1 {
			s.learnB(out[at:])
		}
	}
	s.haveLast = s.haveLast || n > 0
}

// softPeakWords is n peak words of two lines that switch between two
// levels now and then, with converter noise, and some words at the rails.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func softPeakWords(rng *rand.Rand, n int) []byte {
	raw := make([]byte, 4*n)
	lv := [2]int{56, 200}
	for i := range n {
		for ch := range 2 {
			if rng.Intn(20) == 0 {
				lv[ch] = 256 - lv[ch]
			}
			a := min(255, max(0, lv[ch]+rng.Intn(9)-4))
			b := min(255, max(0, lv[ch]+rng.Intn(9)-4))
			if rng.Intn(50) == 0 {
				a = rng.Intn(256)
			}
			if rng.Intn(200) == 0 {
				b = 255 * rng.Intn(2)
			}
			raw[4*i+ch], raw[4*i+2+ch] = uint8(min(a, b)), uint8(max(a, b))
		}
	}
	return raw
}

// TestFeedPeakSame feeds random peak words in random pieces, with and
// without converter offsets, through feedPeak and the reference: the same
// samples, line B's levels and last sample.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestFeedPeakSame(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for trial := range 200 {
		var cal *InterleaveCalibration
		if trial%2 == 1 {
			cal = &InterleaveCalibration{}
			for ch := range 2 {
				for k := range 5 {
					cal.Offset[ch][k] = rng.Float64()*6 - 3
				}
			}
		}
		proto := serDMX
		if trial%3 == 0 {
			proto = serSWD
		}
		p := SerialParams{Proto: proto, ChA: trial % 2}
		a, b := &softStream{p: p}, &softStream{p: p}
		raw := softPeakWords(rng, 1+rng.Intn(3000))
		for at := 0; at < len(raw)/4; {
			n := min(len(raw)/4-at, rng.Intn(400))
			a.feedPeak(raw[4*at:4*(at+n)], cal)
			feedPeakRef(b, raw[4*at:4*(at+n)], cal)
			at += n
		}
		if !slices.Equal(a.pendA, b.pendA) || !slices.Equal(a.pendB, b.pendB) || a.last != b.last ||
			a.haveLast != b.haveLast || a.bLo != b.bLo || a.bHi != b.bHi {
			t.Fatalf("trial %d: feedPeak differs from the reference", trial)
		}
	}
}

// BenchmarkFeedPeak is feedPeak's cost a word on one line.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func BenchmarkFeedPeak(b *testing.B) {
	raw := softPeakWords(rand.New(rand.NewSource(1)), 1<<16)
	s := &softStream{p: SerialParams{Proto: serDMX}}
	b.SetBytes(int64(len(raw)))
	for range b.N {
		s.pendA = s.pendA[:0]
		s.feedPeak(raw, nil)
	}
}

// TestSoftFeedCPU reports feedPeak's CPU a peak word (one line, then two),
// the process's getrusage time, least of five rounds: the live stream's cost
// before the decode (SDS_STREAM_CPU=1; on the scope the cross-compiled test
// binary: SDS_STREAM_CPU=1 ./engine-arm.test -test.run TestSoftFeedCPU -test.v).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSoftFeedCPU(t *testing.T) {
	if os.Getenv("SDS_STREAM_CPU") == "" {
		t.Skip("SDS_STREAM_CPU=1 to measure")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cpu := func() int64 {
		var r syscall.Rusage
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &r)
		return r.Utime.Nano() + r.Stime.Nano()
	}
	raw := softPeakWords(rand.New(rand.NewSource(1)), 1<<16)
	for _, proto := range []int{serDMX, serSWD} {
		var ns [2]float64
		for k, feed := range []func(s *softStream, raw []byte, cal *InterleaveCalibration){feedPeakRef, (*softStream).feedPeak} {
			s := &softStream{p: SerialParams{Proto: proto}}
			for round := range 5 {
				t0, n := cpu(), 0
				for cpu()-t0 < 100e6 || n < 2 {
					// The words in pieces as the drain hands them.
					s.pendA, s.pendB = s.pendA[:0], s.pendB[:0]
					for at := 0; at < len(raw); at += 4 * 4096 {
						feed(s, raw[at:min(len(raw), at+4*4096)], &InterleaveCalibration{})
					}
					n++
				}
				v := float64(cpu()-t0) / float64(n) / float64(len(raw)/4)
				if round == 0 || v < ns[k] {
					ns[k] = v
				}
			}
		}
		t.Logf("feedPeak %s/%s, %d line(s): %.2f ns a word (the branching loop: %.2f)", runtime.GOOS, runtime.GOARCH, 1+btoi(clockedTwoLine(proto)), ns[1], ns[0])
	}
}

// btoi is 1 for true.
// TRLC-LINKS: REQ-SDS-018
func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
