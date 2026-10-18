// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// flash104 is the bench's 104 MHz SPI flash look-alike (clock C2, data C1,
// mode 0, bursts with 2 us idle) as the scope recorded it: the live
// envelope and the decimated record at 500us/div and 100us/div, and 80 us
// of the full-rate record at 20us/div.
type flash104 map[string]struct {
	SampleS float64 `json:"sample_s"`
	C1, C2  []uint8
}

// TRLC-LINKS: REQ-SDS-214
func loadFlash104(t *testing.T) flash104 {
	t.Helper()
	f, err := os.Open("testdata/spi_flash104_search.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]struct {
		SampleS float64 `json:"sample_s"`
		C1, C2  []int
	}
	if err := json.NewDecoder(z).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	out := flash104{}
	for k, r := range raw {
		e := out[k]
		e.SampleS = r.SampleS
		for i := range r.C1 {
			e.C1, e.C2 = append(e.C1, uint8(r.C1[i])), append(e.C2, uint8(r.C2[i]))
		}
		out[k] = e
	}
	return out
}

// frame is one recorded frame of the fixture as the engine sends it.
// TRLC-LINKS: REQ-SDS-214
func (fx flash104) frame(key string, tdiv float64) *engine.Frame {
	r := fx[key]
	f := &engine.Frame{C1: r.C1, C2: r.C2, Valid: len(r.C1), WinCols: len(r.C1), SampleS: r.SampleS, TdivS: tdiv}
	if strings.HasPrefix(key, "env") {
		f.PeakDetect, f.Ordered = true, true
	}
	return f
}

// The live envelope shows a line busy faster than its buckets: the bench's
// 104 MHz flash swings fully in every 248 ns bucket at 100us/div on both
// lines, about an edge a bucket. A quiet line shows no bound.
// TRLC-LINKS: REQ-SDS-214
func TestEnvelopeRunBoundsABusyLine(t *testing.T) {
	fx := loadFlash104(t)
	for _, k := range []string{"env100us", "env500us"} {
		r := fx[k]
		for ch, codes := range [][]uint8{r.C1, r.C2} {
			if got, busy := decode.EnvelopeRun(codes, r.SampleS); got != 8.0/3*r.SampleS || busy < 0.3 {
				t.Errorf("%s C%d: run %.3g busy %.2f, want 4/3 of a bucket %.3g", k, ch+1, got, busy, 8.0/3*r.SampleS)
			}
		}
	}
	quiet := make([]uint8, 2048)
	for i := range quiet {
		quiet[i] = uint8(128 + i%3)
	}
	if got, _ := decode.EnvelopeRun(quiet, 1e-6); got != 0 {
		t.Errorf("a quiet line: run %.3g, want none", got)
	}
}

// AUTO in BUS on the 104 MHz flash: the decimated record at 100us/div
// averages the clock into slow pulses and reads as I2C (a half clock of 3.6
// us), but the live envelope shows both lines changing every 248 ns bucket.
// The I2C does not stand; the search reads the short full-rate record and
// names the SPI bus: mode 0, clock C2, data C1. It never names I2C. The
// envelope shows the bus busy over much of the screen, so the full-rate
// record read is the short one at 10us/div, not 50us/div's.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-223
func TestBusSearchNamesTheFastSPIFlashNotI2C(t *testing.T) {
	fx := loadFlash104(t)
	if res := framecalc.Detect(fx.frame("dec100us", 100e-6), "hex"); res.Proto != "i2c" {
		t.Fatalf("the fixture's decimated record reads as %q: it no longer shows the misreading", res.Proto)
	}
	// and a decimated record that names no bus at all: the envelope is read
	// for it too, and the short record is the dense one
	quiet := make([]uint8, 4000)
	for i := range quiet {
		quiet[i] = 128
	}
	for _, named := range []bool{true, false} {
		searchFlash(t, fx, named, quiet)
	}
}

// searchFlash runs the search of TestBusSearchNamesTheFastSPIFlashNotI2C,
// its decimated records the fixture's (named: read as I2C) or quiet.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-223
func searchFlash(t *testing.T, fx flash104, named bool, quiet []uint8) {
	_, base, fe := newC(t)
	eng := &searchLeaseEng{fakeEng: base}
	ctl := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	ctl.decode(idle(), true)
	ctl.detectSync = true
	raws := map[float64]int{}
	ctl.SetFrameSource(func(fn func(*engine.Frame)) {
		tdiv := eng.Snapshot().TdivS
		slow := "100us"
		if tdiv >= 500e-6 {
			slow = "500us"
		}
		switch {
		case eng.raw.Load():
			raws[tdiv]++
			fn(fx.frame("full20us", tdiv))
		case eng.sampled.Load() && !named:
			fn(&engine.Frame{C1: quiet, C2: quiet, Valid: len(quiet), WinCols: len(quiet), SampleS: 10 * tdiv / float64(len(quiet)), TdivS: tdiv})
		case eng.sampled.Load():
			fn(fx.frame("dec"+slow, tdiv))
		default:
			fn(fx.frame("env"+slow, tdiv))
		}
	})
	ctl.runDetect()
	ctl.mu.Lock()
	proto, chA, chB, cpol, cpha, note := ctl.decProto, ctl.decChA, ctl.decChB, ctl.decCPOL, ctl.decCPHA, ctl.ui.notice
	ctl.mu.Unlock()
	if proto != framecalc.ProtoSPI || chA != 1 || chB != 0 || cpol || cpha {
		t.Fatalf("found %s clock C%d data C%d CPOL %v CPHA %v (notice %q), want SPI mode 0 clock C2 data C1",
			protoName(proto), chA+1, chB+1, cpol, cpha, note)
	}
	if !strings.HasPrefix(note, "AUTO: SPI mode 0 on C2/C1") {
		t.Errorf("notice %q", note)
	}
	if len(raws) != 1 || raws[10e-6] == 0 {
		t.Errorf("named %v: full-rate records read at %v, want only the dense one at 10us/div", named, raws)
	}
}

