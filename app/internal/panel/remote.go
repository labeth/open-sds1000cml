// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"
	"math"
	"strings"
	"time"

	"open-sds/app/internal/panel/wsview"
)

// The remote surface (/api/status, /api/panel, /api/sheets): the phone
// remote labels its buttons exactly as the LCD does and keeps its own copy
// of every settings sheet. Setting a row through it uses the row's own
// setter (the knob and F-key path) and never opens, moves or closes
// anything on the LCD; a one-line REMOTE note there confirms it. One Seq
// counts every change of the snapshot and of the sheet values, and the
// long-poll wakes on it; SheetsSeq counts the sheet values alone, so a
// remote reads /api/sheets again only when they changed. Live texts (a
// mask test's counts, the stack's status, COMPARE's trigger stability) are
// not in the snapshot: they would wake every remote on every frame.

// KeyStatus is one F-key label as drawn.
type KeyStatus struct {
	Label, Value string
	Sel, Dim     bool
}

// ToolSeg is one knob or push and its job now.
type ToolSeg struct{ Knob, Job string }

// OptModel is one option of an enum row.
type OptModel struct {
	Value int
	Text  string
}

// ActModel is one action of an action row.
type ActModel struct {
	ID, Text  string
	Available bool
}

// RowModel is one setting: its id, what it is, its value and how to set it.
type RowModel struct {
	ID        string
	Label     string
	Kind      string // enum, number, action, bytes
	Available bool   // in use now (else it is shown dim)
	Help      string
	Section   string     `json:",omitempty"` // the section header the LCD draws above this row
	Why       string     `json:",omitempty"` // what puts the row in use, while it is not
	Value     any        `json:",omitempty"` // raw: option value, number in Unit, or the match text
	Text      string     // as written on the sheet
	Options   []OptModel `json:",omitempty"`
	Unit      string     `json:",omitempty"`
	Min       *float64   `json:",omitempty"`
	Max       *float64   `json:",omitempty"`
	Step      *float64   `json:",omitempty"` // one knob step (absent: a ladder)
	Ladder    []float64  `json:",omitempty"`
	Presets   []string   `json:",omitempty"` // a number row's labelled values, run by /api/sheets/action
	Actions   []ActModel `json:",omitempty"`
}

// TabModel is one sheet tab.
type TabModel struct {
	ID, Name, Title, Key string
	Rows                 []RowModel
	Notes                []TabNote `json:",omitempty"` // the TRIG tab's status lines, as under its rows on the LCD
}

// TabNote is one status line of a tab; Warn when it says what does not work.
type TabNote struct {
	Text string
	Warn bool `json:",omitempty"`
}

// SheetsModel is the whole sheet model for the remote.
type SheetsModel struct {
	Seq       uint64
	SheetsSeq uint64 // the sheet values' count (Status.SheetsSeq) when this model was read
	Tabs      []TabModel
}

// SheetStatus is the LCD's open sheet.
type SheetStatus struct {
	Open     bool
	Tab      string // tab id
	TabName  string
	TabTitle string
	Tabs     []string // in this workspace's order
	Edit     bool
	Row      *RowModel // the highlighted row (an action row without its live status text)
}

// PickerStatus is the byte picker or the match editor.
type PickerStatus struct {
	Open   bool
	Mode   string // "pick" or "editor"
	Tokens []string
	Cursor int
	KeySet string `json:",omitempty"`
	Key    string `json:",omitempty"`
}

// Status is the panel snapshot in /api/status: what the LCD and lamps show.
type Status struct {
	Seq         uint64
	SheetsSeq   uint64 // moves only when a sheet value or a row's availability changes
	AutosetBusy bool
	AutosetMsg  string
	Workspace   string
	WorkspaceID string
	View        string // the active workspace's view
	Views       [wsview.Count]string
	Mode        string // workspace, sheet, picker, editor
	Sheet       SheetStatus
	FKeys       [5]KeyStatus
	Tool        []ToolSeg
	AdjustRow   string `json:",omitempty"` // the row SCOPE's Adjust tunes (its label alone is ambiguous: CH1 and CH2 both have an Offset)
	Again       string // the pane's again line (COMPARE: the key's job, not the trigger's stability)
	AgainNext   string // what the workspace's key again shows next, whatever the LCD shows ("X-Y")
	Notice      string
	Remote      string
	Picker      PickerStatus
	Lamps       uint16
	LampsLit    []string
	Display     Display
	Screen      string // on, saver or off (ADR-SCREEN-SAVER)
	// the last Autoset sweep that ended and the rows it changed (a remote
	// that started it books them as its own)
	AutosetDone    uint64
	AutosetChanged []string `json:",omitempty"`
	// what REF A and B hold: a page reads them again (GET /api/refs) when
	// one changes
	Refs [2]RefTag
}

// SetResult is the outcome of a remote set, action or typed entry. A
// remote call (remoteCall) also says what it did, measured on the panel
// goroutine: the rows whose value it changed ("workspace" too when it
// switched it), the notice it raised, the sheet values' count after it and
// the Autoset sweep it started.
type SetResult struct {
	OK        bool     `json:"ok"`
	Row       string   `json:"row,omitempty"`
	Value     any      `json:"value,omitempty"`
	Text      string   `json:"text,omitempty"`
	Error     string   `json:"error,omitempty"`
	Changed   []string `json:"changed,omitempty"`
	Notice    string   `json:"notice,omitempty"`
	SheetsSeq uint64   `json:"sheetsSeq,omitempty"`
	Autoset   uint64   `json:"autoset,omitempty"` // Status.AutosetDone names it when the sweep ends
}

