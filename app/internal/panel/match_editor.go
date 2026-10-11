// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"strconv"
	"strings"

	"open-sds/app/internal/engine"
)

// The match editor is a full-screen keyboard for the serial trigger's match
// (SERIAL page, F2). TIME/DIV moves the key highlight left/right, CH2
// POSITION up/down, ADJUST the insertion cursor in the pattern; F2 types the
// highlighted key. Its tokens are the web's "trigger on match" syntax: hex
// units, "??" any unit, "A?" nibble wildcards and ERR for a decoder error.

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

// openMatchEditor loads the armed match into the editor.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) openMatchEditor() {
	se := c.serialTrig()
	if se == nil {
		return
	}
	toks := tokensOf(se.SerialConfig())
	c.mu.Lock()
	c.kb = matchEditor{open: true, toks: toks, cur: len(toks)}
	c.mu.Unlock()
	c.openMenu(pgMatch)
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

// MatchEditor is the editor's render snapshot.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) MatchEditor() MatchEditorView {
	c.mu.Lock()
	k := c.kb
	k.open = k.open && c.menuPage == pgMatch
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

// matchEditorOpen reports the editor is up (its knobs are captured).
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchEditorOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.kb.open && c.menuPage == pgMatch // leaving the page cancels it
}

// matchEditorKnob moves the key highlight (TIME/DIV: column, CH2 POSITION:
// row) or the insertion cursor (ADJUST). It reports whether it took the knob.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchEditorKnob(name string, dir int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := &c.kb
	if !k.open || c.menuPage != pgMatch {
		return false
	}
	grid := kbGrid(k.set)
	switch name {
	case "tdiv":
		n := len(grid[k.row])
		k.col = ((k.col+dir)%n + n) % n
	case "ch2pos":
		// Position knobs turn clockwise to raise the trace: clockwise moves up.
		n := len(grid)
		k.row = ((k.row-dir)%n + n) % n
		k.col = min(k.col, len(grid[k.row])-1)
	case "adjust":
		k.cur = max(0, min(len(k.toks), k.cur+dir))
		k.typing = false
	default:
		return false
	}
	return true
}

// matchEditorCycle handles the editor's softkeys: F1 key set, F2 type the
// highlighted key, F3 delete before the cursor, F4 apply, F5 cancel.
// TRLC-LINKS: REQ-SDS-013
func (c *Controller) matchEditorCycle(slot, dir int) {
	c.mu.Lock()
	k := &c.kb
	if !k.open || c.menuPage != pgMatch {
		c.mu.Unlock()
		return
	}
	k.msg = ""
	switch slot {
	case 0:
		k.set = ((k.set+dir)%kbSets + kbSets) % kbSets
		k.row, k.col, k.typing = 0, 0, false
	case 1:
		k.press(kbGrid(k.set)[k.row][k.col])
	case 2:
		if k.cur > 0 {
			k.toks = append(k.toks[:k.cur-1], k.toks[k.cur:]...)
			k.cur--
		}
		k.typing = false
	case 3:
		bytes, pattern, err := compileTokens(k.toks)
		if err != nil {
			k.msg = err.Error()
			c.mu.Unlock()
			return
		}
		k.open = false
		c.mu.Unlock()
		if se := c.serialTrig(); se != nil {
			p := se.SerialConfig()
			p.Bytes, p.Pattern = bytes, pattern
			se.SetSerialParams(c.serialBus(p))
		}
		c.openMenu(pgTrigQ)
		return
	case 4:
		k.open = false
		c.mu.Unlock()
		c.openMenu(pgTrigQ)
		return
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
