// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math"
	"strconv"
	"strings"
)

// Motion signals read as protocols: a quadrature (Gray code) encoder's A and
// B lines, a stepper driver's STEP and DIR lines, and a PWM line's periods.
// Each unit is one step (one encoder cycle, one STEP pulse) or one PWM
// period, so the symbol trigger, which sees one bit per such unit, matches
// what the decode lists. Mirrored step for step by decode_motion.js.

// LineCfg configures the single-line decoders beyond the first thirty:
// Baud is a rate the decoder would otherwise measure (0: measured) and
// Variant a sub-protocol the line may carry (the IR multi decoder's
// protocol, a frame length).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
type LineCfg struct {
	Threshold float64
	HaveThr   bool
	Format    string
	Baud      int
	Variant   int
}

// medianOf is the middle of a list of lengths (the upper middle of an even
// count), 0 for none; it sorts a copy.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func medianOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return nthValue(v, len(v)/2)
}

// stepPeriod is a stepping line's median time between rising edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func stepPeriod(A sliced) float64 {
	var per []float64
	last := -1.0
	for _, e := range A.edges {
		if e.dir > 0 {
			if last >= 0 {
				per = append(per, e.x-last)
			}
			last = e.x
		}
	}
	return medianOf(per)
}

// DecodeQuadrature decodes a quadrature encoder (sigrok's graycode on two
// lines): the Gray code phase of A (bit 0) and B (bit 1) counts up when A
// leads B and down when B leads, one count an edge; a phase jump of two (both
// lines at once) counts nothing and is flagged ("!"). Each rising edge of A
// is a unit: forward (1) when B is low there, "F" or "R" and the count. A
// unit after A rested two cycles or more opens a motion ("sof"). Baud is the
// cycles a second, SPB a cycle in samples. OK needs two cycles.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func DecodeQuadrature(a, b []uint8, colTimeS float64, cfg ClockedCfg) Result {
	return decodeQuadrature(samplesIn(a), samplesIn(b), colTimeS, cfg)
}

// DecodeQuadratureEdges is DecodeQuadrature over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func DecodeQuadratureEdges(a, b *EdgeLine, colTimeS float64, cfg ClockedCfg) Result {
	return decodeQuadrature(edgesIn(a), edgesIn(b), colTimeS, cfg)
}

// decodeQuadrature is DecodeQuadrature over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func decodeQuadrature(a, b lineIn, colTimeS float64, cfg ClockedCfg) Result {
	return decodeSteps("quad", a, b, colTimeS, cfg, true)
}

// DecodeStepper decodes a stepper driver's STEP (line a) and DIR (line b)
// lines (sigrok's stepper_motor): each rising edge of STEP is a step, up
// (1, "F") with DIR high and down ("R") with it low, and the position after
// it; a step after STEP rested two step periods or more opens a motion
// ("sof"). Baud is the steps a second, Feature the shortest STEP pulse,
// Cover DIR's edges a step. OK needs two steps.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func DecodeStepper(a, b []uint8, colTimeS float64, cfg ClockedCfg) Result {
	return decodeStepper(samplesIn(a), samplesIn(b), colTimeS, cfg)
}

// DecodeStepperEdges is DecodeStepper over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func DecodeStepperEdges(a, b *EdgeLine, colTimeS float64, cfg ClockedCfg) Result {
	return decodeStepper(edgesIn(a), edgesIn(b), colTimeS, cfg)
}

// decodeStepper is DecodeStepper over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func decodeStepper(a, b lineIn, colTimeS float64, cfg ClockedCfg) Result {
	return decodeSteps("stepper", a, b, colTimeS, cfg, false)
}

