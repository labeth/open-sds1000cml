// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// lineTr is one transition of a logic line: its time and new level.
type lineTr struct {
	t  float64
	up bool
}

// lineSet is two logic lines as the converters see them: each line's
// transitions and idle level, its rise and fall times (10-90 %), the
// converters' own offsets, noise, C1 sampled at even and C2 at odd
// nanoseconds plus the bench's 0.44 ns; shift moves a channel's codes (the
// channels at different offsets).
type lineSet struct {
	tr         [2][]lineTr
	idle       [2]bool
	endS       float64
	rise, fall [2]float64
	noise      float64
	seed       int64
	inverted   bool
	shift      [2]float64
}

// TRLC-LINKS: REQ-SDS-229
func (p lineSet) words() []uint64 {
	n := int(p.endS/2e-9) + 64
	n = (n + 3) &^ 3
	offsets := [2][5]float64{{0, 2, 11, 11, 0}, {0, 4, 3, 4, 3}}
	rng := rand.New(rand.NewSource(p.seed))
	codes := [2][]uint8{make([]uint8, n), make([]uint8, n)}
	for ch := 0; ch < 2; ch++ {
		list, idle := p.tr[ch], p.idle[ch]
		sigma := func(up bool) float64 {
			if up {
				return math.Max(p.rise[ch], 0.3e-9) / 2.563
			}
			return math.Max(p.fall[ch], 0.3e-9) / 2.563
		}
		wide := 6 * math.Max(sigma(true), sigma(false))
		k := 0
		for i := 0; i < n; i++ {
			at := float64(i) * 2e-9
			if ch == 1 {
				at += synthPhaseS
			}
			for k < len(list) && list[k].t < at-wide {
				idle = list[k].up
				k++
			}
			v := 0.0
			if idle {
				v = 1
			}
			for j := k; j < len(list) && list[j].t < at+wide; j++ {
				s := sigma(list[j].up)
				step := 0.5 * (1 + math.Erf((at-list[j].t)/(s*math.Sqrt2)))
				if list[j].up {
					v += step
				} else {
					v -= step
				}
			}
			if p.inverted {
				v = 1 - v
			}
			code := 70 + 100*v + p.shift[ch] + offsets[ch][i%5] + rng.NormFloat64()*p.noise
			codes[ch][i] = uint8(math.Max(0, math.Min(255, math.Round(code))))
		}
	}
	wide := make([]uint64, n/4)
	for k := range wide {
		for j := 0; j < 4; j++ {
			wide[k] |= uint64(codes[0][4*k+j])<<(16*j) | uint64(codes[1][4*k+j])<<(16*j+8)
		}
	}
	return wide
}

// TRLC-LINKS: REQ-SDS-229
func addTr(list *[]lineTr, level *bool, at float64, v bool) {
	if *level != v {
		*list = append(*list, lineTr{at, v})
		*level = v
	}
}

// uartFrame is one character on the line: its value, and errors to send.
type uartFrame struct {
	value             int
	badParity, noStop bool
	gapBits           float64 // idle after the stop bit(s)
}

// uartBus is a UART line: format, baud and the transmitter's baud error.
type uartBus struct {
	baud, err    float64 // the transmitter runs at baud*(1+err)
	bits, parity int
	stop         int
	inverted     bool
	ch           int
	frames       []uartFrame
	rise, noise  float64
	seed         int64
}

// TRLC-LINKS: REQ-SDS-229
func (u uartBus) lines() lineSet {
	T := 1 / (u.baud * (1 + u.err))
	var list []lineTr
	lvl := true
	t := 2e-6 + 3*T
	for _, f := range u.frames {
		bits := []bool{false}
		ones := 0
		for i := 0; i < u.bits; i++ {
			b := f.value>>i&1 != 0
			bits = append(bits, b)
			if b {
				ones++
			}
		}
		if u.parity != 0 {
			p := ones&1 != 0 // even parity: the bit makes the count even
			if u.parity == 2 {
				p = !p
			}
			bits = append(bits, p != f.badParity)
		}
		for i := 0; i < u.stop; i++ {
			bits = append(bits, !f.noStop)
		}
		for i, b := range bits {
			addTr(&list, &lvl, t+float64(i)*T, b)
		}
		t += float64(len(bits)) * T
		if f.noStop { // the line returns high after a bit of break
			addTr(&list, &lvl, t, true)
			t += T
		}
		t += f.gapBits * T
	}
	set := lineSet{endS: t + 2e-6, rise: [2]float64{u.rise, u.rise}, fall: [2]float64{u.rise, u.rise}, noise: u.noise, seed: u.seed, inverted: u.inverted}
	set.tr[u.ch], set.idle = list, [2]bool{true, true}
	return set
}

