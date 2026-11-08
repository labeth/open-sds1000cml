// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"os"
	"testing"

	"github.com/labeth/open-dso-core/framecalc"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/scopeapp"
	"github.com/labeth/open-dso-core/session"
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
		return (&composition{fe: &analog.FrontEnd{}}).options() // the front end as the unit has it
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

// captureRecipes are this scope's bench sources (bench/bench.json) as the
// demo's generator makes them, each set up as a bench capture of it would
// be: the made captures stand in for the bench's until the bench's are
// taken (dsosessions capture).
var captureRecipes = []demotest.Recipe{
	{
		Name: "tone-10mhz", About: "C1: the 10 MHz tone, 3.3 V logic (the bench's fpga-tone-10mhz)",
		Signals: map[string]session.Signal{"C1": {Kind: "square", Hz: 10e6, Vpp: 3.3, Offset: 1.65}},
		Rows:    map[string]string{"time.tdiv": "100ns", "ch1.vdiv": "1V", "trig.level": "1.65V"},
	},
	{
		Name: "uart-115200", About: "C1 and C2: UART 115200 8N1 sending 48 69 20 55 aa 0f f0 0a (the bench's fpga-uart)",
		Signals: map[string]session.Signal{
			"C1": {Kind: "uart", Rate: 115200, Vpp: 3.3, Offset: 1.65, Hex: "48692055aa0ff00a"},
			"C2": {Kind: "uart", Rate: 115200, Vpp: 3.3, Offset: 1.65, Hex: "48692055aa0ff00a"},
		},
		Rows:   map[string]string{"time.tdiv": "100us", "ch1.vdiv": "1V", "ch2.vdiv": "1V", "trig.level": "1.65V"},
		Decode: &session.CaptureDecode{Proto: framecalc.ProtoUART, Baud: 115200, ChA: 0},
	},
	{
		Name: "spi-200khz", About: "C1 MOSI, C2 CLK: SPI mode 0 at 200 kHz sending 48 69 20 55 aa 0f f0 0a (the bench's fpga-spi)",
		Signals: map[string]session.Signal{
			"C1": {Kind: "spi", Line: "mosi", Rate: 200e3, Vpp: 3.3, Offset: 1.65, Hex: "48692055aa0ff00a", Opts: map[string]string{"mode": "0"}},
			"C2": {Kind: "spi", Line: "clk", Rate: 200e3, Vpp: 3.3, Offset: 1.65, Hex: "48692055aa0ff00a", Opts: map[string]string{"mode": "0"}},
		},
		Rows:   map[string]string{"time.tdiv": "50us", "ch1.vdiv": "1V", "ch2.vdiv": "1V", "trig.edge": "C2 Rise", "trig.level": "1.65V"},
		Decode: &session.CaptureDecode{Proto: framecalc.ProtoSPI, ChA: 1, ChB: 0},
	},
	{
		Name: "i2c-200khz", About: "C1 SDA, C2 SCL: I2C at 200 kHz writing 55 aa 0f f0 to 0x24 (the bench's fpga-i2c)",
		Signals: map[string]session.Signal{
			"C1": {Kind: "i2c", Line: "sda", Rate: 200e3, Vpp: 3.3, Offset: 1.65, Hex: "55aa0ff0", Opts: map[string]string{"addr": "0x24", "rw": "w"}},
			"C2": {Kind: "i2c", Line: "scl", Rate: 200e3, Vpp: 3.3, Offset: 1.65, Hex: "55aa0ff0", Opts: map[string]string{"addr": "0x24", "rw": "w"}},
		},
		Rows:   map[string]string{"time.tdiv": "50us", "ch1.vdiv": "1V", "ch2.vdiv": "1V", "trig.level": "1.65V"},
		Decode: &session.CaptureDecode{Proto: framecalc.ProtoI2C, ChA: 1, ChB: 0},
	},
}

// A record this scope captured replays through its browser demo with the
// measurements and packets the scope reported of it, exactly
// (demo/captures; UPDATE_DEMO_FIXTURES=1 remakes the recipes').
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
	demotest.Captures(t, prof, demo, "../../../demo/captures", captureRecipes)
}
