// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"strings"

	"open-sds/app/internal/engine"
)

// Serial is the TRIGGER page's fifth trigger type: the engine's serial
// trigger (publish only frames whose decode matches, centred on the match),
// which only the web could arm before. It decodes with the DECODE page's bus
// settings, so a bus is set up once; the SERIAL page (TRIGGER again) edits
// the match. The armed pattern lives in the engine, so the LCD and the web
// always show and edit the same trigger.
const (
	trigTypes  = 5 // Edge, Pulse, Slope, Video, Serial
	trigSerial = 4
)

// serialEngine is the engine's serial trigger.
type serialEngine interface {
	SetSerialMode(m int)
	SetSerialParams(p engine.SerialParams)
	SerialConfig() engine.SerialParams
}

// serialTrig returns the engine's serial trigger, or nil without one.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) serialTrig() serialEngine {
	e, _ := c.eng.(serialEngine)
	return e
}

// trigTypeIndex is the TRIGGER page's Type: the engine's type, or Serial
// while the serial trigger is armed.
// TRLC-LINKS: REQ-SDS-013
func trigTypeIndex(st engine.Stats) int {
	if st.SerialMode != 0 {
		return trigSerial
	}
	return st.TrigType & 3
}

// setTrigTypeIndex steps the TRIGGER page's Type, arming the serial trigger
// on Serial and disarming it on the hardware types.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) setTrigTypeIndex(st engine.Stats, next int) {
	se := c.serialTrig()
	if next == trigSerial && se == nil {
		next = 0 // no serial trigger in this engine: Serial is skipped
	}
	if next == trigSerial {
		c.mu.Lock()
		if c.decProto < 2 { // Off/Auto has no fixed bus: start from UART
			c.decProto = 2
		}
		c.mu.Unlock()
		c.syncDecodeView(true) // the decode strip shows what the trigger decodes
		c.eng.SetTrigType(0)   // the hardware edge paces captures
		p := se.SerialConfig()
		if len(p.Bytes) == 0 && len(p.Pattern) == 0 { // never armed: 55, any I2C address/direction
			p.Bytes, p.Addr, p.RW = []int{0x55}, -1, 2
		}
		se.SetSerialParams(c.serialBus(p))
		se.SetSerialMode(engine.SerialTrigger)
		return
	}
	if st.SerialMode != 0 && se != nil {
		se.SetSerialMode(engine.SerialOff)
	}
	c.eng.SetTrigType(next)
}

// serialBus copies the DECODE page's bus settings into p, keeping its match.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) serialBus(p engine.SerialParams) engine.SerialParams {
	c.mu.Lock()
	defer c.mu.Unlock()
	p.Proto = c.decProto - 1 // panel 2/3/4 = engine UART/I2C/SPI
	p.ChA, p.ChB = c.decChA, c.decChB
	p.Baud, p.Bits, p.Parity = c.decBaud, 8, "none"
	p.CPOL, p.CPHA, p.MSB = c.decCPOL, c.decCPHA, true
	p.HaveThr = false
	if p.Proto != 2 { // only I2C has an address
		p.Addr, p.RW = -1, 2
	}
	return p
}

// syncSerialBus re-sends the DECODE settings to an armed serial trigger, or
// disarms it when decode no longer names a bus (Off/Auto).
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) syncSerialBus() {
	se := c.serialTrig()
	if se == nil || c.eng.Snapshot().SerialMode == 0 {
		return
	}
	c.mu.Lock()
	proto := c.decProto
	c.mu.Unlock()
	if proto < 2 {
		se.SetSerialMode(engine.SerialOff)
		return
	}
	se.SetSerialParams(c.serialBus(se.SerialConfig()))
}

// serialItems is the SERIAL page (TRIGGER pressed again with Serial armed):
// the protocol (the DECODE page's), the match (F2 opens the editor), Clear,
// and the I2C address.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) serialItems() []MenuItem {
	se := c.serialTrig()
	if se == nil {
		return []MenuItem{{"(no serial)", ""}, {"", ""}, {"", ""}, {"", ""}, {"", ""}}
	}
	p := se.SerialConfig()
	match := strings.Join(tokensOf(p), " ")
	if match == "" {
		match = "any"
	}
	if len(match) > 17 { // the softkey column holds ~17 characters
		match = match[:15] + ".."
	}
	items := []MenuItem{
		{"Proto", [...]string{"UART", "I2C", "SPI"}[(p.Proto+2)%3]},
		{"Match >", match},
		{"Clear", ""},
		{"", ""},
		{"", ""},
	}
	if p.Proto == 2 {
		addr := "any"
		if p.Addr >= 0 {
			addr = fmt.Sprintf("%02X", p.Addr)
		}
		items[3] = MenuItem{"Addr", addr}
	}
	return items
}

// serialCycle edits the SERIAL page: F1 the bus protocol (the DECODE page's,
// shared), F2 opens the match editor, F3 clears the match (any data), F4 the
// I2C address (any, 00..7F).
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) serialCycle(slot, dir int) {
	se := c.serialTrig()
	if se == nil {
		return
	}
	if slot == 1 {
		c.openMatchEditor()
		return
	}
	p := se.SerialConfig()
	switch slot {
	case 0:
		c.mu.Lock()
		c.decProto = 2 + ((c.decProto-2+dir)%3+3)%3 // UART/I2C/SPI
		if c.decProto != 2 {
			c.decChB = 1 - c.decChA // clock and data on different channels
		}
		c.mu.Unlock()
		c.syncDecodeView(true)
	case 2:
		p.Bytes, p.Pattern = nil, nil
	case 3:
		if p.Proto == 2 {
			p.Addr = ((p.Addr+1+dir)%129+129)%129 - 1 // any, 00..7F
		}
	}
	se.SetSerialParams(c.serialBus(p))
}
