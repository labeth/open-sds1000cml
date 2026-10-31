// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"open-sds/app/internal/dsp"
	"open-sds/app/internal/sramcapture"
	"slices"
	"time"
)

// SRAMPlan retains the complete physical record. Reduction is the smallest
// supported power of two that fits a screen into that record, leaving the ARM
// all remaining samples for measurements and display reduction.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
type SRAMPlan struct {
	Log             uint8
	Samples, Screen int
	SampleS         float64
}

// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040

// FullRateTdiv is the slowest of tdivs whose SRAM record is sampled at the
// fastest rate (0 for none): where BUS detection looks again when a record
// hid the bus (decimated, or too short).
// TRLC-LINKS: REQ-SDS-214
func FullRateTdiv(tdivs []float64) float64 {
	if len(tdivs) == 0 {
		return 0
	}
	full, best := PlanSRAM(slices.Min(tdivs)).SampleS, 0.0
	for _, t := range tdivs {
		if PlanSRAM(t).SampleS <= full && t > best {
			best = t
		}
	}
	return best
}

// TRLC-LINKS: REQ-SDS-214
func PlanSRAM(tdiv float64) SRAMPlan {
	p := SRAMPlan{Samples: int(sramcapture.SamplesPerChannel), SampleS: 2e-9}
	if !(tdiv > 0) || math.IsInf(tdiv, 0) {
		tdiv = 500e-6
	}
	if 10*tdiv > float64(p.Samples)*p.SampleS {
		p.Log = 4
		p.Samples = int(sramcapture.Words)
		p.SampleS = 32e-9
		for p.Log < 20 && 10*tdiv > float64(p.Samples)*p.SampleS {
			p.Log++
			p.SampleS *= 2
		}
	}
	p.Screen = int(math.Min(float64(p.Samples), math.Max(2, math.Round(10*tdiv/p.SampleS))))
	return p
}

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

// liveSRAMPlan decimates p until its screen fits budget samples (or the
// deepest decimation), with a record of a few screens; ok is false when p's
// screen already reads in budget words.
// TRLC-LINKS: REQ-SDS-010
func liveSRAMPlan(p SRAMPlan, budget int) (SRAMPlan, bool) {
	// A raw screen packs two samples a word: what bounds the readout is words.
	if words := p.Screen / (2 - boolInt(p.Log != 0)); words <= budget {
		return p, false
	}
	span := float64(p.Screen) * p.SampleS
	if p.Log < 4 {
		p.Log, p.SampleS = 4, 32e-9 // the fabric's first decimated rate
	}
	for p.Log < 20 && span/p.SampleS > float64(budget) {
		p.Log++
		p.SampleS *= 2
	}
	p.Screen = int(math.Max(2, math.Round(span/p.SampleS)))
	p.Samples = min(int(sramcapture.Words), max(4*p.Screen, 8192))
	return p, true
}

// PlanPrecisionSRAM keeps the selected sample rate fixed as the view changes.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-036
func PlanPrecisionSRAM(tdiv float64, log uint8) SRAMPlan {
	if !(tdiv > 0) || math.IsInf(tdiv, 0) {
		tdiv = 500e-6
	}
	if log < 4 {
		log = 4
	}
	if log > 20 {
		log = 20
	}
	p := SRAMPlan{Log: log, Samples: int(sramcapture.Words), SampleS: float64(uint64(1)<<log) / 500e6}
	p.Screen = int(math.Min(float64(p.Samples), math.Max(2, math.Round(10*tdiv/p.SampleS))))
	return p
}

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

// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
type sramFrameWriter struct {
	f *Frame
	m sramcapture.Metadata
	n int
}

// TRLC-LINKS: REQ-SDS-040
func (w *sramFrameWriter) Write(b []byte) (int, error) {
	if len(b)%4 != 0 {
		return 0, fmt.Errorf("unaligned SRAM payload")
	}
	samples := len(b) / 4 * int(w.m.SamplesPerWord)
	if w.n+samples > w.f.Valid {
		return 0, fmt.Errorf("SRAM payload exceeds frame")
	}
	if !(w.m.FractionBits == 8 && w.m.SamplesPerWord == 1 || w.m.FractionBits != 8 && w.m.SamplesPerWord == 2) {
		return w.writeWords(b) // a layout other than the capture's two
	}
	// the word format chosen once; the destinations sliced to the samples
	n, f := w.n, w.f
	c1, c2 := f.C1[n:n+samples], f.C2[n:n+samples]
	if w.m.FractionBits == 8 {
		q1, q2 := f.Q1[n:n+samples], f.Q2[n:n+samples]
		for k := range c1 {
			a, c := binary.LittleEndian.Uint16(b[4*k:]), binary.LittleEndian.Uint16(b[4*k+2:])
			q1[k], q2[k] = a, c
			c1[k], c2[k] = roundQ8(a), roundQ8(c)
		}
	} else {
		for k := 0; k+1 < len(c1); k += 2 {
			i := 2 * k
			c1[k], c2[k] = b[i], b[i+1]
			c1[k+1], c2[k+1] = b[i+2], b[i+3]
		}
	}
	w.n += samples
	return len(b), nil
}

// writeWords is Write word by word, for any samples-a-word count.
// TRLC-LINKS: REQ-SDS-040
func (w *sramFrameWriter) writeWords(b []byte) (int, error) {
	for i := 0; i < len(b); i += 4 {
		if w.m.FractionBits == 8 {
			a, c := binary.LittleEndian.Uint16(b[i:]), binary.LittleEndian.Uint16(b[i+2:])
			w.f.Q1[w.n], w.f.Q2[w.n] = a, c
			w.f.C1[w.n], w.f.C2[w.n] = roundQ8(a), roundQ8(c)
			w.n++
		} else {
			w.f.C1[w.n], w.f.C2[w.n] = b[i], b[i+1]
			w.n++
			w.f.C1[w.n], w.f.C2[w.n] = b[i+2], b[i+3]
			w.n++
		}
	}
	return len(b), nil
}

