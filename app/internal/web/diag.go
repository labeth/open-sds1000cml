package web

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"open-sds/app/internal/buildinfo"
	"open-sds/app/internal/bus"
	"open-sds/app/internal/diag"
	"open-sds/app/internal/iface"
)

// Diagnostic block HTTP surface (/api/diag/*, workplan §2 + 04-DESIGN §4).
// Handlers are thin: they parse, call the diag package (which reaches the
// fabric through the engine's Exec on the owner goroutine) and encode the
// result. Nothing here touches the bus.

// SetDiag wires the diagnostic block. Without it the /api/diag routes answer
// 503 so a scripted acceptance run fails loudly instead of silently.
func (s *Server) SetDiag(d *diag.Diag) { s.diag = d }

func (s *Server) registerDiag(mux *http.ServeMux) {
	mux.HandleFunc("/diag", s.hDiagPage)
	mux.HandleFunc("/diag.js", s.hDiagJS)
	mux.HandleFunc("/api/diag/status", s.withDiag(s.hDiagStatus))
	mux.HandleFunc("/api/diag/map", s.hDiagMap)
	mux.HandleFunc("/api/diag/reg", s.withDiag(s.hDiagReg))
	mux.HandleFunc("/api/diag/window", s.withDiag(s.hDiagWindow))
	mux.HandleFunc("/api/diag/census", s.withDiag(s.hDiagCensus))
	mux.HandleFunc("/api/diag/snapshot", s.withDiag(s.hDiagSnapshot))
	mux.HandleFunc("/api/diag/bus", s.withDiag(s.hDiagBus))
	mux.HandleFunc("/api/diag/ctrl", s.withDiag(s.hDiagCtrl))
	mux.HandleFunc("/api/diag/vendor", s.withDiag(s.hDiagVendor))
	mux.HandleFunc("/api/diag/e2", s.withDiag(s.hDiagE2))
	mux.HandleFunc("/api/diag/capture", s.withDiag(s.hDiagCapture))
	mux.HandleFunc("/api/diag/busprobe", s.withDiag(s.hDiagBusProbe))
	mux.HandleFunc("/api/diag/cs3", s.withDiag(s.hDiagCS3))
	mux.HandleFunc("/api/diag/cs3poke", s.withDiag(s.hDiagCS3Poke))
	mux.HandleFunc("/api/diag/sramcrank", s.withDiag(s.hDiagSramCrank))
	mux.HandleFunc("/api/diag/quiet", s.withDiag(s.hDiagQuiet))
	mux.HandleFunc("/api/diag/peek", s.hDiagPeek)
	// schema v3 rungs (06-TIERS §6): R1 GPMC timing, R2 schema, R3 windowed re-drains
	mux.HandleFunc("/api/diag/gpmc", s.withDiag(s.hDiagGpmc))
	mux.HandleFunc("/api/diag/schema", s.withDiag(s.hDiagSchema))
	mux.HandleFunc("/api/diag/redrain", s.withDiag(s.hDiagRedrain))
}

func (s *Server) withDiag(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.diag == nil {
			http.Error(w, "diag not wired (no fabric owner)", http.StatusServiceUnavailable)
			return
		}
		h(w, r)
	}
}

func diagErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
}

func decodeBody(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v)
}

func parseWord(s string) (uint16, error) {
	v, err := strconv.ParseUint(s, 0, 16)
	if err != nil {
		return 0, fmt.Errorf("bad 16-bit value %q", s)
	}
	return uint16(v), nil
}

func (s *Server) hDiagStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.diag.Status())
}

// hDiagMap serves the register / DIAG window / opcode tables (from the
// generated iface) so the page and scripts can address everything by name.
func (s *Server) hDiagMap(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"name": iface.Name, "version": iface.Version, "build_id": fmt.Sprintf("0x%08x", iface.BuildID),
		"registers": iface.Registers(), "diag": iface.DiagWindow(), "opcodes": iface.Opcodes(),
		"bus_balls": diag.BusBalls, "single_balls": diag.SingleBalls,
	})
}

