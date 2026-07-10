package web

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// hZones installs the zone-trigger rectangles (POST JSON array of zones in
// edge-anchored seconds x display codes). Empty array clears.
func (s *Server) hZones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var zs []struct {
		DtLoS  float64 `json:"dt_lo_s"`
		DtHiS  float64 `json:"dt_hi_s"`
		CodeLo int     `json:"code_lo"`
		CodeHi int     `json:"code_hi"`
		Avoid  bool    `json:"avoid"`
		Ch     int     `json:"ch"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&zs); err != nil {
		writeJSON(w, map[string]any{"ok": false, "err": "bad json"})
		return
	}
	if len(zs) > 4 {
		zs = zs[:4]
	}
	out := make([]engine.Zone, 0, len(zs))
	for _, z := range zs {
		if math.IsNaN(z.DtLoS) || math.IsNaN(z.DtHiS) || math.IsInf(z.DtLoS, 0) || math.IsInf(z.DtHiS, 0) {
			continue
		}
		out = append(out, engine.Zone{DtLoS: z.DtLoS, DtHiS: z.DtHiS,
			CodeLo: clampI(z.CodeLo, 0, 255), CodeHi: clampI(z.CodeHi, 0, 255),
			Avoid: z.Avoid, Ch: z.Ch & 1})
	}
	s.sc.SetZones(out)
	writeJSON(w, map[string]any{"ok": true, "zones": len(out)})
}

// hSerial installs the serial/protocol-trigger config (POST JSON = SerialParams:
// {proto,chA,chB,baud,cpol,cpha,msb,addr,rw,bytes}). Arm/disarm is separate, via
// /api/set {control:"serialmode"}. The byte pattern is clamped to 0..255.
func (s *Server) hSerial(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var p engine.SerialParams
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&p); err != nil {
		writeJSON(w, map[string]any{"ok": false, "err": "bad json"})
		return
	}
	p.Proto = clampI(p.Proto, 0, 10) // 0 off … 6 can, 7 mil1553, 8 arinc429, 9 usb, 10 flexray
	p.ChA, p.ChB = p.ChA&1, p.ChB&1
	p.RW = clampI(p.RW, 0, 2)
	if p.Addr > 127 || p.Addr < -1 {
		p.Addr = -1 // out of 7-bit range → "any" (never a silent never-match)
	}
	if p.Baud < 0 || p.Baud > 50_000_000 {
		p.Baud = 0
	}
	if p.Bits != 0 {
		p.Bits = clampI(p.Bits, 1, 16)
	}
	if len(p.Bytes) > 64 { // a match pattern longer than a record is pointless
		p.Bytes = p.Bytes[:64]
	}
	for i := range p.Bytes {
		p.Bytes[i] = clampI(p.Bytes[i], 0, 255)
	}
	s.sc.SetSerialParams(p)
	writeJSON(w, map[string]any{"ok": true})
}

func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// hMask uploads the envelope mask (POST JSON {lo:[],hi:[],win,ch}; empty lo
// clears). The envelopes are display-window columns (win = engine WinCols at
// build time); the client builds + dilates.
func (s *Server) hMask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var m struct {
		Lo  []int `json:"lo"`
		Hi  []int `json:"hi"`
		Win int   `json:"win"`
		Ch  int   `json:"ch"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&m); err != nil {
		writeJSON(w, map[string]any{"ok": false, "err": "bad json"})
		return
	}
	if len(m.Lo) == 0 {
		s.sc.SetMask(nil)
		writeJSON(w, map[string]any{"ok": true, "cleared": true})
		return
	}
	if len(m.Lo) != m.Win || len(m.Hi) != m.Win || m.Win <= 0 || m.Win > 1<<16 {
		writeJSON(w, map[string]any{"ok": false, "err": "length mismatch"})
		return
	}
	lo := make([]uint8, m.Win)
	hi := make([]uint8, m.Win)
	for i := 0; i < m.Win; i++ {
		lo[i] = uint8(clampI(m.Lo[i], 0, 255))
		hi[i] = uint8(clampI(m.Hi[i], 0, 255))
	}
	s.sc.SetMask(&engine.Mask{Lo: lo, Hi: hi, WinCols: m.Win, Ch: m.Ch & 1})
	writeJSON(w, map[string]any{"ok": true, "win": m.Win})
}

// hMaskFail serves one captured failing frame from the ring as JSON (gallery
// click — not a hot path). ?i=k indexes the snapshot, most recent last.
// hBode serves the accumulated Frequency-Response (Bode) curve as parallel
// arrays (compact for the plot): frequency (Hz), magnitude (dB), phase (deg),
// sorted ascending by frequency.
func (s *Server) hBode(w http.ResponseWriter, r *http.Request) {
	pts := s.sc.BodePoints()
	f := make([]float64, len(pts))
	g := make([]float64, len(pts))
	p := make([]float64, len(pts))
	for i, pt := range pts {
		f[i], g[i], p[i] = pt.FreqHz, pt.GainDB, pt.PhaseDeg
	}
	writeJSON(w, map[string]any{"ok": true, "n": len(pts), "freq": f, "gain_db": g, "phase_deg": p})
}

func (s *Server) hMaskFail(w http.ResponseWriter, r *http.Request) {
	ring := s.sc.MaskFails()
	i := 0
	fmt.Sscanf(r.URL.Query().Get("i"), "%d", &i)
	if i < 0 || i >= len(ring) {
		writeJSON(w, map[string]any{"ok": false, "err": "no such failure", "count": len(ring)})
		return
	}
	f := ring[i]
	writeJSON(w, map[string]any{
		"ok": true, "i": i, "count": len(ring),
		"seq": f.Seq, "at_ms": f.AtNs / 1e6,
		"fail_col": f.FailCol, "fail_code": f.FailCode, "fail_sample": f.FailSample,
		"edge_x": f.EdgeX, "sample_s": f.SampleS, "valid": f.Valid, "win_cols": f.WinCols,
		"c1": f.C1, "c2": f.C2,
	})
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
	// 4 KB cap: a scalar-verb body is tens of bytes; an unbounded decoder
	// would buffer an attacker-sized body on a 64 MB-class ARM.
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, map[string]any{"ok": false, "err": "bad json"})
		return
	}
	for _, v := range []float64{req.Value, req.Lvl, req.Lo, req.Hi, req.Min, req.Max} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			writeJSON(w, map[string]any{"ok": false, "err": "bad value"})
			return
		}
	}
	// Instrumentation (realtime acquisition checker): log what we did so the
	// acq log can be read against the command that preceded it.
	s.sc.NoteCmd(req.Control, req.Value)
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
	case "serialmode":
		s.sc.SetSerialMode(int(req.Value))
	case "zonemode":
		s.sc.SetZoneMode(int(req.Value))
	case "maskmode":
		s.sc.SetMaskMode(int(req.Value))
	case "maskclear":
		s.sc.ClearMaskFails()
	case "bodemode":
		// value: 0 off, else on; ref/dut channels come in via Lo/Hi (0=C1,1=C2)
		s.sc.SetBodeMode(req.Value != 0, int(req.Lo), int(req.Hi))
	case "bodeclear":
		s.sc.ClearBode()
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
		// Outer sanity guard only; the per-tier offset law clamps to the real
		// ±1.6 V (×1) / ±40 V (×25) authority and readback reports the applied V.
		if req.Value < -40 || req.Value > 40 {
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
