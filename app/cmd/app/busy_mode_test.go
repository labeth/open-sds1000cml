// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"bytes"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/lcd"
)

// While the engine loads (a full record, an image) the display draws the
// loading indicator alone, in the status slot's rows; AUTO's search draws
// the whole screen a few times a second; a quick read (inside the
// indicator's grace) and the stream view enter no busy mode.
// TRLC-LINKS: REQ-SDS-168
func TestLCDBusyMode(t *testing.T) {
	for _, c := range []struct {
		st                 engine.Stats
		autoset, streaming bool
		want               int
	}{
		{engine.Stats{}, false, false, busyNone},
		{engine.Stats{Busy: "reading", BusyMs: 100}, false, false, busyNone},
		{engine.Stats{Busy: "reading", BusyMs: 400, BusyPct: 30}, false, false, busyLoad},
		{engine.Stats{Busy: "loading qualify image", BusyMs: 900}, true, false, busyLoad},
		{engine.Stats{}, true, false, busyAutoset},
		{engine.Stats{Busy: "reading", BusyMs: 400}, true, true, busyNone},
	} {
		if got := lcdBusyMode(c.st, c.autoset, c.streaming); got != c.want {
			t.Errorf("%q autoset %v streaming %v: mode %d, want %d", c.st.Busy, c.autoset, c.streaming, got, c.want)
		}
	}
	back := lcd.NewMemSurface()
	for i := range back.Pix {
		back.Pix[i] = 0x5A
	}
	before := append([]byte(nil), back.Pix...)
	if lcdBusyDraw(busyAutoset, engine.Stats{}, back) || !bytes.Equal(back.Pix, before) {
		t.Fatal("AUTO's busy mode drew the indicator")
	}
	if !lcdBusyDraw(busyLoad, engine.Stats{Busy: "reading", BusyMs: 400, BusyPct: 30}, back) {
		t.Fatal("the loading mode drew nothing")
	}
	slot := lcd.StatusSlotRows * lcd.W * 2
	if bytes.Equal(back.Pix[:slot], before[:slot]) {
		t.Fatal("the indicator is not in the status slot's rows")
	}
	if !bytes.Equal(back.Pix[slot:], before[slot:]) {
		t.Fatal("the loading mode drew outside the status slot")
	}
}