// remoteState is the published snapshot (guarded by Controller.rmu).
type remoteState struct {
	seq          uint64
	sig          uint64
	sheetsSeq    uint64 // counts sheetSig changes
	sheetSig     uint64
	status       Status
	wake         chan struct{}
	sheetsActive time.Time
	pubAt        time.Time // the last publish
}

// sheetsWindow is how long after a remote's last sheet request the sheet
// values are watched for the Seq.
const sheetsWindow = 60 * time.Second

// lampNames names the lit lamps of a word.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func lampNames(w uint16) []string {
	var out []string
	for _, l := range []struct {
		bit  uint16
		name string
	}{{ledCH1, "CH1"}, {ledCH2, "CH2"}, {ledMath, "MATH"}, {ledRef, "REF"}, {ledRun, "RUN"}, {ledStop, "STOP"}, {ledSingle, "SINGLE"},
		{ledMeasure, "MEASURE"}, {ledCursors, "CURSORS"}, {ledAcquire, "ACQUIRE"}, {ledDisplay, "DISPLAY"}, {ledUtility, "UTILITY"},
		{ledSaveRec, "SAVE/RECALL"}} {
		if w&l.bit != 0 {
			out = append(out, l.name)
		}
	}
	return out
}

// workspaceView names a workspace's view.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func workspaceView(s *sv, ws wsview.WS) string {
	switch ws {
	case wsview.Bus:
		return busViewName(&s.ui)
	case wsview.Measure:
		return map[bool]string{true: "All values", false: "Statistics"}[s.ui.measAll]
	case wsview.Compare:
		if s.stacking {
			return "Stacking"
		}
		return "Compare"
	}
	v := s.ui.views[ws]
	if ws == s.ui.ws {
		v = s.viewMode
	}
	return viewName(v)
}

// status builds the panel snapshot from one state read.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func (c *Controller) status(s *sv) Status {
	u := &s.ui
	d, v := c.displayAndView(s)
	st := Status{AutosetBusy: d.AutosetBusy, AutosetMsg: d.AutosetMsg, Workspace: wsview.Names[u.ws], WorkspaceID: wsview.IDs[u.ws],
		View: workspaceView(s, u.ws), AgainNext: c.againNext(s), Notice: v.Notice, Remote: d.Remote, Display: d}
	for i := range st.Views {
		st.Views[i] = workspaceView(s, wsview.WS(i))
	}
	st.Lamps = c.lampWord(s) // what the lamps show (latched at the next input or sync)
	st.Screen = s.screen.String()
	st.LampsLit = lampNames(st.Lamps)
	c.mu.Lock()
	st.AutosetDone, st.AutosetChanged = c.autosetDone, c.autosetChanged
	c.mu.Unlock()
	st.Refs = c.RefTags()
	switch {
	case u.open && u.sub == subMatch:
		st.Mode = "editor"
	case u.open && u.sub == subPick, u.wpick:
		st.Mode = "picker"
	case u.open:
		st.Mode = "sheet"
	default:
		st.Mode = "workspace"
	}
	if v.Keys != nil {
		for i, k := range v.Keys.Items {
			st.FKeys[i] = KeyStatus{Label: k.Label, Value: k.Value, Sel: k.Sel, Dim: k.Dim}
		}
	} else {
		for i := 0; i < wsview.Count; i++ {
			st.FKeys[i] = KeyStatus{Label: wsview.Tags[i], Sel: wsview.WS(i) == u.ws}
		}
	}
	if u.open {
		t := sheetTabs[u.tab]
		st.Sheet = SheetStatus{Open: true, Tab: t.id, TabName: t.name, TabTitle: t.title, Edit: u.edit}
		for _, ti := range tabOrder(u.ws) {
			st.Sheet.Tabs = append(st.Sheet.Tabs, sheetTabs[ti].name)
		}
		rm := rowModel(s, curRow(u))
		if rm.Kind == "action" {
			rm.Text = "" // a live status (a mask test's counts, the stack's): /api/sheets has it
		}
		st.Sheet.Row = &rm
		st.Tool = []ToolSeg{{"S/DIV", "tab"}, {"Adjust", "row"}, {"push", "edit"}, {"Position", "change the row"}, {"push", "default"}}
		if u.sub == subMatch {
			st.Tool = []ToolSeg{{"Adjust", "key"}, {"push", "type"}, {"Position", "cursor"}, {"push", "clear"}}
		} else if u.sub == subPick {
			st.Tool = pickTool
		}
	} else {
		st.Tool, st.Again = paneTool(c, s, v)
		if v.Cmp != nil {
			st.Again = "F5 again: " + st.AgainNext // the key's job, not the trigger's stability
		}
		if r := adjTarget(s); r != nil && u.ws == wsview.Scope && !u.wpick {
			st.AdjustRow = r.id
		}
	}
	switch {
	case u.wpick || u.open && u.sub == subPick:
		st.Picker = PickerStatus{Open: true, Mode: "pick", Cursor: u.pick.start}
		for _, t := range u.pick.toks {
			st.Picker.Tokens = append(st.Picker.Tokens, t.Text)
		}
	case u.open && u.sub == subMatch:
		m := c.MatchEditor()
		key := ""
		if m.Row < len(m.Keys) && m.Col < len(m.Keys[m.Row]) {
			key = m.Keys[m.Row][m.Col]
		}
		st.Picker = PickerStatus{Open: true, Mode: "editor", Tokens: m.Toks, Cursor: m.Cur, KeySet: m.Set, Key: key}
	}
	return st
}

