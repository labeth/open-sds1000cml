// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"reflect"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
)

// The serial trigger is the TRIG tab's fifth Type; it decodes with the BUS
// tab's bus (one bus, set once, any of the decoders), the BUS tab's Match
// bytes row and the pickers set its match, and Serial refuses without a bus.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-212
func TestSerialTriggerType(t *testing.T) {
	c, eng, _ := newC(t)
	if res := c.SheetSet("trig.type", nil, "Serial"); res.OK || !strings.Contains(res.Error, "decode is off") || eng.stats.SerialMode != 0 {
		t.Fatalf("Serial with decode off: %+v", res)
	}
	c.SheetSet("bus.protocol", nil, "UART")
	if res := c.SheetSet("trig.type", nil, "Serial"); !res.OK || eng.stats.SerialMode == 0 || eng.stats.TrigType != 0 {
		t.Fatalf("Serial not armed: %+v mode %d type %d", res, eng.stats.SerialMode, eng.stats.TrigType)
	}
	if p := eng.SerialConfig(); p.Proto != 1 || len(p.Bytes) != 1 || p.Bytes[0] != 0x55 || p.Addr != -1 || p.RW != 2 {
		t.Fatalf("armed params %+v", p)
	}
	if rowText(c, "trig.type") != "Serial" || rowText(c, "bus.match") != "55" || rowText(c, "bus.trigger_on") != "On" {
		t.Fatalf("rows: type %q match %q on %q", rowText(c, "trig.type"), rowText(c, "bus.match"), rowText(c, "bus.trigger_on"))
	}
	if rowByID["trig.edge"].relevant(c.snap()) {
		t.Fatal("Source/slope in use while Serial uses the BUS lines")
	}
	c.SheetSet("bus.match", nil, "48 69")
	if p := eng.SerialConfig(); !reflect.DeepEqual(p.Bytes, []int{0x48, 0x69}) {
		t.Fatalf("match %+v", p)
	}
	c.SheetSet("bus.match", nil, "any byte")
	if p := eng.SerialConfig(); len(p.Bytes) != 0 || rowText(c, "bus.match") != "any byte" {
		t.Fatalf("cleared match %+v", p)
	}
	c.SheetSet("bus.protocol", nil, "I2C") // the bus is shared: the trigger follows
	c.SheetSet("bus.i2c_address", nil, "0x50")
	if p := eng.SerialConfig(); p.Proto != 2 || p.Addr != 0x50 || c.decChA == c.decChB {
		t.Fatalf("I2C: %+v roles %d/%d", p, c.decChA, c.decChB)
	}
	c.SheetSet("bus.protocol", nil, "SPI") // the I2C address is kept for when the bus is I2C again
	if p := eng.SerialConfig(); p.Proto != 3 || p.Addr != 0x50 || p.ChA != c.decChA || p.ChB != c.decChB {
		t.Fatalf("SPI: %+v (decode roles %d/%d)", p, c.decChA, c.decChB)
	}
	if r := rowModel(c.snap(), rowByID["bus.i2c_address"]); r.Text != "0x50" || r.Available {
		t.Fatalf("the I2C address on SPI: %+v", r)
	}
	// Every bus has a matcher (the packet and line FPGA images): the trigger
	// follows a Manchester bus, decoded as BUS decodes it (IEEE, MSB first),
	// and a CAN bus at its bit rate.
	c.SheetSet("bus.protocol", nil, "Manch")
	if p := eng.SerialConfig(); eng.stats.SerialMode == 0 || p.Proto != 4 || !p.MSB || !p.IEEE || p.HaveThr {
		t.Fatalf("Manchester: mode %d %+v", eng.stats.SerialMode, p)
	}
	c.SheetSet("bus.protocol", nil, "CAN")
	c.mu.Lock()
	c.decBaud = 500000
	c.mu.Unlock()
	c.syncSerialBus()
	if p := eng.SerialConfig(); eng.stats.SerialMode == 0 || p.Proto != 6 || p.Baud != 500000 || !p.MSB {
		t.Fatalf("CAN: mode %d %+v", eng.stats.SerialMode, p)
	}
	c.SheetSet("bus.protocol", nil, "Off") // no bus: disarmed
	if eng.stats.SerialMode != 0 {
		t.Fatal("decode off left the serial trigger armed")
	}
	c.SheetSet("bus.protocol", nil, "UART")
	c.SheetSet("trig.type", nil, "Serial")
	c.SheetSet("trig.type", nil, "Edge")
	if eng.stats.SerialMode != 0 || eng.stats.TrigType != 0 {
		t.Fatalf("Serial -> Edge: mode %d type %d", eng.stats.SerialMode, eng.stats.TrigType)
	}
}

