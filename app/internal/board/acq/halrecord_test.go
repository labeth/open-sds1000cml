// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/hal/prototrig"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// These inputs and fabric configurations were recorded from the passing
// acquisition replay on the SRAM owner. Replaying the distinct arms keeps
// its board programming contract without retaining a second owner loop.
// The shared core's cycle tests cover scheduling, run control and frames.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-008, REQ-SDS-009, REQ-SDS-040
func TestHALRecordReplay(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "halrecord", "arms.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name string
		Caps []string
		Arms []struct {
			Plan    hal.Plan
			Trigger json.RawMessage
			Want    sramcapture.Config
		}
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 19 {
		t.Fatalf("recorded scenarios: got %d, want 19", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			b, m, _ := newTestBoard(tc.Caps...)
			// The recorder had no SPI front end: trigger volts used the board's
			// nominal 1 V/div conversion, just as this board does without one.
			b.SetFrontEnd(nil)
			c := b.ImageCaps()
			for _, capability := range tc.Caps {
				switch capability {
				case "box":
					c.BoxDecim = true
				case "sample-uart":
					c.SampleUART = true
				case "sequence":
					c.Sequence = true
				}
			}
			b.SetImageCaps(c)
			for i, arm := range tc.Arms {
				spec := recordedSpec(t, arm.Trigger)
				if err := b.Trigger().Program(spec); err != nil {
					t.Fatalf("arm %d trigger: %v", i+1, err)
				}
				if err := b.Acquirer().Configure(arm.Plan); err != nil {
					t.Fatalf("arm %d configure: %v", i+1, err)
				}
				if err := b.Acquirer().Arm(); err != nil {
					t.Fatalf("arm %d: %v", i+1, err)
				}
				got := m.armed[len(m.armed)-1]
				if !reflect.DeepEqual(got, arm.Want) {
					t.Errorf("arm %d fabric configuration differs:\n got %+v\nwant %+v", i+1, got, arm.Want)
				}
			}
		})
	}
}

// recordedSpec restores the one polymorphic trigger field. These scenarios
// use either an ordinary trigger or the core's bus request.
func recordedSpec(t *testing.T, data []byte) hal.TriggerSpec {
	t.Helper()
	var raw struct {
		Stages []struct{ Proto json.RawMessage }
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	spec := hal.TriggerSpec{Stages: make([]hal.TrigStage, len(raw.Stages))}
	for i, stage := range raw.Stages {
		if len(stage.Proto) > 0 && string(stage.Proto) != "null" {
			spec.Stages[i].Proto = &prototrig.Bus{}
		}
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}