// pickTool is the byte picker's tool line.
var pickTool = []ToolSeg{{"Adjust", "byte"}, {"push", "trigger on it"}, {"Position", "byte"}, {"push", "back to start"}, {"Menu", "back"}}

// paneTool is the tool line in workspace mode (SCOPE's from its Adjust tag).
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func paneTool(c *Controller, s *sv, v wsview.View) ([]ToolSeg, string) {
	var p *wsview.Pane
	switch {
	case v.Picker != nil:
		return pickTool, ""
	case v.Bus != nil:
		p = &v.Bus.Pane
	case v.Eye != nil:
		p = &v.Eye.Pane
	case v.Spec != nil:
		p = &v.Spec.Pane
	case v.Meas != nil:
		p = &v.Meas.Pane
	case v.Cmp != nil:
		p = &v.Cmp.Pane
	}
	if p != nil {
		var out []ToolSeg
		for _, sg := range p.Tool {
			out = append(out, ToolSeg{sg.Key, sg.Job})
		}
		return out, p.Again
	}
	adj := "the last value set in a sheet"
	if r := adjTarget(s); r != nil {
		adj = r.label
	}
	out := []ToolSeg{{"Adjust", adj}, {"push", "open its sheet row"}}
	for _, sg := range posTool(s) {
		out = append(out, ToolSeg{sg.Key, sg.Job})
	}
	return out,
		"F1 again: " + viewName(map[bool]int{true: viewXY, false: viewYT}[s.viewMode == viewYT])
}

// displayAndView is Snapshot from a state read already taken.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212
func (c *Controller) displayAndView(s *sv) (Display, wsview.View) {
	c.mu.Lock()
	busy, amsg := c.autosetBusy, c.autosetMsg
	c.mu.Unlock()
	d := Display{ShowC1: s.chDisp[0], ShowC2: s.chDisp[1], ShowMeas: s.showMeas, ViewMode: s.viewMode, MathMode: s.mathMode,
		Zoom: max(s.zoom, 1), ZoomOff: s.zoomOff, Persist: s.persist, FFTSrc: s.fftSrc,
		DecProto: s.decProto, DecBaud: s.decBaud, DecChA: s.decChA, DecChB: s.decChB, DecCPOL: s.decCPOL, DecCPHA: s.decCPHA,
		DecLSB: s.decLSB, DecParity: s.decParity, DecInverted: s.decInv, DecBits: s.decBits, DecLayer: s.decLayer, DecLayerAddr: s.decLayerAddr, DecLayerDir: s.decLayerDir, DecDelayNs: s.spiDelayNs(), DecFormat: s.decFormat, DecDetecting: s.ui.busDetecting, DecAuto: s.decAuto, DecNote: s.ui.busNote, CurOn: s.curOn, CurType: s.curType, CurSel: s.curSel, CurX: s.curX, CurY: s.curY,
		CurT: s.curT, CurV: s.curV, CurVCh: s.curVCh,
		AutosetBusy: busy, AutosetMsg: amsg}
	for _, it := range s.meas {
		d.MeasItems = append(d.MeasItems, fmt.Sprintf("%d:%s", it.ch+1, measParams[it.param].id))
	}
	if s.remoteNotices && s.now.Before(s.ui.remoteUnt) {
		d.Remote = s.ui.remote
	}
	return d, c.view(s)
}

// rowModel describes a row for the remote.
// TRLC-LINKS: REQ-SDS-212
func rowModel(s *sv, r *row) RowModel {
	m := RowModel{ID: r.id, Label: r.label, Available: r.relevant(s), Help: r.help, Section: r.hdr, Text: r.valueText(s)}
	if !m.Available {
		m.Why = r.why
	}
	switch r.kind {
	case wsview.Enum:
		m.Kind, m.Value = "enum", r.get(s)
		for _, o := range r.options(s) {
			m.Options = append(m.Options, OptModel{o.v, o.t})
		}
	case wsview.Number:
		m.Kind, m.Value, m.Unit = "number", finiteOr(r.val(s)), r.unitFor(s)
		lo, hi := r.lim(s)
		m.Min, m.Max = ptr(finiteOr(lo)), ptr(finiteOr(hi))
		if st := r.step(s); st > 0 {
			m.Step = ptr(st)
		}
		if r.ladder != nil {
			m.Ladder = r.ladder(s)
		}
		if r.presets != nil {
			for _, p := range r.presets(s) {
				m.Presets = append(m.Presets, p.t)
			}
		}
	case wsview.Bytes:
		m.Kind, m.Value = "bytes", strings.Join(matchTokens(s), " ")
	case wsview.Action, wsview.Slot:
		m.Kind = "action"
		for _, a := range r.acts(s) {
			m.Actions = append(m.Actions, ActModel{ID: a.id, Text: a.t, Available: !a.dim && m.Available})
		}
	}
	return m
}

