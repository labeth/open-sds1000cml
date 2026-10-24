// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "fmt"

// TRLC-LINKS: REQ-SDS-013
type I2CTriggerConfig struct {
	Enabled      bool   `json:"enabled"`
	ClockChannel uint8  `json:"clock_channel"`
	Inverted     bool   `json:"inverted"`
	Address      int    `json:"address"`   // -1 accepts any seven-bit address
	Direction    uint8  `json:"direction"` // 0 write, 1 read, 2 either
	Pattern      uint32 `json:"pattern"`
	Length       uint8  `json:"length"`
	// Samples asks for the receiver on the samples (capability 0x4902,
	// i2c_sample.v), which reads SDA Delay sixteenths of a nanosecond after
	// SCL's interpolated crossing (the channels' sampling phase): it holds
	// high-speed mode's 10 ns setup.
	Samples bool `json:"samples,omitempty"`
	Delay   int8 `json:"delay,omitempty"`
}

// I2C receiver capabilities (register 53): on the 125 MHz line levels, or
// on the converters' samples with interpolated edges.
const (
	i2cLevels  = 0x4901
	i2cSamples = 0x4902
)

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsI2C() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	revision, err := c.read(13)
	if err != nil || revision < 10 {
		return false, err
	}
	capability, err := c.read(53)
	return (capability == i2cLevels || capability == i2cSamples) && err == nil, err
}

// SupportsSampleI2C reports the I2C receiver on the samples (i2c_sample.v).
// TRLC-LINKS: REQ-SDS-229
func (c *Capture) SupportsSampleI2C() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.read(53)
	return v == i2cSamples && err == nil, err
}

// Called with the capture lock held and the fabric idle.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) configureI2C(revision uint16, p I2CTriggerConfig) error {
	capability := uint16(0)
	if revision >= 10 {
		var err error
		capability, err = c.read(53)
		if err != nil {
			return err
		}
	}
	if (capability != i2cLevels && capability != i2cSamples) || (p.Enabled && p.Samples && capability != i2cSamples) {
		if p.Enabled {
			return fmt.Errorf("sramcapture: fabric lacks hardware I2C trigger")
		}
		return nil
	}
	control := uint16(0)
	if p.Enabled {
		control = 1 | uint16(p.ClockChannel)<<1 | uint16(p.Direction)<<3 | uint16(p.Length)<<6
		if p.Inverted {
			control |= 4
		}
		if p.Address < 0 {
			control |= 32
		}
		// The registers, then the control, in one batch.
		regs := [][2]uint16{{54, uint16(p.Address) & 127}, {55, uint16(p.Pattern)}, {56, uint16(p.Pattern >> 16)}}
		if capability == i2cSamples {
			regs = append(regs, [2]uint16{regSampleDelay, uint16(uint8(p.Delay))})
		}
		return c.writeMany(append(regs, [2]uint16{53, control})...)
	}
	return c.write(53, control)
}
