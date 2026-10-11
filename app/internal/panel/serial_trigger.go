// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
)

// Serial is the TRIG tab's fifth trigger type: the engine's serial trigger
// (publish only frames whose decode matches, centred on the match). It
// decodes with the BUS tab's bus settings, so a bus is set up once; the BUS
// tab's Match bytes row and the BUS workspace's byte picker set the match.
// The armed pattern lives in the engine, so the LCD and the web always show
// and edit the same trigger.
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

// trigTypeIndex is the TRIG tab's Type: the engine's type, or Serial while
// the serial trigger is armed.
// TRLC-LINKS: REQ-SDS-013
func trigTypeIndex(st engine.Stats) int {
	if st.SerialMode != 0 {
		return trigSerial
	}
	return st.TrigType & 3
}

// setTrigTypeIndex sets the Type, arming the serial trigger on Serial (the
// caller has checked the bus: armSerial) and disarming it on the hardware
// types.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) setTrigTypeIndex(st engine.Stats, next int) {
	se := c.serialTrig()
	if next == trigSerial && se == nil {
		next = 0 // no serial trigger in this engine: Serial is skipped
	}
	if next == trigSerial {
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

// serialBus copies the BUS tab's bus settings into p, keeping its match.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) serialBus(p engine.SerialParams) engine.SerialParams {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.decProto >= framecalc.ProtoUART && c.decProto <= framecalc.ProtoSPI {
		p.Proto = c.decProto - 1 // panel 2/3/4 = engine UART/I2C/SPI
	}
	p.ChA, p.ChB = c.decChA, c.decChB
	p.Baud, p.Bits, p.Parity = c.decBaud, 8, "none"
	p.CPOL, p.CPHA, p.MSB = c.decCPOL, c.decCPHA, true
	p.HaveThr = false
	if p.Proto != 2 { // only I2C has an address
		p.Addr, p.RW = -1, 2
	}
	return p
}

// syncSerialBus re-sends the BUS settings to an armed serial trigger, or
// disarms it when decode no longer names a bus it can match (Off, Auto, or
// a protocol other than UART, I2C and SPI).
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) syncSerialBus() {
	se := c.serialTrig()
	if se == nil || c.eng.Snapshot().SerialMode == 0 {
		return
	}
	c.mu.Lock()
	proto := c.decProto
	c.mu.Unlock()
	if proto < framecalc.ProtoUART || proto > framecalc.ProtoSPI {
		se.SetSerialMode(engine.SerialOff)
		return
	}
	se.SetSerialParams(c.serialBus(se.SerialConfig()))
}

// setI2CAddr sets the serial trigger's I2C address (-1 = any).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-212
func (c *Controller) setI2CAddr(a int) error {
	se := c.serialTrig()
	if se == nil {
		return fmt.Errorf("no serial trigger in this engine")
	}
	p := se.SerialConfig()
	p.Addr = clampInt(a, -1, 127)
	se.SetSerialParams(c.serialBus(p)) // a bus other than I2C keeps "any"
	return nil
}