// decodeSteps is DecodeQuadrature (quad) and DecodeStepper.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func decodeSteps(proto string, a, b lineIn, colTimeS float64, cfg ClockedCfg, quad bool) Result {
	if b.none() {
		return Result{Proto: proto, Error: "needs both lines"}
	}
	A := a.slice(cfg.Threshold, cfg.HaveThr)
	if !A.ok {
		return Result{Proto: proto, Error: A.reason}
	}
	B := b.slice(cfg.ThresholdB, cfg.HaveThrB)
	if !B.ok && quad {
		return Result{Proto: proto, Error: B.reason}
	}
	// A DIR line that never changes reads against line a's threshold (the
	// same probe scale): a motion one way only.
	dirFlat := 0
	if !B.ok {
		sum := b.sum()
		if b.samples() > 0 && float64(sum)/float64(b.samples()) >= A.threshold {
			dirFlat = 1
		}
	}
	T := stepPeriod(A)
	if !(T > 0) {
		return Result{Proto: proto, Error: "too few steps"}
	}
	n := A.n
	var spans []Span
	var toks []string
	var units []int
	count := 0
	la := int(A.first)
	lb := 0
	if B.ok {
		lb = int(B.first)
	}
	phase := func() int { g := la | lb<<1; return g ^ g>>1 }
	ph := phase()
	prevA := 0.0 // the last edge of A (the record's start first)
	open := false
	pulse := math.Inf(1) // the shortest STEP high
	riseX := -1.0
	lastA := -1.0      // the last edge of A, for B's place in its half cycle
	bMid, bAll := 0, 0 // B's edges, those a quarter cycle from A's
	ia, ib := 0, 0
	steps := 0
	for ia < len(A.edges) || (quad && ib < len(B.edges)) {
		useA := ia < len(A.edges) && (!quad || ib >= len(B.edges) || A.edges[ia].x <= B.edges[ib].x)
		if !useA {
			e := B.edges[ib]
			ib++
			lb = 0
			if e.dir > 0 {
				lb = 1
			}
			if lastA >= 0 {
				bAll++
				if f := (e.x - lastA) / (T / 2); f >= 0.25 && f <= 0.75 {
					bMid++
				}
			}
			np := phase()
			d := ((np-ph+1)%4+4)%4 - 1
			ph = np
			if d == 2 {
				spans = append(spans, pspan(e.x, e.x, n, "!", "frame-error", 0))
				toks = append(toks, "!")
				continue
			}
			count += d
			continue
		}
		e := A.edges[ia]
		ia++
		if e.x-prevA >= 2*T {
			open = true
		}
		if e.dir > 0 {
			riseX = e.x
		} else if riseX >= 0 {
			pulse = math.Min(pulse, e.x-riseX)
		}
		lastA = e.x
		prevA = e.x
		la = 0
		if e.dir > 0 {
			la = 1
		}
		if quad {
			np := phase()
			d := ((np-ph+1)%4+4)%4 - 1
			ph = np
			if d == 2 {
				spans = append(spans, pspan(e.x, e.x, n, "!", "frame-error", 0))
				toks = append(toks, "!")
				continue
			}
			count += d
		}
		if e.dir < 0 {
			continue
		}
		fwd := false
		if quad {
			fwd = lb == 0
		} else {
			fwd = dirFlat == 1
			if B.ok {
				fwd = logicAt(&B, e.x) == 1
			}
			if fwd {
				count++
			} else {
				count--
			}
		}
		if open {
			spans = append(spans, pspan(e.x, e.x, n, "", "sof", 0))
			open = false
		}
		v, txt := 0, "R"
		if fwd {
			v, txt = 1, "F"
		}
		txt += strconv.Itoa(count)
		spans = append(spans, pspan(e.x, e.x+T, n, txt, "data", v))
		toks = append(toks, txt)
		units = append(units, v)
		steps++
	}
	res := Result{OK: steps >= 2, Proto: proto, Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: A.threshold, SPB: T, Baud: int(math.Round(1 / (T * colTimeS)))}
	if !quad && !math.IsInf(pulse, 1) {
		res.Feature = pulse
	}
	switch {
	case quad && bAll > 0 && 10*len(B.edges) >= 9*len(A.edges):
		// B changes once for each of A's edges, a quarter cycle after it
		res.Cover = float64(bMid) / float64(bAll)
	case !quad && B.ok && steps > 0:
		res.Cover = float64(len(B.edges)) / float64(steps) // DIR's changes a step
	}
	if !res.OK {
		res.Error = "too few steps"
	}
	return res
}

// DecodePWM decodes a PWM line (sigrok's pwm): each period from a rising edge
// to the next ("sof" at its start) is a unit, its duty cycle as a byte
// (the high time over the period times 256, at most 255) and in tenths of a
// percent. Baud is the frequency, SPB the period in samples. OK needs two
// periods.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func DecodePWM(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	return decodePWM(samplesIn(codes), colTimeS, cfg)
}

// DecodePWMEdges is DecodePWM over digital lines' edges (EdgeLine).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func DecodePWMEdges(codes *EdgeLine, colTimeS float64, cfg LineCfg) Result {
	return decodePWM(edgesIn(codes), colTimeS, cfg)
}

// decodePWM is DecodePWM over samples or edges.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func decodePWM(codes lineIn, colTimeS float64, cfg LineCfg) Result {
	S := codes.slice(cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "pwm", Error: S.reason}
	}
	n := S.n
	E := S.edges
	periods := len(E)/2 + 1 // a period from each rising edge
	spans := make([]Span, 0, 2*periods)
	toks := make([]string, 0, periods)
	units := make([]int, 0, periods)
	pers := make([]float64, 0, periods)
	narrow := math.Inf(1) // the narrowest high or low
	for k := 0; k < len(E); k++ {
		if E[k].dir < 0 || k+2 >= len(E) {
			continue
		}
		r0, f, r1 := E[k].x, E[k+1].x, E[k+2].x
		per := r1 - r0
		ratio := (f - r0) / per
		v := min(255, int(ratio*256))
		d10 := int(math.Floor(ratio*1000 + 0.5))
		txt := strconv.Itoa(d10/10) + "." + strconv.Itoa(d10%10) + "%"
		spans = append(spans, pspan(r0, r0, n, "", "sof", 0), pspan(r0, r1, n, txt, "data", v))
		toks = append(toks, txt)
		units = append(units, v)
		pers = append(pers, per)
		narrow = math.Min(narrow, math.Min(f-r0, r1-f))
	}
	if len(units) < 2 {
		return Result{Proto: "pwm", Error: "too few periods"}
	}
	T := medianOf(pers)
	return Result{OK: true, Proto: "pwm", Spans: spans, Text: strings.Join(toks, " "), Bytes: units,
		Thr: S.threshold, SPB: T, Baud: int(math.Round(1 / (T * colTimeS))), Feature: narrow}
}
