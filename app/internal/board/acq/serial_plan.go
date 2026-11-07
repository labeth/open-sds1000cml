// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// PlanSerial installs the loaded image's receiver and, beyond its native
// matcher, the sequence trigger for serial request serial (thresholds in
// codes); fallbackNormal is the edge trigger left when the request cannot
// be planned in hardware. arinc holds ARINC 429's slicer levels from a
// recent record (ArincLevels), zero when none is known yet. The capture's
// Bus trigger and the stream paths plan through it.
// TRLC-LINKS: REQ-SDS-013
func (b *Board) PlanSerial(c *sramcapture.Config, serial core.SerialParams, fallbackNormal bool, arinc uint32) {
	caps := b.trig.caps
	if SymbolProtocol(serial.Proto) {
		// ADR-SYMBOL-TRIGGER: the whole request compiles into the symbol
		// trigger (it has no sequence trigger: the units are its window's).
		if caps.Symbol {
			c.Symbol = PlanHardwareSymbol(&serial)
		}
		if !c.Symbol.Enabled {
			c.Normal = fallbackNormal
			return
		}
		c.Normal, c.TriggerChannel = true, c.Symbol.Channel
		first, second := uint8(math.Round(serial.Threshold)), SecondLevel(serial)
		if int(c.Symbol.Channel) != serial.ChA {
			first, second = second, first
		}
		c.TriggerLevel = first
		if c.Symbol.TwoLines {
			c.SecondLevel = second
		}
		return
	}
	if serial.Proto == core.SerMIDI {
		// MIDI is UART characters, 8N1, on the UART receiver.
		serial.Proto, serial.Bits, serial.Parity = core.SerUART, 8, core.ParityNone
	}
	// A request beyond the protocol's native matcher runs the decoder
	// unfiltered and matches with the sequence trigger; without that
	// trigger it stays a software qualification.
	request, needSequence := serial, core.SequenceNeeded(serial) || (caps.SequenceOnly && len(core.PatternOf(serial)) != 0)
	if needSequence {
		serial.Bytes, serial.Pattern = nil, nil
		if !caps.Sequence {
			serial.Proto = 0
		}
	}
	if caps.Packet != 0 {
		c.Packet = PlanHardwarePacket(&serial, caps.Packet, arinc)
	}
	if caps.Manchester {
		c.Manchester = PlanHardwareManchester(&serial)
	}
	if c.Packet.Enabled || c.Manchester.Enabled {
		c.Normal = true
		c.TriggerChannel = uint8(serial.ChA) //nolint:gosec // G115: the channel is 0 or 1 (serialPlanParams)
		c.TriggerLevel = uint8(math.Round(serial.Threshold))
	}
	if caps.USBLS {
		c.USBLS = PlanHardwareUSBLS(&serial)
	}
	if caps.MIL1553 {
		c.MIL1553 = PlanHardwareMIL1553(&serial)
	}
	if caps.SENT {
		c.SENT = PlanHardwareSENT(&serial)
	}
	if caps.UART {
		c.UART = PlanHardwareUART(&serial, caps.SampleUART)
	}
	if caps.SPI {
		c.SPI = PlanHardwareSPI(&serial, caps.SampleSPI)
	}
	if caps.I2C {
		c.I2C = PlanHardwareI2C(&serial, caps.SampleI2C)
	}
	if c.USBLS.Enabled {
		c.Normal = true
		c.TriggerChannel = c.USBLS.Channel
		c.TriggerLevel = uint8(math.Round(serial.Threshold))
	}
	if c.MIL1553.Enabled {
		c.Normal = true
		c.TriggerChannel = c.MIL1553.Channel
		c.TriggerLevel = uint8(math.Round(serial.Threshold))
	}
	if c.SENT.Enabled {
		c.Normal = true
		c.TriggerChannel = c.SENT.Channel
		c.TriggerLevel = uint8(math.Round(serial.Threshold))
	}
	if c.UART.Enabled {
		c.Normal = true
		c.TriggerChannel = c.UART.Channel
		if serial.HaveThr {
			c.TriggerLevel = uint8(math.Round(serial.Threshold))
		}
	}
	if c.SPI.Enabled {
		c.Normal = true
		c.TriggerChannel = c.SPI.ClockChannel
		c.TriggerLevel, c.SecondLevel = uint8(math.Round(serial.Threshold)), SecondLevel(serial)
	}
	if c.I2C.Enabled {
		c.Normal = true
		c.TriggerChannel = c.I2C.ClockChannel
		c.TriggerLevel, c.SecondLevel = uint8(math.Round(serial.Threshold)), SecondLevel(serial)
	}
	if needSequence && HardwareSerialName(*c) != "" {
		if c.Sequence = PlanSequence(request); c.Sequence.Length == 0 {
			// Unplannable in hardware: back to software qualification.
			c.Packet, c.Manchester, c.USBLS, c.MIL1553, c.SENT = sramcapture.PacketTriggerConfig{}, sramcapture.ManchesterTriggerConfig{}, sramcapture.USBLSTriggerConfig{}, sramcapture.MIL1553TriggerConfig{}, sramcapture.SENTTriggerConfig{}
			c.UART, c.SPI, c.I2C = sramcapture.UARTTriggerConfig{}, sramcapture.SPITriggerConfig{}, sramcapture.I2CTriggerConfig{}
			c.Normal = fallbackNormal
		}
	}
}

// SetConverterOffsets attaches the converter cores' measured offsets in
// codes (the interleave calibration's), which the trigger's re-arm band
// spans; nil: none measured.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-016
func (b *Board) SetConverterOffsets(off *[2][5]float64) { b.trig.offsets = off }

