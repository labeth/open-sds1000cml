// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"
)

// BUS: the list holds the complete packets still while the messages repeat;
// Adjust selects a packet, Position jumps match to match and its push goes
// back to the trigger, the S/DIV push zooms onto the selected packet (a
// running scope stops so it holds still) and again zooms off.
// TRLC-LINKS: REQ-SDS-214
func TestBusPacketTools(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	uart := func() { r.show(uartFrame([]string{"AB\r\n", "Hi!\r\n", "OK\r\n"}, 460800, 100e-6, 100e-9, 20000)) }
	uart()
	press(t, c, "f2")
	uart()
	v := c.View()
	if len(v.Bus.Lines) < 6 || v.Bus.Lines[0].Str != "AB.." || v.Bus.Lines[0].Hex[0] != "41" || !v.Bus.Lines[0].Sel {
		t.Fatalf("packet list %+v", v.Bus.Lines)
	}
	if d, _ := c.Snapshot(); d.DecFormat != 0 {
		t.Fatalf("the strip is not hex by default: %d", d.DecFormat)
	}
	rows := len(c.ana.list.Rows())
	uart() // the same messages again: the list holds still
	if len(c.ana.list.Rows()) != rows {
		t.Fatalf("the list moved: %d -> %d rows", rows, len(c.ana.list.Rows()))
	}
	c.SheetSet("bus.match", nil, "4F 4B") // "OK"
	turn(c, "horizpos", 1)
	if sel := c.View().Bus.SelNum; sel != 3 {
		t.Fatalf("Position: packet %d, want the first OK (3)", sel)
	}
	turn(c, "horizpos", 1)
	if sel := c.View().Bus.SelNum; sel != 6 {
		t.Fatalf("Position again: packet %d, want 6", sel)
	}
	turn(c, "adjust", -1)
	if sel := c.View().Bus.SelNum; sel != 5 {
		t.Fatalf("Adjust back: packet %d", sel)
	}
	press(t, c, "tdivpush") // zoom onto packet 5
	if c.zoom <= 1 || r.eng.Snapshot().Running || !strings.Contains(c.View().Notice, "on packet 5") {
		t.Fatalf("zoom: %d running %v notice %q", c.zoom, r.eng.Snapshot().Running, c.View().Notice)
	}
	press(t, c, "tdivpush")
	if c.zoom != 1 {
		t.Fatalf("zoom off: %d", c.zoom)
	}
	press(t, c, "horizpospush")
	if sel := c.View().Bus.SelNum; sel != 1 {
		t.Fatalf("Position push: packet %d", sel)
	}
}

// BUS again: the eye folds the data line; Adjust moves the threshold and
// Position the sample point, the Position push centres both, the Adjust
// push clears the eye.
// TRLC-LINKS: REQ-SDS-215
func TestBusEyeTools(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	uart := func() { r.show(uartFrame([]string{"Hello, eye!\r\n"}, 460800, 60e-6, 100e-9, 20000)) }
	uart()
	press(t, c, "f2", "f2")
	for i := 0; i < 4; i++ {
		uart()
	}
	e := c.View().Eye
	if e == nil || e.Why != "" || !strings.Contains(e.Figures[0][0][1], "kbit/s") || e.SampleCol != e.Cols/2 {
		t.Fatalf("eye %+v", e)
	}
	th := e.ThreshRow
	turn(c, "adjust", 10)
	turn(c, "horizpos", 8)
	e = c.View().Eye
	if e.ThreshRow >= th || e.SampleCol <= e.Cols/2 || !strings.HasPrefix(e.Cursor, "SAMPLE PT +0.12 UI") {
		t.Fatalf("cursors: row %d (was %d) col %d cursor %q", e.ThreshRow, th, e.SampleCol, e.Cursor)
	}
	press(t, c, "horizpospush")
	if e = c.View().Eye; e.ThreshRow != th || e.SampleCol != e.Cols/2 {
		t.Fatalf("Position push: row %d col %d", e.ThreshRow, e.SampleCol)
	}
	press(t, c, "adjustpush")
	if e = c.View().Eye; e.Why == "" {
		t.Fatal("Adjust push did not clear the eye")
	}
	uart()
	if e = c.View().Eye; e.Why != "" {
		t.Fatalf("the eye did not fold again: %q", e.Why)
	}
}
