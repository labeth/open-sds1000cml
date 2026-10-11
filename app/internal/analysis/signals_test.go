// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import "math"

// Deterministic test signals with ground truth for the eye and jitter tests:
// NRZ waveforms built from an explicit edge list, so injected jitter really
// moves the edges, with band-limited (logistic) edges, Gaussian noise and
// optional ringing, sampled at integer sample times.

// rng is a 64-bit LCG with a Box-Muller Gaussian (deterministic per seed).
// TRLC-LINKS: REQ-SDS-215
type rng struct{ s uint64 }

// TRLC-LINKS: REQ-SDS-215
func (r *rng) uniform() float64 {
	r.s = r.s*6364136223846793005 + 1442695040888963407
	return float64(r.s>>11) / (1 << 53)
}

// TRLC-LINKS: REQ-SDS-215
func (r *rng) gauss() float64 {
	u := math.Max(r.uniform(), 1e-300)
	return math.Sqrt(-2*math.Log(u)) * math.Cos(2*math.Pi*r.uniform())
}

// edgeAt is one transition of a waveform at sample time t; up is its direction.
// TRLC-LINKS: REQ-SDS-215
type edgeAt struct {
	t  float64
	up bool
}

// wave describes how edges become samples.
// TRLC-LINKS: REQ-SDS-215
type wave struct {
	n         int     // samples
	lo, hi    float64 // levels (codes)
	startHigh bool    // the level before the first edge
	rise      float64 // 10-90 % edge time in samples; 0 = an ideal step
	noise     float64 // Gaussian sigma (codes)
	ring      float64 // ringing amplitude as a fraction of the swing
	ringPer   float64 // ringing period (samples)
	ringTau   float64 // ringing decay (samples)
	frac      float64 // added to every code after rounding (fractional Q8.8 codes)
	seed      uint64
	unrounded bool // keep the codes as floats instead of rounding to integers
}

// render samples the waveform for an edge list.
// TRLC-LINKS: REQ-SDS-215
func (w wave) render(edges []edgeAt) []float64 {
	r := rng{s: w.seed*0x9e3779b97f4a7c15 + 1}
	k := w.rise / 4.394 // a logistic's 10-90 % time is 2 ln 9 k
	win := 6*w.rise + 8*w.ringTau + 2
	sw := w.hi - w.lo
	settled := 0.0
	if w.startHigh {
		settled = 1
	}
	out := make([]float64, w.n)
	e0 := 0
	for i := range out {
		x := float64(i)
		for e0 < len(edges) && x-edges[e0].t > win {
			if edges[e0].up {
				settled++
			} else {
				settled--
			}
			e0++
		}
		v := settled
		for e := e0; e < len(edges) && edges[e].t-x < win; e++ {
			dt := x - edges[e].t
			dir := 1.0
			if !edges[e].up {
				dir = -1
			}
			switch {
			case k > 0:
				v += dir / (1 + math.Exp(-dt/k))
			case dt >= 0:
				v += dir
			}
			if w.ring > 0 && dt > 0 {
				v += dir * w.ring * math.Exp(-dt/w.ringTau) * math.Sin(2*math.Pi*dt/w.ringPer)
			}
		}
		c := w.lo + sw*v + w.noise*r.gauss()
		if !w.unrounded {
			c = math.Round(c)
		}
		out[i] = math.Max(0, math.Min(255, c+w.frac))
	}
	return out
}

// prbs7 is the PRBS7 bit stream (x^7 + x^6 + 1) from the all-ones state.
// TRLC-LINKS: REQ-SDS-215
func prbs7(nbits int) []int {
	s := 0x7f
	bits := make([]int, nbits)
	for i := range bits {
		bits[i] = (s >> 6) & 1
		fb := ((s >> 6) ^ (s >> 5)) & 1
		s = ((s << 1) | fb) & 0x7f
	}
	return bits
}

// nrzEdges places the transitions of a bit stream on a grid of ui samples
// starting at phase, each moved by tie(k) samples (k = the bit it starts).
// TRLC-LINKS: REQ-SDS-215
func nrzEdges(bits []int, ui, phase float64, tie func(k int) float64) []edgeAt {
	var out []edgeAt
	for k := 1; k < len(bits); k++ {
		if bits[k] != bits[k-1] {
			off := 0.0
			if tie != nil {
				off = tie(k)
			}
			out = append(out, edgeAt{t: phase + float64(k)*ui + off, up: bits[k] > bits[k-1]})
		}
	}
	return out
}

// uartBits is the 8N1 bit stream of bytes, LSB first, each frame followed by
// idle[f % len(idle)] stop-level bits (none when idle is empty).
// TRLC-LINKS: REQ-SDS-215
func uartBits(bytes []byte, idle []int) []int {
	bits := []int{1, 1}
	for f, b := range bytes {
		bits = append(bits, 0)
		for c := 0; c < 8; c++ {
			bits = append(bits, int(b>>c)&1)
		}
		bits = append(bits, 1)
		if len(idle) > 0 {
			for j := 0; j < idle[f%len(idle)]; j++ {
				bits = append(bits, 1)
			}
		}
	}
	return append(bits, 1)
}

// asyncUART places messages of back-to-back 8N1 frames at arbitrary
// (non-integer) times: each message starts gap[m] samples after the last one
// ended, so the bit grid restarts with every message, as on a real UART.
// TRLC-LINKS: REQ-SDS-215
func asyncUART(msgs [][]byte, ui, start float64, gaps []float64) []edgeAt {
	var out []edgeAt
	t := start
	for m, msg := range msgs {
		bits := append(uartBits(msg, nil)[2:], 1) // drop the lead idle
		bits = append([]int{1}, bits...)          // one idle bit before the start bit
		out = append(out, nrzEdges(bits, ui, t-ui, nil)...)
		t += float64(len(bits)-1)*ui + gaps[m%len(gaps)]
	}
	return out
}
