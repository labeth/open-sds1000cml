// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"slices"
	"testing"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/decodedlines"
	"open-sds/app/internal/sramcapture"
)

// linLevels is a LIN line (idle high) at baud: per frame a 13-bit break, the
// delimiter, the sync, the identifier and two data bytes with a checksum
// byte (not checked here), each frame followed by idle; levels at rate sr.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func linLevels(sr, baud float64, frames [][]int) []bool {
	var out []bool
	t := 0.0
	add := func(v bool, d float64) {
		t += d
		for float64(len(out)) < t*sr {
			out = append(out, v)
		}
	}
	char := func(v int) {
		add(false, 1/baud)
		for i := 0; i < 8; i++ {
			add(v>>i&1 == 1, 1/baud)
		}
		add(true, 1/baud)
	}
	add(true, 20e-3)
	for _, f := range frames {
		add(false, 13/baud)
		add(true, 1/baud)
		char(0x55)
		for _, v := range f {
			char(v)
		}
		add(true, 5e-3)
	}
	add(true, 40e-3)
	return out
}

// peakWords reduces levels (m per bucket) to the stream image's peak words
// ({max2,max1,min2,min1}), the line on both channels.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func peakWords(levels []bool, m int) []byte {
	var raw []byte
	for k := 0; k+m <= len(levels); k += m {
		lo, hi := uint8(200), uint8(56)
		for _, v := range levels[k : k+m] {
			c := uint8(56)
			if v {
				c = 200
			}
			lo, hi = min(lo, c), max(hi, c)
		}
		raw = append(raw, lo, lo, hi, hi)
	}
	return raw
}

// A gap-free software stream turns the stream's peak words into frames as
// they arrive in pieces of any size, and on the stop value ends the
// transcript at the frame that holds it, with the trigger marked there.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSoftStreamPeakWordsToEvents(t *testing.T) {
	const baud = 19200
	name := softProtoNames[serLIN]
	log, ok := softGapFreeLog(name, baud, false)
	if !ok {
		t.Fatal("LIN is not gap-free")
	}
	feature, longest, quiet, _ := decode.StreamTiming(name, baud)
	_ = feature
	bucket := float64(uint64(1)<<log) * 2e-9
	frames := [][]int{}
	for i := 0; i < 30; i++ {
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
	raw := peakWords(linLevels(8/bucket, baud, frames), 8)
	for _, stop := range []bool{false, true} {
		d, _ := serialDecoder(serLIN)
		s := &softStream{p: SerialParams{Proto: serLIN, Baud: baud, Threshold: 128, HaveThr: true}, proto: name, decoder: d,
			mask: 0xff, gapFree: true, quiet: quiet, log: uint8(log), trig: StreamManual, id: sramcapture.RecordIdentity{Epoch: softEpochBase | 1}}
		if stop {
			s.trig, s.value = StreamOnValue, 0x80+20 // frame 20's second data byte
		}
		s.sampleS = bucket / 2
		s.ordStep = float64(uint64(1) << (log - 1))
		s.dec = decode.NewStreamDecoder(s.decodeFunc(), int(quiet/s.sampleS), int((longest+quiet)/s.sampleS))
		stopped := false
		for at := 0; at < len(raw) && !stopped; {
			n := min(len(raw)-at, 4*(1+(at*7919)%3000))
			s.feedPeak(raw[at:at+n], nil)
			at += n
			frames := s.dec.Push(s.pendA, nil)
			s.pendA = s.pendA[:0]
			stopped = s.emit(frames, s.ord)
		}
		if !stopped {
			s.emit(s.dec.Flush(), s.ord)
		}
		tr := decodedlines.New(serLIN+1, 1000)
		tr.Add(s.events)
		tr.Flush()
		want := len(frames)
		if stop {
			for want = 0; !slices.Contains(frames[want], 0x80+20); want++ {
			}
			want++
		}
		if tr.Count() != want || stopped != stop {
			for i := 0; i < min(8, tr.Count()); i++ {
				t.Log(tr.Format(i))
			}
			t.Fatalf("stop %v: %d lines (want %d), stopped %v", stop, tr.Count(), want, stopped)
		}
		for i := 0; i < tr.Count(); i++ {
			l := tr.At(i)
			if l.Note != "BRK" || l.N != 4 || l.Errs != 0 || l.Vals[1] != uint32(i) || l.Vals[2] != uint32(0x80+i) {
				t.Fatalf("line %d: %q %X", i, tr.Format(i), l.Vals[:l.N])
			}
		}
		if stop {
			last := s.events[len(s.events)-1]
			if last.Kind != sramcapture.EventTrigger || last.Sample != s.stopFirst || s.stopLast <= s.stopFirst {
				t.Fatalf("the stop: %+v first %d last %d", last, s.stopFirst, s.stopLast)
			}
		}
		for i := 1; i < len(s.events); i++ {
			if s.events[i].Sequence != s.events[i-1].Sequence+1 || s.events[i].Epoch != s.id.Epoch {
				t.Fatalf("event %d: sequence %d after %d", i, s.events[i].Sequence, s.events[i-1].Sequence)
			}
		}
	}
}

