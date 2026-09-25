// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// InterleaveCalibration contains measured offset, gain and aperture corrections.
// Revision 11 does not expose a converter-phase tag. Match the measured joint
// two-channel offset fingerprint to its cyclic rotation, or leave data alone.
// A mismatching input (including a strong Fs/5 signal) must not be calibrated
// by estimating and subtracting its own phase averages.
// TRLC-LINKS: REQ-SDS-016
type InterleaveCalibration struct {
	Vdiv         [2]float64    `json:"vdiv"`
	VerifiedVdiv [2][]float64  `json:"verified_vdiv,omitempty"`
	Offset       [2][5]float64 `json:"offset"`
	Gain         [2][5]float64 `json:"gain,omitempty"`
	GainOffset   [2][5]float64 `json:"gain_offset,omitempty"`
	GainVdiv     [2]float64    `json:"gain_vdiv,omitempty"`
	TimingNs     [2][5]float64 `json:"timing_ns,omitempty"`
	TimingVdiv   [2]float64    `json:"timing_vdiv,omitempty"`
	table        [2][2][5][256]uint16
	tableReady   bool
}

// TRLC-LINKS: REQ-SDS-016
func LoadInterleaveCalibration(path string) (*InterleaveCalibration, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c InterleaveCalibration
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	for ch := 0; ch < 2; ch++ {
		if c.Vdiv[ch] <= 0 || math.IsNaN(c.Vdiv[ch]) || math.IsInf(c.Vdiv[ch], 0) {
			return nil, fmt.Errorf("invalid calibration scale")
		}
		for _, scale := range c.VerifiedVdiv[ch] {
			if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
				return nil, fmt.Errorf("invalid verified calibration scale")
			}
		}
		if c.GainVdiv[ch] != 0 {
			if math.IsNaN(c.GainVdiv[ch]) || math.IsInf(c.GainVdiv[ch], 0) || c.GainVdiv[ch] <= 0 {
				return nil, fmt.Errorf("invalid gain calibration scale")
			}
			gainSum, offsetSum := 0.0, 0.0
			for phase, gain := range c.Gain[ch] {
				offset := c.GainOffset[ch][phase]
				if math.IsNaN(gain) || math.IsInf(gain, 0) || gain < .9 || gain > 1.1 || math.IsNaN(offset) || math.IsInf(offset, 0) || math.Abs(offset) > 2 {
					return nil, fmt.Errorf("invalid converter gain calibration")
				}
				gainSum += gain
				offsetSum += offset
			}
			if math.Abs(gainSum-5) > .001 || math.Abs(offsetSum) > .01 {
				return nil, fmt.Errorf("gain calibration must preserve nominal channel scale and centre")
			}
		}
		if c.TimingVdiv[ch] != 0 {
			if math.IsNaN(c.TimingVdiv[ch]) || math.IsInf(c.TimingVdiv[ch], 0) || c.TimingVdiv[ch] <= 0 || c.TimingVdiv[ch] != c.GainVdiv[ch] {
				return nil, fmt.Errorf("timing calibration requires matching gain scale")
			}
			sum := 0.0
			for _, delay := range c.TimingNs[ch] {
				if math.IsNaN(delay) || math.IsInf(delay, 0) || math.Abs(delay) > .5 {
					return nil, fmt.Errorf("invalid aperture correction")
				}
				sum += delay
			}
			if math.Abs(sum) > .001 {
				return nil, fmt.Errorf("aperture correction must preserve channel delay")
			}
		}
		sum := 0.0
		for _, v := range c.Offset[ch] {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 32 {
				return nil, fmt.Errorf("invalid calibration offset")
			}
			sum += v
		}
		if math.Abs(sum) > 0.01 {
			return nil, fmt.Errorf("calibration must preserve channel DC")
		}
	}
	return &c, nil
}

