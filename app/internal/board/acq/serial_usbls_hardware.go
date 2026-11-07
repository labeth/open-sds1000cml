// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func PlanHardwareUSBLS(p *core.SerialParams) sramcapture.USBLSTriggerConfig {
	if p.Proto != core.SerUSB || !p.HaveThr || p.ChA < 0 || p.ChA > 1 || len(p.Bytes) > 4 || p.Baud <= 0 || math.IsNaN(p.Threshold) || math.IsInf(p.Threshold, 0) || p.Threshold < 0 || p.Threshold > 255 {
		return sramcapture.USBLSTriggerConfig{}
	}
	ticks := math.Round(125e6 * 256 / float64(p.Baud))
	if ticks < 2048 || ticks > 0xffffff {
		return sramcapture.USBLSTriggerConfig{}
	}
	c := sramcapture.USBLSTriggerConfig{Enabled: true, Channel: uint8(p.ChA), Inverted: p.Inverted, BitTicksQ8: uint32(ticks), Length: uint8(len(p.Bytes))} //nolint:gosec // G115: the checks above bound each field
	for _, b := range p.Bytes {
		if b < 0 || b > 255 {
			return sramcapture.USBLSTriggerConfig{}
		}
		c.Pattern = c.Pattern<<8 | uint32(b)
	}
	return c
}