// A decimated find stands against the envelope by the bus's own lines: a
// UART whose bits the envelope's buckets resolve stands, as does one beside
// a fast line it does not use; an I2C whose clock line the envelope shows
// far faster than its half clock does not.
// TRLC-LINKS: REQ-SDS-214
func TestEnvelopeHidesOnlyTheBusLines(t *testing.T) {
	uart := decode.Result{OK: true, Proto: "uart", SPB: 8.5, Baud: 115200, Roles: decode.Roles{ChA: 0}}
	i2c := decode.Result{OK: true, Proto: "i2c", SPB: 57, Roles: decode.Roles{ChA: 1, ChB: 0}}
	for _, c := range []struct {
		name string
		l    look
		hide bool
	}{
		{"UART at 1ms/div, bits filling the buckets", look{res: uart, sampleS: 1.024e-6, envRun: [2]float64{13e-6, 0}}, false},
		{"ARINC 429 at 200us/div, half bits in pairs of buckets", look{res: decode.Result{OK: true, Proto: "arinc429", SPB: 39, Baud: 100000}, sampleS: 256e-9, envRun: [2]float64{2.6e-6, 0}}, false},
		{"UART beside a fast C2", look{res: uart, sampleS: 1.024e-6, envRun: [2]float64{0, 0.5e-6}}, false},
		{"the 104 MHz flash read as I2C", look{res: i2c, sampleS: 128e-9, envRun: [2]float64{1.31e-6, 0}}, true},
		{"an I2C with no envelope seen", look{res: i2c, sampleS: 128e-9}, false},
	} {
		if got := envHides(c.l); got != c.hide {
			t.Errorf("%s: hides %v", c.name, got)
		}
	}
	// a find whose frames' checks passed stands whatever the envelope shows
	// (USB PD's CRC-32: its half bits are close to the buckets at 200us/div);
	// one with no check does not
	pd := look{found: true, res: decode.Result{OK: true, Proto: "usbpd", SPB: 13, Baud: 300000}, sampleS: 256e-9, envRun: [2]float64{0.6e-6, 0}}
	if !envHides(pd) || !stands(pd) {
		t.Errorf("USB PD with its CRC checked: hides %v, stands %v", envHides(pd), stands(pd))
	}
	flash := look{found: true, res: i2c, sampleS: 128e-9, envRun: [2]float64{1.31e-6, 0}}
	if stands(flash) {
		t.Error("the flash read as I2C stands")
	}
}

// AUTO in BUS always says what it did. A sweep that finds no signal to fit
// searches no bus: BUS says AUTO found none (not "decode is off", as if it
// had not run), and the notice says why. A search that finds nothing says
// so on the notice for longer than a BUS entry's detection, and the screen
// carries it.
// TRLC-LINKS: REQ-SDS-223
func TestBusAutoSaysWhatItDid(t *testing.T) {
	c, _, _ := newC(t)
	c.mu.Lock()
	c.ui.ws = wsview.Bus
	c.mu.Unlock()
	c.noSignalBus(make(chan struct{}))
	v := c.View()
	if !strings.HasPrefix(v.Notice, "AUTO: no signal on CH1 or CH2") {
		t.Errorf("notice %q", v.Notice)
	}
	p := c.busPane(c.snap())
	if !strings.HasPrefix(p.Caption, "AUTO: no signal") || len(p.Msg) == 0 || p.Msg[0].Text != "NO BUS FOUND" {
		t.Errorf("BUS caption %q msg %+v", p.Caption, p.Msg)
	}
	cancelled := make(chan struct{})
	close(cancelled)
	c2, _, _ := newC(t)
	c2.noSignalBus(cancelled)
	if n := c2.View().Notice; n != "" {
		t.Errorf("a cancelled AUTO's notice %q: cancelledAuto says so", n)
	}

	// a search that finds nothing
	c3, eng, _ := newC(t)
	c3.detectSync = true
	quiet := make([]uint8, 4096)
	for i := range quiet {
		quiet[i] = 128
	}
	c3.SetFrameSource(func(fn func(*engine.Frame)) {
		fn(&engine.Frame{C1: quiet, C2: quiet, Valid: len(quiet), WinCols: len(quiet), SampleS: 2e-9, TdivS: eng.Snapshot().TdivS})
	})
	c3.mu.Lock()
	c3.ui.ws = wsview.Bus
	c3.busFrameNext = busFrameAuto
	c3.ui.busDetecting = true
	c3.mu.Unlock()
	c3.runDetect()
	c3.mu.Lock()
	notice, until := c3.ui.notice, c3.ui.noticeUnt.Sub(c3.now())
	c3.mu.Unlock()
	if !strings.HasPrefix(notice, "AUTO: no bus found") || until < busAutoNoticeFor/2 {
		t.Errorf("notice %q for %v", notice, until)
	}
	if p := c3.busPane(c3.snap()); !strings.HasPrefix(p.Caption, "AUTO: no bus found") {
		t.Errorf("BUS caption %q", p.Caption)
	}
}