// TRLC-LINKS: REQ-SDS-040
func roundQ8(x uint16) uint8 {
	v := (uint32(x) + 128) >> 8
	if v > 255 {
		v = 255
	}
	return uint8(v)
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
func (h *heldRecall) store(k heldKey, f *Frame) {
	h.key, h.n = k, f.Valid
	h.c1 = append(h.c1[:0], f.C1[:f.Valid]...)
	h.c2 = append(h.c2[:0], f.C2[:f.Valid]...)
	h.q1, h.q2 = h.q1[:0], h.q2[:0]
	if len(f.Q1) == f.Valid && len(f.Q2) == f.Valid {
		h.q1 = append(h.q1, f.Q1...)
		h.q2 = append(h.q2, f.Q2...)
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
func (h *heldRecall) storeCond(k heldCondKey, f *Frame) {
	c := &h.cond
	c.key = k
	c.c1 = append(c.c1[:0], f.C1[:f.Valid]...)
	c.c2 = append(c.c2[:0], f.C2[:f.Valid]...)
	c.q1, c.q2 = c.q1[:0], c.q2[:0]
	if len(f.Q1) == f.Valid && len(f.Q2) == f.Valid {
		c.q1 = append(c.q1, f.Q1...)
		c.q2 = append(c.q2, f.Q2...)
	}
	c.filter, c.guard = f.Filter, f.FilterGuard
	c.noiseGain, c.passband, c.bandwidth = f.NoiseGainIdeal, f.PassbandHz, f.BandwidthHz
}

// restoreCond puts the conditioned record into f (already sized by restore),
// its Q codes into the frame's backing arrays q1, q2.
// TRLC-LINKS: REQ-SDS-033
func (h *heldRecall) restoreCond(f *Frame, q1, q2 []uint16) {
	c := &h.cond
	copy(f.C1, c.c1)
	copy(f.C2, c.c2)
	if n := len(c.q1); n == f.Valid && n == len(c.q2) {
		f.Q1, f.Q2 = qBuffer(q1, n), qBuffer(q2, n)
		copy(f.Q1, c.q1)
		copy(f.Q2, c.q2)
	}
	f.Filter, f.FilterGuard = c.filter, c.guard
	f.NoiseGainIdeal, f.PassbandHz, f.BandwidthHz = c.noiseGain, c.passband, c.bandwidth
}

// restore copies the held codes into f (sized as at store) and returns the
// sample count, as the recall writer would.
// TRLC-LINKS: REQ-SDS-033
func (h *heldRecall) restore(f *Frame) int {
	copy(f.C1, h.c1)
	copy(f.C2, h.c2)
	if len(h.q1) == h.n && len(f.Q1) == h.n && len(f.Q2) == h.n {
		copy(f.Q1, h.q1)
		copy(f.Q2, h.q2)
	}
	return h.n
}

// qBuffer is q sized to n, reused when it holds n; a buffer four times
// larger than needed (a deep stopped record's, under live frames again) is
// let go: the frame slots kept a 1 M-sample record's Q8.8 codes for good,
// and with the other held copies the heap sat at the memory limit.
// TRLC-LINKS: REQ-SDS-040
func qBuffer(q []uint16, n int) []uint16 {
	if cap(q) < n || cap(q) >= 1<<18 && cap(q) > 4*n {
		return make([]uint16, n)
	}
	return q[:n]
}

// TRLC-LINKS: REQ-SDS-010, REQ-SDS-011, REQ-SDS-036, REQ-SDS-040
func (e *Engine) sramConfig() (sramcapture.Config, SRAMPlan, float64, bool, trigParams) {
	var c sramcapture.Config
	p, tdiv, norm, tp := e.sramConfigInto(&c)
	return c, p, tdiv, norm, tp
}

// sramConfigInto is sramConfig filling c in place: the config is 2.7 KB, and
// the capture loop builds one every status poll.
// TRLC-LINKS: REQ-SDS-009
func (e *Engine) sramConfigInto(c *sramcapture.Config) (SRAMPlan, float64, bool, trigParams) {
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
		p = PlanPrecisionSRAM(tdiv, uint8(e.precisionLog.Load()))
	}
	if e.rawOnly && p.Log != 0 {
		// The packet image records raw samples only: the deepest raw record.
		p = SRAMPlan{Samples: int(sramcapture.SamplesPerChannel), SampleS: 2e-9, Screen: int(sramcapture.SamplesPerChannel)}
	}
	build := func(p SRAMPlan) SRAMPlan {
		samples := p.Samples
		perWord := 2 - boolInt(p.Log != 0)
		words := uint32((samples + perWord - 1) / perWord)
		p.Samples = int(words) * perWord
		pre := uint32(float64(words) * math.Float64frombits(e.trigPosFrac.Load()))
		if e.serialMode.Load() == SerialTrigger && p.SampleS > 0 {
			// Software moves the trigger point back to the match's start, as
			// far as it last did: the record holds that before the hardware
			// trigger too (at 200 us/div the record is one screen, and the
			// screen's left edge would go flat). A record deeper than the
			// screen keeps the screen after the trigger first (a T55xx frame
			// start's lead of four 3.6 ms units at 2ms/div took the last 2 ms
			// of the command off the screen; it begins 1.1 ms before).
			s := math.Float64frombits(e.serLv.shiftS.Load())
			if !(s > 0) {
				s = e.matchLeadS(p.SampleS) // none placed yet
			}
			if s > 0 {
				lead := min(uint32(s/p.SampleS)/uint32(perWord), words/2)
				after := uint32(float64(p.Screen)*(1-math.Float64frombits(e.trigPosFrac.Load())))/uint32(perWord) + 64
				if 4*p.Samples > 5*p.Screen {
					lead = min(lead, words-min(words, pre+after))
				}
				pre += lead
			}
		}
		if pre >= words {
			pre = words - 1
		}
		*c = sramcapture.Config{Source: sramcapture.ADC, DecimationLog2: p.Log, PreWords: pre, PostWords: words - pre,
			Normal: tp.typ == TrigEdge && e.serialMode.Load() != SerialTrigger, Falling: !e.trigRising.Load(),
			TriggerChannel: uint8(e.trigSrc.Load()), TriggerLevel: uint8(e.trigLevelWord())}
		if (e.hardwareUART || e.hardwareI2C || e.hardwareSPI || e.hardwareSENT || e.hardwareMIL1553 || e.hardwareUSBLS || e.hardwareManchester || e.hardwarePacket != 0 || e.hardwareSymbol) && e.serialMode.Load() == SerialTrigger {
			e.ser.mu.Lock()
			serial := e.ser.params
			e.ser.mu.Unlock()
			e.spiQuietCheck(serial)
			// The matcher slices at the learned level when the request names none.
			if serial, why := e.serialPlanParams(serial); why != arincOneWord {
				e.planSerial(c, serial, tp.typ == TrigEdge && e.serialMode.Load() != SerialTrigger)
			}
		}
		if tp.typ != TrigEdge && e.serialMode.Load() != SerialTrigger {
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
					c.PreWords = min(c.PreWords+uint32(delayS/p.SampleS)/uint32(perWord), words-1)
					c.PostWords = words - c.PreWords
				}
			} else {
				c.Normal = false
			}
		}
		e.planHysteresis(c, p.Log != 0)
		return p
	}
	p = build(p)
	// Live frames must stay fast. A mode the envelope readout cannot serve
	// (averaging, ERES, precision) is captured decimated to a display-sized
	// record instead of reading a deep raw screen per frame; SINGLE stays deep.
	block := e.envelopeBlock(c, tp)
	// SINGLE keeps the deep record, except in the noise-reducing modes: their
	// result is the filtered capture the preview shows (a block of deep records
	// took minutes, and a raw deep record is noisier than the preview).
	acq := e.acqMode.Load()
	filtered := acq == AcqAverage || acq == AcqPrecision || acq == AcqEres
	budget := liveBudget(block, float64(p.Screen)*p.SampleS, math.Float64frombits(e.busSampleS.Load()))
	if live, ok := liveSRAMPlan(p, budget); ok && (!e.singleArmed.Load() || filtered) && !e.rawOnly && liveDecimatesPlan(block, p) {
		if block == blockSearch || block == blockSamples || block == blockSpectrum || block == blockBus {
			live.Samples = min(live.Samples, max(int(leaseRecord*float64(live.Screen)), 8192))
		}
		p = build(live)
	}
	// BUS's sampled records pick samples where the image can, while a serial
	// trigger reads the raw samples (as the edge and the qualifier do; the
	// zones judge the filter's words): the decode reads edges, which the
	// precision filter's band smooths away
	c.Pick = e.pickAlt && block == blockBus && c.DecimationLog2 != 0 && hardwareSerialName(*c) != ""
	return p, tdiv, norm, tp
}

// autoForceDue reports AUTO's wait for a trigger over: running AUTO (or a
// stopped scope's SINGLE served at once), waited past autoWait, and the
// hardware not triggered - a triggered record only fills its post-trigger
// part, which at slow timebases outlasts the wait (10ms/div: 135 ms).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-011
func autoForceDue(norm, running, triggered bool, waited, autoWait time.Duration) bool {
	return (!norm || !running) && !triggered && waited >= autoWait
}

// statusPollWait is the wait between the status polls of a capture waiting
// for its event, waited since its arm: 1 ms while a fast record and its
// trigger are still due, longer for one that goes on waiting (a slow or
// NORMAL trigger, a serial match). Each poll is a bus read on the worker;
// a capture past 10 ms is seen at most 2 ms later.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-011
func statusPollWait(waited time.Duration) time.Duration {
	if waited < 10*time.Millisecond {
		return time.Millisecond
	}
	return 2 * time.Millisecond
}

// settingsPollEvery is how often a capture waiting for its event looks for
// changed settings (a fresh capture with them): building and comparing the
// whole configuration on every status poll was a third of the wait's work.
const settingsPollEvery = 4 * time.Millisecond

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
		return nearerCrossHint(local, midLevel(local), hint)
	}
	return centerCrossHint(local, midLevel(local), rising, hint)
}

