// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// MEASURE (REQ-SDS-217): running statistics of the chosen measurements (now,
// mean, min, max, standard deviation, N) and the cursors on two knobs:
// Adjust moves cursor A, Position cursor B, the Adjust push cycles Off /
// Time / Volts and the Position push zeroes the statistics. MEASURE again
// shows all values of both channels. Entering it with nothing chosen
// measures what the bottom bar shows: Vpp and Freq of each shown channel.

// setMeasParam sets a parameter's channels (bit 0 = C1, bit 1 = C2). The
// readouts hold at most maxMeasItems; a new item past that drops the oldest.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-217
func (c *Controller) setMeasParam(p, chans int) {
	c.mu.Lock()
	var keep []measItem
	for _, it := range c.meas {
		if it.param != p {
			keep = append(keep, it)
		}
	}
	dropped := false
	for ch := 0; ch < 2; ch++ {
		if chans&(1<<ch) != 0 {
			keep = append(keep, measItem{ch: ch, param: p})
		}
	}
	if len(keep) > maxMeasItems {
		keep, dropped = keep[len(keep)-maxMeasItems:], true
	}
	c.meas = keep
	if chans != 0 {
		c.showMeas = true
	}
	if dropped {
		c.toastLocked(fmt.Sprintf("%d readouts at most: the oldest was removed", maxMeasItems), 1800*time.Millisecond)
	}
	c.mu.Unlock()
	c.pushLEDs()
}

// clearMeas removes every chosen measurement.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-217
func (c *Controller) clearMeas() {
	c.mu.Lock()
	c.meas = nil
	c.mu.Unlock()
	c.pushLEDs()
}

// seedMeas is MEASURE with nothing chosen: Vpp and Freq of each shown
// channel, readouts on, and a notice saying so.
// TRLC-LINKS: REQ-SDS-217
func (c *Controller) seedMeas() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.meas) > 0 {
		return
	}
	for ch := 0; ch < 2; ch++ {
		if c.chDisp[ch] {
			c.meas = append(c.meas, measItem{ch: ch, param: mPkpk}, measItem{ch: ch, param: mFreq})
		}
	}
	if len(c.meas) == 0 {
		return
	}
	c.showMeas = true
	c.toastLocked("Measuring Vpp and Freq - the Measure key picks others", 1800*time.Millisecond)
}

// statKey is what the statistics depend on: a change starts them again.
func statKey(s *sv) string {
	return fmt.Sprint(s.vidx, s.probe, s.cpl, s.offV, s.st.TdivS, s.st.TrigSource, s.st.TrigRising, s.st.TrigType,
		s.st.TrigCode, s.st.AcqMode, s.st.AvgCount, s.st.EresLen, s.meas, s.inv)
}

// statsResetLocked zeroes the statistics (c.amu held).
func (c *Controller) statsResetLocked() {
	c.ana.stats, c.ana.statLast, c.ana.statN = nil, nil, 0
}

// statsReset is the Position push in MEASURE: zero the statistics.
// TRLC-LINKS: REQ-SDS-217
func (c *Controller) statsReset() {
	c.amu.Lock()
	c.statsResetLocked()
	c.amu.Unlock()
	c.mu.Lock()
	c.ui.statT = c.now()
	c.toastLocked("Statistics zeroed", 900*time.Millisecond)
	c.mu.Unlock()
}

// measTabOpen reports the MEAS tab showing (its rows show live values).
// TRLC-LINKS: REQ-SDS-217
func measTabOpen(s *sv) bool { return s.ui.open && sheetTabs[s.ui.tab].id == "meas" }

// measVals measures the channels in need: every parameter's value.
// TRLC-LINKS: REQ-SDS-217
func measVals(a *anaState, s *sv, f *engine.Frame, need [2]bool) {
	for ch := 0; ch < 2; ch++ {
		a.allOK[ch] = false
		if !need[ch] {
			continue
		}
		m := measureCh(f, s, ch)
		if m == nil {
			continue
		}
		vals := make([]float64, len(measParams))
		for p := range measParams {
			vals[p] = measValue(m, p)
		}
		a.allVals[ch], a.allOK[ch] = vals, true
	}
}

