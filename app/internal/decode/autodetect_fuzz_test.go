// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The AUTO fuzzing campaign: every labelled capture the detection tests know
// (the protocols' generated captures, the bench records in testdata) and
// parametric UART, SPI and I2C buses - SPI bit-banged too: short clock
// pulses, uneven gaps, the data set well before each pulse - mutated at
// random (converter noise, slow edges, a smaller swing and an offset, the
// record cut at either end, box-averaged decimation, a distractor on an idle
// channel) and named by the detection the scope runs (AutodetectFast).
// Three rules: a mild mutation names the bus itself on its line; no mutation
// names another bus (but the protocols that are the same line, autoAlike);
// noise and idle lines name none. SDS_AUTO_FUZZ=n runs n mutations
// (SDS_AUTO_FUZZ_SEED the first seed); without it a few run.

// fuzzCase is one labelled capture: the bus, its first line, and whether a
// mutation may hide it (a bus whose decimated record cannot hold it).
type fuzzCase struct {
	detectCase
	fragile bool // the capture is at the edge of what names it: misses are not findings
}

// autoAlike are the names a bus may also take, the same line read at a
// coarser level: a UART-framed protocol's frames are UART characters, a
// 1553 word is Manchester.
var autoAlike = map[string][]string{
	"lin": {"uart"}, "dmx512": {"uart"}, "midi": {"uart"}, "modbus": {"uart"}, "mil1553": {"manchester"},
	"ook": {"manchester"}, "em4100": {"manchester"}, // an Oregon sensor's and a tag's bits are Manchester
}

// clockedOwn are the clocked two-line buses' own decoders: where a cut
// leaves none of the bus's frames whole, SPI's reading of the clocked bits
// is the record's best (fuzzOne).
var clockedOwn = map[string]func(a, b []uint8, ct float64, cfg ClockedCfg) Result{
	"ps2": DecodePS2, "swd": DecodeSWD, "mdio": DecodeMDIO, "pdi": DecodePDI, "sdcard": DecodeSDCard,
	"maple": DecodeMaple, "cjtag": DecodeCJTAG, "caliper": DecodeCaliper,
}

// ownFrames reports a capture on which the bus's own decoder reads frames
// the detection would keep (a score above none), either line its first.
// TRLC-LINKS: REQ-SDS-018
func ownFrames(c detectCase) bool {
	d := clockedOwn[c.proto]
	if d == nil {
		return true
	}
	for _, ord := range [][2][]uint8{{c.c1, c.c2}, {c.c2, c.c1}} {
		if scoreResult(d(ord[0], ord[1], c.colT, ClockedCfg{Format: "hex"})) > 0 {
			return true
		}
	}
	return false
}

// cleanNamesIt reports a mutation whose cut and decimation alone (no noise,
// edge, swing, offset or distractor) leave the bus named: where they do not,
// the cut left a fragment another bus reads as well (a T55xx command cut to
// six clean characters of 4800 Bd), no detection's fault.
// TRLC-LINKS: REQ-SDS-018
func cleanNamesIt(c detectCase, m fuzzMutation) bool {
	clean := fuzzMutation{edge: 1, swing: 1, cutHead: m.cutHead, cutTail: m.cutTail, decim: m.decim}
	n := len(c.c1)
	h, tl := int(clean.cutHead*float64(n)), int(clean.cutTail*float64(n))
	if h+tl > n*7/10 {
		tl = n*7/10 - h
	}
	rng := rand.New(rand.NewSource(0))
	a, b := shapeChannel(rng, c.c1[h:n-tl], clean), shapeChannel(rng, c.c2[h:n-tl], clean)
	r, _ := AutodetectFast(a, b, c.colT*float64(max(1, m.decim)), "hex")
	return r.OK && r.Proto == c.proto
}

// TRLC-LINKS: REQ-SDS-018
func alikeName(want, got string) bool {
	for _, a := range autoAlike[want] {
		if a == got {
			return true
		}
	}
	return false
}

// fuzzUART is a UART bus: text at baud, sampled at sr.
// TRLC-LINKS: REQ-SDS-018
func fuzzUART(rng *rand.Rand, baud, sr float64) []uint8 {
	w := newTimeline(sr)
	w.idle(30 / baud)
	for i := 0; i < 40; i++ {
		tlUART(w, 0x20+rng.Intn(0x5f), baud, "none", 1)
		if rng.Intn(6) == 0 {
			w.idle(float64(5+rng.Intn(30)) / baud)
		}
	}
	w.idle(30 / baud)
	return w.codes()
}

