// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// I2CCfg / SPICfg configure those decoders.
// TRLC-LINKS: REQ-SDS-018
type I2CCfg struct {
	Inverted  bool
	Format    string
	Threshold float64
	HaveThr   bool
}

// TRLC-LINKS: REQ-SDS-018
type SPICfg struct {
	Inverted   bool
	CPOL, CPHA bool
	MSB        bool // bit order; true = MSB-first (default)
	Format     string
	Threshold  float64
	HaveThr    bool
	// DataLag is how far the data line's samples sit before the clock
	// line's, in samples: under six samples a clock period the data line is
	// taken at the clock's interpolated crossing plus DataLag (the channels'
	// sampling phase, C2 0.72 samples after C1 at 2 ns).
	DataLag float64
	// DataDelay, when set, takes the data line that many samples after the
	// clock's interpolated crossing (plus DataLag) at any samples a period:
	// where a device's data settles late (MeasureSPIEye), as the FPGA's
	// receiver on the samples takes it with a delay.
	DataDelay float64
}

// i2cGlitch is the longest run an I2C line holds as a spike: an eighth of
// SCL's middle rise-to-rise gap (edges its edges), in samples. A bit holds
// SDA a whole clock and SCL near half of one; the bus's devices filter out
// spikes (50 ns in Fast-mode) that a noisy line otherwise reads as a START
// and a STOP while SCL is high.
// TRLC-LINKS: REQ-SDS-018
func i2cGlitch(edges []edge) int {
	var gaps []int
	last := -1
	for _, e := range edges {
		if e.dir > 0 {
			if last >= 0 {
				gaps = append(gaps, e.i-last)
			}
			last = e.i
		}
	}
	if len(gaps) == 0 {
		return 0
	}
	sort.Ints(gaps)
	return gaps[len(gaps)/2] / 8
}

// i2cDeglitch gives a line's runs shorter than g samples, between two runs
// of the other level, the level before them.
// TRLC-LINKS: REQ-SDS-018
func i2cDeglitch(level []int8, g int) {
	if g < 2 {
		return
	}
	for i := 0; i < len(level); {
		j := i
		for j < len(level) && level[j] == level[i] {
			j++
		}
		if i > 0 && j < len(level) && j-i < g && level[i-1] >= 0 && level[j] >= 0 && level[i] >= 0 {
			for k := i; k < j; k++ {
				level[k] = level[i-1]
			}
		}
		i = j
	}
}

// DecodeI2C decodes I2C given SCL + SDA codes (decode.js decodeI2C).
// TRLC-LINKS: REQ-SDS-018
func DecodeI2C(scl, sda []uint8, colTimeS float64, cfg I2CCfg) Result {
	if cfg.Inverted {
		scl, sda = invertLogic(scl), invertLogic(sda)
		if cfg.HaveThr {
			cfg.Threshold = 255 - cfg.Threshold
		}
	}
	CL := sliceChannel(scl, cfg.Threshold, cfg.HaveThr)
	DA := sliceChannel(sda, cfg.Threshold, cfg.HaveThr)
	if !CL.ok {
		return Result{Proto: "i2c", Error: "SCL " + CL.reason}
	}
	if !DA.ok {
		return Result{Proto: "i2c", Error: "SDA " + DA.reason}
	}
	n := CL.n
	if DA.n < n {
		n = DA.n
	}
	risings := 0
	for _, e := range CL.edges {
		if e.dir > 0 && e.i < n {
			risings++
		}
	}
	if risings < 2 {
		return Result{Proto: "i2c", Error: "no SCL clock edges"}
	}
	clEdges := make([]edge, 0, len(CL.edges))
	for _, e := range CL.edges {
		if e.i < n {
			clEdges = append(clEdges, e)
		}
	}
	colsPerClock := minEdgeGap(clEdges, func(d int) bool { return d > 0 })
	if colsPerClock < 3 {
		return Result{Proto: "i2c", Error: fmt.Sprintf("%.1f cols/clock; too few samples/bit", colsPerClock)}
	}
	var spans []Span
	var bytes []int
	var toks []string
	cl, da := -1, -1
	inTxn, expectAddr := false, false
	bitCount, val, bitStart := 0, 0, 0
	clLevel, daLevel := CL.levels(), DA.levels()
	g := i2cGlitch(clEdges)
	i2cDeglitch(clLevel, g)
	i2cDeglitch(daLevel, g)
	for i := 0; i < n; i++ {
		l, d := int(clLevel[i]), int(daLevel[i])
		pcl, pda := cl, da
		cl, da = l, d
		if pcl < 0 || pda < 0 {
			continue
		}
		if cl == 1 && pda == 1 && da == 0 { // START
			spans = append(spans, Span{i, i, "S", "start", 0})
			toks = append(toks, "START")
			inTxn, expectAddr, bitCount, val = true, true, 0, 0
			continue
		}
		if cl == 1 && pda == 0 && da == 1 { // STOP
			spans = append(spans, Span{i, i, "P", "stop", 0})
			toks = append(toks, "STOP")
			inTxn, bitCount = false, 0
			continue
		}
		if pcl == 0 && cl == 1 && inTxn { // sample on SCL rising
			bit := logicAt(&DA, float64(i))
			if bit < 0 {
				continue
			}
			if bitCount < 8 {
				if bitCount == 0 {
					bitStart, val = i, 0
				}
				val = (val << 1) | bit
				bitCount++
				if bitCount == 8 {
					if expectAddr {
						addr := val >> 1
						spans = append(spans, Span{bitStart, i, hex2(addr), "addr", addr})
						rw := "W"
						if val&1 != 0 {
							rw = "R"
						}
						spans = append(spans, Span{i, i, rw, "rw", 0})
						toks = append(toks, hex2(addr), rw)
						expectAddr = false
					} else {
						spans = append(spans, Span{bitStart, i, FmtByte(val, cfg.Format), "data", val})
						toks = append(toks, FmtByte(val, cfg.Format))
						bytes = append(bytes, val)
					}
				}
			} else { // 9th clock = ACK/NAK
				k, t := "ack", "A"
				if bit != 0 {
					k, t = "nak", "N"
				}
				spans = append(spans, Span{i, i, t, k, 0})
				if bit == 0 {
					toks = append(toks, "ACK")
				} else {
					toks = append(toks, "NAK")
				}
				bitCount, val = 0, 0
			}
		}
	}
	if inTxn {
		spans = append(spans, Span{n - 1, n - 1, "(no STOP)", "frame-error", 0})
	}
	return Result{OK: true, Proto: "i2c", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytes, SPB: colsPerClock, Thr: CL.threshold}
}

