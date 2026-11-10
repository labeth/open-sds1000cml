// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package streams

import (
	"math"
	"testing"

	"github.com/labeth/open-dso-core/decode"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// A digital period of any whole number of raw words maps the stream's
// samples to their exact 2 ns ordinals, two a raw word, far into a stream;
// the sample time agrees with them; the arm's decimation is one a stream
// takes, spanning a word; the pace sramcapture writes is the period.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSoftStreamPaceOrdinals(t *testing.T) {
	for _, period := range []int{decode.StreamBitsMinPeriod, 13, 90, 166, 2170, decode.StreamBitsMaxPeriod} {
		plan := softPlan{log: softBitsLog(period), period: period, bits: true}
		s := &softStream{}
		s.setTiming(plan)
		for _, i := range []uint64{0, 1, 15, 16, 1<<20 + 3, 1<<36 + 12345} {
			if got, want := s.ord(i), i*2*uint64(period); got != want {
				t.Fatalf("period %d: sample %d at ordinal %d, want %d", period, i, got, want)
			}
			if d := math.Abs(float64(i)*s.sampleS - float64(s.ord(i))*2e-9); d > 1e-12*float64(i)*s.sampleS {
				t.Fatalf("period %d: sample %d at %.15g s, ordinal says %.15g", period, i, float64(i)*s.sampleS, float64(s.ord(i))*2e-9)
			}
		}
		if plan.log < 8 || plan.log > decode.StreamMaxLog || (plan.log < decode.StreamMaxLog && 1<<plan.log < 32*period) || (plan.log > 8 && 1<<(plan.log-1) >= 32*period) {
			t.Errorf("period %d: decimation 2^%d", period, plan.log)
		}
		if p := plan.pace(); p != (sramcapture.StreamPace{Words: uint32(period)}) {
			t.Errorf("period %d: pace %+v", period, p)
		}
	}
	peak := softPlan{log: 11}
	s := &softStream{}
	s.setTiming(peak)
	if s.ordStep != 1024 || s.sampleS != 2.048e-6 || peak.pace() != (sramcapture.StreamPace{}) || peak.wordRate() != 500e6/2048 {
		t.Fatalf("peak plan: step %v, sample %v, pace %+v, %v words a second", s.ordStep, s.sampleS, peak.pace(), peak.wordRate())
	}
}

// LIN at 250 kBd streamed at periods that are no power of two, as at powers
// of two: every frame decoded, each frame's start on the exact ordinal of
// a period, the one before the period its break edge falls in (a period
// reads the new level from the edge on, and LIN starts a frame at the
// sample before its break).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSoftStreamPaceFrameOrdinals(t *testing.T) {
	const baud = 250000
	bitN := 2000 // 2 ns samples a bit
	var levels []bool
	var breaks []uint64
	add := func(v bool, bits int) {
		for range bits * bitN {
			levels = append(levels, v)
		}
	}
	char := func(v int) {
		add(false, 1)
		for i := range 8 {
			add(v>>i&1 == 1, 1)
		}
		add(true, 1)
	}
	add(true, 300)
	var frames [][]int
	for i := range 8 {
		id := 0x10 + i
		bit := func(k int) int { return id >> k & 1 }
		pid := id | (bit(0)^bit(1)^bit(2)^bit(4))<<6 | (1^bit(1)^bit(3)^bit(4)^bit(5))<<7
		sum := pid
		for _, v := range []int{i, 0x80 + i} {
			if sum += v; sum > 0xff {
				sum -= 0xff
			}
		}
		frames = append(frames, []int{pid, i, 0x80 + i, ^sum & 0xff})
		// a break that starts anywhere in a period: an odd sample shift
		add(true, 0)
		for range 37*i + 11 {
			levels = append(levels, true)
		}
		breaks = append(breaks, uint64(len(levels)))
		add(false, 13)
		add(true, 1)
		char(0x55)
		for _, v := range frames[i] {
			char(v)
		}
		add(true, 300)
	}
	add(true, 600)
	name := softProtoNames[core.SerLIN]
	_, longest, quiet, _ := decode.StreamTiming(name, baud)
	for _, period := range []int{16, 64, 13, 90, 166, 251} {
		raw := bitsWords(levels, 2*period)
		d, _ := core.LookupSerialDecoder(core.SerLIN)
		s := &softStream{
			p: SerialParams{Proto: core.SerLIN, Baud: baud, Threshold: 128, HaveThr: true}, proto: name, decoder: d,
			mask: 0xff, gapFree: true, quiet: quiet, bits: true, trig: StreamManual,
			id: sramcapture.RecordIdentity{Epoch: softEpochBase | 1},
		}
		s.setTiming(softPlan{log: softBitsLog(period), period: period, bits: true})
		s.dec = decode.NewStreamDecoder(s.decodeFunc(), int(quiet/s.sampleS), int((longest+quiet)/s.sampleS))
		for at := 0; at < len(raw); {
			n := min(len(raw)-at, 4*(1+(at*7919)%300))
			s.feedBits(raw[at : at+n])
			at += n
			s.emit(s.dec.Push(s.pendA, nil), s.ord)
			s.pendA = s.pendA[:0]
		}
		s.emit(s.dec.Flush(), s.ord)
		var starts []uint64
		for _, ev := range s.events {
			if ev.Kind == sramcapture.EventStart {
				starts = append(starts, ev.Sample)
			}
		}
		if len(starts) != len(breaks) {
			t.Fatalf("period %d: %d frames, want %d", period, len(starts), len(breaks))
		}
		for i, o := range starts {
			// levels[k] is 2 ns sample k. LIN starts a frame at the sample
			// before its break, at the powers of two as at any period: the
			// period before the one the edge falls in.
			if p := uint64(2 * period); o%p != 0 || breaks[i] < o+p || breaks[i] >= o+2*p {
				t.Fatalf("period %d frame %d: start at ordinal %d, break edge at %d", period, i, o, breaks[i])
			}
		}
	}
}