// hDiagReg: GET ?name=RUN (or ?sel=0x24) reads; POST {"name":..,"val":..,"raw":bool} writes.
func (s *Server) hDiagReg(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Name string `json:"name"`
			Val  string `json:"val"`
			Raw  bool   `json:"raw"`
		}
		if err := decodeBody(r, &req); err != nil {
			diagErr(w, fmt.Errorf("bad json: %w", err))
			return
		}
		v, err := parseWord(req.Val)
		if err != nil {
			diagErr(w, err)
			return
		}
		sv, err := s.diag.RegWriteSnoop(req.Name, v, req.Raw)
		if err != nil {
			diagErr(w, err)
			return
		}
		rv, err := s.diag.RegRead(req.Name)
		if err != nil {
			diagErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "readback": rv, "snoop": sv})
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.URL.Query().Get("sel")
	}
	if name == "" { // every non-pop register
		out := []diag.RegVal{}
		for _, reg := range iface.Registers() {
			if reg.Pop {
				continue
			}
			rv, err := s.diag.RegRead(reg.Name)
			if err != nil {
				diagErr(w, err)
				return
			}
			out = append(out, rv)
		}
		writeJSON(w, out)
		return
	}
	rv, err := s.diag.RegRead(name)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, rv)
}

// hDiagWindow: GET ?name=ADC_HOLD (or ?idx=7) reads; POST {"name":..,"val":..} writes.
func (s *Server) hDiagWindow(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Name string `json:"name"`
			Val  string `json:"val"`
		}
		if err := decodeBody(r, &req); err != nil {
			diagErr(w, fmt.Errorf("bad json: %w", err))
			return
		}
		v, err := parseWord(req.Val)
		if err != nil {
			diagErr(w, err)
			return
		}
		if err := s.diag.WindowWrite(req.Name, v); err != nil {
			diagErr(w, err)
			return
		}
		wv, err := s.diag.WindowRead(req.Name)
		if err != nil {
			diagErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "readback": wv})
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.URL.Query().Get("idx")
	}
	if name == "" {
		out := []diag.WindowVal{}
		for _, e := range iface.DiagWindow() {
			n := e.Count
			if n > 1 {
				n = 4 // the LANEMAP head; the rest by name
			}
			for i := 0; i < n; i++ {
				nm := e.Name
				if e.Count > 1 {
					nm = fmt.Sprintf("%s.%d", e.Name, i)
				}
				wv, err := s.diag.WindowRead(nm)
				if err != nil {
					diagErr(w, err)
					return
				}
				out = append(out, wv)
			}
		}
		writeJSON(w, out)
		return
	}
	wv, err := s.diag.WindowRead(name)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, wv)
}

func (s *Server) hDiagCensus(w http.ResponseWriter, r *http.Request) {
	c, err := s.diag.Census()
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, c)
}

// hDiagSnapshot: GET ?mode=0..7&clk=0..3[&format=bin] — the snapshot RAM
// words, as JSON or as little-endian 16-bit binary for download.
func (s *Server) hDiagSnapshot(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mode, _ := strconv.Atoi(q.Get("mode"))
	clk, _ := strconv.Atoi(q.Get("clk"))
	words, err := s.diag.Snapshot(mode, clk)
	if err != nil {
		diagErr(w, err)
		return
	}
	if q.Get("format") == "bin" {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"snapshot-m%d-c%d.bin\"", mode, clk))
		buf := make([]byte, 2*len(words))
		for i, v := range words {
			binary.LittleEndian.PutUint16(buf[2*i:], v)
		}
		w.Write(buf)
		return
	}
	writeJSON(w, map[string]any{"mode": mode, "clk": clk, "n": len(words), "words": words})
}