// A stop value the symbol trigger cannot match at any unit (an IR code's
// address) is matched at its place in the frame, where the transcript read it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSoftStreamValuePatternPlaces(t *testing.T) {
	for _, c := range []struct {
		proto, baud, value, unit int
		bits                     int
	}{{serIRSIRC, 1667, 0x01, 1, 12}, {serIRRC5, 562, 0x0C, 1, 0}, {serDALI, 1200, 0x80, 1, 0}, {serWiegand, 1000, 0x1234, 1, 26}} {
		s := &softStream{p: SerialParams{Proto: c.proto, Baud: c.baud, Bits: c.bits, ChA: 0, ChB: 1, Threshold: 128, HaveThr: true,
			ThresholdB: 128, HaveThrB: true}, value: c.value, mask: unitMask(SerialParams{Proto: c.proto})}
		if !s.symbolPlans(s.valuePattern(-1)) && !s.symbolPlans(s.valuePattern(c.unit)) {
			t.Errorf("protocol %d: %X matches neither at any unit nor as unit %d", c.proto, c.value, c.unit)
		}
	}
}

// A record the symbol trigger did not fire on keeps every frame, though its
// decode read the stop value: the stream goes on, and says it read it.
// TRLC-LINKS: REQ-SDS-260
func TestSoftStreamEmitReadsOnWhereTheFPGADidNotFire(t *testing.T) {
	frame := func(i0 uint64, v int) decode.StreamFrame {
		return decode.StreamFrame{I0: i0, I1: i0 + 10, Spans: []decode.StreamSpan{{Kind: "data", Val: v, I0: i0, I1: i0 + 10}}}
	}
	frames := []decode.StreamFrame{frame(0, 1), frame(20, 6), frame(40, 2)}
	s := &softStream{trig: StreamOnValue, value: 6, mask: 0xff}
	s.noStop = true
	if s.emit(frames, func(x uint64) uint64 { return x }) || !s.valueRead {
		t.Fatalf("no stop: stopped or did not read the value")
	}
	ends := 0
	for _, ev := range s.events {
		if ev.Kind == sramcapture.EventEnd {
			ends++
		}
		if ev.Kind == sramcapture.EventTrigger {
			t.Fatalf("a trigger mark where the FPGA did not fire")
		}
	}
	if ends != 3 {
		t.Fatalf("%d frames kept, want 3", ends)
	}
	s.noStop = false
	if !s.emit(frames, func(x uint64) uint64 { return x }) || s.stopUnit != 0 {
		t.Fatalf("the FPGA fired: no stop")
	}
}

// An IR code's command stops a stream where it came, the second unit of
// its frame: the symbol trigger reads a lone value as the address.
// TRLC-LINKS: REQ-SDS-260
func TestSoftStreamStopPatternAtItsPlace(t *testing.T) {
	s := &softStream{p: SerialParams{Proto: serIRMP, Baud: 1818, Bits: decode.IRSamsung32, Threshold: 128, HaveThr: true},
		proto: "irmp", value: 0xFD02, mask: 0xffff, stopUnit: 1}
	pat := s.stopPattern()
	if len(pat) != 3 || pat[0].Kind != patternStart || pat[1].Mask != 0 || pat[2].Value != 0xFD02 || pat[2].Mask != 0xffff {
		t.Fatalf("Samsung32 command FD02: %+v", pat)
	}
	s.value, s.stopUnit = 0x0707, 0 // the address: unit 0
	if pat := s.stopPattern(); len(pat) != 2 || pat[1].Value != 0x0707 {
		t.Fatalf("Samsung32 address 0707: %+v", pat)
	}
	u := &softStream{p: SerialParams{Proto: serLIN, Baud: 19200, Threshold: 128, HaveThr: true}, proto: "lin", value: 0x55,
		mask: 0xff, stopUnit: 3}
	if pat := u.stopPattern(); len(pat) != 1 { // LIN: the value at any unit
		t.Fatalf("LIN 55: %+v", pat)
	}
}

