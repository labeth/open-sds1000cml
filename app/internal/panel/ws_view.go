// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"strings"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/analysis"
	"open-sds/app/internal/buildinfo"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/streamview"
)

// Display is the instrument display state the LCD renders the frame with
// (the trace overlays the panel owns).
type Display struct {
	ShowC1, ShowC2   bool
	ShowMeas         bool
	MeasItems        []string // "1:pkpk": the chosen readouts, channel and parameter
	ViewMode         int      // 0 Y-T, 1 X-Y, 2 FFT, 3 Bode, 4 spectrogram
	MathMode         int
	Zoom             int
	ZoomOff          float64
	Persist          bool
	FFTSrc           int
	DecProto         int
	DecBaud          int
	DecChA, DecChB   int
	DecCPOL, DecCPHA bool
	DecLSB           bool
	DecParity        string // Modbus RTU's framing ("": found by the decoder)
	DecInverted      bool   // the IR and DALI lines' marks at the high level
	DecBits          int    // SIRC's or Wiegand's frame length (0: not known)
	// DecLayer is the bus's layer (analysis.Layers' ID, "" none) and
	// DecLayerAddr/Dir the I2C filter's address (-1 any) and direction.
	DecLayer     string
	DecLayerAddr int
	DecLayerDir  int
	DecDelayNs   float64 // SPI's data delay in use after the sampling edge (ns)
	DecFormat    int
	// Auto: a detection runs (DecDetecting); the settings are what it filled
	// in (DecAuto); what the last one found, or why it found none (DecNote)
	DecDetecting bool
	DecAuto      bool
	DecNote      string
	CurOn        bool
	CurType      int
	CurSel       int
	CurX, CurY   [2]float64 // where the screen shows the cursors now: fractions across and down (beyond 0..1 off it)
	CurT, CurV   [2]float64 // their signal: times from the trigger (s), volts at CurVCh's tip
	CurVCh       int
	AutosetBusy  bool
	AutosetMsg   string
	Remote       string // the last remote change, while it shows
}

// Snapshot returns the display state and the UI view, read together.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func (c *Controller) Snapshot() (Display, wsview.View) { return c.displayAndView(c.snap()) }

// View is the UI snapshot alone.
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) View() wsview.View { return c.view(c.snap()) }

// view builds the UI snapshot from one state read.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func (c *Controller) view(s *sv) wsview.View {
	u := &s.ui
	if !(u.ws == wsview.Bus && u.busEye) {
		c.endEyeRaw() // the eye left: nothing waits for its raw record
	}
	v := wsview.View{WS: u.ws}
	if s.now.Before(u.noticeUnt) {
		v.Notice = u.notice
	} else if s.st.ADCFault != "" {
		v.Notice = s.st.ADCFault // stays while converters do not convert
	}
	if u.open {
		v.Layout = wsview.Layout{MenuW: 116, Sheet: true, HideTraces: true, HideBottom: true}
		v.Sheet = c.sheetView(s)
		title, keys := c.sheetKeys(s)
		flash := -1
		if u.sub == subNone { // the flash belongs to the row's own keys
			flash = flashIndex(s, curRow(u).id)
		}
		v.Keys = keyColumn(title, 30, keys, flash)
		return v
	}
	v.Layout = wsview.Layout{MenuW: 30, PlotTop: wsDefs[u.ws].plotTop}
	v.Rail = !u.wpick
	if v.Rail && s.now.Before(u.noteUntil) {
		v.RailNote = &wsview.Note{WS: u.noteWS, Head: u.noteHead, Text: u.noteText}
	}
	switch u.ws {
	case wsview.Scope:
		v.Scope = c.scopePane(s)
	case wsview.Bus:
		switch {
		case u.wpick:
			v.Layout.MenuW = 116
			v.Picker = pickerView(s)
			v.Keys = keyColumn("PICK BYTES", 12, c.pickKeys(s), flashIndex(s, "wpick"))
			v.Bus = c.busPane(s) // the strip marks still show
		case u.busEye:
			v.Layout.HideTraces, v.Layout.HideBottom = true, true
			v.Eye = c.eyePane(s)
		case u.busStream:
			v.Bus = c.streamPane(s)
		default:
			v.Bus = c.busPane(s)
		}
	case wsview.Spectrum:
		v.Layout.HideViewCaptions, v.Layout.HideBottom = true, true
		v.Spec = c.specPane(s)
	case wsview.Measure:
		v.Layout.HideMeas = true
		v.Meas = c.measPane(s)
	case wsview.Compare:
		v.Cmp = c.cmpPane(s)
	}
	return v
}

// flashIndex is the F-key flashing on row id (-1 none).
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func flashIndex(s *sv, id string) int {
	if s.ui.flashRow == id && s.now.Before(s.ui.flashUntil) {
		return s.ui.flashI
	}
	return -1
}

