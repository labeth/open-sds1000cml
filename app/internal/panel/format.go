// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"strconv"
	"strings"
)

// siSteps are the SI prefixes the readouts use, largest first.
var siSteps = []struct {
	scale float64
	pre   string
}{{1e9, "G"}, {1e6, "M"}, {1e3, "k"}, {1, ""}, {1e-3, "m"}, {1e-6, "u"}, {1e-9, "n"}, {1e-12, "p"}}

// si formats v at three significant figures with an SI prefix and unit, the
// LCD's style ("1.25V", "500us", "-20mV"), never in exponent notation. The
// prefix boundary is rounding-aware: 999.96 reads "1kHz", not "1e+03Hz".
// TRLC-LINKS: REQ-SDS-212
func si(v float64, unit string) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "---"
	}
	if v == 0 {
		return "0" + unit
	}
	neg := ""
	a := v
	if a < 0 {
		neg, a = "-", -a
	}
	for _, p := range siSteps {
		if a >= p.scale*0.9995 {
			return neg + g3(a/p.scale) + p.pre + unit
		}
	}
	last := siSteps[len(siSteps)-1]
	return neg + g3(a/last.scale) + last.pre + unit
}

// si5 formats v at five significant figures with an SI prefix, a space and
// the unit ("2.0001 Mbit/s"), trailing zeros kept: the web's eng(v, unit, 5),
// for the figures both show (the BUS eye's bit rate).
// TRLC-LINKS: REQ-SDS-215
func si5(v float64, unit string) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "---"
	}
	if v == 0 {
		return "0 " + unit
	}
	neg, a := "", v
	if a < 0 {
		neg, a = "-", -a
	}
	p := siSteps[len(siSteps)-1]
	for _, s := range siSteps {
		if a >= s.scale*0.999995 {
			p = s
			break
		}
	}
	x := a / p.scale
	digits := max(0, int(math.Floor(math.Log10(x)))) // a value promoted to the next prefix (0.99999..) reads 1.0000
	return neg + strconv.FormatFloat(x, 'f', max(0, 4-digits), 64) + " " + p.pre + unit
}

// g3 is three significant figures without exponent or trailing zeros.
// TRLC-LINKS: REQ-SDS-212
func g3(x float64) string { return strconv.FormatFloat(x, 'g', 3, 64) }

// sgnSI is si with an explicit + on positive values (positions, offsets).
// TRLC-LINKS: REQ-SDS-212
func sgnSI(v float64, unit string) string {
	if v > 0 {
		return "+" + si(v, unit)
	}
	return si(v, unit)
}

// fmtPct formats a percentage: "50 %".
// TRLC-LINKS: REQ-SDS-212
func fmtPct(p float64) string { return strconv.FormatFloat(p, 'f', -1, 64) + " %" }

// fmtDiv formats a cursor position in divisions: "5.50 div".
// TRLC-LINKS: REQ-SDS-212
func fmtDiv(d float64) string { return strconv.FormatFloat(d, 'f', 2, 64) + " div" }

// cut shortens s to n characters with a trailing "..".
// TRLC-LINKS: REQ-SDS-212
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:max(1, n-2)] + ".."
}

// wrapText breaks s into lines of at most n characters at spaces.
// TRLC-LINKS: REQ-SDS-212
func wrapText(s string, n int) []string {
	if n > 0 && singleSpaced(s) {
		return wrapSpaced(s, n)
	}
	var out []string
	line := ""
	for _, w := range strings.Fields(s) {
		for len(w) > n {
			if line != "" {
				out = append(out, line)
				line = ""
			}
			out = append(out, w[:n])
			w = w[n:]
		}
		switch {
		case line == "":
			line = w
		case len(line)+1+len(w) <= n:
			line += " " + w
		default:
			out = append(out, line)
			line = w
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// singleSpaced reports ASCII text whose words are parted by single spaces,
// with none at either end: every line wrapText makes of it is a stretch of
// it.
// TRLC-LINKS: REQ-SDS-212
func singleSpaced(s string) bool {
	if s == "" || s[0] == ' ' || s[len(s)-1] == ' ' {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 0x80, c == '\t', c == '\n', c == '\v', c == '\f', c == '\r':
			return false
		case c == ' ' && s[i-1] == ' ':
			return false
		}
	}
	return true
}

// wrapSpaced is wrapText on single-spaced text: its lines cut from s, not
// built word by word.
// TRLC-LINKS: REQ-SDS-212
func wrapSpaced(s string, n int) []string {
	out := make([]string, 0, len(s)/n+1)
	a, b := 0, -1 // the line under way is s[a:b]; b < 0 none
	for ws := 0; ws < len(s); {
		we := ws
		for we < len(s) && s[we] != ' ' {
			we++
		}
		for we-ws > n {
			if b >= 0 {
				out = append(out, s[a:b])
				b = -1
			}
			out = append(out, s[ws:ws+n])
			ws += n
		}
		switch {
		case b < 0:
			a, b = ws, we
		case b-a+1+we-ws <= n:
			b = we
		default:
			out = append(out, s[a:b])
			a, b = ws, we
		}
		ws = we + 1
	}
	if b >= 0 {
		out = append(out, s[a:b])
	}
	return out
}

// hex2 is a byte as two upper-case hex digits.
// TRLC-LINKS: REQ-SDS-214
func hex2(b byte) string {
	const d = "0123456789ABCDEF"
	return string([]byte{d[b>>4], d[b&15]})
}
