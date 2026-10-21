// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
)

// SOUND's web surface (ADR-SOUND): /api/sound.bin hands the audio stream to
// the browser (the page and the phone play it and analyse their own copy),
// /api/sound reads and changes the settings and the scope's shared
// readouts (V/oct table, drift log, envelope capture). Every request keeps
// the stream leased for a few seconds; when the last page goes, the stream
// stops and the scope captures again.

// soundSource is the engine's audio stream (engine/sound_stream.go).
type soundSource interface {
	LeaseSound(time.Duration)
	SoundInfo() engine.SoundInfo
	SoundRead(from uint64, max int, q1, q2 []uint16) engine.SoundBlock
	SoundWait(after uint64, timeout time.Duration) uint64
}

// soundPanel is the panel's SOUND service (panel/sound.go).
type soundPanel interface {
	SoundSettings() panel.SoundSettings
	SetSoundSettings(panel.SoundSettings) panel.SoundSettings
	SoundAction(string) error
	SoundReport(api, marks bool) panel.SoundReport
	SoundWant(api, marks bool)
}

// soundBinMagic starts every /api/sound.bin reply ("SB", version 1).
var soundBinMagic = [3]byte{'S', 'B', 1}

// soundLease is how long a request keeps the stream running.
const soundLease = 3 * time.Second

// soundBinHeader is the JSON header of a /api/sound.bin reply.
type soundBinHeader struct {
	First    uint64             `json:"first"`
	N        int                `json:"n"`
	Next     uint64             `json:"next"`
	Rate     float64            `json:"rate"`
	Scales   []soundScaleOut    `json:"scales"`
	Gaps     []uint64           `json:"gaps"`
	Marks    []engine.SoundMark `json:"marks"`
	State    string             `json:"state"`
	Active   bool               `json:"active"`
	Losses   int                `json:"losses"`
	MarksOn  bool               `json:"marks_on"`
	MarkCh   int                `json:"mark_ch"`
	MarkV    float64            `json:"mark_v"` // the marks' level, volts at the tip
	ServerMS int64              `json:"server_ms"`
	Err      string             `json:"err,omitempty"`
}

// soundScaleOut is a scale as volts at the probe tip: v = (q/256 - 128)*vpc - off.
type soundScaleOut struct {
	From uint64     `json:"from"`
	VPC  [2]float64 `json:"vpc"`
	Off  [2]float64 `json:"off"`
}

// probeOf is channel ch's probe factor (1 without a front end).
// TRLC-LINKS: REQ-SDS-265
func (s *Server) probeOf(ch int) float64 {
	if s.fe == nil {
		return 1
	}
	if p := s.fe.ProbeFactor(ch); p > 0 {
		return p
	}
	return 1
}

