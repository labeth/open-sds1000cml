// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"fmt"
	"strings"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
)

// BUS's Visual view on the LCD: the frame's decode drawn as what it carries,
// the capture on its own (what fell between captures is not known, so
// nothing is carried from one to the next): a MIDI piano with the capture's
// pushes and releases above their keys, DMX's levels, an LED strip's colours,
// PS/2's key presses, and a reading for the buses with one (an IR button, a
// caliper, a Wiegand card, PWM's duty, the DCF77 time, steps), LIN's and
// CAN's frames by identifier, a terminal's text.

// visTop is the picture's first row: under the status line.
const visTop = 14

// visChColours colour MIDI's sixteen channels (the web's colours).
var visChColours = [16]uint16{rgb(79, 195, 247), rgb(255, 183, 77), rgb(129, 199, 132), rgb(229, 115, 115), rgb(186, 104, 200), rgb(255, 241, 118),
	rgb(77, 182, 172), rgb(240, 98, 146), rgb(149, 117, 205), rgb(161, 136, 127), rgb(174, 213, 129), rgb(100, 181, 246),
	rgb(255, 138, 101), rgb(144, 164, 174), rgb(220, 231, 117), rgb(121, 134, 203)}

// drawVisualPane draws the frame's bus as what it carries in the plot.
// TRLC-LINKS: REQ-SDS-281
func drawVisualPane(sf Surface, p plotArea, f *engine.Frame, hud HUD) {
	// under the status line (channels, timebase, trigger): it stays
	if top := visTop - p.y0; top > 0 {
		p.y0, p.h = visTop, p.h-top
	}
	fillRect(sf, 0, p.y0, p.w, p.h, colBG)
	if hud.DecProto == 0 || f == nil {
		DrawText(sf, 8, p.y0+8, "VISUAL  no bus decoded: AUTO finds one", colDim, 1)
		return
	}
	res := decodeFrame(f, hud)
	if !res.OK {
		DrawText(sf, 8, p.y0+8, "VISUAL  "+protoNames[hud.DecProto%len(protoNames)]+": "+res.Error, colStale, 1)
		return
	}
	n := len(f.C1)
	dt := f.SampleS
	if !(dt > 0) && n > 0 {
		dt = f.TdivS * 14 / float64(n)
	}
	a := visArea{x0: 0, y0: p.y0 + 16, w: p.w, h: p.h - 18, n: n, dt: dt}
	title, sub := visDrawBus(sf, a, res)
	DrawText(sf, 8, p.y0+4, "VISUAL  "+title, colInfo, 1)
	if sub != "" {
		DrawTextRight(sf, p.w-6, p.y0+4, sub, colDim, 1)
	}
}

// visArea is the picture's rectangle and the capture's samples and their interval.
type visArea struct {
	x0, y0, w, h, n int
	dt              float64
}

// visDrawBus draws the decode's picture; it gives the title and a line beside it.
// TRLC-LINKS: REQ-SDS-281
func visDrawBus(sf Surface, a visArea, res decode.Result) (string, string) {
	p := res.Proto
	switch {
	case p == "midi":
		return "MIDI keyboard", visDrawMIDI(sf, a, res.Spans)
	case p == "dmx512":
		return "DMX512 levels", visDrawDMX(sf, a, res.Spans)
	case p == "ws281x":
		return "LED strip", visDrawLEDs(sf, a, res.Spans)
	case p == "ps2":
		return "PS/2 keys", visDrawPS2(sf, a, res.Spans)
	case strings.HasPrefix(p, "ir"):
		return "IR remote", visBigLast(sf, a, res.Spans, visIRReading)
	case p == "caliper":
		return "Caliper reading", visBigLast(sf, a, res.Spans, visCaliperReading)
	case p == "wiegand":
		return "Wiegand card", visBigLast(sf, a, res.Spans, visWiegandReading)
	case p == "pwm":
		return "PWM duty", visBigLast(sf, a, res.Spans, visPWMReading)
	case p == "dcf77":
		return "DCF77 clock", visBigLast(sf, a, res.Spans, visDCFReading)
	case p == "quad" || p == "stepper":
		return map[bool]string{true: "Encoder", false: "Stepper"}[p == "quad"] + " steps", visBigLast(sf, a, res.Spans, visStepReading)
	case p == "lin" || p == "canfd" || p == "can":
		return strings.ToUpper(p[:3]) + " frames", visDrawFrames(sf, a, res.Spans, p == "lin")
	case p == "uart":
		return "Terminal", visDrawText(sf, a, res.Spans)
	}
	DrawText(sf, a.x0+8, a.y0+a.h/2, "no picture for this bus: Packets lists it", colDim, 1)
	return strings.ToUpper(p), ""
}