// TRLC-LINKS: REQ-SDS-229
func (u uartBus) config() SampleUARTConfig {
	return SampleUARTConfig{Ch: u.ch, Level: 120, Low: 107, High: 133, Inverted: u.inverted, Period16: UARTPeriod16(u.baud),
		Bits: u.bits, Parity: u.parity, Resync: true}
}

// want is the events the receiver publishes for the frames: their data, or
// an error.
// TRLC-LINKS: REQ-SDS-229
func (u uartBus) want() []SampleBusEvent {
	var out []SampleBusEvent
	for _, f := range u.frames {
		if f.noStop || (f.badParity && u.parity != 0) {
			out = append(out, SampleBusEvent{Kind: 4})
		} else {
			out = append(out, SampleBusEvent{Kind: 2, Data: uint16(f.value & (1<<u.bits - 1))})
		}
	}
	return out
}

// uartFrames is a run of characters with every bit pattern edge case,
// back to back and with gaps, and an error of each kind.
// TRLC-LINKS: REQ-SDS-229
func uartFrames(bits int, seed int64) []uartFrame {
	rng := rand.New(rand.NewSource(seed))
	vals := []int{0x00, 0x1ff, 0x55, 0xaa, 0x01, 0x100, 0x80, 0x7f, 0x48, 0x0f, 0xf0}
	var fs []uartFrame
	for i := 0; i < 28; i++ {
		v := rng.Intn(512)
		if i < len(vals) {
			v = vals[i]
		}
		f := uartFrame{value: v & (1<<bits - 1)}
		switch {
		case i%7 == 3:
			f.gapBits = 0.5 + 3*rng.Float64()
		case i == 13:
			f.badParity = true
		case i == 20:
			f.noStop, f.value = true, 0
		}
		fs = append(fs, f)
	}
	return fs
}

type uartFormat struct {
	bits, parity, stop int
	inverted           bool
}

// uartCases: every rate from 115200 Bd to the receiver's 62.5 MBd, in
// every data width and parity, inverted, on either channel, with the
// transmitter 2 % fast and slow.
// TRLC-LINKS: REQ-SDS-229
func uartCases() []uartBus {
	formats := []uartFormat{{8, 0, 1, false}, {7, 1, 1, false}, {8, 2, 1, false}, {9, 0, 1, false}, {5, 0, 2, false}, {8, 1, 1, true}}
	var cases []uartBus
	for i, baud := range []float64{115200, 1e6, 10e6, 25e6, 50e6, 62.5e6} {
		for j, f := range formats {
			for _, e := range []float64{-0.02, 0, 0.02} {
				u := uartBus{baud: baud, err: e, bits: f.bits, parity: f.parity, stop: f.stop, inverted: f.inverted, ch: (i + j) % 2,
					rise: 3e-9, noise: 1, seed: int64(100*i + 10*j + int(e*100))}
				u.frames = uartFrames(f.bits, u.seed)
				cases = append(cases, u)
			}
		}
	}
	return cases
}

// TRLC-LINKS: REQ-SDS-229
func uartName(u uartBus) string {
	p := "NEO"[u.parity : u.parity+1]
	return fmt.Sprintf("%.0fBd/%d%s%d/inv%v/C%d/err%+.0f%%", u.baud, u.bits, p, u.stop, u.inverted, u.ch+1, 100*u.err)
}

// TRLC-LINKS: REQ-SDS-229
func stripEvents(ev []SampleBusEvent) []SampleBusEvent {
	out := make([]SampleBusEvent, len(ev))
	for i, e := range ev {
		out[i] = SampleBusEvent{Kind: e.Kind, Data: e.Data, Meta: e.Meta}
	}
	return out
}

// The UART receiver reads every synthetic line exactly: each character, and
// an error where the frame has one.
// TRLC-LINKS: REQ-SDS-229
func TestSampleUARTSynthetic(t *testing.T) {
	for _, u := range uartCases() {
		t.Run(uartName(u), func(t *testing.T) {
			ev := SampleUART(u.lines().words(), u.config())
			if got, want := stripEvents(ev), u.want(); !reflect.DeepEqual(got, want) {
				t.Fatalf("events\n%v\nwant\n%v", got, want)
			}
		})
	}
}

