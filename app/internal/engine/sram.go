// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/labeth/open-dso-core/hal"

	"github.com/labeth/open-dso-core/dsp"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// rawScreenTdiv is the slowest timebase whose ten divisions a raw record
// (an image without decimation: 2 ns a sample, one SRAM deep) holds whole.
// TRLC-LINKS: REQ-SDS-010
func rawScreenTdiv() float64 {
	rec := float64(sramcapture.SamplesPerChannel) * 2e-9
	best := 0.0
	for _, t := range SupportedTdivs() {
		if 10*t <= rec && t > best {
			best = t
		}
	}
	return best
}

// liveScreenSamples bounds a live decimated screen (Average, ERES, Precision,
// the sample lease). The fabric's only decimator, the precision filter, passes
// 7.5 % of its output rate (-3 dB at 13.5 %), so the bound sets the live
// bandwidth: 8192 samples keep it at 1.4 to 2.8 times the 768-px plot's pixel
// Nyquist, so nothing a pixel wide is smeared. 4096 leaves it under one (at
// 500us/div, /1024 passes 36.6 kHz and a 115200 Bd UART's bits vanish in
// Average and ERES). About 13 frames a second there, against 20.
const liveScreenSamples = 8192

// fullRateFrame reports whether a frame read with bucket words a bucket (0:
// raw) from a record of the given decimation, in acquisition mode acq, holds
// the converters' own samples at the full rate (Frame.FullRate). The eye
// diagram folds only these: a bucket envelope, a decimated record or a
// filtered acquisition (average, ERES, precision) between them would fold
// another picture.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-215
func fullRateFrame(bucket, decimation uint32, acq int32) bool {
	return bucket == 0 && decimation <= 1 && acq != AcqAverage && acq != AcqEres && acq != AcqPrecision
}

// heldKey identifies a recall: the frozen record and the window read.
type heldKey struct {
	m             sramcapture.Metadata
	offset, words uint32
}

// heldRecall keeps a stopped record's recalled codes in memory, so changing
// the view of a stopped (often deep SINGLE) record does not read SRAM again.
type heldRecall struct {
	key    heldKey
	n      int
	c1, c2 []uint8
	q1, q2 []uint16
	cond   heldCond
}

// TRLC-LINKS: REQ-SDS-033
func (h *heldRecall) store(k heldKey, f *sample.Frame) {
	h.key, h.n = k, f.Len()
	h.c1 = append(h.c1[:0], f.Codes8(0)[:f.Len()]...)
	h.c2 = append(h.c2[:0], f.Codes8(1)[:f.Len()]...)
	h.q1, h.q2 = h.q1[:0], h.q2[:0]
	if len(f.Precision(0)) == f.Len() && len(f.Precision(1)) == f.Len() {
		h.q1 = append(h.q1, f.Precision(0)...)
		h.q2 = append(h.q2, f.Precision(1)...)
	}
}

// heldCondKey identifies a conditioning of the held record: it depends on
// the record and the acquisition filters, not on V/div, offset or timebase.
type heldCondKey struct {
	held      heldKey
	acq, eres int32
}

// heldCond is the held record after conditioning, with the frame fields the
// conditioning sets.
type heldCond struct {
	key                            heldCondKey
	content                        uint64 // Frame.Content of the conditioned record
	c1, c2                         []uint8
	q1, q2                         []uint16
	filter                         string
	guard                          int
	noiseGain, passband, bandwidth float64
}

// TRLC-LINKS: REQ-SDS-033
func (h *heldRecall) storeCond(k heldCondKey, f *sample.Frame) {
	c := &h.cond
	c.key = k
	c.c1 = append(c.c1[:0], f.Codes8(0)[:f.Len()]...)
	c.c2 = append(c.c2[:0], f.Codes8(1)[:f.Len()]...)
	c.q1, c.q2 = c.q1[:0], c.q2[:0]
	if len(f.Precision(0)) == f.Len() && len(f.Precision(1)) == f.Len() {
		c.q1 = append(c.q1, f.Precision(0)...)
		c.q2 = append(c.q2, f.Precision(1)...)
	}
	c.filter, c.guard = f.Cond.Filter, f.Cond.Guard
	c.noiseGain, c.passband, c.bandwidth = f.Cond.NoiseGain, f.Cond.PassbandHz, f.Cond.BandwidthHz
}

// restoreCond puts the conditioned record into f (already sized by restore),
// its Q codes into the frame's backing arrays q1, q2.
// TRLC-LINKS: REQ-SDS-033
func (h *heldRecall) restoreCond(f *sample.Frame, q1, q2 []uint16) {
	c := &h.cond
	copy(f.Codes8(0), c.c1)
	copy(f.Codes8(1), c.c2)
	if n := len(c.q1); n == f.Len() && n == len(c.q2) {
		f.Traces[0].Precision, f.Traces[1].Precision = core.QBuffer(q1, n), core.QBuffer(q2, n)
		copy(f.Precision(0), c.q1)
		copy(f.Precision(1), c.q2)
	}
	f.Cond.Filter, f.Cond.Guard = c.filter, c.guard
	f.Cond.NoiseGain, f.Cond.PassbandHz, f.Cond.BandwidthHz = c.noiseGain, c.passband, c.bandwidth
}

