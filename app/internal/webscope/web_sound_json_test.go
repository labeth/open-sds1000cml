// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/labeth/open-dso-core/sound"
	"github.com/labeth/open-dso-core/webserver/wire"
	"github.com/labeth/open-sds1000cml/app/internal/panel"
)

// TestJSONSafeSeries reads a SOUND report with long series through
// jsonSafe: the finite series keep their JSON names and values, a series
// holding a NaN still reads it as null, and the whole report encodes.
// TRLC-LINKS: REQ-SDS-274
func TestJSONSafeSeries(t *testing.T) {
	var r panel.SoundReport
	for i := range 100 {
		r.Drift = append(r.Drift, sound.DriftPoint{T: float64(i), Hz: 440.5, Cents: -1.25})
		r.ADSREnv = append(r.ADSREnv, float64(i)/8)
		r.VOct = append(r.VOct, sound.VOctPoint{V: float64(i), Hz: 55})
	}
	r.VOct[50].Hz = math.NaN()
	b, err := json.Marshal(map[string]any{"report": wire.JSONSafe(reflect.ValueOf(r))})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Report struct {
			Drift []map[string]any `json:"drift"`
			Env   []any            `json:"adsr_env"`
			VOct  []map[string]any `json:"voct"`
		} `json:"report"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	g := got.Report
	if len(g.Drift) != 100 || g.Drift[7]["t"] != 7.0 || g.Drift[7]["hz"] != 440.5 || g.Drift[7]["cents"] != -1.25 || len(g.Drift[7]) != 3 {
		t.Fatalf("drift %v", g.Drift[7])
	}
	if len(g.Env) != 100 || g.Env[9] != 9.0/8 {
		t.Fatalf("envelope %v", g.Env[:10])
	}
	if len(g.VOct) != 100 || g.VOct[50]["hz"] != nil || g.VOct[51]["hz"] != 55.0 || g.VOct[51]["v"] != 51.0 {
		t.Fatalf("V/oct %v %v", g.VOct[50], g.VOct[51])
	}
}
