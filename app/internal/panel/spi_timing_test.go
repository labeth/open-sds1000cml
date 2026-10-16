// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"testing"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
)

// SPI's data delay: Auto takes the eye's middle measured on the record, a
// value set by hand stays; the bus sent to the serial trigger carries it in
// sixteenths of a nanosecond, and the row reads the margin left.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func TestSPIDataDelayRow(t *testing.T) {
	c, _, _ := newC(t)
	c.mu.Lock()
	c.decProto, c.decChA, c.decChB = framecalc.ProtoSPI, 1, 0
	c.spiEye = decode.SPIEye{DelayS: 3.71e-9, MarginS: 3e-9, PeriodS: 9.6e-9, Transitions: 200, Late: false}
	c.mu.Unlock()
	if err := c.setSPIDelayAuto(); err != nil {
		t.Fatal(err)
	}
	s := c.snap()
	if d := s.spiDelayNs(); d != 3.6875 {
		t.Fatalf("auto delay %v ns", d)
	}
	if got := spiDelayText(s); got != "Auto +3.7 ns, margin +3.0 ns" {
		t.Fatalf("row text %q", got)
	}
	if p := c.serialBus(engine.SerialParams{}); p.SPIDelayNs != 3.6875 {
		t.Fatalf("bus delay %v", p.SPIDelayNs)
	}
	if err := c.setSPIDelay(8); err != nil {
		t.Fatal(err)
	}
	if got := spiDelayText(c.snap()); got != "+8.0 ns (FPGA +6.5 ns), margin +0.2 ns" {
		t.Fatalf("row text %q", got)
	}
}

// Auto reads a late device's eye from records too short to hold more than a
// burst's end or two: the ends counted over the records decide that the
// middle lies after the edge.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func TestSPIEyeAcrossShortRecords(t *testing.T) {
	c, _, _ := newC(t)
	c.mu.Lock()
	c.decProto, c.decChA, c.decChB, c.decDelayAuto = framecalc.ProtoSPI, 1, 0, true
	c.mu.Unlock()
	c1, c2 := lateSPIRecord()
	for start := 0; start+5000 <= len(c1); start += 5000 {
		f := &engine.Frame{C1: c1[start : start+5000], C2: c2[start : start+5000], Valid: 5000, SampleS: 2e-9}
		c.noteSPIEye(c.snap(), f)
	}
	s := c.snap()
	if !s.spiEye.Late || math.Abs(s.spiDelayNs()-7) > 0.6 {
		t.Fatalf("eye %+v, delay %.2f ns", s.spiEye, s.spiDelayNs())
	}
}

// lateSPIRecord is a 104 MHz SPI bus (clock on C2) whose data settles 7 ns
// after the edge that launches it and changes on the last one too: bursts
// of 8 bytes, 2 us apart, as two channels' codes at 2 ns.
// TRLC-LINKS: REQ-SDS-230
func lateSPIRecord() (c1, c2 []uint8) {
	const period, late, n = 1 / 104e6, 7e-9, 40000
	type tr struct {
		t  float64
		up bool
	}
	var clk, dat []tr
	cl, dl := false, false
	set := func(list *[]tr, lvl *bool, at float64, v bool) {
		if *lvl != v {
			*list = append(*list, tr{at, v})
			*lvl = v
		}
	}
	t := 1e-6
	for burst := 0; t < float64(n)*2e-9-10e-6; burst++ {
		for i := 0; i < 64; i++ {
			bit := (burst*7+i*13)%5 < 2
			start := t + float64(i)*period
			set(&dat, &dl, start+late, bit)
			set(&clk, &cl, start+period/2, true)
			set(&clk, &cl, start+period, false)
		}
		t += 64 * period
		set(&dat, &dl, t+late, !dl) // the next bit, launched on the last edge
		t += 2e-6
	}
	render := func(list []tr, phase float64) []uint8 {
		out := make([]uint8, n)
		lvl, k := false, 0
		for i := range out {
			at := float64(i)*2e-9 + phase
			for k < len(list) && list[k].t <= at {
				lvl = list[k].up
				k++
			}
			v := 70.0
			if lvl {
				v = 170
			}
			// a 1.5 ns ramp after each transition
			if k > 0 && at-list[k-1].t < 1.5e-9 {
				f := (at - list[k-1].t) / 1.5e-9
				if lvl {
					v = 70 + 100*f
				} else {
					v = 170 - 100*f
				}
			}
			out[i] = uint8(v)
		}
		return out
	}
	return render(dat, 0), render(clk, 1.44e-9)
}