// hDiagBus: GET reads the bus state; POST {"ball":"R3","oe":true,"level":1} drives
// one ball, {"master":true|false} sets the master enable, {"release_all":true}
// tri-states everything.
func (s *Server) hDiagBus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Ball       string `json:"ball"`
			OE         bool   `json:"oe"`
			Level      uint8  `json:"level"`
			Master     *bool  `json:"master"`
			ReleaseAll bool   `json:"release_all"`
		}
		if err := decodeBody(r, &req); err != nil {
			diagErr(w, fmt.Errorf("bad json: %w", err))
			return
		}
		var err error
		switch {
		case req.ReleaseAll:
			err = s.diag.BusReleaseAll()
		case req.Master != nil:
			err = s.diag.BusMaster(*req.Master)
		case req.Ball != "":
			err = s.diag.BusDrive(req.Ball, req.OE, req.Level)
		default:
			err = fmt.Errorf("nothing to do: give ball, master or release_all")
		}
		if err != nil {
			diagErr(w, err)
			return
		}
	}
	st, err := s.diag.BusRead()
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, st)
}

// hDiagCtrl: POST {"field":"D2","on":true} sets a DIAG_CTRL field bit.
func (s *Server) hDiagCtrl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Field string `json:"field"`
		On    bool   `json:"on"`
	}
	if err := decodeBody(r, &req); err != nil {
		diagErr(w, fmt.Errorf("bad json: %w", err))
		return
	}
	reg, _ := iface.ByName("DIAG_CTRL")
	var mask uint16
	for _, f := range reg.Fields {
		if f.Name == req.Field {
			mask = f.Mask
		}
	}
	if mask == 0 {
		diagErr(w, fmt.Errorf("unknown DIAG_CTRL field %q", req.Field))
		return
	}
	if err := s.diag.SetCtrl(mask, req.On); err != nil {
		diagErr(w, err)
		return
	}
	rv, err := s.diag.RegRead("DIAG_CTRL")
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "readback": rv})
}

// hDiagVendor: POST {"dwell_ms":50} runs the E1 vendor-word sequence.
func (s *Server) hDiagVendor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DwellMs int `json:"dwell_ms"`
	}
	if r.Method == http.MethodPost {
		decodeBody(r, &req)
	}
	res, err := s.diag.VendorSequence(time.Duration(req.DwellMs) * time.Millisecond)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, res)
}

// hDiagE2: POST diag.E2Options runs the bus-ownership experiment.
func (s *Server) hDiagE2(w http.ResponseWriter, r *http.Request) {
	var o diag.E2Options
	if r.Method == http.MethodPost {
		decodeBody(r, &o)
	}
	res, err := s.diag.E2(o)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, res)
}

// hDiagCapture: POST diag.CaptureOptions (or GET ?words=&decim=&samples=1
// &tsrc=&chmode=&start=&len=) runs a diagnostic capture and scores the record
// (with tsrc: rung R2b, the words against the iface pattern models).
func (s *Server) hDiagCapture(w http.ResponseWriter, r *http.Request) {
	var o diag.CaptureOptions
	if r.Method == http.MethodPost {
		decodeBody(r, &o)
	} else {
		q := r.URL.Query()
		o.Words, _ = strconv.Atoi(q.Get("words"))
		d, _ := strconv.Atoi(q.Get("decim"))
		o.Decim = uint32(d)
		o.Samples = q.Get("samples") == "1"
		u16 := func(k string) uint16 { v, _ := strconv.ParseUint(q.Get(k), 0, 16); return uint16(v) }
		o.Tsrc, o.Chmode, o.Start, o.Len = u16("tsrc"), u16("chmode"), u16("start"), u16("len")
	}
	res, err := s.diag.Capture(o)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, res)
}

