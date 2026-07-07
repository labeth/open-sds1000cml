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