// planSerial installs the hardware decoder and, beyond its native matcher,
// the sequence trigger for a serial request; fallbackNormal is the edge
// trigger left when the request cannot be planned in hardware.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) planSerial(c *sramcapture.Config, serial SerialParams, fallbackNormal bool) {
	if symbolProtocol(serial.Proto) {
		// ADR-SYMBOL-TRIGGER: the whole request compiles into the symbol
		// trigger (it has no sequence trigger: the units are its window's).
		if e.hardwareSymbol {
			c.Symbol = planHardwareSymbol(&serial)
		}
		if !c.Symbol.Enabled {
			c.Normal = fallbackNormal
			return
		}
		c.Normal, c.TriggerChannel = true, c.Symbol.Channel
		first, second := uint8(math.Round(serial.Threshold)), secondLevel(serial)
		if int(c.Symbol.Channel) != serial.ChA {
			first, second = second, first
		}
		c.TriggerLevel = first
		if c.Symbol.TwoLines {
			c.SecondLevel = second
		}
		return
	}
	if serial.Proto == serMIDI {
		// MIDI is UART characters, 8N1, on the UART receiver.
		serial.Proto, serial.Bits, serial.Parity = serUART, 8, "none"
	}
	// A request beyond the protocol's native matcher runs the decoder
	// unfiltered and matches with the sequence trigger; without that
	// trigger it stays a software qualification.
	request, needSequence := serial, sequenceNeeded(serial) || (e.sequenceOnly && len(patternOf(serial)) != 0)
	if needSequence {
		serial.Bytes, serial.Pattern = nil, nil
		if !e.hardwareSequence {
			serial.Proto = 0
		}
	}
	if e.hardwarePacket != 0 {
		c.Packet = planHardwarePacket(&serial, e.hardwarePacket, e.arincAux.Load())
	}
	if e.hardwareManchester {
		c.Manchester = planHardwareManchester(&serial)
	}
	if c.Packet.Enabled || c.Manchester.Enabled {
		c.Normal = true
		c.TriggerChannel = uint8(serial.ChA)
		c.TriggerLevel = uint8(math.Round(serial.Threshold))
	}
	if e.hardwareUSBLS {
		c.USBLS = planHardwareUSBLS(&serial)
	}
	if e.hardwareMIL1553 {
		c.MIL1553 = planHardwareMIL1553(&serial)
	}
	if e.hardwareSENT {
		c.SENT = planHardwareSENT(&serial)
	}
	if e.hardwareUART {
		c.UART = planHardwareUART(&serial, e.hardwareSampleUART)
	}
	if e.hardwareSPI {
		c.SPI = planHardwareSPI(&serial, e.hardwareSampleSPI)
	}
	if e.hardwareI2C {
		c.I2C = planHardwareI2C(&serial, e.hardwareSampleI2C)
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
		c.TriggerLevel, c.SecondLevel = uint8(math.Round(serial.Threshold)), secondLevel(serial)
	}
	if c.I2C.Enabled {
		c.Normal = true
		c.TriggerChannel = c.I2C.ClockChannel
		c.TriggerLevel, c.SecondLevel = uint8(math.Round(serial.Threshold)), secondLevel(serial)
	}
	if needSequence && hardwareSerialName(*c) != "" {
		if c.Sequence = planSequence(request); c.Sequence.Length == 0 {
			// Unplannable in hardware: back to software qualification.
			c.Packet, c.Manchester, c.USBLS, c.MIL1553, c.SENT = sramcapture.PacketTriggerConfig{}, sramcapture.ManchesterTriggerConfig{}, sramcapture.USBLSTriggerConfig{}, sramcapture.MIL1553TriggerConfig{}, sramcapture.SENTTriggerConfig{}
			c.UART, c.SPI, c.I2C = sramcapture.UARTTriggerConfig{}, sramcapture.SPITriggerConfig{}, sramcapture.I2CTriggerConfig{}
			c.Normal = fallbackNormal
		}
	}
}

// secondLevel is the code SPI's data line or I2C's SDA is sliced at: its
// own threshold when planned (serialPlanParams), else the first line's.
// TRLC-LINKS: REQ-SDS-013
func secondLevel(p SerialParams) uint8 {
	if p.HaveThrB && p.ThresholdB >= 0 && p.ThresholdB <= 255 {
		return uint8(math.Round(p.ThresholdB))
	}
	return uint8(math.Round(p.Threshold))
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
	if c.SPI.Enabled && c.SPI.GapTicks <= sampleSPIFastGap && c.TriggerHysteresis > sampleSPIFastBand {
		// SPI on the samples at 25 MHz and up: its edges take a few ns, too
		// fast for a converter's offset to cross the level back on them, and
		// its sampled swing shrinks (104 MHz at 1 V/div with 10x probes dipped
		// to 116 of the 115 the offsets' band asked, and a clock edge was lost)
		c.TriggerHysteresis = sampleSPIFastBand
	}
}

// sampleSPIFastGap is the idle gap of an SPI clock of 25 MHz and up (in
// receiver clocks), and sampleSPIFastBand the re-arm band its receiver on
// the samples takes (codes each side of the level).
const (
	sampleSPIFastGap  = 8
	sampleSPIFastBand = 6
)

// decodedIdlePause is the wait between the owner loop's passes while a
// decoded stream's worker builds its lines: a reader's request waits at most
// this long.
const decodedIdlePause = time.Millisecond

// runSRAM exclusively owns the revisioned SRAM backend. It never writes a
// legacy CS1 selector. Fast capture is explicitly freeze/read/rearm; a frozen
// window's software protocol match is not advertised as a gapless trigger.
// TRLC-LINKS: REQ-SDS-003, REQ-SDS-007, REQ-SDS-008, REQ-SDS-009, REQ-SDS-011, REQ-SDS-012, REQ-SDS-036, REQ-SDS-037, REQ-SDS-040
func (e *Engine) runSRAM() {
	defer func() {
		if e.decodedSession != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := e.sram.Halt(ctx); err != nil {
				e.logf("decoded shutdown halt: %v", err)
			}
			if _, err := e.sram.EnableDecodedEvents(false); err != nil {
				e.logf("decoded shutdown stream: %v", err)
			}
		}
		if err := e.closeDecodedDeadline(); err != nil {
			e.logf("decoded shutdown scheduling: %v", err)
		}
		if e.soft != nil {
			e.softStreamStop(e.soft)
		}
	}()
	// A press lights its lamp mid-recall, not after a multi-second deep recall.
	e.sram.SetSpanHook(func(done, total int) {
		e.busyAll.Store(int64(total))
		e.busyDone.Store(int64(done))
		e.flushLEDs()
	})
	e.probeSerialHardware()
	e.mu.Lock()
	e.stats.Image = "general"
	e.mu.Unlock()
	e.logf("engine: full-depth SRAM capture; software protocol qualification has capture dead time")
	e.startConverters("the start") // as after every image load (loadImage)
	var previousFrozen time.Time
	var scratch []uint16
	var average sramAverage
	var averageKey string
	var responseReduction uint32
	var responseGain, responseBW float64
	var retained, recalled bool
	var savedM sramcapture.Metadata
	var savedCfg sramcapture.Config
	var savedPlan SRAMPlan
	var savedNorm, savedForced bool
	var savedTP trigParams
	var savedScale [2]chScale // front-end scale the retained record was captured at
	var savedShownTrigd bool  // the retained capture was published triggered
	var edges edgeDetail      // the last edge windows read, carried to the frames between reads
	var shownScaleGen uint64  // scaleGen when the shown frame was produced
	var singleGen uint64      // SINGLE press the current capture was armed/waiting under
	var held heldRecall       // the stopped record's recall, kept for re-scaled replays
	var lastPace float64      // the previous frame's pacing hold (FrameStages)
	var roll rollView         // the slow-timebase scrolling view (sram_roll.go)
	var streamShown bool      // stopped on a decoded stream's record: its image stays
