package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// I2CCfg / SPICfg configure those decoders.
type I2CCfg struct {
	Format    string
	Threshold float64
	HaveThr   bool
}

type SPICfg struct {
	CPOL, CPHA bool
	MSB        bool // bit order; true = MSB-first (default)
	Format     string
	Threshold  float64
	HaveThr    bool
}

// DecodeI2C decodes I2C given SCL + SDA codes (decode.js decodeI2C).
func DecodeI2C(scl, sda []uint8, colTimeS float64, cfg I2CCfg) Result {
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
	var clEdges []edge
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
	for i := 0; i < n; i++ {
		l, d := int(CL.level[i]), int(DA.level[i])
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
			bit := logicAt(DA, float64(i))
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
func DecodeSPI(clk, data []uint8, colTimeS float64, cfg SPICfg) Result {
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
	var eIn []edge
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
	var sgaps []float64
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
	if period < 6 {
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
	for i := 0; i < n; i++ {
		l := int(CK.level[i])
		pck := ck
		ck = l
		if pck < 0 {
			continue
		}
		rising := pck == 0 && ck == 1
		falling := pck == 1 && ck == 0
		if (sampleRising && rising) || (!sampleRising && falling) {
			bit := logicAt(DA, float64(i))
			if bit < 0 {
				continue
			}
			mSum += math.Min(1, math.Abs(float64(DA.codes[i])-DA.threshold)/halfAmp)
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
