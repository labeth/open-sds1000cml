// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

// BoardTests is everything the takeover stage measured beyond the census.
type BoardTests struct {
	FrontEnd [2]FrontEndResult `json:"front_end"`
	Trigger  TriggerResult     `json:"trigger"`
	Edges    [2]EdgeResult     `json:"edges"`
	SRAM     SRAMResult        `json:"sram"`
	Panel    PanelResult       `json:"panel"`
	GPIO113  OutputResult      `json:"gpio113"`
	PWM      OutputResult      `json:"ehrpwm"`
	I2C      []string          `json:"i2c_answers"`
	Error    string            `json:"error,omitempty"`
}

// boardReferenceJSON is what the takeover stage measured on the SDS1102CML+
// that another board is compared with (a test keeps it in step with the
// census reference).
//
//go:embed board_reference.json
var boardReferenceJSON []byte

// boardReference is the SDS1102CML+'s values.
type boardReference struct {
	Chain        string   `json:"panel_chain"`
	TriggerBall  string   `json:"trigger_ball"`    // C1's comparator
	TriggerBall2 string   `json:"trigger_ball_c2"` // C2's
	GPIO113Ball  string   `json:"gpio113_ball"`
	PWMBall      string   `json:"ehrpwm_ball"`
	ARM          []string `json:"arm"`
	I2C          []string `json:"i2c_answers"`
}

// loadBoardReference parses the embedded reference.
// TRLC-LINKS: REQ-SDS-290, REQ-SDS-291
func loadBoardReference() boardReference {
	var r boardReference
	_ = json.Unmarshal(boardReferenceJSON, &r)
	return r
}

// spread is max/min of xs (1 for fewer than two).
// TRLC-LINKS: REQ-SDS-290
func spread(xs []float64) float64 {
	if len(xs) < 2 {
		return 1
	}
	return slices.Max(xs) / slices.Min(xs)
}

// median is the median of xs.
// TRLC-LINKS: REQ-SDS-290
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[len(s)/2]
}

// verdicts is a list of verdicts being judged.
type verdictList []Verdict

// add appends a verdict.
// TRLC-LINKS: REQ-SDS-290, REQ-SDS-291
func (l *verdictList) add(part, v, f string, args ...any) {
	*l = append(*l, Verdict{part, v, fmt.Sprintf(f, args...)})
}

// boardVerdicts judges the front end of each channel (gain DAC, range relay,
// AC coupling, GND bit, bandwidth limit, the 1 kHz square), each channel's
// trigger comparator, the SRAM, the panel chain and key interrupt, the ARM's
// outputs to the FPGA and the ARM's devices against the SDS1102CML+.
// TRLC-LINKS: REQ-SDS-290, REQ-SDS-291
func boardVerdicts(t BoardTests, inv []string) []Verdict {
	var l verdictList
	ref := loadBoardReference()
	for ch, fr := range t.FrontEnd {
		l.frontEnd(ch, fr, t.Edges[ch])
	}
	l.trigger(t, ref)
	l.sram(t.SRAM)
	l.panel(t.Panel, ref)
	l.outputs(t, ref)
	l = append(l, armVerdict(inv, ref.ARM))
	i2c := armVerdict(t.I2C, ref.I2C)
	i2c.Part = "I2C devices that answer"
	return append(l, i2c)
}

