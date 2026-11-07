// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"math"
	"testing"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
)

// TRLC-LINKS: REQ-SDS-013
func TestHardwareUARTUsesSelectedRawChannelHysteresis(t *testing.T) {
	e := &Engine{band: Band{TdivS: 500e-6}}
	setCaps(e, func(c *acq.Caps) { c.UART = true })
	e.interleaveCalibration = &InterleaveCalibration{Vdiv: [2]float64{1, 1}, Offset: [2][5]float64{{1, -1, 0, 0, 0}, {8, -2, -2, -2, -2}}}
	e.chVdivBits[0].Store(math.Float64bits(1))
	e.chVdivBits[1].Store(math.Float64bits(1))
	e.trigPosFrac.Store(math.Float64bits(.5))
	e.trigSrc.Store(0)
	e.SetSerialParams(SerialParams{Proto: core.SerUART, ChA: 1, Baud: 115200, HaveThr: true, Threshold: 100})
	e.SetSerialMode(SerialTrigger)
	c, p, _, _, _ := e.sramConfig()
	if p.Log == 0 || !c.UART.Enabled || !c.Normal || c.TriggerChannel != 1 || c.TriggerLevel != 100 || c.TriggerHysteresis != 12 {
		t.Fatalf("UART must use C2 raw calibration even with reduced storage rate: %+v %+v", c, p)
	}
}
