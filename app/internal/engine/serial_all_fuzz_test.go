// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"open-sds/app/internal/sramcapture"
)

// fuzzSampleS are the sample times a decoder meets: the scope's rates, the
// gap-free stream's buckets, and the hostile ones a corrupt setting brings.
var fuzzSampleS = []float64{2e-9, 4e-9, 1e-8, 1.024e-6 / 2, 2e-6, 1e-4, 0, -1, math.NaN(), math.Inf(1), 1e30}

// fuzzSerialParams builds every protocol's settings from the fuzzer's
// bytes: in-range values most of the time, hostile ones the rest.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func fuzzSerialParams(proto uint8, knobs []byte, pat []byte) SerialParams {
	k := make([]byte, 24)
	copy(k, knobs)
	u32 := func(i int) uint32 { return binary.LittleEndian.Uint32(k[i:]) }
	bauds := []int{0, 1, 50, 1200, 9600, 19200, 115200, 250000, 1000000, 1500000, 12000000, 62500000}
	p := SerialParams{
		Proto:     int(proto) % (SerialProtocols + 2),
		ChA:       int(k[0]%3) - (int(k[0]>>7) & 1),
		ChB:       int(k[1] % 3),
		Baud:      bauds[int(k[2])%len(bauds)],
		Bits:      int(int8(k[3])),
		Parity:    []string{"", "none", "even", "odd", "mark", "x"}[int(k[4])%6],
		CPOL:      k[5]&1 != 0,
		CPHA:      k[5]&2 != 0,
		MSB:       k[5]&4 != 0,
		IEEE:      k[5]&8 != 0,
		Inverted:  k[5]&16 != 0,
		Overdrive: k[5]&32 != 0,
		HaveThr:   k[5]&64 != 0,
		HaveThrB:  k[5]&128 != 0,
		Threshold: float64(int(k[6])) - 8,
		Addr:      int(int8(k[7])),
		RW:        int(k[8]%4) - 1,
		Nibbles:   int(k[9] % 12),
		TickNs:    float64(k[10]),
	}
	if p.ChB == p.ChA && k[11]&1 == 0 {
		p.ChB = 1 - p.ChA
	}
	if k[11]&2 != 0 {
		p.Baud = int(int32(u32(12)))
	}
	p.ThresholdB = float64(k[16])
	if k[11]&4 != 0 {
		p.Threshold = math.Float64frombits(uint64(u32(12))<<32 | uint64(u32(16)))
	}
	// The pattern: three bytes a token (kind, value, mask), with a wide
	// value now and then.
	for i := 0; i+3 <= len(pat) && len(p.Pattern) < 40; i += 3 {
		e := PatternElem{Kind: int(pat[i] % 6), Value: int(pat[i+1]), Mask: int(pat[i+2])}
		if pat[i]&0x80 != 0 {
			e.Value = e.Value<<23 | e.Value<<8 | int(pat[i+2])
			e.Mask = int(int32(uint32(pat[i+2]) * 0x01010101))
		}
		p.Pattern = append(p.Pattern, e)
	}
	if k[11]&8 != 0 {
		for _, e := range p.Pattern {
			p.Bytes = append(p.Bytes, e.Value)
		}
		p.Pattern = nil
	}
	return p
}

// TestSymbolPlansWhatTheBlockTakes sweeps every symbol protocol's bit rate
// over the range a user can enter: a plan is one the FPGA block accepts
// (a unit of eight receiver clocks at least), else none - never an arm
// refused on every capture.
// TRLC-LINKS: REQ-SDS-013
func TestSymbolPlansWhatTheBlockTakes(t *testing.T) {
	for proto := 1; proto < SerialProtocols; proto++ {
		if !symbolProtocol(proto) {
			continue
		}
		for baud := 100.0; baud < 1e8; baud *= 1.25 {
			for _, bits := range []int{0, 3, 12, 21, 24, 26, 34, 38, 70} {
				p := SerialParams{Proto: proto, ChA: 0, ChB: 1, Baud: int(baud), Bits: bits, HaveThr: true, Threshold: 128}
				if c := planHardwareSymbol(p); c.Enabled && c.Validate() != nil {
					t.Fatalf("proto %d at %d Bd, bits %d: planned %+v, which the block refuses: %v", proto, p.Baud, bits, c, c.Validate())
				}
			}
		}
	}
}

