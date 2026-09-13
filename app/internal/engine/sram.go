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
type SRAMPlan struct {
	Log             uint8
	Samples, Screen int
	SampleS         float64
}

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

type sramFrameWriter struct {
	f *Frame
	m sramcapture.Metadata
	n int
}

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
func roundQ8(x uint16) uint8 {
	v := (uint32(x) + 128) >> 8
	if v > 255 {
		v = 255
	}
	return uint8(v)
}
func qBuffer(q []uint16, n int) []uint16 {
	if cap(q) < n {
		return make([]uint16, n)
	}
	return q[:n]
}

func (e *Engine) sramConfig() (sramcapture.Config, SRAMPlan, float64, bool, trigParams) {
	e.mu.Lock()
	tdiv := e.band.TdivS
	if e.pendSet {
		tdiv = e.pendBand.TdivS
	}
	norm, tp := e.norm, e.tp
	e.mu.Unlock()
	p := PlanSRAM(tdiv)
	pre := uint32(float64(sramcapture.Words) * math.Float64frombits(e.trigPosFrac.Load()))
	if pre >= sramcapture.Words {
		pre = sramcapture.Words - 1
	}
	c := sramcapture.Config{Source: sramcapture.ADC, DecimationLog2: p.Log, PreWords: pre, PostWords: sramcapture.Words - pre,
		Normal: tp.typ == TrigEdge && e.serialMode.Load() != SerialTrigger, Falling: !e.trigRising.Load(),
		TriggerChannel: uint8(e.trigSrc.Load()), TriggerLevel: uint8(e.trigLevelWord())}
	return c, p, tdiv, norm, tp
}

// runSRAM exclusively owns the revisioned SRAM backend. It never writes a
// legacy CS1 selector. Fast capture is explicitly freeze/read/rearm; a frozen
// window's software protocol match is not advertised as a gapless trigger.
func (e *Engine) runSRAM() {
	e.logf("engine: full-depth SRAM capture; software protocol qualification has capture dead time")
	var previousFrozen time.Time
	var scratch []uint16
	for !e.stopReq.Load() {
		e.serviceCommands()
		e.bumpFrames()
		if !e.running.Load() {
			e.clk.Sleep(20 * time.Millisecond)
			continue
		}
		cfg, plan, tdiv, norm, tp := e.sramConfig()
		e.mu.Lock()
		if e.pendSet {
			e.band = e.pendBand
			e.pendSet = false
		}
		e.stats.BandKind = "sram"
		e.stats.HaltMode = "freeze-read-rearm"
		e.stats.TdivS = tdiv
		e.stats.DisplayedS = tdiv
		e.stats.WinCols = plan.Screen
		e.mu.Unlock()
		armAt := e.clk.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := e.sram.Arm(ctx, cfg)
		cancel()
		if err != nil {
			e.busErr(err)
			e.sleepBeating(100 * time.Millisecond)
			continue
		}
		forced := false
		aborted := false
		var m sramcapture.Metadata
		// AUTO waits for enough prehistory plus a bounded event wait. SINGLE/NORM
		// never fabricate a trigger. Long waits continue servicing controls.
		autoWait := 100*time.Millisecond + time.Duration(float64(cfg.PreWords)*float64(2-boolInt(plan.Log != 0))*plan.SampleS*1e9)
		for !e.stopReq.Load() {
			e.serviceCommands()
			e.beatN.Add(1)
			next, _, _, nextNorm, nextTP := e.sramConfig()
			if !e.running.Load() || next != cfg || nextNorm != norm || nextTP != tp {
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
			if cfg.Normal && !norm && !forced && e.clk.Now().Sub(armAt) >= autoWait {
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
		frozenAt := e.clk.Now()
		f := e.arena.Write()
		q1, q2 := f.Q1, f.Q2
		c1, c2 := f.C1, f.C2
		*f = Frame{C1: c1, C2: c2, Valid: int(m.Length) * int(m.SamplesPerWord), WinCols: plan.Screen, SampleS: 1 / m.SampleRateHz, TdivS: tdiv, DisplayedS: tdiv,
			EdgeX: float64(m.TriggerIndex) * float64(m.SamplesPerWord), TrigPos: int(m.TriggerIndex) * int(m.SamplesPerWord),
			Trigd: m.Triggered && cfg.Normal && !forced, Coherent: true, HaltOK: true, Norm: norm, Interp: plan.Screen < 800, Decimation: m.Decimation, TriggerKind: "hardware-edge"}
		f.CaptureDepth = f.Valid
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
		_, err = recall(ctx, 0, m.Length, &writer)
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
			f.BandwidthHz = .075 / f.SampleS
			f.Filter = "CIC3 + compensated FIR"
			for i := 0; i < f.Valid; i++ {
				f.C1[i] = roundQ8(f.Q1[i])
				f.C2[i] = roundQ8(f.Q2[i])
			}
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
		if tp.typ != TrigEdge {
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
		if e.serialMode.Load() == SerialTrigger {
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
		e.mu.Lock()
		e.stats.Coherent++
		e.stats.HaltConfirm++
		e.stats.ValidDepth = f.Valid
		e.stats.MemDepth = f.Valid
		e.stats.LastPtp = f.Ptp
		e.stats.DrainMs = float64(e.clk.Now().Sub(frozenAt)) / float64(time.Millisecond)
		e.mu.Unlock()
		publish := qualified || !norm
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
			if qualified && e.singleArmed.Load() {
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
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
