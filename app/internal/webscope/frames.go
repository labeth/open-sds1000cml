// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"time"

	"github.com/labeth/open-dso-core/measure"
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
		idx, _ := s.fe.Snapshot()
		// Volts per code on the ANALOG range: the 2 and 5 mV detents are display
		// zooms of the 10 mV range (spec 06), and the page applies the zoom.
		vpc[0] = analog.Vdiv(idx[0]) / 25
		vpc[1] = analog.Vdiv(idx[1]) / 25
		if st.OffC1 != 0 {
			off[0] = s.fe.OffsetVolts(0, st.OffC1)
		}
		if st.OffC2 != 0 {
			off[1] = s.fe.OffsetVolts(1, st.OffC2)
		}
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
func (s *Scope) buildReply(f *engine.Frame, cols int, full bool, since uint64, off, vpc [2]float64, posFrac float64, measThrottle bool) wire.FrameReply { //nolint:gocyclo // one reply assembled; each branch adds one optional part
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
	// copy — the fan-out snapshot is shared read-only across clients.
	if f.Valid > len(f.C1) || f.Valid > len(f.C2) {
		fc := *f
		if fc.Valid > len(fc.C1) {
			fc.Valid = len(fc.C1)
		}
		if fc.Valid > len(fc.C2) {
			fc.Valid = len(fc.C2)
		}
		f = &fc
	}
	// Coupling display transform (software on this clone, spec 06 §6): AC
	// removes the DC (mean → mid-scale), GND shows a flat ground trace; both
	// drop that channel's offset so the baseline sits at centre. DC passes
	// through. Envelope frames are left untouched.
	c1, c2 := f.C1, f.C2
	moff := off
	cpl := [2]int{analog.CplDC, analog.CplDC}
	if s.fe != nil && f.Valid > 0 && !f.IsEnv {
		cpl[0], cpl[1] = s.fe.Coupling(0), s.fe.Coupling(1)
		if cpl[0] != analog.CplDC {
			c1, moff[0] = analog.CoupleDisplay(f.C1[:f.Valid], cpl[0]), 0
		}
		if cpl[1] != analog.CplDC {
			c2, moff[1] = analog.CoupleDisplay(f.C2[:f.Valid], cpl[1]), 0
		}
	}
	rep := wire.FrameReply{
		Seq:            f.Seq,
		WinCols:        min(f.WinCols, f.Valid),
		SampleS:        f.SampleS,
		NoiseGainIdeal: f.NoiseGainIdeal, PassbandHz: f.PassbandHz, BandwidthHz: f.BandwidthHz, FilterGuard: f.FilterGuard, Filter: f.Filter, Decimation: f.Decimation, FullRate: f.FullRate, CaptureDepth: f.CaptureDepth, TriggerKind: f.TriggerKind,
		EdgeX:          f.EdgeX,
		Ptp:            f.Ptp,
		TdivS:          f.TdivS,
		DisplayedS:     f.DisplayedS,
		Interp:         f.Interp,
		Norm:           f.Norm,
		Trigd:          f.Trigd,
		Coherent:       f.Coherent,
		Degraded:       f.Degraded,
		PeakDetect:     f.PeakDetect,
		Ordered:        f.Ordered,
		CaptureSampleS: captureSampleS(f),
		Cols:           cols,
		ColSpanS:       f.DisplayedS * 10, // the window spans 10 divisions
		Vpc1:           vpc[0], Vpc2: vpc[1],
		Off1V: moff[0], Off2V: moff[1],
	}
	// ColSpanS stays the 10-div screen time except on the deep path below,
	// which overrides it to the full-record time.
	switch {
	case f.IsEnv:
		rep.IsEnv = true
		rep.E1Min = shape.ResampleEnv(f.EnvMin, f.EnvCols, cols)
		rep.E1Max = shape.ResampleEnv(f.EnvMax, f.EnvCols, cols)
		rep.E2Min = shape.ResampleEnv(f.EnvMin2, f.EnvCols, cols)
		rep.E2Max = shape.ResampleEnv(f.EnvMax2, f.EnvCols, cols)
		rep.EdgeFrac, rep.WinFrac = -1, 1
	case full && (f.CaptureDepth > 0 || (!f.Interp && f.Valid > f.WinCols)):
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
		n := f.Valid
		rep.C1, rep.C2 = shape.RawInt16(c1[:n]), shape.RawInt16(c2[:n])
		if len(f.Q1) == n && len(f.Q2) == n {
			rep.Q1 = shape.CoupleQ8(f.Q1, cpl[0])
			rep.Q2 = shape.CoupleQ8(f.Q2, cpl[1])
			rep.FractionBits = 8
		}
		if f.EdgeX >= 0 {
			rep.EdgeFrac = f.EdgeX / float64(n)
		} else {
			rep.EdgeFrac = -1
		}
		rep.Cols, rep.ColSpanS, rep.Depth = n, float64(n)*f.SampleS, n
		rep.DtS = f.SampleS
		rep.WinFrac = float64(f.WinCols) / float64(n)
		rep.Held = shape.HeldRanges(f.Held, n)
		if v := f.Views[engine.ViewWeb]; v != nil && f.PeakDetect && n > 1 && v.Valid > 1 && v.Valid <= len(v.C1) && v.Valid <= len(v.C2) && v.To > v.From {
			last := v.To - (v.To-v.From)/float64(v.Valid) // where its last value lies
			rep.ViewA, rep.ViewB, rep.ViewN = v.From/float64(n-1), last/float64(n-1), v.Valid
			rep.VC1 = shape.RawInt16(analog.CoupleDisplayBy(v.C1[:v.Valid], f.C1[:n], cpl[0]))
			rep.VC2 = shape.RawInt16(analog.CoupleDisplayBy(v.C2[:v.Valid], f.C2[:n], cpl[1]))
		}
	default:
		// Native-fast / non-deep decimated: today's windowed screen slice.
		rep.C1 = shape.Window(c1, f.Valid, f.WinCols, f.EdgeX, f.Interp, cols, posFrac)
		rep.C2 = shape.Window(c2, f.Valid, f.WinCols, f.EdgeX, f.Interp, cols, posFrac)
		// True per-column time: the window really spans min(WinCols, Valid)
		// hardware samples (mirrors window()'s clamp) at SampleS each — NOT
		// col_span_s/cols, which is nominal on the 1–200 ns/div bands.
		if win := f.WinCols; f.Valid >= 1 {
			if win > f.Valid {
				win = f.Valid
			}
			rep.DtS = float64(win) * f.SampleS / float64(cols)
		}
		rep.WinFrac = 1
		if f.EdgeX >= 0 {
			rep.EdgeFrac = posFrac
		} else {
			rep.EdgeFrac = -1
		}
	}
	rep.StreamSeq, rep.WindowNs, rep.GapNs = f.StreamSeq, f.WindowNs, f.GapNs
	if f.HasMatchEnd && rep.EdgeFrac >= 0 {
		end := f.MatchEndS
		rep.MatchEndS = &end
	}
	// Auto-measurements over the RAW record (accurate, band-independent),
	// computed once per published frame via the value-keyed cache: repeat
	// requests for the same seq (JSON + binary, or several clients) reuse it.
	key := shape.MeasKey{Seq: f.Seq, Cpl1: cpl[0], Cpl2: cpl[1], Vpc: vpc, Off: moff, SampleS: f.SampleS}
	mv := s.meas.Get(key, measThrottle, func() shape.MeasVal {
		a, b := f.MeasureRange() // a roll view's captured stretch
		qa := func(q []uint16) []uint16 {
			if len(q) < b {
				return nil
			}
			return q[a:b]
		}
		v := shape.MeasVal{
			M1:    measure.ComputeAcquisition(c1[a:b], qa(f.Q1), vpc[0], moff[0], f.SampleS, cpl[0], f.FilterGuard),
			M2:    measure.ComputeAcquisition(c2[a:b], qa(f.Q2), vpc[1], moff[1], f.SampleS, cpl[1], f.FilterGuard),
			Clip1: measure.Clipped(f.C1[:f.Valid]), // RAW rail state (pre-coupling)
			Clip2: measure.Clipped(f.C2[:f.Valid]),
			Seq:   f.Seq, EdgeRead: f.EdgeRead,
		}
		if f.PeakDetect {
			v.M1 = v.M1.WithEnvelopeTiming(c1[:f.Valid], vpc[0], moff[0], f.SampleS).WithEdgeDetail(vpc[0], moff[0], f.EdgeWindows(0)...)
			v.M2 = v.M2.WithEnvelopeTiming(c2[:f.Valid], vpc[1], moff[1], f.SampleS).WithEdgeDetail(vpc[1], moff[1], f.EdgeWindows(1)...)
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
func captureSampleS(f *engine.Frame) float64 {
	if f.PeakDetect && f.CaptureSampleS > 0 && f.CaptureSampleS != f.SampleS {
		return f.CaptureSampleS
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
	s.sc.WithFrame(func(f *engine.Frame) {
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
