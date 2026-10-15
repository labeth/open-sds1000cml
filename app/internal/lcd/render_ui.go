// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"math"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// The task-workspace UI on the LCD (ADR-TASK-WORKSPACES): a 30 px pictogram
// rail at the right edge with no menu open, each workspace's task pane
// above the plot, and a full-screen settings sheet or the BUS byte picker
// with the F-key text column when a menu is open. Flat 1 px lines and
// fills, the 5x7 font and the palette only.

// Rail geometry (REQ-SDS-211): one 28x34 tab per F-key row.
const (
	railW = 30
	railX = W - railW
)

// drawUI draws the UI over the base render.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func drawUI(sf Surface, f *engine.Frame, hud HUD, p plotArea) {
	ui := &hud.UI
	if ui.Sheet != nil {
		drawSheet(sf, f, hud)
	} else {
		switch {
		case ui.Scope != nil:
			drawScopeChrome(sf, p, f, hud)
		case ui.Picker != nil:
			drawPickerPane(sf, p, ui.Picker)
		case ui.Bus != nil:
			drawBusPane(sf, p, ui.Bus)
		case ui.Eye != nil:
			drawEyePlot(sf, p, hud, ui.Eye)
			drawEyePane(sf, p, f, hud, ui.Eye)
		case ui.Spec != nil:
			drawSpecPane(sf, p, f, hud, ui.Spec)
		case ui.Meas != nil:
			drawMeasPane(sf, p, ui.Meas)
		case ui.Cmp != nil:
			drawCmpPane(sf, p, ui.Cmp)
		}
	}
	if hud.Match.Open {
		drawMatchEditor(sf, hud.Match)
	}
	if ui.Keys != nil {
		drawKeys(sf, ui.Keys)
	}
	if ui.Rail {
		drawRail(sf, ui.WS)
		if n := ui.RailNote; n != nil {
			drawRailNote(sf, n)
		}
	}
}

// ---- the rail ----

// drawPicto draws a workspace's 22x14 pictogram: a wave (SCOPE), packets on a
// strip (BUS), falling spectral lines (SPECTRUM), two cursors and the
// distance between them (MEASURE), a wave inside a dotted mask band
// (COMPARE).
// TRLC-LINKS: REQ-SDS-211
func drawPicto(sf Surface, ws wsview.WS, x, y int, fg, fg2 uint16) {
	wave := func(y0 int, amp float64, col uint16, dotted bool) {
		px, py := x, y0
		for i := 0; i <= 21; i++ {
			xx := x + i
			yy := y0 - int(math.Round(amp*math.Sin(float64(i)/21*2*math.Pi)))
			switch {
			case dotted:
				if i%2 == 0 {
					sf.SetPixel(xx, yy, col)
				}
			case i > 0:
				drawLine(sf, px, py, xx, yy, col)
			}
			px, py = xx, yy
		}
	}
	switch ws {
	case wsview.Scope:
		wave(y+7, 6, fg, false)
	case wsview.Bus:
		for k := 0; k < 3; k++ {
			strokeRect(sf, x+k*8, y+2, 6, 8, fg)
		}
		fillRect(sf, x+9, y+3, 4, 6, fg)
		hline(sf, x, x+21, y+12, fg2)
	case wsview.Spectrum:
		for k, h := range []int{14, 11, 8, 6, 4, 3} {
			fillRect(sf, x+k*4, y+14-h, 2, h, fg)
		}
	case wsview.Measure:
		vline(sf, x+1, y, y+13, fg2)
		vline(sf, x+20, y, y+13, fg2)
		hline(sf, x+5, x+16, y+7, fg)
		for d := 0; d <= 2; d++ { // arrow heads
			vline(sf, x+3+d, y+7-d, y+7+d, fg)
			vline(sf, x+18-d, y+7-d, y+7+d, fg)
		}
	case wsview.Compare:
		wave(y+2, 3, fg2, true)
		wave(y+12, 3, fg2, true)
		wave(y+7, 3, fg, false)
	}
}

// drawRail draws the five workspace tabs beside F1..F5, the active one
// inverted in amber.
// TRLC-LINKS: REQ-SDS-211
func drawRail(sf Surface, active wsview.WS) {
	fillRect(sf, railX, 12, railW, H-12, colBG)
	for i := 0; i < wsview.Count; i++ {
		x0, w, y0, h := railX+1, railW-2, softkeyY[i]-17, 34
		fg, fg2, tag := colInfo, colDim, colDim
		if wsview.WS(i) == active {
			fillRect(sf, x0, y0, w, h, colTrig)
			fg, fg2, tag = colBG, colBG, colBG
		} else {
			strokeRect(sf, x0, y0, w, h, colGrid)
		}
		drawPicto(sf, wsview.WS(i), x0+3, y0+4, fg, fg2)
		t := wsview.Tags[i]
		DrawText(sf, x0+w/2-TextWidth(t, 1)/2, y0+23, t, tag, 1)
	}
}

// drawRailNote is the short label beside the rail after a workspace key:
// "F3 SPECTRUM   again: FFT / Spectrogram / Bode".
// TRLC-LINKS: REQ-SDS-211
func drawRailNote(sf Surface, n *wsview.Note) {
	w := TextWidth(n.Head, 1) + TextWidth(n.Text, 1) + 26
	x, y := railX-3-w, softkeyY[clampI(int(n.WS), 0, 4)]-8
	fillRect(sf, x, y, w, 17, colMenuBg)
	strokeRect(sf, x, y, w, 17, colTrig)
	DrawText(sf, x+7, y+5, n.Head, colTrig, 1)
	DrawText(sf, x+7+TextWidth(n.Head, 1)+12, y+5, n.Text, colInfo, 1)
}

// clampI clamps v into [lo, hi].
// TRLC-LINKS: REQ-SDS-211
func clampI(v, lo, hi int) int { return min(max(v, lo), hi) }

// ---- the F-key text column (a menu is open) ----

// drawKeys draws today's softkey column with the menu's choices: the title,
// then each choice on its key's row (label over value), the current one
// filled amber.
// TRLC-LINKS: REQ-SDS-212
func drawKeys(sf Surface, k *wsview.Keys) {
	const mw = 116
	x0 := W - mw
	fillRect(sf, x0, k.Top, mw, H-2-k.Top, colMenuBg)
	vline(sf, x0, k.Top, H-3, colTrig)
	maxC := (mw - 9) / 6
	fit := func(s string) string {
		if len(s) > maxC {
			return s[:maxC-2] + ".."
		}
		return s
	}
	DrawText(sf, x0+6, k.Top+4, fit(k.Title), colTrig, 1)
	for i, it := range k.Items {
		if it.Label == "" && it.Value == "" {
			continue
		}
		sy := softkeyY[i] - 7
		lc, vc := colInfo, colTrig
		if it.Dim {
			lc, vc = colDim, colDim
		}
		if it.Sel {
			fillRect(sf, x0+3, sy-2, mw-6, 19, colTrig)
			lc, vc = colBG, colBG
		}
		DrawText(sf, x0+6, sy, fit(it.Label), lc, 1)
		if it.Value != "" {
			DrawText(sf, x0+6, sy+8, fit(it.Value), vc, 1)
		}
	}
}