// spiShape is an SPI bus's timing: its clock rate and duty (the share of a
// period the clock rests away from its idle level), whether its bytes come
// back to back or bit-banged (each bit's period stretched by up to jitter,
// a gap after each byte), MSB first.
type spiShape struct {
	hz, duty, jitter, byteGap float64
	cpol, cpha                bool
}

// fuzzSPI is an SPI bus of shape s: bytes on the data line, the clock on the
// other, as a twoLine (a clock, b data).
// TRLC-LINKS: REQ-SDS-018
func fuzzSPI(rng *rand.Rand, s spiShape, sr float64, bytes int) twoLine {
	l := newTwoLine(sr)
	idle := byte(0)
	if s.cpol {
		idle = 1
	}
	act := 1 - idle
	T := 1 / s.hz
	l.add(idle, 0, 20*T)
	d := byte(0)
	for k := 0; k < bytes; k++ {
		v := rng.Intn(256)
		for i := 7; i >= 0; i-- {
			bit := byte(v >> i & 1)
			// a bit-banging loop: each bit within 5% of its period, one in
			// ten held up (an interrupt) by up to jitter periods
			per := T * (1 + 0.05*(rng.Float64()*2-1))
			if s.jitter > 0 && rng.Intn(10) == 0 {
				per += T * s.jitter * rng.Float64()
			}
			on := per * s.duty
			if !s.cpha {
				// data set before the leading edge, which samples it
				d = bit
				l.add(idle, d, per-on)
				l.add(act, d, on)
			} else {
				// data set on the leading edge, sampled on the trailing one
				l.add(idle, d, per-on)
				d = bit
				l.add(act, d, on)
			}
		}
		if s.byteGap > 0 {
			l.add(idle, d, s.byteGap*T*(0.5+rng.Float64()))
		}
	}
	l.add(idle, d, 20*T)
	return l
}

// fuzzI2C is an I2C bus at hz: transfers to addr of a few bytes, each
// acknowledged, with START and STOP, as a twoLine (a SCL, b SDA).
// TRLC-LINKS: REQ-SDS-018
func fuzzI2C(rng *rand.Rand, hz, sr float64, transfers int) twoLine {
	l := newTwoLine(sr)
	T := 1 / hz
	l.add(1, 1, 20*T)
	// SDA moves a quarter period into SCL low, never with SCL's fall: a
	// device holds it past the fall (300 ns at least) so that no data
	// change reads as a START or a STOP
	sda := byte(0)
	bitOut := func(b byte) {
		l.add(0, sda, T/4)
		l.add(0, b, T/4)
		l.add(1, b, T/2)
		sda = b
	}
	for k := 0; k < transfers; k++ {
		l.add(1, 0, T/2) // START
		l.add(0, 0, T/2)
		sda = 0
		addr := 0x08 + rng.Intn(0x70)
		frame := []int{addr<<1 | rng.Intn(2)}
		for n := 1 + rng.Intn(4); n > 0; n-- {
			frame = append(frame, rng.Intn(256))
		}
		for _, v := range frame {
			for i := 7; i >= 0; i-- {
				bitOut(byte(v >> i & 1))
			}
			bitOut(0) // ACK
		}
		l.add(0, 0, T/2) // STOP
		l.add(1, 0, T/2)
		l.add(1, 1, float64(10+rng.Intn(40))*T)
	}
	return l
}