// ---- MIDI ----

// visMIDIEvent is a push or a release in the capture: its channel and note,
// and its sample index.
type visMIDIEvent struct {
	on       bool
	ch, note int
	i        int
}

// visMIDIEvents reads the capture's note messages from its bytes (running
// status kept; realtime bytes between them skipped).
// TRLC-LINKS: REQ-SDS-281
func visMIDIEvents(spans []decode.Span) []visMIDIEvent {
	var out []visMIDIEvent
	run, need := 0, 0
	var data []int
	for _, sp := range spans {
		if sp.Kind != "data" {
			continue
		}
		b := sp.Val & 0xff
		switch {
		case b >= 0xf8:
			continue
		case b >= 0x80:
			run, data = b, data[:0]
			need = map[int]int{0x8: 2, 0x9: 2, 0xa: 2, 0xb: 2, 0xc: 1, 0xd: 1, 0xe: 2}[b>>4]
			if b >= 0xf0 {
				need = 0
			}
			continue
		}
		if need == 0 {
			continue
		}
		data = append(data, b)
		if len(data) < need {
			continue
		}
		if k := run >> 4; k == 0x9 || k == 0x8 {
			out = append(out, visMIDIEvent{on: k == 0x9 && data[1] > 0, ch: run & 0xf, note: data[0], i: sp.I0})
		}
		data = data[:0]
	}
	return out
}

// visDrawMIDI draws an 88-key piano, the capture's pushes (filled, pointing
// at the key) and releases (hollow) above their keys, each as high as it is
// long before the capture's end, the keys pushed and not released in it lit.
// TRLC-LINKS: REQ-SDS-281
func visDrawMIDI(sf Surface, a visArea, spans []decode.Span) string {
	ev := visMIDIEvents(spans)
	const lo, hi = 21, 108
	black := func(n int) bool { m := n % 12; return m == 1 || m == 3 || m == 6 || m == 8 || m == 10 }
	whites := 0
	for n := lo; n <= hi; n++ {
		if !black(n) {
			whites++
		}
	}
	ww := float64(a.w) / float64(whites)
	keyX := func(n int) (int, int, bool) {
		wi := 0
		for k := lo; k < n; k++ {
			if !black(k) {
				wi++
			}
		}
		if black(n) {
			return int(float64(wi)*ww - ww*0.31), max(2, int(ww*0.62)), true
		}
		return int(float64(wi) * ww), max(2, int(ww)), false
	}
	kbH := min(a.h/3, int(ww*5.5))
	keyTop := a.y0 + a.h - kbH
	lit := map[int]int{}
	for _, e := range ev {
		if e.on {
			lit[e.note] = e.ch
		} else {
			delete(lit, e.note)
		}
	}
	// the time scale: the capture's end at the keys, its start at the top
	span, top := float64(a.n)*a.dt, a.y0+8
	if !(span > 0) {
		span = 1 // a record without samples or their interval: no time scale to read
	}
	hgt := keyTop - 8 - top
	for k := 0; k <= 4; k++ {
		y := keyTop - 8 - k*hgt/4
		hline(sf, 0, a.w-1, y, colGrid)
		if k > 0 {
			DrawText(sf, 3, y-8, fmtTdiv(span*float64(k)/4)+" before", colDim, 1)
		}
	}
	for _, e := range ev {
		if e.note < lo || e.note > hi {
			continue
		}
		x, w, _ := keyX(e.note)
		cx := x + w/2
		age := float64(a.n-e.i) * a.dt
		y := keyTop - 8 - int(age/span*float64(hgt))
		c := visChColours[e.ch&15]
		for yy := y; yy < keyTop; yy += 2 {
			sf.SetPixel(cx, yy, c) // its lane down to the key
		}
		if e.on {
			for d := 0; d < 5; d++ {
				hline(sf, cx-4+d, cx+4-d, y-3+d, c)
			}
		} else {
			strokeRect(sf, cx-3, y-3, 7, 7, c)
		}
	}
	// the keys: white, then black over them; pressed ones in their channel's colour
	fillRect(sf, 0, keyTop, a.w, kbH, rgb(42, 42, 42))
	for n := lo; n <= hi; n++ {
		if black(n) {
			continue
		}
		x, w, _ := keyX(n)
		c := rgb(243, 241, 234)
		if ch, ok := lit[n]; ok {
			c = visChColours[ch&15]
		}
		fillRect(sf, x+1, keyTop, w-1, kbH-1, c)
		if n%12 == 0 && w >= 9 {
			DrawText(sf, x+1, keyTop+kbH-10, fmt.Sprintf("C%d", n/12-1), rgb(138, 134, 118), 1)
		}
	}
	for n := lo; n <= hi; n++ {
		if !black(n) {
			continue
		}
		x, w, _ := keyX(n)
		c := rgb(20, 20, 24)
		if ch, ok := lit[n]; ok {
			c = visChColours[ch&15]
		}
		fillRect(sf, x, keyTop, w, kbH*62/100, c)
	}
	pushes, releases := 0, 0
	for _, e := range ev {
		if e.on {
			pushes++
		} else {
			releases++
		}
	}
	if len(ev) == 0 {
		DrawText(sf, a.w/2-120, a.y0+a.h/3, "this capture holds no push or release", colDim, 1)
	}
	return fmt.Sprintf("this capture: %d pushed (filled), %d released (hollow)", pushes, releases)
}

