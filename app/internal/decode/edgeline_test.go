// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// clocked128 is the stream tests' two-line settings.
var clocked128 = ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true}

// line128 is the stream tests' single-line settings.
var line128 = LineCfg{Threshold: 128, HaveThr: true}

// edgeStreamDecs are the stream cases' decoders over edges, each with its
// sample decoder's settings (streamCases, moreStreamCases, captureCases).
var edgeStreamDecs = map[string]func(a, b *EdgeLine, s float64) Result{
	"lin": func(a, b *EdgeLine, s float64) Result {
		return DecodeLINEdges(a, s, LINCfg{Baud: 19200, Threshold: 128, HaveThr: true})
	},
	"dmx512": func(a, b *EdgeLine, s float64) Result {
		return DecodeDMX512Edges(a, s, DMXCfg{Baud: 250000, Threshold: 128, HaveThr: true})
	},
	"modbus": func(a, b *EdgeLine, s float64) Result {
		return DecodeModbusEdges(a, s, ModbusCfg{Baud: 19200, Parity: "even", Threshold: 128, HaveThr: true})
	},
	"onewire": func(a, b *EdgeLine, s float64) Result {
		return DecodeOneWireEdges(a, s, OneWireCfg{Threshold: 128, HaveThr: true})
	},
	"dht": func(a, b *EdgeLine, s float64) Result {
		return DecodeDHTEdges(a, s, DHTCfg{Threshold: 128, HaveThr: true})
	},
	"cec": func(a, b *EdgeLine, s float64) Result {
		return DecodeCECEdges(a, s, CECCfg{Threshold: 128, HaveThr: true})
	},
	"j1850": func(a, b *EdgeLine, s float64) Result {
		return DecodeJ1850Edges(a, s, J1850Cfg{Threshold: 128, HaveThr: true})
	},
	"dali": func(a, b *EdgeLine, s float64) Result {
		return DecodeDALIEdges(a, s, DALICfg{Threshold: 128, HaveThr: true})
	},
	"ps2": func(a, b *EdgeLine, s float64) Result { return DecodePS2Edges(a, b, s, clocked128) },
	"wiegand": func(a, b *EdgeLine, s float64) Result {
		return DecodeWiegandEdges(a, b, s, clocked128)
	},
	"usbpd": func(a, b *EdgeLine, s float64) Result {
		return DecodeUSBPDEdges(a, s, USBPDCfg{Baud: 300000, Threshold: 128, HaveThr: true})
	},
	"ws281x": func(a, b *EdgeLine, s float64) Result {
		return DecodeWS281xEdges(a, s, WS281xCfg{Threshold: 128, HaveThr: true})
	},
	"swd":  func(a, b *EdgeLine, s float64) Result { return DecodeSWDEdges(a, b, s, clocked128) },
	"mdio": func(a, b *EdgeLine, s float64) Result { return DecodeMDIOEdges(a, b, s, clocked128) },
	"spdif": func(a, b *EdgeLine, s float64) Result {
		return DecodeSPDIFEdges(a, s, SPDIFCfg{Threshold: 128, HaveThr: true})
	},
	"quad":    func(a, b *EdgeLine, s float64) Result { return DecodeQuadratureEdges(a, b, s, clocked128) },
	"stepper": func(a, b *EdgeLine, s float64) Result { return DecodeStepperEdges(a, b, s, clocked128) },
	"rcencode": func(a, b *EdgeLine, s float64) Result {
		return DecodeRCEncodeEdges(a, s, LineCfg{Threshold: 128, HaveThr: true, Variant: 12})
	},
	"ook":   func(a, b *EdgeLine, s float64) Result { return DecodeOOKEdges(a, s, line128) },
	"morse": func(a, b *EdgeLine, s float64) Result { return DecodeMorseEdges(a, s, line128) },
	"dcf77": func(a, b *EdgeLine, s float64) Result {
		return DecodeDCF77Edges(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 1})
	},
	"pjdl": func(a, b *EdgeLine, s float64) Result {
		return DecodePJDLEdges(a, s, LineCfg{Threshold: 128, HaveThr: true, Variant: 1})
	},
	"dsi":     func(a, b *EdgeLine, s float64) Result { return DecodeDSIEdges(a, s, line128) },
	"qi":      func(a, b *EdgeLine, s float64) Result { return DecodeQiEdges(a, s, line128) },
	"caliper": func(a, b *EdgeLine, s float64) Result { return DecodeCaliperEdges(a, b, s, clocked128) },
	"pwm":     func(a, b *EdgeLine, s float64) Result { return DecodePWMEdges(a, s, line128) },
	"miller": func(a, b *EdgeLine, s float64) Result {
		return DecodeMillerEdges(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 106000})
	},
	"swim": func(a, b *EdgeLine, s float64) Result {
		return DecodeSWIMEdges(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 363636})
	},
	"sdq": func(a, b *EdgeLine, s float64) Result {
		return DecodeSDQEdges(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 98425})
	},
	"sdcard": func(a, b *EdgeLine, s float64) Result { return DecodeSDCardEdges(a, b, s, clocked128) },
	"cjtag":  func(a, b *EdgeLine, s float64) Result { return DecodeCJTAGEdges(a, b, s, clocked128) },
	"maple":  func(a, b *EdgeLine, s float64) Result { return DecodeMapleEdges(a, b, s, clocked128) },
	"pdi":    func(a, b *EdgeLine, s float64) Result { return DecodePDIEdges(a, b, s, clocked128) },
}

