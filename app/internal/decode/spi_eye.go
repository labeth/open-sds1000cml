// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math"
	"slices"
	"sort"
)

// SPIEye is where an SPI data line's bits are steadiest after the clock's
// sampling edges, measured on a record: a device launches bit k on the clock
// edge before the sampling edge and its output settles t_V later (a flash
// read at 104 MHz: 6 to 8 ns), so bit k is valid for a period from half a
// period plus t_V before the sampling edge, and its middle is t_V after it.
// Delay is that middle (0 when data changes on the other edge), Margin half
// the opening the data's transitions leave around it. The phase alone does
// not tell bit k settling late from bit k+1 early (the bench's data changes
// 0.19 of a period before the other edge; a flash read at 104 MHz settles
// 0.6 to 0.8 of a period late): a device that settles late launches once
// more on the clock edge after a burst's last sampling edge, so its data
// changes more than half a period after it. Late reads the middle after the
// edge (Late true), early before it.
type SPIEye struct {
	DelayS, MarginS, PeriodS float64
	Transitions              int
	Late                     bool
	// MiddleS is the middle as a phase after the edge (0 to a period); Ends
	// the bursts whose end the record holds, Launched those whose data
	// changed half a period to a period and a half after their last edge.
	MiddleS        float64
	Ends, Launched int
}

// Resolve reads the middle after the edge or before it from bursts' ends
// counted over several records (ends, launched): a short record holds one
// or two.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func (e SPIEye) Resolve(ends, launched int) SPIEye {
	e.Late = e.MiddleS >= e.PeriodS/2 && launched >= 2 && 4*launched >= ends
	e.DelayS = e.MiddleS
	if e.MiddleS >= e.PeriodS/2 && !e.Late {
		e.DelayS -= e.PeriodS
	}
	return e
}

// SPIEyeMin is the fewest data transitions inside clocking an eye is read
// from.
const SPIEyeMin = 16

// MeasureSPIEye reads the eye from a record of the clock and data lines, the
// data line's samples DataLag samples before the clock line's (cfg). Each
// data transition between sampling edges a period apart gives its phase
// after the edge before it; the eye is the widest phase interval holding
// none. ok false with fewer than SPIEyeMin transitions or no clock.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-229, REQ-SDS-230
func MeasureSPIEye(clk, data []uint8, sampleS float64, cfg SPICfg) (SPIEye, bool) {
	if cfg.Inverted {
		clk, data = invertLogic(clk), invertLogic(data)
		if cfg.HaveThr {
			cfg.Threshold = 255 - cfg.Threshold
		}
	}
	CK := sliceChannel(clk, cfg.Threshold, cfg.HaveThr)
	DA := sliceChannel(data, cfg.Threshold, cfg.HaveThr)
	if !CK.ok || !DA.ok || !(sampleS > 0) {
		return SPIEye{}, false
	}
	dir := -1
	if cfg.CPOL == cfg.CPHA { // modes 0 and 3 sample on the rising edge
		dir = 1
	}
	var edges []float64
	for _, e := range CK.edges {
		if e.dir == dir {
			edges = append(edges, e.x)
		}
	}
	if len(edges) < 8 {
		return SPIEye{}, false
	}
	gaps := make([]float64, len(edges)-1)
	for i := range gaps {
		gaps[i] = edges[i+1] - edges[i]
	}
	sorted := slices.Clone(gaps)
	slices.Sort(sorted)
	seed := sorted[len(sorted)/4]
	sum, n := 0.0, 0
	for _, g := range sorted {
		if 2*g >= seed && 2*g <= 3*seed {
			sum += g
			n++
		}
	}
	period := sum / float64(n)
	var phases, dataTimes []float64
	for _, e := range DA.edges {
		at := e.x - cfg.DataLag // on the clock line's samples
		dataTimes = append(dataTimes, at)
		k := sort.SearchFloat64s(edges, at) - 1
		if k < 0 || k+1 >= len(edges) || edges[k+1]-edges[k] > 1.25*period {
			continue // outside clocking: idle, or a frame's gap
		}
		phases = append(phases, (at-edges[k])/(edges[k+1]-edges[k]))
	}
	if len(phases) < SPIEyeMin {
		return SPIEye{}, false
	}
	slices.Sort(phases)
	// the widest gap between neighbouring transition phases, round the circle
	best, from := 1+phases[0]-phases[len(phases)-1], phases[len(phases)-1]
	for i := 1; i < len(phases); i++ {
		if g := phases[i] - phases[i-1]; g > best {
			best, from = g, phases[i-1]
		}
	}
	centre := math.Mod(from+best/2, 1)
	// bursts ending at least three periods before the next: does the data
	// change between half a period and a period and a half after the last
	// sampling edge?
	ends, changed := 0, 0
	for k, e := range edges {
		if k+1 < len(edges) && edges[k+1]-e < 3*period {
			continue
		}
		if k+1 >= len(edges) && float64(len(clk))-e < 1.5*period {
			continue
		}
		ends++
		i := sort.SearchFloat64s(dataTimes, e+0.5*period)
		if i < len(dataTimes) && dataTimes[i] < e+1.5*period {
			changed++
		}
	}
	eye := SPIEye{MiddleS: centre * period * sampleS, MarginS: best / 2 * period * sampleS, PeriodS: period * sampleS, Transitions: len(phases), Ends: ends, Launched: changed}
	return eye.Resolve(ends, changed), true
}