// ---- the pane frame ----

// drawPaneFrame clears the pane (rows 12 .. plotTop-1) and draws its header:
// the amber title, the caption, a right-aligned hint; and the axis rule
// above the plot.
// TRLC-LINKS: REQ-SDS-211
func drawPaneFrame(sf Surface, p plotArea, pn *wsview.Pane) {
	fillRect(sf, 0, 12, p.w, p.y0-12, colBG)
	fillRect(sf, 0, 12, p.w, 12, colMenuBg)
	DrawText(sf, 4, 14, pn.Title, colTrig, 1)
	x := 4 + TextWidth(pn.Title, 1) + 10
	room := p.w - 8 - x
	if pn.Right != "" {
		DrawTextRight(sf, p.w-4, 14, pn.Right, colDim, 1)
		room -= TextWidth(pn.Right, 1) + 12
	}
	if pn.Caption != "" && room/6 > 4 {
		DrawText(sf, x, 14, cutText(pn.Caption, room/6), colorOf(pn.CapCol), 1)
	}
	hline(sf, 0, p.w-1, p.y0-1, colAxis)
	drawToolLine(sf, p, pn)
}

// drawToolLine is "Adjust: packet  push: pick bytes ...   F2 again: Eye
// diagram": the knob names in text, their jobs dim, the key-again view in
// amber at the right end.
// TRLC-LINKS: REQ-SDS-213
func drawToolLine(sf Surface, p plotArea, pn *wsview.Pane) {
	y := p.y0 - 12
	x := 4
	for _, s := range pn.Tool {
		DrawText(sf, x, y, s.Key+":", colInfo, 1)
		x += TextWidth(s.Key+":", 1)
		DrawText(sf, x+4, y, s.Job, colDim, 1)
		x += 4 + TextWidth(s.Job, 1) + 16
	}
	if pn.Again != "" {
		DrawTextRight(sf, p.w-4, y, pn.Again, colTrig, 1)
	}
}

// cutText shortens s to n characters with "..".
// TRLC-LINKS: REQ-SDS-211
func cutText(s string, n int) string {
	if n < 3 || len(s) <= n {
		return s
	}
	return s[:n-2] + ".."
}

// ---- SCOPE ----

// drawScopeChrome draws SCOPE's name in the top bar (clear of the trigger
// pointer) and the Adjust tag in the bottom row.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-213
func drawScopeChrome(sf Surface, p plotArea, f *engine.Frame, hud HUD) {
	tx := trigX(p, f, hud)
	x := 410
	if tx > 398 && tx < 448 {
		x = tx + 8
		if tx >= 560-38 {
			x = tx - 38
		}
	}
	DrawText(sf, x, 2, "SCOPE", colTrig, 1)
	sp := hud.UI.Scope
	if sp == nil || sp.AdjustTag == "" {
		return
	}
	col := colDim
	if sp.Hot {
		col = colTrig
	}
	y := H - 9
	if hud.DecProto != 0 { // above the decode strip's label
		y = H - 47
	}
	fillRect(sf, 158, y-2, TextWidth(sp.AdjustTag, 1)+4, 11, colBG)
	DrawText(sf, 160, y, sp.AdjustTag, col, 1)
}

// ---- BUS ----

// The packet list's fixed columns: the number, a hex box of 16 cells (an 18
// px pitch, a gap after the 8th), a string box with the same bytes as text,
// the byte count and the match.
const (
	hexX     = 30
	hexIn    = 35
	cellW    = 18
	perRow   = 16
	midGap   = 6
	listY    = 37
	listN    = 8
	busToolY = 124
)

// TRLC-LINKS: REQ-SDS-211, REQ-SDS-213
func cellX(k int) int {
	if k >= 8 {
		return hexIn + k*cellW + midGap
	}
	return hexIn + k*cellW
}

var (
	strX   = cellX(perRow-1) + 12 + 6
	strIn  = strX + 6
	strEnd = strIn + perRow*6 + 5
	bytesR = strEnd + 34
	addrX  = strEnd + 46
	matchX = addrX + 66 // ADDR holds a CAN extended ID: "1FFFFFFF W"
)

// drawBusPane draws the packet list: a hex dump with a separate string box.
// TRLC-LINKS: REQ-SDS-214
func drawBusPane(sf Surface, p plotArea, b *wsview.BusPane) {
	drawPaneFrame(sf, p, &b.Pane)
	if b.Off {
		for i, s := range b.Msg {
			if i == 0 {
				DrawText(sf, 16, 34, s.Text, colorOf(s.Col), 2)
				continue
			}
			DrawText(sf, 16, 60+(i-1)*14, cutText(s.Text, (p.w-24)/6), colorOf(s.Col), 1)
		}
		return
	}
	if b.Error != "" {
		DrawText(sf, 16, 40, cutText(b.Error, (p.w-24)/6), colStale, 1)
		DrawText(sf, 16, 54, "Use a faster time/div, or check the protocol on the BUS sheet (Utility).", colDim, 1)
		return
	}
	DrawTextRight(sf, 24, 26, "#", colDim, 1)
	DrawText(sf, hexIn, 26, "HEX", colDim, 1)
	DrawText(sf, strIn, 26, "STRING", colDim, 1)
	DrawTextRight(sf, bytesR, 26, "BYTES", colDim, 1)
	DrawText(sf, addrX, 26, "ADDR", colDim, 1)
	DrawText(sf, matchX, 26, "MATCH", colDim, 1)
	strokeRect(sf, hexX, listY-3, strX-hexX+1, listN*10+4, colAxis)
	strokeRect(sf, strX, listY-3, strEnd-strX, listN*10+4, colAxis)
	if b.Empty != "" {
		DrawText(sf, hexIn, listY, b.Empty, colDim, 1)
	}
	for r, ln := range b.Lines {
		y := listY + r*10
		if ln.Sel {
			fillRect(sf, 0, y-1, hexX, 10, colGrid)
			fillRect(sf, hexX+1, y-1, strX-hexX-1, 10, colGrid)
			fillRect(sf, strX+1, y-1, strEnd-strX-2, 10, colGrid)
			fillRect(sf, strEnd, y-1, p.w-strEnd, 10, colGrid)
		}
		if ln.Num > 0 {
			numCol := colInfo
			if ln.Sel {
				DrawText(sf, 2, y, ">", colTrig, 1)
				numCol = colTrig
			}
			DrawTextRight(sf, 24, y, itoa(ln.Num), numCol, 1)
			DrawTextRight(sf, bytesR, y, itoa(ln.Bytes), colDim, 1)
			DrawText(sf, addrX, y, cutText(ln.Addr, (matchX-addrX)/6-1), colInfo, 1)
			x := matchX
			if ln.Fault != "" { // the packet's fault first, in red, as the web marks it
				DrawText(sf, x, y, ln.Fault, colStale, 1)
				x += (len(ln.Fault) + 1) * 6
			}
			if ln.Match != "" {
				DrawText(sf, x, y, cutText(ln.Match, (p.w-x-12)/6), colTrig, 1)
			} else if ln.Cut != "" {
				DrawText(sf, x, y, cutText(ln.Cut, (p.w-x-12)/6), colDim, 1)
			}
		} else {
			DrawTextRight(sf, 24, y, "+", colDim, 1)
		}
		for c, h := range ln.Hex {
			col := colInfo
			if (c < len(ln.Err) && ln.Err[c]) || ln.Fault != "" {
				col = colStale
			}
			DrawText(sf, cellX(c), y, h, col, 1)
			if c < len(ln.Str) {
				DrawText(sf, strIn+c*6, y, ln.Str[c:c+1], col, 1)
			}
		}
		if ln.More {
			DrawText(sf, cellX(perRow-1), y, "..", colDim, 1)
			DrawText(sf, strIn+(perRow-1)*6, y, ">", colDim, 1)
		}
	}
	if b.Up {
		DrawText(sf, p.w-10, listY, "^", colDim, 1)
	}
	if b.Dn {
		DrawText(sf, p.w-10, listY+(listN-1)*10, "v", colDim, 1)
	}
}