// finiteOr keeps JSON encodable: NaN and Inf read 0.
// TRLC-LINKS: REQ-SDS-212
func finiteOr(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// TRLC-LINKS: REQ-SDS-212
func ptr(v float64) *float64 { return &v }

// sheetSig hashes every row's value and availability (not live counters),
// so the Seq follows a setting changed anywhere.
// TRLC-LINKS: REQ-SDS-212
func sheetSig(s *sv) uint64 {
	h := fnv.New64a()
	for _, t := range sheetTabs {
		for _, r := range t.rows {
			h.Write([]byte(r.id))
			putU64(h, uint64(b2ic(r.relevant(s))))
			if r.kind == wsview.Enum {
				putU64(h, uint64(len(r.options(s))))
			}
			hashValue(h, s, r)
		}
	}
	return h.Sum64()
}

// hashValue writes a row's value into h: its option, its number, the match
// bytes or a setup slot's summary (an action has none; its live status is
// not a setting).
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-220
func hashValue(h hash.Hash64, s *sv, r *row) {
	switch r.kind {
	case wsview.Enum:
		putU64(h, uint64(r.get(s)))
	case wsview.Number:
		putU64(h, math.Float64bits(r.val(s)))
	case wsview.Bytes:
		h.Write([]byte(matchText(s)))
	case wsview.Slot:
		putU64(h, uint64(b2ic(s.slotUsed[r.slot])))
		h.Write([]byte(s.slotSum[r.slot]))
	}
}

// putU64 writes x into h, little-endian.
// TRLC-LINKS: REQ-SDS-211
func putU64(h hash.Hash64, x uint64) {
	var b [8]byte
	for i := range b {
		b[i] = byte(x >> (8 * i))
	}
	h.Write(b[:])
}

// rowKeys is every row's value as a key, in sheet order.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-220
func rowKeys(s *sv) []uint64 {
	h := fnv.New64a()
	keys := make([]uint64, 0, len(rowByID))
	for _, t := range sheetTabs {
		for _, r := range t.rows {
			h.Reset()
			hashValue(h, s, r)
			keys = append(keys, h.Sum64())
		}
	}
	return keys
}

// changeMark is how a remote call (or an Autoset sweep) found the settings:
// each row's value, the workspace and the notice showing.
type changeMark struct {
	keys      []uint64
	ws        wsview.WS
	noticeUnt time.Time
}

// markChanges takes the settings as they are now, for changesSince.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-220
func (c *Controller) markChanges() changeMark {
	s := c.snap()
	m := changeMark{keys: rowKeys(s)}
	c.mu.Lock()
	m.ws, m.noticeUnt = c.ui.ws, c.ui.noticeUnt
	c.mu.Unlock()
	return m
}

// changesSince lists the rows whose value is not what m found, in sheet
// order ("workspace" last when the workspace is another one), and the
// notice raised since. A timebase counts once asked for (the row reads
// the setting; the engine applies it at its next capture, after the call).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-220
func (c *Controller) changesSince(m changeMark) ([]string, string) {
	if len(m.keys) == 0 {
		return nil, ""
	}
	s := c.snap()
	keys := rowKeys(s)
	c.mu.Lock()
	ws := c.ui.ws
	notice := ""
	if c.ui.noticeUnt != m.noticeUnt && s.now.Before(c.ui.noticeUnt) {
		notice = c.ui.notice
	}
	c.mu.Unlock()
	var out []string
	i := 0
	for _, t := range sheetTabs {
		for _, r := range t.rows {
			if keys[i] != m.keys[i] {
				out = append(out, r.id)
			}
			i++
		}
	}
	if ws != m.ws {
		out = append(out, "workspace")
	}
	return out, notice
}

// remoteCall runs fn on the panel goroutine for a remote and adds what the
// call did to its answer: the rows it changed and the notice it raised,
// measured there (a knob turned on the scope, or another remote's call, is
// not in them), and the sheet values' count after it, so the remote knows
// which read of /api/sheets shows the change.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-220
func (c *Controller) remoteCall(row string, fn func() SetResult) SetResult {
	var res SetResult
	if !c.onPanel(func() {
		m := c.markChanges()
		res = fn()
		res.Changed, res.Notice = c.changesSince(m)
	}) {
		return SetResult{Row: row, Error: "the panel is busy"}
	}
	c.rmu.Lock()
	res.SheetsSeq = c.remote.sheetsSeq
	c.rmu.Unlock()
	return res
}

// publish recomputes the snapshot and bumps Seq when it changed.
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) publish() {
	s := c.snap()
	st := c.status(s)
	raw, _ := json.Marshal(st)
	h := fnv.New64a()
	h.Write(raw)
	sig := h.Sum64()
	c.rmu.Lock()
	if s.now.Sub(c.remote.sheetsActive) < sheetsWindow {
		ss := sheetSig(s)
		if ss != c.remote.sheetSig || c.remote.sheetsSeq == 0 {
			c.remote.sheetsSeq++
			c.remote.sheetSig = ss
		}
		sig ^= ss * 0x9E3779B97F4A7C15
	}
	if c.remote.wake == nil {
		c.remote.wake = make(chan struct{})
	}
	if sig != c.remote.sig || c.remote.seq == 0 {
		c.remote.seq++
		c.remote.sig = sig
		close(c.remote.wake)
		c.remote.wake = make(chan struct{})
	}
	st.Seq, st.SheetsSeq = c.remote.seq, c.remote.sheetsSeq
	c.remote.status = st
	c.remote.pubAt = s.now
	c.rmu.Unlock()
}

// PanelStatus is the current panel snapshot (with its Seq).
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) PanelStatus() Status {
	c.publish()
	c.rmu.Lock()
	defer c.rmu.Unlock()
	return c.remote.status
}

