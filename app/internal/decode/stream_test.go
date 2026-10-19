// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// streamCase is a protocol's frame generator and decoder for the stream
// tests: gen appends frame i (and its idle) on the lines.
type streamCase struct {
	name  string
	baud  float64
	gen   func(l twoLine, i int, rng *rand.Rand)
	dec   func(a, b []uint8, sampleS float64) Result
	lines int
	// idle is the lines' idle levels before and after the frames (nil:
	// high, or for the records low then high); frames how many (0: 60).
	idle   *[2]byte
	frames int
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func oneLine(l twoLine) { l.b.add(1, l.a.t-l.b.t) }

// streamCases are the gap-free protocols, each frame different and some
// spoiled (a bad checksum, parity or CRC).
var streamCases = []streamCase{
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "lin", baud: 19200, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		linFrame(l.a, 19200, 13, i%60, []int{i & 0xff, rng.Intn(256)}, false)
		l.a.idle(rng.Float64() * 3e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeLIN(a, s, LINCfg{Baud: 19200, Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "dmx512", baud: 250000, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		dmxPacket(l.a, 100+rng.Float64()*50, 12, 0, []int{i & 0xff, rng.Intn(256), 0x55})
		l.a.idle(rng.Float64() * 1e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeDMX512(a, s, DMXCfg{Baud: 250000, Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "modbus", baud: 19200, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		modbusFrame(l.a, 19200, "even", 1, []int{1 + i%7, 3, 0, i & 0xff, 0, 1})
		l.a.idle(2e-3 + rng.Float64()*5e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeModbus(a, s, ModbusCfg{Baud: 19200, Parity: "even", Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "onewire", baud: 16300, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		owReset(l.a, false, true)
		owByte(l.a, 0xCC, false)
		owByte(l.a, i&0xff, false)
		l.a.idle(300e-6 + rng.Float64()*2e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeOneWire(a, s, OneWireCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "dht", baud: 20000, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		ck := -1
		if i%7 == 3 {
			ck = 0x11
		}
		dhtReading(l.a, 1000+rng.Float64()*17000, [4]int{i & 0xff, 0x8C, rng.Intn(256), 0x5F}, ck)
		l.a.idle(rng.Float64() * 5e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeDHT(a, s, DHTCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "cec", baud: 417, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		cecMessage(l.a, []int{0x40, i & 0xff, rng.Intn(256)}, []int{0, 0, 1})
		l.a.idle(rng.Float64() * 20e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeCEC(a, s, CECCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "j1850", baud: 10400, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		j1850Frame(l.a, []int{0x68, 0x6A, 0xF1, i & 0xff, rng.Intn(256)}, i%9 == 4)
		l.a.add(0, rng.Float64()*2e-3)
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeJ1850(a, s, J1850Cfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "irnec", baud: 889, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		ir := irLine{l.a, false, 0}
		necFrame(ir, i&0xff, -1, rng.Intn(256))
		if i%5 == 2 {
			ir.space(40e-3)
			necRepeat(ir)
		}
		ir.space(40e-3 + rng.Float64()*20e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeIRNEC(a, s, IRCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "irnec-carrier", baud: 889, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		ir := irLine{l.a, true, 38e3}
		necFrame(ir, i&0xff, -1, rng.Intn(256))
		ir.space(40e-3 + rng.Float64()*20e-3)
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeIRNEC(a, s, IRCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "irsirc", baud: 1667, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		ir := irLine{l.a, false, 0}
		sircFrame(ir, []int{12, 15, 20}[i%3], i&0x7f, rng.Intn(32), rng.Intn(256))
		ir.space(20e-3 + rng.Float64()*20e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeIRSIRC(a, s, IRCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "irrc5", baud: 562, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		ir := irLine{l.a, false, 0}
		rc5Frame(ir, i&1, i&0x1f, rng.Intn(64))
		ir.space(30e-3 + rng.Float64()*20e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeIRRC5(a, s, IRCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "irrc6", baud: 1126, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		ir := irLine{l.a, false, 0}
		rc6Frame(ir, 0, i&1, []int{i & 0xff, rng.Intn(256)})
		ir.space(30e-3 + rng.Float64()*20e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeIRRC6(a, s, IRCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "dali", baud: 1200, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		daliFrame(l.a, []int{16, 8, 24}[i%3], (i*2654435761)&0xffffff>>uint(8*(2-i%3)), 10e-3+rng.Float64()*10e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeDALI(a, s, DALICfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "ps2", baud: 12500, lines: 2, gen: func(l twoLine, i int, rng *rand.Rand) {
		ps2Frame(l, i&0xff, false, i%11 == 5, 80e-6, 200e-6+rng.Float64()*1e-3)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodePS2(a, b, s, ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "wiegand", baud: 500, lines: 2, gen: func(l twoLine, i int, rng *rand.Rand) {
		wiegandFrame(l, wiegandBits([]int{26, 34}[i%2], i&0xff, rng.Intn(65536)), 2e-3)
		l.add(1, 1, rng.Float64()*20e-3)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeWiegand(a, b, s, ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "midi", baud: 31250, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		for _, v := range []int{0x90 | i&15, i & 0x7f, rng.Intn(128)} {
			tlUART(l.a, v, 31250, "none", 1)
		}
		l.a.idle(1e-3 + rng.Float64()*3e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeMIDI(a, s, MIDICfg{Baud: 31250, Threshold: 128, HaveThr: true})
	}},
}

// captureCases are the buses too fast for the gap-free stream, streamed as
// back-to-back records.
var captureCases = []streamCase{
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "usbpd", baud: 300000, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		pdMessage(l.a, pdSOP, 0x1161+i&0xf, []uint32{uint32(i) * 0x01010101}, i%7 == 3)
		l.a.add(l.a.bits[len(l.a.bits)-1], rng.Float64()*200e-6)
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeUSBPD(a, s, USBPDCfg{Baud: 300000, Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "ws281x", baud: 800000, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		wsFrame(l.a, 800e3, []int{i & 0xff, rng.Intn(256), 0x5A})
		l.a.add(0, rng.Float64()*100e-6)
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeWS281x(a, s, WS281xCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "swd", baud: 1000000, lines: 2, gen: func(l twoLine, i int, rng *rand.Rand) {
		s := &swdLine{t: l, per: 1e-6}
		s.transfer(i%2 == 0, true, 4*(i%4), 1, uint32(i)*0x01020304, i%9 == 4, 2+rng.Intn(8))
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeSWD(a, b, s, ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "mdio", baud: 1000000, lines: 2, gen: func(l twoLine, i int, rng *rand.Rand) {
		mdioFrame(l, 1e-6, 32, 1, 1+i%2, i&31, (i/3)&31, (i*0x1357)&0xffff, 0)
		l.add(1, 1, rng.Float64()*20e-6)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeMDIO(a, b, s, ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
	{name: "spdif", baud: 3072000, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		spdifStream(l.a, 48e3, i, [][2]int{{(i * 0x1357) & 0xffffff, 0xFFFFF0 - i}})
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeSPDIF(a, s, SPDIFCfg{Threshold: 128, HaveThr: true})
	}},
}

// TestStreamRecordCaptures decodes the fast buses from records cut out of
// a long signal at random: every frame a record gives is the frame the
// whole signal holds there (none made up from a cut frame), and most of the
// frames inside the records come out.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestStreamRecordCaptures(t *testing.T) {
	cases := captureCases
	for _, c := range append(streamCases, moreStreamCases...) {
		if feature, _, _, _ := StreamTiming(strings.TrimSuffix(c.name, "-carrier"), c.baud); !gapFree(feature) {
			cases = append(cases, c) // too fast for the gap-free stream
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(7))
			feature, longest, quiet, ok := StreamTiming(c.name, c.baud)
			if !ok {
				t.Fatal("no timing")
			}
			if gapFree(feature) {
				t.Fatalf("%s fits the gap-free stream", c.name)
			}
			pitch := feature / 8
			sr := 1 / pitch
			if c.name == "spdif" {
				sr = 500e6
			}
			l := newTwoLine(sr)
			if c.idle != nil {
				l.add(c.idle[0], c.idle[1], 50e-6)
			} else {
				l.add(0, 1, 50e-6)
			}
			for i := 0; l.a.t < 30e-3*float64(map[string]int{"spdif": 1}[c.name]+1)/2 || i < 40; i++ {
				c.gen(l, i, rng)
			}
			ss := 1 / sr
			dec := func(a, b []uint8) Result { return c.dec(a, b, ss) }
			whole := NewStreamDecoder(dec, int(quiet/ss), 1<<40)
			whole.SetSubframes(c.name == "spdif")
			truth := append(whole.Push(l.a.codes(), l.b.codes()), whole.Flush()...)
			at := map[uint64]string{}
			for _, f := range truth {
				at[f.I0] = frameKey(f)
			}
			A, B := l.a.codes(), l.b.codes()
			words := max(16384, int(8*quiet/ss), recordCut(c.name, 2*longest, ss)) // as the engine sizes a record
			if c.name == "spdif" {
				words = 2 * 65536 // a full-rate record: two samples a word
			}
			got, inside := 0, 0
			for pos := 0; pos+words < len(A); pos += words + rng.Intn(4*words) {
				var b []uint8
				if c.lines == 2 {
					b = B[pos : pos+words]
				}
				record := StreamRecord // as the engine reads a record
				if StreamRecordAfterIdle(c.name) {
					record = StreamRecordFromIdle
				}
				for _, f := range record(A[pos:pos+words], b, dec, int(quiet/ss), int(StreamRecordHead(c.name, feature, longest)/ss), recordCut(c.name, longest, ss), c.name == "spdif") {
					want, ok := "", false
					for d := uint64(0); d <= 2 && !ok; d++ { // edges a sample apart
						if want, ok = at[uint64(pos)+f.I0+d]; !ok && uint64(pos)+f.I0 >= d {
							want, ok = at[uint64(pos)+f.I0-d]
						}
					}
					if !ok || want != frameKey(f) {
						near := ""
						for _, tf := range truth {
							if tf.I1+2000 > uint64(pos)+f.I0 && tf.I0 < uint64(pos)+f.I1+2000 {
								near += fmt.Sprintf(" [%d..%d %q]", int64(tf.I0)-int64(pos), int64(tf.I1)-int64(pos), frameKey(tf))
							}
						}
						t.Fatalf("record at %d: frame at %d..%d %q, the whole signal %q (%v); near:%s", pos, f.I0, f.I1, frameKey(f), want, ok, near)
					}
					got++
				}
				for _, f := range truth {
					if f.I0 >= uint64(pos) && f.I1 < uint64(pos+words) {
						inside++
					}
				}
			}
			if inside == 0 || float64(got) < 0.5*float64(inside) {
				t.Fatalf("%d frames from records holding %d", got, inside)
			}
			t.Logf("%d of the %d frames inside the records", got, inside)
		})
	}
}

// recordCut is StreamRecord's cutN for a protocol, as the engine passes it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
func recordCut(proto string, longest, ss float64) int {
	if StreamEdgeFramed(proto) {
		return int(longest / ss)
	}
	return 0
}

// gapFree reports a feature the gap-free stream resolves.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func gapFree(feature float64) bool {
	_, ok := StreamBucketLog(feature)
	return ok
}

// peakStream reduces fine samples (m per bucket) to the gap-free stream's
// samples: each bucket's extremes, ordered to continue from the sample
// before them as the engine orders a peak word (orderEnvelope), with a
// little converter noise.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func peakStream(codes []uint8, m int, rng *rand.Rand) []uint8 {
	out := make([]uint8, 0, 2*len(codes)/m+2)
	noise := func(v uint8) uint8 { return uint8(int(v) + rng.Intn(5) - 2) }
	for k := 0; k+m <= len(codes); k += m {
		lo, hi := codes[k], codes[k]
		for _, v := range codes[k : k+m] {
			lo, hi = min(lo, v), max(hi, v)
		}
		lo, hi = noise(lo), noise(hi)
		if len(out) > 0 {
			prev := int(out[len(out)-1])
			if abs(int(hi)-prev) < abs(int(lo)-prev) {
				lo, hi = hi, lo
			}
		}
		out = append(out, lo, hi)
	}
	return out
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// frameKey is a frame's units and errors, the stream's transcript content.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func frameKey(f StreamFrame) string {
	var b strings.Builder
	for _, s := range f.Spans {
		switch s.Kind {
		case "data":
			fmt.Fprintf(&b, "%X ", s.Val)
		case "frame-error", "parity-error", "gap":
			b.WriteString("! ")
		}
	}
	return b.String()
}

// TestStreamDecoderGapFree decodes each gap-free protocol's frames from the
// stream's peak samples arriving in chunks of every size: every frame comes
// out once, as the whole full-rate signal decodes it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestStreamDecoderGapFree(t *testing.T) {
	for _, c := range append(streamCases, moreStreamCases...) {
		t.Run(c.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(len(c.name))))
			name := strings.TrimSuffix(c.name, "-carrier")
			feature, longest, quiet, ok := StreamTiming(name, c.baud)
			if !ok {
				t.Fatalf("no stream timing for %s", name)
			}
			log, ok := StreamBucketLog(feature)
			if !ok {
				return // streamed as records: TestStreamRecordCaptures
			}
			bucket := float64(uint64(1)<<log) * 2e-9
			const m = 8
			fine := newTwoLine(m / bucket)
			idle := [2]byte{1, 1}
			if name == "j1850" || c.name == "irnec-carrier" {
				idle[0] = 0
			}
			if c.idle != nil {
				idle = *c.idle
			}
			fine.add(idle[0], idle[1], quiet+2e-3)
			frames := 60
			if c.frames > 0 {
				frames = c.frames
			}
			for i := 0; i < frames; i++ {
				c.gen(fine, i, rng)
			}
			fine.add(idle[0], idle[1], 2*quiet+longest)
			// What the full-rate signal decodes to, frame by frame.
			sampleS := 1 / fine.a.sr
			whole := NewStreamDecoder(func(a, b []uint8) Result { return c.dec(a, b, sampleS) }, int(quiet/sampleS), 1<<40)
			truth := whole.Push(fine.a.codes(), fine.b.codes())
			truth = append(truth, whole.Flush()...)
			if len(truth) < frames {
				t.Fatalf("the whole signal decodes %d frames of %d", len(truth), frames)
			}
			a, b := peakStream(fine.a.codes(), m, rng), peakStream(fine.b.codes(), m, rng)
			ss := bucket / 2
			coarse := NewStreamDecoder(func(a, b []uint8) Result { return c.dec(a, b, ss) }, int(quiet/ss), 1<<40)
			cf := append(coarse.Push(a, b), coarse.Flush()...)
			for k := 0; k < min(len(cf), len(truth)); k++ {
				if frameKey(cf[k]) != frameKey(truth[k]) {
					t.Errorf("the peak samples whole: frame %d %q, want %q (%+v)", k, frameKey(cf[k]), frameKey(truth[k]), cf[k].Spans)
					break
				}
			}
			for _, sizes := range [][2]int{{1, 64}, {500, 5000}, {5000, 60000}} {
				d := NewStreamDecoder(func(a, b []uint8) Result { return c.dec(a, b, ss) }, int(quiet/ss), int((longest+quiet)/ss))
				d.SetLead(int(StreamLead(name, c.baud) / ss))
				var got []StreamFrame
				for at := 0; at < len(a); {
					n := min(len(a)-at, sizes[0]+rng.Intn(sizes[1]-sizes[0]+1))
					var bl []uint8
					if c.lines == 2 {
						bl = b[at : at+n]
					}
					got = append(got, d.Push(a[at:at+n], bl)...)
					at += n
					if sizes[0] == 1 && at > len(a)/200 {
						sizes = [2]int{2000, 20000} // tiny chunks for a while, then on
					}
				}
				got = append(got, d.Flush()...)
				if len(got) != len(truth) {
					t.Errorf("chunks %v: %d frames, the whole signal %d", sizes, len(got), len(truth))
				}
				for k := 0; k < min(len(got), len(truth)); k++ {
					if frameKey(got[k]) != frameKey(truth[k]) {
						t.Errorf("chunks %v: frame %d %q, want %q", sizes, k, frameKey(got[k]), frameKey(truth[k]))
						if testing.Verbose() {
							for j := max(0, k-1); j < min(len(got), k+3); j++ {
								t.Logf("got %d: %d..%d %+v", j, got[j].I0, got[j].I1, got[j].Spans)
							}
							for j := max(0, k-1); j < min(len(truth), k+3); j++ {
								t.Logf("want %d: %d..%d %+v", j, uint64(float64(truth[j].I0)*sampleS/ss), uint64(float64(truth[j].I1)*sampleS/ss), truth[j].Spans)
							}
						}
						break
					}
					if k > 0 && got[k].I0 <= got[k-1].I1 {
						t.Errorf("frame %d starts at %d, before the last ended (%d)", k, got[k].I0, got[k-1].I1)
					}
				}
			}
		})
	}
}
