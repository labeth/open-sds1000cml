// ENGMODEL-OWNER-UNIT: FU-APP-ANALOG
package analog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// ZeroTrim is a per-channel, per-detent correction to the calibration file's
// offset-DAC zero (ADR-OFFSET-ZERO-TRIM): the input-referred volts at which an
// open input sits with the offset at 0 V. The factory zeros were taken under
// the factory's own offset law; under ours an open input sits up to 2.7 div
// off at 2 mV/div and 0.25 div off at 500 mV/div without the trim.
// TRLC-LINKS: REQ-SDS-095
type ZeroTrim struct {
	Comment string    `json:"comment,omitempty"`
	VdivV   []float64 `json:"vdiv_v"`
	C1V     []float64 `json:"c1_v"`
	C2V     []float64 `json:"c2_v"`
	// detent is each VdivV's detent, found when the trim is read (empty for
	// a trim made in code).
	detent []int
}

// zeroTrimDefault is this unit's open-input zero, measured with
// both inputs unconnected (offset 0, DC, 1 µs/div, mean of two sweeps).
//
//go:embed zero_default.json
var zeroTrimDefault []byte

// DefaultZeroTrim returns the built-in zero trim.
// TRLC-LINKS: REQ-SDS-095
func DefaultZeroTrim() (*ZeroTrim, error) { return parseZeroTrim(zeroTrimDefault) }

// LoadZeroTrim reads a zero trim file.
// TRLC-LINKS: REQ-SDS-095
func LoadZeroTrim(path string) (*ZeroTrim, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseZeroTrim(raw)
}

// TRLC-LINKS: REQ-SDS-095
func parseZeroTrim(raw []byte) (*ZeroTrim, error) {
	var t ZeroTrim
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("zero trim: %v", err)
	}
	if len(t.VdivV) != len(t.C1V) || len(t.VdivV) != len(t.C2V) {
		return nil, fmt.Errorf("zero trim: %d detents, %d/%d channel values", len(t.VdivV), len(t.C1V), len(t.C2V))
	}
	t.detent = make([]int, len(t.VdivV))
	for i, v := range t.VdivV {
		vd, ok := PlanVdiv(v)
		if !ok {
			return nil, fmt.Errorf("zero trim: %g V/div is not a detent", v)
		}
		t.detent[i] = vd
		for _, z := range [2]float64{t.C1V[i], t.C2V[i]} {
			if math.IsNaN(z) || math.Abs(z) > offsetTierRangeV(vd) {
				return nil, fmt.Errorf("zero trim: %g V at %g V/div is out of range", z, v)
			}
		}
	}
	return &t, nil
}

// SetZeroTrim installs a zero trim; detents it does not name keep the
// calibration file's zero. Offsets already set are re-staged.
// TRLC-LINKS: REQ-SDS-095
func (f *FrontEnd) SetZeroTrim(t *ZeroTrim) {
	f.mu.Lock()
	f.zeroTrimV = [2][numDetents]float64{}
	if t != nil {
		for i, v := range t.VdivV {
			vd := 0
			if len(t.detent) == len(t.VdivV) {
				vd = t.detent[i] // read with the trim (LoadZeroTrim)
			} else {
				vd, _ = PlanVdiv(v)
			}
			f.zeroTrimV[0][vd], f.zeroTrimV[1][vd] = t.C1V[i], t.C2V[i]
		}
	}
	set := f.offSet
	req := f.offReqV
	f.mu.Unlock()
	for ch := range 2 {
		if set[ch] {
			f.SetOffset(ch, req[ch])
		}
	}
}
