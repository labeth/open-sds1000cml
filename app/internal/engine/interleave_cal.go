package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// InterleaveCalibration is a measured offset calibration, not a signal filter.
// Revision 11 does not expose a converter-phase tag. Match the measured joint
// two-channel offset fingerprint to its cyclic rotation, or leave data alone.
// A mismatching input (including a strong Fs/5 signal) must not be calibrated
// by estimating and subtracting its own phase averages.
type InterleaveCalibration struct {
	Vdiv         [2]float64    `json:"vdiv"`
	VerifiedVdiv [2][]float64  `json:"verified_vdiv,omitempty"`
	Offset       [2][5]float64 `json:"offset"`
	table        [2][5][256]uint16
	tableReady   bool
}

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
func (c *InterleaveCalibration) apply(a, b []uint8, q1, q2 []uint16, scale [2]float64) bool {
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
			for phase := 0; phase < 5; phase++ {
				for v := 0; v < 256; v++ {
					corrected := math.Round((float64(v) - c.Offset[ch][phase]) * 256)
					if corrected < 0 {
						corrected = 0
					}
					if corrected > 65535 {
						corrected = 65535
					}
					c.table[ch][phase][v] = uint16(corrected)
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
		for i, v := range x {
			q[i] = c.table[ch][(i+rotation)%5][v]
			x[i] = roundQ8(q[i])
		}
	}

	return true
}

// Additional ranges must be measured explicitly. The live fingerprint and
// clipping checks still apply; this never learns offsets from the input.
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
