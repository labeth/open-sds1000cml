// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math"
	"reflect"
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