// keyColumn lays F-key choices out for the LCD's text column.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func keyColumn(title string, top int, keys []fkey, flash int) *wsview.Keys {
	k := &wsview.Keys{Title: title, Top: top}
	for i := 0; i < len(keys) && i < 5; i++ {
		k.Items[i] = wsview.Key{Label: keys[i].label, Value: keys[i].value, Sel: keys[i].sel || i == flash, Dim: keys[i].dim}
	}
	return k
}

// ---- the sheet ----

// sheetView builds the open sheet.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) sheetView(s *sv) *wsview.Sheet {
	u := &s.ui
	t := sheetTabs[u.tab]
	sh := &wsview.Sheet{WSName: wsview.Names[u.ws], ThumbView: s.viewMode, FFTCh: s.fftSrc}
	own := len(wsDefs[u.ws].tabs)
	for i, ti := range tabOrder(u.ws) {
		sh.Tabs = append(sh.Tabs, wsview.Tab{Name: sheetTabs[ti].name, Own: i < own, Active: ti == u.tab})
	}
	focus := clampInt(u.rowAt[u.tab], 0, len(t.rows)-1)
	c.amu.Lock()
	allVals, allOK := c.ana.allVals, c.ana.allOK
	c.amu.Unlock()
	for i, r := range t.rows {
		if r.hdr != "" {
			sh.Rows = append(sh.Rows, wsview.Row{Kind: wsview.Header, Header: r.hdr})
		}
		sh.Rows = append(sh.Rows, c.rowView(s, r, i == focus, i == focus && u.edit, allVals, allOK))
	}
	sh.Status = tabStatus(c, s, t)
	if t.id == "sys" {
		sh.Guide = guidePage(s)
	}
	sh.Side = c.sideView(s, curRow(u))
	switch {
	case u.sub == subPick:
		sh.Sub = &wsview.Sub{Title: fmt.Sprintf("PICK FROM SCREEN  (%d bytes decoded)", len(u.pick.toks)), Toks: u.pick.toks, Cur: u.pick.start,
			Lines: []wsview.Span{{Text: "| = idle gap (message start).  Underlined: the next bytes.", Col: wsview.Dim},
				{Text: "Turn Adjust/Position: first byte.  F1..F4: trigger on", Col: wsview.Dim},
				{Text: "1..4 bytes from it (sets the match, type Serial).", Col: wsview.Dim},
				{Text: "F5 or push Adjust: cancel.   Now: " + matchText(s), Col: wsview.Text}}}
	}
	sh.Hint = sheetHint(s, t)
	return sh
}

// rowView is one row as drawn.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) rowView(s *sv, r *row, focus, edit bool, allVals [2][]float64, allOK [2]bool) wsview.Row {
	rel := r.relevant(s)
	rv := wsview.Row{Kind: r.kind, Label: r.label, Rel: rel, Focus: focus, Edit: edit}
	switch r.kind {
	case wsview.Enum:
		cur := r.get(s)
		for _, o := range r.options(s) {
			sel := o.v == cur
			rv.Opts = append(rv.Opts, wsview.Opt{Text: o.t, Sel: sel, Quiet: sel && (o.t == "Off" || !rel)})
		}
		if r.meas > 0 {
			for ch := 0; ch < 2; ch++ {
				if s.chDisp[ch] && allOK[ch] && len(allVals[ch]) > r.meas-1 {
					rv.Meas[ch] = fmtMeas(r.meas-1, allVals[ch][r.meas-1])
				} else if s.chDisp[ch] {
					rv.Meas[ch] = "---"
				}
			}
		}
	case wsview.Number:
		rv.Value, rv.Gauge = r.valueText(s), r.gauge(s)
	case wsview.Action, wsview.Slot:
		fl := flashIndex(s, r.id)
		for i, a := range r.acts(s) {
			rv.Acts = append(rv.Acts, wsview.Act{Text: a.t, Dim: a.dim || !rel, Flash: i == fl})
		}
		if r.status != nil {
			rv.Status = r.status(s)
		}
		if r.kind == wsview.Slot {
			rv.Used = s.slotUsed[r.slot]
			rv.Value = r.status(s)
		}
	case wsview.Bytes:
		rv.Value = matchText(s)
		rv.Armed = s.st.SerialMode != 0
	}
	return rv
}

