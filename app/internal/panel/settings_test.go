package panel

import (
	"testing"

	"open-sds/app/internal/settings"
)

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