// fuzzCorpus is every labelled capture: the detection tests' and the
// parametric buses'.
// TRLC-LINKS: REQ-SDS-018
func fuzzCorpus(t testing.TB) []fuzzCase {
	var cs []fuzzCase
	for _, c := range protoDetectCases() {
		cs = append(cs, fuzzCase{detectCase: c})
	}
	for _, c := range physDetectCases() {
		cs = append(cs, fuzzCase{detectCase: c})
	}
	for _, sc := range scopeCaptures {
		c1, c2, s := loadGzJSON(t, filepath.Join("testdata", sc.file))
		cs = append(cs, fuzzCase{detectCase: detectCase{sc.file, c1, c2, s, sc.proto, sc.chA}, fragile: true})
	}
	rng := rand.New(rand.NewSource(1))
	for _, baud := range []float64{9600, 115200, 460800, 1e6} {
		sr := 32 * baud
		c := fuzzUART(rng, baud, sr)
		cs = append(cs, fuzzCase{detectCase: detectCase{fmt.Sprintf("UART %g", baud), c, idleLike(c), 1 / sr, "uart", 0}})
	}
	for _, s := range []spiShape{
		{hz: 1e6, duty: 0.5}, {hz: 5e6, duty: 0.5, byteGap: 2},
		{hz: 1e6, duty: 0.125, jitter: 2, byteGap: 3, cpol: true}, // a bit-banged driver, short pulses
		{hz: 500e3, duty: 0.2, jitter: 1, byteGap: 2},
		{hz: 2e6, duty: 0.8, jitter: 1, byteGap: 1, cpha: true},
	} {
		sr := 40 * s.hz
		l := fuzzSPI(rng, s, sr, 24)
		a, b := l.a.codes(), l.b.codes()
		name := fmt.Sprintf("SPI %g duty %.2f jitter %.1f", s.hz, s.duty, s.jitter)
		cs = append(cs, fuzzCase{detectCase: detectCase{name, a, b, 1 / sr, "spi", 0}})
		cs = append(cs, fuzzCase{detectCase: detectCase{name + " swapped", b, a, 1 / sr, "spi", 1}})
	}
	for _, hz := range []float64{100e3, 400e3} {
		sr := 50 * hz
		l := fuzzI2C(rng, hz, sr, 6)
		a, b := l.a.codes(), l.b.codes()
		cs = append(cs, fuzzCase{detectCase: detectCase{fmt.Sprintf("I2C %g", hz), a, b, 1 / sr, "i2c", 0}})
		cs = append(cs, fuzzCase{detectCase: detectCase{fmt.Sprintf("I2C %g swapped", hz), b, a, 1 / sr, "i2c", 1}})
	}
	// a capture whose bus a clean cut of 5% at either end hides (two frames,
	// the first or the last of them cut) is at the edge of what names it
	for i := range cs {
		c := &cs[i]
		n := len(c.c1)
		for _, cut := range [][2]int{{n / 20, n}, {0, n - n/20}, {n / 20, n - n/20}} {
			r, _ := AutodetectFast(c.c1[cut[0]:cut[1]], c.c2[cut[0]:cut[1]], c.colT, "hex")
			if !r.OK || r.Proto != c.proto {
				c.fragile = true
			}
		}
	}
	return cs
}

// fuzzMutation is what a mutation did, and how hard: mild (a clean record,
// cut a little: the bus must be named), moderate (a real one: noise and
// swing a front end gives, cut anywhere, a distractor, decimated by 2: no
// other bus may be named) or heavy (past that: no other bus may be named
// that the scope would keep, decimatedStands).
type fuzzMutation struct {
	noise, edge, swing, offset float64
	cutHead, cutTail           float64
	decim                      int
	distractor                 string
	mild, moderate             bool
}

// TRLC-LINKS: REQ-SDS-018
func (m fuzzMutation) String() string {
	return fmt.Sprintf("noise %.1f edge %.2f swing %.2f offset %+.0f cut %.2f/%.2f decim %d distractor %q",
		m.noise, m.edge, m.swing, m.offset, m.cutHead, m.cutTail, m.decim, m.distractor)
}