// Without resynchronising, the receiver holds a 2 % baud error over a
// frame of nine data bits and parity.
// TRLC-LINKS: REQ-SDS-229
func TestSampleUARTNoResync(t *testing.T) {
	for _, e := range []float64{-0.02, 0.02} {
		u := uartBus{baud: 50e6, err: e, bits: 9, parity: 1, stop: 1, rise: 3e-9, noise: 1, seed: 7}
		u.frames = uartFrames(9, 7)
		c := u.config()
		c.Resync = false
		if got, want := stripEvents(SampleUART(u.lines().words(), c)), u.want(); !reflect.DeepEqual(got, want) {
			t.Fatalf("err %+.0f%%: events\n%v\nwant\n%v", 100*e, got, want)
		}
	}
}

// The byte trigger fires on the character that completes the pattern, and
// only there.
// TRLC-LINKS: REQ-SDS-229
func TestSampleUARTMatch(t *testing.T) {
	u := uartBus{baud: 50e6, bits: 8, stop: 1, rise: 3e-9, noise: 1, seed: 3}
	for _, s := range []string{"OPEN-SDS", "UART", "SDS!"} {
		for _, b := range []byte(s) {
			u.frames = append(u.frames, uartFrame{value: int(b)})
		}
		u.frames[len(u.frames)-1].gapBits = 4
	}
	c := u.config()
	c.Pattern, c.Length = 0x2d534453, 4 // "-SDS"
	var matched []int
	for i, e := range SampleUART(u.lines().words(), c) {
		if e.Match {
			matched = append(matched, i)
		}
	}
	if !reflect.DeepEqual(matched, []int{7}) {
		t.Fatalf("matches at characters %v, want [7]", matched)
	}
}

// i2cBus is an I2C bus as a master and its devices drive it: SCL on
// clockCh, SDA on the other channel; the clock's low and high times, SDA's
// hold after SCL falls, and the lines' rise and fall times.
type i2cBus struct {
	rateHz     float64
	hold       float64 // SDA's change after SCL falls
	rise, fall float64
	clockCh    int
	inverted   bool
	noise      float64
	seed       int64
	transfers  [][]i2cOp
	shift      [2]float64 // each channel's codes moved (C1, C2)
}

// i2cOp is a START (repeated when inside a transfer), a byte with the
// acknowledge bit driven after it, or a STOP.
type i2cOp struct {
	kind byte // 'S', 'B', 'P'
	b    byte
	nack bool
}

// i2cTransfers: a register write then a read with a repeated START, a
// ten-bit-address write, and a NACKed address.
// TRLC-LINKS: REQ-SDS-229
func i2cTransfers(seq byte) [][]i2cOp {
	B := func(b byte) i2cOp { return i2cOp{kind: 'B', b: b} }
	S, P := i2cOp{kind: 'S'}, i2cOp{kind: 'P'}
	read := []i2cOp{S, B(0xa0), B(0x00), B(0x10), S, B(0xa1)}
	for i := 0; i < 8; i++ {
		read = append(read, i2cOp{kind: 'B', b: seq + byte(i), nack: i == 7})
	}
	read = append(read, P)
	return [][]i2cOp{read,
		{S, B(0xf4), B(0xa5), B('H'), B('S'), P}, // ten-bit address 0x2a5, write
		{S, {kind: 'B', b: 0x78, nack: true}, P},
	}
}

// TRLC-LINKS: REQ-SDS-229
func (b i2cBus) lines() lineSet {
	period := 1 / b.rateHz
	low, high := period/2, period/2
	var scl, sda []lineTr
	cl, da := true, true
	t := 2e-6
	for _, tr := range b.transfers {
		for _, op := range tr {
			switch op.kind {
			case 'S':
				if !cl { // repeated START: SDA high while SCL is low, then SCL high
					addTr(&sda, &da, t+b.hold, true)
					addTr(&scl, &cl, t+low, true)
					t += low
				}
				addTr(&sda, &da, t+high/2, false)
				addTr(&scl, &cl, t+high, false)
				t += high
			case 'B':
				for i := 0; i < 9; i++ {
					bit := op.nack
					if i < 8 {
						bit = op.b>>(7-i)&1 != 0
					}
					addTr(&sda, &da, t+b.hold, bit)
					addTr(&scl, &cl, t+low, true)
					addTr(&scl, &cl, t+low+high, false)
					t += low + high
				}
			case 'P':
				addTr(&sda, &da, t+b.hold, false)
				addTr(&scl, &cl, t+low, true)
				addTr(&sda, &da, t+low+high/2, true)
				t += low + high
			}
		}
		t += 4 * period
	}
	set := lineSet{endS: t + 2e-6, rise: [2]float64{b.rise, b.rise}, fall: [2]float64{b.fall, b.fall}, noise: b.noise, seed: b.seed, inverted: b.inverted, shift: b.shift}
	set.tr[b.clockCh], set.tr[1-b.clockCh] = scl, sda
	set.idle = [2]bool{true, true}
	return set
}