// hSoundBin serves the stream from ordinal from (or the newest stretch),
// parking up to wait ms for samples not yet there (or for min of them): a
// binary header (magic,
// flags, the JSON length) and JSON, then N little-endian Q8.8 codes of C1
// and N of C2.
// TRLC-LINKS: REQ-SDS-265
func (s *Server) hSoundBin(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sc.(soundSource)
	if !ok {
		http.Error(w, "no audio stream", http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	src.LeaseSound(soundLease)
	if sp, ok := s.panel.(soundPanel); ok {
		sp.SoundWant(false, q.Get("marks") == "1")
	}
	maxN := 8192
	if v, err := strconv.Atoi(q.Get("max")); err == nil && v > 0 {
		maxN = min(v, 1<<16)
	}
	info := src.SoundInfo()
	var from uint64
	if f, err := strconv.ParseUint(q.Get("from"), 10, 64); err == nil {
		from = f
	} else if info.Next > 4096 {
		from = info.Next - 4096 // a new listener starts near now
	}
	// wait parks the reply until samples come; min until that many have:
	// a listener's request a seal period long, not one per bank, keeps the
	// replies (and the scope's work for them) to about fifty a second.
	minN := 1
	if v, err := strconv.Atoi(q.Get("min")); err == nil && v > 1 {
		minN = min(v, maxN)
	}
	if wait, err := strconv.Atoi(q.Get("wait")); err == nil && wait > 0 && from+uint64(minN) > info.Next {
		src.SoundWait(from+uint64(minN)-1, time.Duration(min(wait, 1500))*time.Millisecond)
		info = src.SoundInfo()
	}
	b := src.SoundRead(from, maxN, nil, nil)
	h := soundBinHeader{First: b.First, N: b.N, Next: b.Next, Rate: b.RateHz, Gaps: b.Gaps, Marks: b.Marks,
		State: info.State, Active: info.Active, Losses: info.Losses, MarksOn: info.Marks, MarkCh: info.MarkCfg.Ch,
		ServerMS: time.Now().UnixMilli(), Err: info.Err}
	h.Scales = s.scalesOut(b.Scales)
	h.MarkV = levelVolts(h.Scales, info.MarkCfg.Ch, info.MarkCfg.Level)
	writeSoundBin(w, h, b, 5*time.Second)
}

// scalesOut is the scales as volts at the probe tip.
// TRLC-LINKS: REQ-SDS-265
func (s *Server) scalesOut(scales []engine.SoundScale) []soundScaleOut {
	var out []soundScaleOut
	for _, sc := range scales {
		var o soundScaleOut
		o.From = sc.From
		for ch := 0; ch < 2; ch++ {
			vpc, off := sc.SoundVolts(ch)
			p := s.probeOf(ch)
			o.VPC[ch], o.Off[ch] = vpc*p, off*p
		}
		out = append(out, o)
	}
	return out
}

// levelVolts is an 8-bit code on channel ch as volts at the tip, at the
// newest scale (0 with none).
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-275
func levelVolts(sc []soundScaleOut, ch int, code uint8) float64 {
	k := len(sc)
	if k == 0 {
		return 0
	}
	ch &= 1
	return (float64(code)-128)*sc[k-1].VPC[ch] - sc[k-1].Off[ch]
}

// writeSoundBin writes a header and block b as /api/sound.bin's format:
// magic, the JSON's length, the JSON, then N little-endian Q8.8 codes of C1
// and N of C2.
// TRLC-LINKS: REQ-SDS-265, REQ-SDS-275
func writeSoundBin(w http.ResponseWriter, h any, b engine.SoundBlock, deadline time.Duration) {
	hj, err := json.Marshal(h)
	if err != nil {
		// A scale or level that is no number (a front end not set yet) goes
		// as null: an empty header broke the page's whole stream.
		hj, _ = json.Marshal(jsonSafe(reflect.ValueOf(h)))
	}
	buf := make([]byte, 8, 8+len(hj)+4*b.N)
	copy(buf, soundBinMagic[:])
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(hj)))
	buf = append(buf, hj...)
	for _, seg := range [][]uint16{b.Q1, b.Q2} {
		for _, v := range seg {
			buf = append(buf, byte(v), byte(v>>8))
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(deadline))
	_, _ = w.Write(buf)
}

// soundTaker is the engine's takes (engine/sound_take.go).
type soundTaker interface {
	SoundTake() *engine.SoundTake
}

// soundTakeHeader is the JSON header of a /api/sound/take.bin reply.
type soundTakeHeader struct {
	ID         uint64             `json:"id"`
	Why        string             `json:"why"`
	First      uint64             `json:"first"`
	N          int                `json:"n"`
	Rate       float64            `json:"rate"`
	Scales     []soundScaleOut    `json:"scales"`
	Gaps       []uint64           `json:"gaps"`
	Marks      []engine.SoundMark `json:"marks"`
	HasTrig    bool               `json:"has_trig"`
	Trig       uint64             `json:"trig"`
	TrigCh     int                `json:"trig_ch"`
	TrigRising bool               `json:"trig_rising"`
	TrigV      float64            `json:"trig_v"` // the trigger's level, volts at the tip
}

// hSoundTakeBin serves the take shown (STOP, or a NORMAL or SINGLE
// trigger's) in /api/sound.bin's format with the take's header; 404 with
// none. A page fetches it once per take (its id in /api/sound's info).
// TRLC-LINKS: REQ-SDS-275
func (s *Server) hSoundTakeBin(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sc.(soundSource)
	tk, ok2 := s.sc.(soundTaker)
	if !ok || !ok2 {
		http.Error(w, "no audio stream", http.StatusNotFound)
		return
	}
	src.LeaseSound(soundLease)
	t := tk.SoundTake()
	if t == nil {
		http.Error(w, "no take", http.StatusNotFound)
		return
	}
	h := soundTakeHeader{ID: t.ID, Why: t.Why, First: t.First, N: t.N, Rate: t.RateHz, Gaps: t.Gaps, Marks: t.Marks,
		HasTrig: t.HasTrig, Trig: t.Trig, TrigCh: t.TrigCh, TrigRising: t.TrigRising}
	h.Scales = s.scalesOut(t.Scales)
	h.TrigV = levelVolts(h.Scales, t.TrigCh, t.TrigLevel)
	writeSoundBin(w, h, t.SoundBlock, 20*time.Second)
}

// soundPost is a /api/sound change: settings (only the fields sent change)
// and an action.
type soundPost struct {
	Settings json.RawMessage `json:"settings"`
	Action   string          `json:"action"`
}

// hSound reads (GET) or changes (POST) SOUND: the settings, the shared
// readouts and the stream's state. GET with marks=1 keeps the FPGA
// marking the tuned channel's edges for a page's cycle view.
// TRLC-LINKS: REQ-SDS-274
func (s *Server) hSound(w http.ResponseWriter, r *http.Request) {
	sp, ok := s.panel.(soundPanel)
	if !ok {
		http.Error(w, "no SOUND here", http.StatusNotFound)
		return
	}
	if src, ok := s.sc.(soundSource); ok && r.URL.Query().Get("lease") != "0" {
		src.LeaseSound(soundLease)
	}
	var errText string
	if r.Method == http.MethodPost {
		var req soundPost
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
			writeJSON(w, map[string]any{"ok": false, "err": "bad json"})
			return
		}
		if len(req.Settings) > 0 {
			cur := sp.SoundSettings()
			if err := json.Unmarshal(req.Settings, &cur); err != nil {
				writeJSON(w, map[string]any{"ok": false, "err": "bad settings"})
				return
			}
			sp.SetSoundSettings(cur)
		}
		if req.Action != "" {
			if err := sp.SoundAction(req.Action); err != nil {
				errText = err.Error()
			}
		}
	}
	// api=1 asks the scope to keep its own tuner and meters current (a page
	// that does not analyse the stream itself); the web page and the phone
	// analyse their copy and leave the scope's CPU to the stream.
	rep := sp.SoundReport(r.URL.Query().Get("api") == "1", r.URL.Query().Get("marks") == "1")
	if src, ok := s.sc.(soundSource); ok {
		rep.Info = src.SoundInfo() // the stream's state now (the service reads it only while it runs)
	}
	out := map[string]any{"ok": errText == "", "report": jsonSafe(reflect.ValueOf(rep))}
	if errText != "" {
		out["err"] = errText
	}
	writeJSON(w, out)
}