// DecodeSPI decodes SPI given CLK + DATA codes, no chip-select (decode.js decodeSPI).
// TRLC-LINKS: REQ-SDS-018
func DecodeSPI(clk, data []uint8, colTimeS float64, cfg SPICfg) Result {
	if cfg.Inverted {
		clk, data = invertLogic(clk), invertLogic(data)
		if cfg.HaveThr {
			cfg.Threshold = 255 - cfg.Threshold
		}
	}
	CK := sliceChannel(clk, cfg.Threshold, cfg.HaveThr)
	DA := sliceChannel(data, cfg.Threshold, cfg.HaveThr)
	if !CK.ok {
		return Result{Proto: "spi", Error: "CLK " + CK.reason}
	}
	if !DA.ok {
		return Result{Proto: "spi", Error: "DATA " + DA.reason}
	}
	n := CK.n
	if DA.n < n {
		n = DA.n
	}
	eIn := make([]edge, 0, len(CK.edges))
	for _, e := range CK.edges {
		if e.i < n {
			eIn = append(eIn, e)
		}
	}
	if len(eIn) < 2 {
		return Result{Proto: "spi", Error: "no CLK edges"}
	}
	cpol, cpha := 0, 0
	if cfg.CPOL {
		cpol = 1
	}
	if cfg.CPHA {
		cpha = 1
	}
	sampleRising := cpol == cpha
	// Frame-split threshold from the SAMPLING-edge cadence — the edges actually
	// used for bits — never from the minimum gap over ALL edges. On a real
	// (rebuilt) clock a single narrow half-cycle (partial first cycle, duty skew)
	// makes min-gap*3 land BELOW one true period (HW: sampling gaps 374-376 cols
	// vs 3*124=372), so every inter-bit gap "exceeded" the reset and the byte
	// assembly restarted on each bit — 0 bytes from a clean signal. Cluster the
	// sampling-edge gaps like the UART/Manchester inference: seed on a low
	// percentile, average the cluster (±0.5 window absorbs quantization/jitter),
	// and reset at 1.5x the TYPICAL clock period.
	dirWant := -1
	if sampleRising {
		dirWant = 1
	}
	sgaps := make([]float64, 0, len(eIn)/2+1)
	prevI := -1
	for _, e := range eIn {
		if e.dir != dirWant {
			continue
		}
		if prevI >= 0 && e.i > prevI {
			sgaps = append(sgaps, float64(e.i-prevI))
		}
		prevI = e.i
	}
	if len(sgaps) == 0 {
		return Result{Proto: "spi", Error: "no CLK sampling edges"}
	}
	sort.Float64s(sgaps)
	period := sgaps[int(float64(len(sgaps))*0.1)]
	sum, cnt := 0.0, 0
	for _, g := range sgaps {
		if math.Abs(g-period) <= 0.5*period {
			sum += g
			cnt++
		}
	}
	if cnt > 0 {
		period = sum / float64(cnt)
	}
	// Under six samples a clock period (past about 80 MHz at 2 ns) the first
	// sample past the clock's crossing lies up to a third of a bit late: the
	// data line is taken at the crossing itself, interpolated between the two
	// samples around the threshold, as the FPGA's receiver on the samples
	// takes it (spi_sample.go). Only on the converters' own samples (2 ns or
	// finer): decimated samples are filtered across the edges, and a capture
	// at the full rate reads the bus.
	fine := period < 6 && colTimeS <= 2.05e-9
	if period < 6 && !fine || period < 3.5 {
		return Result{Proto: "spi", Error: fmt.Sprintf("%.1f cols/clock; too few samples/bit", period)}
	}
	gapReset := 1.5 * period
	var spans []Span
	var bytes []int
	var toks []string
	ck := -1
	bitCount, val, bitStart, lastSample := 0, 0, 0, -1
	halfAmp := DA.amp / 2
	if halfAmp <= 0 {
		halfAmp = 1
	}
	var mSum float64
	mN := 0
	// Under six samples a clock period a sampled high or low half can stop
	// short of the hysteresis band (the samples fall either side of its peak:
	// 36 69 68 47 against a band to 69.1), and the band would drop that bit.
	// There the clock's edges are the FPGA receiver's: each direction's
	// crossing armed by a sample beyond the opposite band (sample_edges.v).
	var armedRise, armedFall []bool
	if fine {
		armedRise, armedFall = armedCrossings(CK, n)
	}
	ckLevel := CK.levels()
	for i := 0; i < n; i++ {
		l := int(ckLevel[i])
		pck := ck
		ck = l
		if pck < 0 {
			continue
		}
		rising := pck == 0 && ck == 1
		falling := pck == 1 && ck == 0
		if fine {
			rising, falling = armedRise[i], armedFall[i]
		}
		if (sampleRising && rising) || (!sampleRising && falling) {
			at := float64(i)
			if fine || cfg.DataDelay != 0 {
				at = crossingAt(CK, i, rising) + cfg.DataLag + cfg.DataDelay
			}
			bit := logicAt(&DA, at)
			if bit < 0 {
				continue
			}
			mSum += math.Min(1, math.Abs(float64(DA.codes[int(math.Round(at))])-DA.threshold)/halfAmp)
			mN++
			if lastSample >= 0 && float64(i-lastSample) > gapReset && bitCount > 0 {
				bitCount, val = 0, 0 // idle gap → new frame
			}
			lastSample = i
			if bitCount == 0 {
				bitStart, val = i, 0
			}
			if cfg.MSB {
				val = (val << 1) | bit
			} else {
				val |= bit << bitCount
			}
			bitCount++
			if bitCount == 8 {
				spans = append(spans, Span{bitStart, i, FmtByte(val, cfg.Format), "data", val & 0xff})
				toks = append(toks, FmtByte(val, cfg.Format))
				bytes = append(bytes, val&0xff)
				bitCount, val = 0, 0
			}
		}
	}
	margin := 0.0
	if mN > 0 {
		margin = mSum / float64(mN)
	}
	return Result{OK: true, Proto: "spi", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytes, SPB: period, Thr: CK.threshold, Margin: margin}
}

