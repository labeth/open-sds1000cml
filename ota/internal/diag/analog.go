// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// detent is one analog V/div of the SDS1102CML+'s ladder (spec 06 §4): the
// fine-gain DAC code and the coarse range relay (true: attenuated).
type detent struct {
	vdiv  float64
	gain  uint8
	atten bool
}

// detents are the analog detents (the 2 and 5 mV/div detents are the 10 mV
// range zoomed, so the same setting).
var detents = []detent{
	{0.010, 146, false},
	{0.020, 63, false},
	{0.050, 25, false},
	{0.100, 12, false},
	{0.200, 6, false},
	{0.500, 115, true},
	{1.0, 57, true},
	{2.0, 28, true},
	{5.0, 11, true},
	{10.0, 6, true},
}

// dacSettle is how long the offset DAC's output takes to settle after a
// step: after a jump across its range 5 ms still reads 50 codes short at
// 1 V/div on the SDS1102CML+.
const dacSettle = 30 * time.Millisecond

// The couplings the relay word selects (spec 06 §6): DC bit 3, GND bit 1,
// neither AC.
const (
	cplDC = iota
	cplAC
	cplGND
)

// fe is one front-end setting for both channels.
type fe struct {
	det    [2]int
	cpl    [2]int
	bwl    [2]bool // the 20 MHz limit engaged
	trgSrc int     // relay byte 2's source: 0 C1, 1 C2
}

// relayWord is the absolute 24-bit relay word for s (never a
// read-modify-write: spec 06 §3).
// TRLC-LINKS: REQ-SDS-290
func (s fe) relayWord() uint32 {
	var b [2]uint32
	for ch := range 2 {
		v := uint32(0x20)
		if !s.bwl[ch] {
			v |= 0x01
		}
		switch s.cpl[ch] {
		case cplDC:
			v |= 0x08
		case cplGND:
			v |= 0x02
		}
		if detents[s.det[ch]].atten {
			v |= 0x04
		}
		if ch == 1 {
			v |= 0x80
		}
		b[ch] = v
	}
	return b[0] | b[1]<<8 | (0x70|uint32(s.trgSrc&3)<<2)<<16
}

// couplingCompanion is the CS3 preset the factory firmware writes with each
// coupling change (spec 06 §6): low selector 0x12/0x13, high 0x32/0x33.
var couplingCompanion = [2][2]uint16{{0x12, 0x32}, {0x13, 0x33}}

// bench drives the front end and reads the converters while the probe image
// runs them, every leg on.
type bench struct {
	b     regBus
	f     frontEnd
	p     *probe
	cores map[string][]int // core -> its lanes' ball indexes, bit 0 first
	chans [2][]string      // each channel's present cores
	sleep func(time.Duration)
	log   func(string, ...any)
}

// apply writes s: the relay word, the gain bytes (CH2 first), the coupling
// companions, then lets the front end settle.
// TRLC-LINKS: REQ-SDS-290
func (bn *bench) apply(s fe) error {
	if err := bn.f.WriteRelay(s.relayWord()); err != nil {
		return err
	}
	bn.sleep(time.Millisecond)
	if err := bn.f.WriteGain(detents[s.det[1]].gain, detents[s.det[0]].gain); err != nil {
		return err
	}
	for ch := range 2 {
		v := uint16(0xced9)
		if s.cpl[ch] == cplDC {
			v = 0xb851
		}
		if err := bn.b.Write(planeCS3, couplingCompanion[ch][0], v&0xff); err != nil {
			return err
		}
		if err := bn.b.Write(planeCS3, couplingCompanion[ch][1], v>>8); err != nil {
			return err
		}
	}
	bn.sleep(30 * time.Millisecond)
	return nil
}

// coreOrder is the cores in balls.txt's order, the order of the probe
// image's held codes (register k: core 2k low byte, core 2k+1 high byte).
var coreOrder = []string{"E1.CH1", "E1.CH2", "E2.CH1", "E2.CH2", "E3.CH1", "E3.CH2", "E4.CH1", "E4.CH2", "E5.CH1", "E5.CH2"}