// edgeLineOf is a line of 0/1 levels as an EdgeLine, built a sample at a
// time.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func edgeLineOf(levels []byte) *EdgeLine {
	l := &EdgeLine{}
	for _, v := range levels {
		l.AppendLevel(v, 1)
	}
	return l
}

// codesOf is a line of 0/1 levels as samples, 0 or 255.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func codesOf(levels []byte) []uint8 {
	out := make([]uint8, len(levels))
	for i, v := range levels {
		out[i] = 255 * v
	}
	return out
}

// randomLevels is a random digital line: runs of random length about mean,
// sometimes a line resting at one level with seldom pulses.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func randomLevels(rng *rand.Rand, n int, mean float64, sparse bool) []byte {
	out := make([]byte, 0, n)
	lvl := byte(rng.Intn(2))
	for len(out) < n {
		run := 1 + int(rng.ExpFloat64()*mean)
		if sparse && lvl == 1 {
			run = 1 + rng.Intn(4)
		} else if sparse {
			run = 1 + rng.Intn(20*n+1)
		}
		for k := 0; k < run && len(out) < n; k++ {
			out = append(out, lvl)
		}
		lvl ^= 1
	}
	return out
}

// An EdgeLine's slicing is sliceChannel's of its samples, field for field,
// at every threshold: inside the band, at its edges, outside the codes and
// none; on busy lines, quiet ones, seldom pulsed ones and short ones.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestEdgeLineSliceMatchesSamples(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	thrs := []float64{128, 127.5, 0, -3, 1, 20, 25.5, 26, 100, 200, 229, 229.5, 230, 254.5, 255, 256, math.NaN()}
	for it := 0; it < 3000; it++ {
		n := []int{0, 1, 3, 5, 7, 8, 9, 30, 500, 5000}[rng.Intn(10)]
		lv := randomLevels(rng, n, []float64{1, 3, 40}[rng.Intn(3)], rng.Intn(4) == 0)
		codes, l := codesOf(lv), edgeLineOf(lv)
		for _, thr := range thrs {
			for _, have := range []bool{true, false} {
				want, got := sliceChannel(codes, thr, have), l.slice(thr, have)
				fast := got.codes == nil // sliced from the edges, not the samples
				want.codes, got.codes = nil, nil
				if len(want.edges) == 0 {
					want.edges = nil
				}
				if len(got.edges) == 0 {
					got.edges = nil
				}
				if !reflect.DeepEqual(fmt.Sprint(want), fmt.Sprint(got)) {
					t.Fatalf("n %d thr %v %v: %+v\nwant %+v", n, thr, have, got, want)
				}
				if want.ok && fast {
					w := sliceChannel(codes, thr, have)
					for k := 0; k < 40; k++ {
						x := rng.Float64()*float64(n+2) - 1
						if a, b := logicAt(&w, x), logicAt(&got, x); a != b {
							t.Fatalf("logicAt(%v) %d, want %d", x, b, a)
						}
					}
				}
			}
		}
	}
}

// AppendWords, AppendRange and Drop build the line the samples are.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestEdgeLineBuilds(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for it := 0; it < 2000; it++ {
		words := 1 + rng.Intn(40)
		raw := make([]byte, 4*words)
		var lv [2][]byte
		for i := 0; i < words; i++ {
			for ch := range 2 {
				var w uint16
				switch rng.Intn(4) {
				case 0:
					w = 0
				case 1:
					w = 0xffff
				default:
					w = uint16(rng.Intn(1 << 16))
				}
				raw[4*i+2*ch], raw[4*i+2*ch+1] = byte(w), byte(w>>8)
				for k := range 16 {
					lv[ch] = append(lv[ch], byte(w>>k&1))
				}
			}
		}
		for ch := range 2 {
			var l EdgeLine
			for at := 0; at < words; {
				m := 1 + rng.Intn(words-at)
				l.AppendWords(raw[4*at:4*(at+m)], ch)
				at += m
			}
			if !reflect.DeepEqual(l.Samples(), codesOf(lv[ch])) {
				t.Fatalf("words: line %d differs", ch)
			}
			// pieces of it, appended and dropped
			var r EdgeLine
			var want []byte
			for k := 0; k < 6; k++ {
				from := rng.Intn(l.N + 1)
				to := from + rng.Intn(l.N-from+1)
				r.AppendRange(&l, from, to)
				want = append(want, lv[ch][from:to]...)
				if rng.Intn(3) == 0 {
					d := rng.Intn(len(want) + 2)
					r.Drop(d)
					want = want[min(d, len(want)):]
				}
				if rng.Intn(4) == 0 {
					v, c := byte(rng.Intn(2)), rng.Intn(40)
					r.AppendLevel(v, c)
					for range c {
						want = append(want, v)
					}
				}
				if r.N != len(want) || !reflect.DeepEqual(r.Samples(), codesOf(want)) {
					t.Fatalf("pieces: %d samples, want %d", r.N, len(want))
				}
			}
		}
	}
}