// TRLC-LINKS: REQ-SDS-229
func (b i2cBus) config() SampleI2CConfig {
	delay := int(math.Round(synthPhaseS * 16e9))
	if b.clockCh == 0 {
		delay = -delay
	}
	scl, sda := uint8(120+b.shift[b.clockCh]), uint8(120+b.shift[1-b.clockCh])
	return SampleI2CConfig{ClockCh: b.clockCh, Level: scl, Low: scl - 13, High: scl + 13, SDALevel: sda, SDALow: sda - 13, SDAHigh: sda + 13,
		Inverted: b.inverted, Delay: delay, Address: -1, Direction: 2}
}

// want is the events the bus carries: START, each byte with its address
// and NACK flags, STOP.
// TRLC-LINKS: REQ-SDS-229
func (b i2cBus) want() []SampleBusEvent {
	var out []SampleBusEvent
	for _, tr := range b.transfers {
		addr := false
		for _, op := range tr {
			switch op.kind {
			case 'S':
				out = append(out, SampleBusEvent{Kind: 1})
				addr = true
			case 'B':
				meta := uint8(0)
				if addr {
					meta = 1
				}
				if op.nack {
					meta |= 2
				}
				out = append(out, SampleBusEvent{Kind: 2, Data: uint16(op.b), Meta: meta})
				addr = false
			case 'P':
				out = append(out, SampleBusEvent{Kind: 3})
			}
		}
	}
	return out
}

// i2cCases: standard, fast, fast-plus and high-speed mode with open-drain
// rise times at each mode's limit, push-pull edges, SDA held 5 ns after SCL
// falls at 3.4 MHz, inverted, and SCL on either channel.
// TRLC-LINKS: REQ-SDS-229
func i2cCases() []i2cBus {
	var cases []i2cBus
	for i, m := range []struct{ rate, rise float64 }{{100e3, 1000e-9}, {400e3, 300e-9}, {1e6, 120e-9}, {3.4e6, 40e-9}} {
		for j, edge := range []float64{m.rise, 3e-9} {
			b := i2cBus{rateHz: m.rate, hold: 0.25 / m.rate, rise: edge, fall: math.Min(edge, 10e-9), clockCh: (i + j) % 2, inverted: i == 2 && j == 1,
				noise: 1, seed: int64(10*i + j)}
			b.transfers = i2cTransfers(byte(16 * i))
			cases = append(cases, b)
		}
	}
	tight := i2cBus{rateHz: 3.4e6, hold: 5e-9, rise: 10e-9, fall: 5e-9, clockCh: 0, noise: 1, seed: 99}
	tight.transfers = i2cTransfers(0x70)
	// the lines apart on the screen: each sliced at its own level, neither
	// line's swing reaching the other's
	apart := i2cBus{rateHz: 3.4e6, hold: 0.25 / 3.4e6, rise: 40e-9, fall: 10e-9, clockCh: 1, noise: 1, seed: 98, shift: [2]float64{75, -60}}
	apart.transfers = i2cTransfers(0x60)
	slow := i2cBus{rateHz: 400e3, hold: 0.25 / 400e3, rise: 300e-9, fall: 10e-9, clockCh: 0, noise: 1, seed: 97, shift: [2]float64{-60, 75}}
	slow.transfers = i2cTransfers(0x30)
	return append(cases, tight, apart, slow)
}

// TRLC-LINKS: REQ-SDS-229
func i2cName(b i2cBus) string {
	return fmt.Sprintf("%.0fkHz/rise%.0fns/hold%.0fns/inv%v/SCL C%d/shift%v", b.rateHz/1e3, b.rise*1e9, b.hold*1e9, b.inverted, b.clockCh+1, b.shift)
}

// The I2C receiver reads every synthetic bus exactly.
// TRLC-LINKS: REQ-SDS-229
func TestSampleI2CSynthetic(t *testing.T) {
	for _, b := range i2cCases() {
		t.Run(i2cName(b), func(t *testing.T) {
			ev, _ := SampleI2C(b.lines().words(), b.config())
			if got, want := stripEvents(ev), b.want(); !reflect.DeepEqual(got, want) {
				t.Fatalf("events\n%v\nwant\n%v", got, want)
			}
		})
	}
}