// measFrame is MEASURE's analysis: the cursors' trace volts, the
// measurements and their statistics.
// TRLC-LINKS: REQ-SDS-217
func (c *Controller) measFrame(a *anaState, s *sv, f *engine.Frame) {
	if f.IsEnv {
		return
	}
	if s.curOn && s.curType == 0 {
		a.curV, a.curOK = cursorTraceVolts(f, s)
	}
	if k := statKey(s); k != a.statKey {
		a.statKey, a.statFed = k, frameKey{}
		c.statsResetLocked()
		c.mu.Lock()
		c.ui.statT = c.now()
		c.mu.Unlock()
	}
	var need [2]bool
	for _, it := range s.meas {
		need[it.ch] = true
	}
	if s.ui.measAll || measTabOpen(s) {
		need = [2]bool{s.chDisp[0], s.chDisp[1]}
	}
	measVals(a, s, f, need)
	if seen(&a.statFed, keyOf(f)) {
		return // counted already
	}
	if a.stats == nil {
		a.stats, a.statLast = map[measItem]*Stats{}, map[measItem]float64{}
	}
	any := false
	for _, it := range s.meas {
		if !a.allOK[it.ch] {
			continue
		}
		v := a.allVals[it.ch][it.param]
		a.statLast[it] = v
		st := a.stats[it]
		if st == nil {
			st = &Stats{}
			a.stats[it] = st
		}
		if !math.IsNaN(v) {
			st.Add(v)
			any = true
		}
	}
	if any {
		a.statN++
	}
}

// ---- cursors ----

// setCursorMode sets Off (0), Time (1) or Volts (2); SCOPE's Adjust then
// moves the active cursor.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-217
func (c *Controller) setCursorMode(v int) {
	c.mu.Lock()
	c.curOn = v != 0
	if v != 0 {
		c.curType = v - 1
		c.ui.adj = "curs." + string(rune('a'+c.curSel))
	}
	c.mu.Unlock()
	c.pushLEDs()
}

// setCursorDiv places cursor k at d divisions (from the left, or from the
// bottom for volts).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-217
func (c *Controller) setCursorDiv(k int, d float64) {
	c.mu.Lock()
	if c.curType == 1 {
		c.curY[k&1] = clampF(1-d/8, 0, 1)
	} else {
		c.curX[k&1] = clampF(d/10, 0, 1)
	}
	c.curSel = k & 1
	c.mu.Unlock()
}

// centreCursor puts cursor k at the screen centre.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) centreCursor(k int) {
	c.mu.Lock()
	if c.curType == 1 {
		c.curY[k&1] = 0.5
	} else {
		c.curX[k&1] = 0.5
	}
	c.mu.Unlock()
}

// cursorAtTrigger puts cursor k on the trigger (time) or its level (volts).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) cursorAtTrigger(k int) {
	s := c.snap()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.curType == 1 {
		src := s.st.TrigSource & 1
		div := (s.trigV + s.offV[src]*s.probe[src]) / s.vdivTip(src)
		c.curY[k&1] = clampF(0.5-div/8, 0, 1)
		return
	}
	pos := s.st.TrigPosFrac
	if !(pos > 0 && pos <= 1) {
		pos = 0.5
	}
	c.curX[k&1] = pos
}

// measCursor moves cursor k by n knob steps (MEASURE: Adjust A, Position B).
// TRLC-LINKS: REQ-SDS-217, REQ-SDS-213
func (c *Controller) measCursor(k, dir, n int) {
	c.mu.Lock()
	if !c.curOn {
		c.toastLocked("Cursors are off: push Adjust for time cursors", 1500*time.Millisecond)
		c.mu.Unlock()
		return
	}
	c.curSel = k & 1
	step := 0.01 * float64(dir*max(n, 1))
	if c.curType == 1 {
		c.curY[k&1] = clampF(c.curY[k&1]-step, 0, 1) // clockwise raises the cursor
	} else {
		c.curX[k&1] = clampF(c.curX[k&1]+step, 0, 1)
	}
	c.mu.Unlock()
}