// WaitPanel parks until the snapshot's Seq differs from seq or the timeout
// (at most 10 s) passes, and returns the snapshot either way.
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) WaitPanel(seq uint64, timeout time.Duration) Status {
	timeout = min(max(timeout, 0), 10*time.Second)
	c.publish()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		c.rmu.Lock()
		c.remote.sheetsActive = c.now()
		cur, wake, st := c.remote.seq, c.remote.wake, c.remote.status
		c.rmu.Unlock()
		if cur != seq {
			return st
		}
		select {
		case <-wake:
		case <-deadline.C:
			c.publish()
			c.rmu.Lock()
			defer c.rmu.Unlock()
			return c.remote.status
		}
	}
}

// Sheets is the whole sheet model, every tab and row with its value.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) Sheets() SheetsModel {
	c.rmu.Lock()
	c.remote.sheetsActive = c.now()
	c.rmu.Unlock()
	c.publish()
	// the counts before the read: a change while it runs counts once more
	var m SheetsModel
	c.rmu.Lock()
	m.Seq, m.SheetsSeq = c.remote.seq, c.remote.sheetsSeq
	c.rmu.Unlock()
	s := c.snap()
	for _, t := range sheetTabs {
		tm := TabModel{ID: t.id, Name: t.name, Title: t.title, Key: t.key}
		for _, r := range t.rows {
			tm.Rows = append(tm.Rows, rowModel(s, r))
		}
		if t.id == "trig" {
			// whether the FPGA decides the trigger, or why it cannot yet
			for _, l := range tabStatus(c, s, t) {
				tm.Notes = append(tm.Notes, TabNote{Text: l.Text, Warn: l.Col == wsview.Warn})
			}
		}
		m.Tabs = append(m.Tabs, tm)
	}
	return m
}

// onPanel runs fn on the panel goroutine and waits for it (bounded), so a
// remote change is ordered with the knobs and keys. Without the panel loop
// (no panel, tests) it runs fn here.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) onPanel(fn func()) bool {
	if !c.looping.Load() {
		c.inlineMu.Lock()
		defer c.inlineMu.Unlock()
		c.noteInput(false) // as the panel loop books it
		fn()
		return true
	}
	done := make(chan struct{})
	select {
	case c.inject <- func() { fn(); close(done) }:
	case <-time.After(3 * time.Second):
		return false
	}
	select {
	case <-done:
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

// SheetSet sets a row from the remote: value is its raw value (an option's
// value or label, a number in the row's unit, the match text) or text is
// typed entry ("1.25V", "115200", "C2 Fall", "55 AA").
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) SheetSet(id string, value any, text string) SetResult {
	return c.remoteCall(id, func() SetResult { return c.setRow(id, value, text, true) })
}

// SheetAction runs an action of a row (the first when action is empty). An
// Autoset it starts is named in the answer: the sweep's rows come with the
// snapshot when it ends (Status.AutosetDone, AutosetChanged).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-220
func (c *Controller) SheetAction(id, action string) SetResult {
	return c.remoteCall(id, func() SetResult {
		c.mu.Lock()
		run := c.autosetRun
		c.mu.Unlock()
		res := c.runAction(id, action, true)
		c.mu.Lock()
		if c.autosetRun != run && c.autosetBusy {
			res.Autoset = c.autosetRun
		}
		c.mu.Unlock()
		return res
	})
}

// RemoteWorkspace switches to a workspace for the remote ("bus", "BUS" or
// "SPEC"), as F1..F5 do with no menu open, whatever the LCD shows: a sheet,
// its edit or the match editor open there stays exactly as it is, where an
// F-key would pick a sheet choice instead. Only the BUS pane's own byte
// picker closes when BUS is left. The workspace already shown stays as it
// is (its key again is RemoteAgain). The LCD confirms with a REMOTE line.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-220
func (c *Controller) RemoteWorkspace(name string) SetResult {
	ws := wsIndex(name)
	if ws < 0 {
		return SetResult{Row: "workspace", Error: fmt.Sprintf("no workspace %q: SCOPE, BUS, SPECTRUM, MEASURE or COMPARE", name)}
	}
	return c.remoteCall("workspace", func() SetResult { return c.remoteWorkspace(wsview.WS(ws)) })
}

// wsIndex finds a workspace by its id, name or tag, any case (-1: none).
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-220
func wsIndex(name string) int {
	n := strings.TrimSpace(name)
	for i := 0; i < wsview.Count; i++ {
		if strings.EqualFold(n, wsview.IDs[i]) || strings.EqualFold(n, wsview.Names[i]) || strings.EqualFold(n, wsview.Tags[i]) {
			return i
		}
	}
	return -1
}

// autosetRefusal: while Autoset sweeps, a remote's changes wait, as the
// panel's keys do (its scale changes and acquisitions are not to be raced);
// only Autoset itself runs, as a second press cancels it.
const autosetRefusal = "Autoset is running: changes wait until it is done (Autoset again cancels it)"

// remoteWorkspace is RemoteWorkspace on the panel goroutine.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-220
func (c *Controller) remoteWorkspace(ws wsview.WS) SetResult {
	if c.AutosetBusy() {
		return SetResult{Row: "workspace", Error: autosetRefusal}
	}
	defer c.afterInput()
	c.mu.Lock()
	cur := c.ui.ws
	if ws != cur && c.ui.wpick {
		c.closePickLocked() // the BUS pane's picker belongs to BUS
	}
	c.mu.Unlock()
	if ws != cur {
		c.switchWS(ws)
		c.remoteLine("workspace", "REMOTE  workspace "+wsview.Names[ws])
	}
	return SetResult{OK: true, Row: "workspace", Value: wsview.IDs[ws], Text: wsview.Names[ws]}
}

