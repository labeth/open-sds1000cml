package web

import (
	"math"
	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
	"time"
)

type frameReply struct {
	Seq        uint64  `json:"seq"`
	Unchanged  bool    `json:"unchanged,omitempty"`
	C1         []int16 `json:"c1,omitempty"`
	C2         []int16 `json:"c2,omitempty"`
	IsEnv      bool    `json:"is_env,omitempty"`
	E1Min      []int16 `json:"e1min,omitempty"`
	E1Max      []int16 `json:"e1max,omitempty"`
	E2Min      []int16 `json:"e2min,omitempty"`
	E2Max      []int16 `json:"e2max,omitempty"`
	EdgeX      float64 `json:"edge_x"`
	Ptp        int     `json:"ptp"`
	TdivS      float64 `json:"tdiv_s"`
	DisplayedS float64 `json:"displayed_sdiv_s"`
	Interp     bool    `json:"interp"`
	Norm       bool    `json:"norm"`
	Trigd      bool    `json:"trigd"`
	Coherent   bool    `json:"coherent"`
	Degraded   bool    `json:"degraded,omitempty"` // native-fast dead tail survived retries: half-capture

	// Scale factors the client uses for cursors/FFT/XY/measurements.
	Cols     int     `json:"cols"`       // number of columns returned per trace
	ColSpanS float64 `json:"col_span_s"` // seconds spanned by the whole column array

	// Deep-memory (full=1 on decimated bands): the served array is the FULL
	// drained record, not the 10-div screen slice; the client windows it.
	Depth    int     `json:"depth,omitempty"` // full-record sample count served (0 = not deep)
	EdgeFrac float64 `json:"edge_frac"`       // trigger anchor as a fraction of the served array (-1 = free-run)
	WinFrac  float64 `json:"win_frac"`        // one 10-div screen as a fraction of the served array (1 = whole)

	// Stream/stitch mode: continuity so the client stitches windows on one axis.
	StreamSeq uint64  `json:"stream_seq,omitempty"` // monotonic window counter (0 = not a stream frame)
	WindowNs  int64   `json:"window_ns,omitempty"`  // this window's captured duration
	GapNs     int64   `json:"gap_ns,omitempty"`     // blackout (drain+re-arm) before this window
	Vpc1      float64 `json:"vpc1"`                 // volts per ADC code, CH1 (Vdiv/32)
	Vpc2      float64 `json:"vpc2"`                 // volts per ADC code, CH2
	Off1V     float64 `json:"off1_v"`               // applied offset volts (input-referred)
	Off2V     float64 `json:"off2_v"`

	M1 *measure.Result `json:"m1,omitempty"` // CH1 auto-measurements
	M2 *measure.Result `json:"m2,omitempty"` // CH2

	Clip1 bool `json:"clip1,omitempty"` // CH1 railed against the ADC full scale
	Clip2 bool `json:"clip2,omitempty"` // CH2 railed — readings/measurements suspect

	// Binary-transport margin counts (set only on /api/frame.bin headers,
	// never on the JSON endpoint): the served arrays' contiguous -1 head/tail
	// runs, so the payload can carry raw uint8 codes with no in-band sentinel.
	Head int `json:"head,omitempty"`
	Tail int `json:"tail,omitempty"`

	// Raw-shape only (/api/frame.bin?raw=1): the per-sample capture interval.
	// The raw shape serves the un-windowed, un-interpolated record for the
	// super-resolution stacker; edge_x is then an absolute (sub-sample)
	// index into that record.
	SampleS float64 `json:"sample_s,omitempty"`
}

// resampleEnv nearest-resamples an envelope column array to n output columns.
func resampleEnv(v []uint8, envCols, n int) []int16 {
	out := make([]int16, n)
	for x := 0; x < n; x++ {
		c := x * envCols / n
		if c < len(v) {
			out[x] = int16(v[c])
		}
	}
	return out
}

func toCols(v []uint8, n int) []int16 {
	out := make([]int16, n)
	for i := 0; i < n && i < len(v); i++ {
		out[i] = int16(v[i])
	}
	return out
}

// window maps the frame onto n screen columns (spec 04 §5): centre on the
// software edge (or record middle for a flat frame), linear interpolation on
// native-fast bands, nearest sample on decimated. The window is clamped to
// stay within the record so the screen is filled with real data — the edge
// stays centred except near the record ends where there is no data to slide
// into view. Gaps (-1) only occur off the record; the client breaks the
// polyline there.
// rawInt16 copies a raw code slice to the []int16 wire type without resampling —
// used when there is no trigger to anchor on (free-run). Codes are contiguous.
func rawInt16(codes []uint8) []int16 {
	out := make([]int16, len(codes))
	for i, c := range codes {
		out[i] = int16(c)
	}
	return out
}

