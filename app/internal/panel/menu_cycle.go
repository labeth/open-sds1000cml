package panel

func (c *Controller) menuCycle(slot, dir int) {
	c.mu.Lock()
	pg := c.menuPage
	if slot >= c.pageSlots(pg) { // empty slot: don't move the highlight or act
		c.mu.Unlock()
		return
	}
	c.menuSel = slot
	c.mu.Unlock()
	st := c.eng.Snapshot()
	switch pg {
	case pgMain: // MAIN menu: each softkey navigates to a sub-page
		switch slot {
		case 0:
			c.openMenu(pgTrig)
		case 1:
			c.openMenu(pgAcq)
		case 2:
			c.openMenu(pgDisp)
		case 3:
			c.openMenu(pgHoriz)
		case 4:
			c.openMenu(pgDecode) // Cursors has its own key, so Decode gets this slot
		}
		return
	case pgSuperres:
		switch slot {
		case 0: // Channel C1/C2 — rebuild the stack aligned on the chosen channel
			c.mu.Lock()
			c.srCh = 1 - c.srCh
			c.mu.Unlock()
			c.srRearm()
		case 1: // Grid ×K — finer/coarser fine grid; the stack size changes → rebuild
			c.mu.Lock()
			c.srK = nextOpt([]int{8, 16, 32, 64}, c.srK, dir)
			c.mu.Unlock()
			c.srRearm()
		case 2: // Stop-on: bit → stacks → time (menu order); seed a sensible target
			c.mu.Lock()
			c.srStopMode = ((c.srStopMode+dir)%3 + 3) % 3
			c.srStopVal = []float64{4, 500, 60}[c.srStopMode]
			c.mu.Unlock()
		case 3: // Target for the active stop mode
			c.mu.Lock()
			switch c.srStopMode {
			case 0: // bits
				c.srStopVal = clampF(c.srStopVal+float64(dir)*0.5, 0.5, 8)
			case 1: // stacks
				c.srStopVal = clampF(c.srStopVal+float64(dir)*50, 50, 100000)
			case 2: // seconds
				c.srStopVal = clampF(c.srStopVal+float64(dir)*10, 5, 3600)
			}
			c.mu.Unlock()
		case 4: // Reset — clear the accumulation, keep the locked reference
			c.srReqReset()
		}
		return
	case pgMask:
		switch slot {
		case 0: // Mode Off/Test/Stop-F (engine resets counters on off->on)
			c.eng.SetMaskMode(((st.MaskMode+dir)%3 + 3) % 3)
		case 1: // Build a golden mask from N live frames (trigger-source channel)
			c.maskBuildStart()
		case 2: // Frames per build
			c.mu.Lock()
			c.maskN = nextOpt([]int{16, 32, 64, 128}, c.maskN, dir)
			c.mu.Unlock()
		case 3: // Tolerance preset (±samples / ±codes, plan §1.4 floor rule)
			c.mu.Lock()
			c.maskTol = ((c.maskTol+dir)%len(maskTols) + len(maskTols)) % len(maskTols)
			c.mu.Unlock()
		case 4: // Reset counters + failure ring
			c.eng.ClearMaskFails()
			c.maskSetMsg("")
		}
		return
	case pgDecode:
		c.mu.Lock()
		fmtCycle := func() { c.decFormat = ((c.decFormat+dir)%3 + 3) % 3 } // Hex/ASCII/Both
		switch slot {
		case 0:
			c.decProto = ((c.decProto+dir)%5 + 5) % 5 // Off/Auto/UART/I2C/SPI (Auto first — most used)
		case 1:
			switch c.decProto {
			case 1: // Auto: slot 1 is the display format
				fmtCycle()
			case 2: // UART baud
				c.decBaud = nextOpt([]int{9600, 19200, 38400, 57600, 115200, 230400}, c.decBaud, dir)
			case 3, 4: // I2C SCL / SPI CLK channel — keep data on the OTHER channel
				c.decChA = 1 - c.decChA
				c.decChB = 1 - c.decChA
			}
		case 2:
			switch c.decProto {
			case 2: // UART source channel
				c.decChA = 1 - c.decChA
			case 3, 4: // I2C SDA / SPI DATA channel — keep clock on the OTHER channel
				c.decChB = 1 - c.decChB
				c.decChA = 1 - c.decChB
			}
		case 3:
			switch c.decProto {
			case 2, 3: // UART / I2C: slot 3 is the display format
				fmtCycle()
			case 4: // SPI mode: cycle CPOL/CPHA (0..3)
				m := (b2ic(c.decCPOL)<<1 | b2ic(c.decCPHA)) + dir
				m = ((m % 4) + 4) % 4
				c.decCPOL, c.decCPHA = m&2 != 0, m&1 != 0
			}
		case 4:
			if c.decProto == 4 { // SPI: slot 4 is the display format
				fmtCycle()
			}
		}
		c.mu.Unlock()
		return
	case pgTrigQ: // trigger-qualifier params for the current trigger TYPE
		c.mu.Lock()
		switch st.TrigType {
		case 1: // PULSE: Level% / Wmin / Wmax / Cond
			switch slot {
			case 0:
				c.pulseLvl = clampF(c.pulseLvl+float64(dir)*0.05, 0.05, 0.95)
			case 1:
				c.pulseMin = stepNs(c.pulseMin, dir)
			case 2:
				c.pulseMax = stepNs(c.pulseMax, dir)
			case 3:
				c.pulseCond = mod4(c.pulseCond + dir)
			}
			pl, pmn, pmx, pc := c.pulseLvl, c.pulseMin, c.pulseMax, c.pulseCond
			c.mu.Unlock()
			c.eng.SetPulseParams(pl, pmn, pmx, pc)
		case 2: // SLOPE: Lo% / Hi% / Tmin / Tmax / Cond
			switch slot {
			case 0:
				c.slopeLo = clampF(c.slopeLo+float64(dir)*0.05, 0.05, 0.95)
			case 1:
				c.slopeHi = clampF(c.slopeHi+float64(dir)*0.05, 0.05, 0.95)
			case 2:
				c.slopeMin = stepNs(c.slopeMin, dir)
			case 3:
				c.slopeMax = stepNs(c.slopeMax, dir)
			case 4:
				c.slopeCond = mod4(c.slopeCond + dir)
			}
			lo, hi, tmn, tmx, sc := c.slopeLo, c.slopeHi, c.slopeMin, c.slopeMax, c.slopeCond
			c.mu.Unlock()
			c.eng.SetSlopeParams(lo, hi, tmn, tmx, sc)
		case 3: // VIDEO: Std / Line / Polarity
			switch slot {
			case 0:
				c.videoStd = 1 - c.videoStd
			case 1:
				c.videoLine = clampInt(c.videoLine+dir, 0, 625)
			case 2:
				c.videoNeg = !c.videoNeg
			}
			std, ln, neg := c.videoStd, c.videoLine, c.videoNeg
			c.mu.Unlock()
			c.eng.SetVideoParams(std, ln, neg)
		default:
			c.mu.Unlock()
		}
		c.pushLEDs()
		return
	case pgTrig:
		switch slot {
		case 0:
			c.eng.SetNorm(!st.Norm)
			c.mu.Lock()
			c.norm = !st.Norm // guarded — pushLEDs reads it
			c.mu.Unlock()
		case 1:
			c.eng.SetTrigSlope(!st.TrigRising)
		case 2:
			c.eng.SetTrigSource(1 - st.TrigSource)
		case 3:
			c.eng.SetTrigType(mod4(st.TrigType + dir))
		case 4:
			c.eng.SetHoldoff(nextHoldoff(st.HoldoffS, dir))
		}
	case pgAcq:
		switch slot {
		case 0:
			c.eng.SetAcqMode(mod4(st.AcqMode + dir))
		case 1:
			c.menuCount(st, dir)
		case 2:
			c.eng.SetETS(!st.ETS)
		case 3: // memory depth (fps <-> data knob)
			c.eng.SetMemDepth(nextOpt([]int{2048, 6144, 14336, 20480}, st.MemDepth, dir))
		}
	case pgDisp:
		switch slot {
		case 0:
			c.mu.Lock()
			c.chDisp[0] = !c.chDisp[0]
			c.mu.Unlock()
		case 1:
			c.mu.Lock()
			c.chDisp[1] = !c.chDisp[1]
			c.mu.Unlock()
		case 2:
			c.mu.Lock()
			c.showMeas = !c.showMeas
			c.mu.Unlock()
		case 3: // View: Y-T → X-Y → FFT → Bode → Spectrogram
			c.mu.Lock()
			c.viewMode = mod5b(c.viewMode + dir)
			c.mu.Unlock()
		case 4: // Math: off -> C1+C2 -> C1-C2 -> C2-C1 -> C1xC2
			c.mu.Lock()
			c.mathMode = mod5(c.mathMode + dir)
			c.mu.Unlock()
		}
	case pgHoriz:
		switch slot {
		case 0: // Time/div: step the ladder
			c.resync()
			c.tdivIdx = clampInt(c.tdivIdx+dir, 0, len(c.tdivs)-1)
			c.eng.SetTdiv(c.tdivs[c.tdivIdx])
		case 1: // Trigger position: step the horizontal fraction
			c.eng.SetTrigPosFrac(clampF(c.trigPos()+float64(dir)*0.05, 0.02, 1))
		case 2: // Horizontal zoom (magnify the displayed window)
			c.mu.Lock()
			c.zoom = nextOpt([]int{1, 2, 5, 10, 20, 50}, c.zoom, dir)
			if c.zoom <= 1 {
				c.zoomOff = 0 // reset pan when back to 1x
			}
			c.mu.Unlock()
		}
	case pgChan:
		if c.fe != nil {
			switch slot {
			case 0: // CH1 coupling: cycle DC → AC → GND
				_ = c.fe.SetCoupling(0, mod3(c.fe.Coupling(0)+dir))
			case 1: // CH2 coupling
				_ = c.fe.SetCoupling(1, mod3(c.fe.Coupling(1)+dir))
			case 2: // CH1 probe: cycle ×1 → ×10 → ×100
				c.fe.SetProbe(0, nextProbe(c.fe.ProbeFactor(0), dir))
			case 3: // CH2 probe
				c.fe.SetProbe(1, nextProbe(c.fe.ProbeFactor(1), dir))
			}
		}
		if slot == 4 { // Persistence toggle (works even without a front end)
			c.mu.Lock()
			c.persist = !c.persist
			c.mu.Unlock()
		}
	case pgRef:
		switch slot {
		case 0: // Save A: snapshot the current frame
			c.captureRef(0)
		case 1: // Save B
			c.captureRef(1)
		case 2: // Show/hide A
			c.mu.Lock()
			if c.refs[0].has {
				c.refs[0].show = !c.refs[0].show
			}
			c.mu.Unlock()
		case 3: // Show/hide B
			c.mu.Lock()
			if c.refs[1].has {
				c.refs[1].show = !c.refs[1].show
			}
			c.mu.Unlock()
		case 4: // Clear both
			c.mu.Lock()
			c.refs = [2]refWave{}
			c.mu.Unlock()
		}
	case pgCursor:
		switch slot {
		case 0:
			c.mu.Lock()
			c.curOn = !c.curOn
			c.mu.Unlock()
		case 1:
			c.mu.Lock()
			c.curType = 1 - c.curType
			c.mu.Unlock()
		case 2:
			c.mu.Lock()
			c.curSel = 1 - c.curSel
			c.mu.Unlock()
		case 3: // F4 nudges the active cursor − (ADJUST moves it continuously)
			c.moveCursor(-1)
		case 4: // F5 nudges +
			c.moveCursor(+1)
		}
	}
	c.pushLEDs()
}
