// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
)

// searchLeaseEng records the sample and decode leases a bus search takes.
type searchLeaseEng struct {
	*fakeEng
	sampled, raw atomic.Bool
	rawLeases    atomic.Int32
}

// TRLC-LINKS: REQ-SDS-214
func (e *searchLeaseEng) LeaseSampleView(d time.Duration) { e.sampled.Store(d > 0) }

// TRLC-LINKS: REQ-SDS-214
func (e *searchLeaseEng) LeaseDecodeView(d time.Duration) {
	e.raw.Store(d > 0)
	if d > 0 {
		e.rawLeases.Add(1)
	}
}

// A bus search looks at a decimated record first - read in tens of
// milliseconds - and keeps the bus found there when its bits span enough of
// that record's samples: a 115200 Bd UART at 200us/div (35 samples a bit)
// needs no full-rate record, and the sample lease ends with the search. A bus
// too fast for it (921600 Bd: 4.4 samples a bit) is looked at again at the
// full rate. Each lease ends with its phase.
// TRLC-LINKS: REQ-SDS-214
func TestBusSearchTakesADecimatedRecordFirst(t *testing.T) {
	for _, c := range []struct {
		baud    float64
		fullRun bool
	}{{115200, false}, {921600, true}} {
		_, base, fe := newC(t)
		eng := &searchLeaseEng{fakeEng: base}
		ctl := New(eng, fe, -1, engine.SupportedTdivs(), 200e-6, t.Logf)
		ctl.decode(idle(), true)
		ctl.detectSync = true
		msgs := []string{"Hi U\x55\xaa\x0f\xf0"}
		raw := uartFrame(msgs, c.baud, 30e-6, 2e-9, 1000000)
		dec := uartFrame(msgs, c.baud, 30e-6, 244e-9, 8192)
		env := envelope2(raw, 244)
		ctl.SetFrameSource(func(fn func(*engine.Frame)) {
			f := *env
			switch {
			case eng.raw.Load():
				f = *raw
			case eng.sampled.Load():
				f = *dec
			}
			fn(&f)
		})
		auto := func() (decode.Result, float64, bool) {
			var res decode.Result
			var sampleS float64
			ctl.frameFn(func(f *engine.Frame) {
				if f.PeakDetect || f.IsEnv {
					return
				}
				res, sampleS = decode.AutodetectFast(f.C1[:f.Valid], f.C2[:f.Valid], f.SampleS, "hex")
			})
			return res, sampleS, res.OK && res.Proto == "uart"
		}
		res, sampleS, found, _ := ctl.findBus(auto, func() bool { return true })
		if !found || res.Proto != "uart" {
			t.Fatalf("%.0f Bd: found %v %q", c.baud, found, res.Proto)
		}
		if ran := eng.rawLeases.Load() > 0; ran != c.fullRun {
			t.Errorf("%.0f Bd: a full-rate record read %v, want %v (decoded at %.3g s a sample, %.1f samples a bit)",
				c.baud, ran, c.fullRun, sampleS, decode.FeatureSamples(res))
		}
		if eng.sampled.Load() || eng.raw.Load() {
			t.Errorf("%.0f Bd: a lease outlived the search (sample %v, full rate %v)", c.baud, eng.sampled.Load(), eng.raw.Load())
		}
	}
}

