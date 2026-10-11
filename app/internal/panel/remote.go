// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"encoding/json"
	"fmt"
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
// long-poll wakes on it.

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
	Value     any        `json:",omitempty"` // raw: option value, number in Unit, or the match text
	Text      string     // as written on the sheet
	Options   []OptModel `json:",omitempty"`
	Unit      string     `json:",omitempty"`
	Min       *float64   `json:",omitempty"`
	Max       *float64   `json:",omitempty"`
	Step      *float64   `json:",omitempty"` // one knob step (absent: a ladder)
	Ladder    []float64  `json:",omitempty"`
	Actions   []ActModel `json:",omitempty"`
}

// TabModel is one sheet tab.
type TabModel struct {
	ID, Name, Title, Key string
	Rows                 []RowModel
}

// SheetsModel is the whole sheet model for the remote.
type SheetsModel struct {
	Seq  uint64
	Tabs []TabModel
}

// SheetStatus is the LCD's open sheet.
type SheetStatus struct {
	Open     bool
	Tab      string // tab id
	TabName  string
	TabTitle string
	Tabs     []string // in this workspace's order
	Edit     bool
	Row      *RowModel // the highlighted row
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
	Again       string
	Notice      string
	Remote      string
	Picker      PickerStatus
	Lamps       uint16
	LampsLit    []string
	Display     Display
}