// itoa formats a small integer.
// TRLC-LINKS: REQ-SDS-214
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for ; n > 0; n /= 10 {
		i--
		b[i] = byte('0' + n%10)
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// drawTokens draws picker tokens at scale 2 in a window around cur: the
// current one filled amber, the next three underlined, a bar before each
// message start.
// TRLC-LINKS: REQ-SDS-214
func drawTokens(sf Surface, x, y, xmax int, toks []wsview.PickTok, cur int) {
	const per = 11
	n := len(toks)
	i0 := clampI(cur-3, 0, max(0, n-per))
	if i0 > 0 {
		DrawText(sf, x-12, y+4, "<", colDim, 1)
	}
	for i := i0; i < min(n, i0+per); i++ {
		t := toks[i]
		w := TextWidth(t.Text, 2)
		if x+w > xmax {
			break
		}
		if t.Start && i > i0 {
			vline(sf, x-7, y-4, y+19, colAxis)
		}
		col := colInfo
		if t.Text == "ERR" { // a framing error, as the packet list writes it
			col = colStale
		}
		switch {
		case i == cur:
			fillRect(sf, x-3, y-3, w+6, 22, colTrig)
			col = colBG
		case i > cur && i < cur+4:
			hline(sf, x-1, x+w, y+18, colDim)
		}
		DrawText(sf, x, y, t.Text, col, 2)
		x += w + 14
	}
	if i0+per < n {
		DrawText(sf, min(x, xmax-6), y+4, ">", colDim, 1)
	}
}

// drawPickerPane draws the BUS pane turned into the amber-framed byte picker.
// TRLC-LINKS: REQ-SDS-214
func drawPickerPane(sf Surface, p plotArea, pk *wsview.Picker) {
	pn := wsview.Pane{Title: "PICK BYTES", Caption: pk.Caption, Right: "Menu On/Off: back"}
	fillRect(sf, 0, 12, p.w, p.y0-12, colBG)
	fillRect(sf, 0, 12, p.w, 12, colMenuBg)
	DrawText(sf, 4, 14, pn.Title, colTrig, 1)
	DrawText(sf, 4+TextWidth(pn.Title, 1)+10, 14, pn.Caption, colInfo, 1)
	DrawTextRight(sf, p.w-4, 14, pn.Right, colDim, 1)
	strokeRect(sf, 0, 24, p.w, p.y0-25, colTrig)
	drawTokens(sf, 16, 36, p.w-12, pk.Toks, pk.Cur)
	for i, l := range pk.Lines {
		DrawText(sf, 16, 66+i*12, cutText(l.Text, (p.w-24)/6), colorOf(l.Col), 1)
	}
	DrawText(sf, 16, 104, pk.Now, colTrig, 1)
}

// ---- the eye ----

// eyeShades are four density shades of a channel colour over the screen
// colour, dim to full (flat fills only).
// TRLC-LINKS: REQ-SDS-214
func eyeShades(ch int) [4]uint16 {
	c := colC1
	if ch == 1 {
		c = colC2
	}
	mix := func(f float64) uint16 {
		r1, g1, b1 := unpack565(colBG)
		r2, g2, b2 := unpack565(c)
		l := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*f) }
		return rgb(l(r1, r2), l(g1, g2), l(b1, b2))
	}
	return [4]uint16{mix(0.3), mix(0.52), mix(0.76), c}
}

// unpack565 is the 8-bit RGB of an RGB565 colour.
// TRLC-LINKS: REQ-SDS-214
func unpack565(c uint16) (r, g, b uint8) {
	return uint8(c>>11&0x1f) << 3, uint8(c>>5&0x3f) << 2, uint8(c&0x1f) << 3
}