// A bus too fast for the full-rate timebase's decimated record is looked for
// on a short full-rate record (50us/div: 500 us, read in under a second)
// before the whole one (2 ms, about four): a 2 MBd UART is found there, and
// BUS steps to the slowest timebase that reads it. A timebase whose own
// full-rate record is long (500us/div) is not read at the full rate at all.
// TRLC-LINKS: REQ-SDS-214
func TestBusSearchReadsAShortFullRateRecord(t *testing.T) {
	_, base, fe := newC(t)
	eng := &searchLeaseEng{fakeEng: base}
	ctl := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	ctl.decode(idle(), true)
	ctl.detectSync = true
	msgs := []string{"Hi U\x55\xaa\x0f\xf0"}
	raws := map[float64]int{}
	ctl.SetFrameSource(func(fn func(*engine.Frame)) {
		tdiv := eng.Snapshot().TdivS
		var f engine.Frame
		switch {
		case eng.raw.Load():
			raws[tdiv]++
			f = *uartFrame(msgs, 2e6, 5e-6, 2e-9, int(10*tdiv/2e-9))
		case eng.sampled.Load():
			f = *uartFrame(msgs, 2e6, 5e-6, 256e-9, 8192) // 2 samples a bit: no bus
		default:
			f = *envelope2(uartFrame(msgs, 2e6, 5e-6, 2e-9, 100000), 50)
		}
		f.TdivS = tdiv
		fn(&f)
	})
	ctl.runDetect()
	ctl.mu.Lock()
	proto, baud, notice := ctl.decProto, ctl.decBaud, ctl.ui.notice
	ctl.mu.Unlock()
	if proto != framecalc.ProtoUART || baud < 1.9e6 || baud > 2.1e6 {
		t.Fatalf("found %d at %d Bd, notice %q", proto, baud, notice)
	}
	if len(raws) != 1 || raws[50e-6] == 0 {
		t.Errorf("full-rate records read at %v, want only the short one at 50us/div", raws)
	}
	if tdiv := eng.Snapshot().TdivS; tdiv != 20e-6 || !strings.Contains(notice, "20us/div to read it") {
		t.Errorf("tdiv %g notice %q: want the slowest that reads 500 ns bits, 20us/div", tdiv, notice)
	}
	if eng.sampled.Load() || eng.raw.Load() {
		t.Errorf("a lease outlived the search (sample %v, full rate %v)", eng.sampled.Load(), eng.raw.Load())
	}
}

// What a decimated record shows of a bus stands by the bus's evidence: CRCs
// (CAN, FlexRay) stand on fewer samples a bit than a UART, and an odd-rate
// UART is a faster bus aliased.
// TRLC-LINKS: REQ-SDS-214
func TestDecimatedStands(t *testing.T) {
	r := func(proto string, spb float64, baud int) decode.Result {
		return decode.Result{OK: true, Proto: proto, SPB: spb, Baud: baud}
	}
	for _, c := range []struct {
		name string
		res  decode.Result
		want bool
	}{
		{"CAN at 500 kbit/s and 200us/div", r("canfd", 7.8, 500000), true},
		{"CAN at 1 Mbit/s and 200us/div", r("canfd", 3.9, 1000000), false},
		{"UART 115200 at 200us/div", r("uart", 34, 115200), true},
		{"UART 500000 at 200us/div", r("uart", 7.8, 500000), false},
		{"UART at an odd rate: Manchester aliased", r("uart", 29.7, 32917), false},
		{"I2C", r("i2c", 19, 0), true},
		{"SPI", r("spi", 19.5, 0), true},
		{"Manchester", r("manchester", 31, 0), true},
	} {
		if got := decimatedStands(c.res); got != c.want {
			t.Errorf("%s: stands %v", c.name, got)
		}
	}
}

// A UART found on a decimated record whose line holds runs well under its
// bit is a faster bus read in part: the bench's 500 kbit/s CAN frames at
// 500us/div (2 us bits, 3 samples) filled in as "UART 113413 8N1" (14
// samples a bit). A UART whose line changes no faster than its bits stands.
// TRLC-LINKS: REQ-SDS-214
func TestDecimatedUARTHidesAFasterBus(t *testing.T) {
	uart := decode.Result{OK: true, Proto: "uart", SPB: 14, Baud: 113413, Roles: decode.Roles{ChA: 1}}
	if !hidesFaster(look{res: uart, runs: [2]int{3, 3}}) {
		t.Fatal("CAN read as a UART: the 3-sample runs under its 14-sample bit stand")
	}
	if hidesFaster(look{res: uart, runs: [2]int{3, 13}}) {
		t.Fatal("a UART on C2 whose runs are bits, beside a fast C1: rejected")
	}
	if hidesFaster(look{res: decode.Result{OK: true, Proto: "i2c", SPB: 14}, runs: [2]int{3, 3}}) {
		t.Fatal("only a UART find is judged by its runs")
	}
}