// The address and data triggers fire on the byte that completes them.
// TRLC-LINKS: REQ-SDS-229
func TestSampleI2CMatch(t *testing.T) {
	b := i2cCases()[6] // 3.4 MHz
	words := b.lines().words()
	type want struct {
		address, direction, length int
		pattern                    uint32
		byteIndex                  []int // the data events (in order) whose eighth bit matched
	}
	for _, w := range []want{
		{0x50, 1, 0, 0, []int{3}},      // the read's address
		{0x50, 0, 2, 0x0010, []int{2}}, // the register address 00 10
		{-1, 2, 2, 0x4853, []int{15}},  // "HS" after the ten-bit address
		{0x7a, 0, 1, 'H', nil},         // ten-bit address high byte: 11110 10 is 0x7a, a write
		{0x3c, 0, 0, 0, []int{16}},     // the NACKed address
		{0x50, 2, 3, 0x123456, nil},    // not on the bus
	} {
		c := b.config()
		c.Address, c.Direction, c.Length, c.Pattern = w.address, w.direction, w.length, w.pattern
		ev, matches := SampleI2C(words, c)
		var at []int
		for _, m := range matches {
			// the data event whose eighth bit this was: the next one
			n := 0
			for _, e := range ev {
				if e.Kind == 2 {
					if e.Clock > m {
						break
					}
					n++
				}
			}
			at = append(at, n)
		}
		if w.address == 0x7a {
			w.byteIndex = []int{14} // "H": the first data byte after the ten-bit address's second byte
		}
		if !reflect.DeepEqual(at, w.byteIndex) {
			t.Fatalf("%+v: matches at data bytes %v, want %v", w, at, w.byteIndex)
		}
	}
}

// rxEdge is an extractor's edge as the testbench prints it.
type rxEdge struct {
	Clock, Time   int
	Rising, Other bool
	Levels        int
}

// rxRun is what tb_sample_rx.v prints: both extractors' edges, the
// receiver's events and its matches.
type rxRun struct {
	a, b    []rxEdge
	events  []SampleBusEvent
	matches []int
}