// drawEyePlot draws the eye in the plot: two UI around the bit centre, the
// fold's density in four shades, the opening and the two cursors.
// TRLC-LINKS: REQ-SDS-215
func drawEyePlot(sf Surface, p plotArea, hud HUD, e *wsview.EyePane) {
	cols := e.Cols
	if cols <= 0 {
		cols = 384
	}
	quarter := cols / 2 // 0.5 UI in pixels (2 px per cell)
	x0 := (p.w - 2*cols) / 2
	y1, eh := p.y0, H-p.y0-12
	for k := 0; k <= 4; k++ {
		col := colGrid
		if k == 2 {
			col = colAxis
		}
		vline(sf, x0+k*quarter, y1, y1+eh-1, col)
	}
	for r := 0; r <= 8; r++ {
		col := colGrid
		if r == 4 {
			col = colAxis
		}
		hline(sf, x0, x0+2*cols, y1+r*(eh-1)/8, col)
	}
	for k, t := range []string{"-1 UI", "-0.5", "0", "+0.5", "+1 UI"} {
		xc := x0 + k*quarter
		switch k {
		case 0:
			DrawText(sf, xc+2, H-10, t, colDim, 1)
		case 4:
			DrawTextRight(sf, xc-1, H-10, t, colDim, 1)
		default:
			DrawText(sf, xc-TextWidth(t, 1)/2, H-10, t, colDim, 1)
		}
	}
	if e.Why != "" || len(e.Grid) < e.Cols*e.Rows || e.Rows == 0 {
		why := e.Why
		if why == "" {
			why = "Folding the bit stream..."
		}
		w := TextWidth(why, 1) + 12
		fillRect(sf, x0+cols-w/2, y1+eh/2-8, w, 16, colBG)
		DrawText(sf, x0+cols-TextWidth(why, 1)/2, y1+eh/2-4, why, colDim, 1)
		return
	}
	sh := eyeShades(e.Ch)
	mx := float64(max(e.Max, 1))
	t1, t2, t3 := mx*0.015, mx*0.08, mx*0.35
	for r := 0; r < e.Rows; r++ {
		y := y1 + 2*r
		if y+1 >= H-12 {
			break
		}
		for c := 0; c < e.Cols; c++ {
			h := float64(e.Grid[r*e.Cols+c])
			if h == 0 {
				continue
			}
			col := sh[0]
			switch {
			case h >= t3:
				col = sh[3]
			case h >= t2:
				col = sh[2]
			case h >= t1:
				col = sh[1]
			}
			fillRect(sf, x0+2*c, y, 2, 2, col)
		}
	}
	if e.HasBox {
		xa, xb := x0+2*e.Box[0], x0+2*e.Box[2]
		ya, yb := y1+2*e.Box[1], y1+2*e.Box[3]
		if xb > xa && yb > ya {
			dashRect(sf, xa, ya, xb-xa+1, yb-ya+1, colTrig)
			fillRect(sf, xa+3, ya+3, TextWidth("EYE OPENING", 1)+4, 10, colBG)
			DrawText(sf, xa+5, ya+4, "EYE OPENING", colTrig, 1)
		}
	}
	xs := x0 + 2*e.SampleCol
	yt := y1 + 2*e.ThreshRow
	vdash(sf, xs, y1, y1+eh-1, colCursor)
	hdash(sf, x0, x0+2*cols, yt, colCursor)
	DrawText(sf, xs+3, y1+3, "S", colCursor, 1)
	if yt >= y1 && yt < y1+eh-10 {
		DrawText(sf, x0+3, yt+2, "T", colCursor, 1)
	}
}

// dashRect draws a dashed rectangle (3 on, 2 off).
// TRLC-LINKS: REQ-SDS-215
func dashRect(sf Surface, x, y, w, h int, c uint16) {
	for i := 0; i < w; i++ {
		if i%5 < 3 {
			sf.SetPixel(x+i, y, c)
			sf.SetPixel(x+i, y+h-1, c)
		}
	}
	for i := 0; i < h; i++ {
		if i%5 < 3 {
			sf.SetPixel(x, y+i, c)
			sf.SetPixel(x+w-1, y+i, c)
		}
	}
}

// drawEyePane draws the eye's figures beside a live time trace.
// TRLC-LINKS: REQ-SDS-215
func drawEyePane(sf Surface, p plotArea, f *engine.Frame, hud HUD, e *wsview.EyePane) {
	drawPaneFrame(sf, p, &e.Pane)
	drawMiniScope(sf, f, hud, 4, 27, 230, 96)
	DrawText(sf, 8, 29, e.TimeLabel, colDim, 1)
	if e.Why != "" {
		DrawText(sf, 250, 30, cutText(e.Why, (p.w-256)/6), colDim, 1)
		return
	}
	for ci, colm := range e.Figures {
		x := 250 + ci*170
		for i, kv := range colm {
			DrawText(sf, x, 27+i*10, kv[0], colDim, 1)
			DrawTextRight(sf, x+160, 27+i*10, kv[1], colInfo, 1)
		}
	}
	DrawText(sf, 250, 72, cutText(e.Cursor, (p.w-256)/6), colTrig, 1)
	if e.Warn != "" {
		DrawText(sf, 250, 88, cutText(e.Warn, (p.w-256)/6), colWarn, 1)
	} else if e.Note != "" {
		DrawText(sf, 250, 88, cutText(e.Note, (p.w-256)/6), colDim, 1)
	}
}

// ---- SPECTRUM ----

// drawSpecPane draws the peak table beside a small time trace.
// TRLC-LINKS: REQ-SDS-216
func drawSpecPane(sf Surface, p plotArea, f *engine.Frame, hud HUD, s *wsview.SpecPane) {
	drawPaneFrame(sf, p, &s.Pane)
	drawMiniScope(sf, f, hud, 4, 27, 230, p.y0-44)
	DrawText(sf, 8, 29, s.TimeLabel, colDim, 1)
	const X = 250
	DrawText(sf, X, 27, "#", colDim, 1)
	hx := []int{X + 24, X + 98, X + 170, X + 236}
	if s.Bode {
		hx = []int{X + 24, X + 104, X + 170, X + 236}
	}
	for i, h := range s.Head {
		DrawText(sf, hx[i], 27, h, colDim, 1)
	}
	freqCol := colC1
	if s.Ch == 1 {
		freqCol = colC2
	}
	if s.Empty != "" {
		DrawText(sf, X, 40, s.Empty, colDim, 1)
	}
	for i, r := range s.Rows {
		y := 37 + i*10
		if y > p.y0-30 {
			break
		}
		numCol := colInfo
		if r.Sel {
			fillRect(sf, X-6, y-1, 296, 10, colGrid)
			DrawText(sf, X-4, y, ">", colTrig, 1)
			numCol = colTrig
		}
		DrawText(sf, X+6, y, itoa(r.Num), numCol, 1)
		if s.Bode {
			DrawText(sf, hx[0], y, r.Freq, colInfo, 1)
			DrawText(sf, hx[1], y, r.Level, colC1, 1)
			DrawText(sf, hx[2], y, r.Rel, colC2, 1)
			continue
		}
		DrawText(sf, hx[0], y, r.Freq, freqCol, 1)
		DrawText(sf, hx[1], y, r.Level, colInfo, 1)
		DrawText(sf, hx[2], y, r.Rel, colInfo, 1)
		DrawText(sf, hx[3], y, r.Harm, colDim, 1)
	}
	sx := X + 300
	DrawText(sf, sx, 27, map[bool]string{true: "SUMMARY", false: "READOUT"}[s.Bode], colDim, 1)
	for i, l := range s.Readout {
		DrawText(sf, sx, 37+i*10, cutText(l.Text, (p.w-sx-4)/6), colorOf(l.Col), 1)
	}
	if s.Marker != "" {
		DrawText(sf, X, p.y0-25, cutText(s.Marker, (p.w-X-4)/6), colTrig, 1)
	}
}

