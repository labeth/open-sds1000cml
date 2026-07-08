package panel

import "fmt"

// MenuView is the render snapshot of the on-screen menu (read by the LCD
// renderer on a different goroutine; taken under the controller mutex).
type MenuView struct {
	Open           bool
	Title          string
	Items          []MenuItem // up to 5, aligned F1(top)..F5(bottom)
	Sel            int        // highlighted slot
	ShowC1, ShowC2 bool       // per-channel display enable (DISPLAY menu / CH keys)
	ShowMeas       bool       // on-device MEASURE panel toggle (DISPLAY menu)
	ViewMode       int        // 0 = Y-T, 1 = X-Y, 2 = FFT (DISPLAY menu "View")
	MathMode       int        // 0 = off, 1 = C1+C2, 2 = C1-C2, 3 = C1×C2
	AutosetBusy    bool       // an autoset sweep is running (LCD shows a hint)
	AutosetMsg     string     // banner text while AutosetBusy
	Zoom           int        // horizontal magnification (1 = none)
	ZoomOff        float64    // zoom-window pan offset (fraction of the record)
	Persist        bool       // display persistence (afterglow)
	DecProto       int        // 0=off,1=Auto,2=UART,3=I2C,4=SPI
	DecBaud        int
	DecChA, DecChB int // channel roles (0=C1,1=C2)
	DecCPOL        bool
	DecCPHA        bool
	DecFormat      int // 0=hex,1=ascii,2=both

	CurOn   bool       // cursors visible
	CurType int        // 0 = X (time), 1 = Y (volts)
	CurSel  int        // active cursor (0 = A, 1 = B)
	CurX    [2]float64 // X cursor screen fractions
	CurY    [2]float64 // Y cursor screen fractions
}

