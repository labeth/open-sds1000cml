// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"encoding/binary"
	"math"
	"testing"

	"open-sds/app/internal/decode"
)

// Native fuzz targets: the seed corpus runs in every `go test`; `go test
// -fuzz=FuzzX` explores further. Each checks that nothing panics and that the
// results keep their invariants.

var fuzzProtos = []string{"uart", "spi", "i2c", "canfd", "manchester", "flexray", "usbls", "mil1553",
	"arinc429", "sent", "", "who"}
var fuzzKinds = []string{"data", "frame-error", "parity-error", "gap", "start", "stop", "addr", "rw", "ack", "nak",
	"sof", "id", "ide", "rtr", "fd", "dlc", "crc", "crc-error", "sync", "pause", "rubbish"}
var fuzzTexts = []string{"", "R", "W", "csync", "dsync", "STUFF!", "(no STOP)", "!"}

// checkPacketInvariants holds for any grouping.
// TRLC-LINKS: REQ-SDS-214
func checkPacketInvariants(t *testing.T, ps []Packet, n int) {
	t.Helper()
	for i, p := range ps {
		if p.I0 < 0 || p.I0 > p.I1 || (n > 0 && p.I1 >= n) || len(p.Bytes) != len(p.ByteErr) {
			t.Fatalf("packet %d: [%d, %d] of %d, %d bytes %d flags", i, p.I0, p.I1, n, len(p.Bytes), len(p.ByteErr))
		}
	}
	var l PacketList
	l.Update(ps)
	l.Update(ps)
	l.Select(len(ps) / 2)
	l.Replace(ps)
	if s := l.Selected(); s < -1 || s >= l.Len() || (l.Len() > 0) != (s >= 0) {
		t.Fatalf("selected %d of %d rows", s, l.Len())
	}
	for _, r := range l.Rows() {
		if !r.Complete {
			t.Fatal("an incomplete packet listed")
		}
	}
	if len(ps) > 0 && len(ps[0].Bytes) > 0 {
		if m := l.FindMatch(ps[0].Bytes[:1], -1, 1); m < -1 || m >= l.Len() {
			t.Fatalf("FindMatch %d of %d rows", m, l.Len())
		}
	}
	if i := l.SelectedIn(ps); i < -1 || i >= len(ps) {
		t.Fatalf("SelectedIn %d of %d", i, len(ps))
	}
}

// FuzzPackets groups arbitrary spans: any order, any position, any kind.
// TRLC-LINKS: REQ-SDS-214
func FuzzPackets(f *testing.F) {
	f.Add([]byte{0, 100, 0, 0, 10, 0, 50, 0, 0, 0x41, 0, 60, 0, 99, 0, 0, 0x42})
	f.Add([]byte{2, 0, 2, 0, 5, 0, 5, 4, 0, 0, 0, 6, 0, 30, 6, 0x24, 1, 0, 31, 0, 31, 7, 0, 1, 0, 40, 0, 70, 0, 0x55, 0})
	f.Add([]byte{7, 0, 9, 0, 1, 0, 5, 4, 3, 0, 0, 10, 0, 80, 0, 0x2c, 0x45, 0, 90, 1, 0, 1, 0xff})
	f.Add([]byte{3, 255, 255, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 3 {
			return
		}
		r := decode.Result{Proto: fuzzProtos[int(data[0])%len(fuzzProtos)], SPB: float64(data[1]%32) + 0.5}
		n := int(binary.BigEndian.Uint16(data[1:3])) - 100 // includes "unknown" (<= 0)
		for b := data[3:]; len(b) >= 8; b = b[8:] {
			r.Spans = append(r.Spans, decode.Span{
				I0:   int(int16(binary.BigEndian.Uint16(b[0:2]))),
				I1:   int(int16(binary.BigEndian.Uint16(b[2:4]))),
				Kind: fuzzKinds[int(b[4])%len(fuzzKinds)],
				Text: fuzzTexts[int(b[5])%len(fuzzTexts)],
				Val:  int(int16(binary.BigEndian.Uint16(b[6:8]))),
			})
		}
		ps := Packets(r, n)
		limit := n
		if n <= 0 {
			limit = 0
		}
		checkPacketInvariants(t, ps, limit)
	})
}

