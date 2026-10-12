// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Typed entry (the remote page and /api/sheets): numbers with SI prefixes and
// units, and serial match bytes as hex or quoted text.

// siPrefix is the multiplier of an SI prefix letter.
var siPrefix = map[rune]float64{
	'p': 1e-12, 'n': 1e-9, 'u': 1e-6, 'µ': 1e-6, 'm': 1e-3,
	'k': 1e3, 'K': 1e3, 'M': 1e6, 'G': 1e9,
}

// parseNumber reads a number such as "1.25V", "500u", "-20 mV", "2k",
// "115200", "50%", "10 div" or "0x55". unit is the row's unit ("V", "s",
// "Hz", "%", "div", "Bd" or ""); a different unit is an error. A percent row
// takes "50" or "50%" and returns 50. A comma is the decimal point when the
// text has no point and one comma ("2,5V", "115,2k": a European keyboard);
// before exactly three digits after a whole number ("57,600") it could be a
// thousands separator too, so that is refused rather than guessed.
// TRLC-LINKS: REQ-SDS-212
func parseNumber(text, unit string) (float64, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}
	if i := strings.IndexByte(s, ','); i >= 0 && !strings.Contains(s, ".") && strings.Count(s, ",") == 1 {
		if twoWays(s, i) {
			return 0, fmt.Errorf("the comma in %q reads two ways: type it without the comma, or with a point", text)
		}
		s = s[:i] + "." + s[i+1:]
	}
	if l := strings.ToLower(s); strings.HasPrefix(l, "0x") || strings.HasPrefix(l, "-0x") {
		neg := strings.HasPrefix(l, "-")
		v, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimPrefix(l, "-"), "0x"), 16, 64)
		if err != nil {
			return 0, fmt.Errorf("bad hex number %q", text)
		}
		if neg {
			v = -v
		}
		return float64(v), nil
	}
	// the numeric part: digits, sign, point and an exponent
	i := 0
scan:
	for i < len(s) {
		c := s[i]
		switch {
		case isDigit(c) || c == '.' || (c == '+' || c == '-') && i == 0:
			i++
		case (c == 'e' || c == 'E') && i > 0 && i+1 < len(s) &&
			(isDigit(s[i+1]) || (s[i+1] == '-' || s[i+1] == '+') && i+2 < len(s) && isDigit(s[i+2])):
			i += 2
		default:
			break scan
		}
	}
	num, rest := s[:i], strings.TrimSpace(s[i:])
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("not a number: %q", text)
	}
	if rest == "" || unitIs(rest, unit) {
		return v, nil
	}
	// an SI prefix, then nothing or the unit
	r := []rune(rest)
	if p, ok := siPrefix[r[0]]; ok {
		u := strings.TrimSpace(string(r[1:]))
		if u == "" || unitIs(u, unit) {
			return v * p, nil
		}
		return 0, fmt.Errorf("unit %q does not fit (want %s)", u, unitName(unit))
	}
	return 0, fmt.Errorf("unit %q does not fit (want %s)", rest, unitName(unit))
}

// twoWays: the comma at i stands after a whole number other than 0 and
// before exactly three digits, as in "57,600": a decimal point or a
// thousands separator.
// TRLC-LINKS: REQ-SDS-212
func twoWays(s string, i int) bool {
	whole := strings.TrimLeft(s[:i], "+-")
	if whole == "" || strings.Trim(whole, "0") == "" || strings.TrimLeft(whole, "0123456789") != "" {
		return false
	}
	n := 0
	for n < len(s)-i-1 && isDigit(s[i+1+n]) {
		n++
	}
	return n == 3
}

// unitIs reports whether u names the row's unit (with common spellings).
// TRLC-LINKS: REQ-SDS-212
func unitIs(u, unit string) bool {
	if unit == "" {
		return false
	}
	if strings.EqualFold(u, unit) {
		return true
	}
	switch unit {
	case "Bd":
		return strings.EqualFold(u, "baud") || strings.EqualFold(u, "bps") || strings.EqualFold(u, "b/s")
	case "s":
		return strings.EqualFold(u, "sec")
	case "V":
		return strings.EqualFold(u, "volt") || strings.EqualFold(u, "volts")
	}
	return false
}

// TRLC-LINKS: REQ-SDS-212
func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// unitName names a unit for an error message.
// TRLC-LINKS: REQ-SDS-212
func unitName(u string) string {
	if u == "" {
		return "a plain number"
	}
	return u
}

// parseMatchText reads serial match bytes: hex tokens ("55 AA 0F", "55AA",
// with the editor's wildcards ?? and A? and ERR) or quoted text ("\"Hi\r\n\"").
// It returns the editor's tokens; empty text is "any byte".
// TRLC-LINKS: REQ-SDS-214
func parseMatchText(text string) ([]string, error) {
	s := strings.TrimSpace(text)
	if s == "" || strings.EqualFold(s, "any") || strings.EqualFold(s, "any byte") {
		return nil, nil // no match bytes: any byte
	}
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		body, err := unescape(s[1 : len(s)-1])
		if err != nil {
			return nil, err
		}
		if len(body) == 0 {
			return nil, fmt.Errorf("empty text")
		}
		var toks []string
		for _, b := range []byte(body) {
			toks = append(toks, fmt.Sprintf("%02X", b))
		}
		if len(toks) > kbMaxTokens {
			return nil, fmt.Errorf("%d bytes: %d at most", len(toks), kbMaxTokens)
		}
		return toks, nil
	}
	bad := func(f string) error {
		return fmt.Errorf("bad byte %q: use hex pairs like 55 AA, ?? or ERR, or \"text\" in quotes", f)
	}
	var toks []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == ',' }) {
		u := strings.ToUpper(f)
		if u == "ERR" || u == "??" {
			toks = append(toks, u)
			continue
		}
		if len(u)%2 == 1 {
			return nil, bad(f)
		}
		for i := 0; i < len(u); i += 2 {
			t := u[i : i+2]
			for _, c := range t {
				if !strings.ContainsRune("0123456789ABCDEF?", c) {
					return nil, bad(f)
				}
			}
			toks = append(toks, t)
		}
	}
	if len(toks) > kbMaxTokens {
		return nil, fmt.Errorf("%d bytes: %d at most", len(toks), kbMaxTokens)
	}
	if _, _, err := compileTokens(toks); err != nil {
		return nil, err
	}
	return toks, nil
}

// unescape resolves \r \n \t \0 \\ \" \' and \xNN in quoted match text.
// TRLC-LINKS: REQ-SDS-214
func unescape(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(s) {
			return "", fmt.Errorf("text ends in a backslash")
		}
		switch s[i] {
		case 'r':
			b.WriteByte('\r')
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '0':
			b.WriteByte(0)
		case '\\', '"', '\'':
			b.WriteByte(s[i])
		case 'x':
			if i+2 >= len(s) {
				return "", fmt.Errorf(`\x needs two hex digits`)
			}
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return "", fmt.Errorf(`\x needs two hex digits`)
			}
			b.WriteByte(byte(v))
			i += 2
		default:
			return "", fmt.Errorf(`unknown escape \%c`, s[i])
		}
	}
	return b.String(), nil
}
