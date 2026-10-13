// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import "open-sds/app/internal/analog"

// DefaultSetup returns the instrument to its power-on setup (DEFAULT SETUP key,
// SCPI *RST): AUTO and running on a rising CH1 edge at 0 V, 500 us/div with the
// trigger centred, normal acquisition, both channels 1 V/div DC x1 at 0 V at
// full bandwidth and not inverted, and every overlay off (math, cursors, zoom, measurements, persistence,
// X-Y/FFT views, super-res, protocol decode with its bus settings and its
// stream, reference display, mask/zone/serial triggering, ETS). The mask is cleared and the
// serial trigger's match bytes and I2C address with it. Saved references and
// setups are kept. The workspace UI is reset by the key's handler
// (defaultSetupUI), which keeps the workspace.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) DefaultSetup() {
	c.srCancel("")
	c.eng.SetNorm(false)
	c.eng.SetTrigType(0)
	c.eng.SetTrigSlope(true)
	c.eng.SetTrigSource(0)
	c.eng.SetAcqMode(0)
	c.eng.SetHoldoff(0)
	c.eng.SetETS(false)
	c.clearMask()
	if e, ok := c.eng.(interface{ SetZoneMode(int) }); ok {
		e.SetZoneMode(0)
	}
	if e, ok := c.eng.(interface{ SetSerialMode(int) }); ok {
		e.SetSerialMode(0)
	}
	if se := c.serialTrig(); se != nil { // no match bytes, any I2C address and direction
		p := se.SerialConfig()
		p.Bytes, p.Pattern, p.Addr, p.RW = nil, nil, -1, 2
		se.SetSerialParams(p)
	}
	c.resetQualifiers()
	c.eng.SetTrigPosFrac(0.5)
	for i, t := range c.tdivs {
		if t == 500e-6 {
			c.tdivIdx = i
			c.eng.SetTdiv(t)
		}
	}
	if c.fe != nil {
		oneV := analog.BootDetent // 1 V/div
		for ch := 0; ch < 2; ch++ {
			c.vIdx[ch] = oneV
			if err := c.fe.SetVdiv(ch, oneV); err != nil {
				c.logf("panel: default setup V/div: %v", err)
			}
			if err := c.fe.SetCoupling(ch, analog.CplDC); err != nil {
				c.logf("panel: default setup coupling: %v", err)
			}
			c.fe.SetProbe(ch, 1)
			c.fe.SetOffset(ch, 0)
			if b, ok := c.fe.(interface{ SetBWL(int, bool) error }); ok { // full bandwidth (it stayed limited)
				if err := b.SetBWL(ch, false); err != nil {
					c.logf("panel: default setup BW limit: %v", err)
				}
			}
		}
	}
	if c.inverter != nil { // the traces the right way up
		for ch := 0; ch < 2; ch++ {
			c.inverter.SetInverted(ch, false)
		}
		c.eng.SetTrigLevelCode(uint16(c.fe.TrigCode(0, 0)))
	}
	c.eng.SetRunning(true)
	c.mu.Lock()
	c.chDisp = [2]bool{true, true}
	c.showMeas, c.viewMode, c.mathMode, c.persist = false, 0, 0, false
	c.curOn, c.curType, c.curSel = false, 0, 0
	c.curX, c.curY = [2]float64{0.35, 0.65}, [2]float64{0.35, 0.65}
	c.zoom, c.zoomOff = 1, 0
	streaming := c.decMode == decModeStream
	c.decProto, c.decMode = 0, decModeView
	c.decGen++
	// the bus settings too: a bus found later started with the last one's
	// bit rate (bug hunt #3: SENT showed the Manchester rate)
	c.decBaud, c.decChA, c.decChB, c.decCPOL, c.decCPHA, c.decFormat = 115200, 0, 1, false, false, 0
	for i := range c.refs {
		c.refs[i].show = false
	}
	c.mu.Unlock()
	if streaming {
		c.leaveStream() // RUN again once the stream's end has stopped the scope
	}
	c.syncDecodeView(false)
	c.pushLEDs()
}

// powerOnLimitNs is the pulse-width and slope-time limits at power-on.
var powerOnLimitNs = [2]float64{100, 1000}

// resetQualifiers puts the pulse, slope and video settings back to their
// power-on values, on the panel and in the engine: DEFAULT SETUP left a
// pulse width of "> 13 us" from before (bug hunt #3).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) resetQualifiers() {
	c.mu.Lock()
	c.pulseLvl, c.pulseMin, c.pulseMax, c.pulseCond = 0.5, powerOnLimitNs[0], powerOnLimitNs[1], 0
	c.slopeLo, c.slopeHi, c.slopeMin, c.slopeMax, c.slopeCond = 0.2, 0.8, powerOnLimitNs[0], powerOnLimitNs[1], 0
	c.videoStd, c.videoLine, c.videoNeg = 0, 0, true
	c.mu.Unlock()
	c.eng.SetPulseParams(0.5, powerOnLimitNs[0], powerOnLimitNs[1], 0)
	c.eng.SetSlopeParams(0.2, 0.8, powerOnLimitNs[0], powerOnLimitNs[1], 0)
	c.eng.SetVideoParams(0, 0, true)
}
