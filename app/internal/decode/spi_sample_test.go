// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/rand"
	"open-sds/app/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// spiBus is an SPI bus as the converters see it: band-limited edges, the
// converters' own offsets, noise, C1 sampled at even and C2 at odd
// nanoseconds plus the bench's 0.44 ns.
type spiBus struct {
	rateHz          float64
	cpol, cpha, msb bool
	inverted        bool
	clockCh         int
	frames          [][]byte
	idleS           float64 // between frames, and before the first
	riseS           float64 // 10-90 %
	noise           float64 // codes rms
	seed            int64
}

// synthPhaseS is C2's sampling instant after C1's (the bench: 1.44 ns).
const synthPhaseS = 1.44e-9

// TRLC-LINKS: REQ-SDS-229
func (p spiBus) words() []uint64 {
	type tr struct {
		t  float64
		up bool
	}
	var clk, dat []tr
	period := 1 / p.rateHz
	t := p.idleS
	cl, dl := p.cpol, false
	set := func(list *[]tr, lvl *bool, at float64, v bool) {
		if *lvl != v {
			*list = append(*list, tr{at, v})
			*lvl = v
		}
	}
	for _, f := range p.frames {
		b := 0
		for _, by := range f {
			for i := 0; i < 8; i++ {
				bit := by>>(7-i)&1 != 0
				if !p.msb {
					bit = by>>i&1 != 0
				}
				start := t + float64(b)*period
				if !p.cpha {
					set(&dat, &dl, start, bit)
				}
				set(&clk, &cl, start+period/2, !p.cpol)
				if p.cpha {
					set(&dat, &dl, start+period/2, bit)
				}
				set(&clk, &cl, start+period, p.cpol)
				b++
			}
		}
		t += float64(b) * period
		set(&dat, &dl, t+period/2, false)
		t += p.idleS
	}
	n := int(t/2e-9) + 64
	n = (n + 3) &^ 3
	sigma := p.riseS / 2.563
	offsets := [2][5]float64{{0, 2, 11, 11, 0}, {0, 4, 3, 4, 3}}
	rng := rand.New(rand.NewSource(p.seed))
	codes := [2][]uint8{make([]uint8, n), make([]uint8, n)}
	for ch := 0; ch < 2; ch++ {
		list, idle := dat, false
		if ch == p.clockCh {
			list, idle = clk, p.cpol
		}
		k := 0
		for i := 0; i < n; i++ {
			at := float64(i) * 2e-9
			if ch == 1 {
				at += synthPhaseS
			}
			for k < len(list) && list[k].t < at-6*sigma {
				idle = list[k].up
				k++
			}
			v := 0.0
			if idle {
				v = 1
			}
			for j := k; j < len(list) && list[j].t < at+6*sigma; j++ {
				step := 0.5 * (1 + math.Erf((at-list[j].t)/(sigma*math.Sqrt2)))
				if list[j].up {
					v += step
				} else {
					v -= step
				}
			}
			if p.inverted {
				v = 1 - v
			}
			code := 70 + 100*v + offsets[ch][i%5] + rng.NormFloat64()*p.noise
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
func (p spiBus) config() SampleSPIConfig {
	delay := int(math.Round(synthPhaseS * 16e9))
	if p.clockCh == 0 {
		delay = -delay
	}
	return SampleSPIConfig{ClockCh: p.clockCh, Level: 120, Low: 107, High: 133, Inverted: p.inverted, CPOL: p.cpol, CPHA: p.cpha, MSB: p.msb,
		Delay: delay, GapClocks: SampleSPIGap(p.rateHz)}
}

// frameBytes groups the receiver's events into frames; a frame that ends
// mid-byte or without a start is reported.
// TRLC-LINKS: REQ-SDS-229
func frameBytes(ev []SampleSPIEvent) ([][]byte, error) {
	var out [][]byte
	var cur []byte
	in := false
	for _, e := range ev {
		switch e.Kind {
		case 1:
			if in {
				return out, fmt.Errorf("start inside a frame at clock %d", e.Clock)
			}
			cur, in = []byte{}, true
		case 2:
			if !in {
				return out, fmt.Errorf("byte outside a frame at clock %d", e.Clock)
			}
			cur = append(cur, e.Data)
		case 3:
			out = append(out, cur)
			in = false
		case 4:
			return out, fmt.Errorf("frame ended mid-byte at clock %d", e.Clock)
		}
	}
	return out, nil
}

// benchFrame is the bench source's burst n (an SPI flash fast read).
// TRLC-LINKS: REQ-SDS-229
func benchFrame(n int) []byte {
	b := append([]byte{0x0b, 0, 0x10, 0, 0xff}, "SPI FLASH 104MHZ OPEN-SDS 0123  "...)
	for i := 0; i < 32; i++ {
		b = append(b, byte(n+i))
	}
	return b
}

// synthCases: every rate the bench runs and one far below, in every mode
// and bit order, inverted, and with the clock on either channel.
// TRLC-LINKS: REQ-SDS-229
func synthCases() []spiBus {
	var cases []spiBus
	frames := [][]byte{benchFrame(0)[:12], {0x00, 0xff, 0xa5, 0x5a}, {0x81, 0x7e, 0x01, 0x80}}
	for i, rate := range []float64{1e6, 10e6, 25e6, 50e6, 75e6, 104e6} {
		for mode := 0; mode < 4; mode++ {
			p := spiBus{rateHz: rate, cpol: mode >= 2, cpha: mode%2 == 1, msb: mode != 1, inverted: mode == 3 && i%2 == 0,
				clockCh: 1 - mode%2, frames: frames, idleS: 2e-6 + 3/rate, riseS: 3e-9, noise: 1, seed: int64(10*i + mode)}
			cases = append(cases, p)
		}
	}
	return cases
}

// The reference decodes every synthetic bus exactly.
// TRLC-LINKS: REQ-SDS-229
func TestSampleSPISynthetic(t *testing.T) {
	for _, p := range synthCases() {
		name := fmt.Sprintf("%.0fMHz/cpol%v/cpha%v/msb%v/inv%v/clockC%d", p.rateHz/1e6, p.cpol, p.cpha, p.msb, p.inverted, p.clockCh+1)
		t.Run(name, func(t *testing.T) {
			ev, _ := SampleSPI(p.words(), p.config())
			got, err := frameBytes(ev)
			if err != nil || !reflect.DeepEqual(got, p.frames) {
				t.Fatalf("frames % x (%v), want % x", got, err, p.frames)
			}
		})
	}
}

// readCapture loads a recorded record's SRAM words (gzip, little-endian).
// TRLC-LINKS: REQ-SDS-229
func readCapture(t testing.TB, name string) []uint64 {
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	words := make([]uint32, len(raw)/4)
	for i := range words {
		words[i] = binary.LittleEndian.Uint32(raw[4*i:])
	}
	return SPIWideWords(words)
}

// spiCapture is a record of the bench source (clock on C2, data on C1, 10x
// probes at 1 V/div) taken by the instrument; the source's bursts are known.
type spiCapture struct {
	file   string
	rateHz float64
}

var spiCaptures = []spiCapture{
	{"spi_sample_104mhz.bin.gz", 104e6}, {"spi_sample_75mhz.bin.gz", 75e6}, {"spi_sample_50mhz.bin.gz", 50e6},
	{"spi_sample_25mhz.bin.gz", 25e6}, {"spi_sample_10mhz.bin.gz", 10e6},
}

// TRLC-LINKS: REQ-SDS-229
func captureConfig(rate float64) SampleSPIConfig {
	return SampleSPIConfig{ClockCh: 1, Level: 128, Low: 115, High: 141, MSB: true, Delay: 23, GapClocks: SampleSPIGap(rate)}
}

// Every whole burst in the recorded captures decodes without a bit error;
// the bursts cut by the record's ends are left out.
// TRLC-LINKS: REQ-SDS-229
func TestSampleSPICaptures(t *testing.T) {
	for _, c := range spiCaptures {
		t.Run(c.file, func(t *testing.T) {
			ev, _ := SampleSPI(readCapture(t, c.file), captureConfig(c.rateHz))
			frames, err := frameBytes(ev)
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) == 0 {
				t.Fatalf("%d frames", len(frames))
			}
			whole := 0
			for i, f := range frames {
				if len(f) < 38 {
					t.Fatalf("frame %d: % x", i, f)
				}
				if want := benchFrame(int(f[37])); !bytes.Equal(f, want) {
					t.Fatalf("frame %d: % x\nwant % x", i, f, want)
				}
				whole++
			}
			t.Logf("%d bursts, %d bits, 0 errors", whole, whole*69*8)
		})
	}
}

// The RTL (sample_edges.v, spi_sample.v) finds the model's edges and
// publishes its events on the same clocks, on the synthetic buses and the
// recorded captures.
// TRLC-LINKS: REQ-SDS-229
func TestSampleSPIRTL(t *testing.T) {
	sim := newRTLSim(t, "tb_spi_sample", "sample_edges.v", "spi_sample.v")
	type run struct {
		name  string
		words []uint64
		c     SampleSPIConfig
	}
	var runs []run
	for _, p := range synthCases() {
		if p.rateHz < 10e6 && p.cpha {
			continue // long; the other 1 MHz modes cover it
		}
		c := p.config()
		c.Pattern, c.Length = 0xffa55a, 3
		runs = append(runs, run{fmt.Sprintf("synthetic %.0fMHz mode %d%d msb %v inv %v", p.rateHz/1e6, b2u8(p.cpol), b2u8(p.cpha), p.msb, p.inverted), p.words(), c})
	}
	for _, c := range spiCaptures {
		words := readCapture(t, c.file)
		cfg := captureConfig(c.rateHz)
		cfg.Pattern, cfg.Length = 0x4f50454e, 4 // "OPEN"
		runs = append(runs, run{c.file, words, cfg})
		// Delays at the window's ends, and a falling-edge receiver on the same words.
		cfg.Delay, cfg.Length = -128, 1
		runs = append(runs, run{c.file + " delay -128", words, cfg})
		cfg.Delay, cfg.CPHA = 127, true
		runs = append(runs, run{c.file + " delay 127 falling", words, cfg})
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			ev, edges := SampleSPI(r.words, r.c)
			gotEdges, gotEv := sim.run(t, r.words, r.c)
			matches := 0
			for _, e := range ev {
				if e.Match {
					matches++
				}
			}
			if len(edges) == 0 || len(ev) == 0 || (matches == 0 && !strings.Contains(r.name, "delay")) {
				t.Fatalf("%d edges, %d events, %d matches", len(edges), len(ev), matches)
			}
			// The model's edges are clocks of the word they came from; the
			// RTL prints them where its edge output shows them.
			for i := range edges {
				edges[i].Clock += sampleEdgesLatency
				edges[i].Time -= 128 * (edges[i].Clock - sampleEdgesLatency)
				edges[i].Bit = false
			}
			if !reflect.DeepEqual(gotEdges, edges[:len(gotEdges)]) || len(edges)-len(gotEdges) > 2 {
				t.Fatalf("edges differ: RTL %d model %d\nRTL   %v\nmodel %v", len(gotEdges), len(edges), head(gotEdges), head(edges))
			}
			if !reflect.DeepEqual(gotEv, ev) {
				t.Fatalf("events differ: RTL %d model %d\nRTL   %v\nmodel %v", len(gotEv), len(ev), headEv(gotEv, ev), headEv(ev, gotEv))
			}
		})
	}
}