// restore copies the held codes into f (sized as at store) and returns the
// sample count, as the recall writer would.
// TRLC-LINKS: REQ-SDS-033
func (h *heldRecall) restore(f *sample.Frame) int {
	copy(f.Codes8(0), h.c1)
	copy(f.Codes8(1), h.c2)
	if len(h.q1) == h.n && len(f.Precision(0)) == h.n && len(f.Precision(1)) == h.n {
		copy(f.Precision(0), h.q1)
		copy(f.Precision(1), h.q2)
	}
	return h.n
}

// TRLC-LINKS: REQ-SDS-010, REQ-SDS-011, REQ-SDS-036, REQ-SDS-040
func (e *Engine) sramConfig() (sramcapture.Config, SRAMPlan, float64, bool, core.TrigParams) {
	var c sramcapture.Config
	p, tdiv, norm, tp := e.sramConfigInto(&c)
	return c, p, tdiv, norm, tp
}

// buildSRAMConfig fills c for capture plan p with trigger tp: the record's
// words around the trigger, the serial trigger, the qualifier and the zones
// the FPGA takes, and the hysteresis. It returns p with its samples a whole
// number of words.
// TRLC-LINKS: REQ-SDS-009, REQ-SDS-011, REQ-SDS-013, REQ-SDS-014
func (e *Engine) buildSRAMConfig(c *sramcapture.Config, p SRAMPlan, tp core.TrigParams) SRAMPlan {
	samples := p.Samples
	perWord := 2 - acq.BoolInt(p.Log != 0)
	words := uint32((samples + perWord - 1) / perWord) //nolint:gosec // G115: the plan's samples fit the SRAM
	p.Samples = int(words) * perWord
	pre := uint32(float64(words) * math.Float64frombits(e.trigPosFrac.Load()))
	if e.serialMode.Load() == SerialTrigger && p.SampleS > 0 {
		pre += e.serialLeadWords(p, perWord, words, pre)
	}
	if pre >= words {
		pre = words - 1
	}
	*c = sramcapture.Config{
		Source: sramcapture.ADC, DecimationLog2: p.Log, PreWords: pre, PostWords: words - pre,
		Normal: tp.Typ == TrigEdge && e.serialMode.Load() != SerialTrigger, Falling: !e.trigRising.Load(),
		TriggerChannel: e.trigChannel(), TriggerLevel: e.trigLevelCode(),
	}
	if e.hasSerialHardware() && e.serialMode.Load() == SerialTrigger {
		e.ser.Mu.Lock()
		serial := e.ser.Params
		e.ser.Mu.Unlock()
		e.spiQuietCheck(serial)
		// The matcher slices at the learned level when the request names none.
		if serial, why := e.serialPlanParams(serial); why != core.ArincOneWord {
			e.planSerial(c, serial, tp.Typ == TrigEdge && e.serialMode.Load() != SerialTrigger)
		}
	}
	if tp.Typ != TrigEdge && e.serialMode.Load() != SerialTrigger {
		// The FPGA's qualifier decides a pulse, slope or video trigger; until
		// it can (no image, the levels still unknown) captures run free
		// and nothing triggers.
		if q, level, why := e.planQualifier(tp, int(c.TriggerChannel), e.trigRising.Load()); why == "" {
			c.Qualifier, c.Normal, c.TriggerLevel = q, true, level
		}
	}
	if e.zonesOn() && e.serialMode.Load() != SerialTrigger {
		// The FPGA's zones qualify the trigger; until they can, nothing
		// triggers. They fire when the latest window has ended: the record
		// holds that delay before the trigger too.
		if z, delayS, why := e.planZones(); why == "" && c.Normal {
			c.Zones = z
			if p.SampleS > 0 {
				c.PreWords = min(c.PreWords+uint32(delayS/p.SampleS)/uint32(perWord), words-1) //nolint:gosec // G115: perWord is 1 or 2
				c.PostWords = words - c.PreWords
			}
		} else {
			c.Normal = false
		}
	}
	e.planHysteresis(c, p.Log != 0)
	return p
}

