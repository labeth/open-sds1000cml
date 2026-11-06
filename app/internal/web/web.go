// Package web hosts the control webpage and JSON API on the device. It is a
// pure producer/consumer against the engine: handlers only call staging
// setters and read frame copies/stat snapshots — never the bus (spec 09 §1).
// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-dso-core/webserver/shape"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/panel"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
	"github.com/labeth/open-sds1000cml/app/internal/streamview"
)

// Client assets (page + classic-script JS modules + CSS) are embedded and
// served generically — see assets.go.

// Scope is the engine surface the web layer needs (the setters come from
// *engine.Engine; WithFrame comes from the frames.Fanout — the arena's
// single-consumer read slot belongs to the fan-out, and every other reader
// works on its snapshot under the fan-out lock).
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
type Scope interface {
	Snapshot() engine.Stats
	WithFrame(fn func(*engine.Frame))
	QuietRLock()   // block during the engine's load-sensitive windows (settle+drain)
	QuietRUnlock() // release
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
	SetSerialParams(p engine.SerialParams)
	SetSerialMode(m int)
	SetBodeMode(on bool, refCh, dutCh int)
	ClearBode()
	BodePoints() []engine.BodePoint

	// Realtime acquisition checker (instrumentation only).
	AcqLog(n int) ([]engine.AcqSample, float64)
	CmdLog(n int) []engine.CmdNote
	NoteCmd(name string, val float64)

	// Live tuning (debug /api/debug/tune) of the frame-rate and CPU knobs.
	Tune(engine.TuneVals) engine.TuneVals
	TuneSnapshot() engine.TuneVals
}

// Analog is the vertical front-end surface (implemented by
// *analog.FrontEnd). It is producer-direct — off the GPMC bus — so the web
// layer drives it without going through the engine (spec 09 §1). May be nil
// when the SPI nodes are unavailable.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
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
	TrigVolts(code uint16, srcCh int) float64
	TrigCalActive(srcCh int) (zero, cpv float64)
}

// Panel is the front-panel injection surface (spec 08 §6): drive any button or
// knob over the API so only the physical matrix decode needs a real press.
// TRLC-LINKS: REQ-SDS-162
type Panel interface {
	InjectButton(name string) bool
	InjectKnob(name string, dir, steps int) bool
}

// superresReporter is the optional device super-res status surface (the panel
// Controller implements it). Handlers type-assert so test doubles can omit it.
// TRLC-LINKS: REQ-SDS-164
type superresReporter interface {
	SuperresStatus() (active, review bool, bits float64, frames, rejected int, status string)
}

// frameWaiter is the optional long-poll surface: implemented by main's
// scopeSource (delegating to frames.Fanout.WaitNext). Handlers type-assert
// for it so test doubles without it degrade to a short seq poll.
// TRLC-LINKS: REQ-SDS-163
type frameWaiter interface {
	WaitNextFrame(last uint64, timeout time.Duration) uint64
}

// Server serves the UI and API. Frame reads happen inside Scope.WithFrame;
// the reply is fully assembled under the fan-out read lock and serialized +
// written to the socket after it is released (never hold the lock over I/O).
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
type Server struct {
	sc     Scope
	fe     Analog
	panel  Panel
	screen func() []byte // PNG of the current LCD render (device-screen view)

	// invSrc reports the per-channel display-invert state (SCPI Cn:INVS).
	// The SCPI handler's shadow is the single source of truth; main wires
	// this to scpi.Handler.Inverted via SetInvertSource before serving.
	// Nil (tests, no SCPI) means no inversion.
	invSrc func() [2]bool

	// setups is the numbered setup store behind /api/setup (SetSetupMemory).
	setups SetupMemory

	// claims is the single-active-client epoch (/api/claim): the device
	// serves at most one live page, bounding the CPU that competes with the
	// engine's drain (spec 03 §2).
	claims *webserver.Epoch

	// recordBusy is a /api/record.bin under way: one at a time, as each holds
	// a whole record (two 500k-sample channels) on a 128 MB device.
	recordBusy atomic.Bool

	// frameAt is when a page last asked for a frame (/api/frame.bin, unix
	// ns): a page showing the scope keeps that long-poll open (FrameClient).
	frameAt atomic.Int64

	// meas is the frame measurements' cache: computed once per published
	// frame and its measurement inputs, shared by every request.
	meas shape.MeasCache
}

