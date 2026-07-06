// Package web hosts the control webpage and JSON API on the device. It is a
// pure producer/consumer against the engine: handlers only call staging
// setters and read frame copies/stat snapshots — never the bus (spec 09 §1).
package web

import (
	"encoding/binary"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

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

//go:embed binframe.js
var binframeJS []byte

//go:embed superres.js
var superresJS []byte

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

// superresReporter is the optional device super-res status surface (the panel
// Controller implements it). Handlers type-assert so test doubles can omit it.
type superresReporter interface {
	SuperresStatus() (active, review bool, bits float64, frames, rejected int, status string)
}

// frameWaiter is the optional long-poll surface: implemented by main's
// scopeSource (delegating to frames.Fanout.WaitNext). Handlers type-assert
// for it so test doubles without it degrade to a short seq poll.
type frameWaiter interface {
	WaitNextFrame(last uint64, timeout time.Duration) uint64
}

// Server serves the UI and API. Frame reads happen inside Scope.WithFrame;
// the reply is fully assembled under the fan-out read lock and serialized +
// written to the socket after it is released (never hold the lock over I/O).
type Server struct {
	sc     Scope
	fe     Analog
	panel  Panel
	screen func() []byte // PNG of the current LCD render (device-screen view)

	// Single-entry auto-measurement cache. measure.Compute over a deep record
	// is the heaviest in-lock CPU besides serialization, and every /api/frame
	// and /api/frame.bin request needs the same numbers for a given published
	// frame — so it runs once per (seq, coupling, scale) and is shared. The
	// key carries the measurement inputs BY VALUE, so mutations from any path
	// (web /api/set, SCPI, panel) change the key and refresh naturally.
	measMu  sync.Mutex
	measKey measKey
	meas    measVal
	measAt  time.Time // when meas was computed (drives the fast-flow throttle)
}

type measKey struct {
	seq        uint64
	cpl1, cpl2 int
	vpc, off   [2]float64
	sampleS    float64
}

type measVal struct {
	m1, m2       *measure.Result
	clip1, clip2 bool
}

func New(sc Scope, fe Analog, panel Panel, screen func() []byte) *Server {
	return &Server{sc: sc, fe: fe, panel: panel, screen: screen}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.hRoot)
	mux.HandleFunc("/api/status", s.hStatus)
	mux.HandleFunc("/api/frame", s.hFrame)
	mux.HandleFunc("/api/frame.bin", s.hFrameBin)
	mux.HandleFunc("/api/set", s.hSet)
	mux.HandleFunc("/api/panel", s.hPanel)
	mux.HandleFunc("/api/screen.png", s.hScreen)
	mux.HandleFunc("/peaks.js", s.hPeaksJS)
	mux.HandleFunc("/decode.js", s.hDecodeJS)
	mux.HandleFunc("/binframe.js", s.hBinframeJS)
	mux.HandleFunc("/superres.js", s.hSuperresJS)
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

// serveJS writes a JS asset with revalidation caching: the OTA agent swaps
// the whole binary (embedded assets included), so a browser-cached app.js
// must never meet a newer server's wire format without a round trip.
func serveJS(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(body)
}

func (s *Server) hPeaksJS(w http.ResponseWriter, r *http.Request) { serveJS(w, peaksJS) }

func (s *Server) hDecodeJS(w http.ResponseWriter, r *http.Request) { serveJS(w, decodeJS) }

func (s *Server) hBinframeJS(w http.ResponseWriter, r *http.Request) { serveJS(w, binframeJS) }

func (s *Server) hSuperresJS(w http.ResponseWriter, r *http.Request) { serveJS(w, superresJS) }

func (s *Server) hTokensCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(tokensCSS)
}

func (s *Server) hBaseCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(baseCSS)
}

func (s *Server) hAppJS(w http.ResponseWriter, r *http.Request) { serveJS(w, appJS) }

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

	// Device super-res (panel stack-and-crunch) live state; omitted when inactive.
	SRActive   bool    `json:"sr_active,omitempty"`
	SRReview   bool    `json:"sr_review,omitempty"`
	SRBits     float64 `json:"sr_bits,omitempty"`
	SRFrames   int     `json:"sr_frames,omitempty"`
	SRRejected int     `json:"sr_rejected,omitempty"`
	SRStatus   string  `json:"sr_status,omitempty"`
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
	if sr, ok := s.panel.(superresReporter); ok {
		rep.SRActive, rep.SRReview, rep.SRBits, rep.SRFrames, rep.SRRejected, rep.SRStatus = sr.SuperresStatus()
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
	st := s.sc.Snapshot()
	posFrac := st.TrigPosFrac
	if posFrac <= 0 {
		posFrac = 0.5
	}

	var rep frameReply
	s.sc.WithFrame(func(f *engine.Frame) {
		rep = s.buildReply(f, cols, full, since, off, vpc, posFrac, st.Running && !st.Single)
	})
	writeJSON(w, rep)
}