// edgeStreamPeriod is the sample time the edge stream tests decode a case
// at: the digital stream's for its feature, or for a bus finer than that
// stream resolves, three and a half samples a feature.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func edgeStreamPeriod(tc streamCPUCase) float64 {
	if log, ok := StreamBitsLog(tc.feature); ok {
		return StreamBitsPeriod(log)
	}
	return tc.feature / 3.5
}

// streamBitsLines is about secs of the case's traffic as the digital
// stream's levels (0 or 1) a sample of ss; bursty as streamCPULines.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func streamBitsLines(tc streamCPUCase, seed int64, secs, ss float64, bursty bool) (a, b []byte) {
	rng := rand.New(rand.NewSource(seed))
	fine := newTwoLine(1 / ss)
	idle := [2]byte{1, 1}
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
	n := min(len(fine.a.bits), len(fine.b.bits))
	return fine.a.bits[:n], fine.b.bits[:n]
}

// edgeStreamRuns decodes the lines as a stream of samples and of edges,
// pushed in the same random pieces, and returns both streams' frames.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func edgeStreamRuns(tc streamCPUCase, la, lb []byte, ss float64, seed int64, edec func(a, b *EdgeLine, s float64) Result) (samples, edges []StreamFrame) {
	closeN, maxN, leadN := int(tc.quiet/ss), int((tc.longest+tc.quiet)/ss), int(tc.lead/ss)
	ds := NewStreamDecoder(func(x, y []uint8) Result { return tc.c.dec(x, y, ss) }, closeN, maxN)
	de := NewEdgeStreamDecoder(func(x, y *EdgeLine) Result { return edec(x, y, ss) }, closeN, maxN)
	for _, d := range []*StreamDecoder{ds, de} {
		d.SetLead(leadN)
		d.SetSubframes(tc.c.name == "spdif")
	}
	ca, cb := codesOf(la), codesOf(lb)
	ea, eb := edgeLineOf(la), edgeLineOf(lb)
	two := tc.c.lines == 2
	rng := rand.New(rand.NewSource(seed))
	pushN := max(64, int(min(0.2, max(2*tc.quiet, 0.002))/ss))
	lastSkip := 0
	for at := 0; at < len(la); {
		e := min(len(la), at+1+rng.Intn(2*pushN))
		var bs []uint8
		var be *EdgeLine
		if two {
			bs, be = cb[at:e], eb
		}
		samples = append(samples, ds.Push(ca[at:e], bs)...)
		edges = append(edges, de.PushEdges(ea, be, at, e)...)
		if rng.Intn(50) == 0 && e-lastSkip > 4*maxN {
			// lost words, seldom enough that whole frames come between:
			// both skip them
			lastSkip = e
			k := rng.Intn(pushN)
			ds.Skip(k)
			de.Skip(k)
			e = min(len(la), e+k)
		}
		at = e
	}
	samples = append(samples, ds.Flush()...)
	edges = append(edges, de.Flush()...)
	return samples, edges
}

// Every protocol with an edge decoder hands out exactly the frames from a
// digital stream's edges that it hands out from its samples: long random
// traffic, dense and bursty, one line and two, in random pieces with lost
// stretches between.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestEdgeStreamMatchesSamples(t *testing.T) {
	names := make([]string, 0, len(edgeStreamDecs))
	for n := range edgeStreamDecs {
		names = append(names, n)
	}
	sort.Strings(names)
	if only := os.Getenv("SDS_EDGE_ONLY"); only != "" {
		names = strings.Split(only, ",")
	}
	budget := 3e6 // samples a run
	if testing.Short() {
		budget = 3e5
	}
	for _, name := range names {
		tc := streamCPUCaseOf(t, name)
		ss := edgeStreamPeriod(tc)
		secs := min(max(streamGoldenSecs(tc), 40*tc.longest), budget*ss)
		for k, bursty := range []bool{false, true} {
			seed := int64(len(name))*31 + int64(k)
			la, lb := streamBitsLines(tc, seed, secs, ss, bursty)
			s, e := edgeStreamRuns(tc, la, lb, ss, seed, edgeStreamDecs[name])
			if len(s) == 0 {
				t.Errorf("%s bursty %v: no frames from %d samples", name, bursty, len(la))
			}
			if !reflect.DeepEqual(s, e) {
				t.Errorf("%s bursty %v: %d frames from edges (%s), %d from samples (%s)", name, bursty,
					len(e), streamDigest(e), len(s), streamDigest(s))
				continue
			}
			t.Logf("%-9s bursty %-5v %.3g us: %9d samples, %5d frames alike", name, bursty, ss*1e6, len(la), len(s))
		}
	}
}