// New is the web UI's server over sc; fe and panel may be nil, and screen
// renders the LCD as a PNG.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
func New(sc Scope, fe Analog, panel Panel, screen func() []byte) *Server {
	s := &Server{sc: sc, fe: fe, panel: panel, screen: screen, claims: webserver.NewEpoch()}
	return s
}

// SetInvertSource wires the SCPI INVS shadow — the single source of truth for
// display-level trace inversion — into the /api/status snapshot (inv1/inv2),
// which the page applies in its trace draw path. Call before serving.
// TRLC-LINKS: REQ-SDS-164
func (s *Server) SetInvertSource(fn func() [2]bool) { s.invSrc = fn }

// Handler routes the page, its assets and the /api endpoints.
// TRLC-LINKS: REQ-SDS-161
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerDecodedCapture(mux)
	s.registerFPGAStack(mux)
	mux.HandleFunc("/", s.hRoot)
	mux.HandleFunc("GET /remote", s.hRemote)
	mux.HandleFunc("GET /r", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/remote", http.StatusFound) })
	mux.HandleFunc("GET /remote/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/remote", http.StatusFound) })
	mux.HandleFunc("/api/status", s.hStatus)
	mux.HandleFunc("/api/frame.bin", s.hFrameBin)
	mux.HandleFunc("/api/set", s.hSet)
	mux.HandleFunc("/api/panel", s.hPanel)
	mux.HandleFunc("/api/refs", s.hRefs)
	mux.HandleFunc("/api/panel/scan", s.hPanelScan)
	mux.HandleFunc("GET /api/panel/wait", s.hPanelWait)
	mux.HandleFunc("GET /api/sheets", s.hSheets)
	mux.HandleFunc("POST /api/sheets/set", s.hSheetsSet)
	mux.HandleFunc("POST /api/stream/page", s.hStreamPage)
	mux.HandleFunc("POST /api/sheets/action", s.hSheetsAction)
	mux.HandleFunc("GET /api/stream/lines", s.hStreamLines)
	mux.HandleFunc("GET /api/stream/lines.bin", s.hStreamLinesBin)
	mux.HandleFunc("GET /api/stream/window", s.hStreamWindow)
	mux.HandleFunc("GET /api/stream/words.bin", s.hStreamWords)
	mux.HandleFunc("POST /api/stream/frames.bin", s.hStreamFrames)
	mux.HandleFunc("GET /api/record.bin", s.hRecord)
	mux.HandleFunc("/api/zones", s.hZones)
	mux.HandleFunc("/api/serial", s.hSerial)
	mux.HandleFunc("GET /api/serial/protocols", func(w http.ResponseWriter, _ *http.Request) { webserver.WriteJSON(w, engine.SerialDecoderList()) })
	mux.HandleFunc("/api/mask", s.hMask)
	mux.HandleFunc("/api/maskfail", s.hMaskFail)
	mux.HandleFunc("/api/bode", s.hBode)
	mux.HandleFunc("GET /api/sound.bin", s.hSoundBin)
	mux.HandleFunc("GET /api/sound/take.bin", s.hSoundTakeBin)
	mux.HandleFunc("/api/sound", s.hSound)
	mux.HandleFunc("/api/screen.png", s.hScreen)
	mux.HandleFunc("/api/claim", s.claims.ServeClaim)
	mux.HandleFunc("/api/setup", s.hSetup)
	mux.HandleFunc("/api/debug/tune", s.hTune)
	// pprof (lab device): live CPU/heap profiling for optimization work.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	// "/" catches the page plus every embedded .js/.css (served in hRoot).
	return mux
}

// hTune applies the live frame-rate and CPU tuning knobs and
// returns the effective values. GET reports current values; POST {json} sets
// them. Fields omitted / negative are left unchanged (see engine.Tune).
// TRLC-LINKS: REQ-SDS-162
func (s *Server) hTune(w http.ResponseWriter, r *http.Request) {
	cur := s.sc.TuneSnapshot()
	if r.Method != http.MethodPost {
		webserver.WriteJSON(w, cur)
		return
	}
	// Decode onto the current values so any omitted field (bools included) keeps
	// its present setting; only what the body carries is overridden.
	t := cur
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&t); err != nil {
		webserver.WriteBadJSON(w)
		return
	}
	webserver.WriteJSON(w, s.sc.Tune(t))
}