// PlanHysteresis sets the fabric trigger's re-arm band from the converter
// offsets of the trigger channel (and the second line of I2C/SPI), at
// least minCodes.
// TRLC-LINKS: REQ-SDS-011
func (b *Board) PlanHysteresis(c *sramcapture.Config) { b.planHysteresis(c, minHysteresis) }

// TRLC-LINKS: REQ-SDS-011
func (b *Board) planHysteresis(c *sramcapture.Config, minCodes uint8) {
	// The fabric sees raw interleaved codes, before display calibration. Its
	// re-arm band must span the measured converter offsets plus quantization
	// noise, otherwise a calibrated-looking falling trace can trigger rising.
	// The offsets are the converter cores' own, after the front-end gain, so
	// they hold at every V/div: gating them on the calibration's verified
	// scale leaves a 4-code band elsewhere, and a level within the cores'
	// spread of the baseline fires on raw noise (2 V/div at 0.4 V and 5 V/div
	// at 1.6 V trigger with no edge in the frame).
	// The edge trigger and the qualifier read the raw codes at every timebase
	// (ADR-EDGE-TRIGGER-RAW), decimated records too: the band spans the
	// cores' offsets there as well.
	c.TriggerHysteresis = minCodes
	off := b.trig.offsets
	if off == nil {
		return
	}
	spread := func(ch int) float64 {
		offsets := off[ch&1]
		lo, hi := offsets[0], offsets[0]
		for _, v := range offsets[1:] {
			lo = math.Min(lo, v)
			hi = math.Max(hi, v)
		}
		return hi - lo
	}
	band := spread(int(c.TriggerChannel))
	if c.I2C.Enabled || c.SPI.Enabled || (c.Symbol.Enabled && c.Symbol.TwoLines) {
		band = math.Max(band, spread(1-int(c.TriggerChannel)))
	}
	c.TriggerHysteresis = uint8(math.Min(255, math.Max(float64(minCodes), math.Ceil(band)+2)))
	if c.SPI.Enabled && c.SPI.GapTicks <= SampleSPIFastGap && c.TriggerHysteresis > SampleSPIFastBand {
		// SPI on the samples at 25 MHz and up: its edges take a few ns, too
		// fast for a converter's offset to cross the level back on them, and
		// its sampled swing shrinks (104 MHz at 1 V/div with 10x probes dipped
		// to 116 of the 115 the offsets' band asked, and a clock edge was lost)
		c.TriggerHysteresis = SampleSPIFastBand
	}
}

// PlanZones is the zone trigger in the FPGA (ADR-HARDWARE-QUALIFIERS) for
// zones zs: each zone's window around the edge in buckets of the smallest
// power-of-two clocks that keep every window and the decision's wait inside
// the 1024 buckets of history, the zones' levels as record codes. delayS is
// how long after an edge trigger's the FPGA fires (the wait for the latest
// window's end and the fixed decision time); why says why there is no plan
// ("" when there is one).
// TRLC-LINKS: REQ-SDS-014
func (b *Board) PlanZones(zs []hal.Zone) (z sramcapture.ZoneConfig, delayS float64, why string) {
	if !b.trig.caps.Qualifier {
		return z, 0, "this FPGA image has no zone trigger"
	}
	if len(zs) > len(z.Zones) {
		return z, 0, "the FPGA takes four zones"
	}
	const clockS = sramcapture.ZoneClockNs * 1e-9
	for bucket := 1; bucket <= 4096; bucket *= 2 {
		bs := clockS * float64(bucket)
		minStart, maxEnd := 0, -1<<30
		var ws [4]sramcapture.ZoneWindow
		for i, zn := range zs {
			lo, hi := math.Min(zn.FromS, zn.ToS), math.Max(zn.FromS, zn.ToS)
			start, end := int(math.Floor(lo/bs)), int(math.Ceil(hi/bs))
			minStart, maxEnd = min(minStart, start), max(maxEnd, end)
			ch := zn.Source.Index & 1
			cl, chi := b.zoneCode(ch, zn.Low), b.zoneCode(ch, zn.High)
			ws[i] = sramcapture.ZoneWindow{
				Enabled: true, Channel: uint8(ch), Lo: uint8(min(cl, chi)), Hi: uint8(max(cl, chi)), //nolint:gosec // G115: zoneCode clamps to 0..255
				Start: int16(max(start, -1024)), End: int16(min(end, 1024)), Avoid: zn.Avoid, //nolint:gosec // G115: a bucket is refused below unless both lie within ±1000
			}
		}
		wait := max(maxEnd+1, 1)
		if minStart < -1000 || maxEnd > 1000 || wait-minStart+1 > 1023 {
			continue // too fine a bucket for the windows: twice as long
		}
		z = sramcapture.ZoneConfig{Zones: ws, Wait: uint16(wait), Bucket: uint16(bucket)}
		return z, float64(wait)*bs + (sramcapture.ZoneDecideClocks*sramcapture.ZoneClockNs+sramcapture.ZoneFireNs)*1e-9, ""
	}
	return sramcapture.ZoneConfig{}, 0, "the zones span more time than the FPGA's history"
}

// zoneCode is a zone's level v on channel ch as a record code, clamped to
// the codes.
// TRLC-LINKS: REQ-SDS-014
func (b *Board) zoneCode(ch int, v float64) int {
	return min(max(int(math.Round(b.levelScale(ch).Word(v))), 0), 255)
}