acquisitionLoop:
	for !e.stopReq.Load() {
		e.setBusy("", 0) // set again only around a recall and its processing
		e.serviceCommands()
		if roll.streaming && (!e.rollActive() || e.protocolImage != imageStream || len(e.sramJobs) != 0) {
			e.rollStreamStop(&roll) // leaving roll: free the fabric for records
		}
		// A decoded stream's triggered record becomes the stopped capture: it is
		// recalled at full depth below, before any image switch can drop it.
		adopting := false
		// Only once the capture has ended: the adoption and the end are two
		// owner calls, and an iteration between them must leave the record.
		var a *adoptedRecord
		if e.decodedSession == nil && e.soft == nil {
			a = e.takeAdopted()
		}
		if a != nil {
			savedM, savedCfg = a.m, a.cfg
			savedScale = e.liveScale()
			savedPlan = SRAMPlan{Samples: int(a.m.Length) * 2, SampleS: 2e-9}
			if a.m.SampleRateHz > 0 && a.m.SamplesPerWord > 0 { // a decimated record (a software stream's capture)
				savedPlan = SRAMPlan{Log: a.cfg.DecimationLog2, Samples: int(a.m.Length) * int(a.m.SamplesPerWord), SampleS: 1 / a.m.SampleRateHz}
			}
			savedPlan = retainedSRAMPlan(savedPlan, e.Snapshot().TdivS)
			savedNorm, savedForced, savedTP = true, false, trigParams{typ: TrigEdge}
			retained, recalled, adopting = true, false, true
			e.logf("engine: showing the decoded stream's record (%d words, trigger word %d)", a.m.Length, a.m.TriggerIndex)
		}
		// Stopped on a stream's record, the image the stream ran on stays: a
		// switch drops the record, and the stream started again (RUN/STOP in
		// BUS Stream) needs that image back (two loads of about four seconds).
		streamShown = (streamShown || adopting) && retained && !e.running.Load()
		if e.soft != nil { // a software-decoded stream owns the acquisition (soft_stream.go)
			retained, recalled = false, false
			e.softStep()
			continue
		}
		if (e.soundWanted() || e.soundStreaming()) && e.soundStep() { // the audio stream (sound_stream.go)
			retained, recalled = false, false
			continue
		}
		// Image-switching jobs run only here, between acquisitions. The switch
		// drops the fabric's record metadata, so the retained record is gone.
		if !adopting && e.decodedSession == nil && e.serviceSRAMJobs() {
			retained, recalled = false, false
			continue
		}
		if !adopting && !streamShown && e.decodedSession == nil && !e.clk.Now().Before(e.decodedStarting) && e.images != nil && e.serviceImage() {
			retained, recalled = false, false
			continue
		}
		if e.decodedSession != nil {
			retained = false
			recalled = false
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
				// the worker keeps the events and builds the lines: this loop only
				// answers the reader's requests, so it waits a little between
				// passes (a pass with nothing to do spun a whole core)
				e.clk.Sleep(decodedIdlePause)
			}
			continue
		}
		// Between acquisitions: a stopped record stays (a recovery reloads).
		if e.running.Load() && !e.rollActive() {
			e.checkCoresDue()
		}
		e.bumpFrames()
		e.viewHeld.Store(retained)
		e.mu.Lock()
		viewPending := e.pendSet
		e.mu.Unlock()
		// A stopped record follows a V/div or offset change: replay it at the
		// new scale instead of reading its codes at the old one.
		viewPending = viewPending || e.scaleGen.Load() != shownScaleGen
		if !e.running.Load() && (!retained || (recalled && !viewPending)) {
			e.clk.Sleep(20 * time.Millisecond)
			continue
		}
		rolling := e.rollActive()
		e.rolling.Store(rolling)
		if rolling {
			retained, recalled = false, false // the roll view is not a retained record
			e.viewHeld.Store(false)
			if e.protocolImage == imageStream {
				e.rollStreamStep(&roll)
			} else {
				e.rollStep(&roll)
			}
			continue
		}
		cfg, plan, tdiv, norm, tp := e.sramConfig()
		replay := !e.running.Load() && retained
		if replay {
			cfg, plan, norm, tp = savedCfg, savedPlan, savedNorm, savedTP
			plan = retainedSRAMPlan(plan, tdiv)
		}
		e.mu.Lock()
		if e.pendSet {
			e.band = e.pendBand
			e.pendSet = false
		}
		e.stats.BandKind = "sram"
		e.stats.HaltMode = "freeze-read-rearm"
		e.stats.SerialBackend = ""
		if name := hardwareSerialName(cfg); name != "" {
			e.stats.SerialBackend = "hardware-" + name
		} else if e.serialMode.Load() == SerialTrigger {
			e.stats.SerialBackend = "none: " + e.serialNoHardware()
		}
		e.stats.TriggerBackend = ""
		if cfg.Qualifier.Enabled {
			e.stats.TriggerBackend = "hardware-" + qualName(cfg.Qualifier.Kind)
		} else if tp.typ != TrigEdge && e.serialMode.Load() != SerialTrigger {
			_, _, why := e.planQualifier(tp, int(cfg.TriggerChannel), e.trigRising.Load())
			e.stats.TriggerBackend = "none: " + why
		}
		e.stats.ZoneBackend = ""
		if cfg.Zones.Enabled() {
			e.stats.ZoneBackend = "hardware"
		} else if e.zonesOn() && e.serialMode.Load() != SerialTrigger {
			why := "the trigger has no hardware yet"
			if e.stats.TriggerBackend == "" {
				_, _, why = e.planZones()
			}
			e.stats.ZoneBackend = "none: " + why
		} else if e.zonesOn() {
			e.stats.ZoneBackend = "none: zones qualify an edge, pulse, slope or video trigger, not a serial one"
		}
		e.stats.TdivS = tdiv
		e.stats.DisplayedS = math.Min(tdiv, float64(plan.Samples)*plan.SampleS/10)
		e.stats.RecordS = float64(plan.Samples) * plan.SampleS
		e.stats.WinCols = plan.Screen
		e.mu.Unlock()
		armAt := e.clk.Now()
		armedAt := armAt
		feAtArm := e.feGen.Load() // the front end's write generation this capture is armed under
		var ctx context.Context
		var cancel context.CancelFunc
		var err error
		forced, aborted := savedForced, false
		symbolArmCount := -1
		userForced := false
		m := savedM
		if !replay {
			retained = false
			held = heldRecall{} // a new capture: the stopped record's copies go
			symbolArmCount = e.symbolCount(cfg)
			ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
			err = e.sram.Arm(ctx, cfg)
			cancel()
			armedAt = e.clk.Now()
			if err != nil {
				e.busErr(err)
				e.sleepBeating(100 * time.Millisecond)
				continue
			}
			forced, userForced = false, false
			// A SINGLE pressed while this capture still waits for its event is
			// served by it; once frozen, a later press needs a fresh capture.
			singleGen = e.singleGen.Load()
			// AUTO waits for enough prehistory plus a bounded event wait. SINGLE/NORM
			// never fabricate a trigger. Long waits continue servicing controls.
			autoWait := 100*time.Millisecond + time.Duration(float64(cfg.PreWords)*float64(2-boolInt(plan.Log != 0))*plan.SampleS*1e9)
			var next sramcapture.Config
			var settingsAt time.Time
			for !e.stopReq.Load() {
				e.serviceCommands()
				if e.decodedSession != nil || e.soft != nil {
					continue acquisitionLoop
				}
				e.beatN.Add(1)
				if len(e.sramJobs) != 0 {
					aborted = true
					break
				}
				// the settings changed: a fresh capture with them (looked at
				// every few polls - a change waits a few milliseconds)
				if now := e.clk.Now(); now.Sub(settingsAt) >= settingsPollEvery {
					settingsAt = now
					_, nextTdiv, nextNorm, nextTP := e.sramConfigInto(&next)
					if next != cfg || nextTdiv != tdiv || nextNorm != norm || nextTP != tp {
						aborted = true
						break
					}
				}
				gen := e.singleGen.Load() // before the poll: a press seen here precedes the freeze
				m, err = e.sram.Poll()
				if err != nil || m.DataFault {
					break
				}
				if m.Frozen && m.Ready {
					m, err = e.sram.Status() // the record's length and positions
					break
				}
				singleGen = gen
				// AUTO forces after its wait, unless the hardware triggered: the
				// record then only fills its post-trigger part (at 10ms/div 135 ms,
				// past the wait, and a frame-start trigger's records read forced);
				// FORCE (ForceTrigger) forces once now, and that frame is shown
				// even in NORMAL.
				autoDue := autoForceDue(norm, e.running.Load(), m.Triggered, e.clk.Now().Sub(armAt), autoWait)
				if cfg.Normal && !forced && !autoDue && e.forceReq.Swap(false) {
					userForced, autoDue = true, true
				}
				if cfg.Normal && !forced && autoDue {
					ctx, cancel = context.WithTimeout(context.Background(), time.Second)
					err = e.sram.Force(ctx)
					cancel()
					forced = true
					if err != nil {
						break
					}
				}
				e.clk.Sleep(statusPollWait(e.clk.Now().Sub(armedAt)))
			}
			if aborted || e.stopReq.Load() || err != nil || m.DataFault {
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
				haltErr := e.sram.Halt(ctx)
				cancel()
				if err != nil {
					e.busErr(err)
				}
				if haltErr != nil {
					e.busErr(haltErr)
				}
				if m.DataFault {
					e.busErr(fmt.Errorf("SRAM acquisition FIFO overflow"))
				}
				continue
			}
		}
		if !replay {
			savedScale = e.liveScale()
			e.noteSymbolMatches(cfg, symbolArmCount, e.clk.Now().Sub(armedAt))
		}
		shownScaleGen = e.scaleGen.Load()
		e.shownScaleGen.Store(shownScaleGen)
		savedM, savedCfg, savedPlan, savedNorm, savedTP, savedForced = m, cfg, plan, norm, tp, forced
		fullRecall := !e.running.Load()
		retained, recalled = true, fullRecall
		e.viewHeld.Store(true)
		recallOffset, recallWords := uint32(0), m.Length
		posFrac := math.Float64frombits(e.trigPosFrac.Load())
		// Pulse, slope and video triggers are the FPGA's: nothing is searched
		// in software, only a video line moves back to its sync's start.
		softQualify := false
		// The capture that serves a SINGLE is first read as a live frame is, so
		// it shows and stops at once; the stop that follows reads its full
		// record (reading the whole record first kept the screen empty for
		// seconds at every timebase).
		singleShot := !replay && e.singleArmed.Load() && singleGen == e.singleGen.Load()
		pre := 0 // samples the live window holds before the screen's
		if !fullRecall {
			// A software-qualified trigger needs room to find its event away
			// from the hardware edge and still fill the screen around it. The
			// FPGA's serial trigger fires at the match's end and software
			// moves the trigger point back to its start: the window holds
			// that much more before the trigger (twice the screen would halve
			// the live envelope's resolution, and a fast bus no longer
			// decodes).
			screen := plan.Screen
			switch {
			case softQualify:
				screen *= 2
			case e.serialMode.Load() == SerialTrigger:
				pre = e.serialPreRoom(plan.SampleS, screen)
			case cfg.Qualifier.Enabled && cfg.Qualifier.Kind == sramcapture.QualVideo && plan.SampleS > 0:
				pre = int(8e-6 / plan.SampleS) // a line sync's width
			}
			recallOffset, recallWords = liveSRAMWindowPre(m, screen, posFrac, pre)
		}
		// While running, a window larger than the display needs is read as a
		// peak-detect envelope; the deep record stays in SRAM for a stop.
		var bucket uint32
		envBuckets := uint32(sramcapture.EnvelopeBuckets)
		if m.FractionBits == 8 {
			envBuckets = sramcapture.EnvelopeBucketsQ8 // two words a bucket
		}
		readout := "full record"
		rawForLease := false // a live raw window read only for the raw leases: abandoned when they end
		if !fullRecall {
			if why := e.envelopeBlock(&cfg, tp); why != "" {
				readout = "raw window: " + why
				rawForLease = why == blockRaw && !singleShot && !replay
			} else {
				bucket, recallOffset, recallWords = envelopeWindow(recallOffset, recallWords, envBuckets, m.Length, uint32(pre/max(1, int(m.SamplesPerWord))))
				readout = "raw window: short"
				if bucket != 0 {
					readout = "envelope"
				}
			}
			if singleShot {
				readout = "single, " + readout
			}
		}
		e.mu.Lock()
		e.stats.LiveReadout = fmt.Sprintf("%s (%d words)", readout, recallWords)
		e.mu.Unlock()
		frozenAt := e.clk.Now()
		f := e.arena.Write()
		q1, q2 := f.Q1, f.Q2
		c1, c2 := f.C1, f.C2
		*f = Frame{C1: c1, C2: c2, Valid: int(recallWords) * int(m.SamplesPerWord), WinCols: plan.Screen, SampleS: 1 / m.SampleRateHz, TdivS: tdiv, DisplayedS: math.Min(tdiv, float64(plan.Samples)*plan.SampleS/10),
			EdgeX: float64(m.TriggerIndex-recallOffset) * float64(m.SamplesPerWord), TrigPos: int(m.TriggerIndex-recallOffset) * int(m.SamplesPerWord),
			Trigd: m.Triggered && (cfg.Normal || m.HostTrigger) && !forced, Coherent: true, HaltOK: true, Norm: norm, Interp: plan.Screen < 800, Decimation: m.Decimation, TriggerKind: "hardware-edge"}
		f.CaptureDepth = int(m.Length) * int(m.SamplesPerWord)
		if !f.Trigd {
			f.TriggerKind = "forced"
			f.EdgeX = -1
		}
		f.CaptureSampleS = f.SampleS
		if bucket != 0 {
			// Each output sample is half a bucket: a (min, max) pair per bucket.
			half := float64(bucket) * float64(m.SamplesPerWord) / 2
			f.Valid = 2 * int(envBuckets)
			f.SampleS *= half
			f.WinCols = min(f.Valid, int(float64(plan.Screen)/half+0.5))
			f.Interp = false
			if f.Trigd || f.EdgeX >= 0 {
				f.EdgeX = float64(m.TriggerIndex-recallOffset) * float64(m.SamplesPerWord) / half
				f.TrigPos = int(f.EdgeX)
			}
		}
		if m.FractionBits == 8 {
			f.Q1 = qBuffer(q1, f.Valid)
			f.Q2 = qBuffer(q2, f.Valid)
		}
		writer := sramFrameWriter{f: f, m: m}
		e.setBusy("reading", 0) // the recall's burst loop reports progress
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		if rawForLease {
			e.rawMu.Lock()
			e.rawCancel = cancel
			e.rawMu.Unlock()
			e.abandonRawRead() // the last lease ended since the readout was chosen
		}
		e.rawMu.Lock()
		e.recallCancel = cancel // a SOUND lease starting abandons it (LeaseSound)
		e.rawMu.Unlock()
		recall := e.sram.Recall
		if m.Revision == 10 {
			recall = e.sram.RecallForward
		}
		recallAt := e.clk.Now()
		var views viewReads
		if bucket != 0 {
			// m is the status the capture froze under, read just now. The
			// views showing part of the screen magnified get that part at full
			// detail (the screen's envelope spreads its buckets over all of
			// it), read with it.
			views = e.planViews(f, m, plan.Screen, recallOffset, recallWords, bucket, envBuckets)
			screenRead := sramcapture.RecordRead{Offset: recallOffset, Bucket: bucket, Count: envBuckets, Dst: &writer}
			err = e.sram.RecallReadsOf(ctx, m, views.reads([]sramcapture.RecordRead{screenRead})...)
			if err != nil && views != (viewReads{}) {
				// A view's read must not cost the frame: read the screen alone.
				views, writer.n = viewReads{}, 0
				err = e.sram.RecallReadsOf(ctx, m, screenRead)
			}
			rejects, last := e.sram.EnvelopeDiagnostics()
			e.mu.Lock()
			e.stats.EnvelopeRejects, e.stats.EnvelopeLast = rejects, last
			e.mu.Unlock()
		} else if key := (heldKey{m, recallOffset, recallWords}); replay && held.key == key {
			// A stopped record re-shown at a new V/div, offset or timebase:
			// the recall is already in memory, skip seconds of SRAM readout.
			writer.n = held.restore(f)
		} else {
			_, err = recall(ctx, recallOffset, recallWords, &writer)
			if err == nil && recallOffset == 0 && recallWords == m.Length && writer.n == f.Valid {
				held.store(key, f)
			} else if !replay {
				held.key = heldKey{} // a new capture: the held record is gone
			}
		}
		recalledAt := e.clk.Now()
		cancelled := errors.Is(ctx.Err(), context.Canceled)
		soundCut := cancelled && e.soundStarting()
		abandoned := rawForLease && cancelled
		cancel()
		e.rawMu.Lock()
		e.recallCancel = nil
		if rawForLease {
			e.rawCancel = nil
		}
		e.rawMu.Unlock()
		if soundCut {
			// SOUND takes the fabric: the record would be dropped anyway.
			e.logf("engine: record read abandoned after %v: SOUND starts", recalledAt.Sub(recallAt).Round(time.Millisecond))
			continue
		}
		if abandoned {
			// Nobody wants the raw record any more: no frame, and the next
			// capture is the live view.
			e.logf("engine: raw read abandoned after %v: the raw leases ended", recalledAt.Sub(recallAt).Round(time.Millisecond))
			continue
		}
		if err != nil || writer.n != f.Valid {
			if err == nil {
				err = fmt.Errorf("short SRAM frame")
			}
			e.busErr(err)
			continue
		}
		if bucket == 0 && !replay && m.Decimation <= 1 && m.FractionBits == 0 && m.SamplesPerWord == 2 {
			e.noteRecordCores(f.C1[:f.Valid], f.C2[:f.Valid]) // raw codes, before any conditioning
		}
		// A live envelope holds no edge shape, nor a mean where the signal is
		// faster than a bucket: read the trigger edge and the next edge the
		// other way at the record's rate, two short reads while it is still
		// frozen, for rise, fall, over/preshoot and such a signal's mean and
		// RMS.
		if bucket != 0 && !replay && f.Trigd {
			if key := (edgeKey{averageCfg(cfg), savedScale}); edges.due(key, e.clk.Now()) {
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
				disc := f.C1[:f.Valid]
				if cfg.TriggerChannel == 1 {
					disc = f.C2[:f.Valid]
				}
				at, trig, rising := m.TriggerIndex, int(f.EdgeX)/2, !cfg.Falling
				if tp.typ != TrigEdge || e.serialMode.Load() == SerialTrigger {
					// the trigger point is no edge of the edge trigger's direction
					if b, r := nearestEdgeBucket(disc, trig); b >= 0 {
						at, trig, rising = recallOffset+uint32(b)*bucket+bucket/2, b, r
					}
				}
				edge := e.recallEdgeWindow(ctx, m, at, savedScale)
				var opp *EdgeWindow
				if b := oppositeEdgeBucket(disc, trig, rising); b >= 0 {
					opp = e.recallEdgeWindow(ctx, m, recallOffset+uint32(b)*bucket+bucket/2, savedScale)
				}
				cancel()
				edges.store(key, e.clk.Now(), edge, opp)
			}
			f.Edge, f.EdgeOpp = edges.edge, edges.opp
			if f.Edge != nil || f.EdgeOpp != nil {
				f.EdgeRead = edges.read
			}
		} else if !replay {
			edges = edgeDetail{} // a capture of another kind: the windows no longer match
		}
		e.windows(f, &views, m, recallOffset, bucket, savedScale)
		detailAt := e.clk.Now()
		if e.soundStarting() {
			// SOUND starts: a second of conditioning for a frame it drops.
			e.logf("engine: record dropped before processing: SOUND starts")
			continue
		}
		e.setBusy("processing", 0)
		// CPU-only conditioning: let a staged lamp latch meanwhile (SetLEDs).
		e.cpuStage.Unlock()
		condKey := heldCondKey{held.key, e.acqMode.Load(), e.eresLen.Load()}
		if replay && held.key == (heldKey{m, recallOffset, recallWords}) && held.cond.key == condKey {
			// The record's conditioning is independent of the view: reuse it.
			held.restoreCond(f, q1, q2)
			f.Content = held.cond.content
		} else {
			e.contentGen++
			f.Content = e.contentGen
			if len(f.Q1) == f.Valid && len(f.Q2) == f.Valid && bucket == 0 && (e.boxDecim || cfg.Pick) && m.Decimation > 1 {
				// Box averages: no CIC droop to compensate; a sinc's -3 dB
				// at 0.443 of the output rate. Picked samples: no filter.
				f.PassbandHz, f.BandwidthHz = .2/f.SampleS, .443/f.SampleS
				f.Filter = "box average"
				if e.pickDecim || cfg.Pick {
					f.PassbandHz, f.BandwidthHz = .5/f.SampleS, .5/f.SampleS
					f.Filter = "every 2^n-th sample"
				}
				for i := 0; i < f.Valid; i++ {
					f.C1[i] = roundQ8(f.Q1[i])
					f.C2[i] = roundQ8(f.Q2[i])
				}
			} else if len(f.Q1) == f.Valid && len(f.Q2) == f.Valid && bucket == 0 {
				scratch = qBuffer(scratch, f.Valid)
				dsp.ConditionQ8(f.Q1, scratch)
				dsp.ConditionQ8(f.Q2, scratch)
				f.FilterGuard = dsp.PrecisionGuard
				if responseReduction != m.Decimation {
					responseGain, responseBW = dsp.PrecisionLimits(int(m.Decimation))
					responseReduction = m.Decimation
				}
				f.NoiseGainIdeal = responseGain
				f.PassbandHz = .075 / f.SampleS
				f.BandwidthHz = responseBW / f.SampleS
				f.Filter = "CIC3 + compensated FIR"
				for i := 0; i < f.Valid; i++ {
					f.C1[i] = roundQ8(f.Q1[i])
					f.C2[i] = roundQ8(f.Q2[i])
				}
			}
			if bucket != 0 {
				// Extremes of whole buckets: no per-core calibration or FIR applies.
				if len(f.Q1) == f.Valid && len(f.Q2) == f.Valid {
					for i := 0; i < f.Valid; i++ {
						f.C1[i] = roundQ8(f.Q1[i])
						f.C2[i] = roundQ8(f.Q2[i])
					}
				}
				f.Filter = fmt.Sprintf("peak-detect display readout, %d words per bucket; full record in SRAM", bucket)
				f.PeakDetect = true
				// Decimated records are filtered across the converters already.
				if cal := e.interleaveCalibration; cal != nil && m.Decimation <= 1 {
					f.Q1, f.Q2 = qBuffer(q1, f.Valid), qBuffer(q2, f.Valid)
					cal.debiasEnvelope(0, f.C1[:f.Valid], f.Q1, len(f.Q1) == f.Valid && m.FractionBits == 8)
					cal.debiasEnvelope(1, f.C2[:f.Valid], f.Q2, len(f.Q2) == f.Valid && m.FractionBits == 8)
					f.Filter += "; converter offsets removed from the extremes"
				}
			} else if m.FractionBits == 0 && e.interleaveCalibration != nil {
				q1 = qBuffer(q1, f.Valid)
				q2 = qBuffer(q2, f.Valid)
				// The converters' calibration is the capture's detent's.
				scale := [2]float64{savedScale[0].vdiv, savedScale[1].vdiv}
				if e.interleaveCalibration.apply(f.C1[:f.Valid], f.C2[:f.Valid], q1, q2, scale, f.SampleS) {
					f.Q1 = q1
					f.Q2 = q2
					f.Filter = "interleave calibration (phase checked)"
					if e.interleaveCalibration.hasTiming(scale, f.SampleS) {
						f.Filter += "; aperture compensation"
						f.FilterGuard = 2
					}
				} else {
					f.Filter = "interleave calibration bypassed (scale, clipping or phase mismatch)"
				}
			}
			if e.acqMode.Load() == AcqEres {
				length := clampEresLen(int(e.eresLen.Load()))
				if length > 1 {
					if len(f.Q1) != f.Valid {
						f.Q1, f.Q2 = qBuffer(q1, f.Valid), qBuffer(q2, f.Valid)
						for i := 0; i < f.Valid; i++ {
							f.Q1[i], f.Q2[i] = uint16(f.C1[i])<<8, uint16(f.C2[i])<<8
						}
					}
					scratch = qBuffer(scratch, f.Valid)
					eresQ8(f.Q1, scratch, length)
					eresQ8(f.Q2, scratch, length)
					for i := 0; i < f.Valid; i++ {
						f.C1[i], f.C2[i] = roundQ8(f.Q1[i]), roundQ8(f.Q2[i])
					}
					f.Filter += fmt.Sprintf("; ERES %d-point boxcar", length)
					f.FilterGuard += length / 2
					bw := .443 / (float64(length) * f.SampleS)
					f.NoiseGainIdeal = math.Sqrt(float64(length))

					if f.BandwidthHz == 0 || bw < f.BandwidthHz {
						f.BandwidthHz = bw
					}
				}
			}
			if held.key == (heldKey{m, recallOffset, recallWords}) {
				held.storeCond(condKey, f)
				held.cond.content = f.Content
			}
		}
		if bucket != 0 {
			// After the (min, max)-pair calibration above: put each pair in
			// time order so the live envelope also serves as samples.
			orderEnvelopeHead(f.C1[:f.Valid], f.Q1)
			orderEnvelopeHead(f.C2[:f.Valid], f.Q2)
			orderEnvelope(f.C1[:f.Valid], f.Q1)
			orderEnvelope(f.C2[:f.Valid], f.Q2)
			f.Ordered = true
		}
		f.FullRate = fullRateFrame(bucket, m.Decimation, e.acqMode.Load())
		if fullRecall {
			f.Filter += "; stopped full record (single acquisition)"
		}
		if live := e.liveScale(); replay && live != savedScale {
			for ch := range live {
				c, q := f.C1[:f.Valid], f.Q1
				if ch == 1 {
					c, q = f.C2[:f.Valid], f.Q2
				}
				if len(q) != f.Valid {
					q = nil
				}
				rescaleCodes(c, q, savedScale[ch], live[ch])
			}
			f.Filter += "; stopped record shown at the current V/div and offset"
		}
		conditionedAt := e.clk.Now()
		e.cpuStage.Lock()
		if !replay {
			e.learnLevels(f, savedScale, feAtArm)
		}
		f.WindowNs = int64(float64(f.Valid) * f.SampleS * 1e9)
		if !previousFrozen.IsZero() {
			f.GapNs = int64(armAt.Sub(previousFrozen))
		}
		previousFrozen = frozenAt
		disc := f.C1[:f.Valid]
		if cfg.TriggerChannel == 1 {
			disc = f.C2[:f.Valid]
		}
		_, _, f.Ptp = ptp(disc)
		qualified := f.Trigd
		if cfg.Zones.Enabled() && f.Trigd && f.EdgeX >= 0 && f.SampleS > 0 {
			// The FPGA's zones decided and fired when the latest window ended:
			// the trigger point moves back to the edge they qualified, before
			// it is placed on its crossing.
			_, delayS, _ := e.planZones()
			f.EdgeX -= delayS / f.SampleS
			f.TrigPos = int(f.EdgeX)
		}
		switch {
		case cfg.Qualifier.Enabled:
			// The FPGA's qualifier decided; software only places the trigger
			// point on the event's level crossing.
			if f.Trigd {
				f.TriggerKind = "hardware-" + qualName(cfg.Qualifier.Kind)
				q := f.Q1
				if cfg.TriggerChannel == 1 {
					q = f.Q2
				}
				f.EdgeX = placeQualified(f, disc, q, cfg.Qualifier, cfg.TriggerLevel)
				f.TrigPos = int(f.EdgeX)
			}
		case tp.typ != TrigEdge && e.serialMode.Load() != SerialTrigger:
			// No qualifier could take it: nothing triggers. Software never
			// decides a trigger.
			qualified, f.Trigd = false, false
			f.TriggerKind, f.EdgeX = "untriggered", -1
		}
		// Place a plain edge trigger at its exact level crossing: the FPGA
		// index is word-granular. Envelope frames hold bucket extremes, not
		// samples in time order.
		if tp.typ == TrigEdge && e.serialMode.Load() != SerialTrigger && f.Trigd && !m.HostTrigger {
			if lvl := e.trigDispLevel(int(cfg.TriggerChannel)); lvl >= 0 {
				q := f.Q1
				if cfg.TriggerChannel == 1 {
					q = f.Q2
				}
				if !e.placeEdgeTrigger(f, disc, q, float64(lvl), !cfg.Falling) && !(replay && savedShownTrigd) {
					// The FPGA pointed at an edge the calibrated record does
					// not have (converter noise): software may check a trigger
					// the hardware pointed out and reject it (owner rule).
					// AUTO shows it untriggered, NORMAL and SINGLE keep waiting.
					// A stopped record whose capture was shown triggered keeps
					// that trigger (its envelope confirmed it), so the record
					// shows the screen the capture did, not its middle.
					f.Trigd, qualified = false, false
					f.TriggerKind, f.EdgeX = "forced", -1
				}
			}
		}
		e.noteARINCLevels(f)
		if m.HostTrigger {
			// A history ring stopped on a decoded stream's frame (ADR-STREAM-RING):
			// for streaming data software is the trigger, and the trigger word is
			// the frame's start, placed through the ring's time stamp.
			f.TriggerKind = "stream"
		} else if name := hardwareSerialName(cfg); e.serialMode.Load() == SerialTrigger && name != "" {
			// The FPGA's matcher decided; software only moves the trigger
			// point from where the match ended to where it began.
			qualified = f.Trigd
			f.TriggerKind = "hardware-" + name
			if qualified {
				e.serialMatches.Add(1)
				if cfg.SPI.Enabled && e.clk.Now != nil {
					e.serLv.spiQuiet.Store(e.clk.Now().UnixNano())
				}
				hw := f.EdgeX
				if at := e.serialMatchStart(f, f.EdgeX); at >= 0 {
					e.serLv.shiftS.Store(math.Float64bits((f.EdgeX - at) * f.SampleS))
					f.EdgeX = at
					f.TrigPos = int(at)
				}
				if name == "uart" && hw >= 0 && f.EdgeX >= 0 {
					// the matched frame's stop bit: the receiver on the samples
					// publishes its match a fixed time after it, longer than a
					// stop bit at its rates
					end := hw
					if cfg.UART.Period16 != 0 && f.SampleS > 0 {
						end = math.Max(f.EdgeX, hw-sampleUARTMatchLagS/f.SampleS)
					}
					f.MatchEndS, f.HasMatchEnd = (end-f.EdgeX)*f.SampleS, true
				}
			}
		} else if e.serialMode.Load() == SerialTrigger {
			// No matcher could take the request (its levels are still being
			// learned from these untriggered captures, or the image has none
			// for it): nothing triggers. Software never decides a trigger.
			qualified, f.Trigd = false, false
			f.TriggerKind, f.EdgeX = "untriggered", -1
		}
		switch {
		case cfg.Zones.Enabled():
			if f.Trigd {
				f.TriggerKind = "hardware-zone"
			}
		case e.zonesOn() && e.serialMode.Load() != SerialTrigger:
			// No zone hardware could take them: nothing triggers. Software
			// never decides a trigger.
			qualified, f.Trigd = false, false
			f.TriggerKind, f.EdgeX = "untriggered", -1
		}
		// Mask test: every triggered frame, published or held (zonemask.go).
		// Stop-on-fail stops on the offending frame and shows it.
		maskStop := false
		if qualified && !fullRecall && e.maskMode.Load() != MaskOff {
			posFrac := math.Float64frombits(e.trigPosFrac.Load())
			if failed, stop := e.maskEval(f, f.Valid, 0, f.EdgeX, f.SampleS, posFrac); failed && stop {
				maskStop = true
			}
		}
		averageDone := true
		if (e.acqMode.Load() == AcqAverage || e.acqMode.Load() == AcqPrecision) && qualified && !fullRecall {
			n := int(e.avgCount.Load())
			key := fmt.Sprintf("%v/%g/%t/%v/%d/%d/%d/%d/%d/%s", averageCfg(cfg), tdiv, norm, tp, e.avgGen.Load(), e.chVdivBits[0].Load(), e.chVdivBits[1].Load(), e.trigOffV[0].Load(), e.trigOffV[1].Load(), f.Filter)
			// Refine the word-level trigger with the observed waveform crossing.
			lo, hi := int(f.EdgeX)-4096, int(f.EdgeX)+4096
			if lo < 0 {
				lo = 0
			}
			if hi > f.Valid {
				hi = f.Valid
			}
			edge := -1.0
			if tp.typ == TrigEdge && e.serialMode.Load() != SerialTrigger && f.EdgeX >= 0 {
				// Already the exact trigger-level crossing (refineTriggerEdge).
				edge = f.EdgeX - float64(lo)
			} else if hi > lo {
				edge = averageAlignEdge(disc[lo:hi], f.EdgeX-float64(lo), e.serialMode.Load() == SerialTrigger || tp.typ != TrigEdge, !cfg.Falling)
			}
			if edge >= 0 {
				f.EdgeX = edge + float64(lo)
			}
			if key != averageKey {
				average.reset()
				averageKey = key
			}
			if len(f.Q1) != f.Valid {
				f.Q1, f.Q2 = qBuffer(q1, f.Valid), qBuffer(q2, f.Valid)
				for i := 0; i < f.Valid; i++ {
					f.Q1[i], f.Q2[i] = uint16(f.C1[i])<<8, uint16(f.C2[i])<<8
				}
			}
			count := 0
			if edge >= 0 {
				count = average.push(f, n)
			} else {
				average.reset()
			}
			f.Filter += fmt.Sprintf("; average %d/%d (waveform aligned)", count, n)
			if count > 0 {
				if f.NoiseGainIdeal == 0 {
					f.NoiseGainIdeal = 1
				}
				f.NoiseGainIdeal *= math.Sqrt(float64(count))
			}
			averageDone = count >= n
			// A stop keeps the averaged frame shown: reading the raw capture
			// again would replace the average with one of its captures.
			recalled = true
		} else {
			average.reset()
		}
		e.mu.Lock()
		e.stats.Coherent++
		e.stats.HaltConfirm++
		e.stats.ValidDepth = f.Valid
		e.stats.MemDepth = plan.Samples
		e.stats.LastPtp = f.Ptp
		e.stats.SRAMRecallMs = float64(recalledAt.Sub(recallAt)) / float64(time.Millisecond)
		e.stats.ConditionMs = float64(conditionedAt.Sub(detailAt)) / float64(time.Millisecond)
		e.stats.DrainMs = float64(e.clk.Now().Sub(frozenAt)) / float64(time.Millisecond)
		e.mu.Unlock()
		// Software qualifiers may reject a capture that froze before the arm
		// polling loop could consume FORCE. Release that held capture once too.
		if !replay && e.forceReq.Swap(false) {
			userForced = true
		}
		if userForced {
			f.Trigd = false
			f.TriggerKind = "forced"
		}
		// Bode measures every capture, triggered or not: its gain and phase
		// are the ratio of the two channels sampled together. Asking for a
		// trigger would stop the sweep, unsaid, with the level off the signal.
		if !replay && !f.PeakDetect && e.bodeMode.Load() == BodeOn {
			e.bodeEval(f, f.Valid, f.SampleS)
		}
		publish := qualified || !norm || fullRecall || userForced || maskStop
		// AUTO with the serial or zone gate armed holds the captures the gate
		// rejected. Publishing every one drew it centred on the record middle
		// between the matched frames, so the matched bytes jumped about.
		if publish && !qualified && !fullRecall && !userForced && !maskStop {
			publish = e.autoGateLiveness()
		} else if qualified {
			e.serialHeld, e.zoneHeld = 0, 0
		}
		if maskStop {
			e.maskStopped.Store(true)
			e.running.Store(false)
			e.mu.Lock()
			e.stats.Running = false
			e.mu.Unlock()
		}
		if !replay {
			savedShownTrigd = publish && f.Trigd
		}
		if publish {
			e.noteChannelMeans(f)
			e.mu.Lock()
			e.pubIdent = frameIdent{f.TdivS, f.SampleS, f.PeakDetect}
			e.mu.Unlock()
			e.seq++
			f.Seq = e.seq
			e.arena.Publish()
			e.mu.Lock()
			e.stats.Published++
			e.lastPublish = e.clk.Now()
			e.stats.Seq = e.seq
			e.lastPubAt = e.clk.Now()
			e.notePublish(e.lastPubAt)
			e.mu.Unlock()
			if (qualified && averageDone || userForced) && e.singleArmed.Load() && !fullRecall && singleGen == e.singleGen.Load() {
				if acq := e.acqMode.Load(); acq == AcqAverage || acq == AcqPrecision {
					recalled = true // the completed block is the result: no raw record over it
				}
				e.singleArmed.Store(false)
				e.running.Store(false)
				e.mu.Lock()
				e.stats.Running = false
				e.stats.Single = false
				e.mu.Unlock()
			}
		} else {
			e.mu.Lock()
			e.stats.Held++
			e.mu.Unlock()
		}
		e.setBusy("", 0)
		doneAt := e.clk.Now()
		ms := func(a, b time.Time) float64 { return float64(b.Sub(a)) / float64(time.Millisecond) }
		e.mu.Lock()
		e.stats.FrameStages = fmt.Sprintf("arm %.0f wait %.0f read %.0f detail %.0f proc %.0f post %.0f prev-pace %.0f (ms)",
			ms(armAt, armedAt), ms(armedAt, frozenAt), ms(frozenAt, recalledAt), ms(recalledAt, detailAt), ms(detailAt, conditionedAt), ms(conditionedAt, doneAt), lastPace)
		e.mu.Unlock()
		e.paceHoldWithFloor(doneAt, qualified && f.Trigd, 0)
		if p := time.Duration(e.displayPaceNs.Load()); p > 0 {
			e.paceHoldWithFloor(armAt, false, p) // the display's share (SetDisplayPace)
		}
		lastPace = ms(doneAt, e.clk.Now())
	}
}