// hDiagBusProbe: POST BusProbeOptions — run the 27-ball generator and record
// what the balls read at the same rate (the acq2 analysis branch).
func (s *Server) hDiagBusProbe(w http.ResponseWriter, r *http.Request) {
	var o diag.BusProbeOptions
	if r.Method == http.MethodPost {
		decodeBody(r, &o)
	} else {
		q := r.URL.Query()
		o.Rate, _ = strconv.Atoi(q.Get("rate"))
		o.Phases, _ = strconv.Atoi(q.Get("phases"))
		oe, _ := strconv.ParseUint(q.Get("oe_phase"), 0, 8)
		o.OePhase = uint8(oe)
		o.Trigger = q.Get("trigger") == "1"
		o.Raw = q.Get("raw") == "1"
	}
	if o.Phases == 0 {
		o.Phases = 5
	}
	res, err := s.diag.BusProbe(o)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, res)
}

// hDiagCS3: GET a read-only census of the MAX V's CS3 register plane.
func (s *Server) hDiagCS3(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	res, err := s.diag.CS3Census(n)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"cells": res})
}

// hDiagCS3Poke: POST CS3PokeOptions — write one MAX V CS3 register and report what moved.
// The configuration port is refused by the bus layer, and every register here is volatile.
func (s *Server) hDiagCS3Poke(w http.ResponseWriter, r *http.Request) {
	var o diag.CS3PokeOptions
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := s.diag.CS3Poke(o)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, res)
}

// hDiagSramCrank: POST CrankOptions — one rung of the counted-edge SRAM-clock crank.
func (s *Server) hDiagSramCrank(w http.ResponseWriter, r *http.Request) {
	var o diag.CrankOptions
	if r.Method != http.MethodPost {
		diagErr(w, fmt.Errorf("POST only"))
		return
	}
	decodeBody(r, &o)
	res, err := s.diag.SramCrank(o)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, res)
}

// hDiagPeek: GET ?cs=2&off=0&n=256 — a READ-ONLY window into one GPMC chip
// select's region. Writes nothing, ever; the mapping is PROT_READ on an
// O_RDONLY /dev/mem. Exists because CS2 is VALID on this board and has never
// been read by any campaign.
func (s *Server) hDiagPeek(w http.ResponseWriter, r *http.Request) {
	cs, _ := strconv.Atoi(r.URL.Query().Get("cs"))
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	off64, _ := strconv.ParseUint(r.URL.Query().Get("off"), 0, 32)
	words, addr, err := bus.PeekCS(cs, uint32(off64), n)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"cs": cs, "addr": addr, "n": len(words), "words": words})
}

// hDiagQuiet: POST QuietOptions — freeze our own converters and listen to the
// 80 ADC lanes. The DQ bus is shared between the ADC and the SRAM, so silencing
// the ADC is the only way to hear the SRAM.
func (s *Server) hDiagQuiet(w http.ResponseWriter, r *http.Request) {
	var o diag.QuietOptions
	if r.Method != http.MethodPost {
		diagErr(w, fmt.Errorf("POST only"))
		return
	}
	decodeBody(r, &o)
	res, err := s.diag.QuietListen(o)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, res)
}

// hDiagGpmc: GET the CS1 timing status (timing in force, boot decision, the
// persisted file, the last sweep); POST {"action":"sweep", ...bus.SweepOptions}
// runs rung R1, {"action":"persist"} writes the last passed sweep next to the
// app, {"action":"apply","which":"persisted"|"factory"} applies through the
// ramp gate (persisted) or restores the factory timing.
func (s *Server) hDiagGpmc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, s.diag.GpmcStatus())
		return
	}
	var req struct {
		Action string `json:"action"`
		Which  string `json:"which"`
		bus.SweepOptions
	}
	if err := decodeBody(r, &req); err != nil {
		diagErr(w, fmt.Errorf("bad json: %w", err))
		return
	}
	switch req.Action {
	case "sweep":
		res, err := s.diag.GpmcSweep(req.SweepOptions)
		if err != nil {
			if res != nil {
				res.Err = err.Error()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(res)
				return
			}
			diagErr(w, err)
			return
		}
		writeJSON(w, res)
	case "persist":
		f, err := s.diag.GpmcPersist(buildinfo.String())
		if err != nil {
			diagErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "file": f, "status": s.diag.GpmcStatus()})
	case "apply":
		bt, err := s.diag.GpmcApply(req.Which)
		if err != nil {
			diagErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": !bt.Rejected, "boot": bt, "status": s.diag.GpmcStatus()})
	default:
		diagErr(w, fmt.Errorf("gpmc: action %q: want sweep, persist or apply", req.Action))
	}
}