// sideView explains the highlighted row (or the sub-mode).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) sideView(s *sv, r *row) wsview.Side {
	u := &s.ui
	const nc = 31 // characters per side-panel line
	dim := func(t string) wsview.Span { return wsview.Span{Text: t, Col: wsview.Dim} }
	text := func(t string) wsview.Span { return wsview.Span{Text: t, Col: wsview.Text} }
	switch u.sub {
	case subMatch:
		return wsview.Side{Title: "MATCH EDITOR", Lines: []wsview.Span{text("Turn Adjust: the key."), text("Position < >: the cursor."),
			text(""), text("F2 or push Adjust: type it;"), text("F1 changes the key set."), text(""),
			text("?? = any byte, A? = nibble,"), text("ERR = a framing error."), text(""), text("F4: use. Menu On/Off: discard.")}}
	case subPick:
		return wsview.Side{Title: "PICK FROM SCREEN", Lines: []wsview.Span{text("Bytes decoded on screen,"), text("starting at the first"),
			text("complete message."), text(""), text("Turn Adjust or Position:"), text("the first byte to match."), text(""),
			text("Push Adjust or F1: trigger"), text("on it (Serial); F2-F4: on"), text("2-4 bytes. F5/Menu: back.")}}
	}
	rel := r.relevant(s)
	sd := wsview.Side{Label: r.label, Value: r.longText(s)}
	sd.Big = len(sd.Value) <= 15
	if !sd.Big {
		for _, l := range wrapText(sd.Value, nc) {
			sd.Lines = append(sd.Lines, text(l))
		}
		sd.Value = ""
	}
	if !rel && r.why != "" {
		for _, l := range wrapText("Not in use: needs "+r.why, nc) {
			sd.Lines = append(sd.Lines, wsview.Span{Text: l, Col: wsview.Warn})
		}
	}
	for _, l := range wrapText(r.help, nc) {
		sd.Lines = append(sd.Lines, dim(l))
	}
	if r.kind == wsview.Number {
		for _, l := range wrapText("Range "+r.rangeText(s), nc) {
			sd.Lines = append(sd.Lines, dim(l))
		}
	}
	if r.knob != "" {
		for _, l := range wrapText(r.knob, nc) {
			sd.Lines = append(sd.Lines, text(l))
		}
	}
	switch {
	case u.edit:
		sd.Hints = []wsview.Span{{Text: "EDITING", Col: wsview.Amber}, dim("Turn Adjust: change"), dim("Push Adjust: done")}
	case r.kind == wsview.Action || r.kind == wsview.Slot:
		sd.Hints = []wsview.Span{dim("F1..F5: run (beside the keys)"), dim("Nothing to edit here"), dim("Adjust: other rows")}
	case r.kind == wsview.Bytes:
		sd.Hints = []wsview.Span{dim("F1 edit  F2 from screen"), dim("F3 any byte"), dim("Push Adjust: edit")}
	default:
		sd.Hints = []wsview.Span{dim("F1..F5: pick"), dim("Push Adjust: edit"), dim("Position < >: change")}
	}
	return sd
}

// sheetHint is the sheet's bottom line: tab, row, pick, and the key that
// really closes it.
// TRLC-LINKS: REQ-SDS-212
func sheetHint(s *sv, t *tab) wsview.Span {
	u := &s.ui
	back := "Menu On/Off: back to " + wsview.Names[u.ws]
	switch {
	case u.sub != subNone:
		return wsview.Span{Text: "Menu On/Off: close and discard   S/DIV: other tab (discard)", Col: wsview.Dim}
	case u.edit:
		return wsview.Span{Text: "EDIT: turn Adjust to change, push Adjust when done      " + back, Col: wsview.Amber}
	}
	closer := t.key
	if t.id == "cmp" || t.id == "sys" {
		closer = keyNames[u.via]
	}
	h := "S/DIV: tab   Adjust: row   F1-F5: pick   "
	if closer != "" {
		h += closer + " or "
	}
	h += back
	if t.id != "sys" {
		h += "   Help: guide"
	}
	return wsview.Span{Text: h, Col: wsview.Dim}
}

// noiseTriggerWarn is the share of edge triggers dropped as noise from which
// the TRIG tab warns.
const noiseTriggerWarn = 0.25