// TRLC-LINKS: REQ-SDS-040
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// liveSRAMWindow selects a contiguous preview anchored at the trigger position. At timebases
// needing more samples, retain the whole visible span instead of aliasing it.
// TRLC-LINKS: REQ-SDS-010
func liveSRAMWindow(m sramcapture.Metadata, screen int, posFrac float64) (uint32, uint32) {
	return liveSRAMWindowPre(m, screen, posFrac, 0)
}

// liveSRAMWindowPre is liveSRAMWindow holding extraPre more samples before the
// trigger, as many as the record holds beyond the screen: the screen's end
// stays (a T55xx command at 2ms/div lost its last 2 ms to a serial
// trigger's room).
// TRLC-LINKS: REQ-SDS-010
func liveSRAMWindowPre(m sramcapture.Metadata, screen int, posFrac float64, extraPre int) (uint32, uint32) {
	per := int(m.SamplesPerWord)
	samples := max(2048, screen+512)
	if m.FractionBits == 8 {
		samples = max(256, screen+128)
	}
	margin := samples - screen
	extraPre = max(0, extraPre)
	if over := samples + extraPre - int(m.Length)*per; over > 0 {
		extraPre = max(0, extraPre-over)
	}
	samples += extraPre
	words := min(m.Length, uint32((samples+per-1)/per))
	// The trigger sits at posFrac of the screen, so the window holds
	// screen·posFrac samples before it plus half the margin. Centring the
	// window leaves a quarter of the screen as a flat extension at trigger
	// position 0.2 or 0.8 (1 µs/div).
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = .5
	}
	pre := uint32((float64(screen)*posFrac + float64(margin)/2 + float64(extraPre)) / float64(per))
	pre = min(pre, words)
	offset := uint32(0)
	if m.TriggerIndex > pre {
		offset = m.TriggerIndex - pre
	}
	if offset+words > m.Length {
		offset = m.Length - words
	}
	return offset, words
}