// frontEnd judges channel ch's front end.
// TRLC-LINKS: REQ-SDS-290
func (l *verdictList) frontEnd(ch int, fr FrontEndResult, edge EdgeResult) {
	add := l.add
	c := fmt.Sprintf("channel %d ", ch+1)
	if fr.Error != "" {
		add(c+"front end", vAbsent, "not measured: %s", fr.Error)
		return
	}
	// the offset is injected ahead of the gain stage: an offset code
	// moves the input by a fixed amount within a range (44× more on the
	// attenuated range, spec 06 §5.2), and the gain makes the codes it
	// moves follow 1/V/div, so slope × V/div holds still within a range
	var prod, slopes [2][]float64
	for _, d := range fr.Detents {
		if d.Error != "" || d.Slope <= 0 {
			continue
		}
		t := 0
		if d.Atten {
			t = 1
		}
		prod[t] = append(prod[t], d.Slope*d.Vdiv)
		slopes[t] = append(slopes[t], d.Slope)
	}
	held := func(xs []float64) (bool, float64) {
		if len(xs) < 3 {
			return false, 0
		}
		m := median(xs)
		in := 0
		for _, x := range xs {
			if math.Abs(x/m-1) < 0.4 {
				in++
			}
		}
		return in*10 >= len(xs)*6, m
	}
	h0, m0 := held(prod[0])
	h1, m1 := held(prod[1])
	switch {
	case h1 && spread(slopes[1]) > 4 || h0 && spread(slopes[0]) > 4:
		add(c+"gain DAC", vPresent, "the codes an offset step moves follow V/div: slope × V/div holds within 40 %% on %s while the slopes span %.0f× and %.0f×", heldText(h0, h1), spread(slopes[0]), spread(slopes[1]))
	case len(slopes[1]) >= 3 && spread(slopes[1]) < 1.5:
		add(c+"gain DAC", vAbsent, "the codes an offset step moves do not change with V/div (the attenuated range's slopes span %.2f×)", spread(slopes[1]))
	default:
		add(c+"gain DAC", vPartial, "slope × V/div does not hold on either range (%d and %d steps measured)", len(prod[0]), len(prod[1]))
	}
	switch {
	case m0 > 0 && m1 > 0:
		r := m1 / m0
		v := vPresent
		if r < 5 {
			v = vAbsent
		}
		add(c+"range relay", v, "an offset code moves the input %.0f× further on the attenuated range than on the sensitive one (44× by spec 06 §5.2)", r)
	default:
		add(c+"range relay", vOther, "not measured: too few steps of one range")
	}
	var amps []string
	sq := false
	for _, d := range fr.Detents {
		if d.Square.Square {
			sq = sq || fits(d.Square)
			amps = append(amps, fmt.Sprintf("%g V/div %.0f", d.Vdiv, d.Square.Amp))
		}
	}
	if sq {
		add(c+"1 kHz input", vPresent, "the square's amplitude in codes: %s", strings.Join(amps, ", "))
	} else {
		add(c+"1 kHz input", vAbsent, "no square on the probe: connect it to the scope's 1 kHz output for the coupling and trigger tests")
	}
	dc, ac, gnd := fr.Coupling["DC"], fr.Coupling["AC"], fr.Coupling["GND"]
	mid := func(l Levels) float64 { return (l.Lo + l.Hi) / 2 }
	// the 1 kHz square from ground up carries a DC level of half its
	// amplitude: AC coupling blocks it, and the square centres on ground
	switch d := mid(ac) - mid(dc); {
	case !fits(dc):
		add(c+"AC coupling", vOther, "not tested: needs the 1 kHz square on the probe (the AC level differs from DC by %.0f codes at %g V/div)", d, fr.Signal)
	case math.Abs(d) >= 0.25*dc.Amp && math.Abs(d) <= 0.75*dc.Amp:
		add(c+"AC coupling", vPresent, "AC blocks the square's DC: its middle moves %.0f codes, its amplitude is %.0f (%g V/div)", d, dc.Amp, fr.Signal)
	default:
		add(c+"AC coupling", vAbsent, "AC leaves the square's DC: its middle moves %.0f codes, its amplitude is %.0f (%g V/div)", d, dc.Amp, fr.Signal)
	}
	switch {
	case !fits(dc):
		add(c+"GND relay bit", vOther, "not tested: needs the 1 kHz square on the probe")
	case !gnd.Square:
		add(c+"GND relay bit", vOther, "GND removes the square (%.0f codes in DC): on the SDS1102CML+ it does not (the factory app grounds in its FPGA)", dc.Amp)
	case math.Abs(mid(gnd)-mid(ac)) <= 8:
		add(c+"GND relay bit", vPresent, "GND reads as AC, as on the SDS1102CML+ (the factory app grounds in its FPGA, CS1 0x21)")
	default:
		add(c+"GND relay bit", vOther, "GND reads %.0f codes from AC: on the SDS1102CML+ the two are one", mid(gnd)-mid(ac))
	}
	e := edge
	switch {
	case e.Error != "":
		add(c+"bandwidth limit", vOther, "not tested: %s", e.Error)
	case e.Rise[1] >= 1.25*e.Rise[0] && e.Rise[1] >= 0.8:
		add(c+"bandwidth limit", vPresent, "the 20 MHz limit slows the square's edge: about %.0f ns without it, %.0f ns with it (10..90 %%, %d captures)", 10*e.Rise[0], 10*e.Rise[1], e.Captures)
	default:
		add(c+"bandwidth limit", vAbsent, "the edge is the same with the 20 MHz limit: about %.0f ns without, %.0f ns with", 10*e.Rise[0], 10*e.Rise[1])
	}
	if e.Error == "" && e.Rise[0] > 0 {
		add(c+"bandwidth", vOther, "the square's 10..90 %% edge takes about %.1f ns (%.0f MHz if the front end sets it; the 1 kHz output's own edge may be slower)", 10*e.Rise[0], 350/(10*e.Rise[0]))
	}
}

// trigger judges each channel's trigger comparator.
// TRLC-LINKS: REQ-SDS-290
func (l *verdictList) trigger(t BoardTests, ref boardReference) {
	add := l.add
	tr := t.Trigger
	for ch := range 2 {
		part := fmt.Sprintf("channel %d trigger comparator", ch+1)
		want := ref.TriggerBall
		if ch == 1 {
			want = ref.TriggerBall2
		}
		sq := false
		for _, d := range t.FrontEnd[ch].Detents {
			sq = sq || fits(d.Square)
		}
		switch b := tr.Comparator[ch]; {
		case tr.Error != "":
			add(part, vAbsent, "%s", tr.Error)
		case b != "" && (want == "" || want == b):
			add(part, vPresent, "ball %s toggles on the square at the trigger levels within it, and stops with the channel railed", b)
		case b != "":
			add(part, vOther, "ball %s toggles on the square (%s on the SDS1102CML+)", b, want)
		case !sq:
			add(part, vOther, "not tested: no square on the channel's probe")
		default:
			add(part, vAbsent, "no ball toggles with the channel's square across the trigger levels (balls that moved: %v)", keysOf3(tr.Patterns))
		}
	}
}

