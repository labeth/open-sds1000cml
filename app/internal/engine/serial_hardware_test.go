// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"testing"
)

// TRLC-LINKS: REQ-SDS-013
func TestHardwareI2CPlanning(t *testing.T) {
	p := SerialParams{Proto: serI2C, ChA: 1, ChB: 0, HaveThr: true, Threshold: 103, Inverted: true, Addr: 0x24, RW: 0, Bytes: []int{0x55, 0xaa}}
	c := planHardwareI2C(p)
	if !c.Enabled || c.ClockChannel != 1 || c.Address != 0x24 || c.Pattern != 0x55aa || c.Length != 2 || !c.Inverted {
		t.Fatalf("bad plan: %+v", c)
	}
	for _, change := range []func(*SerialParams){
		func(p *SerialParams) { p.ChB = p.ChA }, func(p *SerialParams) { p.HaveThr = false },
		func(p *SerialParams) { p.Addr = 128 }, func(p *SerialParams) { p.RW = 3 },
		func(p *SerialParams) { p.Bytes = []int{1, 2, 3, 4, 5} },
	} {
		q := p
		change(&q)
		if planHardwareI2C(q).Enabled {
			t.Fatalf("unsupported plan: %+v", q)
		}
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestHardwareUARTUsesSelectedRawChannelHysteresis(t *testing.T) {
	e := &Engine{hardwareUART: true, band: Band{TdivS: 500e-6}}
	e.interleaveCalibration = &InterleaveCalibration{Vdiv: [2]float64{1, 1}, Offset: [2][5]float64{{1, -1, 0, 0, 0}, {8, -2, -2, -2, -2}}}
	e.chVdivBits[0].Store(math.Float64bits(1))
	e.chVdivBits[1].Store(math.Float64bits(1))
	e.trigPosFrac.Store(math.Float64bits(.5))
	e.trigSrc.Store(0)
	e.SetSerialParams(SerialParams{Proto: serUART, ChA: 1, Baud: 115200, HaveThr: true, Threshold: 100})
	e.SetSerialMode(SerialTrigger)
	c, p, _, _, _ := e.sramConfig()
	if p.Log == 0 || !c.UART.Enabled || !c.Normal || c.TriggerChannel != 1 || c.TriggerLevel != 100 || c.TriggerHysteresis != 12 {
		t.Fatalf("UART must use C2 raw calibration even with reduced storage rate: %+v %+v", c, p)
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestHardwareUARTPlanning(t *testing.T) {
	p := SerialParams{Proto: serUART, Baud: 115200, ChA: 1, Inverted: true, HaveThr: true, Threshold: 103, Bytes: []int{0x48, 0x69}}
	c := planHardwareUART(p)
	if !c.Enabled || c.BitTicks != 1085 || c.Pattern != 0x4869 || c.Length != 2 || c.Channel != 1 || !c.Inverted {
		t.Fatalf("wrong hardware plan: %+v", c)
	}
	for _, change := range []func(*SerialParams){
		func(p *SerialParams) { p.HaveThr = false },
		func(p *SerialParams) { p.Baud = 0 },
		func(p *SerialParams) { p.Baud = 1 },
		func(p *SerialParams) { p.Bits = 7 },
		func(p *SerialParams) { p.Parity = "even" },
		func(p *SerialParams) { p.Proto = serI2C },
		func(p *SerialParams) { p.Bytes = []int{1, 2, 3, 4, 5} },
		func(p *SerialParams) { p.Bytes = []int{256} },
		func(p *SerialParams) { p.HaveThr = true; p.Threshold = -1 },
	} {
		q := p
		change(&q)
		if planHardwareUART(q).Enabled {
			t.Fatalf("unsupported config did not fall back: %+v", q)
		}
	}
}