// tabStatus is the live status under a tab's rows.
// TRLC-LINKS: REQ-SDS-212
func tabStatus(c *Controller, s *sv, t *tab) []wsview.Span {
	dim := func(x string) wsview.Span { return wsview.Span{Text: x, Col: wsview.Dim} }
	var out []wsview.Span
	switch t.id {
	case "trig":
		st := "Trig'd"
		switch {
		case !s.st.Running:
			st = "Stop"
		case s.st.Single:
			st = "Armed (single)"
		case s.st.Norm:
			st = "Normal (waits for a trigger)"
		default:
			st = "Auto"
		}
		out = append(out, dim("Status: "+st))
		if s.st.SerialMode != 0 {
			out = append(out, dim("Serial trigger on "+matchText(s)+"; the bus is the BUS tab's"))
		}
		// Pulse, slope, video and zones are decided in the FPGA: until it
		// can (its image loading, the levels being learned) nothing
		// triggers, and this says why.
		hw := func(what, backend string) {
			if what == "" {
				return // an edge trigger, or a serial one: their own lines
			}
			if why, ok := strings.CutPrefix(backend, "none: "); ok {
				out = append(out, wsview.Span{Text: what + ": no hardware trigger yet - " + why, Col: wsview.Warn})
			} else if strings.HasPrefix(backend, "hardware") {
				out = append(out, dim(what+" decided in the FPGA"))
			}
		}
		hw(map[int]string{1: "Pulse width", 2: "Slope", 3: "Video line"}[trigTypeIndex(s.st)], s.st.TriggerBackend)
		hw("Zones", s.st.ZoneBackend)
		// Edge triggers with no edge at the level, dropped: the FPGA fires
		// on the converters' noise at a small signal's rail
		if trigTypeIndex(s.st) == 0 && s.st.NoiseTriggers > noiseTriggerWarn {
			out = append(out, wsview.Span{Text: fmt.Sprintf("%.0f%% of triggers dropped as noise: raise the gain or move the level", 100*s.st.NoiseTriggers), Col: wsview.Warn})
		}
	case "sys":
		// The firmware's name and version, read on the scope itself.
		out = append(out, dim("open-sds1000cml "+buildinfo.Version+"  ("+buildinfo.Commit+")"))
	case "curs":
		if s.curOn {
			if s.curType == 0 {
				ta, tb := cursorTimes(s)
				dt := tb - ta
				line := "dt " + si(abs64(dt), "s")
				if dt != 0 {
					line += "  1/dt " + si(1/abs64(dt), "Hz")
				}
				out = append(out, wsview.Span{Text: line, Col: wsview.Amber})
			} else {
				var parts []string
				for ch := 0; ch < 2; ch++ {
					if s.chDisp[ch] {
						va, vb := cursorVolts(s, ch)
						parts = append(parts, fmt.Sprintf("dV%d %s", ch+1, si(abs64(vb-va), "V")))
					}
				}
				out = append(out, wsview.Span{Text: strings.Join(parts, "  "), Col: wsview.Amber})
			}
		} else {
			out = append(out, dim("Cursors off: pick Time or Volts above (MEASURE: Adjust push)"))
		}
	case "bus":
		c.amu.Lock()
		res, ok := c.ana.res, c.ana.resOK
		c.amu.Unlock()
		if s.decProto == framecalc.ProtoOff {
			out = append(out, dim("Decode off. The BUS workspace (F2) detects the bus and decodes it."))
		} else if ok {
			out = append(out, dim(fmt.Sprintf("%s: %d units decoded on screen", busName(s.decProto, s.decBaud, s.decChA, s.decChB, s.decCPOL, s.decCPHA), len(res.Bytes))))
			if res.Text != "" {
				out = append(out, wsview.Span{Text: cut("Screen: "+res.Text, 76), Col: wsview.Text})
			}
		} else if res.Error != "" {
			out = append(out, wsview.Span{Text: cut(res.Error, 76), Col: wsview.Stop})
		}
	case "acq":
		if s.st.AcqMode == engine.AcqAverage {
			out = append(out, dim(fmt.Sprintf("Averaging %d captures", s.st.AvgCount)))
		}
		if s.srActive || s.srStatus != "" {
			out = append(out, wsview.Span{Text: cut("Super-res: "+s.srStatus, 76), Col: wsview.SR})
		}
	case "cmp":
		if s.st.MaskMode != 0 {
			col := wsview.Dim
			if s.st.MaskFail > 0 {
				col = wsview.Stop
			}
			out = append(out, wsview.Span{Text: fmt.Sprintf("Mask: pass %d  fail %d", s.st.MaskPass, s.st.MaskFail), Col: col})
		}
		if s.maskMsg != "" {
			out = append(out, dim(s.maskMsg))
		}
		out = append(out, dim("COMPARE (F5) shows each reference against the live trace."))
	case "meas":
		if s.showMeas {
			out = append(out, dim(fmt.Sprintf("%d of at most %d readouts on screen", len(s.meas), maxMeasItems)))
		} else {
			out = append(out, dim("Readouts hidden (Readouts = On shows them)"))
		}
		out = append(out, dim("MEASURE (F4) keeps statistics of the chosen items."))
	case "time":
		w := 10 * s.st.TdivS
		line := "Screen " + si(w, "s") + " wide"
		if s.zoom > 1 {
			line = fmt.Sprintf("Screen %s wide (zoom %dx of %s)", si(w/float64(s.zoom), "s"), s.zoom, si(w, "s"))
		}
		c.amu.Lock()
		g := c.ana.g
		c.amu.Unlock()
		switch {
		case g.peak && g.captureS > 0 && g.captureS < g.sampleS:
			// a peak-detect readout: the converters' interval, not the span
			// of a min/max point (500ns for 2ns sampling)
			line += "   sampled every " + si(g.captureS, "s") + ", shown peak-detected"
		case g.sampleS > 0:
			line += "   sample interval " + si(g.sampleS, "s")
		}
		out = append(out, dim(line))
	case "view":
		out = append(out, dim("Y-T: volts over time      X-Y: CH1 across, CH2 up"), dim("FFT: spectrum of the shown channels"),
			dim("Bode: frequency response sweep (gain and phase)"), dim("Spectrogram: FFT of the source over time, newest at the top"),
			dim("SPECTRUM (F3) shows the analysis views with a peak table."))
	}
	return out
}