// hDiagSchema: POST diag.SchemaOptions (or GET ?writes=) runs rung R2.
func (s *Server) hDiagSchema(w http.ResponseWriter, r *http.Request) {
	var o diag.SchemaOptions
	if r.Method == http.MethodPost {
		decodeBody(r, &o)
	} else {
		o.Writes, _ = strconv.Atoi(r.URL.Query().Get("writes"))
	}
	res, err := s.diag.SchemaCheck(o)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, res)
}

// hDiagRedrain: POST diag.RedrainOptions (or GET ?words=&windows=&passes=&tsrc=)
// runs rung R3.
func (s *Server) hDiagRedrain(w http.ResponseWriter, r *http.Request) {
	var o diag.RedrainOptions
	if r.Method == http.MethodPost {
		decodeBody(r, &o)
	} else {
		q := r.URL.Query()
		o.Words, _ = strconv.Atoi(q.Get("words"))
		o.Windows, _ = strconv.Atoi(q.Get("windows"))
		o.Passes, _ = strconv.Atoi(q.Get("passes"))
		t, _ := strconv.ParseUint(q.Get("tsrc"), 0, 16)
		o.Tsrc = uint16(t)
		o.Adc = q.Get("adc") == "1"
		o.Odd = q.Get("odd") == "1"
	}
	res, err := s.diag.Redrain(o)
	if err != nil {
		diagErr(w, err)
		return
	}
	writeJSON(w, res)
}

func (s *Server) hDiagPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self'; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, diagHTML)
}

func (s *Server) hDiagJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, diagJS)
}