// RemoteAgain is the active workspace's key again for the remote: its next
// view (COMPARE: stack a master, or cancel the one stacking), whatever the
// LCD shows; an open sheet stays as it is. A refused master says why. want
// names the workspace the remote meant ("" the active one): while another
// is active the call is refused, so a remote that has not yet seen a
// switch on the scope does not run that workspace's again.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-220
func (c *Controller) RemoteAgain(want string) SetResult {
	ws := -1
	if strings.TrimSpace(want) != "" {
		if ws = wsIndex(want); ws < 0 {
			return SetResult{Row: "again", Error: fmt.Sprintf("no workspace %q: SCOPE, BUS, SPECTRUM, MEASURE or COMPARE", want)}
		}
	}
	return c.remoteCall("again", func() SetResult {
		c.mu.Lock()
		cur := c.ui.ws
		c.mu.Unlock()
		if ws >= 0 && wsview.WS(ws) != cur {
			return SetResult{Row: "again", Error: wsview.Names[ws] + " again: the scope shows " + wsview.Names[cur] + " now"}
		}
		return c.remoteAgain()
	})
}

// menuRefusal: with a menu open on the LCD, Adjust, Position and their
// pushes move that menu, so a remote's turns and pushes of them wait.
const menuRefusal = "A menu is open on the scope: Adjust and Position move it there, so they wait here until it closes"

// remoteKeys are the keys a remote presses: none of them opens, moves or
// closes a menu on the LCD (Default Setup and Auto leave a remote's LCD menus
// as they are). The menu keys and the F-keys work the LCD's menus: a remote
// opens its own.
var remoteKeys = map[int]bool{btnRunStop: true, btnSingle: true, btnAuto: true, btnDefaultSetup: true, btnSet50: true, btnForce: true,
	btnPrint: true, btnCh1VdivPush: true, btnCh2VdivPush: true, btnCh1PosPush: true, btnCh2PosPush: true, btnTrigLvlPush: true,
	btnTdivPush: true, btnHorizPosPush: true, btnAdjustPsh: true}

// RemoteKey presses a key of the panel for a remote and answers once it has
// run, with the rows it changed and the notice it raised (remoteCall). It
// is refused while Autoset sweeps (but AUTO, which cancels the sweep), as
// the panel's own keys are ignored then, and where it would open, move or
// close a menu on the LCD: the menu keys and F-keys, the Adjust and Position
// pushes while a menu is open there (any key with nomenu), and the Adjust
// push where it opens one (SCOPE's row sheet, BUS's byte picker). Like a
// remote's actions it leaves the LCD's menus as they are.
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-220
func (c *Controller) RemoteKey(name string, nomenu bool) SetResult {
	code, ok := nameCode(name)
	if !ok {
		return SetResult{Row: name, Error: fmt.Sprintf("no key %q", name)}
	}
	if !remoteKeys[code] {
		return SetResult{Row: name, Error: fmt.Sprintf("the %s key works the scope's menus: a remote opens its own", name)}
	}
	return c.remoteCall(name, func() SetResult {
		if why := c.remoteWaits(code != btnAuto, nomenu || code == btnAdjustPsh || code == btnHorizPosPush); why != "" {
			return SetResult{Row: name, Error: why}
		}
		if code == btnAdjustPsh && c.adjustOpensMenu() {
			return SetResult{Row: name, Error: "Here the Adjust push opens a menu on the scope: the phone opens it on itself instead"}
		}
		c.mu.Lock()
		c.apiDepth++
		c.mu.Unlock()
		c.button(code)
		c.mu.Lock()
		c.apiDepth--
		c.mu.Unlock()
		return SetResult{OK: true, Row: name}
	})
}

// RemoteKnob turns a knob of the panel (dir +1 clockwise, steps detents)
// for a remote, refused and answered as RemoteKey: S/DIV, Adjust and
// Position (and any knob with nomenu) wait while a menu is open on the LCD,
// where they would turn its tab, move its row or change it.
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-220
func (c *Controller) RemoteKnob(name string, dir, steps int, nomenu bool) SetResult {
	if !knobNames[name] {
		return SetResult{Row: name, Error: fmt.Sprintf("no knob %q", name)}
	}
	dir = map[bool]int{true: 1, false: -1}[dir >= 0]
	menuKnob := name == "tdiv" || name == "adjust" || name == "horizpos"
	return c.remoteCall(name, func() SetResult {
		if why := c.remoteWaits(true, nomenu || menuKnob); why != "" {
			return SetResult{Row: name, Error: why}
		}
		c.resync()
		c.dispatch(name, dir, max(steps, 1))
		return SetResult{OK: true, Row: name}
	})
}

// adjustOpensMenu: the Adjust push opens a menu on the LCD in this
// workspace: SCOPE's opens its target row's sheet, BUS's the byte picker
// while it decodes packets.
// TRLC-LINKS: REQ-SDS-213, REQ-SDS-220
func (c *Controller) adjustOpensMenu() bool {
	s := c.snap()
	switch {
	case s.ui.ws == wsview.Scope:
		return true
	case s.ui.ws == wsview.Bus && !s.ui.busEye && !s.ui.busStream:
		return busDecoding(s)
	}
	return false
}

// remoteWaits is why a remote's key or knob does not run now ("" runs it):
// Autoset sweeping (when the key waits for it), or a menu open on the LCD
// (with nomenu).
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-220
func (c *Controller) remoteWaits(autoset, nomenu bool) string {
	if autoset && c.AutosetBusy() {
		return autosetRefusal
	}
	c.mu.Lock()
	menu := c.ui.open || c.ui.wpick
	c.mu.Unlock()
	if nomenu && menu {
		return menuRefusal
	}
	return ""
}

