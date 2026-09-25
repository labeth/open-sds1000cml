// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"open-sds/app/internal/dsp"
	"open-sds/app/internal/sramcapture"
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

// TRLC-LINKS: REQ-SDS-040
func qBuffer(q []uint16, n int) []uint16 {
	if cap(q) < n {
		return make([]uint16, n)
	}
	return q[:n]
}

// TRLC-LINKS: REQ-SDS-010, REQ-SDS-011, REQ-SDS-036, REQ-SDS-040
func (e *Engine) sramConfig() (sramcapture.Config, SRAMPlan, float64, bool, trigParams) {
	e.mu.Lock()
	tdiv := e.band.TdivS
	if e.pendSet {
		tdiv = e.pendBand.TdivS
	}
	norm, tp := e.norm, e.tp
	e.mu.Unlock()
	p := PlanSRAM(tdiv)
	if e.acqMode.Load() == AcqPrecision {
		p = PlanPrecisionSRAM(tdiv, uint8(e.precisionLog.Load()))
	}
	samples := p.Samples // live-preview mode always retains the full SRAM record
	perWord := 2 - boolInt(p.Log != 0)
	words := uint32((samples + perWord - 1) / perWord)
	p.Samples = int(words) * perWord
	pre := uint32(float64(words) * math.Float64frombits(e.trigPosFrac.Load()))
	if pre >= words {
		pre = words - 1
	}
	c := sramcapture.Config{Source: sramcapture.ADC, DecimationLog2: p.Log, PreWords: pre, PostWords: words - pre,
		Normal: tp.typ == TrigEdge && e.serialMode.Load() != SerialTrigger, Falling: !e.trigRising.Load(),
		TriggerChannel: uint8(e.trigSrc.Load()), TriggerLevel: uint8(e.trigLevelWord())}
	if (e.hardwareUART || e.hardwareI2C || e.hardwareSPI || e.hardwareSENT || e.hardwareMIL1553 || e.hardwareUSBLS) && e.serialMode.Load() == SerialTrigger {
		e.ser.mu.Lock()
		serial := e.ser.params
		e.ser.mu.Unlock()
		if e.hardwareUSBLS {
			c.USBLS = planHardwareUSBLS(serial)
		}
		if e.hardwareMIL1553 {
			c.MIL1553 = planHardwareMIL1553(serial)
		}
		if e.hardwareSENT {
			c.SENT = planHardwareSENT(serial)
		}
		if e.hardwareUART {
			c.UART = planHardwareUART(serial)
		}
		if e.hardwareSPI {
			c.SPI = planHardwareSPI(serial)
		}
		if e.hardwareI2C {
			c.I2C = planHardwareI2C(serial)
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
			c.TriggerLevel = uint8(math.Round(serial.Threshold))
		}
		if c.I2C.Enabled {
			c.Normal = true
			c.TriggerChannel = c.I2C.ClockChannel
			c.TriggerLevel = uint8(math.Round(serial.Threshold))
		}
	}
	// The fabric sees raw interleaved codes, before display calibration. Its
	// re-arm band must span the measured converter offsets plus quantization
	// noise, otherwise a calibrated-looking falling trace can trigger rising.
	c.TriggerHysteresis = 4
	if cal := e.interleaveCalibration; cal != nil && (p.Log == 0 || c.UART.Enabled || c.I2C.Enabled || c.SPI.Enabled || c.SENT.Enabled || c.MIL1553.Enabled || c.USBLS.Enabled) {
		scale := cal.Vdiv
		scale[c.TriggerChannel] = math.Float64frombits(e.chVdivBits[c.TriggerChannel].Load())
		if cal.supportsScale(scale) {
			offsets := cal.Offset[c.TriggerChannel]
			lo, hi := offsets[0], offsets[0]
			for _, v := range offsets[1:] {
				lo = math.Min(lo, v)
				hi = math.Max(hi, v)
			}
			c.TriggerHysteresis = uint8(math.Min(255, math.Max(4, math.Ceil(hi-lo)+2)))
			if c.I2C.Enabled || c.SPI.Enabled {
				other := 1 - c.TriggerChannel
				scale[other] = math.Float64frombits(e.chVdivBits[other].Load())
				if cal.supportsScale(scale) {
					lo, hi = cal.Offset[other][0], cal.Offset[other][0]
					for _, v := range cal.Offset[other] {
						lo = math.Min(lo, v)
						hi = math.Max(hi, v)
					}
					c.TriggerHysteresis = uint8(math.Min(255, math.Max(float64(c.TriggerHysteresis), math.Ceil(hi-lo)+2)))
				}
			}
		}
	}

	return c, p, tdiv, norm, tp
}