// SetResult is the outcome of a remote set, action or typed entry.
type SetResult struct {
	OK    bool   `json:"ok"`
	Row   string `json:"row,omitempty"`
	Value any    `json:"value,omitempty"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

// remoteState is the published snapshot (guarded by Controller.rmu).
type remoteState struct {
	seq          uint64
	sig          uint64
	status       Status
	wake         chan struct{}
	sheetsActive time.Time
}

// sheetsWindow is how long after a remote's last sheet request the sheet
// values are watched for the Seq.
const sheetsWindow = 60 * time.Second

// lampNames names the lit lamps of a word.
func lampNames(w uint16) []string {
	var out []string
	for _, l := range []struct {
		bit  uint16
		name string
	}{{ledCH1, "CH1"}, {ledCH2, "CH2"}, {ledRun, "RUN"}, {ledStop, "STOP"}, {ledSingle, "SINGLE"}, {ledMeasure, "MEASURE"},
		{ledAcquire, "ACQUIRE"}, {ledDisplay, "DISPLAY"}, {ledUtility, "UTILITY"}, {ledSaveRec, "SAVE/RECALL"}} {
		if w&l.bit != 0 {
			out = append(out, l.name)
		}
	}
	return out
}

// workspaceView names a workspace's view.
func workspaceView(s *sv, ws wsview.WS) string {
	switch ws {
	case wsview.Bus:
		return map[bool]string{true: "Eye diagram", false: "Packets"}[s.ui.busEye]
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
		View: workspaceView(s, u.ws), Notice: v.Notice, Remote: d.Remote, Display: d}
	for i := range st.Views {
		st.Views[i] = workspaceView(s, wsview.WS(i))
	}
	c.mu.Lock()
	st.Lamps = c.ledWord
	c.mu.Unlock()
	st.LampsLit = lampNames(st.Lamps)
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
		st.Sheet.Row = &rm
		st.Tool = []ToolSeg{{"S/DIV", "tab"}, {"Adjust", "row"}, {"push", "edit"}, {"Position", "change the row"}, {"push", "default"}}
		if u.sub == subMatch {
			st.Tool = []ToolSeg{{"Adjust", "key"}, {"push", "type"}, {"Position", "cursor"}, {"push", "clear"}}
		} else if u.sub == subPick {
			st.Tool = []ToolSeg{{"Adjust", "first byte"}, {"push", "cancel"}, {"Position", "first byte"}, {"push", "back to start"}}
		}
	} else {
		st.Tool, st.Again = paneTool(c, s, v)
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

// paneTool is the tool line in workspace mode (SCOPE's from its Adjust tag).
func paneTool(c *Controller, s *sv, v wsview.View) ([]ToolSeg, string) {
	var p *wsview.Pane
	switch {
	case v.Picker != nil:
		return []ToolSeg{{"Adjust", "first byte"}, {"push", "cancel"}, {"Position", "first byte"}, {"push", "back to start"}}, ""
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
	pos := "trigger position"
	if s.zoom > 1 {
		pos = "zoom position"
	}
	return []ToolSeg{{"Adjust", adj}, {"push", "open its sheet row"}, {"Position", pos}, {"push", "centre"}},
		"F1 again: " + viewName(map[bool]int{true: viewXY, false: viewYT}[s.viewMode == viewYT])
}

// displayAndView is Snapshot from a state read already taken.
func (c *Controller) displayAndView(s *sv) (Display, wsview.View) {
	c.mu.Lock()
	busy, amsg := c.autosetBusy, c.autosetMsg
	c.mu.Unlock()
	d := Display{ShowC1: s.chDisp[0], ShowC2: s.chDisp[1], ShowMeas: s.showMeas, ViewMode: s.viewMode, MathMode: s.mathMode,
		Zoom: max(s.zoom, 1), ZoomOff: s.zoomOff, Persist: s.persist, FFTSrc: s.fftSrc,
		DecProto: s.decProto, DecBaud: s.decBaud, DecChA: s.decChA, DecChB: s.decChB, DecCPOL: s.decCPOL, DecCPHA: s.decCPHA,
		DecFormat: s.decFormat, CurOn: s.curOn, CurType: s.curType, CurSel: s.curSel, CurX: s.curX, CurY: s.curY,
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
	m := RowModel{ID: r.id, Label: r.label, Available: r.relevant(s), Help: r.help, Text: r.valueText(s)}
	switch r.kind {
	case wsview.Enum:
		m.Kind, m.Value = "enum", r.get(s)
		for _, o := range r.options(s) {
			m.Options = append(m.Options, OptModel{o.v, o.t})
		}
	case wsview.Number:
		m.Kind, m.Value, m.Unit = "number", finiteOr(r.val(s)), r.unit
		lo, hi := r.lim(s)
		m.Min, m.Max = ptr(finiteOr(lo)), ptr(finiteOr(hi))
		if st := r.step(s); st > 0 {
			m.Step = ptr(st)
		}
		if r.ladder != nil {
			m.Ladder = r.ladder(s)
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
func finiteOr(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func ptr(v float64) *float64 { return &v }

// sheetSig hashes every row's value and availability (not live counters),
// so the Seq follows a setting changed anywhere.
func sheetSig(s *sv) uint64 {
	h := fnv.New64a()
	var b [8]byte
	put := func(x uint64) {
		for i := range b {
			b[i] = byte(x >> (8 * i))
		}
		h.Write(b[:])
	}
	for _, t := range sheetTabs {
		for _, r := range t.rows {
			h.Write([]byte(r.id))
			put(uint64(b2ic(r.relevant(s))))
			switch r.kind {
			case wsview.Enum:
				put(uint64(r.get(s)))
				put(uint64(len(r.options(s))))
			case wsview.Number:
				put(math.Float64bits(r.val(s)))
			case wsview.Bytes:
				h.Write([]byte(matchText(s)))
			case wsview.Slot:
				put(uint64(b2ic(s.slotUsed[r.slot])))
				h.Write([]byte(s.slotSum[r.slot]))
			}
		}
	}
	return h.Sum64()
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
		sig ^= sheetSig(s) * 0x9E3779B97F4A7C15
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
	st.Seq = c.remote.seq
	c.remote.status = st
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
	s := c.snap()
	var m SheetsModel
	for _, t := range sheetTabs {
		tm := TabModel{ID: t.id, Name: t.name, Title: t.title, Key: t.key}
		for _, r := range t.rows {
			tm.Rows = append(tm.Rows, rowModel(s, r))
		}
		m.Tabs = append(m.Tabs, tm)
	}
	c.rmu.Lock()
	m.Seq = c.remote.seq
	c.rmu.Unlock()
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
	var res SetResult
	if !c.onPanel(func() { res = c.setRow(id, value, text, true) }) {
		return SetResult{Row: id, Error: "the panel is busy"}
	}
	return res
}

// SheetAction runs an action of a row (the first when action is empty).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) SheetAction(id, action string) SetResult {
	var res SetResult
	if !c.onPanel(func() { res = c.runAction(id, action, true) }) {
		return SetResult{Row: id, Error: "the panel is busy"}
	}
	return res
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
		v, err := parseNumber(text, r.unit)
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

// remoteNote shows "REMOTE  TRIG level 1.25 V" on the LCD for a moment; a
// repeat on the same row within it only updates the text.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) remoteNote(r *row, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ui.remote = "REMOTE  " + sheetTabs[rowTab[r.id]].name + " " + strings.ToLower(r.label) + " " + text
	c.ui.remoteRow = r.id
	c.ui.remoteUnt = c.now().Add(remoteNoteFor)
}

// typeText applies text to the LCD's current context (panel goroutine).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-214
func (c *Controller) typeText(text string) SetResult {
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
	c.pushLEDs()
	c.publish()
}