// finiteSeries reports a slice or array of finite floats, or of structs of
// finite floats, ints, bools and strings without JSON tag options (their
// encoding/json form is jsonSafe's).
// TRLC-LINKS: REQ-SDS-274
func finiteSeries(v reflect.Value) bool {
	et := v.Type().Elem()
	switch et.Kind() {
	case reflect.Float64, reflect.Float32:
		for i := 0; i < v.Len(); i++ {
			if f := v.Index(i).Float(); math.IsNaN(f) || math.IsInf(f, 0) {
				return false
			}
		}
		return true
	case reflect.Struct:
		var floats []int
		for i := 0; i < et.NumField(); i++ {
			f := et.Field(i)
			if !f.IsExported() || strings.Contains(f.Tag.Get("json"), ",") || f.Anonymous {
				return false
			}
			switch f.Type.Kind() {
			case reflect.Float64, reflect.Float32:
				floats = append(floats, i)
			case reflect.Int, reflect.Int64, reflect.Int32, reflect.Uint64, reflect.Uint32, reflect.Bool, reflect.String:
			default:
				return false
			}
		}
		for i := 0; i < v.Len(); i++ {
			e := v.Index(i)
			for _, k := range floats {
				if f := e.Field(k).Float(); math.IsNaN(f) || math.IsInf(f, 0) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// jsonSafe turns v into plain JSON values with every NaN and infinity as
// null (encoding/json refuses them): structs by their JSON names.
// TRLC-LINKS: REQ-SDS-274
func jsonSafe(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return f
	case reflect.Struct:
		if t, ok := v.Interface().(time.Time); ok {
			return t
		}
		m := map[string]any{}
		ty := v.Type()
		for i := 0; i < ty.NumField(); i++ {
			f := ty.Field(i)
			if !f.IsExported() {
				continue
			}
			name := f.Name
			if tag := f.Tag.Get("json"); tag != "" {
				if tag == "-" {
					continue
				}
				if n, _, _ := strings.Cut(tag, ","); n != "" {
					name = n
				}
			}
			m[name] = jsonSafe(v.Field(i))
		}
		return m
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return []any{}
		}
		// A long series of plain finite numbers (the drift log, the envelope)
		// goes to encoding/json whole: a value built per element costs the
		// scope's CPU hundreds of milliseconds a report for a long log.
		if v.Len() > 16 && finiteSeries(v) {
			if b, err := json.Marshal(v.Interface()); err == nil {
				return json.RawMessage(b)
			}
		}
		out := make([]any, v.Len())
		for i := range out {
			out[i] = jsonSafe(v.Index(i))
		}
		return out
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return jsonSafe(v.Elem())
	}
	return v.Interface()
}