// MenuView is the render snapshot; safe to call from the render goroutine.
func (c *Controller) MenuView() MenuView {
	c.mu.Lock()
	pg, sel, c1, c2, meas := c.menuPage, c.menuSel, c.chDisp[0], c.chDisp[1], c.showMeas
	view, mth, busy, amsg := c.viewMode, c.mathMode, c.autosetBusy, c.autosetMsg
	rA, rAs, rB, rBs := c.refs[0].has, c.refs[0].show, c.refs[1].has, c.refs[1].show
	curOn, curType, curSel, curX, curY := c.curOn, c.curType, c.curSel, c.curX, c.curY
	pl, pmn, pmx, pc := c.pulseLvl, c.pulseMin, c.pulseMax, c.pulseCond
	slo, shi, smn, smx, scnd := c.slopeLo, c.slopeHi, c.slopeMin, c.slopeMax, c.slopeCond
	vstd, vln, vneg := c.videoStd, c.videoLine, c.videoNeg
	zoom, zoomOff, persist := c.zoom, c.zoomOff, c.persist
	decProto, decBaud, decChA, decChB := c.decProto, c.decBaud, c.decChA, c.decChB
	decCPOL, decCPHA, decFormat := c.decCPOL, c.decCPHA, c.decFormat
	srMode, srVal, srCh, srK := c.srStopMode, c.srStopVal, c.srCh, c.srK
	maskN, maskTol, maskBusy := c.maskN, c.maskTol, c.maskBuilding
	c.mu.Unlock()
	v := MenuView{Open: pg != pgNone, Sel: sel, ShowC1: c1, ShowC2: c2, ShowMeas: meas,
		ViewMode: view, MathMode: mth, AutosetBusy: busy, AutosetMsg: amsg,
		Zoom: zoom, ZoomOff: zoomOff, Persist: persist,
		DecProto: decProto, DecBaud: decBaud, DecChA: decChA, DecChB: decChB, DecCPOL: decCPOL, DecCPHA: decCPHA, DecFormat: decFormat,
		CurOn: curOn, CurType: curType, CurSel: curSel, CurX: curX, CurY: curY}
	if pg == pgNone {
		return v
	}
	st := c.eng.Snapshot()
	onoff := func(b bool) string {
		if b {
			return "On"
		}
		return "Off"
	}
	switch pg {
	case pgMain:
		v.Title = "MENU"
		v.Items = []MenuItem{
			{"Trigger", ">"},
			{"Acquire", ">"},
			{"Display", ">"},
			{"Horizontal", ">"},
			{"Decode", ">"}, // Cursors has its own dedicated key
		}
	case pgDecode:
		protos := []string{"Off", "Auto", "UART", "I2C", "SPI"}
		fmts := []string{"Hex", "ASCII", "Both"}
		ch := func(c int) string {
			if c == 1 {
				return "C2"
			}
			return "C1"
		}
		v.Title = "DECODE"
		it := []MenuItem{{"Proto", protos[decProto%5]}, {"", ""}, {"", ""}, {"", ""}, {"", ""}}
		show := MenuItem{"Show", fmts[decFormat%3]}
		switch decProto {
		case 1: // Auto — detects protocol/roles/params from the live signal each frame
			it[1] = show
		case 2: // UART
			it[1] = MenuItem{"Baud", fmt.Sprint(decBaud)}
			it[2] = MenuItem{"Source", ch(decChA)}
			it[3] = show
		case 3: // I2C
			it[1] = MenuItem{"SCL", ch(decChA)}
			it[2] = MenuItem{"SDA", ch(decChB)}
			it[3] = show
		case 4: // SPI
			it[1] = MenuItem{"CLK", ch(decChA)}
			it[2] = MenuItem{"DATA", ch(decChB)}
			it[3] = MenuItem{"Mode", fmt.Sprintf("%d", b2ic(decCPOL)<<1|b2ic(decCPHA))}
			it[4] = show
		}
		v.Items = it
	case pgTrigQ:
		cond := []string{"any", "<min", ">max", "in"}
		pct := func(f float64) string { return fmt.Sprintf("%d%%", int(f*100+0.5)) }
		switch st.TrigType {
		case 1: // PULSE
			v.Title = "PULSE"
			v.Items = []MenuItem{
				{"Level", pct(pl)},
				{"W min", fmtEng(pmn*1e-9, "s")},
				{"W max", fmtEng(pmx*1e-9, "s")},
				{"Cond", cond[pc&3]},
				{"", ""},
			}
		case 2: // SLOPE
			v.Title = "SLOPE"
			v.Items = []MenuItem{
				{"Low", pct(slo)},
				{"High", pct(shi)},
				{"T min", fmtEng(smn*1e-9, "s")},
				{"T max", fmtEng(smx*1e-9, "s")},
				{"Cond", cond[scnd&3]},
			}
		case 3: // VIDEO
			std := "PAL"
			if vstd == 1 {
				std = "NTSC"
			}
			pol := "pos"
			if vneg {
				pol = "neg"
			}
			v.Title = "VIDEO"
			v.Items = []MenuItem{
				{"Std", std},
				{"Line", fmt.Sprint(vln)},
				{"Sync", pol},
				{"", ""}, {"", ""},
			}
		default: // EDGE — no qualifier
			v.Title = "EDGE"
			v.Items = []MenuItem{
				{"(no params)", ""},
				{"", ""}, {"", ""}, {"", ""}, {"", ""},
			}
		}
	case pgTrig:
		slope := "Rise" // LCD font is ASCII — no arrow glyphs
		if !st.TrigRising {
			slope = "Fall"
		}
		src := "CH1"
		if st.TrigSource == 1 {
			src = "CH2"
		}
		types := []string{"Edge", "Pulse", "Slope", "Video"}
		ho := "Off"
		if st.HoldoffS > 0 {
			ho = fmtEng(st.HoldoffS, "s")
		}
		v.Title = "TRIGGER"
		v.Items = []MenuItem{
			{"Mode", ternary(st.Norm, "NORM", "AUTO")},
			{"Slope", slope},
			{"Source", src},
			{"Type", types[st.TrigType&3]},
			{"Holdoff", ho},
		}
	case pgAcq:
		modes := []string{"Normal", "Average", "ERes", "Peak"}
		cnt := "-"
		if st.AcqMode == 1 {
			cnt = fmt.Sprint(st.AvgCount)
		} else if st.AcqMode == 2 {
			cnt = fmt.Sprint(st.EresLen)
		}
		v.Title = "ACQUIRE"
		v.Items = []MenuItem{
			{"Acquire", modes[st.AcqMode&3]},
			{"Count", cnt},
			{"ETS", onoff(st.ETS)},
			{"Mem", depthLabel(st.MemDepth)},
			{"", ""},
		}
	case pgDisp:
		v.Title = "DISPLAY"
		v.Items = []MenuItem{
			{"CH1", onoff(c1)},
			{"CH2", onoff(c2)},
			{"Measure", onoff(meas)},
			{"View", []string{"Y-T", "X-Y", "FFT", "Bode", "Spgm"}[view%5]},
			{"Math", []string{"Off", "C1+C2", "C1-C2", "C2-C1", "C1xC2"}[mth%5]}, // ASCII font: no '×'
		}
	case pgHoriz:
		zl := "1x"
		if zoom > 1 {
			zl = fmt.Sprintf("%dx", zoom)
		}
		v.Title = "HORIZ"
		v.Items = []MenuItem{
			{"Time/div", fmtEng(st.DisplayedS, "s")},
			{"Trig Pos", fmt.Sprintf("%d%%", int(c.trigPos()*100+0.5))},
			{"Zoom", zl},
			{"", ""}, {"", ""},
		}
	case pgChan:
		cpl0, cpl1, p0, p1 := "DC", "DC", "1x", "1x"
		if c.fe != nil {
			cpl0, cpl1 = cplName(c.fe.Coupling(0)), cplName(c.fe.Coupling(1))
			p0, p1 = probeName(c.fe.ProbeFactor(0)), probeName(c.fe.ProbeFactor(1))
		}
		v.Title = "CHANNEL"
		v.Items = []MenuItem{
			{"C1 Coupling", cpl0},
			{"C2 Coupling", cpl1},
			{"C1 Probe", p0},
			{"C2 Probe", p1},
			{"Persist", onoff(persist)},
		}
	case pgRef:
		saved := func(has bool) string {
			if has {
				return "saved"
			}
			return "empty"
		}
		show := func(has, s bool) string {
			if !has {
				return "-"
			}
			return onoff(s)
		}
		v.Title = "REF A/B"
		v.Items = []MenuItem{
			{"Save A", saved(rA)},
			{"Save B", saved(rB)},
			{"Show A", show(rA, rAs)},
			{"Show B", show(rB, rBs)},
			{"Clear", ""},
		}
	case pgMask:
		tol := maskTols[maskTol%len(maskTols)]
		build := "-"
		if maskBusy {
			build = "busy"
		} else if st.MaskSet {
			build = "ready"
		}
		v.Title = "MASK"
		v.Items = []MenuItem{
			{"Mode", []string{"Off", "Test", "Stop-F"}[st.MaskMode%3]},
			{"Build", build},
			{"Frames", fmt.Sprint(maskN)},
			{"Tol", fmt.Sprintf("%ds/%dV", tol[0], tol[1])}, // ASCII font: no ±
			{"Reset", fmt.Sprintf("%d/%d", st.MaskPass, st.MaskFail)},
		}
	case pgSuperres:
		modes := []string{"bits", "stacks", "time"}
		tgt := ""
		switch srMode {
		case 0:
			tgt = fmt.Sprintf("+%.1fb", srVal)
		case 1:
			tgt = fmt.Sprintf("%d", int(srVal+0.5))
		case 2:
			tgt = fmtEng(srVal, "s")
		}
		v.Title = "SUPER-RES"
		v.Items = []MenuItem{
			{"Channel", ternary(srCh == 1, "C2", "C1")},
			{"Grid", fmt.Sprintf("x%d", srK)},
			{"Stop on", modes[srMode%3]},
			{"Target", tgt},
			{"Reset", ""},
		}
	case pgCursor:
		typ, sel := "Time", "A"
		if curType == 1 {
			typ = "Volts"
		}
		if curSel == 1 {
			sel = "B"
		}
		v.Title = "CURSOR"
		v.Items = []MenuItem{
			{"Cursors", onoff(curOn)},
			{"Type", typ},
			{"Active", sel},
			{"Move -", "F4"}, // F4 nudges -, F5 nudges + (ADJUST moves continuously)
			{"Move +", "F5"},
		}
	}
	return v
}
