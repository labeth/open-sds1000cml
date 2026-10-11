// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import "testing"

// The SCPI display commands (XYDS/PESU/MENU/Cn:TRA/CRMS/PACU) are backed by
// these accessors: they must reflect and drive the state the LCD shows.
// TRLC-LINKS: REQ-SDS-136
func TestDisplayStateAccessors(t *testing.T) {
	c, eng, _ := newC(t)

	// X-Y view: SetViewXY(true) enters X-Y; leaving only returns to Y-T when
	// X-Y is current (it must never clobber an unrelated view).
	if c.ViewXY() {
		t.Fatal("boot view must be Y-T")
	}
	c.SetViewXY(true)
	if d, _ := c.Snapshot(); !c.ViewXY() || d.ViewMode != viewXY || c.PanelStatus().View != "X-Y" {
		t.Fatal("SetViewXY(true) did not enter X-Y")
	}
	c.SetViewXY(false)
	if d, _ := c.Snapshot(); c.ViewXY() || d.ViewMode != viewYT {
		t.Fatal("SetViewXY(false) did not return to Y-T")
	}
	c.SheetSet("view.view", nil, "FFT")
	c.SetViewXY(false)
	if d, _ := c.Snapshot(); d.ViewMode != viewFFT {
		t.Fatal("SetViewXY(false) clobbered the FFT view")
	}
	c.SheetSet("view.view", nil, "Y-T")

	// Persistence mirrors the VIEW tab's row.
	if c.PersistOn() {
		t.Fatal("boot persistence must be off")
	}
	c.SetPersist(true)
	if d, _ := c.Snapshot(); !c.PersistOn() || !d.Persist || rowText(c, "view.persist") != "On" {
		t.Fatal("SetPersist(true) not visible to the render snapshot")
	}
	c.SetPersist(false)
	if c.PersistOn() {
		t.Fatal("SetPersist(false) did not stick")
	}

	// MENU ON opens this workspace's sheet as Menu On/Off does, and keeps a
	// sheet that is open where it is; MENU OFF closes a sheet or the picker.
	if c.MenuOpen() {
		t.Fatal("boot menu must be closed")
	}
	press(t, c, "f3")
	c.SetMenuOpen(true)
	if st := c.PanelStatus(); !c.MenuOpen() || st.Sheet.Tab != "view" {
		t.Fatalf("SetMenuOpen(true) opened %s", st.Sheet.Tab)
	}
	press(t, c, "display", "acquire")
	c.SetMenuOpen(true) // idempotent: must not move an open sheet
	if st := c.PanelStatus(); st.Sheet.Tab != "acq" {
		t.Fatalf("SetMenuOpen(true) moved the sheet to %s", st.Sheet.Tab)
	}
	c.SetMenuOpen(false)
	if c.MenuOpen() || c.PanelStatus().Mode != "workspace" {
		t.Fatal("SetMenuOpen(false) left the menu open")
	}

	// Traces, cursors and readouts: the SCPI sets show on the LCD and lamps.
	c.SetTraceOn(1, false)
	if d, _ := c.Snapshot(); c.TraceOn(1) || d.ShowC2 || eng.lastLEDs()&ledCH2 != 0 {
		t.Fatal("SetTraceOn(1, false)")
	}
	c.SetCursorsOn(true)
	if on, _, _, _ := c.CursorState(); !on || !c.CursorsOn() {
		t.Fatal("SetCursorsOn(true)")
	}
	c.SetMeasOn(true)
	if d, _ := c.Snapshot(); !c.MeasOn() || !d.ShowMeas {
		t.Fatal("SetMeasOn(true)")
	}
}