// The probe image's held codes and the register that takes them.
const (
	prHeld = 0x00
	prSnap = 0x05
)

// snapshots reads n codes of every present core, each from one instant of
// the running converters (the legs stay on).
// TRLC-LINKS: REQ-SDS-290
func (bn *bench) snapshots(n int) (map[string][]int, error) {
	out := map[string][]int{}
	for range n {
		if err := bn.b.Write(planeCS1, prSnap, 1); err != nil {
			return nil, err
		}
		var regs [5]uint16
		for k := range regs {
			v, err := bn.b.Read(planeCS1, prHeld+uint16(k))
			if err != nil {
				return nil, err
			}
			regs[k] = v
		}
		for k, core := range coreOrder {
			if _, ok := bn.cores[core]; ok {
				out[core] = append(out[core], int(regs[k/2]>>(8*(k%2))&0xff))
			}
		}
	}
	return out, nil
}

// Levels is what a channel's held codes show: one level (Amp 0) or a
// square's two, and the spread.
type Levels struct {
	Mean, Amp, Noise float64
	Lo, Hi           float64
	Square           bool
}

// codeLevels judges codes: a square when they split into two clusters at least
// 8 codes and six times their noise apart, each with a tenth of the codes.
// TRLC-LINKS: REQ-SDS-290
func codeLevels(codes []int) Levels {
	if len(codes) == 0 {
		return Levels{}
	}
	lo, hi := slices.Min(codes), slices.Max(codes)
	mid := float64(lo+hi) / 2
	var a, b []float64
	for _, c := range codes {
		if float64(c) <= mid {
			a = append(a, float64(c))
		} else {
			b = append(b, float64(c))
		}
	}
	var l Levels
	all := make([]float64, len(codes))
	for i, c := range codes {
		all[i] = float64(c)
	}
	l.Mean, l.Noise = meanStd(all)
	l.Lo, l.Hi = l.Mean, l.Mean
	if hi-lo >= 8 && len(a) >= len(codes)/10 && len(b) >= len(codes)/10 {
		ma, sa := meanStd(a)
		mb, sb := meanStd(b)
		// noise split in two halves is clusters 1.6 sigma apart, each 0.6
		// sigma wide; a square's levels are far apart for their noise
		if mb-ma >= 6*(sa+sb)/2 {
			l.Lo, l.Hi, l.Amp, l.Square, l.Noise = ma, mb, mb-ma, true, (sa+sb)/2
		}
	}
	return l
}

// meanStd is the mean and standard deviation of xs.
// TRLC-LINKS: REQ-SDS-290
func meanStd(xs []float64) (float64, float64) {
	var s, q float64
	for _, x := range xs {
		s += x
	}
	m := s / float64(len(xs))
	for _, x := range xs {
		q += (x - m) * (x - m)
	}
	return m, math.Sqrt(q / float64(len(xs)))
}

// channel is channel ch's levels: each present core's, averaged.
// TRLC-LINKS: REQ-SDS-290
func (bn *bench) channel(ch, n int) (Levels, error) {
	snaps, err := bn.snapshots(n)
	if err != nil {
		return Levels{}, err
	}
	var ls []Levels
	for _, core := range bn.chans[ch] {
		ls = append(ls, codeLevels(snaps[core]))
	}
	if len(ls) == 0 {
		return Levels{}, fmt.Errorf("channel %d has no converter", ch+1)
	}
	var out Levels
	sq := 0
	for _, l := range ls {
		out.Mean += l.Mean / float64(len(ls))
		out.Noise += l.Noise / float64(len(ls))
		out.Lo += l.Lo / float64(len(ls))
		out.Hi += l.Hi / float64(len(ls))
		out.Amp += l.Amp / float64(len(ls))
		if l.Square {
			sq++
		}
	}
	out.Square = sq*2 > len(ls)
	return out, nil
}