// A UART of a byte or two, or at a rate no UART uses, on the record on
// screen is as likely part of another bus's frame: the search looks on at
// the full-rate timebase, and names it only when nothing else is found there.
// TRLC-LINKS: REQ-SDS-214
func TestBusSearchLooksPastAScantUART(t *testing.T) {
	for _, c := range []struct {
		name     string
		atFull   bool
		wantBaud int
	}{{"a UART at the full-rate timebase", true, 115200}, {"nothing else", false, 500000}} {
		ctl, eng, _ := newC(t)
		ctl.detectSync = true
		short := uartFrame([]string{"Hi"}, 500000, 0, 2e-9, 46000) // two bytes after the lead
		idle := make([]uint8, 8192)
		for i := range idle {
			idle[i] = 210
		}
		ctl.SetFrameSource(func(fn func(*engine.Frame)) {
			tdiv := eng.Snapshot().TdivS
			var f engine.Frame
			switch {
			case tdiv < 50e-6:
				f = *short
			case tdiv >= 200e-6 && c.atFull:
				f = *uartFrame([]string{"Hi U\x55\xaa"}, 115200, 30e-6, 256e-9, 8192)
			default:
				f = engine.Frame{C1: idle, C2: idle, Valid: len(idle), WinCols: len(idle), SampleS: 2e-9}
			}
			f.TdivS = tdiv
			fn(&f)
		})
		ctl.setTdivNearest(5e-6)
		ctl.runDetect()
		ctl.mu.Lock()
		proto, baud, notice := ctl.decProto, ctl.decBaud, ctl.ui.notice
		ctl.mu.Unlock()
		if proto != framecalc.ProtoUART || baud != c.wantBaud {
			t.Errorf("%s: found %d at %d Bd (notice %q), want UART %d", c.name, proto, baud, notice, c.wantBaud)
		}
	}
	for _, c := range []struct {
		res   decode.Result
		scant bool
	}{
		{decode.Result{Proto: "uart", Baud: 1500000, Bytes: make([]int, 3)}, true},
		{decode.Result{Proto: "uart", Baud: 115200, Bytes: make([]int, 4)}, false},
		{decode.Result{Proto: "uart", Baud: 10002156, Bytes: make([]int, 12)}, true},
		{decode.Result{Proto: "manchester", Baud: 1000000, Bytes: make([]int, 2)}, false},
	} {
		if got := scantUART(c.res); got != c.scant {
			t.Errorf("%s %d Bd, %d bytes: scant %v", c.res.Proto, c.res.Baud, len(c.res.Bytes), got)
		}
	}
	// an SPI or I2C bus of a byte or so (the bench SPI at Autoset's 5us/div)
	// is looked on from too, a whole message is not
	for _, c := range []struct {
		res   decode.Result
		scant bool
	}{
		{decode.Result{Proto: "spi", Bytes: make([]int, 1)}, true},
		{decode.Result{Proto: "spi", Bytes: make([]int, 8)}, false},
		{decode.Result{Proto: "i2c", Bytes: make([]int, 2)}, true},
		{decode.Result{Proto: "i2c", Bytes: make([]int, 4)}, false},
		{decode.Result{Proto: "canfd", Bytes: make([]int, 2)}, false},
		{decode.Result{Proto: "uart", Baud: 1500000, Bytes: make([]int, 3)}, true},
	} {
		if got := scantFind(c.res); got != c.scant {
			t.Errorf("%s, %d bytes: scant find %v", c.res.Proto, len(c.res.Bytes), got)
		}
	}
}