// serialLeadWords is how many words before the hardware trigger the record
// holds for the serial trigger: software moves the trigger point back to the
// match's start, as far as it last did (at 200 us/div the record is one
// screen, and the screen's left edge would go flat). A record deeper than
// the screen keeps the screen after the trigger first (a T55xx frame
// start's lead of four 3.6 ms units at 2ms/div took the last 2 ms of the
// command off the screen; it begins 1.1 ms before).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-223
func (e *Engine) serialLeadWords(p SRAMPlan, perWord int, words, pre uint32) uint32 {
	s := math.Float64frombits(e.serLv.ShiftS.Load())
	if !(s > 0) {
		s = e.matchLeadS(p.SampleS) // none placed yet
	}
	if !(s > 0) {
		return 0
	}
	lead := min(uint32(s/p.SampleS)/uint32(perWord), words/2)                                              //nolint:gosec // G115: perWord is 1 or 2
	after := uint32(float64(p.Screen)*(1-math.Float64frombits(e.trigPosFrac.Load())))/uint32(perWord) + 64 //nolint:gosec // G115: perWord is 1 or 2
	if 4*p.Samples > 5*p.Screen {
		lead = min(lead, words-min(words, pre+after))
	}
	return lead
}

// hasSerialHardware reports a matcher in the loaded image for any protocol.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) hasSerialHardware() bool {
	return e.hardwareUART || e.hardwareI2C || e.hardwareSPI || e.hardwareSENT || e.hardwareMIL1553 || e.hardwareUSBLS || e.hardwareManchester || e.hardwarePacket != 0 || e.hardwareSymbol
}

// sramConfigInto is sramConfig filling c in place: the config is 2.7 KB, and
// the capture loop builds one every status poll.
// TRLC-LINKS: REQ-SDS-009
func (e *Engine) sramConfigInto(c *sramcapture.Config) (SRAMPlan, float64, bool, core.TrigParams) {
	e.mu.Lock()
	// An image that records raw samples only (2.1 ms) shows no slower
	// screen whole (a SENT bus's frames read 2.4 times as long as they are
	// on a record stretched across 500us/div): the timebase steps to the
	// slowest whose screen it holds. The shipping images all decimate (the
	// packet, line and fast ones by box averages); the pulse, slope and
	// video qualifier fires on decimated records too, its fire held until
	// the record takes it.
	e.stats.TdivMaxS = 0
	if e.rawOnly {
		lim := rawScreenTdiv()
		e.stats.TdivMaxS = lim
		cur := e.band.TdivS
		if e.pendSet {
			cur = e.pendBand.TdivS
		}
		if lim > 0 && cur > lim*(1+1e-6) {
			if b, ok := PlanTdiv(lim); ok {
				e.pendBand, e.pendSet = b, true
			}
		}
	}
	tdiv := e.band.TdivS
	if e.pendSet {
		tdiv = e.pendBand.TdivS
	}
	norm, tp := e.norm, e.tp
	e.mu.Unlock()
	p := PlanSRAM(tdiv)
	if e.acqMode.Load() == AcqPrecision && !e.rawOnly && !e.boxDecim {
		p = PlanPrecisionSRAM(tdiv, uint8(e.precisionLog.Load())) //nolint:gosec // G115: a precision log2 of a few (SetPrecision)
	}
	if e.rawOnly && p.Log != 0 {
		// The packet image records raw samples only: the deepest raw record.
		p = SRAMPlan{Samples: int(sramcapture.SamplesPerChannel), SampleS: 2e-9, Screen: int(sramcapture.SamplesPerChannel)}
	}
	p = e.buildSRAMConfig(c, p, tp)
	// Live frames must stay fast. A mode the envelope readout cannot serve
	// (averaging, ERES, precision) is captured decimated to a display-sized
	// record instead of reading a deep raw screen per frame; SINGLE stays deep.
	block := e.envelopeBlock()
	// SINGLE keeps the deep record, except in the noise-reducing modes: their
	// result is the filtered capture the preview shows (a block of deep records
	// took minutes, and a raw deep record is noisier than the preview).
	mode := e.acqMode.Load()
	filtered := mode == AcqAverage || mode == AcqPrecision || mode == AcqEres
	budget := liveBudget(block, float64(p.Screen)*p.SampleS, math.Float64frombits(e.busSampleS.Load()))
	if live, ok := acq.LiveSRAMPlan(p, budget); ok && (!e.singleArmed.Load() || filtered) && !e.rawOnly && liveDecimatesPlan(block, p) {
		if block == blockSearch || block == blockSamples || block == blockSpectrum || block == blockBus {
			live.Samples = min(live.Samples, max(int(leaseRecord*float64(live.Screen)), 8192))
		}
		p = e.buildSRAMConfig(c, live, tp)
	}
	// BUS's sampled records pick samples where the image can, while a serial
	// trigger reads the raw samples (as the edge and the qualifier do; the
	// zones judge the filter's words): the decode reads edges, which the
	// precision filter's band smooths away
	c.Pick = e.pickAlt && block == blockBus && c.DecimationLog2 != 0 && acq.HardwareSerialName(*c) != ""
	return p, tdiv, norm, tp
}

