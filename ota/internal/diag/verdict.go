// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	core "github.com/labeth/open-dso-core/supervisor/diag"
)

// probeReferenceText is the SDS1102CML+'s static balls as the probe image saw
// them (ball, name, class: low, high, toggles, or any for a line that keeps
// its last level), what another board's census is compared with.
//
//go:embed probe_reference.txt
var probeReferenceText string

// probeReference is each static ball's class on the SDS1102CML+.
// TRLC-LINKS: REQ-SDS-288
func probeReference() map[string]string {
	ref := map[string]string{}
	for line := range strings.SplitSeq(probeReferenceText, "\n") {
		if f := strings.Fields(line); len(f) == 3 && !strings.HasPrefix(line, "#") {
			ref[f[0]] = f[2]
		}
	}
	return ref
}

// staticClass is a ball's class with the legs off, as the reference names
// them: toggles, low (low with the weak pull-up: driven low), or high (high
// with it: driven high, pulled up or open; without the pull-up an open ball
// reads either level, so the two are one class).
// TRLC-LINKS: REQ-SDS-288
func staticClass(pull, open BallState) string {
	switch {
	case pull.Edges > 0 || open.Edges > 0:
		return "toggles"
	case pull.Live:
		return "high"
	}
	return "low"
}

// Verdict is what the census says about one part of the board, in the
// archive's form.
type Verdict = core.Verdict

// Archive is the stick's archive.
type Archive = core.Archive

// The census's verdicts.
const (
	vPresent = "present"
	vAbsent  = "absent"
	vPartial = "partial"
	vOther   = "other"
)

// hz is a 1 ms gate's edge count as a rate.
// TRLC-LINKS: REQ-SDS-288
func hz(edges uint32) string {
	f := float64(edges) * 1000
	switch {
	case f >= 1e6:
		return fmt.Sprintf("%.3f MHz", f/1e6)
	case f >= 1e3:
		return fmt.Sprintf("%.0f kHz", f/1e3)
	}
	return fmt.Sprintf("%.0f Hz", f)
}

// judge holds the census the verdicts are drawn from: main is the pull-up
// build's when it ran (an open lane reads its pull-up), else the other.
type judge struct {
	balls            []Ball
	open, pull, main Census
	both             bool // both builds ran
	vs               []Verdict
}

// add appends a verdict.
// TRLC-LINKS: REQ-SDS-288
func (j *judge) add(part, v, f string, args ...any) {
	j.vs = append(j.vs, Verdict{Part: part, Verdict: v, Detail: fmt.Sprintf(f, args...)})
}

// lanesOf is the indexes of a converter core's lanes.
// TRLC-LINKS: REQ-SDS-288
func (j *judge) lanesOf(core string) []int {
	var is []int
	for i, b := range j.balls {
		if strings.HasPrefix(b.Name, core+".") {
			is = append(is, i)
		}
	}
	return is
}

// verdicts judges each part of the SDS1102CML+'s population from the census
// without (open) and with (pull) the weak pull-ups: the reference clock and
// the encode PLL, each converter core (its eight lanes move when its leg
// clocks it through the offset sweep), each channel's front end (its offset
// DAC moves its converters), and the static balls (SRAM, panel, the others,
// the unused balls and clock inputs) against the SDS1102CML+'s.
// TRLC-LINKS: REQ-SDS-288
func verdicts(balls []Ball, open, pull Census) []Verdict {
	ran := func(c Census) bool { return c.Error == "" && len(c.Off) == len(balls) }
	j := &judge{balls: balls, open: open, pull: pull, main: pull, both: ran(open) && ran(pull)}
	if !ran(pull) {
		j.main = open
	}
	if !ran(j.main) {
		j.add("probe image", vAbsent, "no census: %s / %s", open.Error, pull.Error)
		return j.vs
	}
	j.add("probe image", vPresent, "census without the pull-ups: %s; with them: %s", orOK(open.Error), orOK(pull.Error))
	j.clocks()
	present := j.cores()
	j.channels(present)
	j.statics()
	return j.vs
}

