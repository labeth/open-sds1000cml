// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"os"
	"slices"
	"testing"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/profile"

	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/lcd"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The scope's profile.yaml loads for the SDS1102CML+ and says what the
// app's own constants say, so the data sheet cannot drift from the code.
// TRLC-LINKS: REQ-SDS-010
func TestProfileMatchesTheApp(t *testing.T) {
	data, err := os.ReadFile("../../profile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p, err := profile.Load(data, hal.Identity{Model: "SDS1102CML+"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Timebase.TdivS, engine.SupportedTdivs()) {
		t.Errorf("timebase %v, the engine's ladder %v", p.Timebase.TdivS, engine.SupportedTdivs())
	}
	if p.MaxDepth != int(sramcapture.SamplesPerChannel) || p.RawAccess.MaxDepth != p.MaxDepth {
		t.Errorf("max_depth %d (raw access %d), the SRAM record holds %d", p.MaxDepth, p.RawAccess.MaxDepth, sramcapture.SamplesPerChannel)
	}
	if p.Display.W != lcd.W || p.Display.H != lcd.H || p.Display.Format != hal.RGB565 {
		t.Errorf("display %+v, the LCD is %dx%d RGB565", p.Display, lcd.W, lcd.H)
	}
	if p.Channels != 2 || p.Format.Bits != 8 {
		t.Errorf("channels %d bits %d", p.Channels, p.Format.Bits)
	}
	for _, part := range []string{"logic-probe", "generator"} {
		if p.Absent[part] == "" {
			t.Errorf("no reason for the absent %s", part)
		}
	}
	if _, err := profile.Load(data, hal.Identity{Model: "SDS1102CML"}); err == nil {
		t.Error("the original CML (no +) is not this firmware's unit, and must be refused")
	}
}