// TRLC-LINKS: REQ-SDS-229
func (s rtlSim) runRx(t *testing.T, words []uint64, args ...string) rxRun {
	t.Helper()
	var in strings.Builder
	for _, w := range words {
		fmt.Fprintf(&in, "%016x\n", w)
	}
	path := filepath.Join(s.dir, "words.mem")
	if err := os.WriteFile(path, []byte(in.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd("vvp", append([]string{"-N", s.image, "+input=" + path, fmt.Sprintf("+n=%d", len(words))}, args...)...)
	if err != nil {
		t.Fatalf("simulate: %v\n%s", err, out)
	}
	var r rxRun
	done := false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var a, b, c, d, e int
		switch {
		case strings.HasPrefix(line, "EA "), strings.HasPrefix(line, "EB "):
			if _, err := fmt.Sscanf(line[3:], "%d %d %d %d %d", &a, &b, &c, &d, &e); err != nil {
				t.Fatal(err)
			}
			edge := rxEdge{Clock: a, Time: b, Rising: c != 0, Other: d != 0, Levels: e}
			if line[1] == 'A' {
				r.a = append(r.a, edge)
			} else {
				r.b = append(r.b, edge)
			}
		case strings.HasPrefix(line, "EV "):
			var m int
			if _, err := fmt.Sscanf(line, "EV %d %d %d %d %d", &a, &b, &c, &d, &m); err != nil {
				t.Fatal(err)
			}
			r.events = append(r.events, SampleBusEvent{Clock: a, Kind: uint8(b), Data: uint16(c), Meta: uint8(d), Match: m != 0})
		case strings.HasPrefix(line, "M "):
			if _, err := fmt.Sscanf(line, "M %d", &a); err != nil {
				t.Fatal(err)
			}
			r.matches = append(r.matches, a)
		case strings.HasPrefix(line, "DONE "):
			done = true
		case strings.Contains(line, "$finish called"):
		default:
			t.Fatalf("unexpected simulator output %q", line)
		}
	}
	if !done {
		t.Fatal("simulation did not finish")
	}
	return r
}

// modelEdges is the model's extractor output as the testbench prints it:
// each edge on the clock it leaves the extractor.
// TRLC-LINKS: REQ-SDS-229
func modelEdges(out []SampleLineEdge) []rxEdge {
	var edges []rxEdge
	for k, o := range out {
		if !o.Found {
			continue
		}
		lv := 0
		for j, l := range o.Levels {
			if l {
				lv |= 1 << j
			}
		}
		edges = append(edges, rxEdge{Clock: k + sampleEdgesLatency, Time: o.Time, Rising: o.Rising, Other: o.Other, Levels: lv})
	}
	return edges
}

// TRLC-LINKS: REQ-SDS-229
func sameEdges(t *testing.T, name string, rtl, model []rxEdge) {
	t.Helper()
	// the model runs to the words' end; the RTL's last clocks are not printed
	if len(model)-len(rtl) > 2 || len(rtl) > len(model) || !reflect.DeepEqual(rtl, model[:len(rtl)]) {
		i := 0
		for i < len(rtl) && i < len(model) && rtl[i] == model[i] {
			i++
		}
		t.Fatalf("%s edges differ at %d: RTL %d model %d\nRTL   %v\nmodel %v", name, i, len(rtl), len(model), rtl[i:min(len(rtl), i+4)], model[i:min(len(model), i+4)])
	}
}

// TRLC-LINKS: REQ-SDS-229
func sameEvents(t *testing.T, rtl, model []SampleBusEvent) {
	t.Helper()
	if !reflect.DeepEqual(rtl, model) {
		i := 0
		for i < len(rtl) && i < len(model) && rtl[i] == model[i] {
			i++
		}
		t.Fatalf("events differ at %d: RTL %d model %d\nRTL   %v\nmodel %v", i, len(rtl), len(model), rtl[i:min(len(rtl), i+4)], model[i:min(len(model), i+4)])
	}
}

// The RTL (sample_edges.v, uart_sample.v) finds the model's edges and
// publishes its events and matches on the same clocks.
// TRLC-LINKS: REQ-SDS-229
func TestSampleUARTRTL(t *testing.T) {
	sim := newRTLSim(t, "tb_sample_rx", "sample_edges.v", "uart_sample.v", "i2c_sample.v")
	for i, u := range uartCases() {
		if u.baud < 1e6 || (u.baud < 25e6 && i%3 != 0) {
			continue // long; 1 MBd and above in every format cover it
		}
		t.Run(uartName(u), func(t *testing.T) {
			words := u.lines().words()
			c := u.config()
			c.Pattern, c.Length = 0x55aa01, 3
			c.Resync = i%2 == 0 || u.err == 0
			ev := SampleUART(words, c)
			r := sim.runRx(t, words, "+proto=1", fmt.Sprintf("+ch=%d", c.Ch), fmt.Sprintf("+level=%d", c.Level), fmt.Sprintf("+low=%d", c.Low),
				fmt.Sprintf("+high=%d", c.High), fmt.Sprintf("+inverted=%d", b2u8(c.Inverted)), fmt.Sprintf("+period=%d", c.Period16),
				fmt.Sprintf("+bits=%d", c.Bits), fmt.Sprintf("+parity=%d", c.Parity), fmt.Sprintf("+resync=%d", b2u8(c.Resync)),
				fmt.Sprintf("+pattern=%d", c.Pattern), fmt.Sprintf("+len=%d", c.Length))
			sameEdges(t, "line", r.a, modelEdges(SampleEdges(words, SampleLineConfig{Ch: c.Ch, Rise: true, Fall: true, Level: c.Level, Low: c.Low, High: c.High, OtherLevel: c.Level})))
			sameEvents(t, r.events, ev)
			if len(ev) == 0 {
				t.Fatal("no events")
			}
		})
	}
}

// The RTL (two sample_edges.v, i2c_sample.v) finds the model's edges on
// both lines and publishes its events and matches on the same clocks.
// TRLC-LINKS: REQ-SDS-229
func TestSampleI2CRTL(t *testing.T) {
	sim := newRTLSim(t, "tb_sample_rx", "sample_edges.v", "uart_sample.v", "i2c_sample.v")
	for i, b := range i2cCases() {
		if b.rateHz < 1e6 && i%2 == 1 {
			continue // long; the slow edges at 100 and 400 kHz cover it
		}
		t.Run(i2cName(b), func(t *testing.T) {
			words := b.lines().words()
			c := b.config()
			c.Address, c.Direction, c.Pattern, c.Length = 0x50, 0, 0x0010, 2
			if i%2 == 1 {
				c.Address, c.Direction, c.Length = -1, 1, 0
			}
			ev, matches := SampleI2C(words, c)
			if len(matches) == 0 {
				t.Fatal("no matches")
			}
			r := sim.runRx(t, words, "+proto=2", fmt.Sprintf("+ch=%d", c.ClockCh), fmt.Sprintf("+level=%d", c.Level), fmt.Sprintf("+low=%d", c.Low),
				fmt.Sprintf("+high=%d", c.High), fmt.Sprintf("+level2=%d", c.SDALevel), fmt.Sprintf("+low2=%d", c.SDALow), fmt.Sprintf("+high2=%d", c.SDAHigh),
				fmt.Sprintf("+inverted=%d", b2u8(c.Inverted)), fmt.Sprintf("+delay=%d", c.Delay&0xff),
				fmt.Sprintf("+address=%d", c.Address), fmt.Sprintf("+direction=%d", c.Direction), fmt.Sprintf("+pattern=%d", c.Pattern), fmt.Sprintf("+len=%d", c.Length))
			sameEdges(t, "SCL", r.a, modelEdges(SampleEdges(words, SampleLineConfig{Ch: c.ClockCh, Rise: !c.Inverted, Fall: c.Inverted, Level: c.Level, Low: c.Low, High: c.High, OtherLevel: c.SDALevel, Delay: c.Delay})))
			sameEdges(t, "SDA", r.b, modelEdges(SampleEdges(words, SampleLineConfig{Ch: 1 - c.ClockCh, Rise: true, Fall: true, Level: c.SDALevel, Low: c.SDALow, High: c.SDAHigh, OtherLevel: c.Level, Delay: wrap8(-c.Delay)})))
			sameEvents(t, r.events, ev)
			if !reflect.DeepEqual(r.matches, matches) {
				t.Fatalf("matches RTL %v model %v", r.matches, matches)
			}
		})
	}
}

// uartCapture is a record of the UART bench source taken by the instrument
// (10x probes, 1 V/div): C1 carries the line, C2 its inverse (idle low).
// The source sends bursts of "OPEN-SDS UART!\r\n" and the counting
// characters n..n+15 (bit 8 alternating at nine bits), n stepping a burst.
type uartCapture struct {
	file         string
	baud         float64
	bits, parity int
}

var uartCaptures = []uartCapture{
	{"uart_sample_1mbd.bin.gz", 1e6, 8, 0}, {"uart_sample_10mbd.bin.gz", 10e6, 8, 0}, {"uart_sample_25mbd.bin.gz", 25e6, 8, 0},
	{"uart_sample_50mbd.bin.gz", 50e6, 8, 0}, {"uart_sample_62mbd.bin.gz", 62.5e6, 8, 0}, {"uart_sample_62mbd_7e1.bin.gz", 62.5e6, 7, 1},
	{"uart_sample_62mbd_9o1.bin.gz", 62.5e6, 9, 2}, {"uart_sample_62mbd_8n2.bin.gz", 62.5e6, 8, 0},
}

// uartBenchBurst is the source's burst n at the given data width.
// TRLC-LINKS: REQ-SDS-229
func uartBenchBurst(n, bits int) []int {
	var out []int
	for _, c := range "OPEN-SDS UART!\r\n" {
		out = append(out, int(c))
	}
	for i := 0; i < 16; i++ {
		v := (n + i) & 0xff
		if bits == 9 {
			v |= (i & 1) << 8
		}
		out = append(out, v&(1<<bits-1))
	}
	return out
}

// Every character of the recorded captures decodes, on both channels (C2
// inverted), from the first burst the receiver frames (a record opens
// inside back-to-back characters) to the record's end.
// TRLC-LINKS: REQ-SDS-229
func TestSampleUARTCaptures(t *testing.T) {
	for _, c := range uartCaptures {
		words := readCapture(t, c.file)
		for ch := 0; ch < 2; ch++ {
			t.Run(fmt.Sprintf("%s/C%d", c.file, ch+1), func(t *testing.T) {
				ev := SampleUART(words, SampleUARTConfig{Ch: ch, Level: 128, Low: 115, High: 141, Inverted: ch == 1,
					Period16: UARTPeriod16(c.baud), Bits: c.bits, Parity: c.parity, Resync: true})
				var got []int
				for _, e := range ev {
					v := int(e.Data)
					if e.Kind == 4 {
						v = -1
					}
					got = append(got, v)
				}
				// the first burst: its header, whose counting characters follow
				head := uartBenchBurst(0, c.bits)[:16]
				start := -1
				for i := 0; i+16 <= len(got) && start < 0; i++ {
					if reflect.DeepEqual(got[i:i+16], head) {
						start = i
					}
				}
				if start < 0 || start > 48 || len(got)-start < 17 {
					t.Fatalf("no burst framed: %d characters, header at %d", len(got), start)
				}
				n := got[start+16] & 0xff
				for i := start; i < len(got); i += 32 {
					want := uartBenchBurst(n, c.bits)
					end := min(len(got), i+32)
					if !reflect.DeepEqual(got[i:end], want[:end-i]) {
						t.Fatalf("burst at character %d: %v\nwant %v", i, got[i:end], want[:end-i])
					}
					n++
				}
				t.Logf("%d characters, 0 errors", len(got)-start)
			})
		}
	}
}

// i2cBenchPass is the I2C bench source's program for counter n, as tokens:
// S, P, and each byte as hex with "a" (address byte) and "N" (NACK).
// TRLC-LINKS: REQ-SDS-229
func i2cBenchPass(n int) []string {
	var out []string
	for _, tr := range i2cTransfers(byte(n)) {
		addr := false
		for _, op := range tr {
			switch op.kind {
			case 'S':
				out, addr = append(out, "S"), true
			case 'P':
				out = append(out, "P")
			case 'B':
				s := fmt.Sprintf("%02X", op.b)
				if addr {
					s += "a"
				}
				if op.nack {
					s += "N"
				}
				out, addr = append(out, s), false
			}
		}
	}
	return out
}

// TRLC-LINKS: REQ-SDS-229
func i2cTokens(ev []SampleBusEvent) []string {
	var out []string
	for _, e := range ev {
		switch e.Kind {
		case 1:
			out = append(out, "S")
		case 3:
			out = append(out, "P")
		case 2:
			s := fmt.Sprintf("%02X", e.Data)
			if e.Meta&1 != 0 {
				s += "a"
			}
			if e.Meta&2 != 0 {
				s += "N"
			}
			out = append(out, s)
		}
	}
	return out
}

// Records of the I2C bench source (SCL on C1, SDA on C2, 10x probes, push
// pull) at 400 kHz, 1 MHz and 3.4 MHz decode to its program exactly from the
// first START the record holds whole: a register write, a read with a
// repeated START and a NACKed last byte, a ten-bit-address write and a
// NACKed address, the read's data counting by eight each pass.
// TRLC-LINKS: REQ-SDS-229
func TestSampleI2CCaptures(t *testing.T) {
	for _, f := range []string{"i2c_sample_400khz.bin.gz", "i2c_sample_1mhz.bin.gz", "i2c_sample_3400khz.bin.gz"} {
		t.Run(f, func(t *testing.T) {
			ev, _ := SampleI2C(readCapture(t, f), SampleI2CConfig{ClockCh: 0, Level: 128, Low: 115, High: 141, SDALevel: 128, SDALow: 115, SDAHigh: 141, Delay: -ChannelPhase16, Address: -1, Direction: 2})
			got := i2cTokens(ev)
			// align on the first transfer start of a pass: a START then A0a (the
			// register write), F4a or 78aN
			pass := i2cBenchPass(0)
			at := -1
			for i := 0; i+1 < len(got) && at < 0; i++ {
				if got[i] == "S" && (got[i+1] == "A0a" || got[i+1] == "F4a" || got[i+1] == "78aN") {
					at = i
				}
			}
			if at < 0 || at > 40 {
				t.Fatalf("no transfer start: %v", got)
			}
			// the expected stream from that transfer: the program's passes, the
			// read's data giving n
			off := 0
			for off < len(pass) && !(pass[off] == "S" && pass[off+1] == got[at+1]) {
				off++
			}
			n := -1
			for i := at; i < len(got); i++ {
				if got[i] == "A1a" && i+1 < len(got) {
					fmt.Sscanf(got[i+1], "%02X", &n)
					break
				}
			}
			if n < 0 {
				t.Fatalf("no read in the record: %v", got)
			}
			if off > 5 { // the record opens after this pass's read: its data is the next pass's less 8
				n -= 8
			}
			var want []string
			for len(want) < len(got)-at+len(pass) {
				want = append(want, i2cBenchPass(n&0xff)...)
				n += 8
			}
			want = want[off:]
			if !reflect.DeepEqual(got[at:], want[:len(got)-at]) {
				t.Fatalf("events from %d:\n%v\nwant\n%v", at, got[at:], want[:len(got)-at])
			}
			t.Logf("%d events from the first transfer, 0 errors", len(got)-at)
		})
	}
}

// Detection names the recorded fast buses: a 62.5 MBd UART (not Manchester
// at half its rate) and high-speed-mode I2C.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-229
func TestAutodetectFastBuses(t *testing.T) {
	for _, c := range []struct{ file, proto, src string }{
		{"uart_sample_62mbd.bin.gz", "uart", "C1"}, {"uart_sample_50mbd.bin.gz", "uart", "C1"}, {"i2c_sample_3400khz.bin.gz", "i2c", "SCL C1 SDA C2"},
	} {
		c1, c2 := lines(readCapture(t, c.file))
		if r := Autodetect(c1, c2, 2e-9, "hex"); !r.OK || r.Proto != c.proto || r.Src != c.src {
			t.Fatalf("%s: %v %s %q %s", c.file, r.OK, r.Proto, r.Src, r.Error)
		}
	}
}
