package scpi

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
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
		"C1:BWL", "C1:BWL ON", "C1:BWL?", "C2:UNIT AMP", "C1:UNIT",
		"C1:SKEW", "C1:SKEW 1E309", "C1:SKEW \x00NS", "C2:SKEW?",
		"C1:INVS ON;C1:INVS?", "C1:INVS MAYBE", "TRCP HFREJ", "TRCP",
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
	seeds := []string{"C1:VDIV 0.5", "TDIV 1E-3", "TRMD NORM", "C1:WF? DAT2", "WFSU SP,4,NP,1000,FP,0", "MSIZ 14K", "TRSE EDGE,SR,C1,HT,TI,HV,100NS", "C1:BWL OFF", "C2:UNIT A", "C1:SKEW -5NS", "C1:INVS ON", "TRCP DC"}
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

// TestSCPIFuzzSetQueryInvariant is the randomized form of TestSetNeverLies:
// a long random walk over the settable command surface with randomized valid
// (and deliberately unsupported) arguments, asserting after EVERY set that
// either (a) the set was silent and the paired query reflects exactly the
// value set, or (b) the set returned the expected §3.4 error token and the
// query still reports the true state. Silent success + no effect — the bug
// class where a stub swallows a set and the query then lies — fails here.
func TestSCPIFuzzSetQueryInvariant(t *testing.T) {
	h, _, _ := newHFE(t)
	rng := rand.New(rand.NewSource(7))
	tdivs := engine.SupportedTdivs()

	type round struct {
		set    string
		setErr string // "" = expect silence; else exact error line
		query  string
		want   string // exact query reply
	}
	pick := func(ss ...string) string { return ss[rng.Intn(len(ss))] }
	gens := []func() round{
		func() round { // UNIT: shadow round-trip
			ch, u := 1+rng.Intn(2), pick("V", "A")
			return round{fmt.Sprintf("C%d:UNIT %s", ch, u), "",
				fmt.Sprintf("C%d:UNIT?", ch), fmt.Sprintf("C%d:UNIT %s\n", ch, u)}
		},
		func() round { // SKEW: shadow round-trip through the float grammar
			ch := 1 + rng.Intn(2)
			v := (rng.Float64()*200 - 100) * 1e-9
			return round{fmt.Sprintf("C%d:SKEW %s", ch, strconv.FormatFloat(v, 'E', -1, 64)), "",
				fmt.Sprintf("C%d:SKEW?", ch), fmt.Sprintf("C%d:SKEW %s\n", ch, sciS(v))}
		},
		func() round { // INVS: shadow round-trip
			ch, s := 1+rng.Intn(2), pick("ON", "OFF")
			return round{fmt.Sprintf("C%d:INVS %s", ch, s), "",
				fmt.Sprintf("C%d:INVS?", ch), fmt.Sprintf("C%d:INVS %s\n", ch, s)}
		},
		func() round { // BWL: OFF matches the fixed state, ON must error
			ch := 1 + rng.Intn(2)
			r := round{fmt.Sprintf("C%d:BWL OFF", ch), "",
				fmt.Sprintf("C%d:BWL?", ch), fmt.Sprintf("C%d:BWL OFF\n", ch)}
			if rng.Intn(2) == 0 {
				r.set = fmt.Sprintf("C%d:BWL ON", ch)
				r.setErr = "Data out of range\n"
			}
			return r
		},
		func() round { // TRCP: fixed DC, everything else must error
			r := round{"TRCP DC", "", "TRCP?", "TRCP DC\n"}
			if rng.Intn(2) == 0 {
				r.set = "TRCP " + pick("AC", "HFREJ", "LFREJ")
				r.setErr = "Data out of range\n"
			}
			return r
		},
		func() round { // TDIV: engine ladder round-trip
			v := tdivs[rng.Intn(len(tdivs))]
			return round{"TDIV " + strconv.FormatFloat(v, 'E', -1, 64), "",
				"TDIV?", "TDIV " + sciS(v) + "\n"}
		},
		func() round { // TRLV: shadow echoes the requested level
			v := rng.Float64()*6 - 3
			return round{"TRLV " + strconv.FormatFloat(v, 'E', -1, 64), "",
				"TRLV?", "TRLV " + sciV(v) + "\n"}
		},
		func() round { // TRDL
			v := (rng.Float64()*2 - 1) * 1e-3
			return round{"TRDL " + strconv.FormatFloat(v, 'E', -1, 64), "",
				"TRDL?", "TRDL " + sciS(v) + "\n"}
		},
		func() round { // TRSL
			s := pick("POS", "NEG")
			return round{"TRSL " + s, "", "TRSL?", "TRSL " + s + "\n"}
		},
		func() round { // TRSE source
			src := pick("C1", "C2")
			return round{"TRSE EDGE,SR," + src + ",HT,OFF", "",
				"TRSE?", "TRSE EDGE,SR," + src + ",HT,OFF\n"}
		},
		func() round { // TRMD
			m := pick("AUTO", "NORM", "SINGLE", "STOP")
			return round{"TRMD " + m, "", "TRMD?", "TRMD " + m + "\n"}
		},
		func() round { // ACQW
			m := pick("SAMPLING", "AVERAGE", "ERES", "PEAK_DETECT")
			return round{"ACQW " + m, "", "ACQW?", "ACQW " + m + "\n"}
		},
		func() round { // AVGA
			n := 1 + rng.Intn(256)
			return round{"AVGA " + strconv.Itoa(n), "", "AVGA?", "AVGA " + strconv.Itoa(n) + "\n"}
		},
		func() round { // ATTN
			ch, v := 1+rng.Intn(2), pick("1", "10", "100", "1000")
			return round{fmt.Sprintf("C%d:ATTN %s", ch, v), "",
				fmt.Sprintf("C%d:ATTN?", ch), fmt.Sprintf("C%d:ATTN %s\n", ch, v)}
		},
		func() round { // TRA
			ch, s := 1+rng.Intn(2), pick("ON", "OFF")
			return round{fmt.Sprintf("C%d:TRA %s", ch, s), "",
				fmt.Sprintf("C%d:TRA?", ch), fmt.Sprintf("C%d:TRA %s\n", ch, s)}
		},
		func() round { // CPL
			ch, s := 1+rng.Intn(2), pick("A1M", "D1M", "GND")
			return round{fmt.Sprintf("C%d:CPL %s", ch, s), "",
				fmt.Sprintf("C%d:CPL?", ch), fmt.Sprintf("C%d:CPL %s\n", ch, s)}
		},
		func() round { // VDIV: detent ladder round-trip through the fake FE
			ch := 1 + rng.Intn(2)
			v := analog.Detents[rng.Intn(len(analog.Detents))].VdivV
			return round{fmt.Sprintf("C%d:VDIV %s", ch, strconv.FormatFloat(v, 'E', -1, 64)), "",
				fmt.Sprintf("C%d:VDIV?", ch), fmt.Sprintf("C%d:VDIV %s\n", ch, sciV(v))}
		},
		func() round { // OFST: round-trips through the DAC-code quantizer
			ch := 1 + rng.Intn(2)
			v := rng.Float64()*4 - 2
			code := analog.OffsetCode(ch-1, v)
			w := 0.0
			if code != 0 {
				w = analog.OffsetVolts(ch-1, code)
			}
			return round{fmt.Sprintf("C%d:OFST %s", ch, strconv.FormatFloat(v, 'E', -1, 64)), "",
				fmt.Sprintf("C%d:OFST?", ch), fmt.Sprintf("C%d:OFST %s\n", ch, sciV(w))}
		},
	}

	for i := 0; i < 5000; i++ {
		r := gens[rng.Intn(len(gens))]()
		gotSet := string(h.HandleLine([]byte(r.set + "\n")))
		if gotSet != r.setErr {
			t.Fatalf("round %d: set %q replied %q, want %q", i, r.set, gotSet, r.setErr)
		}
		if gotQ := string(h.HandleLine([]byte(r.query + "\n"))); gotQ != r.want {
			t.Fatalf("round %d: after %q, %q = %q, want %q", i, r.set, r.query, gotQ, r.want)
		}
	}
}
