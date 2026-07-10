package decode

import (
	"math/rand"
	"testing"
	"time"
)

// Decoder fuzz: the protocol decoders parse UNTRUSTED analog data every
// frame — random noise, rail garbage, and degenerate configs must never
// panic (index arithmetic on edge positions is the classic failure).
func TestDecodeFuzzNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(2718))
	sig := func(n int, kind int) []uint8 {
		s := make([]uint8, n)
		switch kind {
		case 0: // noise
			for i := range s {
				s[i] = uint8(rng.Intn(256))
			}
		case 1: // nyquist toggle
			for i := range s {
				s[i] = uint8(i % 2 * 255)
			}
		case 2: // flat mid
			for i := range s {
				s[i] = 128
			}
		case 3: // slow ramp
			for i := range s {
				s[i] = uint8(i * 255 / max(1, n-1))
			}
		case 4: // bursty random walk
			v := 128
			for i := range s {
				if rng.Intn(20) == 0 {
					v = rng.Intn(256)
				}
				s[i] = uint8(v)
			}
		}
		return s
	}
	colTimes := []float64{0, 1e-12, 800e-9, 1}
	formats := []string{"", "hex", "ascii", "both", "garbage"}
	for i := 0; i < 600; i++ {
		n := rng.Intn(5000)
		a := sig(n, rng.Intn(5))
		b := sig(n, rng.Intn(5))
		ct := colTimes[rng.Intn(len(colTimes))]
		fm := formats[rng.Intn(len(formats))]
		t0 := time.Now()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("iter %d panicked: %v", i, r)
				}
			}()
			switch rng.Intn(4) {
			case 0:
				DecodeUART(a, ct, UARTCfg{
					Baud:   []int{0, -9600, 110, 115200, 1 << 30}[rng.Intn(5)],
					Bits:   []int{0, 5, 8, 9, 64}[rng.Intn(5)],
					Parity: []string{"none", "even", "odd", "x"}[rng.Intn(4)],
					Format: fm, Threshold: rng.Float64()*400 - 70, HaveThr: rng.Intn(2) == 0,
				})
			case 1:
				DecodeI2C(a, b, ct, I2CCfg{Format: fm, Threshold: rng.Float64() * 300, HaveThr: rng.Intn(2) == 0})
			case 2:
				DecodeSPI(a, b, ct, SPICfg{CPOL: rng.Intn(2) == 0, CPHA: rng.Intn(2) == 0,
					MSB: rng.Intn(2) == 0, Format: fm, Threshold: rng.Float64() * 300, HaveThr: rng.Intn(2) == 0})
			case 3:
				Autodetect(a, b, ct, fm)
			}
		}()
		if d := time.Since(t0); d > 2*time.Second {
			t.Fatalf("iter %d took %v — a decoder DoS on hostile input", i, d)
		}
	}
}

// TestDecodeFuzzNewProtos hammers the added single-line decoders (Manchester,
// SENT, CAN, MIL-1553B, ARINC429, USB-LS, FlexRay) with the same untrusted
// analog garbage plus adversarial bit rates and thresholds: none may panic or
// DoS. It also feeds TRUNCATED views of a synthesized real frame (a captured
// record almost never contains a whole frame), the input class HW validation
// showed the round-trip tests miss.
func TestDecodeFuzzNewProtos(t *testing.T) {
	rng := rand.New(rand.NewSource(31415))
	mk := func(n, kind int) []uint8 {
		s := make([]uint8, n)
		switch kind {
		case 0:
			for i := range s {
				s[i] = uint8(rng.Intn(256))
			}
		case 1:
			for i := range s {
				s[i] = uint8(i % 2 * 255)
			}
		case 2:
			for i := range s {
				s[i] = uint8([]int{40, 128, 210}[rng.Intn(3)]) // tri-level (ARINC-ish)
			}
		case 3: // a real Manchester frame then TRUNCATE it at a random point
			full := manchesterWave(mBits([]int{0x55, 0xA3, 0x1C}, true, 8), true, 20)
			if len(full) > 0 {
				s = full[:rng.Intn(len(full)+1)]
			}
		case 4:
			v := 128
			for i := range s {
				if rng.Intn(15) == 0 {
					v = []int{40, 210}[rng.Intn(2)]
				}
				s[i] = uint8(v)
			}
		}
		return s
	}
	cts := []float64{0, -1, 1e-12, 500e-9, 2e-6, 1}
	brs := []int{0, -1, 1, 100, 115200, 1_000_000, 12_000_000, 1 << 30}
	for i := 0; i < 900; i++ {
		a := mk(rng.Intn(6000), rng.Intn(5))
		ct := cts[rng.Intn(len(cts))]
		br := brs[rng.Intn(len(brs))]
		thr := rng.Float64()*400 - 70
		hv := rng.Intn(2) == 0
		t0 := time.Now()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("iter %d (br=%d ct=%g n=%d) panicked: %v", i, br, ct, len(a), r)
				}
			}()
			switch rng.Intn(7) {
			case 0:
				DecodeManchester(a, ct, ManchesterCfg{Bitrate: br, IEEE: hv, MSB: !hv, Bits: []int{0, 8, 1, 16, 99}[rng.Intn(5)], Threshold: thr, HaveThr: hv})
			case 1:
				DecodeSENT(a, ct, SENTCfg{TickNs: float64(br), Nibbles: rng.Intn(70), Threshold: thr, HaveThr: hv})
			case 2:
				DecodeCANFD(a, ct, CANFDCfg{NominalBaud: br, DataBaud: br, DominantLow: hv, Threshold: thr, HaveThr: hv})
			case 3:
				DecodeMIL1553(a, ct, MIL1553Cfg{Bitrate: br, Threshold: thr, HaveThr: hv})
			case 4:
				DecodeARINC429(a, ct, ARINC429Cfg{Bitrate: br, Threshold: thr, HaveThr: hv})
			case 5:
				DecodeUSBLS(a, ct, USBLSCfg{Bitrate: br, Threshold: thr, HaveThr: hv})
			case 6:
				DecodeFlexRay(a, ct, FlexRayCfg{Bitrate: br, Threshold: thr, HaveThr: hv})
			}
		}()
		if d := time.Since(t0); d > 2*time.Second {
			t.Fatalf("iter %d took %v — decoder DoS on hostile input", i, d)
		}
	}
}