// ---- MEASURE ----

// drawMeasPane draws the statistics table (or all values) and the cursor
// block.
// TRLC-LINKS: REQ-SDS-217
func drawMeasPane(sf Surface, p plotArea, m *wsview.MeasPane) {
	drawPaneFrame(sf, p, &m.Pane)
	switch {
	case m.All:
		split := m.AllSplit
		if split <= 0 {
			split = 9
		}
		for k, x := range []int{4, 262} {
			DrawText(sf, x, 27, "PARAM", colDim, 1)
			DrawTextRight(sf, x+130, 27, "C1", colC1, 1)
			DrawTextRight(sf, x+206, 27, "C2", colC2, 1)
			for r := 0; r < 9; r++ {
				i := r
				if k == 1 {
					i = split + r
				} else if r >= split {
					break
				}
				if i >= m.AllN {
					break
				}
				y := 37 + r*10
				DrawText(sf, x, y, m.AllRows[i][0], colInfo, 1)
				DrawTextRight(sf, x+130, y, m.AllRows[i][1], colC1, 1)
				DrawTextRight(sf, x+206, y, m.AllRows[i][2], colC2, 1)
			}
		}
	case m.Empty:
		DrawText(sf, 16, 36, "NOTHING CHOSEN", colInfo, 2)
		DrawText(sf, 16, 62, "The Measure key opens the MEAS sheet: pick each measurement Off / C1 / C2 / Both.", colDim, 1)
		DrawText(sf, 16, 74, "F4 again shows all values of both channels.", colDim, 1)
	default:
		xs := []int{4, 84, 158, 232, 306, 380, 444}
		for i, t := range []string{"ITEM", "NOW", "MEAN", "MIN", "MAX", "SDEV", "N"} {
			if i == 0 {
				DrawText(sf, xs[i], 27, t, colDim, 1)
			} else {
				DrawTextRight(sf, xs[i]+54, 27, t, colDim, 1)
			}
		}
		for r, st := range m.Stats {
			y := 37 + r*10
			DrawText(sf, xs[0], y, st.Name, colorOf(st.Col), 1)
			for i, v := range []string{st.Now, st.Mean, st.Min, st.Max, st.Sdev} {
				DrawTextRight(sf, xs[i+1]+54, y, v, colInfo, 1)
			}
			DrawTextRight(sf, xs[6]+54, y, st.N, colDim, 1)
		}
		if m.More != "" {
			DrawText(sf, xs[0], 37+8*10, m.More, colDim, 1)
		}
	}
	const cx = 540
	vline(sf, cx-8, 26, p.y0-15, colGrid)
	for i, l := range m.Cursor {
		DrawText(sf, cx, 27+i*10, cutText(l.Text, (p.w-cx-4)/6), colorOf(l.Col), 1)
	}
	if m.MaskLine.Text != "" {
		DrawText(sf, cx, 97, m.MaskLine.Text, colorOf(m.MaskLine.Col), 1)
	}
}

// ---- COMPARE ----

// drawCmpPane draws the MASTER, REF A, REF B, MASK and MATH rows.
// TRLC-LINKS: REQ-SDS-218
func drawCmpPane(sf Surface, p plotArea, c *wsview.CmpPane) {
	drawPaneFrame(sf, p, &c.Pane)
	for i, r := range c.Rows {
		y := 27 + i*13
		nameCol := colInfo
		if r.Sel {
			fillRect(sf, 0, y-3, p.w, 13, colGrid)
			DrawText(sf, 3, y, ">", colTrig, 1)
			nameCol = colTrig
		}
		DrawText(sf, 12, y, r.Name, nameCol, 1)
		DrawText(sf, 60, y, cutText(r.Info, 27), colorOf(r.InfoCol), 1)
		if r.Bar {
			const bx, bw = 226, 200
			strokeRect(sf, bx, y, bw, 8, colTrig)
			if r.BarOf > 0 {
				fillRect(sf, bx+1, y+1, (bw-2)*min(r.BarN, r.BarOf)/r.BarOf, 6, colTrig)
			}
			DrawText(sf, bx+bw+10, y, cutText(r.BarNote, (p.w-bx-bw-14)/6), colorOf(r.BarNoteCol), 1)
		} else if r.Extra != "" {
			DrawText(sf, 226, y, cutText(r.Extra, 62), colorOf(r.ExtraCol), 1)
		}
		if r.Sel && r.Action != "" {
			w := TextWidth(r.Action, 1) + 8
			fillRect(sf, p.w-4-w, y-2, w, 11, colGrid)
			DrawTextRight(sf, p.w-6, y, r.Action, colTrig, 1)
		}
	}
}

// ---- the mini scope (sheet thumbnail, pane time traces) ----

// drawMiniScope draws a small live view of the frame in a rectangle: its
// graticule, the shown channels as column extremes, the trigger pointer.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func drawMiniScope(sf Surface, f *engine.Frame, hud HUD, x0, y0, w, h int) {
	fillRect(sf, x0, y0, w, h, colBG)
	for c := 0; c <= 10; c++ {
		col := colGrid
		if c == 5 {
			col = colAxis
		}
		vline(sf, x0+c*(w-1)/10, y0, y0+h-1, col)
	}
	for r := 0; r <= 8; r++ {
		col := colGrid
		if r == 4 {
			col = colAxis
		}
		hline(sf, x0, x0+w-1, y0+r*(h-1)/8, col)
	}
	yOf := func(v uint8) int {
		y := y0 + h - 1 - int((float64(v)-28)/200*float64(h-1)+0.5)
		return min(max(y, y0), y0+h-1)
	}
	if f != nil && len(f.C1) > 0 && !(f.IsEnv && f.EnvCols == 0) {
		sc1, sc2 := hud.shownChans()
		for _, ch := range []int{1, 0} {
			if (ch == 0 && !sc1) || (ch == 1 && (!sc2 || !hud.TwoChan)) {
				continue
			}
			col := colC1
			if ch == 1 {
				col = colC2
			}
			if f.IsEnv {
				mn, mx := f.EnvMin, f.EnvMax
				if ch == 1 {
					mn, mx = f.EnvMin2, f.EnvMax2
				}
				for x := 0; x < w; x++ {
					c := x * f.EnvCols / w
					if c < len(mn) && c < len(mx) {
						vline(sf, x0+x, yOf(mx[c]), yOf(mn[c]), col)
					}
				}
				continue
			}
			valid := frameValid(f)
			if ch == 1 && len(f.C2) < valid {
				continue
			}
			sig := f.C1[:valid]
			if ch == 1 {
				sig = f.C2[:valid]
			}
			win, xc, posFrac := traceWindow(f, hud, valid)
			left := xc - float64(win)*posFrac
			prev := -1
			for x := 0; x < w; x++ {
				lo := clampI(int(left+float64(x)*float64(win)/float64(w)), 0, valid-1)
				hi := clampI(int(left+float64(x+1)*float64(win)/float64(w))-1, lo, valid-1)
				mn, mx := sig[lo], sig[lo]
				for _, v := range sig[lo : hi+1] {
					mn, mx = min(mn, v), max(mx, v)
				}
				yt, yb := yOf(mx), yOf(mn)
				if prev >= 0 {
					yt, yb = min(yt, prev), max(yb, prev)
				}
				vline(sf, x0+x, yt, yb, col)
				prev = yOf(sig[hi])
			}
		}
	}
	if frac, ok := trigFrac(f, hud); ok {
		tx := x0 + int(frac*float64(w-1))
		for dy := 0; dy <= 3; dy++ {
			hline(sf, tx-(3-dy), tx+(3-dy), y0+1+dy, colTrig)
		}
	}
	strokeRect(sf, x0, y0, w, h, colAxis)
}