// diagHTML is the minimal diagnostic page: a status line, register / window
// access by name, the census and bus tables, and buttons for the experiments.
const diagHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>acq2 diag</title>
<style>
body{font:13px/1.4 monospace;margin:1em;background:#111;color:#ddd}
h1,h2{font-size:14px;margin:.8em 0 .3em}
button{font:inherit;margin:2px;padding:2px 8px;background:#333;color:#eee;border:1px solid #666;cursor:pointer}
input,select{font:inherit;background:#222;color:#eee;border:1px solid #555;padding:2px 4px}
pre{background:#000;padding:.5em;max-height:22em;overflow:auto;white-space:pre-wrap}
table{border-collapse:collapse}td,th{border:1px solid #444;padding:1px 6px;text-align:right}
th{text-align:left}.ok{color:#8f8}.bad{color:#f88}#status{font-weight:bold}
</style></head><body>
<h1>acq2 default image — diagnostic block</h1>
<div id="status">…</div>
<h2>registers</h2>
<input id="regname" placeholder="RUN | 0x24" size="14"> <input id="regval" placeholder="0x0004" size="8">
<button data-act="regread">read</button> <button data-act="regwrite">write</button>
<label><input type="checkbox" id="regraw"> raw (bypass schema guard)</label>
<button data-act="regall">read all</button>
<h2>DIAG window</h2>
<input id="winname" placeholder="ADC_HOLD | LANEMAP.3 | 0x07" size="14"> <input id="winval" placeholder="0x0007" size="8">
<button data-act="winread">read</button> <button data-act="winwrite">write</button> <button data-act="winall">read all</button>
<h2>census / bus</h2>
<button data-act="census">lane + bus census</button> <button data-act="bus">bus state</button>
<button data-act="release">release all</button>
ball <input id="ball" size="4" placeholder="R3"> level <select id="lvl"><option>0</option><option>1</option></select>
<button data-act="drive">drive</button> <button data-act="tristate">tri-state</button>
<label><input type="checkbox" id="master"> master enable</label> <button data-act="masterset">apply</button>
D2 <select id="d2"><option>0</option><option>1</option></select> <button data-act="d2set">set</button>
<h2>snapshot</h2>
mode <select id="smode"><option>0</option><option>1</option><option>2</option><option>3</option><option>4</option><option>5</option><option>6</option><option>7</option></select>
clk <select id="sclk"><option>0</option><option>1</option><option>2</option><option>3</option></select>
<button data-act="snap">capture (json)</button> <a id="snapbin" href="#">download .bin</a>
<h2>experiments</h2>
<button data-act="vendor">E1 vendor words</button> dwell ms <input id="dwell" size="4" value="50">
<button data-act="e2">E2 ownership</button> blocks <input id="blocks" size="3" value="3"> repeats <input id="repeats" size="3" value="4">
<label><input type="checkbox" id="adchold"> include L4/T2/T7</label>
<button data-act="capture">capture + ramp score</button> words <input id="cwords" size="6" value="20478"> <!-- PRETRIG_MAX: the largest record the fabric finalizes -->
tsrc <select id="tsrc"><option value="0">ADC</option><option value="1">RAMP</option><option value="2">COLTAG</option><option value="3">GLITCH</option></select>
chmode <select id="chmode"><option value="0">dual</option><option value="1">CH1</option><option value="2">CH2</option></select>
window <input id="cstart" size="5" value="0"> + <input id="clen" size="5" value="0">
<h2>schema v3 rungs</h2>
<button data-act="gpmc">R1 timing status</button> <button data-act="sweep">R1 sweep</button> <button data-act="persist">persist</button>
<button data-act="applyp">apply persisted (ramp-gated)</button> <button data-act="applyf">factory timing</button>
<label><input type="checkbox" id="sweepgap"> sweep the cycle gap too</label>
<button data-act="schema">R2 schema check</button> <button data-act="redrain">R3 windowed re-drains</button>
<h2>result</h2>
<div id="table"></div>
<pre id="out"></pre>
<script src="/diag.js"></script>
</body></html>
`

const diagJS = `(function(){
var out=document.getElementById('out'),tbl=document.getElementById('table'),st=document.getElementById('status');
function show(v){out.textContent=typeof v==='string'?v:JSON.stringify(v,null,1);}
function get(u){return fetch(u).then(function(r){return r.json();});}
function post(u,b){return fetch(u,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(b||{})}).then(function(r){return r.json();});}
function val(id){return document.getElementById(id).value;}
function chk(id){return document.getElementById(id).checked;}
function status(){get('/api/diag/status').then(function(s){st.textContent=s.line+(s.heartbeat?'  heartbeat='+s.heartbeat:'')+(s.frames!==undefined?'  frames='+s.frames+' coherent='+s.coherent:'');st.className=(s.identity&&s.identity.ok)?'ok':'bad';}).catch(function(e){st.textContent='status: '+e;st.className='bad';});}
function censusTable(c){var h='<table><tr><th>lane</th><th>tog</th><th>lvl</th><th>e1</th><th>e0</th></tr>';c.lanes.forEach(function(l){h+='<tr><th>'+l.name+'</th><td>'+l.tog+'</td><td>'+l.level+'</td><td>'+(l.ever1?1:0)+'</td><td>'+(l.ever0?1:0)+'</td></tr>';});tbl.innerHTML=h+'</table>';}
function busTable(b){var h='<table><tr><th>ball</th><th>oe</th><th>drv</th><th>rd</th></tr>';Object.keys(b.balls).sort().forEach(function(k){var x=b.balls[k];h+='<tr><th>'+k+'</th><td>'+(x.oe?1:0)+'</td><td>'+x.drive+'</td><td>'+x.read+'</td></tr>';});tbl.innerHTML=h+'</table>';}
function e2Table(r){var h='<table><tr><th>ball</th>';r.conditions.forEach(function(c){h+='<th>D2='+c.d2+' f0</th><th>f1</th><th>rest</th><th>spread</th>';});h+='</tr>';r.conditions[0].scores.forEach(function(s,i){h+='<tr><th>'+s.ball+'</th>';r.conditions.forEach(function(c){var x=c.scores[i];h+=x.held?'<td colspan=4>held</td>':'<td>'+x.follow0.toFixed(2)+'</td><td>'+x.follow1.toFixed(2)+'</td><td>'+x.rest_high.toFixed(2)+'</td><td>'+x.spread.toFixed(2)+'</td>';});h+='</tr>';});tbl.innerHTML=h+'</table>';}
var acts={
 regread:function(){return get('/api/diag/reg?name='+encodeURIComponent(val('regname')));},
 regwrite:function(){return post('/api/diag/reg',{name:val('regname'),val:val('regval'),raw:chk('regraw')});},
 regall:function(){return get('/api/diag/reg');},
 winread:function(){return get('/api/diag/window?name='+encodeURIComponent(val('winname')));},
 winwrite:function(){return post('/api/diag/window',{name:val('winname'),val:val('winval')});},
 winall:function(){return get('/api/diag/window');},
 census:function(){return get('/api/diag/census').then(function(c){censusTable(c);return c.summary;});},
 bus:function(){return get('/api/diag/bus').then(function(b){busTable(b);return b;});},
 release:function(){return post('/api/diag/bus',{release_all:true}).then(function(b){busTable(b);return b;});},
 drive:function(){return post('/api/diag/bus',{ball:val('ball'),oe:true,level:+val('lvl')}).then(function(b){busTable(b);return b.balls[val('ball').toUpperCase()];});},
 tristate:function(){return post('/api/diag/bus',{ball:val('ball'),oe:false}).then(function(b){busTable(b);return b.balls[val('ball').toUpperCase()];});},
 masterset:function(){return post('/api/diag/bus',{master:chk('master')});},
 d2set:function(){return post('/api/diag/ctrl',{field:'D2',on:val('d2')==='1'});},
 snap:function(){return get('/api/diag/snapshot?mode='+val('smode')+'&clk='+val('sclk'));},
 vendor:function(){return post('/api/diag/vendor',{dwell_ms:+val('dwell')});},
 e2:function(){return post('/api/diag/e2',{blocks:+val('blocks'),repeats:+val('repeats'),include_adc_hold:chk('adchold')}).then(function(r){if(r.conditions)e2Table(r);return r.summary||r;});},
 capture:function(){return post('/api/diag/capture',{words:+val('cwords'),tsrc:+val('tsrc'),chmode:+val('chmode'),start:+val('cstart'),len:+val('clen')});},
 gpmc:function(){return get('/api/diag/gpmc');},
 sweep:function(){return post('/api/diag/gpmc',{action:'sweep',sweep_gap:chk('sweepgap')});},
 persist:function(){return post('/api/diag/gpmc',{action:'persist'});},
 applyp:function(){return post('/api/diag/gpmc',{action:'apply',which:'persisted'});},
 applyf:function(){return post('/api/diag/gpmc',{action:'apply',which:'factory'});},
 schema:function(){return get('/api/diag/schema');},
 redrain:function(){return get('/api/diag/redrain?odd=1');}
};
document.body.addEventListener('click',function(ev){var a=ev.target.getAttribute&&ev.target.getAttribute('data-act');if(!a||!acts[a])return;show('…');acts[a]().then(show).catch(function(e){show('error: '+e);});status();});
document.getElementById('snapbin').addEventListener('click',function(ev){ev.preventDefault();location.href='/api/diag/snapshot?format=bin&mode='+val('smode')+'&clk='+val('sclk');});
status();setInterval(status,3000);
})();
`
