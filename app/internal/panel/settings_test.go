// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"testing"

	"open-sds/app/internal/settings"
)

// TRLC-LINKS: REQ-SDS-136
func TestSettingsViewRoundTrip(t *testing.T) {
	c, _, _ := newC(t)
	want := settings.ViewState{
		ViewMode: 2,
		Decode: settings.Decode{
			Proto: 3, Baud: 57600, ChA: 1, ChB: 0,
			CPOL: true, CPHA: false, Format: 1,
		},
	}
	c.ApplySettingsView(want)
	if got := c.SettingsView(); got != want {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	// The render path must see the restored state (the LCD/HUD reads MenuView).
	mv := c.MenuView()
	if mv.ViewMode != 2 || mv.DecProto != 3 || mv.DecBaud != 57600 ||
		mv.DecChA != 1 || mv.DecChB != 0 || !mv.DecCPOL || mv.DecCPHA || mv.DecFormat != 1 {
		t.Fatalf("MenuView does not reflect the restored state: %+v", mv)
	}
}

// TRLC-LINKS: REQ-SDS-136
func TestApplySettingsViewClamps(t *testing.T) {
	c, _, _ := newC(t)
	c.ApplySettingsView(settings.ViewState{
		ViewMode: -7,
		Decode: settings.Decode{
			Proto: 99, Baud: 12345, ChA: -3, ChB: -3, Format: 47,
		},
	})
	got := c.SettingsView()
	if got.ViewMode != mod5b(-7) {
		t.Errorf("view mode not wrapped like the DISPLAY menu: %d", got.ViewMode)
	}
	if got.Decode.Proto != 0 {
		t.Errorf("out-of-range proto must default Off: %d", got.Decode.Proto)
	}
	if got.Decode.Baud != 115200 {
		t.Errorf("off-ladder baud must default 115200: %d", got.Decode.Baud)
	}
	if got.Decode.ChA>>1 != 0 || got.Decode.ChB>>1 != 0 {
		t.Errorf("channel roles escaped 0/1: %+v", got.Decode)
	}
	if got.Decode.Format != 2 { // mod3(47)
		t.Errorf("format not wrapped: %d", got.Decode.Format)
	}
}

// TRLC-LINKS: REQ-SDS-136
func TestApplySettingsViewKeepsTwoSignalRolesOpposite(t *testing.T) {
	c, _, _ := newC(t)
	// A (hostile or hand-edited) file putting SPI CLK and DATA on the same
	// channel must be repaired to the menu invariant (data on the other one).
	c.ApplySettingsView(settings.ViewState{
		Decode: settings.Decode{Proto: 4, Baud: 115200, ChA: 1, ChB: 1},
	})
	got := c.SettingsView().Decode
	if got.ChA != 1 || got.ChB != 0 {
		t.Fatalf("SPI roles not forced onto opposite channels: %+v", got)
	}
	// UART only uses ChA; any ChB value is normalized into 0/1 but not forced.
	c.ApplySettingsView(settings.ViewState{
		Decode: settings.Decode{Proto: 2, Baud: 19200, ChA: 0, ChB: 0},
	})
	if got := c.SettingsView().Decode; got.ChA != 0 {
		t.Fatalf("UART source lost: %+v", got)
	}
}

// Math, measurements, persistence and channel visibility round-trip through
// the saved setup (bug hunt 2026-10-10: they were lost on restart).
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
func TestSettingsViewDisplayRoundTrip(t *testing.T) {
	a, _, _ := newC(t)
	a.mu.Lock()
	a.mathMode, a.showMeas, a.persist, a.chDisp = 3, true, true, [2]bool{true, false}
	a.mu.Unlock()
	b, _, _ := newC(t)
	b.ApplySettingsView(a.SettingsView())
	v := b.MenuView()
	if v.MathMode != 3 || !v.ShowMeas || !v.Persist || !v.ShowC1 || v.ShowC2 {
		t.Fatalf("display setup lost: %+v", v)
	}
}

// The decoded-stream trigger and its value persist with the rest of DECODE
// (bug hunt 2026-10-10: a restart reset them to Error / 00).
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-018
func TestSettingsViewStreamTriggerRoundTrip(t *testing.T) {
	a, _, _ := newC(t)
	a.mu.Lock()
	a.decProto, a.decTrig, a.decValue = 1, 1, 0x55
	a.mu.Unlock()
	b, _, _ := newC(t)
	b.ApplySettingsView(a.SettingsView())
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.decTrig != 1 || b.decValue != 0x55 {
		t.Fatalf("stream trigger lost: trig %d value %#x", b.decTrig, b.decValue)
	}
}
