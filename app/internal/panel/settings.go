package panel

import "open-sds/app/internal/settings"

// decBauds is the DECODE menu's UART baud ladder (menuCycle slot 1) — the
// domain a restored baud must come from.
var decBauds = []int{9600, 19200, 38400, 57600, 115200, 230400}

// SettingsView reports the controller-owned slice of the persisted setup
// (settings.Panel surface): the device decode config and the view mode.
func (c *Controller) SettingsView() settings.ViewState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return settings.ViewState{
		ViewMode: c.viewMode,
		Decode: settings.Decode{
			Proto: c.decProto, Baud: c.decBaud,
			ChA: c.decChA, ChB: c.decChB,
			CPOL: c.decCPOL, CPHA: c.decCPHA,
			Format: c.decFormat,
		},
	}
}

// ApplySettingsView restores the controller-owned setup, enforcing the same
// domains the DECODE/DISPLAY menus keep (menuCycle): proto ∈ [0,4], baud from
// the menu ladder, clock/data roles on opposite channels for the two-signal
// protocols, format ∈ [0,2], view mode ∈ [0,4]. Hostile values degrade to
// defaults — never a panic, never an out-of-domain field. All fields are
// mu-guarded plain state with no engine side-effects (the render loop picks
// them up via MenuView, exactly as after a menu press).
func (c *Controller) ApplySettingsView(v settings.ViewState) {
	d := v.Decode
	if d.Proto < 0 || d.Proto > 4 {
		d.Proto = 0 // Off
	}
	inLadder := false
	for _, b := range decBauds {
		if b == d.Baud {
			inLadder = true
			break
		}
	}
	if !inLadder {
		d.Baud = 115200
	}
	d.ChA &= 1
	d.ChB &= 1
	if d.Proto == 3 || d.Proto == 4 { // I2C/SPI: data on the OTHER channel (menu invariant)
		d.ChB = 1 - d.ChA
	}
	d.Format = mod3(d.Format)
	view := mod5b(v.ViewMode)

	c.mu.Lock()
	c.viewMode = view
	c.decProto, c.decBaud = d.Proto, d.Baud
	c.decChA, c.decChB = d.ChA, d.ChB
	c.decCPOL, c.decCPHA = d.CPOL, d.CPHA
	c.decFormat = d.Format
	c.mu.Unlock()
}