// chScale is a channel's front-end scale: analog V/div (the codes' range) and
// the applied input-referred offset. A code reads (code-128)·vdiv/25 - off.
type chScale struct{ vdiv, off float64 }

// liveScale is the front end's scale now.
// TRLC-LINKS: REQ-SDS-040
func (e *Engine) liveScale() [2]chScale {
	var s [2]chScale
	for ch := range s {
		s[ch] = chScale{math.Float64frombits(e.chVdivBits[ch].Load()), math.Float64frombits(e.trigOffV[ch].Load())}
	}
	return s
}

// rescaleCodes re-expresses codes captured at one scale at another, so every
// consumer that reads them at the live V/div and offset sees the captured
// volts. Codes that leave the 8-bit range clip, as on the screen.
// TRLC-LINKS: REQ-SDS-015, REQ-SDS-040
func rescaleCodes(c []uint8, q []uint16, from, to chScale) {
	if !(from.vdiv > 0) || !(to.vdiv > 0) {
		return
	}
	k := from.vdiv / to.vdiv
	b := (to.off - from.off) * 25 / to.vdiv
	// Tables, not per-sample float math: 2M samples of float clamping take
	// 3.7 s on the ARM.
	var lut8 [256]uint8
	for v := range lut8 {
		lut8[v] = uint8(math.Round(math.Max(0, math.Min(255, 128+(float64(v)-128)*k+b))))
	}
	for i, v := range c {
		c[i] = lut8[v]
	}
	if len(q) == 0 {
		return
	}
	// Fixed point, not a 64K table: random lookups into 128 KB miss the ARM's
	// caches on every sample (~0.6 s for a deep record per V/div step).
	kf := int64(math.Round(k * (1 << 16)))
	bf := int64(math.Round((32768 + b*256) * (1 << 16)))
	for i, v := range q {
		x := ((int64(v)-32768)*kf + bf + 1<<15) >> 16
		q[i] = uint16(min(max(x, 0), 65535))
	}
}

