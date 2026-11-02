// ENGMODEL-OWNER-UNIT: FU-APP-DECODE

package decode

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// edgeDec and sampleDec are one protocol's decoders over edges and over
// samples.
type (
	edgeDec   func(a, b *EdgeLine, s float64) Result
	sampleDec func(a, b []uint8, s float64) Result
)

// edgeThrDecs are edgeStreamDecs' decoders, over edges and over samples,
// at thresholds thr (line A) and thrB (line B of a two-line bus).
var edgeThrDecs = map[string]func(thr, thrB float64) (edgeDec, sampleDec){
	"caliper": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return CaliperEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return Caliper(a, b, s, cfg) }
	},
	"cec": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := CECCfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return CECEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return CEC(a, s, cfg) }
	},
	"cjtag": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return CJTAGEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return CJTAG(a, b, s, cfg) }
	},
	"dali": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := DALICfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return DALIEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return DALI(a, s, cfg) }
	},
	"dcf77": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Baud: 1}
		return func(a, _ *EdgeLine, s float64) Result { return DCF77Edges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return DCF77(a, s, cfg) }
	},
	"dht": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := DHTCfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return DHTEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return DHT(a, s, cfg) }
	},
	"dmx512": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := DMXCfg{Baud: 250000, Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return DMX512Edges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return DMX512(a, s, cfg) }
	},
	"dsi": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return DSIEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return DSI(a, s, cfg) }
	},
	"j1850": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := J1850Cfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return J1850Edges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return J1850(a, s, cfg) }
	},
	"lin": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LINCfg{Baud: 19200, Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return LINEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return LIN(a, s, cfg) }
	},
	"maple": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return MapleEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return Maple(a, b, s, cfg) }
	},
	"mdio": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return MDIOEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return MDIO(a, b, s, cfg) }
	},
	"miller": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Baud: 106000}
		return func(a, _ *EdgeLine, s float64) Result { return MillerEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return Miller(a, s, cfg) }
	},
	"modbus": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := ModbusCfg{Baud: 19200, Parity: "even", Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return ModbusEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return Modbus(a, s, cfg) }
	},
	"morse": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return MorseEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return Morse(a, s, cfg) }
	},
	"onewire": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := OneWireCfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return OneWireEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return OneWire(a, s, cfg) }
	},
	"ook": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return OOKEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return OOK(a, s, cfg) }
	},
	"pdi": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return PDIEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return PDI(a, b, s, cfg) }
	},
	"pjdl": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Variant: 1}
		return func(a, _ *EdgeLine, s float64) Result { return PJDLEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return PJDL(a, s, cfg) }
	},
	"ps2": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return PS2Edges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return PS2(a, b, s, cfg) }
	},
	"pwm": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return PWMEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return PWM(a, s, cfg) }
	},
	"qi": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return QiEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return Qi(a, s, cfg) }
	},
	"quad": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return QuadratureEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return Quadrature(a, b, s, cfg) }
	},
	"rcencode": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Variant: 12}
		return func(a, _ *EdgeLine, s float64) Result { return RCEncodeEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return RCEncode(a, s, cfg) }
	},
	"sdcard": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return SDCardEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return SDCard(a, b, s, cfg) }
	},
	"sdq": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Baud: 98425}
		return func(a, _ *EdgeLine, s float64) Result { return SDQEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return SDQ(a, s, cfg) }
	},
	"spdif": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := SPDIFCfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return SPDIFEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return SPDIF(a, s, cfg) }
	},
	"stepper": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return StepperEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return Stepper(a, b, s, cfg) }
	},
	"swd": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return SWDEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return SWD(a, b, s, cfg) }
	},
	"swim": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Baud: 363636}
		return func(a, _ *EdgeLine, s float64) Result { return SWIMEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return SWIM(a, s, cfg) }
	},
	"usbpd": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := USBPDCfg{Baud: 300000, Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return USBPDEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return USBPD(a, s, cfg) }
	},
	"wiegand": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return WiegandEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return Wiegand(a, b, s, cfg) }
	},
	"ws281x": func(thr, _ float64) (edgeDec, sampleDec) {
		cfg := WS281xCfg{Threshold: thr, HaveThr: true}
		return func(a, _ *EdgeLine, s float64) Result { return WS281xEdges(a, s, cfg) },
			func(a, _ []uint8, s float64) Result { return WS281x(a, s, cfg) }
	},
}

// Every edge decoder's whole Result is its samples decoder's at thresholds
// across the codes: a crossing a sample's share past its edge below, at and
// above the half (127.5 puts it on the half, where rounding a crossing to
// its sample goes up) and a hair off the half, the lines' thresholds alike
// and apart, on each protocol's dense traffic and windows cut anywhere in
// it: the decoders read digital lines' edges by their samples (roundUps,
// nthNear) where they lie a share of a sample from them.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestEdgeDecodersMatchSamplesAtThresholds(t *testing.T) {
	names := make([]string, 0, len(edgeThrDecs))
	for n := range edgeThrDecs {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) != len(edgeStreamDecs) {
		t.Fatalf("%d protocols at thresholds, %d edge decoders", len(names), len(edgeStreamDecs))
	}
	thrs := [][2]float64{{90, 160}, {127.5, 127.5}, {127.5 + 1e-12, 200}, {200, 90}, {30, 228}, {127.4, 127.6}}
	for _, name := range names {
		tc := streamCPUCaseOf(t, name)
		ss := edgeStreamPeriod(tc)
		la, lb := streamBitsLines(tc, int64(len(name)), min(max(0.02, 3*(tc.longest+tc.quiet)), 2e6*ss), ss, false)
		ea, eb := edgeLineOf(la), edgeLineOf(lb)
		ca, cb := codesOf(la), codesOf(lb)
		rng := rand.New(rand.NewSource(int64(len(la))))
		for _, th := range thrs {
			edec, sdec := edgeThrDecs[name](th[0], th[1])
			for w := range 3 {
				from, to := 0, len(la)
				if w > 0 {
					from = rng.Intn(len(la))
					to = min(len(la), from+1+rng.Intn(1<<17))
				}
				var a, b EdgeLine
				a.AppendRange(ea, from, to)
				b.AppendRange(eb, from, to)
				got := edec(&a, &b, ss)
				want := sdec(ca[from:to], cb[from:to], ss)
				if g, w := fmt.Sprintf("%#v", got), fmt.Sprintf("%#v", want); g != w {
					t.Fatalf("%s thr %v window %d..%d: over edges\n%.400s\nover samples\n%.400s", name, th, from, to, g, w)
				}
			}
		}
	}
}