// midpoint is the middle of codes between their extremes, one code trimmed
// at each end: a square's middle or a level, whichever the probe carries,
// and whether either extreme is at a rail.
// TRLC-LINKS: REQ-SDS-290
func midpoint(codes []int) (float64, bool) {
	s := slices.Clone(codes)
	slices.Sort(s)
	if len(s) >= 8 {
		s = s[1 : len(s)-1]
	}
	if len(s) == 0 {
		return 0, true
	}
	lo, hi := s[0], s[len(s)-1]
	return float64(lo+hi) / 2, lo <= 8 || hi >= 247
}

// channelMid is channel ch's midpoint over n snapshots, its present cores
// averaged, and whether any core reached a rail.
// TRLC-LINKS: REQ-SDS-290
func (bn *bench) channelMid(ch, n int) (float64, bool, error) {
	snaps, err := bn.snapshots(n)
	if err != nil {
		return 0, false, err
	}
	sum, clip := 0.0, false
	for _, core := range bn.chans[ch] {
		m, c := midpoint(snaps[core])
		sum += m
		clip = clip || c
	}
	if len(bn.chans[ch]) == 0 {
		return 0, false, fmt.Errorf("channel %d has no converter", ch+1)
	}
	return sum / float64(len(bn.chans[ch])), clip, nil
}

// center sets channel ch's offset DAC so its mean code sits near mid-scale
// (128): a scan across the DAC's working range finds the step where the code
// crosses 128, and bisection the code. It returns an error when the offset
// moves nothing across 128.
// TRLC-LINKS: REQ-SDS-290
func (bn *bench) center(ch int) (uint16, error) {
	at := func(code int) (float64, error) {
		if err := setOffsetDAC(bn.b, ch, uint16(code)); err != nil { //nolint:gosec // G115: a DAC code in range
			return 0, err
		}
		bn.sleep(dacSettle)
		m, _, err := bn.channelMid(ch, 32)
		return m, err
	}
	const step = 0x200
	prev, err := at(dacLow)
	if err != nil {
		return 0, err
	}
	lo, hi := -1, -1
	seen := []float64{prev}
	for c := dacLow + step; c <= dacHigh; c += step {
		m, err := at(c)
		if err != nil {
			return 0, err
		}
		seen = append(seen, m)
		if (prev < 128) != (m < 128) {
			lo, hi = c-step, c
			break
		}
		prev = m
	}
	if lo < 0 {
		return 0, fmt.Errorf("the offset DAC moves channel %d's code between %.0f and %.0f only, never across mid-scale", ch+1, slices.Min(seen), slices.Max(seen))
	}
	mlo, err := at(lo)
	if err != nil {
		return 0, err
	}
	below := mlo < 128
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		m, err := at(mid)
		if err != nil {
			return 0, err
		}
		if (m < 128) == below {
			lo = mid
		} else {
			hi = mid
		}
	}
	return uint16(lo), nil
}

// slope is channel ch's code change per offset-DAC code around code: the
// step widens until the cores' midpoints move at least 20 codes, each core
// counted where it stays off the rails at both ends of the step (the cores'
// own offsets differ).
// TRLC-LINKS: REQ-SDS-290
func (bn *bench) slope(ch int, code uint16) (float64, error) {
	at := func(c int) (map[string][]int, error) {
		if err := setOffsetDAC(bn.b, ch, uint16(max(dacLow, min(dacHigh, c)))); err != nil {
			return nil, err
		}
		bn.sleep(dacSettle)
		return bn.snapshots(32)
	}
	last := 0.0
	for d := 4; d <= 0x2000; d *= 2 {
		a, err := at(int(code) - d)
		if err != nil {
			return 0, err
		}
		b, err := at(int(code) + d)
		if err != nil {
			return 0, err
		}
		sum, n := 0.0, 0
		for _, core := range bn.chans[ch] {
			ma, ca := midpoint(a[core])
			mb, cb := midpoint(b[core])
			if !ca && !cb {
				sum += math.Abs(mb - ma)
				n++
			}
		}
		if n == 0 {
			if last > 0 {
				return last, nil
			}
			return 0, fmt.Errorf("a rail reached at a step of %d", d)
		}
		moved := sum / float64(n)
		last = moved / float64(2*d)
		if moved >= 20 {
			return last, nil
		}
	}
	return last, nil
}

