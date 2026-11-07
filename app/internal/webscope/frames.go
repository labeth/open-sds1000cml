// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"math"
	"time"

	"github.com/labeth/open-dso-core/dsp"
	"github.com/labeth/open-dso-core/measure"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-dso-core/webserver/shape"
	"github.com/labeth/open-dso-core/webserver/wire"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// vertScales returns the applied offset volts and volts-per-code (Vdiv/25 —
// the 25-codes/div render scale, spec 10 §7.1) for each channel, using the
// front-end V/div when available.
// TRLC-LINKS: REQ-SDS-164
func (s *Scope) vertScales() (off, vpc [2]float64) {
	vpc = [2]float64{1.0 / 25, 1.0 / 25} // nominal 1 V/div when no front end
	st := s.sc.Snapshot()
	if s.fe != nil {
		// Volts per code on the ANALOG range: the 2 and 5 mV detents are display
		// zooms of the 10 mV range (spec 06), and the page applies the zoom.
		vpc[0] = s.fe.RangeV(0) / 25
		vpc[1] = s.fe.RangeV(1) / 25
		off[0], off[1] = s.lv.OffsetIn(0, st), s.lv.OffsetIn(1, st)
		// Probe attenuation is input-referred: it scales both the per-code
		// volts and the offset so every downstream readout (measurements,
		// cursors, CSV, ground marker) reports the signal at the probe tip.
		p0, p1 := s.fe.ProbeFactor(0), s.fe.ProbeFactor(1)
		vpc[0] *= p0
		vpc[1] *= p1
		off[0] *= p0
		off[1] *= p1
	}
	return off, vpc
}