// softAnchor bounds a software qualifier to the anchors the display can
// centre a full screen on: posFrac of the screen before, the rest after. It
// prefers the event nearest the hardware edge. A window no larger than the
// screen leaves only the hardware edge's own neighbourhood.
// TRLC-LINKS: REQ-SDS-011
func softAnchor(valid, screen int, posFrac, hwEdge float64) anchorSpan {
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = .5
	}
	lo := int(math.Ceil(float64(screen) * posFrac))
	hi := valid - int(math.Ceil(float64(screen)*(1-posFrac)))
	target := int(hwEdge)
	if hwEdge < 0 || hwEdge >= float64(valid) {
		target = (lo + hi) / 2
	}
	if lo > hi { // the window cannot hold a screen: anywhere, as before
		return anchorSpan{target: target, lo: 0, hi: valid}
	}
	return anchorSpan{target: target, lo: lo, hi: hi}
}

// A stopped timebase change reuses the captured sample pitch and depth.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func retainedSRAMPlan(p SRAMPlan, tdiv float64) SRAMPlan {
	p.Screen = int(math.Min(float64(p.Samples), math.Max(2, math.Round(10*tdiv/p.SampleS))))
	return p
}

// offRecord is how many of a screen's winCols columns, centred on anchor at
// posFrac, fall outside a record of valid samples; zero when the record
// cannot hold a screen at all (then nothing better exists).
// TRLC-LINKS: REQ-SDS-013
func offRecord(valid, winCols int, posFrac float64, anchor int) int {
	if winCols <= 0 || winCols > valid || anchor < 0 {
		return 0
	}
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = .5
	}
	left := anchor - int(math.Round(posFrac*float64(winCols)))
	right := left + winCols - valid
	return max(0, -left) + max(0, right)
}

// symbolCount reads the symbol trigger's match count (modulo 2^16), or -1
// without the block.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) symbolCount(cfg sramcapture.Config) int {
	if !cfg.Symbol.Enabled {
		return -1
	}
	v, err := e.sram.SymbolMatches()
	if err != nil {
		return -1
	}
	return int(v)
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