// DetentResult is one detent's measurements on one channel.
type DetentResult struct {
	Vdiv   float64 `json:"vdiv"`
	Atten  bool    `json:"attenuated"`
	Center uint16  `json:"offset_center"`
	Slope  float64 `json:"codes_per_dac_code"`
	Square Levels  `json:"square"` // the 1 kHz square (or the level), centred
	Error  string  `json:"error,omitempty"`
}

// FrontEndResult is the front end of one channel.
type FrontEndResult struct {
	Detents  []DetentResult    `json:"detents"`
	Signal   float64           `json:"signal_vdiv"` // the V/div of the coupling and edge tests
	Coupling map[string]Levels `json:"coupling"`    // DC, AC, GND at Signal, centred in DC
	Error    string            `json:"error,omitempty"`
}

// fits is a square of 30..200 codes clear of the rails.
// TRLC-LINKS: REQ-SDS-290
func fits(l Levels) bool { return l.Square && l.Amp >= 30 && l.Amp <= 200 && l.Lo > 5 && l.Hi < 250 }

// frontEndTests measures channel ch: every detent centred with its square
// and offset slope, then at the detent where the square fits best (the 10
// mV/div range without a square) the three couplings.
// TRLC-LINKS: REQ-SDS-290
func (bn *bench) frontEndTests(ch int) FrontEndResult {
	r := FrontEndResult{Coupling: map[string]Levels{}}
	base := fe{det: [2]int{6, 6}}
	sd := -1
	for d := range detents {
		s := base
		s.det[ch] = d
		dr := DetentResult{Vdiv: detents[d].vdiv, Atten: detents[d].atten}
		if err := bn.apply(s); err != nil {
			r.Error = err.Error()
			return r
		}
		c, err := bn.center(ch)
		if err != nil {
			dr.Error = err.Error()
			r.Detents = append(r.Detents, dr)
			continue
		}
		dr.Center = c
		_ = setOffsetDAC(bn.b, ch, c)
		bn.sleep(dacSettle)
		if l, err := bn.channel(ch, 64); err == nil {
			dr.Square = l
		}
		if dr.Square.Square && dr.Square.Amp > 200 {
			dr.Error = "the square fills the range"
		} else if dr.Slope, err = bn.slope(ch, c); err != nil {
			dr.Error = err.Error()
		}
		if fits(dr.Square) && (sd < 0 || dr.Square.Amp > r.Detents[sd].Square.Amp) {
			sd = d
		}
		r.Detents = append(r.Detents, dr)
		bn.log("channel %d %g V/div: centre %d, %.5f codes per DAC code, square %v amp %.1f %s", ch+1, dr.Vdiv, c, dr.Slope, dr.Square.Square, dr.Square.Amp, dr.Error)
	}
	if sd < 0 {
		sd = 0
	}
	r.Signal = detents[sd].vdiv
	at := func(s fe) (Levels, error) {
		if err := bn.apply(s); err != nil {
			return Levels{}, err
		}
		_ = setOffsetDAC(bn.b, ch, r.Detents[sd].Center)
		bn.sleep(dacSettle)
		return bn.channel(ch, 64)
	}
	for _, cp := range []struct {
		name string
		cpl  int
	}{{"DC", cplDC}, {"AC", cplAC}, {"GND", cplGND}} {
		s := base
		s.det[ch], s.cpl[ch] = sd, cp.cpl
		l, err := at(s)
		if err != nil {
			r.Error = err.Error()
			return r
		}
		r.Coupling[cp.name] = l
	}
	return r
}