// FuzzSerialAllProtocols drives every protocol's symbol-trigger planner,
// decoder, matcher, software qualifier and stream planning with any
// settings and any lines: none may panic or hang, a decoded span lies in
// the record, and a planned trigger is one the FPGA block accepts.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func FuzzSerialAllProtocols(f *testing.F) {
	for proto := 1; proto < SerialProtocols; proto++ {
		f.Add(uint8(proto), []byte{0, 1, byte(proto % 12), 8, 0, 64, 128, 0xff, 2, 0, 0, 0}, []byte{1, 0, 0, 2, 0xa5, 0xff}, []byte{128, 255, 255, 0, 0, 255, 255, 255, 0, 0, 0}, uint8(0))
		f.Add(uint8(proto), []byte{1, 0, 4, 0, 0, 0x41, 120, 0, 0, 0, 0, 1}, []byte{2, 0, 0, 0x82, 0xff, 0xff}, []byte{0, 0, 0, 0, 255, 255, 255, 255}, uint8(3))
	}
	f.Fuzz(func(t *testing.T, proto uint8, knobs, pat, line []byte, rate uint8) {
		p := fuzzSerialParams(proto, knobs, pat)
		// The lines: each byte a run of a level, so short inputs make long
		// records with real edges.
		var a, b []uint8
		for i, v := range line {
			n := 1 + int(v&0x3f)*int(1+v>>6)
			lvA, lvB := uint8(255*(v&1)), uint8(255*(v>>1&1))
			if i%5 == 4 {
				lvA = v // a level between the rails now and then
			}
			for j := 0; j < n && len(a) < 1<<16; j++ {
				a, b = append(a, lvA), append(b, lvB)
			}
		}
		sampleS := fuzzSampleS[int(rate)%len(fuzzSampleS)]
		t0 := time.Now()
		c := planHardwareSymbol(p)
		if c.Enabled {
			if c.Channel > 1 || c.Mode > sramcapture.SymbolClocked || c.LeadLen > 4 || c.Prescale > 7 || c.UnitQ8 < 2048 || c.UnitQ8 >= 1<<28 ||
				c.FrameUnits > 63 || c.Period > 63 || c.Phase0 > 63 || (c.Period != 0 && c.Phase0 >= c.Period) || c.K > 1023 {
				t.Fatalf("proto %d planned a trigger the block refuses: %+v (params %+v)", p.Proto, c, p)
			}
		}
		_ = SoftStreamMode(p.Proto, p.Baud, p.Overdrive)
		_ = unitMask(p)
		if d, ok := serialDecoder(p.Proto); ok {
			for _, bl := range [][]uint8{b, nil} {
				r := d.Decode(a, bl, sampleS, p)
				for _, s := range r.Spans {
					if s.I0 < 0 || s.I1 < s.I0 || s.I0 > len(a) || s.I1 > len(a)+1 {
						t.Fatalf("%s: span %+v outside the record of %d", d.Name, s, len(a))
					}
				}
				if d.Match != nil {
					_, _ = d.Match(r.Spans, p)
				}
			}
		}
		e := &Engine{}
		e.SetSerialParams(p)
		f := &Frame{C1: a, C2: b}
		if p.ChA == 1 {
			f.C1, f.C2 = b, a
		}
		_, _ = e.serialQualify(f, len(a), sampleS)
		if el := time.Since(t0); el > 5*time.Second {
			t.Fatalf("proto %d took %v on %d samples", p.Proto, el, len(a))
		}
	})
}
