// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
)

// Hardware plans mirror the registry decoders: a pattern the software matcher
// could not find in one packet is never sent to hardware, and polarity or
// threshold choices the decoder does not honour are not added.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func serialThresholdOK(p SerialParams) bool {
	return p.HaveThr && !math.IsNaN(p.Threshold) && !math.IsInf(p.Threshold, 0) && p.Threshold >= 0 && p.Threshold <= 255 &&
		p.ChA >= 0 && p.ChA <= 1 && p.Baud > 0
}

// q8Ticks is a bit period in 125 MHz receiver clocks with eight fraction bits.
// TRLC-LINKS: REQ-SDS-013
func q8Ticks(baud int) uint32 { return uint32(math.Round(125e6 * 256 / float64(baud))) }

// planHardwarePacket plans CAN / CAN FD, FlexRay or ARINC 429 for the shared
// packet block. mask is the image's protocol mask; arinc holds slicer levels
// taken from a recent record (arincLevels), zero when none is known yet.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func planHardwarePacket(p SerialParams, mask uint16, arinc uint32) sramcapture.PacketTriggerConfig {
	off := sramcapture.PacketTriggerConfig{}
	if !serialThresholdOK(p) || p.Proto < 0 || p.Proto > 15 || mask&(1<<p.Proto) == 0 {
		return off
	}
	c := sramcapture.PacketTriggerConfig{Enabled: true, Protocol: uint8(p.Proto), Channel: uint8(p.ChA)}
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
		c.Length = uint8(len(p.Bytes))
		return true
	}
	switch p.Proto {
	case serCAN:
		c.Ticks, c.Aux = q8Ticks(p.Baud), q8Ticks(p.Baud)
		if p.DataBaud > 0 {
			c.Aux = q8Ticks(p.DataBaud)
		}
		// The registry decodes dominant-low regardless of the polarity control.
		if c.Ticks < 2048 || c.Ticks > 0xffffff || c.Aux < 2048 || c.Aux > 0xffffff || !bytes(8) {
			return off
		}
	case serFlexRay:
		c.Ticks = q8Ticks(p.Baud)
		if c.Ticks < 1024 || c.Ticks > 0xffffff || !bytes(8) {
			return off
		}
	case serARINC:
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
			c.Pattern = uint64(0x7ffff)<<42 | uint64(p.Bytes[0])<<10
		}
	default:
		return off
	}
	return c
}

// arincLevels converts the decoder's slicer for this record into the packet
// block's integer thresholds {exit_hi, exit_lo, hi, lo}: a code enters HI at
// ceil(hi), LO at floor(lo), and returns to NULL below ceil(exit_hi) or above
// floor(exit_lo), matching the decoder's float comparisons.
// TRLC-LINKS: REQ-SDS-018
func arincLevels(codes []uint8, p SerialParams) uint32 {
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

// planHardwareManchester follows DecodeManchester: 1..16-bit words (8 by
// default), either convention and bit order, up to four contiguous words.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func planHardwareManchester(p SerialParams) sramcapture.ManchesterTriggerConfig {
	bits := p.Bits
	if bits == 0 {
		bits = 8
	}
	ticks := math.Round(125e6 / math.Max(1, float64(p.Baud)))
	if p.Proto != serManchester || !serialThresholdOK(p) || bits < 1 || bits > 16 || len(p.Bytes) > 4 || ticks < 8 || ticks > 0x3fffff {
		return sramcapture.ManchesterTriggerConfig{}
	}
	c := sramcapture.ManchesterTriggerConfig{Enabled: true, Channel: uint8(p.ChA), IEEE: p.IEEE, MSB: p.MSB, Bits: uint8(bits), BitTicks: uint32(ticks), Length: uint8(len(p.Bytes))}
	for _, w := range p.Bytes {
		if w < 0 || w >= 1<<bits {
			return sramcapture.ManchesterTriggerConfig{}
		}
		c.Pattern = c.Pattern<<16 | uint64(w)
	}
	return c
}

// hardwareSerialName names the enabled hardware decoder, or "" for none,
// with a "-sequence" suffix when the sequence trigger matches for it.
// TRLC-LINKS: REQ-SDS-013
func hardwareSerialName(c sramcapture.Config) string {
	name := decoderName(c)
	if name != "" && c.Sequence.Length != 0 {
		name += "-sequence"
	}
	return name
}

