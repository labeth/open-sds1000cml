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
	got := c.SettingsView()
	got.UI = settings.UI{}         // a file without the workspace UI keeps the UI as it is
	got.Serial = settings.Serial{} // ... and one without the serial block keeps the trigger
	if got != want {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	// The render path must see the restored state (the LCD/HUD reads Snapshot).
	mv, _ := c.Snapshot()
	if mv.ViewMode != 2 || mv.DecProto != 3 || mv.DecBaud != 57600 ||
		mv.DecChA != 1 || mv.DecChB != 0 || !mv.DecCPOL || mv.DecCPHA || mv.DecFormat != 1 {
		t.Fatalf("Snapshot does not reflect the restored state: %+v", mv)
	}
}

// The workspace, each workspace's view, the chosen measurements, the FFT
// source, the master depth and the remote-notice switch survive a restart or
// a recalled setup; a recall enters the saved workspace on its view.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-211
func TestSettingsViewWorkspaceRoundTrip(t *testing.T) {
	a, _, _ := newC(t)
	press(t, a, "f3", "f3") // SPECTRUM, then its next view: the spectrogram
	a.mu.Lock()
	a.meas = []measItem{{ch: 1, param: 3}, {ch: 0, param: 0}}
	a.fftSrc, a.stackN, a.remoteNotices = 1, 128, false
	a.ui.busEye = true
	a.mu.Unlock()
	saved := a.SettingsView()
	if !saved.UI.Set || saved.UI.Workspace != 2 || saved.UI.Views[2] != viewSpectrogram || saved.ViewMode != viewSpectrogram {
		t.Fatalf("saved UI %+v view %d", saved.UI, saved.ViewMode)
	}
	b, _, _ := newC(t)
	press(t, b, "menu") // a sheet open on the LCD closes on a panel recall
	b.ApplySettingsView(saved)
	st := b.PanelStatus()
	if st.Workspace != "SPECTRUM" || st.View != "Spectrogram" || st.Mode != "workspace" || st.Views[1] != "Eye diagram" {
		t.Fatalf("restored %s/%s mode %s views %v", st.Workspace, st.View, st.Mode, st.Views)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.meas) != 2 || b.meas[0] != (measItem{ch: 1, param: 3}) || b.fftSrc != 1 || b.stackN != 128 || b.remoteNotices {
		t.Fatalf("restored meas %v src %d depth %d notices %v", b.meas, b.fftSrc, b.stackN, b.remoteNotices)
	}
}

// TRLC-LINKS: REQ-SDS-136
func TestApplySettingsViewClamps(t *testing.T) {
	c, _, _ := newC(t)
	c.ApplySettingsView(settings.ViewState{
		ViewMode: -7,
		Decode: settings.Decode{
			Proto: 99, Baud: 7, ChA: -3, ChB: -3, Format: 47,
		},
	})
	got := c.SettingsView()
	if got.ViewMode != mod5(-7) {
		t.Errorf("view mode not wrapped like the VIEW tab: %d", got.ViewMode)
	}
	if got.Decode.Proto != 0 {
		t.Errorf("out-of-range proto must default Off: %d", got.Decode.Proto)
	}
	if got.Decode.Baud != 115200 {
		t.Errorf("a baud no decoder takes must default 115200: %d", got.Decode.Baud)
	}
	c.ApplySettingsView(settings.ViewState{Decode: settings.Decode{Proto: 2, Baud: 12345}})
	if b := c.SettingsView().Decode.Baud; b != 12345 { // a typed baud (the BUS tab takes any)
		t.Errorf("custom baud lost: %d", b)
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
	v, _ := b.Snapshot()
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

// The serial trigger's match, I2C address/direction and armed state survive a
// restart or a recalled setup; an older file without the block leaves it
// alone, and a match that no longer parses comes back as any byte.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-089
func TestSettingsViewSerialRoundTrip(t *testing.T) {
	a, aeng, _ := newC(t)
	a.ApplySettingsView(settings.ViewState{Decode: settings.Decode{Proto: 2, Baud: 115200, ChA: 0, ChB: 1}})
	if err := a.setMatchTokens([]string{"55", "AA"}); err != nil {
		t.Fatal(err)
	}
	if err := a.armSerial(); err != nil {
		t.Fatal(err)
	}
	saved := a.SettingsView()
	if want := (settings.Serial{Set: true, Armed: true, Match: "55 AA", Addr: -1, RW: 2}); saved.Serial != want {
		t.Fatalf("saved %+v, want %+v", saved.Serial, want)
	}
	if aeng.Snapshot().SerialMode == 0 {
		t.Fatal("source trigger not armed")
	}

	b, beng, _ := newC(t)
	b.ApplySettingsView(saved)
	if beng.Snapshot().SerialMode == 0 {
		t.Fatal("restored trigger not armed")
	}
	if p := beng.SerialConfig(); len(p.Bytes) != 2 || p.Bytes[0] != 0x55 || p.Bytes[1] != 0xAA || p.Proto != 1 {
		t.Fatalf("restored params %+v", p)
	}

	old := saved
	old.Serial = settings.Serial{} // a file from before the block
	b.ApplySettingsView(old)
	if beng.Snapshot().SerialMode == 0 {
		t.Fatal("a file without the serial block disarmed the trigger")
	}

	off := saved
	off.Serial.Armed, off.Serial.Match = false, "not hex"
	b.ApplySettingsView(off)
	if beng.Snapshot().SerialMode != 0 {
		t.Fatal("saved disarmed, restored armed")
	}
	if p := beng.SerialConfig(); len(p.Bytes) != 0 || len(p.Pattern) != 0 {
		t.Fatalf("a bad saved match should be any byte, got %+v", p)
	}
}