// TriggerResult is the trigger comparators as the level sweeps found them:
// each sweep's pattern for every ball that moved (0, 1, or ~ toggling), with
// both channels centred, with C1 railed and with C2 railed. A ball toggling
// on the square that stops when a channel is railed is that channel's
// comparator.
type TriggerResult struct {
	Comparator [2]string            `json:"comparator_ball"`
	Patterns   map[string][3]string `json:"patterns,omitempty"`
	Error      string               `json:"error,omitempty"`
}

// The trigger level DAC (spec 05 §2): lane A 0x14/0x34, its mirror lane B
// 0x15/0x35, low byte then high, the same code to both.
var trigDAC = [2][2]uint16{{0x14, 0x34}, {0x15, 0x35}}

// setTrigLevel writes code to both trigger-level lanes.
// TRLC-LINKS: REQ-SDS-290
func setTrigLevel(b regBus, code uint16) error {
	for _, l := range trigDAC {
		if err := b.Write(planeCS3, l[0], code&0xff); err != nil {
			return err
		}
		if err := b.Write(planeCS3, l[1], code>>8); err != nil {
			return err
		}
	}
	return nil
}

// triggerTests sweeps the trigger level across its DAC with both channels
// at their signal detents, centred, and again with C1 and with C2 railed,
// recording each non-converter ball's level or toggling at every step.
// TRLC-LINKS: REQ-SDS-290
func (bn *bench) triggerTests(balls []Ball, det [2]int, centre [2]uint16) TriggerResult {
	r := TriggerResult{Patterns: map[string][3]string{}}
	if err := bn.apply(fe{det: det}); err != nil {
		r.Error = err.Error()
		return r
	}
	pat := map[int]*[3][]byte{}
	for run := range 3 {
		off := centre
		switch run {
		case 1:
			off[0] = dacLow
		case 2:
			off[1] = dacLow
		}
		_ = setOffsetDAC(bn.b, 0, off[0])
		_ = setOffsetDAC(bn.b, 1, off[1])
		bn.sleep(dacSettle)
		for step := range 33 {
			if err := setTrigLevel(bn.b, uint16(min(step*2048, 0xffff))); err != nil {
				r.Error = err.Error()
				return r
			}
			bn.sleep(3 * time.Millisecond)
			if err := bn.p.gate(probeClockHz / 50); err != nil {
				r.Error = err.Error()
				return r
			}
			e0, e1, live, err := bn.p.flags()
			if err != nil {
				r.Error = err.Error()
				return r
			}
			for i, bl := range balls {
				if bl.Group == "adc" || bl.Group == "clock" || bl.Group == "sram" {
					continue
				}
				if pat[i] == nil {
					pat[i] = &[3][]byte{}
				}
				c := byte('0')
				switch {
				case e0[i] && e1[i]:
					c = '~'
				case live[i]:
					c = '1'
				}
				pat[i][run] = append(pat[i][run], c)
			}
		}
	}
	_ = setOffsetDAC(bn.b, 0, centre[0])
	_ = setOffsetDAC(bn.b, 1, centre[1])
	keys := make([]int, 0, len(pat))
	for i := range pat {
		keys = append(keys, i)
	}
	sort.Ints(keys)
	for _, i := range keys {
		p := [3]string{string(pat[i][0]), string(pat[i][1]), string(pat[i][2])}
		moved := func(s string) bool {
			return strings.Contains(s, "~") || strings.Contains(s, "0") && strings.Contains(s, "1")
		}
		if !moved(p[0]) && !moved(p[1]) && !moved(p[2]) {
			continue
		}
		r.Patterns[balls[i].Ball] = p
		for ch := range 2 {
			if r.Comparator[ch] == "" && band(p[0]) && !band(p[ch+1]) && band(p[2-ch]) {
				r.Comparator[ch] = balls[i].Ball
			}
		}
	}
	if len(r.Patterns) == 0 {
		r.Error = "no ball followed the trigger level"
	}
	return r
}

// band reports whether a sweep pattern toggles at two or more consecutive
// trigger levels (the first, at the DAC's end, aside): a square crossing the
// levels, where noise on a flat line toggles at one.
// TRLC-LINKS: REQ-SDS-290
func band(p string) bool {
	return len(p) > 1 && strings.Contains(p[1:], "~~")
}

