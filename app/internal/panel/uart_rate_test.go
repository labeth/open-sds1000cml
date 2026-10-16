// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"testing"

	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/settings"
)

// A UART on the fast image's receiver runs to 62.5 MBd: the Baud row takes
// such a rate typed, and a setup holding one restores it rather than 115200.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-229
func TestBaudReachesTheFastImagesUART(t *testing.T) {
	r := newViewRig(t)
	set(t, r.c, "bus.protocol", "UART")
	for _, tc := range []struct {
		text string
		want int
	}{{"25M", 25_000_000}, {"62.5M", 62_500_000}} {
		set(t, r.c, "bus.baud", tc.text)
		if got := r.c.snap().decBaud; got != tc.want {
			t.Fatalf("Baud %s: decoder at %d", tc.text, got)
		}
	}
	r.c.ApplySettingsView(settings.ViewState{Decode: settings.Decode{Proto: framecalc.ProtoUART, Baud: 62_500_000, ChB: 1}})
	if got := r.c.snap().decBaud; got != 62_500_000 {
		t.Fatalf("a setup at 62.5 MBd restored the decoder at %d", got)
	}
	if d := r.c.SettingsView().Decode; d.Baud != 62_500_000 {
		t.Fatalf("saved again as %d", d.Baud)
	}
}