// averageAlignEdge is the crossing an average aligns a capture on, near
// hint (the FPGA's trigger point in local). A serial match's edge and a
// pulse, slope or video qualifier's closing edge go either way (a UART start
// bit falls; a high pulse closes falling): with nearer the crossing nearest
// the hint, else the nearest crossing of the trigger's slope. Aligned on the
// slope's own direction, a pulse trigger's captures found none in the window
// and the average never started ("average 0/64").
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-013, REQ-SDS-218
func averageAlignEdge(local []uint8, hint float64, nearer, rising bool) float64 {
	if nearer {
		return nearerCrossHint(local, core.MidLevel(local), hint)
	}
	return centerCrossHint(local, core.MidLevel(local), rising, hint)
}

// planSerial installs the hardware decoder and, beyond its native matcher,
// the sequence trigger for a serial request; fallbackNormal is the edge
// trigger left when the request cannot be planned in hardware.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) planSerial(c *sramcapture.Config, serial SerialParams, fallbackNormal bool) {
	if acq.SymbolProtocol(serial.Proto) {
		// ADR-SYMBOL-TRIGGER: the whole request compiles into the symbol
		// trigger (it has no sequence trigger: the units are its window's).
		if e.hardwareSymbol {
			c.Symbol = acq.PlanHardwareSymbol(&serial)
		}
		if !c.Symbol.Enabled {
			c.Normal = fallbackNormal
			return
		}
		c.Normal, c.TriggerChannel = true, c.Symbol.Channel
		first, second := uint8(math.Round(serial.Threshold)), acq.SecondLevel(serial)
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
	request, needSequence := serial, core.SequenceNeeded(serial) || (e.sequenceOnly && len(core.PatternOf(serial)) != 0)
	if needSequence {
		serial.Bytes, serial.Pattern = nil, nil
		if !e.hardwareSequence {
			serial.Proto = 0
		}
	}
	if e.hardwarePacket != 0 {
		c.Packet = acq.PlanHardwarePacket(&serial, e.hardwarePacket, e.arincAux.Load())
	}
	if e.hardwareManchester {
		c.Manchester = acq.PlanHardwareManchester(&serial)
	}
	if c.Packet.Enabled || c.Manchester.Enabled {
		c.Normal = true
		c.TriggerChannel = uint8(serial.ChA) //nolint:gosec // G115: the channel is 0 or 1 (serialPlanParams)
		c.TriggerLevel = uint8(math.Round(serial.Threshold))
	}
	if e.hardwareUSBLS {
		c.USBLS = acq.PlanHardwareUSBLS(&serial)
	}
	if e.hardwareMIL1553 {
		c.MIL1553 = acq.PlanHardwareMIL1553(&serial)
	}
	if e.hardwareSENT {
		c.SENT = acq.PlanHardwareSENT(&serial)
	}
	if e.hardwareUART {
		c.UART = acq.PlanHardwareUART(&serial, e.hardwareSampleUART)
	}
	if e.hardwareSPI {
		c.SPI = acq.PlanHardwareSPI(&serial, e.hardwareSampleSPI)
	}
	if e.hardwareI2C {
		c.I2C = acq.PlanHardwareI2C(&serial, e.hardwareSampleI2C)
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
		c.TriggerLevel, c.SecondLevel = uint8(math.Round(serial.Threshold)), acq.SecondLevel(serial)
	}
	if c.I2C.Enabled {
		c.Normal = true
		c.TriggerChannel = c.I2C.ClockChannel
		c.TriggerLevel, c.SecondLevel = uint8(math.Round(serial.Threshold)), acq.SecondLevel(serial)
	}
	if needSequence && acq.HardwareSerialName(*c) != "" {
		if c.Sequence = acq.PlanSequence(request); c.Sequence.Length == 0 {
			// Unplannable in hardware: back to software qualification.
			c.Packet, c.Manchester, c.USBLS, c.MIL1553, c.SENT = sramcapture.PacketTriggerConfig{}, sramcapture.ManchesterTriggerConfig{}, sramcapture.USBLSTriggerConfig{}, sramcapture.MIL1553TriggerConfig{}, sramcapture.SENTTriggerConfig{}
			c.UART, c.SPI, c.I2C = sramcapture.UARTTriggerConfig{}, sramcapture.SPITriggerConfig{}, sramcapture.I2CTriggerConfig{}
			c.Normal = fallbackNormal
		}
	}
}