// TRLC-LINKS: REQ-SDS-016
func (c *InterleaveCalibration) apply(a, b []uint8, q1, q2 []uint16, scale [2]float64, sampleS float64) bool {
	if c == nil || !c.supportsScale(scale) || len(a) < 2000 || len(a) != len(b) || len(q1) != len(a) || len(q2) != len(a) {
		return false
	}
	var means [2][5]float64
	for ch, x := range [][]uint8{a, b} {
		var count [5]int
		var sums [5]uint32
		for i, v := range x {
			if v == 0 || v == 255 {
				return false
			}
			sums[i%5] += uint32(v)
			count[i%5]++
		}
		avg := 0.0
		for i := 0; i < 5; i++ {
			means[ch][i] = float64(sums[i]) / float64(count[i])
			avg += means[ch][i] / 5
		}
		for i := 0; i < 5; i++ {
			means[ch][i] -= avg
		}
	}
	best, second, rotation := math.Inf(1), math.Inf(1), 0
	for r := 0; r < 5; r++ {
		score := 0.0
		for ch := 0; ch < 2; ch++ {
			for i := 0; i < 5; i++ {
				d := means[ch][i] - c.Offset[ch][(i+r)%5]
				score += d * d / 10
			}
		}
		if score < best {
			second = best
			best = score
			rotation = r
		} else if score < second {
			second = score
		}
	}
	if best > 0.25 || second-best < 1 {
		return false
	}
	if !c.tableReady {
		for ch := 0; ch < 2; ch++ {
			for mode := 0; mode < 2; mode++ {
				for phase := 0; phase < 5; phase++ {
					for v := 0; v < 256; v++ {
						corrected := float64(v) - c.Offset[ch][phase]
						if mode == 1 && c.GainVdiv[ch] > 0 {
							corrected = 128 + (corrected-128-c.GainOffset[ch][phase])/c.Gain[ch][phase]
						}
						corrected = math.Round(corrected * 256)
						if corrected < 0 {
							corrected = 0
						}
						if corrected > 65535 {
							corrected = 65535
						}
						c.table[ch][mode][phase][v] = uint16(corrected)
					}
				}
			}
		}
		c.tableReady = true
	}
	for ch, x := range [][]uint8{a, b} {
		q := q1
		if ch == 1 {
			q = q2
		}
		mode := 0
		if c.GainVdiv[ch] > 0 && scale[ch] == c.GainVdiv[ch] {
			mode = 1
		}
		for i, v := range x {
			q[i] = c.table[ch][mode][(i+rotation)%5][v]
			x[i] = roundQ8(q[i])
		}
		if c.TimingVdiv[ch] > 0 && scale[ch] == c.TimingVdiv[ch] && sampleS == 2e-9 {
			correctAperture(q, x, c.TimingNs[ch], rotation)
		}
	}

	return true
}

// Additional ranges must be measured explicitly. The live fingerprint and
// clipping checks still apply; this never learns offsets from the input.
// TRLC-LINKS: REQ-SDS-016
func (c *InterleaveCalibration) supportsScale(scale [2]float64) bool {
	for ch := 0; ch < 2; ch++ {
		matched := scale[ch] == c.Vdiv[ch]
		for _, v := range c.VerifiedVdiv[ch] {
			if scale[ch] == v {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// Five-point derivative shifts each converter to the common sample grid.
// Keep original neighbours while updating in place; preserve endpoint samples.
// Qualified for the raw 500 MS/s path, not decimated/precision records.
// TRLC-LINKS: REQ-SDS-016
func correctAperture(q []uint16, codes []uint8, delayNs [5]float64, rotation int) {
	if len(q) < 5 {
		return
	}
	prev2, prev1 := float64(q[0]), float64(q[1])
	for i := 2; i < len(q)-2; i++ {
		current := float64(q[i])
		derivative := (prev2 - 8*prev1 + 8*float64(q[i+1]) - float64(q[i+2])) / 12
		corrected := math.Round(current + delayNs[(i+rotation)%5]/2*derivative)
		q[i] = uint16(math.Max(0, math.Min(65535, corrected)))
		codes[i] = roundQ8(q[i])
		prev2, prev1 = prev1, current
	}
}

// TRLC-LINKS: REQ-SDS-016
func (c *InterleaveCalibration) hasTiming(scale [2]float64, sampleS float64) bool {
	return sampleS == 2e-9 && ((c.TimingVdiv[0] > 0 && c.TimingVdiv[0] == scale[0]) || (c.TimingVdiv[1] > 0 && c.TimingVdiv[1] == scale[1]))
}
