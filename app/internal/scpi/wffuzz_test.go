package scpi

import (
	"math/rand"
	"testing"

	"open-sds/app/internal/engine"
)

// Waveform readout fuzz: the VXI-11 WF? path is network-exposed and does
// index math on user WFSU params over a frame of arbitrary geometry. Neither
// hostile SP/NP/FP nor a degenerate frame may panic the handler.
func TestWaveformReadoutFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(0xBEEF))
	for i := 0; i < 4000; i++ {
		h, fs := newH(t)
		// hostile-but-in-protocol WFSU (out-of-range values are rejected, but
		// legal combinations over a tiny/empty record must still be safe)
		sp := []int{-5, 0, 1, 3, 255, 300}[rng.Intn(6)]
		np := []int{-1, 0, 1, 100, 81920, 99999}[rng.Intn(6)]
		fp := []int{-1, 0, 1, 5000, 81920, 99999}[rng.Intn(6)]
		h.HandleLine([]byte("WFSU SP," + itoa(sp) + ",NP," + itoa(np) + ",FP," + itoa(fp)))
		// degenerate frame geometry
		n := rng.Intn(4000)
		clen := n
		if rng.Intn(4) == 0 {
			clen = n / 3 // Valid > len(C1)
		}
		f := &engine.Frame{
			C1: make([]uint8, clen), C2: make([]uint8, clen), Valid: n,
			WinCols: n, SampleS: 800e-9, RollCodes: rng.Intn(2) == 0,
		}
		for j := range f.C1 {
			f.C1[j] = uint8(rng.Intn(256))
			f.C2[j] = uint8(rng.Intn(256))
		}
		fs.frame = f
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("iter %d (sp=%d np=%d fp=%d valid=%d clen=%d) panicked: %v", i, sp, np, fp, n, clen, r)
				}
			}()
			h.HandleLine([]byte("C1:WF? DAT2"))
			h.HandleLine([]byte("C2:WF? DAT2"))
			h.HandleLine([]byte("C1:WF? DESC"))
			h.HandleLine([]byte("C1:WF? ALL"))
		}()
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
