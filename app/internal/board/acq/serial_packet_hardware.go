// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"

	"github.com/labeth/open-dso-core/decode"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// SerialThresholdOK reports whether p has a usable threshold (0..255 codes),
// channel and rate. Hardware plans mirror the registry decoders: a pattern
// the software matcher could not find in one packet is never sent to
// hardware, and polarity or threshold choices the decoder does not honour
// are not added.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func SerialThresholdOK(p *core.SerialParams) bool {
	return p.HaveThr && !math.IsNaN(p.Threshold) && !math.IsInf(p.Threshold, 0) && p.Threshold >= 0 && p.Threshold <= 255 &&
		p.ChA >= 0 && p.ChA <= 1 && p.Baud > 0
}

// q8Ticks is a bit period in 125 MHz receiver clocks with eight fraction bits.
// TRLC-LINKS: REQ-SDS-013
func q8Ticks(baud int) uint32 { return uint32(math.Round(125e6 * 256 / float64(baud))) }

// PlanHardwarePacket plans CAN / CAN FD, FlexRay or ARINC 429 for the shared
// packet block. mask is the image's protocol mask; arinc holds slicer levels
// taken from a recent record (arincLevels), zero when none is known yet.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func PlanHardwarePacket(p *core.SerialParams, mask uint16, arinc uint32) sramcapture.PacketTriggerConfig {
	off := sramcapture.PacketTriggerConfig{}
	if !SerialThresholdOK(p) || p.Proto < 0 || p.Proto > 15 || mask&(1<<p.Proto) == 0 {
		return off
	}
	c := sramcapture.PacketTriggerConfig{Enabled: true, Protocol: uint8(p.Proto), Channel: uint8(p.ChA)} //nolint:gosec // G115: the protocol is checked to 0..15 above, the channel is 0 or 1
	bytes := func(limit int) bool {
		if len(p.Bytes) > limit {
			return false
		}
		for _, b := range p.Bytes {
			if b < 0 || b > 255 {
				return false
			}
			c.Pattern = c.Pattern<<8 | uint64(b)
		}
		c.Length = uint8(len(p.Bytes)) //nolint:gosec // G115: a pattern of at most eight bytes
		return true
	}
	switch p.Proto {
	case core.SerCAN:
		c.Ticks, c.Aux = q8Ticks(p.Baud), q8Ticks(p.Baud)
		if p.DataBaud > 0 {
			c.Aux = q8Ticks(p.DataBaud)
		}
		// The registry decodes dominant-low regardless of the polarity control.
		if c.Ticks < 2048 || c.Ticks > 0xffffff || c.Aux < 2048 || c.Aux > 0xffffff || !bytes(8) {
			return off
		}
	case core.SerFlexRay:
		c.Ticks = q8Ticks(p.Baud)
		if c.Ticks < 1024 || c.Ticks > 0xffffff || !bytes(8) {
			return off
		}
	case core.SerARINC:
		// One data field per word: a single pattern value selects bits 11..29.
		ticks := math.Round(125e6 / float64(p.Baud))
		if arinc == 0 || ticks < 4 || ticks > 0xffffff || len(p.Bytes) > 1 {
			return off
		}
		c.Ticks, c.Aux = uint32(ticks), arinc
		if len(p.Bytes) == 1 {
			if p.Bytes[0] < 0 || p.Bytes[0] > 0x7ffff {
				return off
			}
			c.Pattern = uint64(0x7ffff)<<42 | uint64(p.Bytes[0])<<10 //nolint:gosec // G115: checked to 0..0x7ffff above
		}
	default:
		return off
	}
	return c
}

// ArincLevels converts the decoder's slicer for this record into the packet
// block's integer thresholds {exit_hi, exit_lo, hi, lo}: a code enters HI at
// ceil(hi), LO at floor(lo), and returns to NULL below ceil(exit_hi) or above
// floor(exit_lo), matching the decoder's float comparisons.
// TRLC-LINKS: REQ-SDS-018
func ArincLevels(codes []uint8, p core.SerialParams) uint32 {
	_, hi, lo, exitHi, exitLo, reason := decode.ARINC429Levels(codes, decode.ARINC429Cfg{Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: p.HaveThr})
	if reason != "" {
		return 0
	}
	h, l, eh, el := math.Ceil(hi), math.Floor(lo), math.Ceil(exitHi), math.Floor(exitLo)
	if !(l >= 0 && l < el && el <= eh && eh < h && h <= 255) {
		return 0
	}
	return uint32(eh)<<24 | uint32(el)<<16 | uint32(h)<<8 | uint32(l)
}