// planHysteresis sets the fabric trigger's re-arm band from the converter
// offsets of the trigger channel (and the second line of I2C/SPI).
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) planHysteresis(c *sramcapture.Config, _ bool) {
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
	c.TriggerHysteresis = 4
	cal := e.interleaveCalibration
	if cal == nil {
		return
	}
	spread := func(ch int) float64 {
		offsets := cal.Offset[ch&1]
		if len(offsets) == 0 {
			return 0
		}
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
	c.TriggerHysteresis = uint8(math.Min(255, math.Max(4, math.Ceil(band)+2)))
	if c.SPI.Enabled && c.SPI.GapTicks <= acq.SampleSPIFastGap && c.TriggerHysteresis > acq.SampleSPIFastBand {
		// SPI on the samples at 25 MHz and up: its edges take a few ns, too
		// fast for a converter's offset to cross the level back on them, and
		// its sampled swing shrinks (104 MHz at 1 V/div with 10x probes dipped
		// to 116 of the 115 the offsets' band asked, and a clock edge was lost)
		c.TriggerHysteresis = acq.SampleSPIFastBand
	}
}

// decodedIdlePause is the wait between the owner loop's passes while a
// decoded stream's worker builds its lines: a reader's request waits at most
// this long.
const decodedIdlePause = time.Millisecond

// decodedIdle is the owner loop's pass while a decoded capture runs: the
// events are pumped elsewhere, so it waits on the FIFO service, or a little
// between passes when the worker builds the lines (a pass with nothing to
// do spun a whole core).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func (e *Engine) decodedIdle() {
	if e.decodedError != nil {
		e.clk.Sleep(20 * time.Millisecond)
	}
	if e.decodedDeadline != nil {
		stage := time.Now()
		if err := e.decodedDeadline.Pause(); err != nil {
			e.decodedError = err
		}
		e.decodedService.maxPause = max(e.decodedService.maxPause, time.Since(stage))
	} else if e.decodedLines {
		e.clk.Sleep(decodedIdlePause)
	}
}

// noteBackendsLocked reports what serves the serial trigger, the trigger
// qualifier and the zones of capture cfg (e.mu held).
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-013, REQ-SDS-014
func (e *Engine) noteBackendsLocked(cfg sramcapture.Config, tp core.TrigParams) {
	e.stats.SerialBackend = ""
	if name := acq.HardwareSerialName(cfg); name != "" {
		e.stats.SerialBackend = "hardware-" + name
	} else if e.serialMode.Load() == SerialTrigger {
		e.stats.SerialBackend = "none: " + e.serialNoHardware()
	}
	e.stats.TriggerBackend = ""
	if cfg.Qualifier.Enabled {
		e.stats.TriggerBackend = "hardware-" + acq.QualName(cfg.Qualifier.Kind)
	} else if tp.Typ != TrigEdge && e.serialMode.Load() != SerialTrigger {
		_, _, why := e.planQualifier(tp, int(cfg.TriggerChannel), e.trigRising.Load())
		e.stats.TriggerBackend = "none: " + why
	}
	e.stats.ZoneBackend = ""
	switch {
	case cfg.Zones.Enabled():
		e.stats.ZoneBackend = "hardware"
	case e.zonesOn() && e.serialMode.Load() != SerialTrigger:
		why := "the trigger has no hardware yet"
		if e.stats.TriggerBackend == "" {
			_, _, why = e.planZones()
		}
		e.stats.ZoneBackend = "none: " + why
	case e.zonesOn():
		e.stats.ZoneBackend = "none: zones qualify an edge, pulse, slope or video trigger, not a serial one"
	}
}

// frameConditioner is the conditioning's state carried between frames.
type frameConditioner struct {
	scratch           []uint16
	responseReduction uint32  // the decimation responseGain and responseBW are for
	responseGain      float64 // the precision filter's noise gain
	responseBW        float64 // and bandwidth, of the output rate
}

// conditionFrame conditions recalled frame f of record m, in place: the
// decimated record's filter (box average, picked samples or the precision
// filter), the envelope's or the full-rate record's converter calibration,
// and ERES. q1 and q2 are the frame's Q8.8 buffers, returned grown.
// TRLC-LINKS: REQ-SDS-012, REQ-SDS-040
func (e *Engine) conditionFrame(f *sample.Frame, m sramcapture.Metadata, cfg sramcapture.Config, bucket uint32, q1, q2 []uint16, savedScale [2]core.ChScale, c *frameConditioner) ([]uint16, []uint16) {
	if len(f.Precision(0)) == f.Len() && len(f.Precision(1)) == f.Len() && bucket == 0 && (e.boxDecim || cfg.Pick) && m.Decimation > 1 {
		// Box averages: no CIC droop to compensate; a sinc's -3 dB
		// at 0.443 of the output rate. Picked samples: no filter.
		f.Cond.PassbandHz, f.Cond.BandwidthHz = .2/f.SampleS, .443/f.SampleS
		f.Cond.Filter = "box average"
		if e.pickDecim || cfg.Pick {
			f.Cond.PassbandHz, f.Cond.BandwidthHz = .5/f.SampleS, .5/f.SampleS
			f.Cond.Filter = "every 2^n-th sample"
		}
		dsp.RoundQ8(f.Codes8(0)[:f.Len()], f.Precision(0)[:f.Len()])
		dsp.RoundQ8(f.Codes8(1)[:f.Len()], f.Precision(1)[:f.Len()])
	} else if len(f.Precision(0)) == f.Len() && len(f.Precision(1)) == f.Len() && bucket == 0 {
		c.scratch = core.QBuffer(c.scratch, f.Len())
		dsp.ConditionQ8(f.Precision(0), c.scratch)
		dsp.ConditionQ8(f.Precision(1), c.scratch)
		f.Cond.Guard = dsp.PrecisionGuard
		if c.responseReduction != m.Decimation {
			c.responseGain, c.responseBW = dsp.PrecisionLimits(int(m.Decimation))
			c.responseReduction = m.Decimation
		}
		f.Cond.NoiseGain = c.responseGain
		f.Cond.PassbandHz = .075 / f.SampleS
		f.Cond.BandwidthHz = c.responseBW / f.SampleS
		f.Cond.Filter = "CIC3 + compensated FIR"
		dsp.RoundQ8(f.Codes8(0)[:f.Len()], f.Precision(0)[:f.Len()])
		dsp.RoundQ8(f.Codes8(1)[:f.Len()], f.Precision(1)[:f.Len()])
	}
	if bucket != 0 {
		e.calibrateEnvelope(f, m, bucket, q1, q2)
	} else if m.FractionBits == 0 && e.interleaveCalibration != nil {
		q1, q2 = e.calibrateRecord(f, q1, q2, savedScale)
	}
	if length := core.ClampEresLen(int(e.eresLen.Load())); e.acqMode.Load() == AcqEres && length > 1 {
		core.Eres(f, length, &c.scratch, q1, q2)
	}
	return q1, q2
}