// ---- the sheet ----

// Sheet geometry (hud/sheet.js): the tab strip, the rows, the side panel
// with the mini view, the hint line.
const (
	tabY       = 12
	tabH       = 17
	rowY0      = 34
	rowH       = 17
	hdrH       = 13
	xLbl       = 12
	xOpt       = 110
	xEnd       = 470
	sideX      = 478
	sideW      = 202
	thumbY     = 34
	thumbH     = 158
	hintY      = 469
	rowsBottom = 458
)

// drawSheet draws a full-screen settings sheet.
// TRLC-LINKS: REQ-SDS-212
func drawSheet(sf Surface, f *engine.Frame, hud HUD) {
	sh := hud.UI.Sheet
	fillRect(sf, 0, 12, W, H-12, colBG)
	drawTabs(sf, sh)
	bottom, matchY := drawRows(sf, sh)
	y := bottom + 8
	for _, l := range sh.Status {
		if y > rowsBottom-8 {
			break
		}
		DrawText(sf, 4, y, cutText(l.Text, 77), colorOf(l.Col), 1)
		y += 11
	}
	if len(sh.Guide) > 0 {
		y = max(y, bottom+10)
		for i, l := range sh.Guide {
			if i > 0 && l.Col == wsview.Amber {
				y += 5
			}
			if y <= rowsBottom-8 {
				DrawText(sf, 6, y, l.Text, colorOf(l.Col), 1)
			}
			y += 12
		}
	}
	if sh.Sub != nil && matchY >= 0 {
		drawSheetSub(sf, sh.Sub, matchY)
	}
	drawThumb(sf, f, hud, sh)
	drawSide(sf, sh)
	DrawText(sf, 4, hintY, cutText(sh.Hint.Text, 132), colorOf(sh.Hint.Col), 1)
}

// drawTabs draws the tab strip: the workspace's name, its own tabs, a double
// bar, the other tabs dim; the open tab filled amber.
// TRLC-LINKS: REQ-SDS-212
func drawTabs(sf Surface, sh *wsview.Sheet) {
	fillRect(sf, 0, tabY, W, tabH, colMenuBg)
	hline(sf, 0, W-1, tabY+tabH, colAxis)
	x := 4
	DrawText(sf, x, tabY+5, sh.WSName, colTrig, 1)
	x += TextWidth(sh.WSName, 1) + 6
	DrawText(sf, x, tabY+5, ":", colTrig, 1)
	x += 8
	for i, t := range sh.Tabs {
		w := TextWidth(t.Name, 1) + 12
		if i > 0 && !t.Own && sh.Tabs[i-1].Own {
			vline(sf, x+1, tabY+2, tabY+tabH-2, colDim)
			vline(sf, x+3, tabY+2, tabY+tabH-2, colDim)
			x += 6
		}
		col := colDim
		if t.Own {
			col = colInfo
		}
		if t.Active {
			fillRect(sf, x, tabY+1, w, tabH, colTrig)
			col = colBG
		}
		DrawText(sf, x+6, tabY+5, t.Name, col, 1)
		x += w
		if i < len(sh.Tabs)-1 && !t.Active && !sh.Tabs[i+1].Active && (t.Own == sh.Tabs[i+1].Own) {
			vline(sf, x, tabY+4, tabY+tabH-4, colAxis)
		}
		x++
	}
	DrawTextRight(sf, 796, tabY+5, "< S/DIV >", colDim, 1)
}

// drawRows draws the rows; it returns the row area's bottom and the y of the
// Match bytes row (-1 when the tab has none).
// TRLC-LINKS: REQ-SDS-212
func drawRows(sf Surface, sh *wsview.Sheet) (bottom, matchY int) {
	y, matchY := rowY0, -1
	for _, r := range sh.Rows {
		if r.Kind == wsview.Header {
			DrawText(sf, 4, y+3, r.Header, colDim, 1)
			w := TextWidth(r.Header, 1) + 8
			hline(sf, 4+w, xEnd, y+6, colGrid)
			y += hdrH
			continue
		}
		if r.Kind == wsview.Bytes {
			matchY = y
		}
		drawRow(sf, r, y)
		y += rowH
	}
	return y, matchY
}