// pickerView is the BUS workspace picker.
// TRLC-LINKS: REQ-SDS-214
func pickerView(s *sv) *wsview.Picker {
	p := s.ui.pick
	return &wsview.Picker{Caption: fmt.Sprintf("to trigger on  (%d decoded on screen)", len(p.toks)), Toks: p.toks, Cur: p.start,
		Lines: []wsview.Span{{Text: "| = idle gap (a message starts).  Underlined: the bytes the F-keys would add.", Col: wsview.Dim},
			{Text: "Turn Adjust or Position: the first byte.  Push Adjust (or F1): trigger on it; F2-F4: on 2-4 bytes.", Col: wsview.Text},
			{Text: "F5 or Menu On/Off: back, the trigger as it was.", Col: wsview.Dim}},
		Now: "Now: " + matchText(s) + map[bool]string{true: "  (serial trigger on)", false: ""}[s.st.SerialMode != 0]}
}

// ---- the panes ----

// scopePane is SCOPE's chrome: the Adjust tag.
// TRLC-LINKS: REQ-SDS-213
func (c *Controller) scopePane(s *sv) *wsview.ScopePane {
	p := &wsview.ScopePane{}
	if r := adjTarget(s); r != nil {
		p.AdjustTag = cut("Adjust: "+r.label+" "+r.valueText(s), 38)
		p.Hot = s.now.Sub(s.ui.adjT) < 1500*time.Millisecond
	}
	return p
}