// calibrateEnvelope readies an envelope frame f of bucket words a bucket:
// extremes of whole buckets, so no per-core calibration or FIR applies, only
// the converters' offsets on a full-rate record (a decimated one is
// filtered across the converters already).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func (e *Engine) calibrateEnvelope(f *sample.Frame, m sramcapture.Metadata, bucket uint32, q1, q2 []uint16) {
	if len(f.Precision(0)) == f.Len() && len(f.Precision(1)) == f.Len() {
		dsp.RoundQ8(f.Codes8(0)[:f.Len()], f.Precision(0)[:f.Len()])
		dsp.RoundQ8(f.Codes8(1)[:f.Len()], f.Precision(1)[:f.Len()])
	}
	f.Cond.Filter = fmt.Sprintf("peak-detect display readout, %d words per bucket; full record in SRAM", bucket)
	f.Content = sample.PeakPairs
	if cal := e.interleaveCalibration; cal != nil && m.Decimation <= 1 {
		f.Traces[0].Precision, f.Traces[1].Precision = core.QBuffer(q1, f.Len()), core.QBuffer(q2, f.Len())
		cal.debiasEnvelope(0, f.Codes8(0)[:f.Len()], f.Precision(0), len(f.Precision(0)) == f.Len() && m.FractionBits == 8)
		cal.debiasEnvelope(1, f.Codes8(1)[:f.Len()], f.Precision(1), len(f.Precision(1)) == f.Len() && m.FractionBits == 8)
		f.Cond.Filter += "; converter offsets removed from the extremes"
	}
}

// calibrateRecord applies the converters' interleave calibration, the
// capture's detent's, to full-rate record frame f into q1 and q2 (returned
// grown).
// TRLC-LINKS: REQ-SDS-040
func (e *Engine) calibrateRecord(f *sample.Frame, q1, q2 []uint16, savedScale [2]core.ChScale) ([]uint16, []uint16) {
	q1 = core.QBuffer(q1, f.Len())
	q2 = core.QBuffer(q2, f.Len())
	scale := [2]float64{savedScale[0].Vdiv, savedScale[1].Vdiv}
	if !e.interleaveCalibration.apply(f.Codes8(0)[:f.Len()], f.Codes8(1)[:f.Len()], q1, q2, scale, f.SampleS) {
		f.Cond.Filter = "interleave calibration bypassed (scale, clipping or phase mismatch)"
		return q1, q2
	}
	f.Traces[0].Precision = q1
	f.Traces[1].Precision = q2
	f.Cond.Filter = "interleave calibration (phase checked)"
	if e.interleaveCalibration.hasTiming(scale, f.SampleS) {
		f.Cond.Filter += "; aperture compensation"
		f.Cond.Guard = 2
	}
	return q1, q2
}

