// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import "open-sds/app/internal/analog"

// DefaultSetup returns the instrument to its power-on setup (DEFAULT SETUP key,
// SCPI *RST): AUTO and running on a rising CH1 edge at 0 V, 500 us/div with the
// trigger centred, normal acquisition, both channels 1 V/div DC x1 at 0 V,
// and every overlay off (math, cursors, zoom, measurements, persistence,
// X-Y/FFT views, super-res, protocol decode and its stream, reference
// display, mask/zone/serial triggering, ETS). Saved references and setups are
// kept. The workspace UI is reset by the key's handler (defaultSetupUI),
// which keeps the workspace.
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
	c.eng.SetMaskMode(0)
	if e, ok := c.eng.(interface{ SetZoneMode(int) }); ok {
		e.SetZoneMode(0)
	}
	if e, ok := c.eng.(interface{ SetSerialMode(int) }); ok {
		e.SetSerialMode(0)
	}
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
	for i := range c.refs {
		c.refs[i].show = false
	}
	c.mu.Unlock()
	if streaming {
		c.stopStream()
	}
	c.syncDecodeView(false)
	c.pushLEDs()
}