// Which buses stream gap-free from the continuous stream and which as
// captures, at their usual rates (the bench sources' and the panel's).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSoftStreamModes(t *testing.T) {
	for proto, want := range map[int]string{serLIN: "gap-free", serDMX: "gap-free", serModbus: "gap-free",
		serOneWire: "gap-free", serDHT: "gap-free", serCEC: "gap-free", serJ1850: "gap-free", serIRNEC: "gap-free",
		serIRSIRC: "gap-free", serIRRC5: "gap-free", serIRRC6: "gap-free", serDALI: "gap-free", serPS2: "gap-free",
		serWiegand: "gap-free", serUSBPD: "captures", serSPDIF: "captures", serSWD: "captures", serMDIO: "captures",
		serWS281x: "captures", serUART: "", serMIDI: "", serUSB: "",
		serQuad: "gap-free", serStepper: "gap-free", serPWM: "captures", serRCEncode: "gap-free", serOOK: "gap-free",
		serMiller: "captures", serMorse: "gap-free", serDCF77: "gap-free", serEM4100: "gap-free", serEM4305: "gap-free",
		serT55xx: "gap-free", serSWIM: "captures", serSDQ: "captures", serPJDL: "gap-free", serDSI: "gap-free", serQi: "gap-free",
		serSDCard: "captures", serPDI: "captures", serCJTAG: "captures", serMaple: "captures", serCaliper: "gap-free", serIRMP: "gap-free"} {
		baud := map[int]int{serLIN: 19200, serDMX: 250000, serModbus: 19200, serOneWire: 16300, serDHT: 20000,
			serCEC: 417, serJ1850: 10400, serIRNEC: 889, serIRSIRC: 1667, serIRRC5: 562, serIRRC6: 1126, serDALI: 1200,
			serPS2: 16700, serWiegand: 1000, serUSBPD: 300000, serSPDIF: 3072000, serSWD: 1000000, serMDIO: 1000000,
			serWS281x: 800000, serQuad: 10000, serStepper: 5000, serPWM: 10000, serRCEncode: 2857, serOOK: 1024, serMiller: 106007,
			serMorse: 500, serDCF77: 333, serEM4100: 1953, serEM4305: 125000, serT55xx: 125000, serSWIM: 363636, serSDQ: 98425,
			serPJDL: 22727, serDSI: 600, serQi: 2000, serSDCard: 250000, serPDI: 250000, serCJTAG: 250000, serMaple: 1333333,
			serCaliper: 12500, serIRMP: 1786}[proto]
		if got := SoftStreamMode(proto, baud, false); got != want {
			t.Errorf("protocol %d at %d: %q, want %q", proto, baud, got, want)
		}
	}
	// Modbus streams gap-free to 230,400 Bd (4.3 us bits at 2^9), finer
	// decimations only for the features 2^11 does not resolve.
	for _, c := range []struct {
		baud int
		mode string
		log  int
	}{{19200, "gap-free", 12}, {57600, "gap-free", 11}, {115200, "gap-free", 10}, {230400, "gap-free", 9}, {460800, "captures", 0}} {
		log, _ := softGapFreeLog("modbus", c.baud, false)
		if got := SoftStreamMode(serModbus, c.baud, false); got != c.mode || log != c.log {
			t.Errorf("Modbus at %d Bd: %q at 2^%d, want %q at 2^%d", c.baud, got, log, c.mode, c.log)
		}
	}
	if log, _ := softGapFreeLog("dmx512", 250000, false); log != 9 {
		t.Errorf("DMX512 at 2^%d, want 2^9", log)
	}
	if got := SoftStreamMode(serOneWire, 160000, true); got != "captures" {
		t.Errorf("1-Wire overdrive: %q", got)
	}
	// The captures' record rates: eight samples in the shortest feature.
	for _, c := range []struct {
		feature float64
		log     uint8
	}{{163e-9, 0}, {500e-9, 4}, {1.67e-6, 6}, {0.42e-6, 4}} {
		if got := softCaptureLog(c.feature); got != c.log || (got != 0 && float64(uint64(1)<<got)*2e-9*8 > c.feature) {
			t.Errorf("feature %g: log %d, want %d", c.feature, got, c.log)
		}
	}
	if math.Abs(float64(softEpochBase&0x3fffffff)) != 0 {
		t.Fatal("software stream epochs overlap the fabric's")
	}
}
