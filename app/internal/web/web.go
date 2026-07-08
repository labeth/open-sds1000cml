// Package web hosts the control webpage and JSON API on the device. It is a
// pure producer/consumer against the engine: handlers only call staging
// setters and read frame copies/stat snapshots — never the bus (spec 09 §1).
package web

import (
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

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

//go:embed eyejitter.js
var eyejitterJS []byte

//go:embed bode.js
var bodeJS []byte

//go:embed spectrogram.js
var spectrogramJS []byte

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
	SetZones(z []engine.Zone)
	SetZoneMode(m int)
	SetMask(m *engine.Mask)
	SetMaskMode(m int)
	ClearMaskFails()
	MaskFails() []engine.MaskFail
	SetBodeMode(on bool, refCh, dutCh int)
	ClearBode()
	BodePoints() []engine.BodePoint
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
	mux.HandleFunc("/api/zones", s.hZones)
	mux.HandleFunc("/api/mask", s.hMask)
	mux.HandleFunc("/api/maskfail", s.hMaskFail)
	mux.HandleFunc("/api/bode", s.hBode)
	mux.HandleFunc("/api/screen.png", s.hScreen)
	mux.HandleFunc("/peaks.js", s.hPeaksJS)
	mux.HandleFunc("/decode.js", s.hDecodeJS)
	mux.HandleFunc("/binframe.js", s.hBinframeJS)
	mux.HandleFunc("/superres.js", s.hSuperresJS)
	mux.HandleFunc("/eyejitter.js", s.hEyejitterJS)
	mux.HandleFunc("/bode.js", s.hBodeJS)
	mux.HandleFunc("/spectrogram.js", s.hSpectrogramJS)
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
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
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

func (s *Server) hEyejitterJS(w http.ResponseWriter, r *http.Request)   { serveJS(w, eyejitterJS) }
func (s *Server) hBodeJS(w http.ResponseWriter, r *http.Request)        { serveJS(w, bodeJS) }
func (s *Server) hSpectrogramJS(w http.ResponseWriter, r *http.Request) { serveJS(w, spectrogramJS) }

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

const screenCols = 800

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
