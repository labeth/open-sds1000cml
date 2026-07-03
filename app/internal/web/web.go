// Package web hosts the control webpage and JSON API on the device. It is a
// pure producer/consumer against the engine: handlers only call staging
// setters and read frame copies/stat snapshots — never the bus (spec 09 §1).
package web

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/buildinfo"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
)

//go:embed ui.html
var uiHTML []byte

//go:embed peaks.js
var peaksJS []byte

//go:embed decode.js
var decodeJS []byte

//go:generate go run gen_tokens.go
//go:embed tokens.css
var tokensCSS []byte

//go:embed base.css
var baseCSS []byte

//go:embed app.js
var appJS []byte

// Scope is the engine surface the web layer needs (the setters come from
// *engine.Engine; WithFrame comes from the frames.Fanout — the arena's
// single-consumer read slot belongs to the fan-out, and every other reader
// works on its snapshot under the fan-out lock).
type Scope interface {
	Snapshot() engine.Stats
	WithFrame(fn func(*engine.Frame))
	SetRunning(bool)
	SetNorm(bool)
	SetTdiv(float64) (engine.Band, bool)
	SetTrigLevelCode(uint16) uint16
	SetTrigSlope(rising bool)
	SetTrigSource(ch int)
	SetOffsetDAC(ch int, code uint16)
	SetETS(on bool)
	SetTrigType(t int)
	SetPulseParams(lvlFrac, wMinNs, wMaxNs float64, cond int)
	SetSlopeParams(loFrac, hiFrac, tMinNs, tMaxNs float64, cond int)
	SetVideoParams(std, line int, neg bool)
	SetAcqMode(m int)
	SetAvgCount(n int)
	SetEresLen(l int)
	SetSingle()
	SetTrigPosFrac(frac float64)
	SetMemDepth(samples int) int
	SetFramePeriod(ms int) int
	SetStreamMode(on bool) bool
	SetHoldoff(sec float64) float64
}

// Analog is the vertical front-end surface (implemented by
// *analog.FrontEnd). It is producer-direct — off the GPMC bus — so the web
// layer drives it without going through the engine (spec 09 §1). May be nil
// when the SPI nodes are unavailable.
type Analog interface {
	SetVdiv(ch, idx int) error
	Snapshot() (idx [2]int, emitted bool)
	SetOffset(ch int, volts float64) uint16
	OffsetVolts(ch int, code uint16) float64
	CalSource() string
	DCVolts(ch int, meanCode float64) float64
	SetProbe(ch int, x float64)
	ProbeFactor(ch int) float64
	SetCoupling(ch, mode int) error
	Coupling(ch int) int
}

// Panel is the front-panel injection surface (spec 08 §6): drive any button or
// knob over the API so only the physical matrix decode needs a real press.
type Panel interface {
	InjectButton(name string) bool
	InjectKnob(name string, dir, steps int) bool
}

// Server serves the UI and API. Frame reads happen inside Scope.WithFrame,
// which holds the fan-out read lock for the duration of serialization.
type Server struct {
	sc     Scope
	fe     Analog
	panel  Panel
	screen func() []byte // PNG of the current LCD render (device-screen view)
}

func New(sc Scope, fe Analog, panel Panel, screen func() []byte) *Server {
	return &Server{sc: sc, fe: fe, panel: panel, screen: screen}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.hRoot)
	mux.HandleFunc("/api/status", s.hStatus)
	mux.HandleFunc("/api/frame", s.hFrame)
	mux.HandleFunc("/api/set", s.hSet)
	mux.HandleFunc("/api/panel", s.hPanel)
	mux.HandleFunc("/api/screen.png", s.hScreen)
	mux.HandleFunc("/peaks.js", s.hPeaksJS)
	mux.HandleFunc("/decode.js", s.hDecodeJS)
	mux.HandleFunc("/tokens.css", s.hTokensCSS)
	mux.HandleFunc("/base.css", s.hBaseCSS)
	mux.HandleFunc("/app.js", s.hAppJS)
	return mux
}

