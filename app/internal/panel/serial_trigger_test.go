// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"reflect"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// serialEng is the fake engine plus a serial trigger.
type serialEng struct {
	*fakeEng
	p engine.SerialParams
}

func (e *serialEng) SetSerialMode(m int) {
	e.statMu.Lock()
	e.stats.SerialMode = m
	e.statMu.Unlock()
}
func (e *serialEng) SetSerialParams(p engine.SerialParams) { e.p = p }
func (e *serialEng) SetTrigType(t int) {
	e.statMu.Lock()
	e.stats.TrigType = t
	e.statMu.Unlock()
}
func (e *serialEng) SerialConfig() engine.SerialParams {
	p := e.p
	p.Bytes = append([]int(nil), p.Bytes...)
	return p
}

// The serial trigger sits in the TRIGGER menu as a fifth Type, decodes with
// the DECODE page's bus, and the SERIAL page edits its match (it was only
// reachable from the web).
// TRLC-LINKS: REQ-SDS-013
func TestSerialTriggerInTriggerMenu(t *testing.T) {
	_, base, fe := newC(t)
	eng := &serialEng{fakeEng: base}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	eng.stats.TrigType = 3 // Video: the next Type is Serial
	c.openMenu(pgTrig)
	c.pressWrap = true
	c.menuCycle(3, 1)
	if eng.stats.SerialMode == 0 || eng.stats.TrigType != 0 {
		t.Fatalf("Serial not armed: mode %d type %d", eng.stats.SerialMode, eng.stats.TrigType)
	}
	if eng.p.Proto != 1 || len(eng.p.Bytes) != 1 || eng.p.Bytes[0] != 0x55 || eng.p.Addr != -1 || eng.p.RW != 2 {
		t.Fatalf("armed params %+v", eng.p)
	}
	if v := c.MenuView(); v.Items[3].Value != "Serial" {
		t.Fatalf("Type shows %q", v.Items[3].Value)
	}
	c.openMenu(pgTrigQ)
	v := c.MenuView()
	if v.Title != "SERIAL" || v.Items[0].Value != "UART" || v.Items[1].Value != "55" {
		t.Fatalf("SERIAL page %+v", v)
	}
	c.menuCycle(2, 1) // Clear: any data
	if len(eng.p.Bytes) != 0 || c.MenuView().Items[1].Value != "any" {
		t.Fatalf("Clear left %+v", eng.p)
	}
	c.menuCycle(0, 1) // UART -> I2C, shared with DECODE
	c.menuCycle(3, 1) // address any -> 00
	if eng.p.Proto != 2 || eng.p.Addr != 0 || c.decProto != 3 || c.decChB == c.decChA {
		t.Fatalf("I2C: params %+v decProto %d roles %d/%d", eng.p, c.decProto, c.decChA, c.decChB)
	}
	c.mu.Lock()
	n := c.pageSlots(pgTrigQ)
	c.mu.Unlock()
	if n != 4 {
		t.Fatalf("I2C SERIAL page has %d slots, want 4 (Addr)", n)
	}
	c.menuCycle(0, 1) // I2C -> SPI
	c.mu.Lock()
	c.decBaud = 9600
	c.mu.Unlock()
	c.openMenu(pgDecode)
	c.menuCycle(2, 1) // any DECODE change re-sends the bus to the armed trigger
	c.mu.Lock()
	chA, chB := c.decChA, c.decChB
	c.mu.Unlock()
	if eng.p.Proto != 3 || eng.p.Baud != 9600 || eng.p.ChA != chA || eng.p.ChB != chB {
		t.Fatalf("bus not shared: %+v (decode roles %d/%d)", eng.p, chA, chB)
	}
	if eng.p.Addr != -1 {
		t.Fatalf("SPI kept an I2C address: %+v", eng.p)
	}
	c.openMenu(pgTrig)
	c.menuCycle(3, 1) // Serial -> Edge disarms
	if eng.stats.SerialMode != 0 || eng.stats.TrigType != 0 {
		t.Fatalf("Serial -> Edge: mode %d type %d", eng.stats.SerialMode, eng.stats.TrigType)
	}
}

