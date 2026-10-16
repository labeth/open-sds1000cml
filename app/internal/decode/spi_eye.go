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
// not tell bit k settling late from bit k+1 early when the middle lies past
// half a period (the bench's data changes 0.19 of a period before the other
// edge; a flash read at 104 MHz settles 0.6 to 0.8 of a period late); the
// bursts' ends and starts do (SPIEyeVotes). Late reads the middle after the
// edge (Late true), early before it; Resolving says the bursts have not
// told yet and the reading is the usual one (Resolve).
type SPIEye struct {
	DelayS, MarginS, PeriodS float64
	Transitions              int
	Late, Resolving          bool
	// MiddleS is the middle as a phase after the edge (0 to a period); Ends
	// the bursts whose end the record holds, Launched those whose data
	// changed in the period after the middle following their last edge (a
	// late device's launch on the edge after it); Starts the bursts whose
	// start the record holds after an idle clock, Ahead those whose data
	// changed in the period before the middle a period ahead of their first
	// edge (an early device's first bit, set up before the clock runs).
	MiddleS                       float64
	Ends, Launched, Starts, Ahead int
}

// SPIEyeVotes weighs the bursts' ends and starts seen over records for a
// late reading (Late) or an early one (Early). A late device launches once
// more on the edge after a burst's last sampling edge, and a flash reading
// on changes its data there about every other burst: a launch votes late by
// one, an end without one early by a quarter (as likely as not for the
// flash, never one for an early device: a launch is four times the
// evidence). An early device sets its first bit up before the clock runs,
// so its data changes about a period ahead of the first edge where a late
// device's line idles: that votes early by one; a start without it votes
// neither way (a host whose first bit is the idle level, as a read
// command's 0 often is, shows none either way). Each vote keeps
// spiEyeKeep of the votes before it, so the reading follows the bus's
// recent bursts rather than the first ones; a clock period changed by more
// than 3% starts them over (another bus, or another rate).
type SPIEyeVotes struct {
	Late, Early, PeriodS float64
}

// spiEyeKeep is the share of the votes a vote keeps (about the last ten
// weigh), spiEyeLead the lead one reading needs to win: a launch, or four
// ends without one; spiEyeQuiet an end without a launch's vote. spiLateMaxS is the latest a device's data settles after the edge
// launching it that the reading takes with no burst seen (the data delay
// row's reach: a flash read at 104 MHz settles 6 to 8 ns late).
const (
	spiEyeKeep  = 0.9
	spiEyeLead  = 1.0
	spiEyeQuiet = 0.25
	spiLateMaxS = 9e-9
)

// Add counts a record's ends and starts.
// TRLC-LINKS: REQ-SDS-230
func (v *SPIEyeVotes) Add(e SPIEye) {
	if e.PeriodS <= 0 {
		return
	}
	if v.PeriodS > 0 && math.Abs(e.PeriodS-v.PeriodS) > 0.03*v.PeriodS {
		*v = SPIEyeVotes{}
	}
	k := math.Pow(spiEyeKeep, float64(e.Ends+e.Ahead))
	v.Late = v.Late*k + float64(e.Launched)
	v.Early = v.Early*k + float64(e.Ahead) + spiEyeQuiet*float64(e.Ends-e.Launched)
	v.PeriodS = e.PeriodS
}

// Resolve reads the middle after the edge or before it from the votes (v):
// with the middle in the first half period after the edge the data settles
// that long after the other edge launches it, and no burst is needed. Past
// half a period, the votes decide once one reading leads by spiEyeLead.
// Until then (no end or start seen, as at 100 ns/div where a record holds a
// burst's start and middle only, or the votes alike) the reading is
// Resolving: the middle after the edge when it lies within spiLateMaxS, as
// a device's data is valid a t_V after the edge launching it (0 < t_V <= a
// period), and the middle before the edge means data changing ahead of
// that edge, which only a skew between the lines gives (the bench's clock
// buffer); a middle later than any device's t_V is the data changing at
// the other edge with such a skew (a host's data at a few MHz), read
// before the edge.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func (e SPIEye) Resolve(v SPIEyeVotes) SPIEye {
	e.DelayS, e.Late, e.Resolving = e.MiddleS, false, false
	if e.MiddleS < e.PeriodS/2 {
		return e
	}
	switch d := v.Late - v.Early; {
	case d >= spiEyeLead:
		e.Late = true
	case d <= -spiEyeLead:
	default:
		e.Late, e.Resolving = e.MiddleS <= spiLateMaxS, true
	}
	if !e.Late {
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
	// the bursts' ends and starts, the clock idle three periods after or
	// before them: the data's transitions sit half a period from the middle,
	// so a late device's launch on the edge after a burst's last sampling
	// edge changes the data in the period after the middle following it, and
	// an early device's first bit, set up before the clock runs, in the
	// period before the middle a period ahead of the first edge
	in := func(from, to float64) bool {
		i := sort.SearchFloat64s(dataTimes, from)
		return i < len(dataTimes) && dataTimes[i] < to
	}
	eye := SPIEye{MiddleS: centre * period * sampleS, MarginS: best / 2 * period * sampleS, PeriodS: period * sampleS, Transitions: len(phases)}
	for k, e := range edges {
		if (k+1 < len(edges) && edges[k+1]-e >= 3*period) || (k+1 >= len(edges) && float64(len(clk))-e >= (centre+1)*period) {
			eye.Ends++
			if in(e+centre*period, e+(centre+1)*period) {
				eye.Launched++
			}
		}
		if (k > 0 && e-edges[k-1] >= 3*period) || (k == 0 && e >= 3*period) {
			eye.Starts++
			if in(e+(centre-2)*period, e+(centre-1)*period) {
				eye.Ahead++
			}
		}
	}
	var v SPIEyeVotes
	v.Add(eye)
	return eye.Resolve(v), true
}
