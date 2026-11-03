// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"
)

// probeBallsText is fpga/images/probe/balls.txt, the probe image's census
// balls in index order (a test keeps the copy equal).
//
//go:embed probe_balls.txt
var probeBallsText string

// Ball is one census ball: the FPGA ball, what it is on the SDS1102CML+ and
// its group (adc, sram, panel, other, unused, clkin, clock).
type Ball struct {
	Ball  string `json:"ball"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

// probeBalls parses the census balls.
// TRLC-LINKS: REQ-SDS-288
func probeBalls() []Ball {
	var bs []Ball
	for line := range strings.SplitSeq(probeBallsText, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && !strings.HasPrefix(line, "#") {
			bs = append(bs, Ball{f[0], f[1], f[2]})
		}
	}
	return bs
}

// The probe image's registers (fpga/images/probe/probe_top.v).
const (
	prID     = 0x12
	prInfo   = 0x13
	prStatus = 0x14
	prStart  = 0x15
	prGateLo = 0x16
	prGateHi = 0x17
	prLegs   = 0x18
	prEdges  = 0x20
	prHighs  = 0x30
	prChan   = 0x40
	prEver0  = 0x50
	prEver1  = 0x60
	prLive   = 0x70

	probeID       = 0x5052
	probeChannels = 8
	probeClockHz  = 50_000_000 // the gate counts clk, the ARM's GPMC clock
	allLegs       = 0x3ff
)

// legCore is the converter core each encode leg clocks on the SDS1102CML+:
// leg 2p is pair E(p+1)'s enc_p ball, 2p+1 its enc_n ball (spec 12 §4.1).
var legCore = [10]string{"E1.CH2", "E1.CH1", "E2.CH1", "E2.CH2", "E3.CH2", "E3.CH1", "E4.CH1", "E4.CH2", "E5.CH1", "E5.CH2"}

// BallState is what one ball did in a gate with the legs off and with all on.
type BallState struct {
	Ever0, Ever1, Live bool
	Edges, High        uint32 // rising edges and clk cycles high in a 1 ms gate
}

// Census is one probe image's census of every ball.
type Census struct {
	Pull   bool        `json:"pull"`
	Locked bool        `json:"pll_locked"`
	Off    []BallState `json:"legs_off"`
	On     []BallState `json:"legs_on"`
	// Moved: the ball was seen 0 and 1 across the offset sweep with every
	// leg on; Chan: it followed that channel's offset from one end to the
	// other with every leg on; Leg: it was seen 0 and 1 with only that leg
	// on (every other core stopped, holding its code).
	Moved []bool     `json:"moved"`
	Chan  [2][]bool  `json:"moved_by_channel"`
	Leg   [10][]bool `json:"moved_by_leg"`
	Steps []string   `json:"steps"`
	Error string     `json:"error,omitempty"`
}

// probe drives the census image over the bus.
type probe struct {
	b     regBus
	n     int
	sleep func(time.Duration)
}

// openProbe checks the probe image answers with its identity and ball count.
// TRLC-LINKS: REQ-SDS-288
func openProbe(b regBus, n int, sleep func(time.Duration)) (*probe, bool, error) {
	id, err := b.Read(planeCS1, prID)
	if err != nil {
		return nil, false, err
	}
	if id != probeID {
		return nil, false, fmt.Errorf("the probe image does not answer: identity 0x%04x, want 0x%04x", id, probeID)
	}
	info, err := b.Read(planeCS1, prInfo)
	if err != nil {
		return nil, false, err
	}
	if int(info>>8) != n {
		return nil, false, fmt.Errorf("the probe image has %d balls, the list %d", info>>8, n)
	}
	return &probe{b: b, n: n, sleep: sleep}, info&1 != 0, nil
}

// gate runs one gate of clocks cycles of clk and waits for it to end.
// TRLC-LINKS: REQ-SDS-288
func (p *probe) gate(clocks uint32) error {
	for _, w := range [][2]uint16{{prGateLo, uint16(clocks & 0xffff)}, {prGateHi, uint16(clocks >> 16 & 0xff)}, {prStart, 1}} {
		if err := p.b.Write(planeCS1, w[0], w[1]); err != nil {
			return err
		}
	}
	p.sleep(time.Duration(clocks) * time.Second / probeClockHz)
	for range 200 {
		v, err := p.b.Read(planeCS1, prStatus)
		if err != nil {
			return err
		}
		if v&1 == 0 {
			return nil
		}
		p.sleep(time.Millisecond)
	}
	return errors.New("the probe's gate did not end")
}

// flags reads every ball's ever-0, ever-1 and live level.
// TRLC-LINKS: REQ-SDS-288
func (p *probe) flags() (e0, e1, live []bool, err error) {
	read := func(base uint16) ([]bool, error) {
		out := make([]bool, p.n)
		for w := 0; w*16 < p.n; w++ {
			v, err := p.b.Read(planeCS1, base+uint16(w))
			if err != nil {
				return nil, err
			}
			for i := 0; i < 16 && w*16+i < p.n; i++ {
				out[w*16+i] = v>>i&1 != 0
			}
		}
		return out, nil
	}
	if e0, err = read(prEver0); err != nil {
		return e0, e1, live, err
	}
	if e1, err = read(prEver1); err != nil {
		return e0, e1, live, err
	}
	live, err = read(prLive)
	return e0, e1, live, err
}

// states is one gate's flags for every ball and, eight balls a gate, its
// edges and high time in 1 ms.
// TRLC-LINKS: REQ-SDS-288
func (p *probe) states() ([]BallState, error) {
	const ms = probeClockHz / 1000
	st := make([]BallState, p.n)
	if err := p.gate(ms); err != nil {
		return nil, err
	}
	e0, e1, live, err := p.flags()
	if err != nil {
		return nil, err
	}
	for i := range st {
		st[i].Ever0, st[i].Ever1, st[i].Live = e0[i], e1[i], live[i]
	}
	for base := 0; base < p.n; base += probeChannels {
		for c := range probeChannels {
			if err := p.b.Write(planeCS1, prChan+uint16(c), uint16(min(base+c, 0xff))); err != nil {
				return nil, err
			}
		}
		if err := p.gate(ms); err != nil {
			return nil, err
		}
		for c := 0; c < probeChannels && base+c < p.n; c++ {
			e, err := p.read24(prEdges + uint16(2*c))
			if err != nil {
				return nil, err
			}
			h, err := p.read24(prHighs + uint16(2*c))
			if err != nil {
				return nil, err
			}
			st[base+c].Edges, st[base+c].High = e, h
		}
	}
	return st, nil
}

// read24 reads a channel count: the low 16 bits at sel, the high 8 at sel+1.
// TRLC-LINKS: REQ-SDS-288
func (p *probe) read24(sel uint16) (uint32, error) {
	lo, err := p.b.Read(planeCS1, sel)
	if err != nil {
		return 0, err
	}
	hi, err := p.b.Read(planeCS1, sel+1)
	return uint32(hi&0xff)<<16 | uint32(lo), err
}

// moved runs a gate at each pair of offset codes (channel 1, channel 2) with
// the legs given on, and reports which balls were seen both 0 and 1 across
// the gates (seen), or, with followed, which balls' flags differ between the
// gates: a converter at one rail and then the other follows the DAC, one at
// mid-scale with noise (its front end missing, or the other channel's)
// shows both levels in every gate.
// TRLC-LINKS: REQ-SDS-288
func (p *probe) moved(legs uint16, codes [][2]uint16, followed bool) ([]bool, error) {
	if err := p.b.Write(planeCS1, prLegs, legs); err != nil {
		return nil, err
	}
	e0 := make([]bool, p.n)
	e1 := make([]bool, p.n)
	differs := make([]bool, p.n)
	for gi, cc := range codes {
		for ch, code := range cc {
			if err := setOffsetDAC(p.b, ch, code); err != nil {
				return nil, err
			}
		}
		p.sleep(50 * time.Millisecond) // the front end settles after the step
		if err := p.gate(probeClockHz / 200); err != nil {
			return nil, err
		}
		g0, g1, _, err := p.flags()
		if err != nil {
			return nil, err
		}
		for i := range e0 {
			if gi > 0 && (g0[i] != e0[i] || g1[i] != e1[i]) {
				differs[i] = true
			}
			if followed {
				e0[i], e1[i] = g0[i], g1[i]
			} else {
				e0[i] = e0[i] || g0[i]
				e1[i] = e1[i] || g1[i]
			}
		}
	}
	if followed {
		return differs, nil
	}
	out := make([]bool, p.n)
	for i := range out {
		out[i] = e0[i] && e1[i]
	}
	return out, nil
}

// The offset DAC codes of the census. The SDS1102CML+'s DAC moves the input
// steadily over 0x0c00..0x4800 only (below 0x0b00 it wraps to the other rail;
// from about 0x4800 up the converters sit at one rail): its ends put each
// channel's converters at the two rails at every V/div (every code bit flips
// between them), the codes between through mid-scale.
const (
	dacLow  = 0x0c00
	dacMid  = 0x2800
	dacHigh = 0x4800
)

// runCensus is the census on a loaded probe image: every ball with the legs
// off and on, the offset sweep with every leg on, each channel's offset
// alone and each leg alone. The legs end off and the DACs at mid-scale.
// TRLC-LINKS: REQ-SDS-288
func runCensus(b regBus, n int, sleep func(time.Duration)) Census {
	var cs Census
	p, pull, err := openProbe(b, n, sleep)
	if err != nil {
		cs.Error = err.Error()
		return cs
	}
	cs.Pull = pull
	defer func() {
		_ = b.Write(planeCS1, prLegs, 0)
		_ = setOffsetDAC(b, 0, dacMid)
		_ = setOffsetDAC(b, 1, dacMid)
	}()
	step := func(s string) { cs.Steps = append(cs.Steps, s) }
	fail := func(err error) Census { cs.Error = err.Error(); return cs }
	for range 100 {
		if v, err := b.Read(planeCS1, prStatus); err == nil && v&2 != 0 {
			cs.Locked = true
			break
		}
		sleep(2 * time.Millisecond)
	}
	step(fmt.Sprintf("encode PLL locked: %v", cs.Locked))
	if err := b.Write(planeCS1, prLegs, 0); err != nil {
		return fail(err)
	}
	for ch := range 2 {
		if err := setOffsetDAC(b, ch, dacMid); err != nil {
			return fail(err)
		}
	}
	sleep(50 * time.Millisecond)
	if cs.Off, err = p.states(); err != nil {
		return fail(err)
	}
	step("legs off: flags and rates")
	if err := b.Write(planeCS1, prLegs, allLegs); err != nil {
		return fail(err)
	}
	sleep(5 * time.Millisecond)
	if cs.On, err = p.states(); err != nil {
		return fail(err)
	}
	step("legs on: flags and rates")
	sweep := [][2]uint16{{dacLow, dacLow}, {0x1800, 0x1800}, {dacMid, dacMid}, {0x3800, 0x3800}, {dacHigh, dacHigh}}
	if cs.Moved, err = p.moved(allLegs, sweep, false); err != nil {
		return fail(err)
	}
	step("legs on: offset sweep")
	for ch := range 2 {
		codes := [][2]uint16{{dacMid, dacMid}, {dacMid, dacMid}}
		codes[0][ch], codes[1][ch] = dacLow, dacHigh
		if cs.Chan[ch], err = p.moved(allLegs, codes, true); err != nil {
			return fail(err)
		}
		step(fmt.Sprintf("channel %d's offset alone", ch+1))
	}
	for l := range 10 {
		if cs.Leg[l], err = p.moved(1<<l, [][2]uint16{{dacLow, dacLow}, {dacHigh, dacHigh}}, false); err != nil {
			return fail(err)
		}
	}
	step("each leg alone")
	return cs
}