// busPane builds the BUS packet list.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) busPane(s *sv) *wsview.BusPane {
	u := &s.ui
	p := &wsview.BusPane{}
	p.Title = "BUS"
	p.Again = "F2 again: Eye diagram"
	zoomJob := "on packet"
	if s.zoom > 1 {
		zoomJob = "zoom off"
	}
	c.amu.Lock()
	a := &c.ana
	dec := busDecoding(s)
	res := a.res
	rows, partial := busRows(a)
	rows = append([]Packet(nil), rows...)
	sel := busSelected(a, rows, partial)
	live := append([]Packet(nil), a.live...)
	liveSel := selectedLive(a, partial, sel)
	c.amu.Unlock()
	if !dec {
		failed := u.busFailed
		switch {
		case u.busDetecting:
			p.Caption, p.CapCol = "AUTO: detecting the bus on CH1 and CH2...", wsview.Amber
			p.Msg = []wsview.Span{{Text: "DETECTING...", Col: wsview.Text}}
		default:
			p.Caption, p.CapCol = map[bool]string{true: "no bus found - decode is off", false: "decode is off"}[failed], wsview.Dim
			p.Msg = []wsview.Span{{Text: map[bool]string{true: "NO BUS FOUND", false: "DECODE IS OFF"}[failed], Col: wsview.Text},
				{Text: "Push Adjust: try again - auto-detects UART, I2C, SPI and more on CH1/CH2 and decodes it", Col: wsview.Amber},
				{Text: "Utility or Menu On/Off: set the protocol, baud and lines by hand on the BUS sheet", Col: wsview.Dim}}
			if failed {
				if u.busNote != "" { // what the search did: AUTO, BUS entry or Adjust
					p.Caption = "AUTO: " + u.busNote
				}
				p.Msg = append(p.Msg, wsview.Span{Text: "Check that the bus is connected and its channel is not on GND coupling.", Col: wsview.Dim})
			}
		}
		p.Right = "Utility: BUS sheet"
		p.Off = true
		p.Tool = append([]wsview.Seg{{Key: "Adjust push", Job: "try again"}}, append(posTool(s), wsview.Seg{Key: "S/DIV push", Job: "zoom"})...)
		return p
	}
	toks := matchTokens(s)
	hits := 0
	for _, q := range rows {
		if tokMatch(toks, q) {
			hits++
		}
	}
	switch {
	case len(toks) == 0:
		p.Right = "match bytes: none yet"
	case s.st.SerialMode != 0:
		p.Right = fmt.Sprintf("TRIG ON %s: %d %s", strings.Join(toks, " "), hits, plural(hits, "packet"))
	default:
		p.Right = fmt.Sprintf("match %s: %d %s", strings.Join(toks, " "), hits, plural(hits, "packet"))
	}
	name := busName(s.decProto, s.decBaud, s.decChA, s.decChB, s.decCPOL, s.decCPHA)
	if s.decAuto {
		name += " (Auto)"
	}
	if l, ok := analysis.LayerByID(s.decLayer); ok && s.decLayer != "" {
		name += " / " + l.Name
	}
	if !res.OK {
		p.Caption, p.CapCol, p.Error = res.Error, wsview.Stop, res.Error
	} else {
		cutN := 0
		for _, q := range live {
			if !q.Complete {
				cutN++
			}
		}
		cap := fmt.Sprintf("%s  -  %d %s", name, len(rows), plural(len(rows), "packet"))
		if partial {
			cap += " cut by the record edges"
		} else if cutN > 0 {
			cap += fmt.Sprintf(", %d cut at the edges", cutN)
		}
		p.Caption, p.CapCol = cap, wsview.Text
	}
	if u.busDetecting { // Auto again, on the bus decoded meanwhile
		p.Caption, p.CapCol = "AUTO: detecting the bus on CH1 and CH2...", wsview.Amber
	}
	// The trigger is the FPGA's matcher (ADR-SERIAL-HARDWARE-TRIGGER): while
	// none can take the request nothing triggers, and the caption says why.
	if why, ok := strings.CutPrefix(s.st.SerialBackend, "none: "); ok && s.st.SerialMode != 0 {
		p.Caption, p.CapCol = "No hardware trigger yet: "+why, wsview.Warn
	}
	if len(rows) == 0 {
		p.Empty = "No packets on screen"
	}
	// the lines: 16 bytes each, at most 3 per packet; with a layer, its note
	// on one line a packet and the selected packet's bytes on at most two
	type line struct {
		pi, from, to int
		more, note   bool
	}
	layered := s.decLayer != ""
	if l, ok := analysis.LayerByID(s.decLayer); ok && layered {
		p.Layer = l.Name
	}
	var lines []line
	for i, q := range rows {
		n := len(q.Bytes)
		k := min(3, max(1, (n+15)/16))
		if layered {
			lines = append(lines, line{pi: i, note: true})
			if i != sel || n == 0 {
				continue
			}
			k = min(2, k)
		}
		for part := 0; part < k; part++ {
			more := part == k-1 && n > k*16
			to := min(n, part*16+16)
			if more {
				to = part*16 + 15
			}
			lines = append(lines, line{pi: i, from: part * 16, to: to, more: more})
		}
	}
	selLine := 0
	for selLine < len(lines) && lines[selLine].pi != sel {
		selLine++
	}
	top := clampInt(selLine-2, 0, max(0, len(lines)-8))
	for r := 0; r < 8 && top+r < len(lines); r++ {
		ln := lines[top+r]
		q := rows[ln.pi]
		bl := wsview.BusLine{Sel: ln.pi == sel, More: ln.more, Fault: q.Err}
		if ln.note {
			bl.Note, bl.NoteErr = q.Note, q.NoteErr
			if bl.Note == "" {
				bl.Note = fmt.Sprintf("(%d %s)", len(q.Bytes), plural(len(q.Bytes), "byte"))
			}
		}
		if ln.from == 0 && (ln.note || !layered) {
			bl.Num, bl.Bytes = ln.pi+1, len(q.Bytes)
			if q.HasAddr { // as the web's list writes it
				bl.Addr = analysis.AddrText(res.Proto, q)
			}
			if len(toks) > 0 && tokMatch(toks, q) {
				bl.Match = cut(strings.Join(toks, " "), 24)
			} else if !q.Complete {
				bl.Cut = "cut at the record edge"
			}
		}
		if ln.note {
			p.Lines = append(p.Lines, bl)
			continue
		}
		var str []byte
		for k := ln.from; k < ln.to; k++ {
			bad := k < len(q.ByteErr) && q.ByteErr[k]
			b := q.Bytes[k]
			if bad {
				bl.Hex = append(bl.Hex, "ER")
				str = append(str, '.')
			} else {
				bl.Hex = append(bl.Hex, hex2(b))
				if b >= 32 && b < 127 {
					str = append(str, b)
				} else {
					str = append(str, '.')
				}
			}
			bl.Err = append(bl.Err, bad)
		}
		bl.Str = string(str)
		p.Lines = append(p.Lines, bl)
	}
	p.Up, p.Dn = top > 0, top+8 < len(lines)
	// the strip: the selected packet boxed (found by its message), matches ticked
	for i, q := range live {
		p.Marks = append(p.Marks, wsview.PacketMark{I0: q.I0, I1: q.I1, Sel: i == liveSel, Match: len(toks) > 0 && tokMatch(toks, q),
			Text: q.Note, Err: q.NoteErr})
	}
	p.SelNum = sel + 1
	p.Tool = append(append([]wsview.Seg{{Key: "Adjust", Job: "packet"}, {Key: "push", Job: "pick bytes"}}, posTool(s)...), wsview.Seg{Key: "Zoom", Job: zoomJob})
	return p
}

