// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
)

// The match editor is a full-screen keyboard for the serial trigger's match,
// opened from the BUS tab's Match bytes row (F1 Edit bytes, or Adjust push).
// Adjust moves the key highlight, Position the insertion cursor; F1 changes
// the key set, F2 (or the Adjust push) types the highlighted key, F3 deletes,
// F4 applies, F5 cancels; the Position push clears the pattern. S/DIV and
// Menu On/Off leave it as they leave any sheet sub-mode, discarding the edit.
// Its tokens are the web's "trigger on match" syntax: hex units, "??" any
// unit, "A?" nibble wildcards and ERR for a decoder error.

// Key sets.
const (
	kbHex = iota
	kbText
	kbSpecial
	kbSets
)

// kbMaxTokens is the longest pattern (the sequence trigger's limit).
const kbMaxTokens = 32

// kbSetNames label the key sets.
var kbSetNames = [kbSets]string{"Hex", "Text", "Special"}

// Special keys that are actions rather than tokens.
const (
	kbKeyLeft  = "<"
	kbKeyRight = ">"
	kbKeyClear = "CLR"
)

// kbGrid is a key set's keys, row by row.
// TRLC-LINKS: REQ-SDS-013
func kbGrid(set int) [][]string {
	switch set {
	case kbText:
		var rows [][]string
		for r := 0; r < 6; r++ {
			var row []string
			for k := 0; k < 16; k++ {
				if c := 0x20 + r*16 + k; c < 0x7f {
					row = append(row, string(rune(c)))
				}
			}
			rows = append(rows, row)
		}
		return rows
	case kbSpecial:
		return [][]string{
			{"??", "ERR", "CR", "LF"},
			{"NUL", kbKeyLeft, kbKeyRight, kbKeyClear},
		}
	}
	return [][]string{
		{"0", "1", "2", "3", "4", "5"},
		{"6", "7", "8", "9", "A", "B"},
		{"C", "D", "E", "F", "?", "??"},
	}
}

// matchEditor is the editor's state (guarded by Controller.mu).
type matchEditor struct {
	open     bool
	set      int
	row, col int
	toks     []string // pattern tokens, web syntax
	cur      int      // insertion point, 0..len(toks)
	typing   bool     // the token left of cur is a hex token still being typed
	msg      string
}

// MatchEditorView is the render snapshot of the editor.
// TRLC-LINKS: REQ-SDS-013
type MatchEditorView struct {
	Open     bool
	Set      string
	Keys     [][]string
	Row, Col int
	Toks     []string
	Cur      int
	Typing   bool
	Proto    string
	Msg      string
}

// tokensOf renders a trigger's match as editor tokens.
// TRLC-LINKS: REQ-SDS-013
func tokensOf(p engine.SerialParams) []string {
	var out []string
	if len(p.Pattern) == 0 {
		for _, b := range p.Bytes {
			out = append(out, fmt.Sprintf("%02X", b&0xff))
		}
		return out
	}
	for _, e := range p.Pattern {
		switch {
		case e.Kind == 4:
			out = append(out, "ERR")
		case e.Mask&0xff == 0:
			out = append(out, "??")
		case e.Mask&0xff == 0xf0:
			out = append(out, fmt.Sprintf("%X?", e.Value>>4&0xf))
		case e.Mask&0xff == 0x0f:
			out = append(out, fmt.Sprintf("?%X", e.Value&0xf))
		default:
			out = append(out, fmt.Sprintf("%02X", e.Value&0xff))
		}
	}
	return out
}

// compileTokens turns editor tokens into the trigger's match, as the web's
// stParsePattern does: plain hex stays a byte list; any wildcard or error
// makes a token pattern.
// TRLC-LINKS: REQ-SDS-013
func compileTokens(toks []string) (bytes []int, pattern []engine.PatternElem, err error) {
	plain := true
	for _, t := range toks {
		if t == "ERR" {
			pattern = append(pattern, engine.PatternElem{Kind: 4})
			plain = false
			continue
		}
		if len(t) < 1 || len(t) > 2 {
			return nil, nil, fmt.Errorf("bad token %q", t)
		}
		v, perr := strconv.ParseUint(strings.ReplaceAll(t, "?", "0"), 16, 8)
		if perr != nil {
			return nil, nil, fmt.Errorf("bad token %q", t)
		}
		mask := 0xff
		switch {
		case t == "??" || t == "?":
			mask = 0
		case len(t) == 2 && t[1] == '?':
			mask = 0xf0
		case len(t) == 2 && t[0] == '?':
			mask = 0x0f
		}
		if mask != 0xff {
			plain = false
		}
		pattern = append(pattern, engine.PatternElem{Kind: 2, Value: int(v), Mask: mask})
	}
	if plain {
		for _, e := range pattern {
			bytes = append(bytes, e.Value)
		}
		return bytes, nil, nil
	}
	return nil, pattern, nil
}