// buildReply assembles the frame reply — shared verbatim by /api/frame (JSON)
// and /api/frame.bin (binary header + raw payload) so the two transports can
// never drift. MUST run inside Scope.WithFrame (f is only valid under the
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

// Binary frame layout served by /api/frame.bin (all integers little-endian):
//
//	[0]     u8  magic 0xF5 — bump on any layout change
//	[1]     u8  flags: bit0 unchanged, bit1 envelope, bit2 deep, bit3 empty
//	[2:4]   u16 reserved (0)
//	[4:8]   u32 H = JSON header byte length
//	[8:8+H]     UTF-8 JSON header: frameReply with the array fields nilled,
//	            plus head/tail = the contiguous -1 margin counts
//	[8+H:]      payload, raw uint8 ADC codes:
//	            native: c1[cols] c2[cols]
//	            deep:   c1[cols-head-tail] c2[cols-head-tail] (cols == depth)
//	            env:    e1min[cols] e1max[cols] e2min[cols] e2max[cols]
//	            unchanged/empty: none
//
// The -1 sentinels in the int16 reply arrays are provably contiguous head/tail
// runs (deepWindow walks the record monotonically; window rail-extends), so
// two counts replace the in-band sentinel and the payload narrows to uint8.
const binMagic = 0xF5

const (
	binUnchanged = 1 << 0
	binEnv       = 1 << 1
	binDeep      = 1 << 2
	binEmpty     = 1 << 3
	binRaw       = 1 << 4 // raw=1: un-windowed record, payload c1[cols] c2[cols]
)

// encodeBinFrame serializes a built reply to the binary wire format. It runs
// after WithFrame returns — rep owns its data, so no lock is held here.
func encodeBinFrame(rep frameReply) []byte {
	var flags byte
	var segs [][]int16
	head, tail := 0, 0
	switch {
	case rep.Unchanged:
		flags |= binUnchanged
	case rep.IsEnv:
		flags |= binEnv
		segs = [][]int16{rep.E1Min, rep.E1Max, rep.E2Min, rep.E2Max}
	default:
		if rep.Depth > 0 {
			flags |= binDeep
		}
		n := len(rep.C1)
		for head < n && rep.C1[head] < 0 {
			head++
		}
		if head == n { // whole array is margin (defensive: Valid<1 never publishes)
			flags |= binEmpty
		} else {
			for rep.C1[n-1-tail] < 0 {
				tail++
			}
			segs = [][]int16{rep.C1, rep.C2}
		}
	}
	hdr := rep
	hdr.C1, hdr.C2 = nil, nil
	hdr.E1Min, hdr.E1Max, hdr.E2Min, hdr.E2Max = nil, nil, nil, nil
	hdr.Head, hdr.Tail = head, tail
	hj, err := json.Marshal(hdr)
	if err != nil { // unreachable with finite inputs; keep the wire well-formed
		flags = binUnchanged
		segs, head, tail = nil, 0, 0
		hj = []byte(`{"seq":0,"unchanged":true,"edge_x":-1}`)
	}
	segLen := 0
	if len(segs) > 0 {
		segLen = len(segs[0]) - head - tail
	}
	buf := make([]byte, 8, 8+len(hj)+segLen*len(segs))
	buf[0] = binMagic
	buf[1] = flags
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(hj)))
	buf = append(buf, hj...)
	for _, seg := range segs {
		for _, v := range seg[head : head+segLen] {
			buf = append(buf, uint8(v))
		}
	}
	return buf
}

