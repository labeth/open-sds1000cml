// ENGMODEL-OWNER-UNIT: FU-APP-ANALOG

package analog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/hal"
)

// The front end as hal.FrontEnd speaks volts at the probe tip: the ladder
// is the detents times the probe, Set changes only what differs, a V/div off
// the ladder is refused, and a probe change keeps the BNC settings.
// TRLC-LINKS: REQ-SDS-015, REQ-SDS-095, REQ-SDS-097
func TestFrontEndAsHAL(t *testing.T) {
	tr := &fakeTr{}
	f := New(tr, func(time.Duration) {}, nil)
	var staged []uint16
	f.OnOffset(func(_ int, c uint16) { staged = append(staged, c) })
	c := f.Get(1)
	if c.VoltsDiv != 1 || c.Probe != 1 || c.OffsetV != 0 || c.Coupling != hal.DC || c.BWLimit != 0 {
		t.Fatalf("boot channel %+v", c)
	}
	if l := f.Ladder(1); len(l) != len(Detents) || l[0].VoltsDiv != 0.002 || l[0].Zoom != 5 {
		t.Fatalf("ladder %+v", l)
	}
	if _, err := f.Set(1, c); err != nil || len(tr.ops) != 0 || len(staged) != 0 {
		t.Fatalf("an unchanged Set wrote: %v ops %d offsets %d", err, len(tr.ops), len(staged))
	}
	c.Probe, c.VoltsDiv, c.OffsetV = 10, 5, 2
	got, err := f.Set(1, c)
	if err != nil || got.VoltsDiv != 5 || f.VdivV(1) != 0.5 || f.OffsetReqV(1) != 0.2 || got.OffsetV != 2 {
		t.Fatalf("10x, 5 V/div, 2 V at the tip: %+v %v (BNC %v V/div, %v V)", got, err, f.VdivV(1), f.OffsetReqV(1))
	}
	c.VoltsDiv = 3
	var r *hal.Refusal
	if _, err := f.Set(1, c); !errors.As(err, &r) {
		t.Fatalf("3 V/div at 10x: %v, want a refusal", err)
	}
	g := f.OffsetGrid(1)
	if g.Step <= 0 || g.Min >= g.Max || got.OffsetV < g.Min || got.OffsetV > g.Max {
		t.Fatalf("offset grid %+v", g)
	}
	if f.RangeV(0) != 1 {
		t.Fatalf("C1's range %v V/div", f.RangeV(0))
	}
	_ = f.SetVdivV(0, 0.005)
	if f.RangeV(0) != 0.01 || f.VdivV(0) != 0.005 {
		t.Fatalf("5 mV/div: range %v", f.RangeV(0))
	}
}

// The front end names the factory table's author and refuses a
// self-calibration it does not have.
// TRLC-LINKS: REQ-SDS-015
func TestFrontEndCalibrationAsHAL(t *testing.T) {
	f := New(&fakeTr{}, func(time.Duration) {}, nil)
	c := f.Calibration()
	if c.Valid != (c.Author == "vendor") {
		t.Errorf("calibration %+v: a valid table is the vendor's, the defaults have no author", c)
	}
	if _, err := f.SelfCal(context.Background(), nil); err == nil {
		t.Error("self-calibration accepted")
	} else if _, ok := hal.AsRefusal(err); !ok {
		t.Errorf("self-calibration: %v, want a refusal", err)
	}
}