// fuzzMutate is c mutated by a random mutation of rng (mild with odds
// mildShare: a little noise and edge, no cut past a tenth, no decimation).
// TRLC-LINKS: REQ-SDS-018
func fuzzMutate(rng *rand.Rand, c detectCase) (detectCase, fuzzMutation) {
	m := fuzzMutation{mild: rng.Float64() < 0.5}
	if m.mild {
		m.noise, m.edge, m.swing = rng.Float64()*2, 0.6+rng.Float64()*0.4, 0.8+rng.Float64()*0.2
		m.cutHead, m.cutTail = rng.Float64()*0.05, rng.Float64()*0.05
		m.decim = 1
	} else if m.moderate = rng.Float64() < 0.6; m.moderate {
		m.noise, m.edge, m.swing = rng.Float64()*4, 0.4+rng.Float64()*0.6, 0.5+rng.Float64()*0.5
		m.offset = (rng.Float64() - 0.5) * 40
		m.cutHead, m.cutTail = rng.Float64()*0.4, rng.Float64()*0.4
		m.decim = []int{1, 1, 2}[rng.Intn(3)]
	} else {
		m.noise, m.edge, m.swing = rng.Float64()*8, 0.15+rng.Float64()*0.85, 0.3+rng.Float64()*0.7
		m.offset = (rng.Float64() - 0.5) * 60
		m.cutHead, m.cutTail = rng.Float64()*0.4, rng.Float64()*0.4
		m.decim = []int{1, 2, 4, 8}[rng.Intn(4)]
	}
	idle := [2]bool{isIdle(c.c1), isIdle(c.c2)}
	if !m.mild && (idle[0] || idle[1]) && rng.Intn(3) == 0 {
		m.distractor = []string{"noise", "square", "telegraph"}[rng.Intn(3)]
	}
	n := len(c.c1)
	h, tl := int(m.cutHead*float64(n)), int(m.cutTail*float64(n))
	if h+tl > n*7/10 {
		tl = n*7/10 - h
	}
	out := c
	out.colT *= float64(m.decim)
	chans := [2][]uint8{c.c1, c.c2}
	for k := 0; k < 2; k++ {
		src := chans[k][h : n-tl]
		if idle[k] && m.distractor != "" {
			src = distractor(rng, m.distractor, len(src))
		}
		chans[k] = shapeChannel(rng, src, m)
	}
	out.c1, out.c2 = chans[0], chans[1]
	return out, m
}

// isIdle reports a flat channel (idleLike's).
// TRLC-LINKS: REQ-SDS-018
func isIdle(c []uint8) bool {
	for _, v := range c {
		if v != c[0] {
			return false
		}
	}
	return true
}

// distractor is a channel of kind: noise about mid-scale, a free-running
// square wave, or a random telegraph (two levels, edges at random).
// TRLC-LINKS: REQ-SDS-018
func distractor(rng *rand.Rand, kind string, n int) []uint8 {
	out := make([]uint8, n)
	switch kind {
	case "noise":
		for i := range out {
			out[i] = uint8(min(255, max(0, 128+rng.NormFloat64()*6)))
		}
	case "square":
		half := 5 + rng.Intn(200)
		for i := range out {
			out[i] = oracleLo
			if i/half%2 == 1 {
				out[i] = oracleHi
			}
		}
	case "telegraph":
		hi := true
		for i := range out {
			if rng.Intn(150) == 0 {
				hi = !hi
			}
			out[i] = oracleLo
			if hi {
				out[i] = oracleHi
			}
		}
	}
	return out
}

// shapeChannel is c as the mutation's front end records it: a slow edge
// (first-order, edge the share a sample moves), the swing and offset about
// mid-scale, noise, then box-averaged by decim.
// TRLC-LINKS: REQ-SDS-018
func shapeChannel(rng *rand.Rand, c []uint8, m fuzzMutation) []uint8 {
	if len(c) == 0 {
		return c
	}
	f := make([]float64, len(c))
	prev := float64(c[0])
	for i, v := range c {
		prev += (float64(v) - prev) * m.edge
		f[i] = 128 + (prev-128)*m.swing + m.offset + rng.NormFloat64()*m.noise
	}
	d := max(1, m.decim)
	out := make([]uint8, 0, len(f)/d+1)
	for i := 0; i+d <= len(f); i += d {
		s := 0.0
		for k := 0; k < d; k++ {
			s += f[i+k]
		}
		out = append(out, uint8(min(255, max(0, math.Round(s/float64(d))))))
	}
	return out
}

// fuzzFinding is one rule a mutated capture broke.
type fuzzFinding struct {
	rule, name, got string
	seed            int64
	m               fuzzMutation
}

// fuzzRun runs mutations seed0..seed0+n-1 over the corpus (case seed%len),
// in parallel, and returns the findings.
// TRLC-LINKS: REQ-SDS-018
func fuzzRun(corpus []fuzzCase, seed0 int64, n int) []fuzzFinding {
	var mu sync.Mutex
	var out []fuzzFinding
	jobs := make(chan int64)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seed := range jobs {
				if f, ok := fuzzOne(corpus, seed); ok {
					mu.Lock()
					out = append(out, f)
					mu.Unlock()
				}
			}
		}()
	}
	for s := seed0; s < seed0+int64(n); s++ {
		jobs <- s
	}
	close(jobs)
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].seed < out[j].seed })
	return out
}