// drawRow draws one row at y.
// TRLC-LINKS: REQ-SDS-212
func drawRow(sf Surface, r wsview.Row, y int) {
	if r.Focus {
		bg := colGrid
		if r.Edit {
			bg = colMenuBg
		}
		fillRect(sf, 0, y, xEnd+4, rowH-1, bg)
		if r.Edit {
			strokeRect(sf, 0, y, xEnd+4, rowH-1, colTrig)
		}
		DrawText(sf, 3, y+5, ">", colTrig, 1)
	}
	lc := colDim
	switch {
	case r.Edit:
		lc = colTrig
	case r.Rel:
		lc = colInfo
	}
	DrawText(sf, xLbl, y+5, cutText(r.Label, 15), lc, 1)
	xe := xEnd
	switch r.Kind {
	case wsview.Enum:
		limit := xEnd + 2
		if r.Meas != [2]string{} {
			limit = 290
		}
		xe = drawOptions(sf, r.Opts, xOpt, y, r.Rel, limit)
		if r.Meas != [2]string{} {
			for ch, v := range r.Meas {
				if v == "" {
					continue
				}
				col, x := colC1, 300
				if ch == 1 {
					col, x = colC2, 388
				}
				DrawText(sf, x, y+5, cutText("C"+itoa(ch+1)+" "+v, 13), col, 1)
			}
			xe = xEnd
		}
	case wsview.Number:
		vc := colDim
		if r.Rel {
			vc = colTrig
		}
		DrawText(sf, xOpt, y+5, cutText(r.Value, 15), vc, 1)
		end := xEnd - 6
		if r.Edit {
			end = xEnd - 36
		}
		drawGauge(sf, xOpt+100, end, y, r.Gauge, r.Rel)
		xe = xEnd - 34
	case wsview.Action:
		x := xOpt
		for _, a := range r.Acts {
			w := TextWidth(a.Text, 1) + 8
			col := colInfo
			switch {
			case a.Flash:
				fillRect(sf, x, y+2, w, rowH-4, colTrig)
				col = colBG
			case a.Dim || !r.Rel:
				strokeRect(sf, x, y+2, w, rowH-4, colAxis)
				col = colDim
			default:
				strokeRect(sf, x, y+2, w, rowH-4, colDim)
			}
			DrawText(sf, x+4, y+5, a.Text, col, 1)
			x += w + 4
		}
		if r.Status != "" {
			DrawText(sf, x+4, y+5, cutText(r.Status, max(4, (xEnd-x-4)/6)), colDim, 1)
		}
	case wsview.Slot:
		col := colDim
		if r.Used {
			col = colInfo
		}
		DrawText(sf, xOpt, y+5, cutText(r.Value, 57), col, 1)
	case wsview.Bytes:
		col := colTrig
		if r.Value == "any byte" {
			col = colDim
		}
		DrawText(sf, xOpt, y+5, cutText(r.Value, 40), col, 1)
		if r.Armed {
			DrawText(sf, xOpt+TextWidth(cutText(r.Value, 40), 1)+12, y+5, "(armed)", colOK, 1)
		}
	}
	if r.Edit && xe <= xEnd-30 {
		DrawTextRight(sf, xEnd, y+5, "EDIT", colTrig, 1)
	}
}

// drawOptions writes an enum row's options inline, the current one filled
// (amber when active, grey for "Off" or a row not in use); a list too long
// for the row shows a window around the current option.
// TRLC-LINKS: REQ-SDS-212
func drawOptions(sf Surface, opts []wsview.Opt, x, y int, rel bool, xmax int) int {
	ws := make([]int, len(opts))
	cur := 0
	for i, o := range opts {
		ws[i] = TextWidth(o.Text, 1) + 6
		if o.Sel {
			cur = i
		}
	}
	total := func(a, b, gap int) int {
		t := 0
		for i := a; i < b; i++ {
			t += ws[i] + gap
		}
		return t - gap
	}
	gap, i0, i1 := 3, 0, len(opts)
	if x+total(0, len(opts), 3) > xmax {
		gap = 2
	}
	if x+total(0, len(opts), gap) > xmax {
		i0, i1 = cur, cur+1
		for {
			grew := false
			if i1 < len(opts) && x+16+total(i0, i1+1, gap) <= xmax {
				i1++
				grew = true
			}
			if i0 > 0 && x+16+total(i0-1, i1, gap) <= xmax {
				i0--
				grew = true
			}
			if !grew {
				break
			}
		}
		if i0 > 0 {
			DrawText(sf, x, y+5, "<", colDim, 1)
			x += 8
		}
	}
	for i := i0; i < i1; i++ {
		o := opts[i]
		col := colDim
		if rel {
			col = colInfo
		}
		if o.Sel {
			bg := colTrig
			col = colBG
			if o.Quiet {
				bg, col = colAxis, colInfo
			}
			fillRect(sf, x, y+2, ws[i], rowH-4, bg)
		}
		DrawText(sf, x+3, y+5, o.Text, col, 1)
		x += ws[i] + gap
	}
	if i1 < len(opts) {
		DrawText(sf, x, y+5, ">", colDim, 1)
	}
	return x
}

// drawGauge draws a number row's range gauge with its value's knob.
// TRLC-LINKS: REQ-SDS-212
func drawGauge(sf Surface, x0, x1, y int, pos float64, rel bool) {
	yy := y + 8
	hline(sf, x0, x1-1, yy, colAxis)
	vline(sf, x0, yy-2, yy+2, colAxis)
	vline(sf, x1-1, yy-2, yy+2, colAxis)
	px := x0 + int(math.Round(math.Min(math.Max(pos, 0), 1)*float64(x1-x0-3)))
	col := colDim
	if rel {
		col = colTrig
	}
	fillRect(sf, px, yy-4, 3, 9, col)
}

// drawSheetSub draws the sheet's byte picker under the Match bytes row.
// TRLC-LINKS: REQ-SDS-214
func drawSheetSub(sf Surface, s *wsview.Sub, rowY int) {
	const bh = 120
	by := rowY + rowH + 2
	if by+bh > rowsBottom {
		by = rowY - bh - 2
	}
	fillRect(sf, 2, by, xEnd+2, bh, colBoxBg)
	strokeRect(sf, 2, by, xEnd+2, bh, colTrig)
	DrawText(sf, 10, by+6, s.Title, colTrig, 1)
	drawTokens(sf, 12, by+30, xEnd-6, s.Toks, s.Cur)
	for i, l := range s.Lines {
		DrawText(sf, 12, by+62+i*12, l.Text, colorOf(l.Col), 1)
	}
}

// drawSide draws the panel explaining the highlighted row.
// TRLC-LINKS: REQ-SDS-212
func drawSide(sf Surface, sh *wsview.Sheet) {
	y0 := thumbY + thumbH + 6
	h := rowsBottom - y0
	fillRect(sf, sideX, y0, sideW, h, colBoxBg)
	strokeRect(sf, sideX, y0, sideW, h, colGrid)
	x, y := sideX+6, y0+5
	sd := sh.Side
	if sd.Title != "" {
		DrawText(sf, x, y, sd.Title, colTrig, 1)
		y += 14
		for _, l := range sd.Lines {
			DrawText(sf, x, y, l.Text, colorOf(l.Col), 1)
			y += 10
		}
		return
	}
	DrawText(sf, x, y, cutText(sd.Label, 31), colTrig, 1)
	y += 12
	if sd.Big && sd.Value != "" {
		DrawText(sf, x, y, sd.Value, colInfo, 2)
		y += 20
	}
	limit := y0 + h - 4 - len(sd.Hints)*10 - 8
	for _, l := range sd.Lines {
		if y > limit-10 {
			break
		}
		DrawText(sf, x, y, l.Text, colorOf(l.Col), 1)
		y += 10
	}
	hy := y0 + h - 4 - len(sd.Hints)*10
	hline(sf, sideX+4, sideX+sideW-5, hy-4, colGrid)
	for _, l := range sd.Hints {
		DrawText(sf, x, hy, l.Text, colorOf(l.Col), 1)
		hy += 10
	}
}

