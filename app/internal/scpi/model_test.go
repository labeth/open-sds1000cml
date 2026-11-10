// ENGMODEL-OWNER-UNIT: FU-APP-SCPI

package scpi

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	inst "github.com/labeth/open-dso-core/instrument"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/uimodel/panel"
	scope "github.com/labeth/open-sds1000cml/app"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/board"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	engine "github.com/labeth/open-sds1000cml/app/internal/scopeapi"
)

// nullSPI is the front end's SPI without hardware.
type nullSPI struct{}

// TRLC-LINKS: REQ-SDS-024
func (nullSPI) WriteRelay(uint32) error { return nil }

// TRLC-LINKS: REQ-SDS-024
func (nullSPI) WriteGain(_, _ uint8) error { return nil }

// framed is a scope's controls with a fixed published frame.
type framed struct {
	inst.Controls
	f *sample.Frame
}

// TRLC-LINKS: REQ-SDS-024
func (s framed) WithFrame(fn func(*sample.Frame)) { fn(asPublished(s.f)) }

// modelHandler is the dialect over the instrument model alone, as the
// core's HAL cycle runs the scope: HALScope's controls over the board, the
// front end
// keeping its offset codes beside the model and the levels over them.
func modelHandler(tb testing.TB) *Handler {
	tb.Helper()
	m := inst.New(board.PowerOn(2), nil)
	m.SetTimebases(engine.SupportedTdivs())
	codes := board.NewModelCodes(m)
	fe := analog.New(nullSPI{}, func(time.Duration) {}, nil)
	fe.OnOffset(codes.SetOffsetDAC)
	lv := board.New(codes, fe)
	lv.Project(m)
	m.OnTrigType(board.ShareTrigger) // one comparator for every type, as the device composes it
	m.OnTrigSlope(board.KeepSlope)
	prof, err := scope.Profile(scope.Identity())
	if err != nil {
		tb.Fatal(err)
	}
	b := acq.New(acq.Config{})
	b.SetProfile(prof)
	b.SetFrontEnd(fe)
	return dualParts(tb, framed{panel.NewHALScope(m, b, "").Engine(), goldenScope().frame}, fe, lv)
}

// dualParts is the handler over sc, fe and lv with the golden display and
// setup memory.
func dualParts(tb testing.TB, sc Scope, fe *analog.FrontEnd, lv *board.Levels) *Handler {
	tb.Helper()
	h := New(sc, fe, lv, nil, func() []byte { return []byte("BMPDATA") }, tb.Logf)
	h.disp = &goldenDisplay{on: [2]bool{true, true}, x: [2]float64{.25, .75}, y: [2]float64{.25, .75}}
	h.SetSetupMemory(&fakeSetupFiles{files: map[string]bool{}})
	return h
}

// modelDiffers are the headers whose replies differ between the two
// compositions, with why: none may differ in what a controller reads back
// once the owner applied what was set.
var modelDiffers = map[string]string{
	// The cursors' time reads the snapshot's timebase: the engine reports a
	// timebase once its owner switched the band (never here: the engine is
	// not run), the model the one set. On the device the band switches at
	// the owner's next pass; TDIV? answers the set one on both.
	"C1:CRVA? HREL": "the timebase is applied by an owner this test does not run",
}

// The SDS1000CML+'s dialect answers every header the same over the
// instrument model alone as over its engine: the read-backs a controller
// sees do not change when the scope runs the core's cycle.
// TRLC-LINKS: REQ-SDS-024
func TestDialectOverTheModel(t *testing.T) {
	hm := modelHandler(t)
	raw, err := os.ReadFile("testdata/dialect.json")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	var diffs []string
	for i, l := range goldenLines {
		ge, ok := want[fmt.Sprintf("%03d", i)]
		if !ok {
			t.Fatalf("no recorded reply for command %d %q", i, l)
		}
		gm := hm.HandleLine([]byte(l + "\n"))
		if string(ge) == string(gm) {
			continue
		}
		if _, known := modelDiffers[l]; known {
			continue
		}
		diffs = append(diffs, fmt.Sprintf("%q: engine %q, model %q", l, ge, gm))
	}
	if len(diffs) > 0 {
		t.Fatalf("%d replies differ:\n%s", len(diffs), strings.Join(diffs, "\n"))
	}
}
