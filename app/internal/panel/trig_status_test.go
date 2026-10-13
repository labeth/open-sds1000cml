// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"

	"open-sds/app/internal/panel/wsview"
)

// The TRIG tab says whether the FPGA decides a pulse, slope, video or zone
// trigger, or why it cannot yet (ADR-HARDWARE-QUALIFIERS), drawn for a choice
// row too.
// TRLC-LINKS: REQ-SDS-212
func TestTrigTabNamesTheHardwareTrigger(t *testing.T) {
	c, eng, _ := newC(t)
	c.SheetSet("trig.type", nil, "Pulse")
	press(t, c, "trigmenu")
	status := func() (string, bool) {
		var text []string
		warn := false
		for _, sp := range c.View().Sheet.Status {
			text = append(text, sp.Text)
			warn = warn || sp.Col == wsview.Warn
		}
		return strings.Join(text, " | "), warn
	}
	eng.stats.TriggerBackend = "none: learning the signal levels on C1"
	if got, warn := status(); !strings.Contains(got, "Pulse width: no hardware trigger yet - learning the signal levels on C1") || !warn {
		t.Fatalf("waiting: %q (warn %v)", got, warn)
	}
	eng.stats.TriggerBackend, eng.stats.ZoneBackend = "hardware-pulse", "none: the FPGA takes four zones"
	if got, _ := status(); !strings.Contains(got, "Pulse width decided in the FPGA") || !strings.Contains(got, "Zones: no hardware trigger yet - the FPGA takes four zones") {
		t.Fatalf("decided: %q", got)
	}
	// edge triggers dropped as noise (no edge where the FPGA pointed) are said
	c.SheetSet("trig.type", nil, "Edge")
	eng.stats.TriggerBackend, eng.stats.ZoneBackend, eng.stats.NoiseTriggers = "", "", 0.6
	if got, warn := status(); !strings.Contains(got, "60% of triggers dropped as noise: raise the gain or move the level") || !warn {
		t.Fatalf("noise triggers: %q (warn %v)", got, warn)
	}
	// it fits the LCD's status line (77 characters)
	if got, _ := status(); len("100% of triggers dropped as noise: raise the gain or move the level") > 77 {
		t.Fatalf("the warning is longer than the LCD line: %q", got)
	}
	eng.stats.NoiseTriggers = 0.1
	if got, _ := status(); strings.Contains(got, "noise") {
		t.Fatalf("a few noise triggers warned: %q", got)
	}
	// an edge trigger names no qualifier, whatever the last type left
	eng.stats.TriggerBackend = "hardware-pulse"
	if got, _ := status(); strings.Contains(got, "decided in the FPGA") {
		t.Fatalf("an edge trigger: %q", got)
	}
}

// The phone's TRIG page shows the same lines (the sheets model's notes).
// TRLC-LINKS: REQ-SDS-220
func TestRemoteTrigPageNamesTheHardwareTrigger(t *testing.T) {
	c, eng, _ := newC(t)
	c.SheetSet("trig.type", nil, "Slope")
	eng.stats.TriggerBackend = "none: loading the qualifier FPGA image"
	for _, tm := range c.Sheets().Tabs {
		if tm.ID != "trig" {
			if len(tm.Notes) != 0 {
				t.Fatalf("tab %s has notes %+v", tm.ID, tm.Notes)
			}
			continue
		}
		for _, n := range tm.Notes {
			if n.Warn && n.Text == "Slope: no hardware trigger yet - loading the qualifier FPGA image" {
				return
			}
		}
		t.Fatalf("TRIG notes %+v", tm.Notes)
	}
	t.Fatal("no TRIG tab")
}
