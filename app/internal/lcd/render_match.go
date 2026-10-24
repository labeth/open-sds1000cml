// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import "strconv"

// MatchView is the serial trigger's match editor (a full-screen keyboard),
// mirrored from panel.MatchEditorView.
type MatchView struct {
	Open     bool
	Set      string
	Keys     [][]string
	Row, Col int
	Toks     []string
	Cur      int
	Typing   bool
	Proto    string
	Msg      string
}

// drawMatchEditor fills the screen left of the F-key column with the match
// editor: the pattern with its cursor, an ASCII preview, the key grid with
// the highlighted key, and the knob hints. The F-key column (Keys, Type,
// Delete, OK, Cancel) is the sheet's.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-214
func drawMatchEditor(sf Surface, m MatchView) {
	if !m.Open {
		return
	}
	const areaW = W - 116 // left of the softkey column
	fillRect(sf, 0, 12, areaW, H-12, colBG)
	DrawText(sf, 6, 16, "SERIAL MATCH  "+m.Proto+"  keys: "+m.Set+"  "+strconv.Itoa(len(m.Toks))+"/32 units", colTrig, 1)

	// Pattern line: tokens at scale 2, the cursor a bar, the token being
	// typed underlined; scrolled so the cursor stays in view.
	const py, scale = 34, 2
	charW := TextWidth("0", scale)
	tokW := func(t string) int { return TextWidth(t, scale) + charW }
	// The first token from which the cursor fits: the width up to the
	// cursor, less each token dropped from the front.
	w := 0
	for i := 0; i < m.Cur && i < len(m.Toks); i++ {
		w += tokW(m.Toks[i])
	}
	start := 0
	for w >= areaW-60 && start < m.Cur {
		if start < len(m.Toks) {
			w -= tokW(m.Toks[start])
		}
		start++
	}
	fillRect(sf, 4, py-4, areaW-8, 24, colGrid)
	x := 10
	if start > 0 {
		DrawText(sf, 10, py, "..", colDim, scale)
		x += TextWidth("..", scale) + charW
	}
	if len(m.Toks) == 0 {
		DrawText(sf, x+6, py, "(any data)", colDim, scale)
	}
	cursorX := x
	for i := start; i < len(m.Toks); i++ {
		if i == m.Cur {
			cursorX = x
		}
		if x+tokW(m.Toks[i]) > areaW-8 {
			DrawText(sf, x, py, "..", colDim, scale)
			break
		}
		col := colInfo
		if m.Toks[i] == "ERR" {
			col = colTrig
		}
		DrawText(sf, x, py, m.Toks[i], col, scale)
		if m.Typing && i == m.Cur-1 {
			fillRect(sf, x, py+15, TextWidth(m.Toks[i], scale), 2, colTrig)
		}
		x += tokW(m.Toks[i])
		if i == len(m.Toks)-1 && m.Cur == len(m.Toks) {
			cursorX = x
		}
	}
	fillRect(sf, cursorX-charW/2-1, py-3, 2, 20, colTrig)

	// ASCII preview of the exact units ('.' for anything else).
	var buf [64]byte
	preview := append(buf[:0], "text: "...)
	for _, t := range m.Toks {
		v, ok := hexPair(t)
		if !ok || v < 0x20 || v >= 0x7f {
			v = '.'
		}
		preview = append(preview, v)
	}
	if len(m.Toks) > 0 {
		DrawText(sf, 10, 62, string(preview), colDim, 1)
	}

	// Key grid.
	const gy0, gy1 = 80, H - 44
	rows := len(m.Keys)
	cols := 0
	for _, r := range m.Keys {
		cols = max(cols, len(r))
	}
	if rows == 0 || cols == 0 {
		return
	}
	cw := (areaW - 8) / cols
	ch := min(56, (gy1-gy0)/rows)
	ks := 2
	if cw < 30 {
		ks = 1
	}
	for r, row := range m.Keys {
		for c, key := range row {
			x0, y0 := 4+c*cw, gy0+r*ch
			label := key
			if label == " " {
				label = "SP"
			}
			bg, fg := colGrid, colInfo
			if r == m.Row && c == m.Col {
				bg, fg = colTrig, colBG
			}
			fillRect(sf, x0+1, y0+1, cw-2, ch-2, bg)
			tw := TextWidth(label, ks)
			DrawText(sf, x0+(cw-tw)/2, y0+(ch-8*ks)/2, label, fg, ks)
		}
	}
	DrawText(sf, 6, H-36, "Adjust: key  push: type it   Position: cursor  push: clear   Menu On/Off: discard", colDim, 1)
	DrawText(sf, 6, H-24, "hex: 2 digits a byte, ? = any nibble   ?? any byte   ERR decode error", colDim, 1)
	if m.Msg != "" {
		DrawText(sf, 6, H-12, m.Msg, colTrig, 1)
	}
}

// hexPair parses a two-digit hex byte (either case), as ParseUint(t, 16, 8)
// on a two-character token.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-214
func hexPair(t string) (byte, bool) {
	if len(t) != 2 {
		return 0, false
	}
	hi, ok1 := hexNibble(t[0])
	lo, ok2 := hexNibble(t[1])
	return hi<<4 | lo, ok1 && ok2
}

// TRLC-LINKS: REQ-SDS-021, REQ-SDS-214
func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
