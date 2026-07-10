package panel

import "testing"

// The SCPI display commands (XYDS/PESU/MENU) are backed by these accessors —
// they must reflect and drive the same state the menus/LCD use.
func TestDisplayStateAccessors(t *testing.T) {
	c, _, _ := newC(t)

	// X-Y view: SetViewXY(true) enters X-Y; leaving only returns to Y-T when
	// X-Y is current (it must never clobber an unrelated view).
	if c.ViewXY() {
		t.Fatal("boot view must be Y-T")
	}
	c.SetViewXY(true)
	if !c.ViewXY() || c.MenuView().ViewMode != 1 {
		t.Fatal("SetViewXY(true) did not enter X-Y")
	}
	c.SetViewXY(false)
	if c.ViewXY() || c.MenuView().ViewMode != 0 {
		t.Fatal("SetViewXY(false) did not return to Y-T")
	}
	c.mu.Lock()
	c.viewMode = 2 // FFT
	c.mu.Unlock()
	c.SetViewXY(false)
	if c.MenuView().ViewMode != 2 {
		t.Fatal("SetViewXY(false) clobbered the FFT view")
	}
	c.mu.Lock()
	c.viewMode = 0
	c.mu.Unlock()

	// Persistence mirrors the CHANNEL-menu toggle state.
	if c.PersistOn() {
		t.Fatal("boot persistence must be off")
	}
	c.SetPersist(true)
	if !c.PersistOn() || !c.MenuView().Persist {
		t.Fatal("SetPersist(true) not visible to the render snapshot")
	}
	c.SetPersist(false)
	if c.PersistOn() {
		t.Fatal("SetPersist(false) did not stick")
	}

	// Menu: SetMenuOpen(true) opens the MAIN page; false closes whatever is
	// open (and re-latches the LEDs so a page key's lamp drops).
	if c.MenuOpen() {
		t.Fatal("boot menu must be closed")
	}
	c.SetMenuOpen(true)
	if !c.MenuOpen() || c.MenuView().Title != "MENU" {
		t.Fatalf("SetMenuOpen(true) did not open MAIN: %q", c.MenuView().Title)
	}
	c.SetMenuOpen(true) // idempotent: must not reset an open page
	c.menuButton(btnDisplay)
	if c.MenuView().Title != "DISPLAY" {
		t.Fatal("setup: DISPLAY page not open")
	}
	c.SetMenuOpen(false)
	if c.MenuOpen() {
		t.Fatal("SetMenuOpen(false) left the menu open")
	}
}