// The match editor: hex digits pair into bytes, "?" makes a nibble wildcard,
// the text keys insert characters, the special keys insert ?? and ERR, and
// OK arms exactly what the web's syntax would (bug-hunt request: a full-
// screen keyboard driven by TIME/DIV and CH2 POSITION).
// TRLC-LINKS: REQ-SDS-013
func TestMatchEditor(t *testing.T) {
	_, base, fe := newC(t)
	eng := &serialEng{fakeEng: base}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	eng.stats.TrigType = 3
	c.openMenu(pgTrig)
	c.pressWrap = true
	c.menuCycle(3, 1) // arm Serial (match 55)
	c.openMenu(pgTrigQ)
	c.menuCycle(1, 1) // F2: open the editor
	m := c.MatchEditor()
	if !m.Open || len(m.Toks) != 1 || m.Toks[0] != "55" || m.Cur != 1 || c.MenuView().Title != "MATCH" {
		t.Fatalf("editor opened as %+v", m)
	}
	key := func(row, col int) {
		c.mu.Lock()
		c.kb.row, c.kb.col = row, col
		c.mu.Unlock()
		c.menuCycle(1, 1) // F2 enter
	}
	key(1, 4) // A
	key(0, 0) // 0 -> "A0"
	key(1, 4) // A
	key(2, 4) // ? -> "A?"
	key(2, 5) // ??
	// Knobs: TIME/DIV moves the highlight right, CH2 POSITION up; neither
	// touches the timebase while the editor is open.
	tdiv := c.tdivIdx
	c.dispatch("tdiv", 1, 1)
	c.dispatch("ch2pos", 1, 1)
	if c.tdivIdx != tdiv {
		t.Fatal("TIME/DIV changed the timebase under the editor")
	}
	if m := c.MatchEditor(); m.Row != 1 || m.Col != 0 {
		t.Fatalf("knob highlight row %d col %d, want 1/0", m.Row, m.Col)
	}
	c.menuCycle(0, 1) // Keys: Text
	key(2, 8)         // 'H' (0x48)
	c.menuCycle(0, 1) // Keys: Special
	key(0, 1)         // ERR
	c.dispatch("adjust", -1, 1)
	c.menuCycle(2, 1) // Del before the cursor: drops 'H'
	if m := c.MatchEditor(); strings.Join(m.Toks, " ") != "55 A0 A? ?? ERR" || m.Cur != 4 {
		t.Fatalf("tokens %v cursor %d", m.Toks, m.Cur)
	}
	c.menuCycle(3, 1) // OK
	want := []engine.PatternElem{{Kind: 2, Value: 0x55, Mask: 0xff}, {Kind: 2, Value: 0xa0, Mask: 0xff},
		{Kind: 2, Value: 0xa0, Mask: 0xf0}, {Kind: 2, Mask: 0}, {Kind: 4}}
	if !reflect.DeepEqual(eng.p.Pattern, want) || len(eng.p.Bytes) != 0 {
		t.Fatalf("armed %+v, want %+v", eng.p, want)
	}
	if c.MatchEditor().Open || c.MenuView().Title != "SERIAL" {
		t.Fatal("OK did not close the editor back to SERIAL")
	}
	c.menuCycle(1, 1)  // reopen: the armed pattern loads back
	c.menuCycle(2, 1)  // Del
	c.openMenu(pgNone) // leaving the page cancels
	if c.MatchEditor().Open || len(eng.p.Pattern) != 5 {
		t.Fatalf("leaving the page applied or kept the editor: %+v", eng.p)
	}
	if b, p, _ := compileTokens([]string{"48", "69", "5"}); len(p) != 0 || !reflect.DeepEqual(b, []int{0x48, 0x69, 0x05}) {
		t.Fatalf("plain hex compiled to %v %v", b, p)
	}
}