// sram judges the SRAM test.
// TRLC-LINKS: REQ-SDS-291
func (l *verdictList) sram(s SRAMResult) {
	add := l.add
	switch {
	case s.Error != "":
		add("SRAM", vOther, "not measured: %s", s.Error)
	case s.Checked > 500000 && s.PatternError == 0 && s.CountError <= 4:
		add("SRAM", vPresent, "%d words written and read back through the address counter", s.Checked)
	case s.PatternError*2 > s.Checked:
		add("SRAM", vAbsent, "%d of %d words read back wrong (bits %08x): no SRAM answers", s.PatternError, s.Checked, s.BadBits)
	default:
		add("SRAM", vPartial, "%d words wrong, %d out of sequence (an address line), data bits %08x wrong", s.PatternError, s.CountError, s.BadBits)
	}
}

// panel judges the key chain and the key interrupt.
// TRLC-LINKS: REQ-SDS-291
func (l *verdictList) panel(p PanelResult, ref boardReference) {
	add := l.add
	switch {
	case p.Error != "" && p.Chain == "":
		add("front panel key chain", vOther, "not measured: %s", p.Error)
	case !strings.Contains(p.Chain, "0"):
		add("front panel key chain", vAbsent, "the chain reads all ones: nothing drives J1")
	case ref.Chain != "" && p.Chain != ref.Chain:
		add("front panel key chain", vPresent, "the chain answers; bits %v differ from the SDS1102CML+ with no key pressed", diffBits(p.Chain, ref.Chain))
	default:
		add("front panel key chain", vPresent, "the chain answers")
	}
	switch {
	case p.IRQ && p.IRQQuiet:
		add("front panel key interrupt", vPresent, "F1's pulse reaches the ARM (SIGIO on /dev/fpga_key)")
	case p.IRQ:
		add("front panel key interrupt", vOther, "SIGIO came, but also without the pulse")
	default:
		add("front panel key interrupt", vAbsent, "no SIGIO on /dev/fpga_key for F1's pulse %s", p.Error)
	}
}

// outputs judges the ARM's outputs to the FPGA.
// TRLC-LINKS: REQ-SDS-291
func (l *verdictList) outputs(t BoardTests, ref boardReference) {
	add := l.add
	for _, o := range []struct {
		part, want string
		r          OutputResult
	}{{"ARM gpio113 to the FPGA", ref.GPIO113Ball, t.GPIO113}, {"ARM eHRPWM to the FPGA", ref.PWMBall, t.PWM}} {
		switch {
		case o.r.Ball != "" && o.want == o.r.Ball:
			add(o.part, vPresent, "reaches ball %s", o.r.Ball)
		case o.r.Ball != "" && o.want == "":
			add(o.part, vOther, "reaches ball %s (none on the SDS1102CML+)", o.r.Ball)
		case o.r.Ball != "":
			add(o.part, vOther, "reaches ball %s (%s on the SDS1102CML+)", o.r.Ball, o.want)
		case o.want == "":
			add(o.part, vOther, "reaches no FPGA ball, nor on the SDS1102CML+: not testable from the FPGA")
		default:
			add(o.part, vAbsent, "reaches no ball (%s on the SDS1102CML+): %s", o.want, o.r.Error)
		}
	}
}

// heldText names the ranges on which slope × V/div held.
// TRLC-LINKS: REQ-SDS-290
func heldText(sens, att bool) string {
	switch {
	case sens && att:
		return "both ranges"
	case att:
		return "the attenuated range"
	}
	return "the sensitive range"
}

// keysOf3 is m's keys, sorted.
// TRLC-LINKS: REQ-SDS-290
func keysOf3(m map[string][3]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// diffBits is the indexes where a and b differ.
// TRLC-LINKS: REQ-SDS-291
func diffBits(a, b string) []int {
	var out []int
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			out = append(out, i)
		}
	}
	return out
}

// armVerdict compares the ARM's devices with the SDS1102CML+'s.
// TRLC-LINKS: REQ-SDS-291
func armVerdict(inv, ref []string) Verdict {
	if len(ref) == 0 {
		return Verdict{"ARM devices", vOther, fmt.Sprintf("%d devices (no SDS1102CML+ reference to compare)", len(inv))}
	}
	var missing, extra []string
	for _, r := range ref {
		if !slices.Contains(inv, r) {
			missing = append(missing, r)
		}
	}
	for _, i := range inv {
		if !slices.Contains(ref, i) {
			extra = append(extra, i)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return Verdict{"ARM devices", vPresent, fmt.Sprintf("all %d as on the SDS1102CML+", len(ref))}
	}
	return Verdict{"ARM devices", vOther, fmt.Sprintf("missing: %s; extra: %s", strings.Join(missing, "; "), strings.Join(extra, "; "))}
}