// ---- DMX, LEDs, PS/2, text ----

// visDrawDMX draws the levels of the capture's longest packet (one the
// record's end cuts short holds fewer slots) as 512 bars, 32 a row.
// TRLC-LINKS: REQ-SDS-281
func visDrawDMX(sf Surface, a visArea, spans []decode.Span) string {
	var pkt, cur []int
	in := false
	keep := func() {
		if in && len(cur) > 1 && len(cur) >= len(pkt) {
			pkt = cur
		}
	}
	for _, sp := range spans {
		if sp.Kind == "sof" {
			keep()
			cur, in = nil, true
			continue
		}
		if in && sp.Kind == "data" {
			cur = append(cur, sp.Val&0xff)
		}
	}
	keep()
	if len(pkt) < 2 {
		DrawText(sf, 8, a.y0+a.h/2, "no whole packet in this capture", colDim, 1)
		return ""
	}
	cw, ch := a.w/32, a.h/16
	for i := 0; i < 512; i++ {
		x, y := (i%32)*cw, a.y0+(i/32)*ch
		bg := rgb(8, 11, 15)
		if i+1 < len(pkt) {
			bg = rgb(16, 24, 32)
		}
		fillRect(sf, x+1, y+1, cw-2, ch-2, bg)
		if i+1 < len(pkt) {
			if v := pkt[i+1]; v > 0 {
				bh := (ch - 2) * v / 255
				fillRect(sf, x+1, y+ch-1-bh, cw-2, bh, rgb(uint8(60+v*7/10), uint8(120+v/4), uint8(230-v/2)))
			}
		}
		if i%8 == 0 {
			DrawText(sf, x+2, y+2, fmt.Sprint(i+1), colDim, 1)
		}
	}
	return fmt.Sprintf("start code %02X · %d slots", pkt[0], len(pkt)-1)
}

// visDrawLEDs draws the capture's last latched frame's LEDs (GRB) in their colours.
// TRLC-LINKS: REQ-SDS-281
func visDrawLEDs(sf Surface, a visArea, spans []decode.Span) string {
	var frame, cur []int
	for _, sp := range spans {
		switch {
		case sp.Kind == "sof":
			cur = nil
		case sp.Kind == "data":
			cur = append(cur, sp.Val&0xff)
		case sp.Kind == "rw" && sp.Text == "RST" && len(cur) >= 3:
			frame, cur = cur, nil
		}
	}
	if len(frame) < 3 {
		DrawText(sf, 8, a.y0+a.h/2, "no frame latched in this capture (one ends with its reset)", colDim, 1)
		return ""
	}
	leds := len(frame) / 3
	per := min(leds, max(8, a.w/40))
	d := min(a.w/per, a.h/((leds+per-1)/per))
	if d < 8 { // a long strip: as many as fit at the least size, the rest said
		d = 8
		per = max(1, a.w/d)
		leds = min(leds, per*max(1, a.h/d))
	}
	for i := 0; i < leds; i++ {
		g, r, b := frame[3*i], frame[3*i+1], frame[3*i+2]
		x, y := (i%per)*d, a.y0+(i/per)*d
		fillRect(sf, x+3, y+3, d-6, d-6, rgb(uint8(r), uint8(g), uint8(b)))
		strokeRect(sf, x+2, y+2, d-4, d-4, colGrid)
	}
	return fmt.Sprintf("%d LEDs (GRB)", len(frame)/3)
}

