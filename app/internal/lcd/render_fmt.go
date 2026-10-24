// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"open-sds/app/internal/analog"
	"strconv"
)

// TRLC-LINKS: REQ-SDS-021
func g3(x float64) string { return strconv.FormatFloat(x, 'g', 3, 64) }

// siScale formats v at 3 significant figures with an SI prefix, NEVER emitting
// scientific notation. The 'g' verb switches to "1e+03" once a mantissa rounds
// to ≥1000 (e.g. a 999.9 ns period), which is unreadable on a scope. The
// boundary is rounding-aware — 0.9995·scale is where g3 rounds up to 1000 — so
// such a value PROMOTES to the next-larger prefix ("1.00 µs"). `units` is
// ordered high→low; the sign is preserved.
// TRLC-LINKS: REQ-SDS-021
func siScale(v float64, units []siUnit) string {
	var buf [24]byte
	return string(appendSI(buf[:0], v, units))
}

// appendSI appends siScale's text to b: one allocation for the label, none
// for the pieces.
// TRLC-LINKS: REQ-SDS-021
func appendSI(b []byte, v float64, units []siUnit) []byte {
	a := v
	if a < 0 {
		b, a = append(b, '-'), -a
	}
	u := units[len(units)-1]
	for _, c := range units {
		if a >= c.scale*0.9995 {
			u = c
			break
		}
	}
	b = strconv.AppendFloat(b, a/u.scale, 'g', 3, 64)
	return append(b, u.suffix...)
}

// TRLC-LINKS: REQ-SDS-021
func fmtVolt(v float64) string { return siScale(v, voltUnits) }

// cplTag returns the coupling suffix for the HUD, shown only when it is not the
// default DC (so the common case stays uncluttered): " AC" or " GND".
// TRLC-LINKS: REQ-SDS-021
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
// TRLC-LINKS: REQ-SDS-021
func vdivLabel(vdivV, probe float64) string {
	if probe < 1 {
		probe = 1
	}
	var buf [32]byte
	b := appendSI(buf[:0], vdivV*probe, voltUnits)
	if probe != 1 {
		b = append(b, ' ')
		b = strconv.AppendFloat(b, probe, 'g', -1, 64)
		b = append(b, 'x')
	}
	return string(b)
}

// TRLC-LINKS: REQ-SDS-021
func fmtTdiv(s float64) string { return siScale(s, timeUnits) }

// TRLC-LINKS: REQ-SDS-021
func fmtFreq(f float64) string { return siScale(f, freqUnits) }