// readEdgeWindows reads envelope frame f's trigger edge and the next edge
// the other way at record m's rate: a live envelope holds no edge shape,
// nor a mean where the signal is faster than a bucket of bucket words.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-040
func (e *Engine) readEdgeWindows(f *sample.Frame, m sramcapture.Metadata, cfg sramcapture.Config, tp core.TrigParams, recallOffset, bucket uint32, savedScale [2]core.ChScale) (edge, opp *sample.Frame) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	disc := f.Codes8(0)[:f.Len()]
	if cfg.TriggerChannel == 1 {
		disc = f.Codes8(1)[:f.Len()]
	}
	at, trig, rising := m.TriggerIndex, int(f.TriggerAt)/2, !cfg.Falling
	if tp.Typ != TrigEdge || e.serialMode.Load() == SerialTrigger {
		// the trigger point is no edge of the edge trigger's direction
		if b, r := core.NearestEdgeBucket(disc, trig); b >= 0 {
			at, trig, rising = recallOffset+uint32(b)*bucket+bucket/2, b, r //nolint:gosec // G115: b is a bucket index, non-negative here
		}
	}
	edge = e.recallEdgeWindow(ctx, m, at, savedScale)
	if b := core.OppositeEdgeBucket(disc, trig, rising); b >= 0 {
		opp = e.recallEdgeWindow(ctx, m, recallOffset+uint32(b)*bucket+bucket/2, savedScale) //nolint:gosec // G115: b is a bucket index, non-negative here
	}
	return edge, opp
}

// refineEdgeTrigger places frame f's edge trigger on its exact crossing of
// the trigger level in disc, the trigger channel's codes; false when the
// record has no such crossing there. An unset level leaves the trigger.
// TRLC-LINKS: REQ-SDS-009, REQ-SDS-011
func (e *Engine) refineEdgeTrigger(f *sample.Frame, disc []uint8, cfg sramcapture.Config) bool {
	lvl := e.trigDispLevel(int(cfg.TriggerChannel))
	if lvl < 0 {
		return true
	}
	q := f.Precision(0)
	if cfg.TriggerChannel == 1 {
		q = f.Precision(1)
	}
	return e.placeEdgeTrigger(f, disc, q, float64(lvl), !cfg.Falling)
}

// serialTriggerKind names frame f's trigger when a decoded stream's record
// or the serial trigger decided it, and places the serial trigger's point
// at the match's start. It returns whether f is qualified.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-223
func (e *Engine) serialTriggerKind(f *sample.Frame, m sramcapture.Metadata, cfg sramcapture.Config, qualified bool) bool {
	if m.HostTrigger {
		// A history ring stopped on a decoded stream's frame (ADR-STREAM-RING):
		// for streaming data software is the trigger, and the trigger word is
		// the frame's start, placed through the ring's time stamp.
		f.TrigKind, f.TrigNode = sample.StreamStop, ""
		return qualified
	}
	if e.serialMode.Load() != SerialTrigger {
		return qualified
	}
	name := acq.HardwareSerialName(cfg)
	if name == "" {
		// No matcher could take the request (its levels are still being
		// learned from these untriggered captures, or the image has none
		// for it): nothing triggers. Software never decides a trigger.
		f.Status.Triggered = false
		f.TrigKind, f.TrigNode = sample.Untriggered, ""
		f.TriggerAt = -1
		return false
	}
	// The FPGA's matcher decided; software only moves the trigger point
	// from where the match ended to where it began.
	f.TrigKind, f.TrigNode = sample.HardwareNode, name
	if !f.Status.Triggered {
		return false
	}
	e.serialMatches.Add(1)
	if cfg.SPI.Enabled && e.clk.Now != nil {
		e.serLv.SPIQuiet.Store(e.clk.Now().UnixNano())
	}
	hw := f.TriggerAt
	if at := e.serialMatchStart(f, f.TriggerAt); at >= 0 {
		e.serLv.ShiftS.Store(math.Float64bits((f.TriggerAt - at) * f.SampleS))
		f.TriggerAt = at
	}
	if name == "uart" && hw >= 0 && f.TriggerAt >= 0 {
		// the matched frame's stop bit: the receiver on the samples
		// publishes its match a fixed time after it, longer than a
		// stop bit at its rates
		end := hw
		if cfg.UART.Period16 != 0 && f.SampleS > 0 {
			end = math.Max(f.TriggerAt, hw-acq.SampleUARTMatchLagS/f.SampleS)
		}
		f.Match = &sample.Match{EndS: (end - f.TriggerAt) * f.SampleS}
	}
	return true
}