// remoteAgain is RemoteAgain on the panel goroutine.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-218, REQ-SDS-220
func (c *Controller) remoteAgain() SetResult {
	if c.AutosetBusy() {
		return SetResult{Row: "again", Error: autosetRefusal}
	}
	defer c.afterInput()
	c.mu.Lock()
	ws, stacking := c.ui.ws, c.stack != nil
	c.mu.Unlock()
	switch {
	case ws == wsview.Compare && stacking:
		c.stackCancel("", false)
	case ws == wsview.Compare:
		if err := c.stackStart(); err != nil {
			return SetResult{Row: "again", Error: err.Error()} // the LCD's notice, word for word
		}
	default:
		c.nextView()
	}
	view := workspaceView(c.snap(), ws)
	c.remoteLine("again", "REMOTE  "+wsview.Names[ws]+" "+view)
	return SetResult{OK: true, Row: "again", Value: view, Text: view}
}

// againNext is what the active workspace's key again shows next. The pane's
// own again line is not drawn while a menu covers it, so the remote reads
// this one.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-220
func (c *Controller) againNext(s *sv) string {
	switch s.ui.ws {
	case wsview.Bus:
		return busViewNext(&s.ui)
	case wsview.Spectrum:
		return viewName(nextSpecView(s.viewMode))
	case wsview.Measure:
		return map[bool]string{true: "Statistics", false: "All values"}[s.ui.measAll]
	case wsview.Compare:
		// the key's job: whether the trigger is stable enough is said when it
		// is pressed (a verdict that flickers with the signal would wake every
		// remote on every frame)
		return map[bool]string{true: "cancel stack", false: "stack master"}[s.stacking]
	}
	return viewName(map[bool]int{true: viewXY, false: viewYT}[s.viewMode == viewYT])
}

// TypeText applies text to the LCD's current context: the highlighted sheet
// row, the byte picker or the match editor.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-214
func (c *Controller) TypeText(text string) SetResult {
	var res SetResult
	if !c.onPanel(func() { res = c.typeText(text) }) {
		return SetResult{Error: "the panel is busy"}
	}
	return res
}

// setRow applies a value to a row through its setter (panel goroutine).
// remote marks a phone change: the LCD shows a REMOTE note.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) setRow(id string, value any, text string, remote bool) SetResult {
	r := rowByID[id]
	if r == nil {
		return SetResult{Row: id, Error: fmt.Sprintf("no row %q", id)}
	}
	if remote && c.AutosetBusy() {
		return SetResult{Row: id, Error: autosetRefusal}
	}
	s := c.snap()
	err := c.applyRow(r, s, value, text)
	defer c.afterInput()
	if err != nil {
		return SetResult{Row: id, Error: err.Error()}
	}
	s = c.snap()
	m := rowModel(s, r)
	if remote {
		c.remoteNote(r, m.Text)
	}
	return SetResult{OK: true, Row: id, Value: m.Value, Text: m.Text}
}

// applyRow sets a row from a raw value or typed text.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) applyRow(r *row, s *sv, value any, text string) error {
	switch r.kind {
	case wsview.Enum:
		if text != "" {
			v, err := r.parseEnum(s, text)
			if err != nil {
				return err
			}
			return r.setEnum(c, s, v)
		}
		switch v := value.(type) {
		case float64:
			if v != math.Trunc(v) {
				return fmt.Errorf("%s: %v is not an option", r.label, v)
			}
			return r.setEnum(c, s, int(v))
		case int:
			return r.setEnum(c, s, v)
		case bool:
			return r.setEnum(c, s, b2ic(v))
		case string:
			n, err := r.parseEnum(s, v)
			if err != nil {
				return err
			}
			return r.setEnum(c, s, n)
		}
		return fmt.Errorf("%s: give a value or text", r.label)
	case wsview.Number:
		if text == "" {
			switch v := value.(type) {
			case float64:
				return r.setNumber(c, s, v)
			case int:
				return r.setNumber(c, s, float64(v))
			case string:
				text = v
			default:
				return fmt.Errorf("%s: give a value or text", r.label)
			}
		}
		if r.id == "bus.i2c_address" && strings.EqualFold(strings.TrimSpace(text), "any") {
			return r.setNumber(c, s, -1)
		}
		if r.id == "trig.holdoff" && strings.EqualFold(strings.TrimSpace(text), "off") {
			return r.setNumber(c, s, 0)
		}
		v, err := parseNumber(text, r.unitFor(s))
		if err != nil {
			return fmt.Errorf("%s: %v", r.label, err)
		}
		return r.setNumber(c, s, v)
	case wsview.Bytes:
		if text == "" {
			t, ok := value.(string)
			if !ok {
				return fmt.Errorf("%s: give the bytes as hex like 55 AA, or \"text\"", r.label)
			}
			text = t
		}
		toks, err := parseMatchText(text)
		if err != nil {
			return err
		}
		return c.setMatchTokens(toks)
	}
	return fmt.Errorf("%s is an action: use /api/sheets/action", r.label)
}