// Auto locks the bus it found before arming, and refuses a bus the serial
// trigger cannot match.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-214
func TestSerialTriggerLocksAuto(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	r.show(uartFrame([]string{"Hi\r\n"}, 115200, 200e-6, 200e-9, 20000))
	c.SheetSet("bus.protocol", nil, "Auto")
	if res := c.SheetSet("trig.type", nil, "Serial"); !res.OK || c.decProto != framecalc.ProtoUART || r.eng.stats.SerialMode == 0 {
		t.Fatalf("Auto + Serial: %+v proto %d", res, c.decProto)
	}
	if !strings.Contains(c.View().Notice, "Decode fixed to UART") {
		t.Fatalf("notice %q", c.View().Notice)
	}
}

// An engine without the serial trigger has no Serial type.
// TRLC-LINKS: REQ-SDS-013
func TestSerialTriggerAbsent(t *testing.T) {
	_, base, fe := newC(t)
	c := New(plainEng{base}, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.SheetSet("bus.protocol", nil, "UART")
	if res := c.SheetSet("trig.type", nil, "Serial"); res.OK || !strings.Contains(res.Error, "no serial trigger") {
		t.Fatalf("Serial without a serial trigger: %+v", res)
	}
	if rowText(c, "bus.match") != "any byte" {
		t.Fatal("match row")
	}
}

// The match editor: hex digits pair into bytes, ? makes a nibble wildcard,
// the text keys insert characters, the special keys insert ?? and ERR, and
// OK arms exactly what the web's syntax would. Adjust moves the key,
// Position the cursor; S/DIV and Menu On/Off discard (a full-screen keyboard
// over the BUS tab).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-214
func TestMatchEditor(t *testing.T) {
	c, eng, _ := newC(t)
	c.SheetSet("bus.protocol", nil, "UART")
	c.SheetSet("trig.type", nil, "Serial") // match 55
	press(t, c, "utility")
	goRow(t, c, "bus.match")
	pickKey(t, c, "Edit bytes")
	m := c.MatchEditor()
	if !m.Open || len(m.Toks) != 1 || m.Toks[0] != "55" || m.Cur != 1 || c.PanelStatus().Mode != "editor" {
		t.Fatalf("editor opened as %+v", m)
	}
	key := func(row, col int) { // highlight a key with Adjust and type it
		t.Helper()
		g := kbGrid(c.kb.set)
		want := 0
		for r := 0; r < row; r++ {
			want += len(g[r])
		}
		want += col
		cur := 0
		for r := 0; r < c.kb.row; r++ {
			cur += len(g[r])
		}
		cur += c.kb.col
		turn(c, "adjust", want-cur)
		if c.kb.row != row || c.kb.col != col {
			t.Fatalf("Adjust reached %d/%d, want %d/%d", c.kb.row, c.kb.col, row, col)
		}
		press(t, c, "adjustpush")
	}
	key(1, 4) // A
	key(0, 0) // 0 -> "A0"
	key(1, 4) // A
	key(2, 4) // ? -> "A?"
	key(2, 5) // ??
	tdiv := eng.Snapshot().TdivS
	turn(c, "horizpos", -1) // the cursor back one
	turn(c, "horizpos", 1)
	if eng.Snapshot().TdivS != tdiv || c.MatchEditor().Cur != 4 {
		t.Fatalf("Position moved the timebase or lost the cursor: %d", c.MatchEditor().Cur)
	}
	pickKey(t, c, "Keys") // Text
	key(2, 8)             // 'H' (0x48)
	pickKey(t, c, "Keys") // Special
	key(0, 1)             // ERR
	turn(c, "horizpos", -1)
	pickKey(t, c, "Delete") // drops 'H' before the cursor
	if m := c.MatchEditor(); strings.Join(m.Toks, " ") != "55 A0 A? ?? ERR" || m.Cur != 4 {
		t.Fatalf("tokens %v cursor %d", m.Toks, m.Cur)
	}
	pickKey(t, c, "OK")
	want := []engine.PatternElem{{Kind: 2, Value: 0x55, Mask: 0xff}, {Kind: 2, Value: 0xa0, Mask: 0xff},
		{Kind: 2, Value: 0xa0, Mask: 0xf0}, {Kind: 2, Mask: 0}, {Kind: 4}}
	if p := eng.SerialConfig(); !reflect.DeepEqual(p.Pattern, want) || len(p.Bytes) != 0 {
		t.Fatalf("armed %+v, want %+v", p, want)
	}
	if st := c.PanelStatus(); c.MatchEditor().Open || st.Mode != "sheet" || st.Sheet.Row.ID != "bus.match" {
		t.Fatal("OK did not return to the Match bytes row")
	}
	pickKey(t, c, "Edit bytes") // reopen: the armed pattern loads back
	pickKey(t, c, "Delete")
	turn(c, "tdiv", 1) // S/DIV: another tab, the edit discarded
	if c.MatchEditor().Open || len(eng.SerialConfig().Pattern) != 5 {
		t.Fatalf("leaving the tab applied or kept the editor: %+v", eng.SerialConfig())
	}
	if b, p, _ := compileTokens([]string{"48", "69", "5"}); len(p) != 0 || !reflect.DeepEqual(b, []int{0x48, 0x69, 0x05}) {
		t.Fatalf("plain hex compiled to %v %v", b, p)
	}
}

// The BUS workspace's picker: the decoded bytes of the listed packets, the
// selected packet's first byte highlighted, F1..F4 trigger on 1..4 bytes
// from it (setting the match and the type Serial); the sheet's From screen
// picks the same way.
// TRLC-LINKS: REQ-SDS-214
func TestBytePickers(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	uart := func() { r.show(uartFrame([]string{"Hi!\r\n", "OK\r\n"}, 460800, 100e-6, 100e-9, 20000)) }
	uart()
	press(t, c, "f2")
	uart()
	turn(c, "adjust", 1) // the second packet: "OK\r\n"
	press(t, c, "adjustpush")
	st := c.PanelStatus()
	if st.Mode != "picker" || st.Picker.Tokens[st.Picker.Cursor] != "4F" || st.FKeys[1].Value != "4F 4B" {
		t.Fatalf("picker at %d of %v, keys %+v", st.Picker.Cursor, st.Picker.Tokens, st.FKeys)
	}
	turn(c, "horizpos", 1) // the first byte one on: 4B
	pickKey(t, c, "Trig on 3")
	if p := r.eng.SerialConfig(); !reflect.DeepEqual(p.Bytes, []int{0x4B, 0x0D, 0x0A}) || r.eng.stats.SerialMode == 0 {
		t.Fatalf("picked %+v mode %d", p, r.eng.stats.SerialMode)
	}
	if st := c.PanelStatus(); st.Mode != "workspace" || !strings.HasPrefix(c.View().Bus.Right, "TRIG ON 4B 0D 0A") {
		t.Fatalf("after the pick: %s %q", st.Mode, c.View().Bus.Right)
	}
	press(t, c, "adjustpush", "f5") // Cancel keeps the match
	if p := r.eng.SerialConfig(); len(p.Bytes) != 3 {
		t.Fatalf("cancel changed the match: %+v", p)
	}
	press(t, c, "utility")
	goRow(t, c, "bus.match")
	pickKey(t, c, "From screen")
	if st := c.PanelStatus(); st.Mode != "picker" || st.Picker.Tokens[st.Picker.Cursor] != "48" {
		t.Fatalf("sheet picker %+v", st.Picker)
	}
	pickKey(t, c, "Trig on 2")
	if p := r.eng.SerialConfig(); !reflect.DeepEqual(p.Bytes, []int{0x48, 0x69}) {
		t.Fatalf("sheet pick %+v", p)
	}
	if st := c.PanelStatus(); st.Mode != "sheet" || st.Sheet.Row.ID != "bus.match" {
		t.Fatalf("after the sheet pick: %s", st.Mode)
	}
}

// OK's notice names the match the trigger now holds: a lone typed nibble is
// the byte 0A, so "55 0A", not "55 A".
// TRLC-LINKS: REQ-SDS-013
func TestMatchEditorOKNamesTheArmedMatch(t *testing.T) {
	c, _, _ := newC(t)
	c.SheetSet("bus.protocol", nil, "UART")
	c.SheetSet("trig.type", nil, "Serial") // match 55
	press(t, c, "utility")
	goRow(t, c, "bus.match")
	pickKey(t, c, "Edit bytes")
	c.mu.Lock()
	c.kb.toks, c.kb.cur = []string{"55", "A"}, 2
	c.mu.Unlock()
	pickKey(t, c, "OK")
	if n := c.View().Notice; n != "Match: 55 0A" {
		t.Fatalf("OK's notice %q", n)
	}
}