// A decimated record that resolves its every edge (a UART's bits at
// 10us/div, a few hundred samples each) shows at the full rate only what it
// showed: the search reads no full-rate record of that screen and goes on to
// the full-rate timebase. One with edges closer than it can tell apart (a
// fast bus) is read at the full rate first.
// TRLC-LINKS: REQ-SDS-214
func TestBusSearchSkipsAFullRateRecordTheDecimatedOneResolves(t *testing.T) {
	for _, c := range []struct {
		name string
		run  int // samples between the 10us/div record's edges
		raw  bool
	}{{"resolved", 300, false}, {"edges too close", 4, true}} {
		_, base, fe := newC(t)
		eng := &searchLeaseEng{fakeEng: base}
		ctl := New(eng, fe, -1, engine.SupportedTdivs(), 10e-6, t.Logf)
		ctl.decode(idle(), true)
		ctl.detectSync = true
		// edges c.run to 2*c.run samples apart, irregular (no clock); C2 idles
		square, idleC2 := make([]uint8, 3125), make([]uint8, 3125)
		level, next := uint8(40), c.run
		for i := range square {
			if i == next {
				level = 250 - level
				next += c.run + (i*7919)%(c.run+1)
			}
			square[i], idleC2[i] = level, 210
		}
		raws := map[float64]int{}
		ctl.SetFrameSource(func(fn func(*engine.Frame)) {
			tdiv := eng.Snapshot().TdivS
			var f engine.Frame
			switch {
			case eng.raw.Load():
				raws[tdiv]++
				f = engine.Frame{C1: square, C2: idleC2, Valid: len(square), WinCols: len(square), SampleS: 2e-9}
			case eng.sampled.Load() && tdiv >= 200e-6:
				f = *uartFrame([]string{"Hi U\x55\xaa"}, 115200, 30e-6, 256e-9, 8192)
			case eng.sampled.Load():
				f = engine.Frame{C1: square, C2: idleC2, Valid: len(square), WinCols: len(square), SampleS: 32e-9}
			default:
				f = *envelope2(uartFrame([]string{"Hi"}, 115200, 30e-6, 2e-9, 100000), 50)
			}
			f.TdivS = tdiv
			fn(&f)
		})
		ctl.setTdivNearest(10e-6)
		ctl.runDetect()
		ctl.mu.Lock()
		proto, notice := ctl.decProto, ctl.ui.notice
		ctl.mu.Unlock()
		if proto != framecalc.ProtoUART {
			t.Fatalf("%s: found %d, notice %q", c.name, proto, notice)
		}
		if read := raws[10e-6] > 0; read != c.raw {
			t.Errorf("%s: the 10us/div screen read at the full rate %v, want %v (full-rate reads %v, notice %q)", c.name, read, c.raw, raws, notice)
		}
	}
}

// While an FPGA image loads no capture comes, and the frame on screen is from
// before it: the search waits for the load and reads a capture after it. A
// UART streamed just before was found on the I2C bus flashed in meanwhile:
// the search's wait ran out during the load and it read the old frame.
// TRLC-LINKS: REQ-SDS-214
func TestDetectionWaitsOutAnImageLoad(t *testing.T) {
	c, e, _ := newC(t)
	stale := uartFrame([]string{"old bus\r\n"}, 115200, 60e-6, 100e-9, 20000)
	stale.Seq, stale.Content = 7, 7001
	var loading atomic.Bool
	loading.Store(true)
	var seq atomic.Uint64
	seq.Store(100)
	e.set(func(st *engine.Stats) { st.Running, st.Busy = true, "loading general image" })
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		if loading.Load() {
			fn(stale)
			return
		}
		f := fastUART()
		f.Seq = seq.Add(1)
		f.Content = 9000 + f.Seq
		fn(f)
	})
	go func() {
		time.Sleep(400 * time.Millisecond)
		e.set(func(st *engine.Stats) { st.Busy = "" })
		loading.Store(false)
	}()
	all := func(*engine.Frame, bool) bool { return true }
	l := c.detectWith(false, false, all, c.detectOnScreen, 150*time.Millisecond)
	if !l.found || l.res.Baud != 460800 {
		t.Fatalf("the search read baud %d (found %v): the frame from before the image load", l.res.Baud, l.found)
	}
}

// AUTO in BUS ends the bus search running (BUS entry's, on a front end
// Default setup left unfitted): its wait for a frame ends at once, not after
// the seconds of its looks, and AUTO's own search runs undisturbed.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-223
func TestAutoEndsTheSearchRunning(t *testing.T) {
	c, e, _ := newC(t)
	e.set(func(st *engine.Stats) { st.Running = true })
	c.SetFrameSource(func(fn func(*engine.Frame)) {}) // no frame comes
	none := func(*engine.Frame, bool) bool { return false }
	c.searchRun = c.searchGen.Load()
	go func() {
		time.Sleep(100 * time.Millisecond)
		c.searchGen.Add(1) // AUTO pressed
	}()
	t0 := time.Now()
	if l := c.detectWith(true, true, none, c.detectOnScreen, 3*time.Second); l.found {
		t.Fatal("found a bus without a frame")
	}
	if d := time.Since(t0); d > time.Second {
		t.Fatalf("the search waited %v after AUTO", d)
	}
	c.searchRun = c.searchGen.Load() // AUTO's search
	if c.searchDropped() {
		t.Fatal("AUTO's own search dropped")
	}
}