// PlanHardwareManchester follows Manchester: 1..16-bit words (8 by
// default), either convention and bit order, up to four contiguous words.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func PlanHardwareManchester(p *core.SerialParams) sramcapture.ManchesterTriggerConfig {
	bits := p.Bits
	if bits == 0 {
		bits = 8
	}
	ticks := math.Round(125e6 / math.Max(1, float64(p.Baud)))
	if p.Proto != core.SerManchester || !SerialThresholdOK(p) || bits < 1 || bits > 16 || len(p.Bytes) > 4 || ticks < 8 || ticks > 0x3fffff {
		return sramcapture.ManchesterTriggerConfig{}
	}
	c := sramcapture.ManchesterTriggerConfig{Enabled: true, Channel: uint8(p.ChA), IEEE: p.IEEE, MSB: p.MSB, Bits: uint8(bits), BitTicks: uint32(ticks), Length: uint8(len(p.Bytes))} //nolint:gosec // G115: the checks above bound each field
	for _, w := range p.Bytes {
		if w < 0 || w >= 1<<bits {
			return sramcapture.ManchesterTriggerConfig{}
		}
		c.Pattern = c.Pattern<<16 | uint64(w)
	}
	return c
}

// HardwareSerialName names the enabled hardware decoder, or "" for none,
// with a "-sequence" suffix when the sequence trigger matches for it.
// TRLC-LINKS: REQ-SDS-013
func HardwareSerialName(c sramcapture.Config) string {
	name := decoderName(c)
	if name != "" && c.Sequence.Length != 0 {
		name += "-sequence"
	}
	return name
}

// TRLC-LINKS: REQ-SDS-013
func decoderName(c sramcapture.Config) string {
	switch {
	case c.Symbol.Enabled:
		return "symbol"
	case c.Packet.Enabled && c.Packet.Protocol == sramcapture.PacketCAN:
		return "can"
	case c.Packet.Enabled && c.Packet.Protocol == sramcapture.PacketARINC:
		return "arinc429"
	case c.Packet.Enabled && c.Packet.Protocol == sramcapture.PacketFlexRay:
		return "flexray"
	case c.Manchester.Enabled:
		return "manchester"
	case c.USBLS.Enabled:
		return "usbls"
	case c.MIL1553.Enabled:
		return "mil1553"
	case c.SENT.Enabled:
		return "sent"
	case c.SPI.Enabled:
		return "spi"
	case c.I2C.Enabled:
		return "i2c"
	case c.UART.Enabled:
		return "uart"
	}
	return ""
}

// Protocol images (ADR-PROTOCOL-PACKET-IMAGE); "" is the general image.
const (
	ImagePacket = "packet"
	// ImageLine has the line-code decoder Manchester; the packet
	// image has ARINC 429, CAN and FlexRay.
	ImageLine = "line"
	// ImageFrame has MIL-1553, SENT and USB, out of the packet and line
	// images so each keeps room for box decimation (ADR-TRIGGER-ANY-TIMEBASE).
	ImageFrame = "frame"
	// ImageStream is the continuous-capture image roll mode runs on
	// (ADR-STREAM-IMAGE).
	ImageStream = "stream"
	// ImageBits is the stream image with digital words, the decoded
	// stream's lines sliced in the FPGA (ADR-STREAM-BITS).
	ImageBits = "bits"
	// ImageFast has SPI, UART and I2C on the samples: SPI to a 125 MHz
	// clock, UART to 62.5 MBd, I2C's high-speed mode (ADR-SAMPLE-DOMAIN-SPI).
	ImageFast = "fast"
)

// ImageGeneralName is how the status names the general image.
const ImageGeneralName = "general"

// ProtocolImage names the image whose hardware triggers p, or "" for the
// general image with UART, I2C and SPI (ADR-IMAGE-REGROUP-DECIMATION). SPI,
// UART and I2C past the reach of the general image's receivers on 125 MHz
// line levels (an SPI clock of about 20 MHz, a UART of about 10 MBd or any
// format but 8N1, an I2C clock past fast-mode plus), or with Fine asking
// for the best timing, run on the fast image's receivers on the samples
// (ADR-SAMPLE-DOMAIN-SPI); otherwise the general image stays, without a
// load of seconds.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func ProtocolImage(p core.SerialParams) string {
	if SymbolProtocol(p.Proto) {
		return ImageQualify
	}
	switch p.Proto {
	case core.SerManchester:
		return ImageLine
	case core.SerCAN, core.SerARINC, core.SerFlexRay:
		return ImagePacket
	case core.SerMIL1553, core.SerSENT, core.SerUSB:
		return ImageFrame
	case core.SerSPI:
		if (p.Fine || !PlanHardwareSPI(&p, false).Enabled) && PlanHardwareSPI(&p, true).Enabled {
			return ImageFast
		}
	case core.SerUART:
		if (p.Fine || !PlanHardwareUART(&p, false).Enabled) && PlanHardwareUART(&p, true).Enabled {
			return ImageFast
		}
	case core.SerI2C:
		if (p.Fine || p.I2CClockHz > generalI2CReachHz) && PlanHardwareI2C(&p, true).Enabled {
			return ImageFast
		}
	}
	return ""
}

// ArincBitSamples is the samples a bit a decimated record must hold for the
// ARINC slicer levels to be taken from it: its filter rounds the edges, not
// the levels a half bit holds.
const ArincBitSamples = 8
