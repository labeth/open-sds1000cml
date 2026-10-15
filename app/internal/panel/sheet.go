// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"strings"
	"time"

	"open-sds/app/internal/panel/wsview"
)

// Settings sheets (REQ-SDS-212): full screen, every option written out.
// S/DIV turns the tab, Adjust moves the row, F1..F5 pick the highlighted
// row's choices, Position changes the row directly and its push resets it.
// Menu On/Off and the dedicated keys open and close them; the key that
// opened a tab closes it. Actions run only from their labelled F-key.

// keyTabs are the dedicated keys that open a tab.
var keyTabs = map[string]int{
	"trigmenu": tabTrig, "ch1": tabCh1, "ch2": tabCh2, "horizmenu": tabTime, "acquire": tabAcq,
	"display": tabView, "measure": tabMeas, "cursors": tabCurs, "math": tabCmp, "ref": tabCmp,
	"utility": tabBus, "saverecall": tabFile, "help": tabSys,
}

// keyRows are the keys that open their tab on a fixed row.
var keyRows = map[string]string{"math": "cmp.math", "ref": "cmp.store_a", "help": "sys.help"}

// keyNames are the printed names of the keys, for the hint line.
var keyNames = map[string]string{"math": "Math", "ref": "Ref", "help": "Help"}

// tabOrder is a workspace's sheet tab order: its own tabs, then the rest.
// TRLC-LINKS: REQ-SDS-212
func tabOrder(ws wsview.WS) []int {
	var out []int
	for _, id := range wsDefs[ws].tabs {
		out = append(out, tabIndex(id))
	}
	for i := 0; i < numTabs; i++ {
		own := false
		for _, o := range out {
			own = own || o == i
		}
		if !own {
			out = append(out, i)
		}
	}
	return out
}

// fkey is one F-key choice of a menu.
type fkey struct {
	label, value string
	sel, dim     bool
	run          func() error
	row          bool // a row's value changed: SCOPE's Adjust then tunes it
}

// leaveModesLocked ends an edit or a sub-mode (c.mu held).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) leaveModesLocked() {
	c.ui.edit = false
	if c.ui.sub == subPick {
		c.toastLocked(pickCancelled, noticeFor)
	}
	if c.ui.sub != subNone {
		c.ui.sub = subNone
		c.kb.open = false
		if !c.ui.wpick {
			c.ui.pick = pickState{}
		}
	}
}

// closePickLocked closes the workspace byte picker unused, with a notice
// that it is cancelled (c.mu held).
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) closePickLocked() {
	if c.ui.wpick {
		c.toastLocked(pickCancelled, noticeFor)
	}
	c.ui.wpick = false
	c.ui.pick = pickState{}
}

// closeSheetLocked closes the sheet, remembering the tab for this workspace.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) closeSheetLocked() {
	if c.ui.open {
		c.ui.wsTab[c.ui.ws] = c.ui.tab
	}
	c.ui.open, c.ui.via = false, ""
	c.leaveModesLocked()
}

// openSheetLocked opens tab ti (c.mu held).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) openSheetLocked(ti int, via string) {
	c.closePickLocked()
	c.leaveModesLocked()
	c.ui.posJob = false // Position is back on the trigger when the sheet closes
	c.ui.open, c.ui.tab, c.ui.via = true, ti, via
}

// openWsSheetLocked is Menu On/Off: this workspace's sheet where it was left
// here, the first time on the workspace's own first tab.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) openWsSheetLocked() {
	ti := c.ui.wsTab[c.ui.ws]
	if ti < 0 || ti >= numTabs {
		ti = tabIndex(wsDefs[c.ui.ws].tabs[0])
	}
	c.openSheetLocked(ti, "menu")
}

// keyTab is a dedicated key: open its tab, or close it when it is showing.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) keyTab(key string) {
	ti, ok := keyTabs[key]
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ui.open && c.ui.tab == ti && (keyRows[key] == "" || c.ui.via == key) {
		c.closeSheetLocked()
		return
	}
	c.openSheetLocked(ti, key)
	if id := keyRows[key]; id != "" {
		c.ui.rowAt[ti] = rowIndex(id)
	}
}