// measCursorMode is the Adjust push in MEASURE: cursors Off, Time, Volts.
// TRLC-LINKS: REQ-SDS-217
func (c *Controller) measCursorMode() {
	s := c.snap()
	next := map[int]int{0: 1, 1: 2, 2: 0}[map[bool]int{false: 0, true: 1 + s.curType}[s.curOn]]
	c.setCursorMode(next)
	c.toast("Cursors "+[]string{"off", "Time (Adjust: A, Position: B)", "Volts (Adjust: A, Position: B)"}[next], 1400*time.Millisecond)
}

// cursorTimes are the time cursors' positions from the trigger (seconds).
func cursorTimes(s *sv) (ta, tb float64) {
	pos := s.st.TrigPosFrac
	if !(pos > 0 && pos <= 1) {
		pos = 0.5
	}
	span := 10 * s.st.TdivS
	if s.zoom > 1 {
		span /= float64(s.zoom)
	}
	return (s.curX[0] - pos) * span, (s.curX[1] - pos) * span
}

// cursorVolts are a channel's volts at the voltage cursors.
func cursorVolts(s *sv, ch int) (va, vb float64) {
	vd := s.vdivTip(ch)
	off := s.offV[ch] * s.probe[ch]
	at := func(y float64) float64 { return (4-y*8)*vd - off }
	return at(s.curY[0]), at(s.curY[1])
}

// cursorLines is the MEASURE pane's cursor block.
// TRLC-LINKS: REQ-SDS-217
func cursorLines(s *sv, liveV [2][2]float64, liveOK [2]bool) []wsview.Span {
	switch {
	case !s.curOn:
		return []wsview.Span{{Text: "CURSORS OFF", Col: wsview.Dim}, {Text: "Push Adjust: time cursors", Col: wsview.Dim},
			{Text: "then Adjust moves A,", Col: wsview.Dim}, {Text: "Position moves B.", Col: wsview.Dim}}
	case s.curType == 0:
		ta, tb := cursorTimes(s)
		dt := math.Abs(tb - ta)
		out := []wsview.Span{{Text: "CURSORS TIME", Col: wsview.Amber},
			{Text: "A " + sgnSI(ta, "s") + "  B " + sgnSI(tb, "s"), Col: wsview.Text}}
		inv := "---"
		if dt > 0 {
			inv = si(1/dt, "Hz")
		}
		out = append(out, wsview.Span{Text: "dt " + si(dt, "s") + "  1/dt " + inv, Col: wsview.Amber})
		for ch := 0; ch < 2; ch++ {
			if s.chDisp[ch] && liveOK[ch] {
				out = append(out, wsview.Span{Text: fmt.Sprintf("C%d A %s  B %s", ch+1, si(liveV[ch][0], "V"), si(liveV[ch][1], "V")),
					Col: wsview.C1 + wsview.Col(ch)})
			}
		}
		return out
	}
	out := []wsview.Span{{Text: "CURSORS VOLTS", Col: wsview.Amber}}
	for ch := 0; ch < 2; ch++ {
		if !s.chDisp[ch] {
			continue
		}
		va, vb := cursorVolts(s, ch)
		out = append(out, wsview.Span{Text: fmt.Sprintf("C%d A %s  B %s", ch+1, si(va, "V"), si(vb, "V")), Col: wsview.C1 + wsview.Col(ch)},
			wsview.Span{Text: "   dV " + si(math.Abs(vb-va), "V"), Col: wsview.Amber})
	}
	return out
}

// cursorTraceVolts reads each channel's trace at the time cursors.
// TRLC-LINKS: REQ-SDS-217
func cursorTraceVolts(f *engine.Frame, s *sv) (v [2][2]float64, ok [2]bool) {
	if f == nil || f.IsEnv || len(f.C1) == 0 {
		return
	}
	g := geom{valid: framecalc.Valid(f), winCols: f.WinCols, edgeX: f.EdgeX, sampleS: f.SampleS}
	left, win := windowOf(g, s)
	for ch := 0; ch < 2; ch++ {
		if ch == 1 && len(f.C2) < g.valid {
			continue
		}
		sig := framecalc.Channel(f, ch, g.valid)
		for k := 0; k < 2; k++ {
			i := clampInt(int(left+s.curX[k]*float64(win)), 0, g.valid-1)
			v[ch][k] = (float64(sig[i])-128)*voltsPerCode(s, ch) - s.offV[ch]*s.probe[ch]
		}
		ok[ch] = true
	}
	return
}