// runAction runs one of a row's actions (panel goroutine).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) runAction(id, action string, remote bool) SetResult {
	r := rowByID[id]
	if r == nil {
		return SetResult{Row: id, Error: fmt.Sprintf("no row %q", id)}
	}
	if remote && id != "sys.autoset" && c.AutosetBusy() {
		return SetResult{Row: id, Error: autosetRefusal}
	}
	if r.kind == wsview.Number && r.presets != nil {
		return c.runPreset(r, action, remote)
	}
	if r.kind != wsview.Action && r.kind != wsview.Slot {
		return SetResult{Row: id, Error: fmt.Sprintf("%s is a setting, not an action: use /api/sheets/set", r.label)}
	}
	s := c.snap()
	acts := r.acts(s)
	var a *act
	for i := range acts {
		if action == "" || strings.EqualFold(acts[i].id, action) || strings.EqualFold(acts[i].t, action) {
			a = &acts[i]
			break
		}
	}
	if a == nil {
		var ids []string
		for _, x := range acts {
			ids = append(ids, x.id)
		}
		return SetResult{Row: id, Error: fmt.Sprintf("%s has no action %q (%s)", r.label, action, strings.Join(ids, ", "))}
	}
	if a.dim {
		return SetResult{Row: id, Error: fmt.Sprintf("%s: %s is not available now", r.label, a.t)}
	}
	c.mu.Lock()
	c.apiDepth++
	c.mu.Unlock()
	err := a.run(c)
	c.mu.Lock()
	c.apiDepth--
	c.mu.Unlock()
	defer c.afterInput()
	if err != nil {
		return SetResult{Row: id, Error: err.Error()}
	}
	if remote {
		c.remoteNote(r, a.t)
	}
	m := rowModel(c.snap(), r)
	return SetResult{OK: true, Row: id, Text: m.Text}
}

// runPreset applies one of a number row's labelled values (panel
// goroutine), as its F-key on the sheet does: by label, the first when
// label is empty.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-220
func (c *Controller) runPreset(r *row, label string, remote bool) SetResult {
	s := c.snap()
	ps := r.presets(s)
	var p *preset
	for i := range ps {
		if label == "" || strings.EqualFold(ps[i].t, strings.TrimSpace(label)) {
			p = &ps[i]
			break
		}
	}
	if p == nil {
		var names []string
		for _, x := range ps {
			names = append(names, x.t)
		}
		return SetResult{Row: r.id, Error: fmt.Sprintf("%s has no preset %q (%s)", r.label, label, strings.Join(names, ", "))}
	}
	defer c.afterInput()
	var err error
	if p.run != nil {
		err = p.run(c)
	} else {
		err = r.setNumber(c, s, p.v)
	}
	if err != nil {
		return SetResult{Row: r.id, Error: err.Error()}
	}
	m := rowModel(c.snap(), r)
	if remote {
		c.remoteNote(r, m.Text)
	}
	return SetResult{OK: true, Row: r.id, Value: m.Value, Text: m.Text}
}

// remoteNote shows "REMOTE  TRIG level 1.25 V" on the LCD for a moment; a
// repeat on the same row within it only updates the text.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) remoteNote(r *row, text string) {
	c.remoteLine(r.id, "REMOTE  "+sheetTabs[rowTab[r.id]].name+" "+strings.ToLower(r.label)+" "+text)
}

// remoteLine shows a REMOTE line on the LCD for a moment; key names what
// it is about, so a repeat replaces it in place.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-220
func (c *Controller) remoteLine(key, line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ui.remote, c.ui.remoteRow, c.ui.remoteUnt = line, key, c.now().Add(remoteNoteFor)
}

// typeText applies text to the LCD's current context (panel goroutine).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-214
func (c *Controller) typeText(text string) SetResult {
	if c.AutosetBusy() {
		return SetResult{Error: autosetRefusal}
	}
	s := c.snap()
	u := &s.ui
	defer c.afterInput()
	switch {
	case u.wpick || u.open && u.sub == subPick:
		toks, err := parseMatchText(text)
		if err != nil {
			return SetResult{Error: err.Error()}
		}
		if len(toks) == 0 {
			return SetResult{Error: "no bytes: type hex like 55 AA, or \"text\""}
		}
		if err := c.setMatchTokens(toks); err != nil {
			return SetResult{Error: err.Error()}
		}
		c.pickCancel()
		if err := c.armSerial(); err != nil {
			return SetResult{Row: "bus.match", Error: err.Error()}
		}
		return SetResult{OK: true, Row: "bus.match", Value: strings.Join(toks, " "), Text: strings.Join(toks, " ")}
	case u.open && u.sub == subMatch:
		toks, err := parseMatchText(text)
		if err != nil {
			return SetResult{Error: err.Error()}
		}
		c.mu.Lock()
		c.kb.toks, c.kb.cur, c.kb.typing = toks, len(toks), false
		c.mu.Unlock()
		if err := c.matchOK(); err != nil {
			return SetResult{Error: err.Error()}
		}
		return SetResult{OK: true, Row: "bus.match", Value: strings.Join(toks, " "), Text: matchText(c.snap())}
	case u.open:
		r := curRow(u)
		if err := c.applyRow(r, s, nil, text); err != nil {
			return SetResult{Row: r.id, Error: err.Error()}
		}
		c.setAdjTarget(r)
		m := rowModel(c.snap(), r)
		return SetResult{OK: true, Row: r.id, Value: m.Value, Text: m.Text}
	}
	return SetResult{Error: "nothing to type into: open a sheet row, the byte picker or the match editor"}
}

// afterInput latches the lamps and publishes the snapshot after a change.
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) afterInput() {
	c.mu.Lock()
	c.setupSameAt = time.Time{} // the input may have changed a saved setting
	c.mu.Unlock()
	c.pushLEDs()
	c.publish()
}