// drawThumb draws the sheet's live mini view: the trace with its cursors,
// or the FFT, X-Y, Bode or spectrogram being shown, tagged LIVE, WAIT or
// STOP.
// TRLC-LINKS: REQ-SDS-212
func drawThumb(sf Surface, f *engine.Frame, hud HUD, sh *wsview.Sheet) {
	x0, y0, w, h := sideX, thumbY, sideW, thumbH
	tag, tagCol := "LIVE", colOK
	switch {
	case !hud.Running:
		tag, tagCol = "STOP", colStale
	case hud.Waiting || hud.Single:
		tag, tagCol = "WAIT", colTrig
	}
	switch sh.ThumbView {
	case 1, 2, 3:
		fillRect(sf, x0, y0, w, h, colBG)
		for i := 1; i < 4; i++ {
			hline(sf, x0+1, x0+w-2, y0+i*h/4, colGrid)
		}
		for i := 1; i < 5; i++ {
			vline(sf, x0+i*w/5, y0+1, y0+h-2, colGrid)
		}
		switch sh.ThumbView {
		case 1:
			tag = "X-Y"
			drawThumbXY(sf, f, x0, y0, w, h)
		case 2:
			tag = "FFT CH" + itoa(sh.FFTCh+1)
			drawThumbFFT(sf, f, hud, sh.FFTCh, x0, y0, w, h)
		case 3:
			tag = "BODE " + itoa(len(hud.BodeFreq))
			drawThumbBode(sf, hud, x0, y0, w, h)
		}
		strokeRect(sf, x0, y0, w, h, colAxis)
	case 4:
		fillRect(sf, x0, y0, w, h, colBG)
		tag = "SPGM CH" + itoa(sh.FFTCh+1)
		if sg := hud.Spect; sg != nil && sg.rows > 0 {
			for y := 1; y < h-1; y++ {
				sy := sgY0 + (y-1)*sgRows/(h-2)
				for x := 1; x < w-1; x++ {
					sf.SetPixel(x0+x, y0+y, sg.img.At(sgX0+(x-1)*sgCols/(w-2), sy))
				}
			}
		} else {
			DrawText(sf, x0+w/2-TextWidth("waiting for data", 1)/2, y0+h/2-4, "waiting for data", colDim, 1)
		}
		strokeRect(sf, x0, y0, w, h, colAxis)
	default:
		drawMiniScope(sf, f, hud, x0, y0, w, h)
		if hud.CurOn {
			for k := 0; k < 2; k++ {
				col := colDim
				if k == hud.CurSel {
					col = colTrig
				}
				lab := string(rune('A' + k))
				if hud.CurType == 0 {
					xx := x0 + int(hud.CurX[k]*float64(w-1))
					vdash(sf, xx, y0+1, y0+h-2, col)
					DrawText(sf, xx+2, y0+8, lab, col, 1)
				} else {
					yy := y0 + int(hud.CurY[k]*float64(h-1))
					hdash(sf, x0+1, x0+w-2, yy, col)
					DrawText(sf, x0+w-9, yy+2, lab, col, 1)
				}
			}
		}
	}
	fillRect(sf, x0+1, y0+h-11, TextWidth(tag, 1)+5, 10, colBG)
	DrawText(sf, x0+3, y0+h-10, tag, tagCol, 1)
}

// drawThumbXY is the X-Y mini view.
// TRLC-LINKS: REQ-SDS-212
func drawThumbXY(sf Surface, f *engine.Frame, x0, y0, w, h int) {
	if f == nil || f.IsEnv || len(f.C1) == 0 {
		return
	}
	valid := frameValid(f)
	if len(f.C2) < valid {
		return
	}
	step := max(1, valid/600)
	px, py := -1, -1
	for i := 0; i < valid; i += step {
		x := x0 + 1 + int(float64(f.C1[i])*float64(w-3)/255)
		y := y0 + h - 2 - int(float64(f.C2[i])*float64(h-3)/255)
		if px >= 0 {
			drawLine(sf, px, py, x, y, colMath)
		}
		px, py = x, y
	}
}

// drawThumbFFT is the FFT mini view (column maxima, -80 dB at the bottom).
// TRLC-LINKS: REQ-SDS-212
func drawThumbFFT(sf Surface, f *engine.Frame, hud HUD, ch, x0, y0, w, h int) {
	if f == nil || f.IsEnv || len(f.C1) == 0 {
		return
	}
	spec, ok := framecalc.Spectrum(f, ch, hud.SampleS)
	if !ok {
		return
	}
	sp := spec.Mags
	nb := len(sp)
	if nb < 4 {
		return
	}
	cols := w - 3
	col := colC1
	if ch == 1 {
		col = colC2
	}
	pk := math.Max(spec.Peak, 1e-12)
	prevX, prevY := -1, 0
	for c := 0; c < cols; c++ {
		k0 := 1 + c*(nb-1)/cols
		k1 := max(k0, (c+1)*(nb-1)/cols)
		m := 0.0
		for k := k0; k <= k1 && k < nb; k++ {
			m = math.Max(m, sp[k])
		}
		db := math.Max(-80, 20*math.Log10(m/pk+1e-12))
		x, y := x0+1+c, y0+1+int(-db/80*float64(h-3))
		if prevX >= 0 {
			drawLine(sf, prevX, prevY, x, y, col)
		}
		prevX, prevY = x, y
	}
}

// drawThumbBode is the Bode mini view: gain (C1 colour) and phase (C2).
// TRLC-LINKS: REQ-SDS-212
func drawThumbBode(sf Surface, hud HUD, x0, y0, w, h int) {
	n := len(hud.BodeFreq)
	if n < 2 || hud.BodeFreq[0] <= 0 {
		return
	}
	lf0, lf1 := math.Log10(hud.BodeFreq[0]), math.Log10(hud.BodeFreq[n-1])
	if lf1 <= lf0 {
		return
	}
	lo, hi := -20.0, 10.0
	for _, g := range hud.BodeGain {
		lo, hi = math.Min(lo, g), math.Max(hi, g)
	}
	xOf := func(i int) int { return x0 + 1 + int((math.Log10(hud.BodeFreq[i])-lf0)/(lf1-lf0)*float64(w-3)) }
	for i := 1; i < n; i++ {
		ya := y0 + 1 + int((hi-hud.BodeGain[i-1])/(hi-lo)*float64(h-3))
		yb := y0 + 1 + int((hi-hud.BodeGain[i])/(hi-lo)*float64(h-3))
		drawLine(sf, xOf(i-1), ya, xOf(i), yb, colC1)
		pa := y0 + 1 + int(math.Min(math.Max(-hud.BodePhase[i-1]/180, 0), 1)*float64(h-3))
		pb := y0 + 1 + int(math.Min(math.Max(-hud.BodePhase[i]/180, 0), 1)*float64(h-3))
		drawLine(sf, xOf(i-1), pa, xOf(i), pb, colC2)
	}
}
