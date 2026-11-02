// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"open-sds/app/internal/decode"
	"open-sds/app/internal/decodedlines"
	"open-sds/app/internal/sramcapture"
	"testing"
)

// bitsWords slices levels (m per period) as stream_bits.v does: a period
// reads the level before it flipped when any of its samples differs from
// that level, sixteen periods a word, the line on both channels.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func bitsWords(levels []bool, m int) []byte {
	var raw []byte
	last := len(levels) > 0 && levels[0]
	var w uint16
	n := 0
	for k := 0; k+m <= len(levels); k += m {
		differ := false
		for _, v := range levels[k : k+m] {
			differ = differ || v != last
		}
		last = last != differ
		if last {
			w |= 1 << n
		}
		if n++; n == 16 {
			raw = append(raw, byte(w), byte(w>>8), byte(w), byte(w>>8))
			w, n = 0, 0
		}
	}
	return raw
}

// withBitsImage runs f as a build with the digital stream image.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func withBitsImage(t *testing.T, f func()) {
	t.Helper()
	was := bitsImage.Load()
	bitsImage.Store(true)
	defer bitsImage.Store(was)
	f()
}

// With the digital stream image the buses past the peak stream's reach
// stream gap-free, the slower ones at a coarser period, any whole number of
// raw words: six periods a feature, or the finest the budget allows (a
// clocked bus's two lines share it) where that keeps three and a half;
// carrier lines (IR, RFID) keep the peak words.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSoftStreamModesWithBits(t *testing.T) {
	withBitsImage(t, func() {
		for _, c := range []struct {
			proto, baud int
			mode        string
			bits        bool
			period      int // raw words
		}{
			// 1.67 us: six periods of 69 raw words, 3.6 M samples a second
			{serWS281x, 800000, "captures", false, 0},
			{serUSBPD, 300000, "gap-free", true, 69},
			{serSWD, 1000000, "captures", false, 0},
			{serMDIO, 1000000, "captures", false, 0},
			// 2 us on two lines: 2 M samples a second a line, 4 periods
			{serMDIO, 250000, "gap-free", true, 125},
			{serPDI, 250000, "gap-free", true, 125},
			{serMiller, 106007, "gap-free", true, 98},
			{serMaple, 1333333, "captures", false, 0},
			{serDMX, 250000, "gap-free", true, 166},
			{serLIN, 19200, "gap-free", true, 2170},
			{serModbus, 460800, "gap-free", true, 90},
			{serSPDIF, 3072000, "captures", false, 0},
			{serIRNEC, 889, "gap-free", false, 0},
			{serEM4100, 1953, "gap-free", false, 0},
		} {
			name, _ := softProtoName(c.proto)
			plan, _ := softGapFreePlan(name, c.baud, false, softLines(c.proto))
			if got := SoftStreamMode(c.proto, c.baud, false); got != c.mode || plan.bits != c.bits || (c.bits && plan.period != c.period) {
				t.Errorf("%s at %d: %q bits %v period %d, want %q bits %v period %d", name, c.baud, got, plan.bits, plan.period, c.mode, c.bits, c.period)
			}
			if c.bits {
				feature, _, _, _ := decode.StreamTiming(name, float64(c.baud))
				perLine := 1 / decode.StreamBitsPeriod(plan.period)
				if n := feature / decode.StreamBitsPeriod(plan.period); n < 3.5 || float64(softLines(c.proto))*perLine > softBitsBudget || plan.wordRate() > decode.StreamBusWords {
					t.Errorf("%s at %d: %.2f periods a feature, %.3g samples a second a line, %.3g words a second", name, c.baud, n, perLine, plan.wordRate())
				}
			}
		}
	})
}

// A gap-free stream of digital words decodes LIN from 19.2 kBd to 1 MBd,
// arriving in pieces of any size, the same frames the line carried.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSoftStreamBitsToEvents(t *testing.T) {
	withBitsImage(t, func() {
		for _, baud := range []int{19200, 250000, 1000000} {
			name := softProtoNames[serLIN]
			plan, ok := softGapFreePlan(name, baud, false, 1)
			if !ok || !plan.bits {
				t.Fatalf("LIN at %d: not a digital gap-free stream", baud)
			}
			_, longest, quiet, _ := decode.StreamTiming(name, float64(baud))
			period := decode.StreamBitsPeriod(plan.period)
			frames := [][]int{}
			for i := range 20 {
				id := 0x10 + i%40
				bit := func(k int) int { return id >> k & 1 }
				pid := id | (bit(0)^bit(1)^bit(2)^bit(4))<<6 | (1^bit(1)^bit(3)^bit(4)^bit(5))<<7
				sum := pid
				for _, v := range []int{i, 0x80 + i} {
					if sum += v; sum > 0xff {
						sum -= 0xff
					}
				}
				frames = append(frames, []int{pid, i, 0x80 + i, ^sum & 0xff})
			}
			// 13 samples a period, so edges fall anywhere in one.
			raw := bitsWords(linLevels(13/period, float64(baud), frames), 13)
			d, _ := serialDecoder(serLIN)
			s := &softStream{
				p: SerialParams{Proto: serLIN, Baud: baud, Threshold: 128, HaveThr: true}, proto: name, decoder: d,
				mask: 0xff, gapFree: true, quiet: quiet, bits: true, trig: StreamManual,
				id: sramcapture.RecordIdentity{Epoch: softEpochBase | 1},
			}
			s.setTiming(plan)
			s.dec = decode.NewStreamDecoder(s.decodeFunc(), int(quiet/s.sampleS), int((longest+quiet)/s.sampleS))
			for at := 0; at < len(raw); {
				n := min(len(raw)-at, 4*(1+(at*7919)%3000))
				s.feedBits(raw[at : at+n])
				at += n
				s.emit(s.dec.Push(s.pendA, nil), s.ord)
				s.pendA = s.pendA[:0]
			}
			s.emit(s.dec.Flush(), s.ord)
			tr := decodedlines.New(serLIN+1, 1000)
			tr.Add(s.events)
			tr.Flush()
			if tr.Count() != len(frames) {
				t.Fatalf("LIN at %d (%d raw words, %.3g us): %d lines, want %d", baud, plan.period, period*1e6, tr.Count(), len(frames))
			}
			for i := 0; i < tr.Count(); i++ {
				l := tr.At(i)
				if l.Note != "BRK" || l.N != 4 || l.Errs != 0 || l.Vals[1] != uint32(i) || l.Vals[2] != uint32(0x80+i) {
					t.Fatalf("LIN at %d line %d: %q", baud, i, tr.Format(i))
				}
			}
		}
	})
}

