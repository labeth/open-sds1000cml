// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// A flash-like bus whose data settles 7 ns after the edge that launches it
// (6 to 8 ns at 104 MHz) reads its eye's middle 7 ns after the sampling
// edge; the receiver on the samples and the software decoder taking the
// data there read every byte, where the data at the edge reads garbage.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func TestSPIEyeFindsLateData(t *testing.T) {
	frames := [][]byte{benchFrame(0)[:20], benchFrame(7)[30:69], {0x00, 0xff, 0xa5, 0x5a}}
	for _, late := range []float64{0, 3e-9, 7e-9} {
		for _, clockCh := range []int{0, 1} {
			p := spiBus{rateHz: 104e6, msb: true, clockCh: clockCh, frames: frames, idleS: 2e-6, riseS: 3e-9, noise: 1, seed: 11, dataLateS: late}
			words := p.words()
			c1, c2 := lines(words)
			clk, data := c1, c2
			if clockCh == 1 {
				clk, data = c2, c1
			}
			lag := SPIDataLag(clockCh, 2e-9)
			eye, ok := MeasureSPIEye(clk, data, 2e-9, SPICfg{MSB: true, Threshold: 120, HaveThr: true, DataLag: lag})
			if !ok || math.Abs(eye.DelayS-late) > 0.6e-9 || eye.MarginS < 3e-9 || math.Abs(eye.PeriodS-1/104e6) > 0.1e-9 || eye.Late != (late > 1/208e6) {
				t.Fatalf("late %.0f ns C%d: %+v %v", late*1e9, clockCh+1, eye, ok)
			}
			var want []int
			for _, f := range frames {
				for _, b := range f {
					want = append(want, int(b))
				}
			}
			r := DecodeSPI(clk, data, 2e-9, SPICfg{MSB: true, Threshold: 120, HaveThr: true, DataLag: lag, DataDelay: eye.DelayS / 2e-9})
			if !r.OK || !reflect.DeepEqual(r.Bytes, want) {
				t.Fatalf("late %.0f ns C%d: software decode at the eye: % x", late*1e9, clockCh+1, r.Bytes)
			}
			c := p.config()
			c.Delay = max(-128, min(127, c.Delay+int(math.Round(eye.DelayS*16e9))))
			ev, _ := SampleSPI(words, c)
			if got, err := frameBytes(ev); err != nil || !reflect.DeepEqual(got, frames) {
				t.Fatalf("late %.0f ns C%d: receiver at the eye (delay %d): % x %v", late*1e9, clockCh+1, c.Delay, got, err)
			}
			if late == 7e-9 {
				if got, err := frameBytes(first(SampleSPI(words, p.config()))); err == nil && reflect.DeepEqual(got, frames) {
					t.Fatalf("C%d: data 7 ns late read at the edge", clockCh+1)
				}
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-229
func first(ev []SampleSPIEvent, _ []SampleSPIEdge) []SampleSPIEvent { return ev }

// The bench source's recorded 104 MHz bursts change data 1.8 ns before the
// clock's other edge (its clock leaves through a clock buffer): the eye's
// middle is that far before the sampling edge, the opening wide.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func TestSPIEyeRecorded(t *testing.T) {
	c1, c2 := lines(readCapture(t, "spi_sample_104mhz.bin.gz"))
	eye, ok := MeasureSPIEye(c2, c1, 2e-9, SPICfg{MSB: true, Threshold: 128, HaveThr: true, DataLag: SPIDataLag(1, 2e-9)})
	if !ok || eye.Late || eye.DelayS > 0 || eye.DelayS < -2.5e-9 || eye.MarginS < 3e-9 {
		t.Fatalf("recorded 104 MHz eye %+v %v", eye, ok)
	}
	t.Logf("delay %.2f ns, margin +-%.2f ns, %d transitions", eye.DelayS*1e9, eye.MarginS*1e9, eye.Transitions)
}

// A gated clock's last pulse of a burst can be a runt that crosses the
// level but not the band beyond it: the bursts after it still read whole
// (the line low for a receiver clock re-arms the edge).
// TRLC-LINKS: REQ-SDS-229
func TestSampleSPIRuntPulse(t *testing.T) {
	frames := [][]byte{benchFrame(1)[:12], benchFrame(2)[:12], benchFrame(3)[:12]}
	for _, runt := range []float64{2e-9, 2.2e-9, 2.4e-9} { // peaks of about 130 to 139: past the level, short of the band's top
		p := spiBus{rateHz: 50e6, msb: true, clockCh: 1, frames: frames, idleS: 1e-6, riseS: 3e-9, noise: 1, seed: 5, runtS: runt}
		c := p.config()
		c.High = 150
		ev, _ := SampleSPI(p.words(), c)
		if got, err := frameBytes(ev); err != nil || !reflect.DeepEqual(got, frames) {
			t.Fatalf("runt %.1f ns: % x %v", runt*1e9, got, err)
		}
	}
}

// flash104Records are records of the 104 MHz flash look-alike (clock C2, data
// C1, mode 0, the data 7 ns after the launching edge) taken by the
// instrument at 2 ns: four at 100 ns/div, the serial trigger on a burst's
// start (its start and first 2.5 us, no end), and two at 1 us/div.
// TRLC-LINKS: REQ-SDS-230
func flash104Records(t *testing.T) (r []struct {
	TdivS  float64 `json:"tdiv_s"`
	C1, C2 []uint8
}) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "spi_flash104_records.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Records []struct {
			TdivS  float64 `json:"tdiv_s"`
			C1, C2 []uint8
		}
	}
	if err := json.NewDecoder(z).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v.Records
}

