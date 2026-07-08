package lcd

import (
	"fmt"
	"open-sds/app/internal/analog"
	"strconv"
)

func g3(x float64) string { return strconv.FormatFloat(x, 'g', 3, 64) }

// siScale formats v at 3 significant figures with an SI prefix, NEVER emitting
// scientific notation. The 'g' verb switches to "1e+03" once a mantissa rounds
// to ≥1000 (e.g. a 999.9 ns period), which is unreadable on a scope. The
// boundary is rounding-aware — 0.9995·scale is where g3 rounds up to 1000 — so
// such a value PROMOTES to the next-larger prefix ("1.00 µs"). `units` is
// ordered high→low; the sign is preserved.
func siScale(v float64, units []siUnit) string {
	neg := ""
	a := v
	if a < 0 {
		neg, a = "-", -a
	}
	for _, u := range units {
		if a >= u.scale*0.9995 {
			return neg + g3(a/u.scale) + u.suffix
		}
	}
	last := units[len(units)-1]
	return neg + g3(a/last.scale) + last.suffix
}

func fmtVolt(v float64) string { return siScale(v, voltUnits) }

// cplTag returns the coupling suffix for the HUD, shown only when it is not the
// default DC (so the common case stays uncluttered): " AC" or " GND".
func cplTag(mode int) string {
	switch mode {
	case analog.CplAC:
		return " AC"
	case analog.CplGND:
		return " GND"
	}
	return ""
}

// vdivLabel formats a channel's volts/div at the probe tip. A probe factor
// >1 scales the electrical V/div and appends a "10x"/"100x" tag so the label
// matches what the operator actually measures.
func vdivLabel(vdivV, probe float64) string {
	if probe < 1 {
		probe = 1
	}
	s := fmtVolt(vdivV * probe)
	if probe != 1 {
		s += fmt.Sprintf(" %gx", probe)
	}
	return s
}

func fmtTdiv(s float64) string { return siScale(s, timeUnits) }

func fmtFreq(f float64) string { return siScale(f, freqUnits) }