// chanKey is the 1 or 2 key: the channel's tab, switching an off channel on;
// on its own tab it closes the sheet (also right after Channel = Off).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) chanKey(ch int) {
	ti := tabCh1 + ch
	c.mu.Lock()
	c.closePickLocked()
	if c.ui.open && c.ui.tab == ti {
		c.closeSheetLocked()
		c.mu.Unlock()
		return
	}
	shown := c.chDisp[ch]
	if !shown {
		c.chDisp[ch] = true
		c.openSheetLocked(ti, fmt.Sprintf("ch%d", ch+1))
		c.toastLocked(fmt.Sprintf("CH%d on", ch+1), 900*time.Millisecond)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.keyTab(fmt.Sprintf("ch%d", ch+1))
}

// curRow is the sheet's highlighted row.
// TRLC-LINKS: REQ-SDS-212
func curRow(u *uiState) *row {
	t := sheetTabs[u.tab]
	return t.rows[clampInt(u.rowAt[u.tab], 0, len(t.rows)-1)]
}

// sheetKeys are the F-key choices of the open sheet (or its sub-mode).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) sheetKeys(s *sv) (string, []fkey) {
	u := &s.ui
	switch u.sub {
	case subMatch:
		return "MATCH EDITOR", c.matchKeys(s)
	case subPick:
		return "PICK BYTES", c.pickKeys(s)
	}
	r := curRow(u)
	title := strings.ToUpper(r.label)
	if u.edit {
		title = "EDIT " + title
	}
	return title, c.rowKeys(s, r)
}

// rowKeys are a row's F-key choices: options, Down/Up and presets, actions,
// the match entry, or a slot's Save and Recall.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) rowKeys(s *sv, r *row) []fkey {
	var out []fkey
	switch r.kind {
	case wsview.Enum:
		cur := r.get(s)
		for _, o := range r.quickOpts(s) {
			o := o
			out = append(out, fkey{label: o.t, sel: o.v == cur, run: func() error { return r.setEnum(c, c.snap(), o.v) }})
		}
	case wsview.Number:
		out = append(out, fkey{label: "Down", value: "one step", row: true, run: func() error { return r.stepN(c, -1) }},
			fkey{label: "Up", value: "one step", row: true, run: func() error { return r.stepN(c, 1) }})
		if r.presets != nil {
			v := r.val(s)
			for _, p := range r.presets(s) {
				if len(out) >= 5 {
					break
				}
				p := p
				k := fkey{label: p.t, row: true}
				if p.run != nil {
					k.run = func() error { return p.run(c) }
				} else {
					k.sel = abs64(v-p.v) <= 1e-12+1e-9*abs64(p.v)
					k.run = func() error { return r.setNumber(c, c.snap(), p.v) }
				}
				out = append(out, k)
			}
		}
	case wsview.Action, wsview.Slot:
		for _, a := range r.acts(s) {
			if len(out) >= 5 {
				break
			}
			a := a
			k := fkey{label: a.t, dim: a.dim, run: func() error { return a.run(c) }}
			if r.kind == wsview.Slot {
				used := s.slotUsed[r.slot]
				switch a.id {
				case "save":
					k.value = map[bool]string{true: "overwrite", false: "store here"}[used]
				case "recall":
					k.value = "empty"
					if used {
						k.value = "restore"
						if w := c.slotWorkspace(r.slot); w >= 0 {
							k.value = "into " + wsview.Names[w]
						}
					}
				}
			}
			out = append(out, k)
		}
	case wsview.Bytes:
		out = append(out,
			fkey{label: "Edit bytes", value: "keyboard", run: func() error { c.openMatchSub(); return nil }},
			fkey{label: "From screen", value: "pick decoded", run: func() error { return c.openPickSub() }},
			fkey{label: "Any byte", value: "clear match", run: func() error { return c.setMatchTokens(nil) }})
	}
	return out
}

// abs64 is |x|.
// TRLC-LINKS: REQ-SDS-212
func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// sheetPressF is F1..F5 with a sheet open: the choice beside the key.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) sheetPressF(i int) {
	s := c.snap()
	_, keys := c.sheetKeys(s)
	if i >= len(keys) {
		return
	}
	k := keys[i]
	r := curRow(&s.ui)
	c.mu.Lock()
	if s.ui.sub == subNone {
		c.ui.edit = false
		c.ui.flashRow, c.ui.flashI, c.ui.flashUntil = r.id, i, c.now().Add(350*time.Millisecond)
	}
	c.mu.Unlock()
	if err := k.run(); err != nil {
		c.toast(err.Error(), 2000*time.Millisecond)
		return
	}
	if k.row && s.ui.sub == subNone {
		c.setAdjTarget(r)
	}
}