// The flash look-alike's records at 100 ns/div hold a burst's start and no
// end: nothing tells the middle after the edge (+7.3 ns) from the one a
// period before it (-2.3 ns), and Auto reads after the edge, resolving, as
// a device's data settles after the edge launching it; the burst then reads
// as sent, where before the edge every bit read one clock off ("85 80 08 00
// 7F A9"). The records at 1 us/div hold ends, and the launches after them
// keep the reading after the edge.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func TestSPIEyeFlashStartsOnly(t *testing.T) {
	var v SPIEyeVotes
	for i, r := range flash104Records(t) {
		cfg := SPICfg{MSB: true, Threshold: 128, HaveThr: true, DataLag: SPIDataLag(1, 2e-9)}
		eye, ok := MeasureSPIEye(r.C2, r.C1, 2e-9, cfg)
		if !ok {
			t.Fatalf("record %d: no eye", i)
		}
		v.Add(eye)
		one, all := eye, eye.Resolve(v)
		for _, e := range []SPIEye{one, all} {
			if !e.Late || e.DelayS < 6.5e-9 || e.DelayS > 8e-9 || (r.TdivS < 1e-6 && !e.Resolving) {
				t.Fatalf("record %d (%g s/div): %+v", i, r.TdivS, e)
			}
		}
		cfg.DataDelay = all.DelayS / 2e-9
		res := DecodeSPI(r.C2, r.C1, 2e-9, cfg)
		if want := []int{0x0B, 0x00, 0x10, 0x00, 0xFF, 0x53, 0x50, 0x49}; !res.OK || !slices.Contains(starts(res.Bytes, want), true) {
			t.Fatalf("record %d at %+.2f ns: % x", i, all.DelayS*1e9, res.Bytes)
		}
	}
}

// starts reports, for each offset of b, whether want begins there.
// TRLC-LINKS: REQ-SDS-230
func starts(b, want []int) []bool {
	var out []bool
	for i := 0; i+len(want) <= len(b); i++ {
		out = append(out, slices.Equal(b[i:i+len(want)], want))
	}
	return out
}

// The bench source's 104 MHz bursts change data 1.8 ns before the clock's
// other edge, and nothing after a burst's last edge: across records of two
// or three bursts, their ends without a launch read the middle before the
// edge, and the bursts read as sent.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func TestSPIEyeEarlyAcrossRecords(t *testing.T) {
	c1, c2 := lines(readCapture(t, "spi_sample_104mhz.bin.gz"))
	cfg := SPICfg{MSB: true, Threshold: 128, HaveThr: true, DataLag: SPIDataLag(1, 2e-9)}
	var v SPIEyeVotes
	var eye SPIEye
	for start := 0; start+5000 <= len(c1); start += 5000 {
		e, ok := MeasureSPIEye(c2[start:start+5000], c1[start:start+5000], 2e-9, cfg)
		if !ok {
			t.Fatalf("record at %d: no eye", start)
		}
		v.Add(e)
		eye = e.Resolve(v)
	}
	if eye.Late || eye.Resolving || eye.DelayS > -1e-9 || eye.DelayS < -2.5e-9 {
		t.Fatalf("eye %+v, votes %+v", eye, v)
	}
	cfg.DataDelay = eye.DelayS / 2e-9
	if r := DecodeSPI(c2, c1, 2e-9, cfg); !r.OK || strings.Count(string(intsToBytes(r.Bytes)), "SPI FLASH 104MHZ OPEN-SDS 0123") < 6 {
		t.Fatalf("at %+.2f ns: %q", eye.DelayS*1e9, intsToBytes(r.Bytes))
	}
}

// The reading follows the votes, not the first ones: a bus read before the
// edge on ends without launches turns after it within a few bursts that
// launch, and back; a clock period changed by more than 3% starts the votes
// over; with no burst seen a middle past any device's settling reads
// before the edge (a host's data changing at the other edge, skewed).
// TRLC-LINKS: REQ-SDS-230
func TestSPIEyeVotesFollowBursts(t *testing.T) {
	const T = 9.6e-9
	mid := SPIEye{MiddleS: 7.3e-9, PeriodS: T}
	var v SPIEyeVotes
	for range 30 {
		v.Add(SPIEye{PeriodS: T, Ends: 2, Starts: 2})
	}
	if e := mid.Resolve(v); e.Late || e.Resolving || math.Abs(e.DelayS+2.3e-9) > 1e-12 {
		t.Fatalf("ends without launches: %+v %+v", e, v)
	}
	n := 0
	for ; mid.Resolve(v).DelayS < 0; n++ {
		v.Add(SPIEye{PeriodS: T, Ends: 1, Launched: n % 2, Starts: 1})
	}
	if n > 6 {
		t.Fatalf("after the edge after %d records: %+v", n, v)
	}
	for n = 0; mid.Resolve(v).DelayS > 0; n++ {
		v.Add(SPIEye{PeriodS: T, Ends: 1, Starts: 1, Ahead: 1})
	}
	if n > 8 {
		t.Fatalf("before the edge after %d records: %+v", n, v)
	}
	v.Add(SPIEye{PeriodS: 1 / 100e6})
	if v.Late != 0 || v.Early != 0 {
		t.Fatalf("votes kept across a rate change: %+v", v)
	}
	if e := mid.Resolve(v); !e.Late || !e.Resolving {
		t.Fatalf("no bursts: %+v", e)
	}
	host := SPIEye{MiddleS: 98.6e-9, PeriodS: 100e-9}
	if e := host.Resolve(SPIEyeVotes{}); e.Late || !e.Resolving || math.Abs(e.DelayS+1.4e-9) > 1e-12 {
		t.Fatalf("10 MHz host: %+v", e)
	}
}