// hPanel injects a front-panel button or knob event (spec 08 §6), or types
// into the LCD's current context. Body: {"button":"F1"},
// {"knob":"adjust","dir":1,"steps":1}, {"type":{"text":"55 AA"}} (the
// highlighted sheet row, the byte picker or the match editor),
// {"set":{"row":"trig.level","text":"1.25V"}} (a row by id, the LCD left
// as it is), {"workspace":"bus"} or {"again":true} (the workspace keys for
// the phone remote, an open LCD sheet left as it is; {"again":true,
// "workspace":"bus"} runs BUS's again only while BUS is active). A key or
// knob with "remote":true is answered once it has run, with the rows it
// changed and the notice it raised; it waits while Autoset sweeps, and with
// "nomenu":true while a menu is open on the LCD. {"button":"auto","for":"bus"}
// runs the AUTO of the workspace the page shows, whichever the LCD shows.
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-220, REQ-SDS-226
func (s *Server) hPanel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Button    string `json:"button"`
		Knob      string `json:"knob"`
		Dir       int    `json:"dir"`
		Steps     int    `json:"steps"`
		Remote    bool   `json:"remote"`
		NoMenu    bool   `json:"nomenu"`
		Workspace string `json:"workspace"`
		Again     bool   `json:"again"`
		For       string `json:"for"` // AUTO: the workspace it frames for
		Type      *struct {
			Text string `json:"text"`
		} `json:"type"`
		Set *struct {
			Row  string `json:"row"`
			Text string `json:"text"`
		} `json:"set"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		webserver.WriteBadJSON(w)
		return
	}
	if s.panel == nil {
		webserver.WriteJSON(w, map[string]any{"ok": false, "err": "no panel"})
		return
	}
	if req.Workspace != "" || req.Again {
		s.hPanelWorkspace(w, req.Workspace, req.Again)
		return
	}
	if req.Type != nil || req.Set != nil {
		pr, ok := s.panel.(panelRemote)
		if !ok {
			webserver.WriteJSON(w, map[string]any{"ok": false, "err": "no panel"})
			return
		}
		var res panel.SetResult
		if req.Type != nil {
			res = pr.TypeText(req.Type.Text)
		} else {
			res = pr.SheetSet(req.Set.Row, nil, req.Set.Text)
		}
		webserver.WriteJSON(w, map[string]any{"ok": res.OK, "applied": res.Text, "row": res.Row, "value": res.Value, "error": res.Error}) //nolint:goconst // JSON field names: the literal shows the reply's shape
		return
	}
	if pk, isKeys := s.panel.(panelKeys); isKeys && req.Remote && (req.Button != "" || req.Knob != "") {
		if req.Button != "" {
			webserver.WriteJSON(w, pk.RemoteKey(req.Button, req.NoMenu))
		} else {
			webserver.WriteJSON(w, pk.RemoteKnob(req.Knob, req.Dir, req.Steps, req.NoMenu))
		}
		return
	}
	ok := false
	if pa, isAuto := s.panel.(panelAutoFor); isAuto && req.For != "" && strings.EqualFold(req.Button, "auto") {
		ok = pa.InjectAutoFor(req.For)
	} else if req.Button != "" {
		ok = s.panel.InjectButton(req.Button)
	} else if req.Knob != "" {
		if req.Steps == 0 {
			req.Steps = 1
		}
		ok = s.panel.InjectKnob(req.Knob, req.Dir, req.Steps)
	}
	webserver.WriteJSON(w, map[string]any{"ok": ok})
}

// hScreen returns a PNG of the current LCD render — the exact device screen.
// TRLC-LINKS: REQ-SDS-161
func (s *Server) hScreen(w http.ResponseWriter, _ *http.Request) {
	if s.screen == nil {
		http.Error(w, "no screen", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(s.screen())
}

// hRoot serves the page at "/" (with the strict CSP) and every embedded
// asset by bare filename (.js/.css, the remote's manifest, icons and
// videos). It is the ServeMux catch-all; the /api/* routes are registered
// explicitly and never reach here.
// TRLC-LINKS: REQ-SDS-161
func (s *Server) hRoot(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		w.Header().Set("Content-Security-Policy", webserver.PageCSP)
		webserver.ServeAsset(w, r, "ui.html")
		return
	}
	if webserver.StaticName(p) && webserver.ServeAsset(w, r, p) {
		return
	}
	http.NotFound(w, r)
}

// hRemote serves the phone remote (/remote; /r redirects here): its own
// small app on the sheet model API, with the same strict CSP as the page.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-220
func (s *Server) hRemote(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", webserver.PageCSP)
	webserver.ServeAsset(w, r, "remote.html")
}

const screenCols = 800

// panelScanner is the engine's front-panel scan (ADR-PANEL-SERIAL-SCAN).
type panelScanner interface {
	SetPanelScan(sramcapture.PanelConfig) error
	PanelScan() (sramcapture.PanelFrame, error)
}

// hPanelScan reads the panel scan's last frame (GET) or sets its config (POST).
// TRLC-LINKS: REQ-SDS-022
func (s *Server) hPanelScan(w http.ResponseWriter, r *http.Request) {
	ps, ok := s.sc.(panelScanner)
	if !ok {
		webserver.WriteJSON(w, map[string]any{"ok": false, "err": "no panel scan"})
		return
	}
	if r.Method == http.MethodPost {
		var p sramcapture.PanelConfig
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&p); err != nil {
			webserver.WriteBadJSON(w)
			return
		}
		if err := ps.SetPanelScan(p); err != nil {
			webserver.WriteJSON(w, map[string]any{"ok": false, "err": err.Error()})
			return
		}
	}
	f, err := ps.PanelScan()
	if err != nil {
		webserver.WriteJSON(w, map[string]any{"ok": false, "err": err.Error()})
		return
	}
	webserver.WriteJSON(w, map[string]any{"ok": true, "frame": f})
}

// hStreamLines serves the decoded stream's transcript as data from an
// absolute line index (?from=N&max=M, at most 8192 lines), so a client can
// follow a stream and check it for gaps (ADR-PANEL-DECODED-STREAM).
// TRLC-LINKS: REQ-SDS-018
func (s *Server) hStreamLines(w http.ResponseWriter, r *http.Request) {
	src, ok := s.panel.(interface {
		StreamLines(from, limit int) (streamview.LinesPage, bool)
	})
	if !ok {
		http.Error(w, "no decoded stream", http.StatusNotFound)
		return
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	n, err := strconv.Atoi(r.URL.Query().Get("max"))
	if err != nil || n <= 0 || n > 8192 {
		n = 8192
	}
	page, ok := src.StreamLines(from, n)
	if !ok {
		http.Error(w, "no decoded stream", http.StatusNotFound)
		return
	}
	webserver.WriteJSON(w, page)
}

// hStreamWindow serves the stream's newest ?rows=N (1..40) transcript lines
// as the LCD's list writes them: the web's BUS shows the scope's stream.
// TRLC-LINKS: REQ-SDS-018
func (s *Server) hStreamWindow(w http.ResponseWriter, r *http.Request) {
	src, ok := s.panel.(interface {
		StreamWindow(rows int) (streamview.Page, bool)
	})
	if !ok {
		http.Error(w, "no decoded stream", http.StatusNotFound)
		return
	}
	n, err := strconv.Atoi(r.URL.Query().Get("rows"))
	if err != nil || n <= 0 || n > streamview.ListRows {
		n = streamview.ListRows
	}
	page, ok := src.StreamWindow(n)
	if !ok {
		http.Error(w, "no decoded stream", http.StatusNotFound)
		return
	}
	webserver.WriteJSON(w, page)
}

// hStreamLinesBin serves the transcript from ?from=N in the compact binary
// line format (see decodedlines), up to 1 MiB: JSON at 1.5 MBd cost the
// scope's one CPU more than the stream itself.
// TRLC-LINKS: REQ-SDS-018
func (s *Server) hStreamLinesBin(w http.ResponseWriter, r *http.Request) {
	src, ok := s.panel.(interface {
		StreamLinesBinary(from int, dst []byte) (int, bool)
	})
	if !ok {
		http.Error(w, "no decoded stream", http.StatusNotFound)
		return
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	buf := make([]byte, 1<<20)
	n, ok := src.StreamLinesBinary(from, buf)
	if !ok {
		http.Error(w, "no decoded stream", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf[:n])
}