// buildReply assembles the frame reply for /api/frame.bin (binary header + raw
// payload). MUST run inside Engine.WithFrame (f is only valid under the
// fan-out read lock); the returned reply owns all its data. measThrottle
// permits reusing ≤100 ms-old measurements on a seq advance (fast free-run
// only — pass false for single-shot/stopped, where values must be exact).
// TRLC-LINKS: REQ-SDS-163, REQ-SDS-164
func (s *Scope) buildReply(f *sample.Frame, cols int, full bool, since uint64, off, vpc [2]float64, posFrac float64, measThrottle bool) wire.FrameReply {
	if f == nil || f.Seq == 0 || f.Seq == since {
		seq := uint64(0)
		if f != nil {
			seq = f.Seq
		}
		return wire.FrameReply{Seq: seq, Unchanged: true, EdgeX: -1}
	}
	// Clamp Valid to what the sample slices actually hold. Valid > len is an
	// engine-invariant violation, but every downstream f.C1[:f.Valid] would
	// panic the serve goroutine and crash the UI on one bad frame (frame-serve
	// fuzz). Clamp ONCE here so every path below is safe. Work on a shallow
	// copy with traces of its own: the fan-out snapshot is shared read-only
	// across clients.
	if f.Len() > len(f.Codes8(0)) || f.Len() > len(f.Codes8(1)) {
		fc := *f
		fc.Traces = append([]sample.Trace(nil), f.Traces...)
		if fc.Len() > len(fc.Codes8(0)) {
			fc.SetLen(len(fc.Codes8(0)))
		}
		if fc.Len() > len(fc.Codes8(1)) {
			fc.SetLen(len(fc.Codes8(1)))
		}
		f = &fc
	}
	// Coupling display transform (software on this clone, spec 06 §6): AC
	// removes the DC (mean → mid-scale), GND shows a flat ground trace; both
	// drop that channel's offset so the baseline sits at centre. DC passes
	// through. Envelope frames are left untouched.
	c1, c2 := f.Codes8(0), f.Codes8(1)
	moff := off
	cpl := [2]int{analog.CplDC, analog.CplDC}
	if s.fe != nil && f.Len() > 0 && !f.Flags.Has(sample.Envelope) {
		cpl[0], cpl[1] = s.fe.Coupling(0), s.fe.Coupling(1)
		if cpl[0] != analog.CplDC {
			c1, moff[0] = dsp.CoupleDisplay(f.Codes8(0)[:f.Len()], cpl[0]), 0
		}
		if cpl[1] != analog.CplDC {
			c2, moff[1] = dsp.CoupleDisplay(f.Codes8(1)[:f.Len()], cpl[1]), 0
		}
	}
	rep := wire.FrameReply{
		Seq:            f.Seq,
		WinCols:        min(f.WinPoints, f.Len()),
		SampleS:        f.SampleS,
		NoiseGainIdeal: f.Cond.NoiseGain, PassbandHz: f.Cond.PassbandHz, BandwidthHz: f.Cond.BandwidthHz, FilterGuard: f.Cond.Guard, Filter: f.Cond.Filter, Decimation: uint32(f.AcqDecim), FullRate: f.Content == sample.RawFullRate, CaptureDepth: f.Depth, TriggerKind: f.TriggerName(),
		EdgeX:          f.TriggerAt,
		Ptp:            f.SourcePtp,
		TdivS:          f.Snapshot.TdivS,
		DisplayedS:     f.Snapshot.DisplayedS,
		Interp:         f.Flags.Has(sample.Interp),
		Norm:           f.Snapshot.Norm,
		Trigd:          f.Status.Triggered,
		Coherent:       f.Status.Coherent,
		Degraded:       f.Status.Degraded,
		PeakDetect:     f.Content == sample.PeakPairs,
		Ordered:        f.Flags.Has(sample.Ordered),
		CaptureSampleS: captureSampleS(f),
		Cols:           cols,
		ColSpanS:       f.Snapshot.DisplayedS * 10, // the window spans 10 divisions
		Vpc1:           vpc[0], Vpc2: vpc[1],
		Off1V: moff[0], Off2V: moff[1],
	}
	// ColSpanS stays the 10-div screen time except on the deep path below,
	// which overrides it to the full-record time.
	switch {
	case f.Flags.Has(sample.Envelope):
		rep.IsEnv = true
		rep.E1Min = shape.ResampleEnv(f.Min8(0), len(f.Min8(0)), cols)
		rep.E1Max = shape.ResampleEnv(f.Max8(0), len(f.Min8(0)), cols)
		rep.E2Min = shape.ResampleEnv(f.Min8(1), len(f.Min8(0)), cols)
		rep.E2Max = shape.ResampleEnv(f.Max8(1), len(f.Min8(0)), cols)
		rep.EdgeFrac, rep.WinFrac = -1, 1
	case full && (f.Depth > 0 || (!f.Flags.Has(sample.Interp) && f.Len() > f.WinPoints)):
		// DECIMATED deep memory: serve the full drained record VERBATIM (NOT
		// re-centered) and report the trigger's REAL position (edge_frac =
		// EdgeX/n). The web centers/windows it client-side, so the display, the
		// super-res gate and the raw-fed stacker all share ONE coordinate system —
		// the raw record. (Server-side re-centering put the display in a different,
		// edge-shifted, -1-margin-padded frame than the stacker's raw feed, so a
		// gate dragged on the display stacked a different region.) The record is
		// HW-trigger-anchored (phase-stable), so the client homes it ONCE per
		// acquisition instead of chasing the edge — see applyFrame. col_span_s is
		// the whole-record time so client Nyquist/Δt/decode/CSV stay consistent.
		n := f.Len()
		rep.C1, rep.C2 = shape.RawInt16(c1[:n]), shape.RawInt16(c2[:n])
		if len(f.Precision(0)) == n && len(f.Precision(1)) == n {
			rep.Q1 = shape.CoupleQ8(f.Precision(0), cpl[0])
			rep.Q2 = shape.CoupleQ8(f.Precision(1), cpl[1])
			rep.FractionBits = 8
		}
		if f.TriggerAt >= 0 {
			rep.EdgeFrac = f.TriggerAt / float64(n)
		} else {
			rep.EdgeFrac = -1
		}
		rep.Cols, rep.ColSpanS, rep.Depth = n, float64(n)*f.SampleS, n
		rep.DtS = f.SampleS
		rep.WinFrac = float64(f.WinPoints) / float64(n)
		rep.Held = shape.HeldRanges(f.Held(0), n)
		if d := engine.ViewDetail(f, engine.ViewWeb); d != nil && f.Content == sample.PeakPairs && n > 1 {
			v, vn := d.Frame, d.Frame.Len()
			if vn > 1 && vn <= len(v.Codes8(0)) && vn <= len(v.Codes8(1)) && d.To > d.From {
				last := d.To - (d.To-d.From)/float64(vn) // where its last value lies
				rep.ViewA, rep.ViewB, rep.ViewN = d.From/float64(n-1), last/float64(n-1), vn
				rep.VC1 = shape.RawInt16(dsp.CoupleDisplayBy(v.Codes8(0)[:vn], f.Codes8(0)[:n], cpl[0]))
				rep.VC2 = shape.RawInt16(dsp.CoupleDisplayBy(v.Codes8(1)[:vn], f.Codes8(1)[:n], cpl[1]))
			}
		}
	default:
		// Native-fast / non-deep decimated: today's windowed screen slice.
		rep.C1 = shape.Window(c1, f.Len(), f.WinPoints, f.TriggerAt, f.Flags.Has(sample.Interp), cols, posFrac)
		rep.C2 = shape.Window(c2, f.Len(), f.WinPoints, f.TriggerAt, f.Flags.Has(sample.Interp), cols, posFrac)
		// True per-column time: the window really spans min(WinCols, Valid)
		// hardware samples (mirrors window()'s clamp) at SampleS each — NOT
		// col_span_s/cols, which is nominal on the 1–200 ns/div bands.
		if win := f.WinPoints; f.Len() >= 1 {
			if win > f.Len() {
				win = f.Len()
			}
			rep.DtS = float64(win) * f.SampleS / float64(cols)
		}
		rep.WinFrac = 1
		if f.TriggerAt >= 0 {
			rep.EdgeFrac = posFrac
		} else {
			rep.EdgeFrac = -1
		}
	}
	rep.StreamSeq, rep.WindowNs, rep.GapNs = f.Stream.Seq, int64(math.Round(f.Stream.WindowS*1e9)), int64(math.Round(f.Stream.GapS*1e9))
	if f.Match != nil && rep.EdgeFrac >= 0 {
		end := f.Match.EndS
		rep.MatchEndS = &end
	}
	// Auto-measurements over the RAW record (accurate, band-independent),
	// computed once per published frame via the value-keyed cache: repeat
	// requests for the same seq (JSON + binary, or several clients) reuse it.
	key := shape.MeasKey{Seq: f.Seq, Cpl1: cpl[0], Cpl2: cpl[1], Vpc: vpc, Off: moff, SampleS: f.SampleS}
	mv := s.meas.Get(key, measThrottle, func() shape.MeasVal {
		a, b := engine.MeasureRange(f) // a roll view's captured stretch
		qa := func(q []uint16) []uint16 {
			if len(q) < b {
				return nil
			}
			return q[a:b]
		}
		v := shape.MeasVal{
			M1:    measure.ComputeAcquisition(c1[a:b], qa(f.Precision(0)), vpc[0], moff[0], f.SampleS, cpl[0], f.Cond.Guard),
			M2:    measure.ComputeAcquisition(c2[a:b], qa(f.Precision(1)), vpc[1], moff[1], f.SampleS, cpl[1], f.Cond.Guard),
			Clip1: measure.Clipped(f.Codes8(0)[:f.Len()]), // RAW rail state (pre-coupling)
			Clip2: measure.Clipped(f.Codes8(1)[:f.Len()]),
			Seq:   f.Seq, EdgeRead: engine.EdgeRead(f),
		}
		if f.Content == sample.PeakPairs {
			v.M1 = v.M1.WithEnvelopeTiming(c1[:f.Len()], vpc[0], moff[0], f.SampleS).WithEdgeDetail(vpc[0], moff[0], engine.EdgeWindows(f, 0)...)
			v.M2 = v.M2.WithEnvelopeTiming(c2[:f.Len()], vpc[1], moff[1], f.SampleS).WithEdgeDetail(vpc[1], moff[1], engine.EdgeWindows(f, 1)...)
		}
		return v
	})
	rep.M1, rep.M2, rep.Clip1, rep.Clip2 = mv.M1, mv.M2, mv.Clip1, mv.Clip2
	rep.MeasSeq, rep.EdgeRead = mv.Seq, mv.EdgeRead
	if s.invSrc != nil { // measure what the inverted trace shows (SCPI Cn:INVS)
		inv := s.invSrc()
		if inv[0] {
			rep.M1 = rep.M1.Inverted()
		}
		if inv[1] {
			rep.M2 = rep.M2.Inverted()
		}
	}
	return rep
}