// setAdjTarget makes a number row SCOPE's Adjust target (the last value set).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-213
func (c *Controller) setAdjTarget(r *row) {
	if r.kind != wsview.Number {
		return
	}
	c.mu.Lock()
	c.ui.adj = r.id
	c.mu.Unlock()
}

// sheetTurnTab is S/DIV with a sheet open: the previous or next tab.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) sheetTurnTab(dir int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.leaveModesLocked()
	order := tabOrder(c.ui.ws)
	i := 0
	for k, t := range order {
		if t == c.ui.tab {
			i = k
		}
	}
	c.ui.tab, c.ui.via = order[((i+dir)%len(order)+len(order))%len(order)], ""
}

// sheetAdjust is the Adjust knob with a sheet open: the row (wrapping), the
// value while editing, or the sub-mode's cursor.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) sheetAdjust(dir, steps int) {
	c.mu.Lock()
	sub, edit := c.ui.sub, c.ui.edit
	if sub == subNone && !edit {
		n := len(sheetTabs[c.ui.tab].rows)
		c.ui.rowAt[c.ui.tab] = ((c.ui.rowAt[c.ui.tab]+dir)%n + n) % n
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	switch sub {
	case subMatch:
		c.matchMoveKey(dir * min(steps, 16))
	case subPick:
		c.pickMove(dir)
	default:
		c.sheetValueTurn(dir, steps)
	}
}

// sheetPosition is Position < > with a sheet open: change the row directly
// (the sub-mode: its other cursor).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) sheetPosition(dir, steps int) {
	c.mu.Lock()
	sub := c.ui.sub
	c.mu.Unlock()
	switch sub {
	case subMatch:
		c.matchMoveCursor(dir)
	case subPick:
		c.pickMove(dir)
	default:
		c.sheetValueTurn(dir, steps)
	}
}

// sheetValueTurn steps the highlighted row: the next or previous option, or
// the value by the knob's accelerated steps.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) sheetValueTurn(dir, steps int) {
	s := c.snap()
	r := curRow(&s.ui)
	var err error
	switch r.kind {
	case wsview.Enum:
		o := r.options(s)
		i := r.optIndex(s)
		j := clampInt(max(i, 0)+dir, 0, len(o)-1)
		if i < 0 && dir < 0 {
			j = 0
		}
		if j != i {
			err = r.setEnum(c, s, o[j].v)
		}
	case wsview.Number:
		err = r.stepN(c, dir*max(steps, 1))
		c.setAdjTarget(r)
	case wsview.Bytes:
		c.toast("Match bytes: F1 edit, F2 from screen", 1200*time.Millisecond)
	default:
		c.toast("Press an F-key to run this", 1200*time.Millisecond)
	}
	if err != nil {
		c.toast(err.Error(), 2000*time.Millisecond)
	}
}

// sheetAdjustPush is the Adjust push with a sheet open: edit / done; the
// match editor on Match bytes; picking bytes, trigger on the highlighted
// one; actions only explain (they run from F-keys).
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) sheetAdjustPush() {
	c.mu.Lock()
	sub := c.ui.sub
	c.mu.Unlock()
	switch sub {
	case subMatch:
		c.matchTypeKey()
		return
	case subPick:
		c.pickUseKey(1)
		return
	}
	s := c.snap()
	r := curRow(&s.ui)
	switch r.kind {
	case wsview.Action, wsview.Slot:
		c.toast("Nothing to edit: run it with the F-key beside its name", 1500*time.Millisecond)
	case wsview.Bytes:
		c.openMatchSub()
	default:
		c.mu.Lock()
		c.ui.edit = !c.ui.edit
		c.mu.Unlock()
	}
}

// sheetPositionPush is Position push (Zero) with a sheet open: the row back
// to its factory value.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) sheetPositionPush() {
	c.mu.Lock()
	sub := c.ui.sub
	c.mu.Unlock()
	switch sub {
	case subMatch:
		c.matchClear()
		return
	case subPick:
		c.mu.Lock()
		c.ui.pick.start = c.ui.pick.first
		c.mu.Unlock()
		return
	}
	s := c.snap()
	r := curRow(&s.ui)
	if r.def == nil {
		c.toast("Nothing to reset on this row", 1000*time.Millisecond)
		return
	}
	if err := r.def(c); err != nil {
		c.toast(err.Error(), 2000*time.Millisecond)
		return
	}
	c.setAdjTarget(r)
	c.toast(r.label+": default", 900*time.Millisecond)
}