// averageFrame adds frame f, aligned on its observed trigger crossing in
// disc, to the average ring (restarted when the settings change) and leaves
// the average in f; true once the ring holds the count asked.
// TRLC-LINKS: REQ-SDS-012
func (e *Engine) averageFrame(f *sample.Frame, avg *core.SramAverage, cfg sramcapture.Config, tdiv float64, norm bool, tp core.TrigParams, disc []uint8, q1, q2 []uint16) bool {
	n := int(e.avgCount.Load())
	key := fmt.Sprintf("%v/%g/%t/%v/%d/%d/%d/%d/%d/%s", averageCfg(cfg), tdiv, norm, tp, e.avgGen.Load(), e.chVdivBits[0].Load(), e.chVdivBits[1].Load(), e.trigOffV[0].Load(), e.trigOffV[1].Load(), f.Cond.Filter)
	// Refine the word-level trigger with the observed waveform crossing.
	lo, hi := int(f.TriggerAt)-4096, int(f.TriggerAt)+4096
	if lo < 0 {
		lo = 0
	}
	if hi > f.Len() {
		hi = f.Len()
	}
	edge := -1.0
	if tp.Typ == TrigEdge && e.serialMode.Load() != SerialTrigger && f.TriggerAt >= 0 {
		// Already the exact trigger-level crossing (refineTriggerEdge).
		edge = f.TriggerAt - float64(lo)
	} else if hi > lo {
		edge = averageAlignEdge(disc[lo:hi], f.TriggerAt-float64(lo), e.serialMode.Load() == SerialTrigger || tp.Typ != TrigEdge, !cfg.Falling)
	}
	if edge >= 0 {
		f.TriggerAt = edge + float64(lo)
	}
	if key != avg.Key {
		avg.Reset()
		avg.Key = key
	}
	core.EnsurePrecision(f, q1, q2)
	count := 0
	if edge >= 0 {
		count = avg.Push(f, n)
	} else {
		avg.Reset()
	}
	f.Cond.Filter += fmt.Sprintf("; average %d/%d (waveform aligned)", count, n)
	if count > 0 {
		if f.Cond.NoiseGain == 0 {
			f.Cond.NoiseGain = 1
		}
		f.Cond.NoiseGain *= math.Sqrt(float64(count))
	}
	return count >= n
}

// liveReadoutName names how a live frame is read for the status: why the
// envelope readout cannot serve it ("" when it can), the envelope's bucket
// (0: the window was short enough to read raw), and a SINGLE's capture.
// TRLC-LINKS: REQ-SDS-010
func liveReadoutName(why string, bucket uint32, single bool) string {
	readout := "raw window: " + why
	switch {
	case why != "":
	case bucket != 0:
		readout = "envelope"
	default:
		readout = "raw window: short"
	}
	if single {
		readout = "single, " + readout
	}
	return readout
}

// recallEnvelope reads frozen record m's screen window as an envelope of
// count buckets of bucket words into w, with the views that show part of
// the screen magnified at full detail in the same request (the screen's
// envelope spreads its buckets over all of it). A view's failed read must
// not cost the frame: the screen is then read alone.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func (e *Engine) recallEnvelope(ctx context.Context, f *sample.Frame, m sramcapture.Metadata, screen int, offset, words, bucket, count uint32) (viewReads, int, error) {
	views := e.planViews(f, m, screen, offset, words, bucket, count)
	e.hw().SetRecord(m)
	screenWin := acq.Window(m, sample.Main, offset, bucket*count, bucket)
	ws, fs := views.windows(m, append(e.readWins[:0], screenWin), append(e.readFrames[:0], f))
	e.readWins, e.readFrames = ws, fs
	err := e.reader().Read(ctx, hal.ReadRequest{Windows: ws}, fs, e.yield())
	if err != nil && views != (viewReads{}) && !errors.Is(err, acq.ErrShortRead) {
		views = viewReads{}
		err = e.reader().Read(ctx, hal.ReadRequest{Windows: ws[:1]}, fs[:1], e.yield())
	} else if err == nil {
		views.got(fs[1:])
	}
	n := 0
	if err == nil {
		n = f.Len()
	}
	rejects, last := e.hw().EnvelopeDiagnostics()
	e.mu.Lock()
	e.stats.EnvelopeRejects, e.stats.EnvelopeLast = rejects, last
	e.mu.Unlock()
	return views, n, err
}

// liveScale is the front end's scale now.
// TRLC-LINKS: REQ-SDS-040
func (e *Engine) liveScale() [2]core.ChScale {
	var s [2]core.ChScale
	for ch := range s {
		s[ch] = core.ChScale{Vdiv: math.Float64frombits(e.chVdivBits[ch].Load()), Off: math.Float64frombits(e.trigOffV[ch].Load())}
	}
	return s
}

// symbolCount reads the symbol trigger's match count (modulo 2^16), or -1
// without the block.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) symbolCount(cfg sramcapture.Config) int {
	if !cfg.Symbol.Enabled {
		return -1
	}
	st, ok := e.hw().Trigger().Matches()
	if !ok {
		return -1
	}
	return int(st.Matches & 0xffff)
}

// noteSymbolMatches publishes the symbol trigger's matches during the record
// just frozen and how long it was armed (it counts only while armed): their
// ratio is the bus's rate of matches.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) noteSymbolMatches(cfg sramcapture.Config, armed int, took time.Duration) {
	done := e.symbolCount(cfg)
	n := int64(-1)
	if armed >= 0 && done >= 0 {
		n = int64((done - armed) & 0xffff)
	}
	e.mu.Lock()
	e.stats.SymbolMatches, e.stats.SymbolArmedS = n, took.Seconds()
	e.mu.Unlock()
}