// captureSampleS reports a frame's converter interval when it is not sample_s.
// TRLC-LINKS: REQ-SDS-163
func captureSampleS(f *sample.Frame) float64 {
	if f.Content == sample.PeakPairs && f.CaptureS > 0 && f.CaptureS != f.SampleS {
		return f.CaptureS
	}
	return 0
}

// Frames is the scope's frame source for /api/frame.bin.
// TRLC-LINKS: REQ-SDS-163
func (s *Scope) Frames() webserver.Frames { return frames{s} }

// frames serves the fan-out's frames; its quiet lock is the engine's.
type frames struct{ s *Scope }

// Wait parks until the fan-out snapshots a frame after since, or timeout.
// TRLC-LINKS: REQ-SDS-163
func (fr frames) Wait(since uint64, timeout time.Duration) { fr.s.waitFrame(since, timeout) }

// QuietRLock blocks during the engine's load-sensitive windows (settle+drain).
// TRLC-LINKS: REQ-SDS-163
func (fr frames) QuietRLock() { fr.s.sc.QuietRLock() }

// QuietRUnlock releases QuietRLock.
// TRLC-LINKS: REQ-SDS-163
func (fr frames) QuietRUnlock() { fr.s.sc.QuietRUnlock() }

// Frame builds the reply for q from the newest frame, with the current
// scales and trigger position; measurements reuse the previous frame's for
// 100 ms while running free (not on a single capture or stopped).
// TRLC-LINKS: REQ-SDS-163, REQ-SDS-164
func (fr frames) Frame(q webserver.FrameQuery) wire.FrameReply {
	s := fr.s
	off, vpc := s.vertScales()
	st := s.sc.Snapshot()
	posFrac := st.TrigPosFrac
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = 0.5
	}
	var rep wire.FrameReply
	s.sc.WithFrame(func(f *sample.Frame) {
		rep = s.buildReply(f, q.Cols, q.Full, q.Since, off, vpc, posFrac, st.Running && !st.Single)
	})
	return rep
}