// fuzzOne mutates the corpus's case for seed (a noise or idle capture on one
// seed in ten) and judges the detection's name by the three rules.
// TRLC-LINKS: REQ-SDS-018
func fuzzOne(corpus []fuzzCase, seed int64) (fuzzFinding, bool) {
	rng := rand.New(rand.NewSource(seed))
	if seed%10 == 9 { // nothing on either channel: no bus
		n := 20000 + rng.Intn(80000)
		kinds := []string{"noise", "flat", "noise+flat"}
		k := kinds[rng.Intn(len(kinds))]
		a, b := distractor(rng, "noise", n), distractor(rng, "noise", n)
		if k != "noise" {
			b = idleLike(b)
		}
		if k == "flat" {
			a = idleLike(a)
		}
		r, _ := AutodetectFast(a, b, 1e-8*float64(1+rng.Intn(1000)), "hex")
		if r.OK && r.Proto != "off" {
			return fuzzFinding{"named a bus on " + k, k, r.Proto, seed, fuzzMutation{}}, true
		}
		return fuzzFinding{}, false
	}
	c := corpus[int(seed)%len(corpus)]
	mc, m := fuzzMutate(rng, c.detectCase)
	r, _ := AutodetectFast(mc.c1, mc.c2, mc.colT, "hex")
	named := r.OK && r.Proto != "off"
	// a find on a decimated record stands at six samples of the bus's
	// shortest feature, as the scope's search keeps it (panel decimatedStands)
	stands := m.decim <= 1 || FeatureSamples(r) >= 6
	switch {
	case named && stands && r.Proto != c.proto && !alikeName(c.proto, r.Proto) && cleanNamesIt(c.detectCase, m):
		rule := "named another bus (heavy)"
		if m.mild || m.moderate {
			rule = "named another bus"
		}
		return fuzzFinding{rule, c.name, r.Proto + " (" + r.Src + ")", seed, m}, true
	case m.mild && !c.fragile && (!named || r.Proto != c.proto):
		return fuzzFinding{"missed a mild one", c.name, r.Proto + " " + r.Error, seed, m}, true
	case m.mild && !c.fragile && r.Roles.ChA != c.chA && c.proto != "quad": // either line of an encoder may be A
		return fuzzFinding{"named the wrong line", c.name, fmt.Sprintf("C%d", r.Roles.ChA+1), seed, m}, true
	}
	return fuzzFinding{}, false
}

// TestAutodetectFuzzCampaign runs the campaign: SDS_AUTO_FUZZ mutations (24
// by default) from SDS_AUTO_FUZZ_SEED; it lists every finding by rule and
// case and fails on any. SDS_AUTO_FUZZ_ONE=seed replays one.
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectFuzzCampaign(t *testing.T) {
	corpus := fuzzCorpus(t)
	if s := os.Getenv("SDS_AUTO_FUZZ_ONE"); s != "" {
		seed, _ := strconv.ParseInt(s, 10, 64)
		f, bad := fuzzOne(corpus, seed)
		t.Logf("seed %d: case %s, finding %v %+v", seed, corpus[int(seed)%len(corpus)].name, bad, f)
		return
	}
	n, seed0 := 24, int64(0)
	if v, err := strconv.Atoi(os.Getenv("SDS_AUTO_FUZZ")); err == nil {
		n = v
	}
	if v, err := strconv.ParseInt(os.Getenv("SDS_AUTO_FUZZ_SEED"), 10, 64); err == nil {
		seed0 = v
	}
	findings := fuzzRun(corpus, seed0, n)
	groups := map[string][]fuzzFinding{}
	for _, f := range findings {
		k := f.rule + " | " + f.name + " -> " + strings.Fields(f.got + " ")[0]
		groups[k] = append(groups[k], f)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := groups[k]
		t.Logf("%4d  %s  (seed %d: %s)", len(g), k, g[0].seed, g[0].m)
	}
	t.Logf("%d mutations of %d captures, %d findings in %d groups", n, len(corpus), len(findings), len(groups))
	if len(findings) > 0 {
		t.Errorf("%d findings", len(findings))
	}
}