// FuzzDecodedPackets runs a real decoder on an arbitrary waveform and groups
// its output.
// TRLC-LINKS: REQ-SDS-214
func FuzzDecodedPackets(f *testing.F) {
	f.Add(byte(0), uartMsgs([][]byte{[]byte("Hi"), {0x55}}, 8, 20, 20, 20))
	f.Add(byte(1), []byte{0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 0, 255, 128, 0, 255})
	f.Add(byte(4), manchesterFrames([][]byte{{0xAA, 0x12}}, 10, 8))
	f.Add(byte(9), sentLine([][]int{{1, 2, 3, 4, 5, 6, 7, 8}}, 4))
	f.Fuzz(func(t *testing.T, proto byte, w []byte) {
		if len(w) > 1<<14 {
			return
		}
		half := len(w) / 2
		var r decode.Result
		switch proto % 10 {
		case 0:
			r = decode.DecodeUART(w, 1e-6, decode.UARTCfg{})
		case 1:
			r = decode.DecodeSPI(w[:half], w[half:2*half], 1e-6, decode.SPICfg{MSB: true})
		case 2:
			r = decode.DecodeI2C(w[:half], w[half:2*half], 1e-6, decode.I2CCfg{})
		case 3:
			r = decode.DecodeCANFD(w, 1e-6, decode.CANFDCfg{DominantLow: true})
		case 4:
			r = decode.DecodeManchester(w, 1e-6, decode.ManchesterCfg{IEEE: true, MSB: true})
		case 5:
			r = decode.DecodeFlexRay(w, 1e-8, decode.FlexRayCfg{})
		case 6:
			r = decode.DecodeUSBLS(w, 1e-7, decode.USBLSCfg{})
		case 7:
			r = decode.DecodeMIL1553(w, 1e-7, decode.MIL1553Cfg{})
		case 8:
			r = decode.DecodeARINC429(w, 1e-6, decode.ARINC429Cfg{})
		default:
			r = decode.DecodeSENT(w, 1e-6, decode.SENTCfg{})
		}
		checkPacketInvariants(t, Packets(r, len(w)), len(w))
	})
}

// FuzzEye folds arbitrary codes, sample interval and bit hint.
// TRLC-LINKS: REQ-SDS-215
func FuzzEye(f *testing.F) {
	rec := prbsRecords(1, 600, 9.5, wave{lo: 60, hi: 200, rise: 2, noise: 0.5}, nil)[0]
	seed := make([]byte, len(rec))
	for i, v := range rec {
		seed[i] = byte(v)
	}
	f.Add(seed, 2e-9, 0.0, uint16(384), uint16(170))
	f.Add(seed, 2e-9, 9.5*2e-9, uint16(7), uint16(3))
	f.Add([]byte{0, 0, 255, 255, 0, 0, 255, 255, 0, 0, 255, 255, 0, 0, 255, 255, 0, 0}, 1.0, 1e300, uint16(1), uint16(1))
	f.Add([]byte{}, math.NaN(), math.Inf(1), uint16(0), uint16(0))
	f.Fuzz(func(t *testing.T, data []byte, sampleS, hint float64, cols, rows uint16) {
		codes := make([]float64, len(data))
		for i, v := range data {
			codes[i] = float64(v) + float64(i%3)/4
		}
		e := NewEye(int(cols), int(rows))
		e.Feed(codes, sampleS, hint)
		e.Feed(codes, sampleS, hint)
		r := e.Result()
		if r.Records+r.Rejected != 2 || len(r.Grid) != r.Cols*r.Rows || r.Cols%2 != 0 {
			t.Fatalf("records %d rejected %d grid %d (%dx%d)", r.Records, r.Rejected, len(r.Grid), r.Cols, r.Rows)
		}
		if r.Records > 0 && !(r.UI > 0 && r.SamplesPerUI >= 1.2) {
			t.Fatalf("folded with UI %v, %v samples/UI", r.UI, r.SamplesPerUI)
		}
		for name, v := range map[string]float64{"width": r.EyeWidth, "crossing": r.Crossing} {
			if !math.IsNaN(v) && (v < 0 || v > 1) {
				t.Fatalf("%s %v", name, v)
			}
		}
		if !math.IsNaN(r.EyeHeight) && r.EyeHeight < 0 {
			t.Fatalf("height %v", r.EyeHeight)
		}
		e.Opening(float64(cols)/100, float64(rows))
		e.Reset()
	})
}