// Leaving out a quiet line's digital words changes nothing decoded: the
// same frames at the same sample ordinals as every word gives, LIN with
// quiet stretches longer than Close and shorter, in pieces of any size;
// and the words left out are most of a quiet bus.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSoftStreamBitsQuietSkipped(t *testing.T) {
	withBitsImage(t, func() {
		const baud = 250000
		name := softProtoNames[serLIN]
		plan, _ := softGapFreePlan(name, baud, false, 1)
		_, longest, quiet, _ := decode.StreamTiming(name, float64(baud))
		period := decode.StreamBitsPeriod(plan.period)
		var levels []bool
		sr := 13 / period
		for i := range 12 {
			id := 0x10 + i
			bit := func(k int) int { return id >> k & 1 }
			pid := id | (bit(0)^bit(1)^bit(2)^bit(4))<<6 | (1^bit(1)^bit(3)^bit(4)^bit(5))<<7
			sum := pid
			for _, v := range []int{i, 0x80 + i} {
				if sum += v; sum > 0xff {
					sum -= 0xff
				}
			}
			l := linLevels(sr, baud, [][]int{{pid, i, 0x80 + i, ^sum & 0xff}})
			if i%3 == 1 {
				l = l[:len(l)*2/3] // a quiet stretch shorter than Close
			}
			levels = append(levels, l...)
		}
		raw := bitsWords(levels, 13)
		var decodedN [2]int
		run := func(skip bool) ([]sramcapture.DecodedEvent, *softStream) {
			k := 0
			if skip {
				k = 1
			}
			d, _ := serialDecoder(serLIN)
			s := &softStream{
				p: SerialParams{Proto: serLIN, Baud: baud, Threshold: 128, HaveThr: true}, proto: name, decoder: d,
				mask: 0xff, gapFree: true, quiet: quiet, bits: true, trig: StreamManual,
				id: sramcapture.RecordIdentity{Epoch: softEpochBase | 1},
			}
			s.setTiming(plan)
			f := s.decodeFunc()
			s.dec = decode.NewStreamDecoder(func(a, b []uint8) decode.Result { decodedN[k] += len(a); return f(a, b) }, int(quiet/s.sampleS), int((longest+quiet)/s.sampleS))
			lead := int(decode.StreamLead(name, baud) / s.sampleS)
			s.dec.SetLead(lead)
			s.leadWords = lead/16 + 2
			s.keepWords = int(quiet/s.sampleS)/16 + s.leadWords + 2
			e := &Engine{}
			for at := 0; at < len(raw); {
				n := min(len(raw)-at, 4*(1+(at*7919)%3000))
				if skip {
					e.feedBitsQuiet(s, raw[at:at+n])
				} else {
					s.feedBits(raw[at : at+n])
				}
				at += n
				e.softPush(s)
			}
			s.emit(s.dec.Flush(), s.ord)
			return s.events, s
		}
		all, _ := run(false)
		got, s := run(true)
		if len(all) == 0 || len(got) != len(all) {
			t.Fatalf("%d events with quiet words left out, %d with all", len(got), len(all))
		}
		for i := range all {
			if got[i] != all[i] {
				t.Fatalf("event %d: %+v, want %+v", i, got[i], all[i])
			}
		}
		if s.skipped != 0 && s.idleWords == 0 {
			t.Fatalf("skip state %d/%d", s.skipped, s.idleWords)
		}
		if decodedN[1]*4 > decodedN[0] {
			t.Fatalf("decoded %d samples with quiet words left out, %d with all", decodedN[1], decodedN[0])
		}
		t.Logf("%d words, %d events; samples decoded %d of %d", len(raw)/4, len(all), decodedN[1], decodedN[0])
	})
}