// measPane builds the MEASURE pane.
// TRLC-LINKS: REQ-SDS-217
func (c *Controller) measPane(s *sv) *wsview.MeasPane {
	u := &s.ui
	p := &wsview.MeasPane{All: u.measAll}
	p.Title = "MEASURE"
	p.Right = "Measure key: choose"
	c.amu.Lock()
	a := &c.ana
	n := a.statN
	type rowv struct {
		it                    measItem
		now, mean, mn, mx, sd float64
		cnt                   int
	}
	var rows []rowv
	for _, it := range s.meas {
		r := rowv{it: it, now: math.NaN(), mean: math.NaN(), mn: math.NaN(), mx: math.NaN(), sd: math.NaN()}
		if v, ok := a.statLast[it]; ok {
			r.now = v
		}
		if st := a.stats[it]; st != nil && st.N() > 0 {
			r.mean, r.mn, r.mx, r.cnt = st.Mean(), st.Min(), st.Max(), st.N()
			if st.N() > 1 {
				r.sd = st.Sdev()
			}
		}
		rows = append(rows, r)
	}
	all, allOK := a.allVals, a.allOK
	liveV, liveOK := a.curV, a.curOK
	c.amu.Unlock()
	if u.measAll {
		p.Caption = "all values, both channels (live)"
		for i := range measParams {
			if i >= len(p.AllRows) {
				break
			}
			p.AllRows[i][0] = measParams[i].short
			for ch := 0; ch < 2; ch++ {
				p.AllRows[i][1+ch] = "---"
				if s.chDisp[ch] && allOK[ch] {
					p.AllRows[i][1+ch] = fmtMeas(i, all[ch][i])
				}
			}
		}
		p.AllN = min(len(measParams), len(p.AllRows))
	} else {
		p.Caption = fmt.Sprintf("statistics  N %d  over %.1f s", n, s.now.Sub(u.statT).Seconds())
		p.Empty = len(rows) == 0
		for i, r := range rows {
			if i >= 8 {
				p.More = fmt.Sprintf("+%d more: MEAS sheet", len(rows)-8)
				break
			}
			sdev := "---"
			if !math.IsNaN(r.sd) {
				sdev = fmtMeas(r.it.param, r.sd)
			}
			p.Stats = append(p.Stats, wsview.StatRow{Name: fmt.Sprintf("C%d %s", r.it.ch+1, measParams[r.it.param].short),
				Col: wsview.C1 + wsview.Col(r.it.ch), Now: fmtMeas(r.it.param, r.now), Mean: fmtMeas(r.it.param, r.mean),
				Min: fmtMeas(r.it.param, r.mn), Max: fmtMeas(r.it.param, r.mx), Sdev: sdev, N: fmt.Sprint(r.cnt)})
		}
	}
	p.Cursor = cursorLines(s, liveV, liveOK)
	if s.st.MaskMode != 0 && s.st.MaskSet {
		col := wsview.Mask
		if s.st.MaskFail > 0 {
			col = wsview.Stop
		}
		p.MaskLine = wsview.Span{Text: fmt.Sprintf("MASK pass %d fail %d", s.st.MaskPass, s.st.MaskFail), Col: col}
	}
	next := []string{"Time", "Volts", "off"}[map[bool]int{false: 0, true: 1 + s.curType}[s.curOn]]
	p.Tool = []wsview.Seg{{Key: "Adjust", Job: "cursor A"}, {Key: "push", Job: "cursors " + next}, {Key: "Position", Job: "cursor B"}, {Key: "push", Job: "zero statistics"}}
	p.Again = "F4 again: " + map[bool]string{true: "Statistics", false: "All values"}[u.measAll]
	return p
}