// FuzzEyeJitter feeds the eyejitter.js port arbitrary records.
// TRLC-LINKS: REQ-SDS-215, REQ-SDS-199
func FuzzEyeJitter(f *testing.F) {
	f.Add([]byte{60, 60, 200, 200, 60, 200, 60, 60, 60, 200, 200, 200, 60, 200, 60, 200, 200, 60, 60, 200}, uint8(1))
	f.Add([]byte{128, 129, 127, 128}, uint8(0))
	f.Fuzz(func(t *testing.T, data []byte, mode uint8) {
		sig := make([]float64, len(data))
		for i, v := range data {
			sig[i] = float64(v)
			if mode&1 == 1 && i%17 == 0 {
				sig[i] = math.NaN()
			}
		}
		s := NewEyeJitter(EyeJitterOptions{EyeW: int(mode % 7), EyeH: int(mode % 5), FFTN: int(mode % 33),
			TIECap: int(mode % 3)})
		for k := 0; k < 3; k++ {
			s.Feed(sig, 1e-9)
		}
		r := s.Result()
		if r.Records+r.Rejected != 3 {
			t.Fatalf("records %d rejected %d", r.Records, r.Rejected)
		}
	})
}

// FuzzPeaks searches arbitrary spectra and numbers their harmonics.
// TRLC-LINKS: REQ-SDS-216
func FuzzPeaks(f *testing.F) {
	f.Add([]byte{0, 10, 200, 10, 0, 0, 5, 100, 5, 0, 0, 3, 50, 3, 0}, uint8(8), 0.0)
	f.Add([]byte{255, 255, 255, 255, 255, 255}, uint8(0), 1e3)
	f.Fuzz(func(t *testing.T, data []byte, maxPeaks uint8, f0 float64) {
		db := make([]float64, len(data))
		for i, v := range data {
			db[i] = -float64(255-int(v)) / 2
			if v == 7 {
				db[i] = math.Inf(-1)
			}
		}
		ps := Peaks(db, 12.5, int(maxPeaks%20))
		if len(ps) > int(maxPeaks%20) {
			t.Fatalf("%d peaks, max %d", len(ps), maxPeaks%20)
		}
		for i, p := range ps {
			if p.RelDB > 1e-9 || (i > 0 && p.Freq < ps[i-1].Freq) || p.Bin < 2 || p.Bin >= len(db)-2 {
				t.Fatalf("peak %d: %+v", i, p)
			}
		}
		th := Harmonics(ps, f0)
		for _, p := range ps {
			if p.Harm < 0 || p.Harm > MaxHarmonic {
				t.Fatalf("harmonic %d", p.Harm)
			}
		}
		if th.N < 0 || th.N > MaxHarmonic-1 || (th.Fund >= len(ps)) || (!math.IsNaN(th.Pct) && th.Pct < 0) {
			t.Fatalf("THD %+v", th)
		}
	})
}

// FuzzCompare runs RefDiff, Repeats and Stats on arbitrary traces with gaps.
// TRLC-LINKS: REQ-SDS-217, REQ-SDS-218
func FuzzCompare(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8}, []byte{1, 2, 3, 4, 5, 6, 7, 9}, 0.5, 2)
	f.Add([]byte{}, []byte{0}, -1.0, -5)
	f.Fuzz(func(t *testing.T, a, b []byte, tol float64, slack int) {
		conv := func(d []byte) []float64 {
			out := make([]float64, len(d))
			for i, v := range d {
				out[i] = float64(v)/32 - 4
				if v == 0xff {
					out[i] = math.NaN()
				}
			}
			return out
		}
		x, y := conv(a), conv(b)
		mx, rms, pct := RefDiff(x, y, tol)
		if !math.IsNaN(pct) && (pct < 0 || pct > 100 || mx < 0 || rms < 0 || rms > mx+1e-12) {
			t.Fatalf("RefDiff %v %v %v", mx, rms, pct)
		}
		Repeats(x, y, tol, slack%4096)
		if Repeats(x, x, math.Abs(tol), slack%4096) && len(x) == 0 {
			t.Fatal("nothing repeats itself")
		}
		var s Stats
		for _, v := range x {
			s.Add(v)
		}
		if s.N() > 0 && !(s.Min() <= s.Mean()+1e-12 && s.Mean() <= s.Max()+1e-12) {
			t.Fatalf("min %v mean %v max %v", s.Min(), s.Mean(), s.Max())
		}
		if s.N() > 1 && !(s.Sdev() >= 0) {
			t.Fatalf("sdev %v", s.Sdev())
		}
	})
}
