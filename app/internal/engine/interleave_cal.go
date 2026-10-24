// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	_ "embed"
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
	return parseInterleaveCalibration(b)
}

// TRLC-LINKS: REQ-SDS-016
func parseInterleaveCalibration(b []byte) (*InterleaveCalibration, error) {
	var c InterleaveCalibration
	if err := json.Unmarshal(b, &c); err != nil {
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

// apply corrects per-converter offsets (and, at their measured scales, gain
// and aperture) on a raw 500 MS/s record. Converter offsets do not depend on
// V/div (measured identical at 1, 0.5 and 0.1 V/div), so the offset
// correction applies at every scale. The record's converter rotation is found
// from whichever channel matches its fingerprint best — one channel carrying a
// strong signal does not block the other — and clipped samples neither vote
// nor change.
// TRLC-LINKS: REQ-SDS-016
func (c *InterleaveCalibration) apply(a, b []uint8, q1, q2 []uint16, scale [2]float64, sampleS float64) bool {
	if c == nil || len(a) < 2000 || len(a) != len(b) || len(q1) != len(a) || len(q2) != len(a) {
		return false
	}
	var means [2][5]float64
	var usable [2]bool
	for ch, x := range [][]uint8{a, b} {
		sums, count := phaseSums(x)
		usable[ch] = true
		avg := 0.0
		for i := 0; i < 5; i++ {
			if count[i] < len(x)/10 {
				usable[ch] = false
				break
			}
			means[ch][i] = float64(sums[i]) / float64(count[i])
			avg += means[ch][i] / 5
		}
		for i := 0; i < 5; i++ {
			means[ch][i] -= avg
		}
	}
	rotation, found := 0, false
	bestFit := math.Inf(1)
	for ch := 0; ch < 2; ch++ {
		if !usable[ch] {
			continue
		}
		best, second, r0 := math.Inf(1), math.Inf(1), 0
		for r := 0; r < 5; r++ {
			score := 0.0
			for i := 0; i < 5; i++ {
				d := means[ch][i] - c.Offset[ch][(i+r)%5]
				score += d * d / 5
			}
			if score < best {
				second, best, r0 = best, score, r
			} else if score < second {
				second = score
			}
		}
		if best <= .5 && second-best >= 1 && best < bestFit {
			rotation, found, bestFit = r0, true, best
		}
	}
	if !found {
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
					// Clipped samples neither vote nor change.
					c.table[ch][mode][phase][0] = 0
					c.table[ch][mode][phase][255] = 255 << 8
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
		correctPhases(x, q[:len(x)], &c.table[ch][mode], rotation%5)
		if c.TimingVdiv[ch] > 0 && scale[ch] == c.TimingVdiv[ch] && sampleS == 2e-9 {
			correctAperture(q, x, c.TimingNs[ch], rotation)
		}
	}

	return true
}

// phaseSums is each converter phase's sum and count of unclipped codes in
// x (phase i holds samples i, i+5, …). Five samples a turn, a phase each, so
// there is no phase counter (and no i%5: no divider on this ARM); a clipped
// code is added and taken back, which is rare.
// TRLC-LINKS: REQ-SDS-016
//
//go:noinline
func phaseSums(x []uint8) (sums [5]uint32, count [5]int) {
	var s0, s1, s2, s3, s4 uint32
	var clip [5]int
	turns := 0
	for len(x) >= 5 {
		a, b, c, d, e := uint32(x[0]), uint32(x[1]), uint32(x[2]), uint32(x[3]), uint32(x[4])
		s0, s1, s2, s3, s4 = s0+a, s1+b, s2+c, s3+d, s4+e
		if a-1 >= 254 || b-1 >= 254 || c-1 >= 254 || d-1 >= 254 || e-1 >= 254 {
			for k, v := range [5]uint32{a, b, c, d, e} {
				if v-1 >= 254 {
					clip[k]++
					sums[k] -= v
				}
			}
		}
		turns++
		x = x[5:]
	}
	sums[0] += s0
	sums[1] += s1
	sums[2] += s2
	sums[3] += s3
	sums[4] += s4
	for k := range count {
		count[k] = turns - clip[k]
	}
	for k, v := range x {
		if v != 0 && v != 255 {
			sums[k] += uint32(v)
			count[k]++
		}
	}
	return sums, count
}

// correctPhases looks each code of x up in its phase's table (phase rot
// first) into q, and rounds it back into x. Five samples a turn, a table each;
// the tables map clipped codes to themselves.
// TRLC-LINKS: REQ-SDS-016
//
//go:noinline
func correctPhases(x []uint8, q []uint16, tbl *[5][256]uint16, rot int) {
	var t [5]*[256]uint16
	for k := range t {
		t[k] = &tbl[rot]
		if rot++; rot == 5 {
			rot = 0
		}
	}
	t0, t1, t2, t3, t4 := t[0], t[1], t[2], t[3], t[4]
	for len(x) >= 5 && len(q) >= 5 {
		a, b, c, d, e := t0[x[0]], t1[x[1]], t2[x[2]], t3[x[3]], t4[x[4]]
		q[0], q[1], q[2], q[3], q[4] = a, b, c, d, e
		x[0], x[1], x[2], x[3], x[4] = roundQ8(a), roundQ8(b), roundQ8(c), roundQ8(d), roundQ8(e)
		x, q = x[5:], q[5:]
	}
	for k := range x {
		q[k] = t[k][x[k]]
		x[k] = roundQ8(q[k])
	}
}

// supportsScale reports whether both channels sit at a measured scale. Offset
// correction no longer needs it; it remains for range bookkeeping.
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

// defaultInterleave is this unit's converter offsets, measured on
// the inverted-sample images (300 raw 500 MS/s records, quiet CH1 fixing each
// record's rotation; CH2 averaged under an asynchronous 10 MHz square).
//
//go:embed interleave_default.json
var defaultInterleave []byte

// DefaultInterleaveCalibration returns the built-in offset calibration.
// TRLC-LINKS: REQ-SDS-016
func DefaultInterleaveCalibration() (*InterleaveCalibration, error) {
	return parseInterleaveCalibration(defaultInterleave)
}

// debiasEnvelope removes the converter offsets from peak-detect extremes.
// Envelope buckets hold (min, max) pairs taken by the FPGA over raw samples of
// all five converters, so a bucket's maximum is the signal plus the highest
// converter offset and its minimum the signal plus the lowest: on CH1 a flat
// 3.28 V top reads 3.52 V and the base -0.24 V, a 0.48 V amplitude error at
// 1 V/div. Minima drop the lowest offset, maxima the
// highest; rail values stay. q receives the result in Q8.8 (fromQ: q already
// holds the samples), and sig its rounding.
// TRLC-LINKS: REQ-SDS-016, REQ-SDS-010
func (c *InterleaveCalibration) debiasEnvelope(ch int, sig []uint8, q []uint16, fromQ bool) {
	off := c.envelopeOffsets(ch)
	q = q[:len(sig)]
	if fromQ { // the source chosen once, not per sample
		for i, x := range q {
			q[i] = uint16(debiasQ(int(x), off[i&1]))
			sig[i] = roundQ8(q[i])
		}
		return
	}
	for i, v := range sig {
		q[i] = uint16(debiasQ(int(v)<<8, off[i&1]))
		sig[i] = roundQ8(q[i])
	}
}

// envelopeOffsets are the lowest and highest converter offsets of channel ch
// in Q8.8, which an envelope bucket's minimum and maximum carry
// (debiasEnvelope). Q8.8 integers, not float per sample: they run on every
// live envelope and every word of a software-decoded stream.
// TRLC-LINKS: REQ-SDS-016, REQ-SDS-010
func (c *InterleaveCalibration) envelopeOffsets(ch int) [2]int {
	lo, hi := c.Offset[ch&1][0], c.Offset[ch&1][0]
	for _, v := range c.Offset[ch&1] {
		lo, hi = min(lo, v), max(hi, v)
	}
	return [2]int{int(math.Round(lo * 256)), int(math.Round(hi * 256))}
}

// debiasQ removes offset off from the Q8.8 extreme x; rails (within half a
// code) stay.
// TRLC-LINKS: REQ-SDS-016, REQ-SDS-010
func debiasQ(x, off int) int {
	if x > 128 && x < 254*256+128 {
		x = min(max(x-off, 0), 255*256)
	}
	return x
}
