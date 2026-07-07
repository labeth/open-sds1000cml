package scpi

import (
	"math/rand"
	"strings"
	"testing"
)

// SCPI parser fuzz: HandleLine must never panic, whatever arrives on the
// wire — every panic here is a remotely-triggerable crash of the instrument
// loop. Seeded corpus of structural edge cases plus random mutations.
func TestSCPIFuzzNoPanic(t *testing.T) {
	h, _ := newH(t)
	corpus := []string{
		"", ";", ";;;", ":", "::", "*", "*IDN?", "*IDN? extra",
		"C1:", "C9:", "CX:", "C1", "c1:vdiv", "C1:VDIV ", "C1:VDIV 1e309",
		"C1:VDIV -0", "C2:OFST 1e-320", "TDIV", "TDIV ?", "TDIV 0",
		"TRMD", "TRMD ,,,,", "TRSE EDGE,SR,C1,HT,OFF",
		"TRSE ,,,,,,,,,,,,,,,,,,",
		"WFSU SP,0,NP,0,FP,0", "WFSU SP", "WFSU ,",
		"C1:WF? DAT2", "C1:WF?", "WF?",
		"MSIZ 999999999999999999", "MSIZ -7", "MSIZ 7K", "MSIZ ?",
		"PESU", "PACU 1,,", strings.Repeat("A", 65536),
		strings.Repeat(":", 4096), strings.Repeat(";", 4096),
		"\x00\x01\x02\xff\xfe", "C1:VDIV \x00", "TDIV \xff\xff",
		"*RST;*IDN?;TDIV 1E-3;;;",
	}
	for i, c := range corpus {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("corpus %d (%.60q) panicked: %v", i, c, r)
				}
			}()
			h.HandleLine([]byte(c))
		}()
	}
	// random mutations of real-looking commands
	rng := rand.New(rand.NewSource(42))
	seeds := []string{"C1:VDIV 0.5", "TDIV 1E-3", "TRMD NORM", "C1:WF? DAT2", "WFSU SP,4,NP,1000,FP,0", "MSIZ 14K", "TRSE EDGE,SR,C1,HT,TI,HV,100NS"}
	for i := 0; i < 20000; i++ {
		b := []byte(seeds[rng.Intn(len(seeds))])
		for k := 0; k < 1+rng.Intn(6); k++ {
			switch rng.Intn(4) {
			case 0: // flip a byte
				b[rng.Intn(len(b))] = byte(rng.Intn(256))
			case 1: // truncate
				b = b[:rng.Intn(len(b)+1)]
			case 2: // duplicate a slice
				if len(b) > 0 {
					p := rng.Intn(len(b))
					b = append(b[:p], append([]byte(string(b[p:])), b[p:]...)...)
				}
			case 3: // inject separators
				b = append(b, []byte{',', ';', ':', ' '}[rng.Intn(4)])
			}
			if len(b) == 0 {
				b = []byte{','}
			}
			if len(b) > 8192 {
				b = b[:8192]
			}
		}
		line := append([]byte(nil), b...)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("mutation %d (%.80q) panicked: %v", i, line, r)
				}
			}()
			h.HandleLine(line)
		}()
	}
}