// sampleEdgesLatency is the clocks from a word's arrival to its edge on
// sample_edges.v's output (the testbench's view).
const sampleEdgesLatency = 7

// TRLC-LINKS: REQ-SDS-229
func head(e []SampleSPIEdge) []SampleSPIEdge {
	if len(e) > 8 {
		return e[:8]
	}
	return e
}

// headEv shows a few events around the first difference.
// TRLC-LINKS: REQ-SDS-229
func headEv(a, b []SampleSPIEvent) []SampleSPIEvent {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	lo := max(0, i-2)
	return a[lo:min(len(a), i+4)]
}

type rtlSim struct{ dir, image string }

// TRLC-LINKS: REQ-SDS-229
func newRTLSim(t *testing.T, tb string, files ...string) rtlSim {
	t.Helper()
	needIverilog(t)
	rtl := rtlDir(t)
	dir := t.TempDir()
	image := filepath.Join(dir, tb+".vvp")
	args := []string{"-g2012", "-s", tb, "-o", image}
	for _, f := range files {
		args = append(args, filepath.Join(rtl, f))
	}
	args = append(args, filepath.Join(rtl, "sim", tb+".v"))
	if out, err := runCmd("iverilog", args...); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	return rtlSim{dir, image}
}

// TRLC-LINKS: REQ-SDS-229
func (s rtlSim) run(t *testing.T, words []uint64, c SampleSPIConfig) ([]SampleSPIEdge, []SampleSPIEvent) {
	t.Helper()
	var in strings.Builder
	for _, w := range words {
		fmt.Fprintf(&in, "%016x\n", w)
	}
	path := filepath.Join(s.dir, "words.mem")
	if err := os.WriteFile(path, []byte(in.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	rising := (c.CPOL == c.CPHA) != c.Inverted
	out, err := runCmd("vvp", "-N", s.image, "+input="+path, fmt.Sprintf("+n=%d", len(words)), fmt.Sprintf("+ch=%d", c.ClockCh),
		fmt.Sprintf("+rising=%d", b2u8(rising)), fmt.Sprintf("+level=%d", c.Level), fmt.Sprintf("+low=%d", c.Low), fmt.Sprintf("+high=%d", c.High),
		fmt.Sprintf("+inverted=%d", b2u8(c.Inverted)), fmt.Sprintf("+msb=%d", b2u8(c.MSB)), fmt.Sprintf("+delay=%d", c.Delay&0xff),
		fmt.Sprintf("+gap=%d", c.GapClocks), fmt.Sprintf("+pattern=%d", c.Pattern), fmt.Sprintf("+len=%d", c.Length))
	if err != nil {
		t.Fatalf("simulate: %v\n%s", err, out)
	}
	var edges []SampleSPIEdge
	var events []SampleSPIEvent
	done := false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var a, b, c2, d int
		switch {
		case strings.HasPrefix(line, "EDGE "):
			if _, err := fmt.Sscanf(line, "EDGE %d %d", &a, &b); err != nil {
				t.Fatal(err)
			}
			edges = append(edges, SampleSPIEdge{Clock: a, Time: b})
		case strings.HasPrefix(line, "EV "):
			if _, err := fmt.Sscanf(line, "EV %d %d %d %d", &a, &b, &c2, &d); err != nil {
				t.Fatal(err)
			}
			events = append(events, SampleSPIEvent{Clock: a, Kind: uint8(b), Data: uint8(c2), Match: d != 0})
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
	return edges, events
}

// TRLC-LINKS: REQ-SDS-229
func needIverilog(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
}

// TRLC-LINKS: REQ-SDS-229
func rtlDir(t *testing.T) string { return testenv.RTLDir(t) }

// TRLC-LINKS: REQ-SDS-229
func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// lines splits receiver words into the two channels' codes.
// TRLC-LINKS: REQ-SDS-229
func lines(wide []uint64) (c1, c2 []uint8) {
	for _, w := range wide {
		for j := 0; j < 4; j++ {
			c1 = append(c1, sampleOf(w, 0, j))
			c2 = append(c2, sampleOf(w, 1, j))
		}
	}
	return
}

// The software decoder takes the data line at the clock's interpolated
// crossing under six samples a clock period: the synthetic buses past 80 MHz
// and the recorded 104 MHz bursts decode exactly.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-229
func TestDecodeSPIFine(t *testing.T) {
	for _, p := range synthCases() {
		if p.rateHz < 75e6 || p.inverted {
			continue
		}
		c1, c2 := lines(p.words())
		clk, data, lag := c2, c1, synthPhaseS/2e-9
		if p.clockCh == 0 {
			clk, data, lag = c1, c2, -lag
		}
		r := DecodeSPI(clk, data, 2e-9, SPICfg{CPOL: p.cpol, CPHA: p.cpha, MSB: p.msb, Threshold: 120, HaveThr: true, DataLag: lag})
		var want []int
		for _, f := range p.frames {
			for _, b := range f {
				want = append(want, int(b))
			}
		}
		if !r.OK || !reflect.DeepEqual(r.Bytes, want) {
			t.Fatalf("%.0f MHz mode %d%d: %v %s\n% x\nwant % x", p.rateHz/1e6, b2u8(p.cpol), b2u8(p.cpha), r.OK, r.Error, r.Bytes, want)
		}
	}
	c1, c2 := lines(readCapture(t, "spi_sample_104mhz.bin.gz"))
	r := DecodeSPI(c2, c1, 2e-9, SPICfg{MSB: true, Threshold: 128, HaveThr: true, DataLag: 0.72})
	text := string(intsToBytes(r.Bytes))
	if !r.OK || strings.Count(text, "SPI FLASH 104MHZ OPEN-SDS 0123") < 6 {
		t.Fatalf("recorded bursts: %v %s %q", r.OK, r.Error, text)
	}
}

// TRLC-LINKS: REQ-SDS-229
func intsToBytes(v []int) []byte {
	b := make([]byte, len(v))
	for i, x := range v {
		b[i] = byte(x)
	}
	return b
}