// clocks judges the 100 MHz reference on M2 and the encode PLL on it.
// TRLC-LINKS: REQ-SDS-288
func (j *judge) clocks() {
	m2 := j.main.Off[len(j.balls)-1].Edges
	f := float64(m2) * 1000
	switch {
	case m2 == 0:
		j.add("reference clock (M2)", vAbsent, "no edges on M2")
	case f > 98e6 && f < 102e6:
		j.add("reference clock (M2)", vPresent, "%s", hz(m2))
	default:
		j.add("reference clock (M2)", vOther, "%s, not 100 MHz", hz(m2))
	}
	if j.main.Locked {
		j.add("encode PLL", vPresent, "locked on M2")
	} else {
		j.add("encode PLL", vAbsent, "not locked: the encode legs do not run")
	}
}

// cores judges each converter core and returns those present.
// TRLC-LINKS: REQ-SDS-288
func (j *judge) cores() map[string]bool {
	present := map[string]bool{}
	for _, core := range []string{"E1.CH1", "E1.CH2", "E2.CH1", "E2.CH2", "E3.CH1", "E3.CH2", "E4.CH1", "E4.CH2", "E5.CH1", "E5.CH2"} {
		lanes := j.lanesOf(core)
		leg := slices.Index(legCore[:], core)
		own, sweep := bitsMoved(j.main.Leg[leg], lanes), bitsMoved(j.main.Moved, lanes)
		part := "converter " + core
		switch {
		case len(own) == len(lanes):
			present[core] = true
			d := fmt.Sprintf("its %d lanes move with its encode leg %d", len(lanes), leg)
			if foreign := j.foreign(leg, lanes); len(foreign) > 0 {
				d += fmt.Sprintf("; with that leg also %v", foreign)
			}
			j.add(part, vPresent, "%s", d)
		case len(sweep) == 0:
			j.absentCore(part, leg, lanes)
		default:
			j.add(part, vPartial, "bits %v move with its leg, %v in the sweep with every leg", own, sweep)
		}
	}
	return present
}

// foreign is the balls outside lanes that moved with leg alone, but for those
// that move with the legs off too (a clock moves with every leg).
// TRLC-LINKS: REQ-SDS-288
func (j *judge) foreign(leg int, lanes []int) []string {
	var out []string
	for i, m := range j.main.Leg[leg] {
		if m && !slices.Contains(lanes, i) && (!j.main.Off[i].Ever0 || !j.main.Off[i].Ever1) {
			out = append(out, j.balls[i].Ball)
		}
	}
	return out
}

// absentCore judges a core none of whose lanes moved with every leg on: one
// another leg moves is wired otherwise, else it is not fitted.
// TRLC-LINKS: REQ-SDS-288
func (j *judge) absentCore(part string, leg int, lanes []int) {
	for l := range j.main.Leg {
		if len(bitsMoved(j.main.Leg[l], lanes)) > 0 {
			j.add(part, vOther, "its lanes move with leg %d (%s on the SDS1102CML+), not leg %d", l, legCore[l], leg)
			return
		}
	}
	d := "none of its lanes move; with the pull-up they read " + levels(j.pull.Off, lanes)
	if j.both {
		d += ", without " + levels(j.open.Off, lanes)
	}
	j.add(part, vAbsent, "%s", d)
}

// channels judges each channel's front end and offset DAC by the lanes of
// its present converters that follow its offset.
// TRLC-LINKS: REQ-SDS-288
func (j *judge) channels(present map[string]bool) {
	for ch := range 2 {
		var lanes []int
		for core := range present {
			if strings.HasSuffix(core, fmt.Sprintf("CH%d", ch+1)) {
				lanes = append(lanes, j.lanesOf(core)...)
			}
		}
		part := fmt.Sprintf("channel %d front end and offset DAC", ch+1)
		n := len(bitsMoved(j.main.Chan[ch], lanes))
		cross := 0
		for i, m := range j.main.Chan[ch] {
			if m && !slices.Contains(lanes, i) && j.balls[i].Group == "adc" {
				cross++
			}
		}
		switch {
		case len(lanes) == 0:
			j.add(part, vAbsent, "no converter of channel %d is present", ch+1)
		case n == len(lanes):
			j.add(part, vPresent, "its offset DAC moves all %d lanes of its converters%s", n, crossText(cross))
		case n == 0:
			j.add(part, vAbsent, "its offset DAC moves none of its converters' %d lanes%s", len(lanes), crossText(cross))
		default:
			j.add(part, vPartial, "its offset DAC moves %d of its converters' %d lanes%s", n, len(lanes), crossText(cross))
		}
	}
}

