// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
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
func lateSPIRecord() (c1, c2 []uint8) { return spiRecord(7e-9) }

// spiRecord is that bus with its data settling late after the edge that
// launches it; with late negative (data ahead of the edge, the bench's
// clock buffer) it sets the first bit up before the clock runs and changes
// nothing after the last edge.
// TRLC-LINKS: REQ-SDS-230
func spiRecord(late float64) (c1, c2 []uint8) {
	const period, n = 1 / 104e6, 40000
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
		if late > 0 {
			set(&dat, &dl, t+late, !dl) // the next bit, launched on the last edge
		}
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

// flashRecords are the decode package's records of the 104 MHz flash
// look-alike (clock C2, data C1) at 100 ns/div and 1 us/div.
// TRLC-LINKS: REQ-SDS-230
func flashRecords(t *testing.T) (tdivs []float64, frames []*engine.Frame) {
	t.Helper()
	f, err := os.Open("../decode/testdata/spi_flash104_records.json.gz")
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
	for _, r := range v.Records {
		tdivs = append(tdivs, r.TdivS)
		frames = append(frames, &engine.Frame{C1: r.C1, C2: r.C2, Valid: len(r.C1), SampleS: 2e-9})
	}
	return tdivs, frames
}

// After a restart at 100 ns/div the records hold a burst's start and no end:
// Auto reads the flash's data after the edge (+7.x ns, resolving) and the
// packet list reads the burst as sent, where it locked a period early
// (-2.2 ns: "85 80 08 00 7F A9 ..."). The records at 1 us/div keep it.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func TestSPIEyeFlashAfterRestart(t *testing.T) {
	tdivs, frames := flashRecords(t)
	c, _, _ := newC(t)
	c.mu.Lock()
	c.decProto, c.decChA, c.decChB, c.decDelayAuto = framecalc.ProtoSPI, 1, 0, true
	c.mu.Unlock()
	for i, f := range frames {
		c.noteSPIEye(c.snap(), f)
		s := c.snap()
		if d := s.spiDelayNs(); d < 6.5 || d > 8 {
			t.Fatalf("record %d (%g s/div): delay %+.2f ns, eye %+v", i, tdivs[i], d, s.spiEye)
		}
		if txt := spiDelayText(s); tdivs[i] < 1e-6 && !strings.HasSuffix(txt, ", resolving") {
			t.Fatalf("record %d: row %q", i, txt)
		}
		r := framecalc.Decode(f, decodeParamsOf(s))
		hex := ""
		for _, b := range r.Bytes {
			hex += fmt.Sprintf(" %02X", b)
		}
		if !r.OK || !strings.Contains(hex, " 0B 00 10 00 FF 53 50 49") {
			t.Fatalf("record %d at %+.2f ns:%s", i, s.spiDelayNs(), hex)
		}
	}
}

// The choice between the middle after the edge and the one before it
// follows the bursts: a bus read before the edge (its first bit set up
// ahead of the clock, nothing after its last edge) turns after it within a
// few records of a device launching after its last edge; a clock rate set
// on the row starts the bursts' votes over.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func TestSPIEyeChoiceFollowsBursts(t *testing.T) {
	c, _, _ := newC(t)
	c.mu.Lock()
	c.decProto, c.decChA, c.decChB, c.decDelayAuto = framecalc.ProtoSPI, 1, 0, true
	c.mu.Unlock()
	feed := func(c1, c2 []uint8, records int) float64 {
		for start := 0; start+5000 <= len(c1) && records > 0; start, records = start+5000, records-1 {
			c.noteSPIEye(c.snap(), &engine.Frame{C1: c1[start : start+5000], C2: c2[start : start+5000], Valid: 5000, SampleS: 2e-9})
		}
		return c.snap().spiDelayNs()
	}
	e1, e2 := spiRecord(-1.8e-9)
	for range 3 {
		if d := feed(e1, e2, 8); d > -1 || d < -3 || c.snap().spiEye.Resolving {
			t.Fatalf("early bus: %+.2f ns, eye %+v", d, c.snap().spiEye)
		}
	}
	l1, l2 := lateSPIRecord()
	if d := feed(l1, l2, 3); d < 6.5 || d > 7.5 {
		t.Fatalf("late bus after the early one: %+.2f ns, eye %+v", d, c.snap().spiEye)
	}
	if c.serialTrig() == nil {
		t.Fatal("no serial trigger")
	}
	before := c.spiEyeVotes
	if err := c.setSPIClock(100e6); err != nil {
		t.Fatal(err)
	}
	feed(l1, l2, 1)
	if v := c.spiEyeVotes; v.Late >= before.Late {
		t.Fatalf("votes kept across the clock rate: %+v then %+v", before, v)
	}
}

// An armed SPI trigger with its data delay on Auto measures the eye in
// every workspace: after a restart in COMPARE nothing measured it, the
// trigger read the 104 MHz flash's data on its edges and never fired until
// BUS was shown.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func TestSPIEyeMeasuredOutsideBus(t *testing.T) {
	_, frames := flashRecords(t)
	for _, armed := range []bool{true, false} {
		c, eng, _ := newC(t)
		c.mu.Lock()
		c.decProto, c.decChA, c.decChB, c.decDelayAuto = framecalc.ProtoSPI, 1, 0, true
		c.ui.ws = wsview.Compare
		c.mu.Unlock()
		if armed {
			eng.SetSerialMode(engine.SerialTrigger)
		}
		for _, f := range frames {
			c.OnFrame(f)
		}
		d := c.snap().spiDelayNs()
		if armed && (d < 6.5 || d > 8) {
			t.Fatalf("armed in COMPARE: delay %+.2f ns, eye %+v", d, c.snap().spiEye)
		}
		if !armed && c.snap().spiEye.PeriodS > 0 {
			t.Fatalf("not armed: the eye was measured outside BUS (%+v)", c.snap().spiEye)
		}
	}
}
