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
		return func(a, b *EdgeLine, s float64) Result { return DecodeCaliperEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeCaliper(a, b, s, cfg) }
	},
	"cec": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := CECCfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeCECEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeCEC(a, s, cfg) }
	},
	"cjtag": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeCJTAGEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeCJTAG(a, b, s, cfg) }
	},
	"dali": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := DALICfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeDALIEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeDALI(a, s, cfg) }
	},
	"dcf77": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Baud: 1}
		return func(a, b *EdgeLine, s float64) Result { return DecodeDCF77Edges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeDCF77(a, s, cfg) }
	},
	"dht": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := DHTCfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeDHTEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeDHT(a, s, cfg) }
	},
	"dmx512": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := DMXCfg{Baud: 250000, Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeDMX512Edges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeDMX512(a, s, cfg) }
	},
	"dsi": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeDSIEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeDSI(a, s, cfg) }
	},
	"j1850": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := J1850Cfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeJ1850Edges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeJ1850(a, s, cfg) }
	},
	"lin": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LINCfg{Baud: 19200, Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeLINEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeLIN(a, s, cfg) }
	},
	"maple": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeMapleEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeMaple(a, b, s, cfg) }
	},
	"mdio": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeMDIOEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeMDIO(a, b, s, cfg) }
	},
	"miller": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Baud: 106000}
		return func(a, b *EdgeLine, s float64) Result { return DecodeMillerEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeMiller(a, s, cfg) }
	},
	"modbus": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ModbusCfg{Baud: 19200, Parity: "even", Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeModbusEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeModbus(a, s, cfg) }
	},
	"morse": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeMorseEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeMorse(a, s, cfg) }
	},
	"onewire": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := OneWireCfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeOneWireEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeOneWire(a, s, cfg) }
	},
	"ook": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeOOKEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeOOK(a, s, cfg) }
	},
	"pdi": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodePDIEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodePDI(a, b, s, cfg) }
	},
	"pjdl": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Variant: 1}
		return func(a, b *EdgeLine, s float64) Result { return DecodePJDLEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodePJDL(a, s, cfg) }
	},
	"ps2": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodePS2Edges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodePS2(a, b, s, cfg) }
	},
	"pwm": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodePWMEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodePWM(a, s, cfg) }
	},
	"qi": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeQiEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeQi(a, s, cfg) }
	},
	"quad": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeQuadratureEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeQuadrature(a, b, s, cfg) }
	},
	"rcencode": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Variant: 12}
		return func(a, b *EdgeLine, s float64) Result { return DecodeRCEncodeEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeRCEncode(a, s, cfg) }
	},
	"sdcard": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeSDCardEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeSDCard(a, b, s, cfg) }
	},
	"sdq": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Baud: 98425}
		return func(a, b *EdgeLine, s float64) Result { return DecodeSDQEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeSDQ(a, s, cfg) }
	},
	"spdif": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := SPDIFCfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeSPDIFEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeSPDIF(a, s, cfg) }
	},
	"stepper": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeStepperEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeStepper(a, b, s, cfg) }
	},
	"swd": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeSWDEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeSWD(a, b, s, cfg) }
	},
	"swim": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := LineCfg{Threshold: thr, HaveThr: true, Baud: 363636}
		return func(a, b *EdgeLine, s float64) Result { return DecodeSWIMEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeSWIM(a, s, cfg) }
	},
	"usbpd": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := USBPDCfg{Baud: 300000, Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeUSBPDEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeUSBPD(a, s, cfg) }
	},
	"wiegand": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := ClockedCfg{Threshold: thr, HaveThr: true, ThresholdB: thrB, HaveThrB: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeWiegandEdges(a, b, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeWiegand(a, b, s, cfg) }
	},
	"ws281x": func(thr, thrB float64) (edgeDec, sampleDec) {
		cfg := WS281xCfg{Threshold: thr, HaveThr: true}
		return func(a, b *EdgeLine, s float64) Result { return DecodeWS281xEdges(a, s, cfg) },
			func(a, b []uint8, s float64) Result { return DecodeWS281x(a, s, cfg) }
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
			for w := 0; w < 3; w++ {
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
