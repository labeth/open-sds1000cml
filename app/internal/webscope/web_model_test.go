// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"testing"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/instrument"
	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-sds1000cml/app/internal/board"
	engine "github.com/labeth/open-sds1000cml/app/internal/scopeapi"
)

// Over the instrument model alone the controls' snapshot carries no DAC
// codes: the status takes the comparator's and the offsets' codes from the
// levels (board.ModelCodes), so its codes, the level's volts and the
// offsets read the same as over the engine, whose snapshot carries them.
// TRLC-LINKS: REQ-SDS-164, REQ-SDS-098, REQ-SDS-095
func TestStatusCodesOverTheModel(t *testing.T) {
	const trig, off1, off2 = 30000, 0x7000, 0x9000
	eng := &fakeScope{stats: engine.Stats{TrigCode: trig, OffC1: off1, OffC2: off2, TrigSource: 1}}
	overEngine := getStatus(t, newServer(eng, nil, nil, nil))

	m := instrument.New(instrument.State{Channels: make([]hal.Channel, 2)}, nil)
	codes := board.NewModelCodes(m)
	codes.SetTrigLevelCode(trig)
	codes.SetOffsetDAC(0, off1)
	codes.SetOffsetDAC(1, off2)
	model := &fakeScope{stats: engine.Stats{TrigSource: 1}}
	sc := New(model, nil, board.New(codes, nil), nil, nil)
	overModel := getStatus(t, &Server{Scope: sc, Server: webserver.New(sc)})
	for _, k := range []string{"trig_code", "off_c1", "off_c2", "trig_volts", "off1_v", "off2_v"} {
		if overModel[k] != overEngine[k] {
			t.Errorf("%s: %v over the model, %v over the engine", k, overModel[k], overEngine[k])
		}
	}
	if overModel["trig_code"] != float64(trig) {
		t.Errorf("trig_code %v", overModel["trig_code"])
	}
}