// RawFrame builds the raw shape (rawFrame).
// TRLC-LINKS: REQ-SDS-163
func (fr frames) RawFrame(since uint64) wire.RawFrame { return fr.s.rawFrame(since) }

// Lease renews or ends one of the engine's frame leases: raw (decode view),
// spectrum, samples, search, or the web's view window.
// TRLC-LINKS: REQ-SDS-163
func (fr frames) Lease(n webserver.Need) bool {
	switch n.Kind {
	case webserver.NeedRaw:
		if sc, ok := fr.s.sc.(interface{ LeaseDecodeView(time.Duration) }); ok {
			sc.LeaseDecodeView(n.Hold)
			return true
		}
	case webserver.NeedSpectrum:
		if sc, ok := fr.s.sc.(interface{ LeaseSpectrumView(time.Duration) }); ok {
			sc.LeaseSpectrumView(n.Hold)
			return true
		}
	case webserver.NeedSamples:
		if sc, ok := fr.s.sc.(interface{ LeaseSampleView(time.Duration) }); ok {
			sc.LeaseSampleView(n.Hold)
			return true
		}
	case webserver.NeedSearch:
		if sc, ok := fr.s.sc.(interface{ LeaseSearchView(time.Duration) }); ok {
			sc.LeaseSearchView(n.Hold)
			return true
		}
	case webserver.NeedView:
		if sc, ok := fr.s.sc.(interface {
			LeaseViewWindow(engine.ViewSurface, float64, float64, int, time.Duration)
		}); ok {
			sc.LeaseViewWindow(engine.ViewWeb, n.View.A, n.View.B, n.View.Cols, n.Hold)
			return true
		}
	}
	return false
}