// deepWindow serves the drained record RE-CENTERED on the trigger: the edge is
// placed at posFrac of a fixed-length (outLen) output, so the trigger is the
// stable anchor and the pre-/post-trigger record spreads symmetrically around
// it. Where the output runs past the captured data (near the record ends), it is
// filled with -1 = blank margin the client renders as empty but can still pan
// through. Fixed length ⇒ the served array size never jitters (no re-home churn).
func deepWindow(sig []uint8, valid, outLen int, edgeX, posFrac float64) []int16 {
	out := make([]int16, outLen)
	if !(edgeX >= 0) { // NaN/-Inf edge -> centre on the record (see window())
		edgeX = float64(valid) / 2
	}
	start := edgeX - posFrac*float64(outLen)
	for i := 0; i < outLen; i++ {
		si := int(math.Round(start)) + i
		if si >= 0 && si < valid {
			out[i] = int16(sig[si])
		} else {
			out[i] = -1
		}
	}
	return out
}

func window(sig []uint8, valid, winCols int, edgeX float64, interp bool, n int, posFrac float64) []int16 {
	out := make([]int16, n)
	if valid < 1 {
		for x := range out {
			out[x] = -1
		}
		return out
	}
	win := winCols
	if win > valid {
		win = valid
	}
	xc := edgeX
	// !(xc >= 0) also catches NaN and -Inf; +Inf is handled by the per-column
	// upper clamp below. A non-finite index (int(NaN) = min-int) panicked the
	// whole serve goroutine on one bad frame (frame-serve fuzz).
	if !(xc >= 0) {
		xc = float64(valid) / 2
	}
	// Anchor the crossing at posFrac of the window (0.5 = centre). Do NOT clamp
	// `left` into the record: that would drag the anchor off posFrac whenever the
	// record has no crossing near its middle (sub-period displays), which is what
	// made 50/100 µs jitter. Instead clamp the sample INDEX per column — off-record
	// columns extend the nearest rail (repeat-nearest). This keeps the anchor
	// exactly at posFrac every frame and is periodicity-invariant: a crossing near
	// a record end renders identically to a mid-record one. It is byte-identical
	// wherever the window already fits inside the record.
	left := xc - float64(win)*posFrac
	for x := 0; x < n; x++ {
		pos := left + float64(x)*float64(win)/float64(n)
		if !(pos >= 0) { // NaN/-Inf -> first sample (belt-and-suspenders)
			pos = 0
		} else if pos > float64(valid-1) {
			pos = float64(valid - 1)
		}
		if interp {
			i := int(pos)
			frac := pos - float64(i)
			v := float64(sig[i])
			if i+1 < valid {
				v = v*(1-frac) + float64(sig[i+1])*frac
			}
			out[x] = int16(math.Round(v))
		} else {
			out[x] = int16(sig[int(pos)])
		}
	}
	return out
}