// TRLC-LINKS: REQ-SDS-013
func planHardwareUART(p SerialParams) sramcapture.UARTTriggerConfig {
	// Hardware must not silently substitute the edge-trigger level for the
	// decoder's automatic threshold. Auto slicing remains a software request.
	if !p.HaveThr || p.Proto != serUART || p.Baud <= 0 || (p.Bits != 0 && p.Bits != 8) ||
		(p.Parity != "" && p.Parity != "none") || len(p.Bytes) > 4 || p.ChA < 0 || p.ChA > 1 {
		return sramcapture.UARTTriggerConfig{}
	}
	if p.HaveThr && (math.IsNaN(p.Threshold) || math.IsInf(p.Threshold, 0) || p.Threshold < 0 || p.Threshold > 255) {
		return sramcapture.UARTTriggerConfig{}
	}
	ticks := math.Round(125e6 / float64(p.Baud))
	if ticks < 4 || ticks > 0xffffff {
		return sramcapture.UARTTriggerConfig{}
	}
	c := sramcapture.UARTTriggerConfig{Enabled: true, Channel: uint8(p.ChA), Inverted: p.Inverted, BitTicks: uint32(ticks), Length: uint8(len(p.Bytes))}
	for _, b := range p.Bytes {
		if b < 0 || b > 255 {
			return sramcapture.UARTTriggerConfig{}
		}
		c.Pattern = c.Pattern<<8 | uint32(b)
	}
	return c
}

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
	}()
	var capabilityErr error
	e.hardwareUART, capabilityErr = e.sram.SupportsUART()
	if capabilityErr != nil {
		e.logf("hardware UART capability: %v; using software", capabilityErr)
	}
	e.hardwareI2C, capabilityErr = e.sram.SupportsI2C()
	if capabilityErr != nil {
		e.logf("hardware I2C capability: %v; using software", capabilityErr)
	}
	e.hardwareSPI, capabilityErr = e.sram.SupportsSPI()
	if capabilityErr != nil {
		e.logf("hardware SPI capability: %v; using software", capabilityErr)
	}
	e.hardwareSENT, capabilityErr = e.sram.SupportsSENT()
	if capabilityErr != nil {
		e.logf("hardware SENT capability: %v; using software", capabilityErr)
	}
	e.hardwareUSBLS, capabilityErr = e.sram.SupportsUSBLS()
	if capabilityErr != nil {
		e.logf("hardware USB capability: %v; using software", capabilityErr)
	}
	e.hardwareMIL1553, capabilityErr = e.sram.SupportsMIL1553()
	if capabilityErr != nil {
		e.logf("hardware MIL-STD-1553 capability: %v; using software", capabilityErr)
	}
	e.logf("engine: full-depth SRAM capture; software protocol qualification has capture dead time")
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
acquisitionLoop:
	for !e.stopReq.Load() {
		e.serviceCommands()
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
			}
			continue
		}
		e.bumpFrames()
		e.mu.Lock()
		viewPending := e.pendSet
		e.mu.Unlock()
		if !e.running.Load() && (!retained || (recalled && !viewPending)) {
			e.clk.Sleep(20 * time.Millisecond)
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
		e.stats.SerialBackend = "software"
		if cfg.UART.Enabled {
			e.stats.SerialBackend = "hardware-uart"
		}
		if cfg.I2C.Enabled {
			e.stats.SerialBackend = "hardware-i2c"
		}
		if cfg.SENT.Enabled {
			e.stats.SerialBackend = "hardware-sent"
		}
		if cfg.USBLS.Enabled {
			e.stats.SerialBackend = "hardware-usbls"
		}
		if cfg.MIL1553.Enabled {
			e.stats.SerialBackend = "hardware-mil1553"
		}
		if cfg.SPI.Enabled {
			e.stats.SerialBackend = "hardware-spi"
		}
		e.stats.TdivS = tdiv
		e.stats.DisplayedS = math.Min(tdiv, float64(plan.Samples)*plan.SampleS/10)
		e.stats.WinCols = plan.Screen
		e.mu.Unlock()
		armAt := e.clk.Now()
		var ctx context.Context
		var cancel context.CancelFunc
		var err error
		forced, aborted := savedForced, false
		m := savedM
		if !replay {
			retained = false
			ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
			err = e.sram.Arm(ctx, cfg)
			cancel()
			if err != nil {
				e.busErr(err)
				e.sleepBeating(100 * time.Millisecond)
				continue
			}
			forced = false
			// AUTO waits for enough prehistory plus a bounded event wait. SINGLE/NORM
			// never fabricate a trigger. Long waits continue servicing controls.
			autoWait := 100*time.Millisecond + time.Duration(float64(cfg.PreWords)*float64(2-boolInt(plan.Log != 0))*plan.SampleS*1e9)
			for !e.stopReq.Load() {
				e.serviceCommands()
				if e.decodedSession != nil {
					continue acquisitionLoop
				}
				e.beatN.Add(1)
				next, _, nextTdiv, nextNorm, nextTP := e.sramConfig()
				if next != cfg || nextTdiv != tdiv || nextNorm != norm || nextTP != tp {
					aborted = true
					break
				}
				m, err = e.sram.Status()
				if err != nil || m.DataFault {
					break
				}
				if m.Frozen && m.Ready {
					break
				}
				if cfg.Normal && (!norm || !e.running.Load()) && !forced && e.clk.Now().Sub(armAt) >= autoWait {
					ctx, cancel = context.WithTimeout(context.Background(), time.Second)
					err = e.sram.Force(ctx)
					cancel()
					forced = true
					if err != nil {
						break
					}
				}
				e.clk.Sleep(time.Millisecond)
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
		savedM, savedCfg, savedPlan, savedNorm, savedTP, savedForced = m, cfg, plan, norm, tp, forced
		fullRecall := !e.running.Load()
		retained, recalled = true, fullRecall
		recallOffset, recallWords := uint32(0), m.Length
		if !fullRecall {
			recallOffset, recallWords = liveSRAMWindow(m, plan.Screen)
		}
		frozenAt := e.clk.Now()
		f := e.arena.Write()
		q1, q2 := f.Q1, f.Q2
		c1, c2 := f.C1, f.C2
		*f = Frame{C1: c1, C2: c2, Valid: int(recallWords) * int(m.SamplesPerWord), WinCols: plan.Screen, SampleS: 1 / m.SampleRateHz, TdivS: tdiv, DisplayedS: math.Min(tdiv, float64(plan.Samples)*plan.SampleS/10),
			EdgeX: float64(m.TriggerIndex-recallOffset) * float64(m.SamplesPerWord), TrigPos: int(m.TriggerIndex-recallOffset) * int(m.SamplesPerWord),
			Trigd: m.Triggered && cfg.Normal && !forced, Coherent: true, HaltOK: true, Norm: norm, Interp: plan.Screen < 800, Decimation: m.Decimation, TriggerKind: "hardware-edge"}
		f.CaptureDepth = int(m.Length) * int(m.SamplesPerWord)
		if !f.Trigd {
			f.TriggerKind = "forced"
			f.EdgeX = -1
		}
		if m.FractionBits == 8 {
			f.Q1 = qBuffer(q1, f.Valid)
			f.Q2 = qBuffer(q2, f.Valid)
		}
		writer := sramFrameWriter{f: f, m: m}
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		recall := e.sram.Recall
		if m.Revision == 10 {
			recall = e.sram.RecallForward
		}
		recallAt := e.clk.Now()
		_, err = recall(ctx, recallOffset, recallWords, &writer)
		recalledAt := e.clk.Now()
		cancel()
		if err != nil || writer.n != f.Valid {
			if err == nil {
				err = fmt.Errorf("short SRAM frame")
			}
			e.busErr(err)
			continue
		}
		if len(f.Q1) == f.Valid && len(f.Q2) == f.Valid {
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
		if m.FractionBits == 0 && e.interleaveCalibration != nil {
			q1 = qBuffer(q1, f.Valid)
			q2 = qBuffer(q2, f.Valid)
			scale := [2]float64{math.Float64frombits(e.chVdivBits[0].Load()), math.Float64frombits(e.chVdivBits[1].Load())}
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
		if fullRecall {
			f.Filter += "; stopped full record (single acquisition)"
		}
		conditionedAt := e.clk.Now()
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
		if tp.typ != TrigEdge && e.serialMode.Load() != SerialTrigger {
			switch tp.typ {
			case TrigPulse:
				f.EdgeX = qualifyPulse(disc, f.SampleS*1e9, tp, !cfg.Falling)
			case TrigSlope:
				f.EdgeX = qualifySlope(disc, f.SampleS*1e9, tp, !cfg.Falling)
			case TrigVideo:
				f.EdgeX = qualifyVideo(disc, tp)
			}
			qualified = f.EdgeX >= 0
			f.Trigd = qualified
			f.TriggerKind = "software-window"
		}
		if e.serialMode.Load() == SerialTrigger && (cfg.UART.Enabled || cfg.I2C.Enabled || cfg.SPI.Enabled || cfg.SENT.Enabled || cfg.MIL1553.Enabled || cfg.USBLS.Enabled) {
			qualified = f.Trigd
			f.TriggerKind = "hardware-uart"
			if cfg.USBLS.Enabled {
				f.TriggerKind = "hardware-usbls"
			}
			if cfg.MIL1553.Enabled {
				f.TriggerKind = "hardware-mil1553"
			}
			if cfg.SENT.Enabled {
				f.TriggerKind = "hardware-sent"
			}
			if cfg.I2C.Enabled {
				f.TriggerKind = "hardware-i2c"
			}
			if cfg.SPI.Enabled {
				f.TriggerKind = "hardware-spi"
			}
			if qualified {
				e.serialMatches.Add(1)
			}
		} else if e.serialMode.Load() == SerialTrigger {
			matched, anchor := e.serialQualify(f, f.Valid, f.SampleS)
			qualified = matched
			f.Trigd = matched
			f.TriggerKind = "software-window"
			if matched {
				f.EdgeX = float64(anchor)
				e.serialMatches.Add(1)
			} else {
				f.EdgeX = -1
			}
		}
		if e.zoneMode.Load() == ZoneTrigger {
			qualified = qualified && e.zonesQualify(f, f.Valid, f.EdgeX, f.SampleS)
			f.Trigd = qualified
		}
		averageDone := true
		if (e.acqMode.Load() == AcqAverage || e.acqMode.Load() == AcqPrecision) && qualified && !fullRecall {
			n := int(e.avgCount.Load())
			key := fmt.Sprintf("%v/%g/%t/%v/%d/%d/%d/%d/%d/%s", cfg, tdiv, norm, tp, e.avgGen.Load(), e.chVdivBits[0].Load(), e.chVdivBits[1].Load(), e.trigOffV[0].Load(), e.trigOffV[1].Load(), f.Filter)
			// Refine the word-level trigger with the observed waveform crossing.
			lo, hi := int(f.EdgeX)-4096, int(f.EdgeX)+4096
			if lo < 0 {
				lo = 0
			}
			if hi > f.Valid {
				hi = f.Valid
			}
			edge := -1.0
			if hi > lo {
				local := disc[lo:hi]
				edge = centerCrossHint(local, midLevel(local), !cfg.Falling, f.EdgeX-float64(lo))
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
			f.Filter += fmt.Sprintf("; block average %d/%d (waveform aligned)", count, n)
			if count > 0 {
				if f.NoiseGainIdeal == 0 {
					f.NoiseGainIdeal = 1
				}
				f.NoiseGainIdeal *= math.Sqrt(float64(count))
			}
			averageDone = count >= n
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
		e.stats.ConditionMs = float64(conditionedAt.Sub(recalledAt)) / float64(time.Millisecond)
		e.stats.DrainMs = float64(e.clk.Now().Sub(frozenAt)) / float64(time.Millisecond)
		e.mu.Unlock()
		publish := qualified || !norm || fullRecall
		if publish {
			e.seq++
			f.Seq = e.seq
			e.arena.Publish()
			e.mu.Lock()
			e.stats.Published++
			e.stats.Seq = e.seq
			e.lastPubAt = e.clk.Now()
			e.pubTimes = append(e.pubTimes, e.lastPubAt)
			if len(e.pubTimes) > 64 {
				e.pubTimes = e.pubTimes[len(e.pubTimes)-64:]
			}
			e.mu.Unlock()
			if qualified && averageDone && e.singleArmed.Load() && !fullRecall {
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
		if qualified {
			e.sleepBeating(time.Duration(e.holdoffNs.Load()))
		}
	}
}

// TRLC-LINKS: REQ-SDS-040
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// liveSRAMWindow selects a contiguous trigger-centred preview. At timebases
// needing more samples, retain the whole visible span instead of aliasing it.
// TRLC-LINKS: REQ-SDS-010
func liveSRAMWindow(m sramcapture.Metadata, screen int) (uint32, uint32) {
	per := int(m.SamplesPerWord)
	samples := max(2048, screen+512)
	if m.FractionBits == 8 {
		samples = max(256, screen+128)
	}
	words := min(m.Length, uint32((samples+per-1)/per))
	offset := uint32(0)
	if m.TriggerIndex > words/2 {
		offset = m.TriggerIndex - words/2
	}
	if offset+words > m.Length {
		offset = m.Length - words
	}
	return offset, words
}

// A stopped timebase change reuses the captured sample pitch and depth.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func retainedSRAMPlan(p SRAMPlan, tdiv float64) SRAMPlan {
	p.Screen = int(math.Min(float64(p.Samples), math.Max(2, math.Round(10*tdiv/p.SampleS))))
	return p
}