// AUTO in BUS forgets the bus decoded before, takes the SPI data delay from
// the eye measured on the bus found next (Data delay Auto: Default setup's
// 0 ns sat on the 104 MHz flash's data edges) and measures the clock rate
// again (a rate set for another bus stayed).
// TRLC-LINKS: REQ-SDS-223, REQ-SDS-230
func TestBusAutoForgetsTheBusAndTakesTheDelayFromTheEye(t *testing.T) {
	c, _, _ := newC(t)
	c.mu.Lock()
	c.decProto, c.decDelayNs, c.decDelayAuto, c.spiEye = framecalc.ProtoSPI, 0, false, decode.SPIEye{PeriodS: 9.6e-9}
	c.mu.Unlock()
	if c.serialTrig() == nil {
		t.Fatal("rig without a serial trigger: the clock rate is not checked")
	}
	if err := c.setSPIClock(10_400_000); err != nil {
		t.Fatal(err)
	}
	c.forgetBus()
	if hz := c.serialTrig().SerialConfig().SPIClockHz; hz != 0 {
		t.Errorf("Clock rate kept at %d Hz: AUTO measures it again", hz)
	}
	s := c.snap()
	if s.decProto != framecalc.ProtoOff || !s.decDelayAuto || s.spiEye.PeriodS != 0 {
		t.Fatalf("proto %d, delay auto %v, eye %+v", s.decProto, s.decDelayAuto, s.spiEye)
	}
	if got := spiDelayText(s); got != "Auto (no eye yet)" {
		t.Errorf("Data delay %q", got)
	}
}

// A decimated find is weighed against the envelope only when a line of its
// bus holds runs under 3/4 of its shortest feature: the flash read as I2C
// (data runs of 2 samples, clock runs of 7, a 14-sample half clock), not a
// bench I2C whose lines change no faster than its clock, nor a line the bus
// does not use.
// TRLC-LINKS: REQ-SDS-214
func TestShortRunsAskForTheEnvelope(t *testing.T) {
	i2c := decode.Result{OK: true, Proto: "i2c", SPB: 28, Roles: decode.Roles{ChA: 1, ChB: 0}}
	uart := decode.Result{OK: true, Proto: "uart", SPB: 35, Baud: 115200, Roles: decode.Roles{ChA: 0}}
	for _, c := range []struct {
		name  string
		l     look
		short bool
	}{
		{"the flash read as I2C", look{res: i2c, runs: [2]int{2, 15}}, true},
		{"the flash read as I2C, its data runs longer", look{res: i2c, runs: [2]int{12, 7}}, true},
		{"a bench I2C", look{res: i2c, runs: [2]int{14, 14}}, false},
		{"a UART beside a fast C2", look{res: uart, runs: [2]int{35, 2}}, false},
	} {
		if got := shortRuns(c.l); got != c.short {
			t.Errorf("%s: short runs %v", c.name, got)
		}
	}
}

// A short-run find no live envelope could be read for does not stand (the
// search goes on at the full rate): after AUTO's sweep the screen held a
// decimated record, and the flash's I2C reading stood unweighed. A find
// with no short runs stands without one.
// TRLC-LINKS: REQ-SDS-214
func TestAShortRunFindNeedsTheEnvelope(t *testing.T) {
	c, _, _ := newC(t)
	c.detectSync = true
	flat := make([]uint8, 4096)
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		fn(&engine.Frame{C1: flat, C2: flat, Valid: len(flat), WinCols: len(flat), SampleS: 256e-9})
	})
	all := func(*engine.Frame, bool) bool { return true }
	i2c := decode.Result{OK: true, Proto: "i2c", SPB: 28, Roles: decode.Roles{ChA: 1, ChB: 0}}
	short := look{res: i2c, sampleS: 256e-9, found: true, runs: [2]int{2, 7}}
	if c.weighEnvelope(&short, all) {
		t.Error("the flash's I2C reading stood with no envelope read")
	}
	bench := look{res: i2c, sampleS: 256e-9, found: true, runs: [2]int{14, 14}}
	if !c.weighEnvelope(&bench, all) {
		t.Error("a bench I2C did not stand")
	}
}