// vertScales returns the applied offset volts and volts-per-code (Vdiv/25 —
// the 25-codes/div render scale, spec 10 §7.1) for each channel, using the
// front-end V/div when available.
func (s *Server) vertScales() (off [2]float64, vpc [2]float64) {
	vpc = [2]float64{1.0 / 25, 1.0 / 25} // nominal 1 V/div when no front end
	st := s.sc.Snapshot()
	if s.fe != nil {
		idx, _ := s.fe.Snapshot()
		vpc[0] = analog.Detents[idx[0]].VdivV / 25
		vpc[1] = analog.Detents[idx[1]].VdivV / 25
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
// payload). MUST run inside Scope.WithFrame (f is only valid under the
// fan-out read lock); the returned reply owns all its data. measThrottle
// permits reusing ≤100 ms-old measurements on a seq advance (fast free-run
// only — pass false for single-shot/stopped, where values must be exact).
func (s *Server) buildReply(f *engine.Frame, cols int, full bool, since uint64, off, vpc [2]float64, posFrac float64, measThrottle bool) frameReply {
	if f == nil || f.Seq == 0 || f.Seq == since {
		seq := uint64(0)
		if f != nil {
			seq = f.Seq
		}
		return frameReply{Seq: seq, Unchanged: true, EdgeX: -1}
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
	rep := frameReply{
		Seq:        f.Seq,
		EdgeX:      f.EdgeX,
		Ptp:        f.Ptp,
		TdivS:      f.TdivS,
		DisplayedS: f.DisplayedS,
		Interp:     f.Interp,
		Norm:       f.Norm,
		Trigd:      f.Trigd,
		Coherent:   f.Coherent,
		Degraded:   f.Degraded,
		Cols:       cols,
		ColSpanS:   f.DisplayedS * 10, // the window spans 10 divisions
		Vpc1:       vpc[0], Vpc2: vpc[1],
		Off1V: moff[0], Off2V: moff[1],
	}
	// ColSpanS stays the 10-div screen time except on the deep path below,
	// which overrides it to the full-record time.
	switch {
	case f.IsEnv:
		rep.IsEnv = true
		rep.E1Min = resampleEnv(f.EnvMin, f.EnvCols, cols)
		rep.E1Max = resampleEnv(f.EnvMax, f.EnvCols, cols)
		rep.E2Min = resampleEnv(f.EnvMin2, f.EnvCols, cols)
		rep.E2Max = resampleEnv(f.EnvMax2, f.EnvCols, cols)
		rep.EdgeFrac, rep.WinFrac = -1, 1
	case full && !f.Interp && f.Valid > f.WinCols:
		// DECIMATED deep memory: serve the full drained record so the client
		// windows/navigates it. col_span_s becomes the whole-record time so
		// every client formula (Nyquist, cursor Δt, decode, CSV) stays
		// self-consistent. The record is RE-CENTERED on the trigger (edge at
		// posFrac) so the trigger — not the frame — is the stable anchor, with
		// symmetric scrollable pre-/post-trigger margin (blank past the ends).
		n := f.Valid
		if f.EdgeX >= 0 {
			rep.C1 = deepWindow(c1, n, n, f.EdgeX, posFrac)
			rep.C2 = deepWindow(c2, n, n, f.EdgeX, posFrac)
			rep.EdgeFrac = posFrac
		} else {
			rep.C1, rep.C2 = rawInt16(c1[:n]), rawInt16(c2[:n])
			rep.EdgeFrac = -1
		}
		rep.Cols, rep.ColSpanS, rep.Depth = n, float64(n)*f.SampleS, n
		rep.WinFrac = float64(f.WinCols) / float64(n)
	default:
		// Native-fast / non-deep decimated: today's windowed screen slice.
		rep.C1 = window(c1, f.Valid, f.WinCols, f.EdgeX, f.Interp, cols, posFrac)
		rep.C2 = window(c2, f.Valid, f.WinCols, f.EdgeX, f.Interp, cols, posFrac)
		rep.WinFrac = 1
		if f.EdgeX >= 0 {
			rep.EdgeFrac = posFrac
		} else {
			rep.EdgeFrac = -1
		}
	}
	rep.StreamSeq, rep.WindowNs, rep.GapNs = f.StreamSeq, f.WindowNs, f.GapNs
	// Auto-measurements over the RAW record (accurate, band-independent),
	// computed once per published frame via the value-keyed cache: repeat
	// requests for the same seq (JSON + binary, or several clients) reuse it.
	key := measKey{seq: f.Seq, cpl1: cpl[0], cpl2: cpl[1], vpc: vpc, off: moff, sampleS: f.SampleS}
	s.measMu.Lock()
	if s.measKey != key {
		// Fast-flow throttle: while frames stream at full rate, recompute at
		// most every 100 ms and reuse the previous values in between — the
		// numbers jitter per frame anyway, and at 20 fps × 2×20480 samples
		// this loop was a large share of the device's per-frame CPU. Only a
		// SEQ advance may reuse: any config change (coupling/scale/offset)
		// recomputes immediately, and the caller disables the throttle for
		// single-shot/stopped captures where one frame's values must be exact
		// (a stale reuse there would never be corrected by a next frame).
		sameCfg := key.cpl1 == s.measKey.cpl1 && key.cpl2 == s.measKey.cpl2 &&
			key.vpc == s.measKey.vpc && key.off == s.measKey.off && key.sampleS == s.measKey.sampleS
		if !(measThrottle && sameCfg && s.meas.m1 != nil && time.Since(s.measAt) < 100*time.Millisecond) {
			s.measKey = key
			s.measAt = time.Now()
			s.meas = measVal{
				m1:    measure.Compute(c1[:f.Valid], vpc[0], moff[0], f.SampleS),
				m2:    measure.Compute(c2[:f.Valid], vpc[1], moff[1], f.SampleS),
				clip1: measure.Clipped(f.C1[:f.Valid]), // RAW rail state (pre-coupling)
				clip2: measure.Clipped(f.C2[:f.Valid]),
			}
		}
	}
	rep.M1, rep.M2, rep.Clip1, rep.Clip2 = s.meas.m1, s.meas.m2, s.meas.clip1, s.meas.clip2
	s.measMu.Unlock()
	return rep
}
