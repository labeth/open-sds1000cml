// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"os"
	"testing"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/scopeapp"
	"github.com/labeth/open-dso-core/simdemo/demotest"
	scope "github.com/labeth/open-sds1000cml/app"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	cmlpanel "github.com/labeth/open-sds1000cml/app/internal/panel"
)

// demoFile is the browser demo's file of this scope.
const demoFile = "../../../demo/demo.json"

// The core keeps this scope's device composition, which its browser demo
// is compared with (open-dso-core simdemo.TestCompositionsAgree): the
// options the app runs with, over the hardware's parts this test has none
// of (only their presence is described).
// TRLC-LINKS: REQ-SDS-167
func TestDemoComposition(t *testing.T) {
	demo, err := os.ReadFile(demoFile)
	if err != nil {
		t.Fatal(err)
	}
	prof, err := os.ReadFile("../../profile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	demotest.Composition(t, "open-sds1000cml", prof, demo, func(hal.Board) scopeapp.Options {
		fe := &analog.FrontEnd{}
		return (&composition{fe: fe, afe: &modelAnalog{FrontEnd: fe}}).options() // the front end as the unit has it
	})
}

// The browser demo's front panel is this panel: its key table is the
// matrix decode's, its layout places every key and knob, and the profile
// lists what they send.
// TRLC-LINKS: REQ-SDS-135
func TestDemoPanel(t *testing.T) {
	p, err := scope.Profile(scope.Identity())
	if err != nil {
		t.Fatal(err)
	}
	demotest.Panel(t, demoFile, p.Inputs, cmlpanel.Table())
}

// A record this scope captured on the bench (demo/captures, dsosessions
// capture) replays through its browser demo with the measurements and
// packets the scope reported of it, exactly, but for what the scope
// measured on edge windows the record does not hold
// (session.CaptureFile.FromEdgeWindows). The captures are the bench's, so
// no recipe remakes them.
// TRLC-LINKS: REQ-SDS-167
func TestDemoReplaysCaptures(t *testing.T) {
	demo, err := os.ReadFile(demoFile)
	if err != nil {
		t.Fatal(err)
	}
	prof, err := os.ReadFile("../../profile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	demotest.Captures(t, prof, demo, "../../../demo/captures", nil)
}