// visDrawPS2 lists the capture's key presses and releases (the scan codes,
// an extended key E0-prefixed), the newest last.
// TRLC-LINKS: REQ-SDS-281
func visDrawPS2(sf Surface, a visArea, spans []decode.Span) string {
	var lines []string
	ext, brk := false, false
	for _, sp := range spans {
		if sp.Kind != "data" || strings.HasPrefix(sp.Text, "H") {
			continue
		}
		b := sp.Val & 0xff
		switch b {
		case 0xe0:
			ext = true
			continue
		case 0xf0:
			brk = true
			continue
		}
		code := fmt.Sprintf("%02X", b)
		if ext {
			code = "E0 " + code
		}
		lines = append(lines, map[bool]string{true: "released ", false: "pressed  "}[brk]+code)
		ext, brk = false, false
	}
	if len(lines) == 0 {
		DrawText(sf, 8, a.y0+a.h/2, "no key in this capture", colDim, 1)
		return ""
	}
	rows := a.h / 12
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	for i, l := range lines {
		DrawText(sf, 12, a.y0+4+i*12, l, colC1, 1)
	}
	return fmt.Sprintf("this capture: %d key events (a key held across the gaps is not known)", len(lines))
}

// visDrawText writes the capture's text (a byte not printable as a dot).
// TRLC-LINKS: REQ-SDS-281
func visDrawText(sf Surface, a visArea, spans []decode.Span) string {
	var b strings.Builder
	for _, sp := range spans {
		if sp.Kind != "data" {
			continue
		}
		c := sp.Val & 0xff
		switch {
		case c == '\n':
			b.WriteByte('\n')
		case c >= 32 && c < 127:
			b.WriteByte(byte(c))
		case c != '\r':
			b.WriteByte('.')
		}
	}
	cols := max(10, (a.w-16)/6)
	var lines []string
	for _, l := range strings.Split(b.String(), "\n") {
		for len(l) > cols {
			lines, l = append(lines, l[:cols]), l[cols:]
		}
		lines = append(lines, l)
	}
	rows := a.h / 11
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	for i, l := range lines {
		DrawText(sf, 8, a.y0+4+i*11, l, colInfo, 1)
	}
	return "this capture's text"
}

// visDrawFrames lists the capture's frames by identifier (LIN's protected
// identifier's six bits, CAN's identifier): each one's newest bytes and how
// many came.
// TRLC-LINKS: REQ-SDS-281
func visDrawFrames(sf Surface, a visArea, spans []decode.Span, lin bool) string {
	type fr struct {
		bytes []int
		n     int
	}
	ids := map[int]*fr{}
	var order []int
	id, bytes, open := -1, []int(nil), false
	done := func() {
		if open && id >= 0 {
			f := ids[id]
			if f == nil {
				f = &fr{}
				ids[id] = f
				order = append(order, id)
			}
			f.bytes, f.n = bytes, f.n+1
		}
		id, bytes, open = -1, nil, false
	}
	for _, sp := range spans {
		switch {
		case sp.Kind == "sof":
			done()
			open = true
		case !open:
		case lin && strings.HasPrefix(sp.Text, "ID"):
			id = sp.Val & 0x3f
		case lin && strings.HasPrefix(strings.TrimPrefix(sp.Text, "!"), "CK"):
			if sp.Kind != "data" {
				id = -1 // its checksum failed
			}
			done()
		case !lin && sp.Kind == "id":
			id = sp.Val
		case sp.Kind == "data":
			bytes = append(bytes, sp.Val&0xff)
		case sp.Kind == "frame-error" || sp.Kind == "crc-error" || sp.Kind == "parity-error":
			id = -1
		}
	}
	done()
	if len(order) == 0 {
		DrawText(sf, 8, a.y0+a.h/2, "no whole frame in this capture", colDim, 1)
		return ""
	}
	for i, k := range order {
		y := a.y0 + 4 + i*14
		if y > a.y0+a.h-12 {
			break
		}
		f := ids[k]
		hex := make([]string, len(f.bytes))
		for j, b := range f.bytes {
			hex[j] = fmt.Sprintf("%02X", b)
		}
		DrawText(sf, 12, y, fmt.Sprintf("%03X", k), colSR, 1)
		DrawText(sf, 60, y, strings.Join(hex, " "), colInfo, 1)
		DrawTextRight(sf, a.w-12, y, fmt.Sprintf("x%d", f.n), colDim, 1)
	}
	return fmt.Sprintf("this capture: %d identifier%s", len(order), map[bool]string{true: "", false: "s"}[len(order) == 1])
}

// ---- readings ----