// armedCrossings marks a sliced line's crossings as the FPGA's receiver on
// the samples takes them: a rising one at the first sample at or past the
// threshold after a sample at or below the band's low edge, a falling one at
// the first sample below it after one at or above the high edge. Each
// direction is armed on its own, so a half-period whose samples miss the far
// edge of the band still gives the next crossing.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-229
func armedCrossings(s sliced, n int) (rise, fall []bool) {
	rise, fall = make([]bool, n), make([]bool, n)
	upArmed, downArmed := false, false
	for i := 0; i < n; i++ {
		c := float64(s.codes[i])
		if upArmed && c >= s.threshold {
			rise[i], upArmed = true, false
		}
		if downArmed && c < s.threshold {
			fall[i], downArmed = true, false
		}
		if c <= s.thLo {
			upArmed = true
		}
		if c >= s.thHi {
			downArmed = true
		}
	}
	return rise, fall
}

// crossingAt is where a sliced line crossed its threshold before the sample
// i its level changed on, in samples: linear between the two samples around
// the threshold.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-229
func crossingAt(s sliced, i int, rising bool) float64 {
	past := func(j int) bool { return (float64(s.codes[j]) >= s.threshold) == rising }
	j := i
	for j > 0 && past(j-1) {
		j--
	}
	if j == 0 || !past(j) {
		return float64(i)
	}
	a, b := float64(s.codes[j-1]), float64(s.codes[j])
	return float64(j-1) + (s.threshold-a)/(b-a)
}