// setMatchTokens sets the serial trigger's match (nil = any byte), keeping
// the bus and whether it is armed.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-214
func (c *Controller) setMatchTokens(toks []string) error {
	se := c.serialTrig()
	if se == nil {
		return fmt.Errorf("no serial trigger in this engine")
	}
	if len(toks) > kbMaxTokens {
		return fmt.Errorf("%d units: %d at most", len(toks), kbMaxTokens)
	}
	b, pat, err := compileTokens(toks)
	if err != nil {
		return err
	}
	p := se.SerialConfig()
	p.Bytes, p.Pattern = b, pat
	se.SetSerialParams(c.serialBus(p))
	return nil
}

// openMatchSub opens the editor on the armed match (the sheet's sub-mode).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-212
func (c *Controller) openMatchSub() {
	var toks []string
	if se := c.serialTrig(); se != nil {
		toks = tokensOf(se.SerialConfig())
	}
	c.mu.Lock()
	c.ui.edit = false
	c.kb = matchEditor{open: true, toks: toks, cur: len(toks)}
	c.ui.sub = subMatch
	c.mu.Unlock()
}

// MatchEditor is the editor's render snapshot.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) MatchEditor() MatchEditorView {
	c.mu.Lock()
	k := c.kb
	k.open = k.open && c.ui.open && c.ui.sub == subMatch
	k.toks = append([]string(nil), k.toks...)
	c.mu.Unlock()
	if !k.open {
		return MatchEditorView{}
	}
	proto := ""
	if se := c.serialTrig(); se != nil {
		proto = [...]string{"UART", "I2C", "SPI"}[(se.SerialConfig().Proto+2)%3]
	}
	return MatchEditorView{Open: true, Set: kbSetNames[k.set], Keys: kbGrid(k.set), Row: k.row, Col: k.col,
		Toks: k.toks, Cur: k.cur, Typing: k.typing, Proto: proto, Msg: k.msg}
}

// matchKeys are the editor's F-keys: key set, type, delete, apply, cancel.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchKeys(s *sv) []fkey {
	c.mu.Lock()
	k := c.kb
	c.mu.Unlock()
	key := "-"
	if g := kbGrid(k.set); k.row < len(g) && k.col < len(g[k.row]) {
		key = g[k.row][k.col]
	}
	if key == " " {
		key = "space"
	}
	return []fkey{
		{label: "Keys", value: kbSetNames[k.set], run: func() error { c.matchCycleSet(); return nil }},
		{label: "Type", value: key, run: func() error { c.matchTypeKey(); return nil }},
		{label: "Delete", value: "<", run: func() error { c.matchDelete(); return nil }},
		{label: "OK", value: "use these bytes", run: c.matchOK},
		{label: "Cancel", value: "keep the match", run: func() error { c.matchCancel(); return nil }},
	}
}

// matchMoveKey moves the key highlight n keys through the grid, row by row.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchMoveKey(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := &c.kb
	g := kbGrid(k.set)
	total, idx := 0, 0
	for r, row := range g {
		if r == k.row {
			idx = total + min(k.col, len(row)-1)
		}
		total += len(row)
	}
	idx = ((idx+n)%total + total) % total
	for r, row := range g {
		if idx < len(row) {
			k.row, k.col = r, idx
			return
		}
		idx -= len(row)
	}
}

// matchMoveCursor moves the insertion cursor.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchMoveCursor(dir int) {
	c.mu.Lock()
	c.kb.cur = max(0, min(len(c.kb.toks), c.kb.cur+dir))
	c.kb.typing = false
	c.mu.Unlock()
}

// matchCycleSet steps the key set (Hex, Text, Special).
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchCycleSet() {
	c.mu.Lock()
	c.kb.set = (c.kb.set + 1) % kbSets
	c.kb.row, c.kb.col, c.kb.typing, c.kb.msg = 0, 0, false, ""
	c.mu.Unlock()
}

// matchTypeKey types the highlighted key at the cursor.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchTypeKey() {
	c.mu.Lock()
	k := &c.kb
	k.msg = ""
	if g := kbGrid(k.set); k.row < len(g) && k.col < len(g[k.row]) {
		k.press(g[k.row][k.col])
	}
	c.mu.Unlock()
}

// matchDelete deletes the token before the cursor.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchDelete() {
	c.mu.Lock()
	k := &c.kb
	if k.cur > 0 {
		k.toks = append(k.toks[:k.cur-1], k.toks[k.cur:]...)
		k.cur--
	}
	k.typing, k.msg = false, ""
	c.mu.Unlock()
}

// matchClear empties the pattern (the factory value: any byte).
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchClear() {
	c.mu.Lock()
	c.kb.toks, c.kb.cur, c.kb.typing, c.kb.msg = nil, 0, false, ""
	c.mu.Unlock()
}