// statics judges each group of static balls against the SDS1102CML+'s.
// TRLC-LINKS: REQ-SDS-288
func (j *judge) statics() {
	reference := probeReference()
	for _, g := range []struct{ group, part string }{
		{"sram", "SRAM bus (the app run says whether it works)"},
		{"panel", "front panel lines"},
		{"other", "other balls"},
		{"unused", "balls unused on the SDS1102CML+"},
		{"clkin", "clock inputs unused on the SDS1102CML+"},
	} {
		var diffs []string
		n := 0
		for i, b := range j.balls {
			if b.Group != g.group {
				continue
			}
			n++
			c := staticClass(j.main.Off[i], stateOr(j.open.Off, i))
			if r := reference[b.Ball]; c != r && (r != "any" || c == "toggles") {
				diffs = append(diffs, fmt.Sprintf("%s %s %s (%s on the SDS1102CML+)", b.Ball, b.Name, c, r))
			}
		}
		if len(diffs) == 0 {
			j.add(g.part, vPresent, "all %d balls as on the SDS1102CML+", n)
		} else {
			j.add(g.part, vOther, "%d of %d balls differ from the SDS1102CML+: %s", len(diffs), n, strings.Join(diffs, "; "))
		}
	}
}

// bitsMoved is the bit numbers, within lanes, of the balls that moved.
// TRLC-LINKS: REQ-SDS-288
func bitsMoved(moved []bool, lanes []int) []int {
	var out []int
	for b, i := range lanes {
		if i < len(moved) && moved[i] {
			out = append(out, b)
		}
	}
	return out
}

// levels is the lanes' live levels, bit 0 first.
// TRLC-LINKS: REQ-SDS-288
func levels(st []BallState, lanes []int) string {
	var b strings.Builder
	for _, i := range lanes {
		if i < len(st) && st[i].Live {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

// stateOr is st[i], or the zero state when the census has none.
// TRLC-LINKS: REQ-SDS-288
func stateOr(st []BallState, i int) BallState {
	if i < len(st) {
		return st[i]
	}
	return BallState{}
}

// crossText notes lanes of the other channel's converters that moved too.
// TRLC-LINKS: REQ-SDS-288
func crossText(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("; it also moves %d lanes of the other channel's converters", n)
}

// orOK is err, or "ok".
// TRLC-LINKS: REQ-SDS-288
func orOK(err string) string {
	if err == "" {
		return "ok"
	}
	return err
}

// verdictText is the verdicts as the report's text, then every ball.
// TRLC-LINKS: REQ-SDS-288
func verdictText(vs []Verdict, balls []Ball, open, pull Census) string {
	var b strings.Builder
	b.WriteString("# The board's population as the probe image saw it (present / absent / partial / other)\n")
	for _, v := range vs {
		fmt.Fprintf(&b, "%-44s %-8s %s\n", v.Part, v.Verdict, v.Detail)
	}
	b.WriteString("\n# ball name | legs off without pull-up: level, edges | with: level, edges | legs on: edges | moved in the sweep | legs that moved it\n")
	for i, bl := range balls {
		o, p, on := stateOr(open.Off, i), stateOr(pull.Off, i), stateOr(pull.On, i)
		var legs []int
		for l := range pull.Leg {
			if i < len(pull.Leg[l]) && pull.Leg[l][i] {
				legs = append(legs, l)
			}
		}
		fmt.Fprintf(&b, "%-4s %-16s | %d %8d | %d %8d | %8d | %-5v | %v\n", bl.Ball, bl.Name, b2i(o.Live), o.Edges, b2i(p.Live), p.Edges, on.Edges,
			i < len(pull.Moved) && pull.Moved[i], legs)
	}
	return b.String()
}

// b2i is 1 for true.
// TRLC-LINKS: REQ-SDS-288
func b2i(v bool) int {
	if v {
		return 1
	}
	return 0
}