// streamPane builds BUS's Stream view: the stream's state, what stops it
// and the controls, over the transcript the plot shows (or, with no bus,
// why there is none and how to find it).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (c *Controller) streamPane(s *sv) *wsview.BusPane {
	u := &s.ui
	p := &wsview.BusPane{Off: true}
	p.Title, p.Again = "BUS", "F2 again: Packets"
	stops := streamStopText(s.decTrig, s.decValue)
	switch {
	case u.busDetecting:
		p.Caption, p.CapCol = "STREAM: detecting the bus on CH1 and CH2...", wsview.Amber
		p.Msg = []wsview.Span{{Text: "DETECTING...", Col: wsview.Text},
			{Text: "The stream starts on the bus found.", Col: wsview.Dim}}
		p.Tool = posTool(s)
		return p
	case !busDecoding(s):
		p.Caption, p.CapCol = "STREAM: no bus decoded", wsview.Dim
		p.Msg = []wsview.Span{{Text: map[bool]string{true: "NO BUS FOUND", false: "NO BUS DECODED"}[u.busFailed], Col: wsview.Text},
			{Text: "Push Adjust: detect the bus on CH1/CH2 - the stream then starts on it", Col: wsview.Amber},
			{Text: "Utility or Menu On/Off: set the protocol, baud and lines by hand on the BUS sheet", Col: wsview.Dim},
			{Text: "A stream decodes the bus into a transcript (Stream mode on the BUS sheet says how)", Col: wsview.Dim}}
		if u.busFailed {
			p.Msg[3] = wsview.Span{Text: "Check that the bus is connected and its channel is not on GND coupling.", Col: wsview.Dim}
		}
		p.Right = "Utility: BUS sheet"
		p.Tool = append([]wsview.Seg{{Key: "Adjust push", Job: "detect the bus"}}, posTool(s)...)
		return p
	case !s.hasStream || !serialProto(s.decProto):
		p.Caption, p.CapCol = "STREAM: "+protoName(s.decProto), wsview.Dim
		p.Msg = []wsview.Span{{Text: "NO HARDWARE STREAM", Col: wsview.Text},
			{Text: "This scope has no hardware decoder to stream " + protoName(s.decProto) + ".", Col: wsview.Dim}}
		p.Tool = posTool(s)
		return p
	}
	name := busName(s.decProto, s.decBaud, s.decChA, s.decChB, s.decCPOL, s.decCPHA)
	p.Caption, p.CapCol = "STREAM  "+name, wsview.Text
	p.Right = "stops " + stops
	v, _ := c.StreamStatus()
	head, col := "STARTING", wsview.Amber
	push, again := "stop", "Run/Stop or push Adjust: stop - the record goes on screen"
	switch v.State {
	case streamview.Streaming:
		head, col = "STREAMING", wsview.Text
	case streamview.Triggered:
		head, col = "STOPPED ON THE TRIGGER", wsview.Amber
	case streamview.Stopped:
		head, col = "STOPPED", wsview.Amber
	case streamview.Failed:
		head, col = "NOT STREAMING", wsview.Stop
	}
	if s.streamStopped {
		push, again = "stream again", "Run/Stop or push Adjust: stream again"
	}
	p.Msg = []wsview.Span{{Text: head, Col: wsview.Text},
		{Text: fmt.Sprintf("%d units  %s/s  errors %d  lost %d", v.Units, si(math.Round(v.UnitsPerSecond), ""), v.Errors, v.Lost), Col: col},
		{Text: "Mode: " + streamModeText(s), Col: wsview.Dim}}
	switch {
	case v.Err != "":
		p.Msg = append(p.Msg, wsview.Span{Text: cut(v.Err, 120), Col: wsview.Stop})
	case v.Note != "" && v.State == streamview.Starting:
		p.Msg = append(p.Msg, wsview.Span{Text: cut(v.Note, 120), Col: wsview.Amber})
	}
	p.Msg = append(p.Msg, wsview.Span{Text: again, Col: wsview.Dim},
		wsview.Span{Text: "Adjust: what stops it (an error, the Stream value, by hand)", Col: wsview.Dim})
	p.Tool = append([]wsview.Seg{{Key: "Adjust", Job: "stops " + map[int]string{0: "on error", 1: "on value", 2: "by hand"}[mod3(s.decTrig)]},
		{Key: "push", Job: push}}, posTool(s)...)
	if s.streamStopped {
		p.Tool = append(p.Tool, wsview.Seg{Key: "Zoom", Job: map[bool]string{true: "wave", false: "list"}[s.streamList]})
	}
	return p
}

// plural is "1 packet" / "2 packets".
// TRLC-LINKS: REQ-SDS-214
func plural(n int, w string) string {
	if n == 1 {
		return w
	}
	return w + "s"
}