// hFrameBin is the long-poll binary frame endpoint: same reply as /api/frame
// (built by the shared buildReply), encoded as a small JSON header + raw
// uint8 payload — ~1 ms on the device versus 50-150 ms of reflective JSON
// over int16 arrays, which is what capped the browser at a few fps. With
// since= and waitms= the request parks (no locks held) until the fan-out
// snapshots a newer frame, so delivery latency is one response write and the
// client needs no poll timer: request-when-ready IS the backpressure, and a
// slow client simply skips to the newest frame.
func (s *Server) hFrameBin(w http.ResponseWriter, r *http.Request) {
	var since uint64
	fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
	cols := screenCols
	if c := r.URL.Query().Get("cols"); c != "" {
		fmt.Sscanf(c, "%d", &cols)
	}
	if cols < 64 {
		cols = 64
	}
	if cols > 4096 {
		cols = 4096
	}
	full := r.URL.Query().Get("full") == "1"
	waitms := 0
	fmt.Sscanf(r.URL.Query().Get("waitms"), "%d", &waitms)
	if waitms < 0 {
		waitms = 0
	}
	if waitms > 2000 {
		waitms = 2000
	}
	// Park even for since=0: WaitNext returns immediately once ANY frame has
	// published, and blocks when none has — otherwise a fresh page against an
	// idle engine hot-loops instant unchanged replies at the client's tick.
	if waitms > 0 {
		timeout := time.Duration(waitms) * time.Millisecond
		if fw, ok := s.sc.(frameWaiter); ok {
			fw.WaitNextFrame(since, timeout)
		} else { // test doubles: degrade to a short seq poll
			for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
				var seq uint64
				s.sc.WithFrame(func(f *engine.Frame) {
					if f != nil {
						seq = f.Seq
					}
				})
				if seq != since {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}
	var buf []byte
	if r.URL.Query().Get("raw") == "1" {
		buf = s.rawBinMsg(since)
	} else {
		off, vpc := s.vertScales()
		st := s.sc.Snapshot()
		posFrac := st.TrigPosFrac
		if posFrac <= 0 {
			posFrac = 0.5
		}
		var rep frameReply
		s.sc.WithFrame(func(f *engine.Frame) {
			rep = s.buildReply(f, cols, full, since, off, vpc, posFrac, st.Running && !st.Single)
		})
		buf = encodeBinFrame(rep) // encode + write strictly outside the fan-out lock
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	// Bound the write: the server sets no global timeouts (long-poll depends
	// on that), so a half-open peer must not hold this goroutine for minutes.
	// CLEAR the deadline afterwards — with WriteTimeout=0 net/http never
	// resets it, and the keep-alive connection is reused by other handlers
	// that set none (an absolute deadline left armed would fail them later).
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
	w.Write(buf)
	rc.SetWriteDeadline(time.Time{})
}

// rawBinMsg assembles the raw-shape binary message (?raw=1): the un-windowed,
// un-interpolated, pre-coupling record — what the browser super-resolution
// stacker aligns and drizzles. Payload = c1[cols] c2[cols] raw ADC codes,
// copied under the fan-out lock (the backing arrays are reused by the next
// tick); header carries sample_s and the engine's sub-sample edge_x. No
// measurements — the stacker computes its own statistics, and skipping the
// meas pass keeps the raw feed cheap next to the display path.
func (s *Server) rawBinMsg(since uint64) []byte {
	off, vpc := s.vertScales()
	var hdr frameReply
	var payload []byte
	var flags byte = binRaw
	s.sc.WithFrame(func(f *engine.Frame) {
		if f == nil || f.Seq == 0 || f.Seq == since {
			var seq uint64
			if f != nil {
				seq = f.Seq
			}
			hdr = frameReply{Seq: seq, Unchanged: true, EdgeX: -1}
			flags |= binUnchanged
			return
		}
		n := f.Valid
		if n <= 0 {
			hdr = frameReply{Seq: f.Seq, Unchanged: true, EdgeX: -1}
			flags |= binUnchanged | binEmpty
			return
		}
		hdr = frameReply{
			Seq: f.Seq, EdgeX: f.EdgeX, Ptp: f.Ptp, TdivS: f.TdivS,
			DisplayedS: f.DisplayedS, Interp: f.Interp, Norm: f.Norm,
			Trigd: f.Trigd, Coherent: f.Coherent, IsEnv: f.IsEnv,
			Cols: n, ColSpanS: float64(n) * f.SampleS, SampleS: f.SampleS,
			EdgeFrac: -1, WinFrac: 1,
			Vpc1: vpc[0], Vpc2: vpc[1], Off1V: off[0], Off2V: off[1],
		}
		hdr.StreamSeq, hdr.WindowNs, hdr.GapNs = f.StreamSeq, f.WindowNs, f.GapNs
		payload = make([]byte, 2*n)
		copy(payload[:n], f.C1[:n])
		copy(payload[n:], f.C2[:n])
	})
	hj, err := json.Marshal(hdr)
	if err != nil { // unreachable with finite inputs
		hj, payload, flags = []byte(`{"seq":0,"unchanged":true,"edge_x":-1}`), nil, binRaw|binUnchanged
	}
	buf := make([]byte, 8, 8+len(hj)+len(payload))
	buf[0] = binMagic
	buf[1] = flags
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(hj)))
	buf = append(buf, hj...)
	return append(buf, payload...)
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