// matchOK applies the pattern and returns to the sheet.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchOK() error {
	c.mu.Lock()
	toks := append([]string(nil), c.kb.toks...)
	c.mu.Unlock()
	if err := c.setMatchTokens(toks); err != nil {
		c.mu.Lock()
		c.kb.msg = err.Error() // the editor stays open with the reason
		c.mu.Unlock()
		return err
	}
	c.matchCancel()
	text := strings.Join(toks, " ")
	if text == "" {
		text = "any byte"
	}
	c.toast("Match: "+text, 1500*time.Millisecond)
	return nil
}

// matchCancel leaves the editor without applying.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchCancel() {
	c.mu.Lock()
	c.kb.open = false
	if c.ui.sub == subMatch {
		c.ui.sub = subNone
	}
	c.mu.Unlock()
}

// press types one key at the cursor.
// TRLC-LINKS: REQ-SDS-013
func (k *matchEditor) press(key string) {
	insert := func(t string) bool {
		if len(k.toks) >= kbMaxTokens {
			k.msg = fmt.Sprintf("%d units max", kbMaxTokens)
			return false
		}
		k.toks = append(k.toks[:k.cur], append([]string{t}, k.toks[k.cur:]...)...)
		k.cur++
		return true
	}
	switch k.set {
	case kbText:
		insert(fmt.Sprintf("%02X", key[0]))
		k.typing = false
		return
	case kbSpecial:
		switch key {
		case kbKeyLeft:
			k.cur = max(0, k.cur-1)
		case kbKeyRight:
			k.cur = min(len(k.toks), k.cur+1)
		case kbKeyClear:
			k.toks, k.cur = nil, 0
		case "CR":
			insert("0D")
		case "LF":
			insert("0A")
		case "NUL":
			insert("00")
		default: // ?? and ERR are tokens as they are
			insert(key)
		}
		k.typing = false
		return
	}
	// Hex: two digits make a unit; a ? digit is a nibble wildcard.
	if key == "??" {
		insert("??")
		k.typing = false
		return
	}
	if k.typing && k.cur > 0 && len(k.toks[k.cur-1]) == 1 {
		k.toks[k.cur-1] += key
		k.typing = false
		return
	}
	k.typing = insert(key)
}

// ---- the byte picker ----

// pickFrom builds the picker's bytes from packets, starting at packet start.
// TRLC-LINKS: REQ-SDS-214
func pickFrom(rows [][]string, starts []bool, startPkt int) pickState {
	var p pickState
	for i, toks := range rows {
		if i == startPkt {
			p.start = len(p.toks)
		}
		for j, t := range toks {
			if len(p.toks) < 256 {
				p.toks = append(p.toks, wsview.PickTok{Text: t, Start: j == 0 && (starts[i] || i > 0)})
			}
		}
	}
	p.start = clampInt(p.start, 0, max(0, len(p.toks)-1))
	p.first = p.start
	return p
}

// pickKeys are the picker's F-keys: trigger on 1..4 bytes, cancel.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) pickKeys(s *sv) []fkey {
	p := s.ui.pick
	var out []fkey
	for n := 1; n <= 4; n++ {
		n := n
		ok := p.start+n <= len(p.toks)
		var toks []string
		for i := p.start; i < p.start+n && i < len(p.toks); i++ {
			toks = append(toks, p.toks[i].Text)
		}
		v := "-"
		if ok {
			v = strings.Join(toks, " ")
		}
		out = append(out, fkey{label: fmt.Sprintf("Trig on %d", n), value: v, dim: !ok, run: func() error { return c.pickUse(n) }})
	}
	out = append(out, fkey{label: "Cancel", value: "keep the match", run: func() error { c.pickCancel(); return nil }})
	return out
}

// pickMove moves the first byte.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) pickMove(dir int) {
	c.mu.Lock()
	c.ui.pick.start = clampInt(c.ui.pick.start+dir, 0, max(0, len(c.ui.pick.toks)-1))
	c.mu.Unlock()
}

// pickCancel closes the picker without a change.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) pickCancel() {
	c.mu.Lock()
	if c.ui.wpick {
		c.closePickLocked()
	} else if c.ui.sub == subPick {
		c.ui.sub, c.ui.pick = subNone, pickState{}
	}
	c.mu.Unlock()
}

// pickUse triggers on n bytes from the highlighted one: it sets the match
// and switches the trigger to Serial.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) pickUse(n int) error {
	c.mu.Lock()
	p := c.ui.pick
	c.mu.Unlock()
	if p.start+n > len(p.toks) {
		return fmt.Errorf("not enough bytes after the highlighted one")
	}
	var toks []string
	for _, t := range p.toks[p.start : p.start+n] {
		toks = append(toks, t.Text)
	}
	if err := c.setMatchTokens(toks); err != nil {
		return err
	}
	c.pickCancel()
	if err := c.armSerial(); err != nil {
		return err
	}
	c.toast("Trigger on "+strings.Join(toks, " "), 1600*time.Millisecond)
	return nil
}