// TRLC-LINKS: REQ-SDS-013
func decoderName(c sramcapture.Config) string {
	switch {
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

// probeSerialHardware records the loaded image's hardware decoders. It runs
// at start and after every image switch.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) probeSerialHardware() {
	for _, probe := range []struct {
		name string
		dst  *bool
		fn   func() (bool, error)
	}{
		{"UART", &e.hardwareUART, e.sram.SupportsUART}, {"I2C", &e.hardwareI2C, e.sram.SupportsI2C},
		{"SPI", &e.hardwareSPI, e.sram.SupportsSPI}, {"SENT", &e.hardwareSENT, e.sram.SupportsSENT},
		{"USB", &e.hardwareUSBLS, e.sram.SupportsUSBLS}, {"MIL-STD-1553", &e.hardwareMIL1553, e.sram.SupportsMIL1553},
		{"Manchester", &e.hardwareManchester, e.sram.SupportsManchester},
		{"sequence", &e.hardwareSequence, e.sram.SupportsSequence},
		{"envelope", &e.hardwareEnvelope, e.sram.SupportsEnvelope},
		{"sequence-only", &e.sequenceOnly, e.sram.SequenceOnly},
	} {
		var err error
		if *probe.dst, err = probe.fn(); err != nil {
			e.logf("hardware %s capability: %v; using software", probe.name, err)
		}
	}
	var err error
	if e.hardwarePacket, err = e.sram.PacketProtocols(); err != nil {
		e.logf("hardware packet capability: %v; using software", err)
	}
	if e.rawOnly, err = e.sram.RawOnly(); err != nil {
		e.logf("decimation capability: %v", err)
	}
}

// Protocol images (ADR-PROTOCOL-PACKET-IMAGE); "" is the general image.
const (
	imagePacket = "packet"
	// imageLine has the line-code decoders Manchester and USB; the packet
	// image has ARINC 429, CAN, FlexRay, MIL-1553 and SENT.
	imageLine = "line"
	// imageStream is the continuous-capture image roll mode runs on
	// (ADR-STREAM-IMAGE).
	imageStream = "stream"
)

// protocolImage names the image whose hardware triggers p, or "" for the
// general image with UART, I2C and SPI (ADR-IMAGE-REGROUP-DECIMATION).
// TRLC-LINKS: REQ-SDS-013
func protocolImage(p SerialParams) string {
	switch p.Proto {
	case serManchester, serUSB:
		return imageLine
	case serCAN, serARINC, serFlexRay, serMIL1553, serSENT:
		return imagePacket
	}
	return ""
}

// hasImage reports an embedded protocol image.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) hasImage(name string) bool {
	switch name {
	case imagePacket:
		return e.images.HasPacket()
	case imageLine:
		return e.images.HasLine()
	case imageStream:
		return e.images.HasStream()
	}
	return true
}

// wantImage: a hardware-plannable Manchester, CAN, ARINC or FlexRay trigger
// needs its protocol image; everything else runs on the general image, which
// also keeps precision decimation.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) wantImage() string {
	if e.images == nil {
		return ""
	}
	// Roll streaming (opt-in) runs gap-free on the stream image; a stop keeps
	// it, so RUN resumes without a reload.
	if e.rollStream.Load() && e.images.HasStream() && (e.rollActive() || (!e.running.Load() && e.protocolImage == imageStream)) {
		return imageStream
	}
	if e.serialMode.Load() != SerialTrigger {
		return ""
	}
	e.ser.mu.Lock()
	p := e.ser.params
	e.ser.mu.Unlock()
	if name := protocolImage(p); name != "" && serialThresholdOK(p) && e.hasImage(name) {
		return name
	}
	return ""
}

// serviceImage loads the image the serial trigger needs, between
// acquisitions. It reports whether the fabric was replaced. A failed load
// returns to the general image and is not retried until the choice changes.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-005
func (e *Engine) serviceImage() bool {
	want := e.wantImage()
	if want != e.failedImage {
		e.failedImage = ""
	}
	if want == e.protocolImage || (want != "" && want == e.failedImage) {
		return false
	}
	return e.loadImage(want)
}

// loadImage switches to a protocol image, or the general image for "".
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-005
func (e *Engine) loadImage(name string) bool {
	load := e.images.LoadGeneral
	switch name {
	case imagePacket:
		load = e.images.LoadPacket
	case imageLine:
		load = e.images.LoadLine
	case imageStream:
		load = e.images.LoadStream
	}
	shown := name
	if shown == "" {
		shown = "general"
	}
	e.logf("engine: loading the %s image", shown)
	e.setBusy("loading "+shown+" image", 0) // the loading indicator covers the reload
	// A load takes seconds without bus reads; beat so the OTA agent does not
	// judge the app hung and restart it.
	beating := make(chan struct{})
	defer close(beating)
	go func() {
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-beating:
				return
			case <-tick.C:
				e.beatN.Add(1)
			}
		}
	}()
	if err := load(); err != nil {
		e.logf("engine: %s image: %v", shown, err)
		if name != "" {
			e.failedImage = name
			if restore := e.images.LoadGeneral(); restore != nil {
				e.busErr(restore)
			}
		} else {
			e.busErr(err)
		}
		name, shown = "", "general"
	}
	e.protocolImage = name
	e.probeSerialHardware()
	if err := e.applyPanelScan(); err != nil {
		e.logf("engine: panel scan: %v", err)
	}
	e.mu.Lock()
	e.stats.Image = shown
	e.mu.Unlock()
	return true
}

// noteARINCLevels takes the ARINC slicer levels from a raw record the first
// time the current decode parameters need them; hardware ARINC triggering and
// streaming wait for them.
// TRLC-LINKS: REQ-SDS-018
func (e *Engine) noteARINCLevels(f *Frame) {
	if e.arincAux.Load() != 0 {
		return
	}
	e.ser.mu.Lock()
	p := e.ser.params
	e.ser.mu.Unlock()
	if p.Proto != serARINC || !serialThresholdOK(p) || f.Valid < 8 || f.Decimation > 1 {
		return
	}
	codes := f.C1[:f.Valid]
	if p.ChA == 1 {
		codes = f.C2[:f.Valid]
	}
	e.arincAux.Store(arincLevels(codes, p))
}
