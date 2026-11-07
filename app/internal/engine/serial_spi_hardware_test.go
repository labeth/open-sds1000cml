// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"testing"

	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// SPI on the samples at 25 MHz and up re-arms on a band of 6 codes, inside
// the converters' offset spread the slower serial triggers take.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-229
func TestPlanHysteresisFastSPI(t *testing.T) {
	e := &Engine{interleaveCalibration: &InterleaveCalibration{Offset: [2][5]float64{{-4, -2, 7, 3, -4}, {-2, 2, 1, 1, -2}}}}
	for _, c := range []struct {
		gap  uint32
		want uint8
	}{{4, 6}, {8, 6}, {9, 13}, {188, 13}} {
		cfg := sramcapture.Config{SPI: sramcapture.SPITriggerConfig{Enabled: true, ClockChannel: 1, GapTicks: c.gap}}
		e.planHysteresis(&cfg)
		if cfg.TriggerHysteresis != c.want {
			t.Fatalf("gap %d: band %d, want %d", c.gap, cfg.TriggerHysteresis, c.want)
		}
	}
}