// hPanel injects a front-panel button or knob event (spec 08 §6). Body:
// {"button":"F1"} or {"knob":"adjust","dir":1,"steps":1}.
func (s *Server) hPanel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Button string `json:"button"`
		Knob   string `json:"knob"`
		Dir    int    `json:"dir"`
		Steps  int    `json:"steps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]any{"ok": false, "err": "bad json"})
		return
	}
	if s.panel == nil {
		writeJSON(w, map[string]any{"ok": false, "err": "no panel"})
		return
	}
	ok := false
	if req.Button != "" {
		ok = s.panel.InjectButton(req.Button)
	} else if req.Knob != "" {
		if req.Steps == 0 {
			req.Steps = 1
		}
		ok = s.panel.InjectKnob(req.Knob, req.Dir, req.Steps)
	}
	writeJSON(w, map[string]any{"ok": ok})
}

// hScreen returns a PNG of the current LCD render — the exact device screen.
func (s *Server) hScreen(w http.ResponseWriter, r *http.Request) {
	if s.screen == nil {
		http.Error(w, "no screen", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(s.screen())
}

func (s *Server) hPeaksJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Write(peaksJS)
}

func (s *Server) hDecodeJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Write(decodeJS)
}

func (s *Server) hTokensCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(tokensCSS)
}

func (s *Server) hBaseCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(baseCSS)
}

func (s *Server) hAppJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Write(appJS)
}

func (s *Server) hRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// Strict same-origin CSP. Script and connect are 'self' only (no inline
	// script — app.js/peaks.js/decode.js are all external same-origin modules).
	// style keeps 'unsafe-inline' until Phase 4 removes the last inline style=
	// hooks (display:none). img allows data: for the canvas PNG export.
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self'; connect-src 'self'; "+
			"style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
			"object-src 'none'; base-uri 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(uiHTML)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

type statusReply struct {
	engine.Stats
	Tdivs       []float64 `json:"tdivs"`
	TrigVolts   float64   `json:"trig_volts"`
	TrigCodeMin uint16    `json:"trig_code_min"`
	TrigCodeMax uint16    `json:"trig_code_max"`
	Vdivs       []float64 `json:"vdivs,omitempty"`
	Vdiv1       float64   `json:"vdiv1,omitempty"`
	Vdiv2       float64   `json:"vdiv2,omitempty"`
	Probe1      float64   `json:"probe1,omitempty"`
	Probe2      float64   `json:"probe2,omitempty"`
	Cpl1        int       `json:"cpl1"` // 0=DC 1=AC 2=GND
	Cpl2        int       `json:"cpl2"`
	Zoom1       int       `json:"zoom1,omitempty"`
	Zoom2       int       `json:"zoom2,omitempty"`
	VdivLive    bool      `json:"vdiv_live"` // false until the first emit
	Off1V       float64   `json:"off1_v"`
	Off2V       float64   `json:"off2_v"`
	CalSource   string    `json:"cal_source,omitempty"`
	DC1V        float64   `json:"dc1_v"` // calibrated DC diagnostic (GAIN/110)
	DC2V        float64   `json:"dc2_v"`
	Version     string    `json:"version"`
}

func (s *Server) hStatus(w http.ResponseWriter, r *http.Request) {
	st := s.sc.Snapshot()
	rep := statusReply{
		Stats:       st,
		Tdivs:       engine.SupportedTdivs(),
		TrigVolts:   engine.TrigLevelVolts(st.TrigCode),
		TrigCodeMin: engine.TrigCodeMin,
		TrigCodeMax: engine.TrigCodeMax,
		Version:     buildinfo.String(),
	}
	if s.fe != nil {
		idx, emitted := s.fe.Snapshot()
		for _, d := range analog.Detents {
			rep.Vdivs = append(rep.Vdivs, d.VdivV)
		}
		rep.Vdiv1 = analog.Detents[idx[0]].VdivV
		rep.Vdiv2 = analog.Detents[idx[1]].VdivV
		rep.Zoom1 = analog.Detents[idx[0]].Zoom
		rep.Zoom2 = analog.Detents[idx[1]].Zoom
		rep.VdivLive = emitted
		rep.Probe1 = s.fe.ProbeFactor(0)
		rep.Probe2 = s.fe.ProbeFactor(1)
		rep.Cpl1 = s.fe.Coupling(0)
		rep.Cpl2 = s.fe.Coupling(1)
		// The trigger level is input-referred to its source channel, so scale
		// the volts readout by that channel's probe (the code is unchanged).
		rep.TrigVolts *= s.fe.ProbeFactor(st.TrigSource)
	}
	offVolts := analog.OffsetVolts
	if s.fe != nil {
		offVolts = s.fe.OffsetVolts
		rep.CalSource = s.fe.CalSource()
		// Calibrated DC diagnostic over the latest frame's record means. The
		// GAIN/110 mapping is for the deep 50-codes/div scale; roll codes are
		// half-scale, so skip the diagnostic on a roll frame rather than
		// report double the true voltage.
		s.sc.WithFrame(func(f *engine.Frame) {
			if f == nil || f.Valid == 0 || f.IsEnv || f.RollCodes {
				return
			}
			mean := func(sig []uint8) float64 {
				sum := 0
				for _, v := range sig[:f.Valid] {
					sum += int(v)
				}
				return float64(sum) / float64(f.Valid)
			}
			rep.DC1V = s.fe.DCVolts(0, mean(f.C1))
			rep.DC2V = s.fe.DCVolts(1, mean(f.C2))
		})
	}
	if st.OffC1 != 0 {
		rep.Off1V = offVolts(0, st.OffC1)
	}
	if st.OffC2 != 0 {
		rep.Off2V = offVolts(1, st.OffC2)
	}
	if s.fe != nil { // report offsets at the probe tip, matching the frame's off_v
		rep.Off1V *= s.fe.ProbeFactor(0)
		rep.Off2V *= s.fe.ProbeFactor(1)
	}
	writeJSON(w, rep)
}

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

	// Scale factors the client uses for cursors/FFT/XY/measurements.
	Cols     int     `json:"cols"`       // number of columns returned per trace
	ColSpanS float64 `json:"col_span_s"` // seconds spanned by the whole column array

	// Deep-memory (full=1 on decimated bands): the served array is the FULL
	// drained record, not the 10-div screen slice; the client windows it.
	Depth    int     `json:"depth,omitempty"` // full-record sample count served (0 = not deep)
	EdgeFrac float64 `json:"edge_frac"`       // trigger anchor as a fraction of the served array (-1 = free-run)
	WinFrac  float64 `json:"win_frac"`        // one 10-div screen as a fraction of the served array (1 = whole)

	// Stream/stitch mode: continuity so the client stitches windows on one axis.
	StreamSeq uint64 `json:"stream_seq,omitempty"` // monotonic window counter (0 = not a stream frame)
	WindowNs  int64  `json:"window_ns,omitempty"`  // this window's captured duration
	GapNs     int64  `json:"gap_ns,omitempty"`     // blackout (drain+re-arm) before this window
	Vpc1     float64 `json:"vpc1"`       // volts per ADC code, CH1 (Vdiv/32)
	Vpc2     float64 `json:"vpc2"`       // volts per ADC code, CH2
	Off1V    float64 `json:"off1_v"`     // applied offset volts (input-referred)
	Off2V    float64 `json:"off2_v"`

	M1 *measure.Result `json:"m1,omitempty"` // CH1 auto-measurements
	M2 *measure.Result `json:"m2,omitempty"` // CH2

	Clip1 bool `json:"clip1,omitempty"` // CH1 railed against the ADC full scale
	Clip2 bool `json:"clip2,omitempty"` // CH2 railed — readings/measurements suspect
}

// clipped reports whether a trace is railed against the ADC full scale: it
// flags when more than 0.5 % of samples sit in the top/bottom code band, so a
// single stray spike doesn't trip it but a genuinely clamped signal does. A
// clipped trace makes Vpp/Vmax and any derived measurement untrustworthy.
func clipped(sig []uint8) bool {
	n := len(sig)
	if n == 0 {
		return false
	}
	railed := 0
	for _, v := range sig {
		if v <= 1 || v >= 254 {
			railed++
		}
	}
	return railed*200 > n // >0.5 %
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
	if xc < 0 {
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
		if pos < 0 {
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

const screenCols = 800

// vertScales returns the applied offset volts and volts-per-code (Vdiv/32)
// for each channel, using the front-end V/div when available.
func (s *Server) vertScales() (off [2]float64, vpc [2]float64) {
	vpc = [2]float64{1.0 / 32, 1.0 / 32} // nominal 1 V/div when no front end
	st := s.sc.Snapshot()
	if s.fe != nil {
		idx, _ := s.fe.Snapshot()
		vpc[0] = analog.Detents[idx[0]].VdivV / 32
		vpc[1] = analog.Detents[idx[1]].VdivV / 32
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

func (s *Server) hFrame(w http.ResponseWriter, r *http.Request) {
	var since uint64
	fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
	cols := screenCols
	if c := r.URL.Query().Get("cols"); c != "" {
		fmt.Sscanf(c, "%d", &cols)
	}
	// Scale to the client's canvas width; clamp to a sane band.
	if cols < 64 {
		cols = 64
	}
	if cols > 4096 {
		cols = 4096
	}
	// full=1: serve the FULL drained record on decimated deep-memory frames so
	// the client can window/navigate it. Off → today's windowed screen slice.
	full := r.URL.Query().Get("full") == "1"
	// Debug: raw undecimated record dump (diagnostics only).
	if r.URL.Query().Get("raw") == "1" {
		var out map[string]any
		s.sc.WithFrame(func(f *engine.Frame) {
			if f == nil {
				out = map[string]any{"seq": 0}
				return
			}
			c1 := make([]uint8, f.Valid)
			copy(c1, f.C1[:f.Valid])
			out = map[string]any{
				"seq": f.Seq, "valid": f.Valid, "wincols": f.WinCols,
				"edgex": f.EdgeX, "trigpos": f.TrigPos, "c1": c1,
			}
		})
		writeJSON(w, out)
		return
	}
	off, vpc := s.vertScales()
	posFrac := s.sc.Snapshot().TrigPosFrac
	if posFrac <= 0 {
		posFrac = 0.5
	}

	var rep frameReply
	s.sc.WithFrame(func(f *engine.Frame) {
		if f == nil || f.Seq == 0 || f.Seq == since {
			seq := uint64(0)
			if f != nil {
				seq = f.Seq
			}
			rep = frameReply{Seq: seq, Unchanged: true, EdgeX: -1}
			return
		}
		// Coupling display transform (software on this clone, spec 06 §6): AC
		// removes the DC (mean → mid-scale), GND shows a flat ground trace; both
		// drop that channel's offset so the baseline sits at centre. DC passes
		// through. Envelope frames are left untouched.
		c1, c2 := f.C1, f.C2
		moff := off
		if s.fe != nil && f.Valid > 0 && !f.IsEnv {
			if m := s.fe.Coupling(0); m != analog.CplDC {
				c1, moff[0] = analog.CoupleDisplay(f.C1[:f.Valid], m), 0
			}
			if m := s.fe.Coupling(1); m != analog.CplDC {
				c2, moff[1] = analog.CoupleDisplay(f.C2[:f.Valid], m), 0
			}
		}
		rep = frameReply{
			Seq:        f.Seq,
			EdgeX:      f.EdgeX,
			Ptp:        f.Ptp,
			TdivS:      f.TdivS,
			DisplayedS: f.DisplayedS,
			Interp:     f.Interp,
			Norm:       f.Norm,
			Trigd:      f.Trigd,
			Coherent:   f.Coherent,
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
		// Auto-measurements over the RAW record (accurate, band-independent).
		rawSample := f.SampleS
		rep.M1 = measure.Compute(c1[:f.Valid], vpc[0], moff[0], rawSample)
		rep.M2 = measure.Compute(c2[:f.Valid], vpc[1], moff[1], rawSample)
		rep.Clip1 = clipped(f.C1[:f.Valid]) // clip flags the RAW rail state
		rep.Clip2 = clipped(f.C2[:f.Valid])
	})
	writeJSON(w, rep)
}

type setReq struct {
	Control string  `json:"control"`
	Value   float64 `json:"value"`
	// Qualifier parameter fields (pulseparams/slopeparams/videoparams).
	Lvl  float64 `json:"lvl"`  // pulse level fraction 0..1
	Lo   float64 `json:"lo"`   // slope low fraction
	Hi   float64 `json:"hi"`   // slope high fraction
	Min  float64 `json:"min"`  // width/time window min, ns
	Max  float64 `json:"max"`  // width/time window max, ns
	Cond int     `json:"cond"` // 0=any 1=less 2=greater 3=inside
	Std  int     `json:"std"`  // video: 0=PAL 1=NTSC
	Line int     `json:"line"` // video: 0=any, else line N
	Neg  bool    `json:"neg"`  // video: negative sync
}

func (s *Server) hSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req setReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]any{"ok": false, "err": "bad json"})
		return
	}
	for _, v := range []float64{req.Value, req.Lvl, req.Lo, req.Hi, req.Min, req.Max} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			writeJSON(w, map[string]any{"ok": false, "err": "bad value"})
			return
		}
	}
	ok, applied, errStr := true, req.Value, ""
	switch req.Control {
	case "run":
		s.sc.SetRunning(req.Value != 0)
	case "norm":
		s.sc.SetNorm(req.Value != 0)
	case "tdiv":
		if b, found := s.sc.SetTdiv(req.Value); found {
			applied = b.TdivS
		} else {
			ok, errStr = false, "tdiv not in ladder"
		}
	case "triglevelcode":
		// Validate in float space: an out-of-range float→uint16 conversion
		// is implementation-defined (and differs between amd64 and ARMv7).
		if req.Value < 0 || req.Value > 65535 {
			ok, errStr = false, "triglevelcode out of range"
			break
		}
		applied = float64(s.sc.SetTrigLevelCode(uint16(req.Value)))
	case "trigslope":
		s.sc.SetTrigSlope(req.Value != 0)
	case "ets":
		s.sc.SetETS(req.Value != 0)
	case "single":
		s.sc.SetSingle()
	case "trigpos":
		s.sc.SetTrigPosFrac(req.Value)
	case "memdepth":
		applied := s.sc.SetMemDepth(int(req.Value))
		writeJSON(w, map[string]any{"ok": true, "applied": applied})
		return
	case "frameperiod":
		applied := s.sc.SetFramePeriod(int(req.Value))
		writeJSON(w, map[string]any{"ok": true, "applied": applied})
		return
	case "holdoff":
		applied := s.sc.SetHoldoff(req.Value)
		writeJSON(w, map[string]any{"ok": true, "applied": applied})
		return
	case "stream":
		on := s.sc.SetStreamMode(req.Value != 0)
		writeJSON(w, map[string]any{"ok": true, "applied": on})
		return
	case "trigtype":
		s.sc.SetTrigType(int(req.Value))
	case "pulseparams":
		s.sc.SetPulseParams(req.Lvl, req.Min, req.Max, req.Cond)
	case "slopeparams":
		s.sc.SetSlopeParams(req.Lo, req.Hi, req.Min, req.Max, req.Cond)
	case "videoparams":
		s.sc.SetVideoParams(req.Std, req.Line, req.Neg)
	case "acqmode":
		s.sc.SetAcqMode(int(req.Value))
	case "avgcount":
		s.sc.SetAvgCount(int(req.Value))
	case "eres":
		s.sc.SetEresLen(int(req.Value))
	case "eresbits":
		s.sc.SetEresLen(engine.EresLenForBits(req.Value))
	case "trigsource":
		ch := 0
		if req.Value == 1 {
			ch = 1
		}
		s.sc.SetTrigSource(ch)
	case "vdiv1", "vdiv2":
		if s.fe == nil {
			ok, errStr = false, "vertical front end unavailable"
			break
		}
		idx, found := analog.PlanVdiv(req.Value)
		if !found {
			ok, errStr = false, "vdiv not in ladder"
			break
		}
		ch := 0
		if req.Control == "vdiv2" {
			ch = 1
		}
		if err := s.fe.SetVdiv(ch, idx); err != nil {
			ok, errStr = false, err.Error()
			break
		}
		applied = analog.Detents[idx].VdivV
	case "offset1", "offset2":
		// Value in input-referred volts; the code mapping clamps to the DAC
		// linear region and uses the calibrated per-detent zero when the
		// front end (and its cal table) is present.
		if req.Value < -10 || req.Value > 10 {
			ok, errStr = false, "offset out of range"
			break
		}
		ch := 0
		if req.Control == "offset2" {
			ch = 1
		}
		if s.fe != nil {
			// The front end stages the DAC (re-anchoring on V/div changes).
			code := s.fe.SetOffset(ch, req.Value)
			applied = s.fe.OffsetVolts(ch, code)
		} else {
			code := analog.OffsetCode(ch, req.Value)
			applied = analog.OffsetVolts(ch, code)
			s.sc.SetOffsetDAC(ch, code)
		}
	case "probe1", "probe2":
		if s.fe == nil {
			ok, errStr = false, "vertical front end unavailable"
			break
		}
		x := req.Value
		if x != 1 && x != 10 && x != 100 {
			ok, errStr = false, "probe must be 1, 10 or 100"
			break
		}
		ch := 0
		if req.Control == "probe2" {
			ch = 1
		}
		s.fe.SetProbe(ch, x)
		applied = x
	case "coupling1", "coupling2":
		if s.fe == nil {
			ok, errStr = false, "vertical front end unavailable"
			break
		}
		mode := int(req.Value)
		if mode < analog.CplDC || mode > analog.CplGND {
			ok, errStr = false, "coupling must be 0 (DC), 1 (AC) or 2 (GND)"
			break
		}
		ch := 0
		if req.Control == "coupling2" {
			ch = 1
		}
		if err := s.fe.SetCoupling(ch, mode); err != nil {
			ok, errStr = false, err.Error()
			break
		}
		applied = req.Value
	default:
		ok, errStr = false, "unknown control"
	}
	writeJSON(w, map[string]any{"ok": ok, "applied": applied, "err": errStr})
}