// The probe image's edge capture (pair E1: C1's E1.CH1 low byte, C2's
// E1.CH2 high byte, 512 samples at 100 MS/s, 256 before the trigger).
const (
	prCapture  = 0x06
	prCapAddr  = 0x07
	prCapData  = 0x08
	capWords   = 512
	capTrigger = 256
)

// edgeCapture arms the capture on ball's rising edge and reads it back,
// oldest first.
// TRLC-LINKS: REQ-SDS-290
func (bn *bench) edgeCapture(ball int) ([]uint16, error) {
	if err := bn.b.Write(planeCS1, prCapture, 1|uint16(ball)<<8); err != nil { //nolint:gosec // G115: a ball index
		return nil, err
	}
	done := false
	for range 50 {
		bn.sleep(time.Millisecond)
		if v, err := bn.b.Read(planeCS1, prCapture); err == nil && v&1 != 0 {
			done = true
			break
		}
	}
	if !done {
		return nil, errors.New("the edge capture did not trigger")
	}
	out := make([]uint16, capWords)
	for a := range out {
		if err := bn.b.Write(planeCS1, prCapAddr, uint16(a)); err != nil {
			return nil, err
		}
		v, err := bn.b.Read(planeCS1, prCapData)
		if err != nil {
			return nil, err
		}
		out[a] = v
	}
	return out, nil
}

// EdgeResult is a channel's square edge as the edge captures sampled it.
type EdgeResult struct {
	Ball     string     `json:"trigger_ball"`
	Captures int        `json:"captures"`
	Rise     [2]float64 `json:"samples_within_the_edge"` // per edge, 10..90 %: the limit off, on
	Error    string     `json:"error,omitempty"`
}

// riseSamples is the samples of one capture within its edge's 10..90 %
// band near the trigger, the levels read before and after it; -1 when the
// capture shows no edge.
// TRLC-LINKS: REQ-SDS-290
func riseSamples(codes []float64) float64 {
	pre, _ := meanStd(codes[capTrigger-200 : capTrigger-40])
	post, _ := meanStd(codes[capTrigger+40 : capTrigger+200])
	if math.Abs(post-pre) < 20 {
		return -1
	}
	lo, hi := math.Min(pre, post), math.Max(pre, post)
	a, b := lo+0.1*(hi-lo), lo+0.9*(hi-lo)
	n := 0
	for _, c := range codes[capTrigger-30 : capTrigger+30] {
		if c > a && c < b {
			n++
		}
	}
	return float64(n)
}

// edgeTests captures channel ch's square edges, triggered by its comparator
// ball at a level within the square, with the 20 MHz limit off and on: the
// trigger is asynchronous to the sample clock, so the samples a capture
// catches within the edge average to its rise time over the sample period
// (10 ns). The limit slows the edge.
// TRLC-LINKS: REQ-SDS-290
func (bn *bench) edgeTests(ch, ball, det int, centre, level uint16) EdgeResult {
	const n = 80
	r := EdgeResult{}
	for i, on := range []bool{false, true} {
		s := fe{det: [2]int{6, 6}}
		s.det[ch] = det
		s.bwl[ch] = on
		if err := bn.apply(s); err != nil {
			r.Error = err.Error()
			return r
		}
		_ = setOffsetDAC(bn.b, ch, centre)
		_ = setTrigLevel(bn.b, level)
		bn.sleep(dacSettle)
		sum, k := 0.0, 0
		for range n {
			w, err := bn.edgeCapture(ball)
			if err != nil {
				r.Error = err.Error()
				return r
			}
			codes := make([]float64, len(w))
			for j, v := range w {
				codes[j] = float64(v >> (8 * ch) & 0xff)
			}
			if x := riseSamples(codes); x >= 0 {
				sum += x
				k++
			}
		}
		if k == 0 {
			r.Error = "the captures show no edge"
			return r
		}
		r.Rise[i] = sum / float64(k)
		r.Captures += k
	}
	return r
}