// eyePane builds the BUS eye's pane and grid.
// TRLC-LINKS: REQ-SDS-215
func (c *Controller) eyePane(s *sv) *wsview.EyePane {
	u := &s.ui
	p := &wsview.EyePane{}
	p.Title, p.Again = "BUS", "F2 again: Stream"
	zoomed := s.zoom > 1
	c.amu.Lock()
	a := &c.ana
	dec := busDecoding(s)
	r, ok, why, ch := a.eyeRes, a.eyeOK, a.eyeWhy, a.eyeCh
	g, at := a.g, a.eyeAt
	th := eyeThreshold(u, r)
	var cur EyeBox
	if ok && a.eye != nil {
		cur = a.eye.Opening(1+u.eyeX, th) // the opening at the cursors
	}
	c.amu.Unlock()
	name := "no bus decoded"
	if dec {
		c.leaseEyeRaw() // between captures too: a deep raw record outlasts a lease renewed per capture
		name = busName(s.decProto, s.decBaud, s.decChA, s.decChB, s.decCPOL, s.decCPHA)
	}
	p.Caption = "EYE  " + name
	if zoomed {
		p.Caption += fmt.Sprintf("  (zoom %dx window)", s.zoom)
	}
	p.TimeLabel = "TIME"
	if zoomed {
		p.TimeLabel = "TIME (ZOOM)"
	}
	p.Ch = ch
	zoomJob := "finer samples"
	if zoomed {
		zoomJob = "zoom off"
	}
	p.Tool = append(append([]wsview.Seg{{Key: "Adjust", Job: "threshold"}, {Key: "push", Job: "clear eye"}}, posTool(s)...), wsview.Seg{Key: "Zoom", Job: zoomJob})
	if !ok {
		p.Why = why
		if p.Why == "" {
			p.Why = "Folding the bit stream..."
		}
		return p
	}
	p.Right = fmt.Sprintf("%.0f UI folded from %d %s", r.UIs, r.Records, plural(r.Records, "capture"))
	vpc := dispVoltsPerCode(s, ch)
	mark := func(bound bool, sign string) string {
		if bound {
			return sign
		}
		return ""
	}
	fig := func(v float64, text string) string {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "---"
		}
		return text
	}
	// The web's eye card shows the same figures: the bit rate to five
	// figures, the UI with its samples, RJ/DJ and the period jitter.
	tb := mark(r.TIEBound, "<")
	p.Figures = [3][4][2]string{
		{{"BIT RATE", fig(r.BitRate, si5(r.BitRate, "bit/s"))},
			{"UI", fig(r.UI, si(r.UI, "s")+fmt.Sprintf(" %.1f samples", r.SamplesPerUI))},
			{"CROSSING", fig(r.Crossing, fmt.Sprintf("%.1f %%", r.Crossing*100))}, {"FOLDED", fmt.Sprintf("%.0f UI", r.UIs)}},
		{{"EYE HEIGHT", fig(r.EyeHeight, si(r.EyeHeight*vpc, "V"))},
			{"EYE WIDTH", fig(r.EyeWidth, fmt.Sprintf("%s%.2f UI", mark(r.WidthBound, ">"), r.EyeWidth))},
			{"RISE", fig(r.Rise, mark(r.RiseBound, "<")+si(r.Rise, "s"))}, {"FALL", fig(r.Fall, mark(r.FallBound, "<")+si(r.Fall, "s"))}},
		{{"TIE RMS", fig(r.TIERms, tb+si(r.TIERms, "s"))}, {"TIE PK-PK", fig(r.TIEPkPk, tb+si(r.TIEPkPk, "s"))},
			{"RJ / DJ", fig(r.RJ, tb+si(r.RJ, "s")+" / "+si(r.DJ, "s"))},
			{"PER / C2C", fig(r.PeriodJ, tb+si(r.PeriodJ, "s")+" / "+si(r.C2CJ, "s"))}},
	}
	ov, oh := "---", "---"
	if !math.IsNaN(cur.Height()) {
		ov = si(cur.Height()*vpc, "V")
	}
	if !math.IsNaN(cur.Width()) {
		oh = fmt.Sprintf("%s%.2f UI", mark(r.WidthBound, ">"), cur.Width())
	}
	z := float64(max(analog.Detents[s.vidx[ch]].Zoom, 1))
	thV := ((th-128)/z)*voltsPerCode(s, ch) - s.offV[ch]*s.probe[ch]
	p.Cursor = fmt.Sprintf("SAMPLE PT %+.2f UI: opening %s     THRESHOLD %s: opening %s", u.eyeX, ov, si(thV, "V"), oh)
	switch {
	case s.st.Running && !at.IsZero() && c.now().Sub(at) > eyeHeldAfter:
		// The eye holds what it folded: no other frame feeds it.
		p.Warn = fmt.Sprintf("Held: no full-rate capture for %.0f s", c.now().Sub(at).Seconds())
		if fix := c.eyeRawFix(s); fix != "" {
			p.Warn += " - " + fix
		}
	case r.SampleLimited:
		fix := "S/DIV clockwise or Zoom"
		if zoomed {
			fix = "S/DIV clockwise (more zoom)"
		}
		p.Warn = fmt.Sprintf("Sample-limited: %.1f samples per UI - %s: sharper eye", r.SamplesPerUI, fix)
	case r.TIEBound:
		p.Note = "< >: bounds - the edges fall between samples (dt " + si(g.sampleS, "s") + ")"
	case r.LastErr != "":
		p.Note = "Last capture not folded: " + eyeWhyText(r.LastErr, ch)
	}
	p.Grid, p.Cols, p.Rows, p.Max = r.Grid, r.Cols, r.Rows, r.GridMax
	if o := r.Open; o.HOK && o.VOK && o.Right > o.Left && o.Hi > o.Lo {
		p.HasBox, p.Box = true, [4]int{eyeColOf(r, o.Left), eyeRowOf(r, o.Hi), eyeColOf(r, o.Right), eyeRowOf(r, o.Lo)}
	}
	p.SampleCol = eyeColOf(r, 1+u.eyeX)
	p.ThreshRow = eyeRowOf(r, th)
	return p
}