// visBigLast writes the capture's newest reading large (read from its spans
// by reading: its text and the line under it; ok false none).
// TRLC-LINKS: REQ-SDS-281
func visBigLast(sf Surface, a visArea, spans []decode.Span, reading func([]decode.Span) (string, string, bool)) string {
	big, small, ok := reading(spans)
	if !ok {
		DrawText(sf, 8, a.y0+a.h/2, "no whole reading in this capture", colDim, 1)
		return ""
	}
	scale := max(1, min(5, (a.w-40)/(max(1, len(big))*6)))
	DrawText(sf, (a.w-TextWidth(big, scale))/2, a.y0+a.h/2-scale*8, big, colInfo, scale)
	DrawText(sf, (a.w-TextWidth(small, 1))/2, a.y0+a.h/2+scale*6, small, colDim, 1)
	return "this capture's newest"
}

// visIRReading is the newest button: its address and command (A.. and C..).
// TRLC-LINKS: REQ-SDS-281
func visIRReading(spans []decode.Span) (string, string, bool) {
	addr, cmd, ok := "-", "", false
	name := ""
	for _, sp := range spans {
		switch {
		case sp.Kind == "sof" && sp.Text != "":
			name = sp.Text
		case sp.Kind == "data" && strings.HasPrefix(sp.Text, "A"):
			addr = sp.Text[1:]
		case sp.Kind == "data" && strings.HasPrefix(sp.Text, "C"):
			cmd, ok = sp.Text[1:], true
		}
	}
	return "A " + addr + "  C " + cmd, name, ok
}

// visCaliperReading is the newest reading ("12.34mm").
// TRLC-LINKS: REQ-SDS-281
func visCaliperReading(spans []decode.Span) (string, string, bool) {
	for i := len(spans) - 1; i >= 0; i-- {
		if spans[i].Kind == "rw" && (strings.HasSuffix(spans[i].Text, "mm") || strings.HasSuffix(spans[i].Text, "in")) {
			return spans[i].Text, "caliper", true
		}
	}
	return "", "", false
}

// visWiegandReading is the newest card: its number, facility and length.
// TRLC-LINKS: REQ-SDS-281
func visWiegandReading(spans []decode.Span) (string, string, bool) {
	bits, fac, card, ok := "", "", "", false
	for _, sp := range spans {
		switch {
		case sp.Kind == "rw":
			bits = sp.Text
		case sp.Kind == "data" && strings.HasPrefix(sp.Text, "F"):
			fac = sp.Text[1:]
		case sp.Kind == "data" && strings.HasPrefix(sp.Text, "C"):
			card, ok = sp.Text[1:], true
		}
	}
	return "card " + card, bits + " · facility " + fac, ok
}

// visPWMReading is the capture's mean duty and the periods it holds.
// TRLC-LINKS: REQ-SDS-281
func visPWMReading(spans []decode.Span) (string, string, bool) {
	sum, n := 0.0, 0
	for _, sp := range spans {
		var d float64
		if sp.Kind == "data" {
			if _, err := fmt.Sscanf(sp.Text, "%f%%", &d); err == nil {
				sum += d
				n++
			}
		}
	}
	if n == 0 {
		return "", "", false
	}
	return fmt.Sprintf("%.1f %%", sum/float64(n)), fmt.Sprintf("the mean of %d periods", n), true
}

// visDCFReading is the minute's time and date (BCD fields).
// TRLC-LINKS: REQ-SDS-281
func visDCFReading(spans []decode.Span) (string, string, bool) {
	f := map[string]int{}
	for _, sp := range spans {
		if sp.Kind != "data" {
			continue
		}
		for _, k := range []string{"MIN", "HR", "DAY", "DOW", "MON", "YR"} {
			if strings.HasPrefix(sp.Text, k) {
				f[k] = (sp.Val>>4)*10 + sp.Val&0xf
			}
		}
	}
	mn, ok1 := f["MIN"]
	hr, ok2 := f["HR"]
	if !ok1 || !ok2 {
		return "", "", false
	}
	date := ""
	if d, ok := f["DAY"]; ok {
		date = fmt.Sprintf("%02d.%02d.20%02d", d, f["MON"], f["YR"])
	}
	return fmt.Sprintf("%02d:%02d", hr, mn), date, true
}

// visStepReading is the capture's steps: the newest count from its start and its direction.
// TRLC-LINKS: REQ-SDS-281
func visStepReading(spans []decode.Span) (string, string, bool) {
	for i := len(spans) - 1; i >= 0; i-- {
		t := spans[i].Text
		if spans[i].Kind == "data" && len(t) > 1 && (t[0] == 'F' || t[0] == 'R') {
			return t[1:] + " steps", map[bool]string{true: "forward", false: "reverse"}[t[0] == 'F'] + " · counted from this capture's start", true
		}
	}
	return "", "", false
}
