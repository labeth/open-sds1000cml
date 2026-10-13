// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/streamview"
)

// i2cFrame is an I2C bus (SCL on C1, SDA on C2) repeating a write of the
// bytes to address 0x24, the clock at fclk, n samples sampleS apart. SDA
// moves only mid SCL-low, as the bench source does.
// TRLC-LINKS: REQ-SDS-214
func i2cFrame(fclk, sampleS float64, n int, bytes ...int) *engine.Frame {
	c1, c2 := make([]uint8, n), make([]uint8, n)
	q := 1 / fclk / 4 / sampleS // a quarter clock in samples
	t := 0.0
	put := func(scl, sda uint8, quarters float64) {
		for i := int(t); i < min(int(t+quarters*q), n); i++ {
			c1[i], c2[i] = scl, sda
		}
		t += quarters * q
	}
	const lo, hi = 40, 210
	bit := func(b int) {
		v := uint8(lo)
		if b != 0 {
			v = hi
		}
		put(lo, v, 2)
		put(hi, v, 2)
	}
	for t < float64(n) {
		put(hi, hi, 8) // idle
		put(hi, lo, 2) // START: SDA falls, SCL high
		put(lo, lo, 1)
		for _, b := range append([]int{0x24 << 1}, bytes...) {
			for k := 7; k >= 0; k-- {
				bit(b >> k & 1)
			}
			bit(0) // ACK
		}
		put(lo, lo, 2)
		put(hi, lo, 2)
		put(hi, hi, 2) // STOP: SDA rises, SCL high
	}
	return &engine.Frame{C1: c1, C2: c2, Valid: n, WinCols: n, EdgeX: float64(n) / 2, SampleS: sampleS,
		TdivS: float64(n) * sampleS / 10, Trigd: true}
}

// envelope2 is f as the live readout's time-ordered envelope: a (min, max)
// pair per bucket of both channels, each pair in the order that continues
// from the value before it.
// TRLC-LINKS: REQ-SDS-214
func envelope2(f *engine.Frame, bucket int) *engine.Frame {
	n := f.Valid / bucket
	env := func(src []uint8) []uint8 {
		out := make([]uint8, 0, 2*n)
		last := src[0]
		for b := 0; b < n; b++ {
			lo, hi := uint8(255), uint8(0)
			for _, v := range src[b*bucket : (b+1)*bucket] {
				lo, hi = min(lo, v), max(hi, v)
			}
			if absDiff(last, hi) < absDiff(last, lo) {
				out = append(out, hi, lo)
				last = lo
			} else {
				out = append(out, lo, hi)
				last = hi
			}
		}
		return out
	}
	return &engine.Frame{C1: env(f.C1), C2: env(f.C2), Valid: 2 * n, WinCols: 2 * n, EdgeX: float64(n),
		SampleS: f.SampleS * float64(bucket) / 2, TdivS: f.TdivS, PeakDetect: true, Ordered: true}
}

// TRLC-LINKS: REQ-SDS-214
func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// A live envelope whose buckets span the bus's clock is not decoded: the
// clock aliases there into starts and stops without bytes (I2C at 500us/div:
// packets of no bytes). A fine one decodes.
// TRLC-LINKS: REQ-SDS-214
func TestCoarseEnvelopeIsNotDecoded(t *testing.T) {
	raw := i2cFrame(184e3, 10e-9, 400000, 0x55, 0xAA, 0x0F, 0xF0) // 4 ms, the clock 5.4 us
	p := framecalc.DecodeParams{Proto: framecalc.ProtoI2C, ChA: 0, ChB: 1, Format: "hex"}
	if res := framecalc.Decode(raw, p); !res.OK || !slices.Equal(res.Bytes[:4], []int{0x55, 0xAA, 0x0F, 0xF0}) {
		t.Fatalf("raw: %+v", res)
	}
	fine := framecalc.Decode(envelope2(raw, 50), p) // 0.5 us buckets
	if !fine.OK || len(fine.Bytes) < 4 || !slices.Equal(fine.Bytes[:4], []int{0x55, 0xAA, 0x0F, 0xF0}) {
		t.Fatalf("a fine envelope: %v %q %v", fine.OK, fine.Error, fine.Bytes)
	}
	coarse := framecalc.Decode(envelope2(raw, 488), p) // 4.9 us buckets: 500us/div's live readout
	if coarse.OK || !strings.Contains(coarse.Error, "I2C not resolved at this timebase") {
		t.Fatalf("a coarse envelope decoded: ok %v error %q bytes %v", coarse.OK, coarse.Error, coarse.Bytes)
	}
}

// BUS's auto-detect steps the timebase to the slowest that reads the bus it
// found live, and says so; a bus the timebase reads keeps it.
// TRLC-LINKS: REQ-SDS-214
func TestBusDetectPicksAReadableTimebase(t *testing.T) {
	c, eng, _ := newC(t)
	raw := i2cFrame(184e3, 2e-9, 1000000, 0x55, 0xAA, 0x0F, 0xF0)
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(raw) })
	c.setTdivNearest(500e-6)
	c.runDetect()
	c.mu.Lock()
	proto, notice := c.decProto, c.ui.notice
	c.mu.Unlock()
	if tdiv := eng.Snapshot().TdivS; proto != framecalc.ProtoI2C || tdiv != 100e-6 || !strings.Contains(notice, "100us/div to read it") {
		t.Fatalf("I2C: proto %d tdiv %g notice %q", proto, tdiv, notice)
	}

	// a 115200 UART's bit is 3.6 points of 500us/div's live readout, which read
	// 0A as 02 on the bench: 200us/div reads it; 100us/div keeps
	c, eng, _ = newC(t)
	uart := uartFrame([]string{"Hi U\\x55\\xaa"}, 115200, 300e-6, 20e-9, 200000)
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(uart) })
	c.setTdivNearest(500e-6)
	c.runDetect()
	c.mu.Lock()
	notice = c.ui.notice
	c.mu.Unlock()
	if tdiv := eng.Snapshot().TdivS; tdiv != 200e-6 || !strings.Contains(notice, "200us/div to read it") {
		t.Fatalf("UART at 500us/div: tdiv %g notice %q", tdiv, notice)
	}
	c, eng, _ = newC(t)
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(uart) })
	c.setTdivNearest(100e-6)
	c.runDetect()
	c.mu.Lock()
	notice = c.ui.notice
	c.mu.Unlock()
	if tdiv := eng.Snapshot().TdivS; tdiv != 100e-6 || strings.Contains(notice, "to read it") {
		t.Fatalf("UART at 100us/div: tdiv %g notice %q", tdiv, notice)
	}
}

// A record the timebase decimates hides a fast bus: BUS's auto-detect then
// searches again at the slowest full-rate timebase and keeps it (a 10 Mbit/s
// FlexRay is not found at 500us/div's 32 ns); with no bus there either, the
// timebase goes back.
// TRLC-LINKS: REQ-SDS-214
func TestBusDetectRetriesAtTheFullRate(t *testing.T) {
	c, eng, _ := newC(t)
	fast := uartFrame([]string{"Hi U\x55\xaa"}, 20e6, 2e-6, 2e-9, 100000) // 50 ns bits
	coarse := uartFrame([]string{"Hi U\x55\xaa"}, 20e6, 2e-6, 32e-9, 20000)
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		if eng.Snapshot().TdivS >= 500e-6 {
			fn(coarse)
			return
		}
		fn(fast)
	})
	c.setTdivNearest(500e-6)
	c.runDetect()
	c.mu.Lock()
	proto, notice := c.decProto, c.ui.notice
	c.mu.Unlock()
	if tdiv := eng.Snapshot().TdivS; proto != framecalc.ProtoUART || tdiv != 2e-6 || !strings.Contains(notice, "2us/div to read it") {
		t.Fatalf("a 20 Mbaud UART: proto %d tdiv %g notice %q", proto, tdiv, notice)
	}

	c, eng, _ = newC(t)
	idle := make([]uint8, 1000)
	for i := range idle {
		idle[i] = 210
	}
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		sampleS := 2e-9 // each timebase's record
		if eng.Snapshot().TdivS >= 500e-6 {
			sampleS = 32e-9
		}
		fn(&engine.Frame{C1: idle, C2: idle, Valid: len(idle), WinCols: len(idle), SampleS: sampleS, TdivS: eng.Snapshot().TdivS})
	})
	c.setTdivNearest(500e-6)
	c.runDetect()
	if tdiv := eng.Snapshot().TdivS; tdiv != 500e-6 || !strings.Contains(c.View().Notice, "no bus found") {
		t.Fatalf("no bus: tdiv %g notice %q", tdiv, c.View().Notice)
	}

	// a record too short for the bus (Autoset's 10us/div holds about one UART
	// byte): the slowest full-rate timebase finds it and stays
	c, eng, _ = newC(t)
	uart := uartFrame([]string{"Hi U\x55\xaa"}, 115200, 300e-6, 20e-9, 200000)
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		if td := eng.Snapshot().TdivS; td < 100e-6 {
			fn(&engine.Frame{C1: idle, C2: idle, Valid: len(idle), WinCols: len(idle), SampleS: 2e-9, TdivS: td})
			return
		}
		u := *uart
		u.TdivS = eng.Snapshot().TdivS
		fn(&u)
	})
	c.setTdivNearest(10e-6)
	c.runDetect()
	c.mu.Lock()
	proto, notice = c.decProto, c.ui.notice
	c.mu.Unlock()
	if tdiv := eng.Snapshot().TdivS; proto != framecalc.ProtoUART || tdiv != 200e-6 || !strings.Contains(notice, "200us/div to read it") {
		t.Fatalf("a short record: proto %d tdiv %g notice %q", proto, tdiv, notice)
	}
}

// While running, BUS's auto-detect reads a frame captured after it began: the
// frame on screen, or one in flight, may predate a setting just changed (an
// ARINC bus is not found in a frame from before its bandwidth limit took
// effect).
// TRLC-LINKS: REQ-SDS-214
func TestBusDetectReadsAFreshFrame(t *testing.T) {
	c, eng, _ := newC(t)
	eng.set(func(st *engine.Stats) { st.Running = true })
	idle := make([]uint8, 20000)
	for i := range idle {
		idle[i] = 210
	}
	uart := uartFrame([]string{"Hi U\x55\xaa"}, 115200, 300e-6, 20e-9, 200000)
	looks := 0
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		looks++
		seq := uint64(10 + looks/3) // a new capture every third look: 10 on screen, 11 in flight
		if seq < 12 {
			fn(&engine.Frame{C1: idle, C2: idle, Valid: len(idle), WinCols: len(idle), SampleS: 20e-9, TdivS: 500e-6, Seq: seq})
			return
		}
		u := *uart
		u.Seq = seq
		fn(&u)
	})
	c.setTdivNearest(500e-6)
	start := time.Now()
	c.runDetect()
	c.mu.Lock()
	proto, notice := c.decProto, c.ui.notice
	c.mu.Unlock()
	// found in the fresh frame, then stepped to the timebase that reads it
	if tdiv := eng.Snapshot().TdivS; proto != framecalc.ProtoUART || tdiv != 200e-6 || time.Since(start) > 3*time.Second {
		t.Fatalf("detected %d at %g s/div in %v, notice %q: the frames from before the search were read", proto, tdiv, time.Since(start), notice)
	}
}

// envEng is the scope at a slow timebase: the live frames are the
// time-ordered envelope, sampled frames only while a decode lease runs.
type envEng struct {
	*fakeEng
	leased atomic.Bool
}

// TRLC-LINKS: REQ-SDS-214
func (e *envEng) LeaseDecodeView(d time.Duration) { e.leased.Store(d > 0) }

// recStream records what the decoded stream was told. Its end stops the
// scope a moment after Stop, as the engine ends a stream (its record shown).
type recStream struct {
	mu                     sync.Mutex
	label, refused         string
	active, started, stops int
	eng                    *envEng
}

// TRLC-LINKS: REQ-SDS-214
func (s *recStream) Start(_ engine.SerialParams, _ engine.StreamTrigger, _ int, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.label, s.active, s.started = label, 1, s.started+1
	return nil
}

// TRLC-LINKS: REQ-SDS-214
func (s *recStream) Stop() {
	if !s.Active() {
		return
	}
	time.Sleep(50 * time.Millisecond)
	s.eng.SetRunning(false)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active, s.stops = 0, s.stops+1
}

// TRLC-LINKS: REQ-SDS-214
func (s *recStream) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active == 1
}

// TRLC-LINKS: REQ-SDS-214
func (s *recStream) Snapshot() streamview.View { return streamview.View{} }

// TRLC-LINKS: REQ-SDS-214
func (s *recStream) Refuse(why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refused = why
}

// TRLC-LINKS: REQ-SDS-214
func (s *recStream) Lines(int, int) streamview.LinesPage { return streamview.LinesPage{} }

// TRLC-LINKS: REQ-SDS-214
func (s *recStream) LinesBinary(int, []byte) int { return 0 }

// newEnvRig is a controller at 500us/div with a 115200 UART on C1 that its
// live envelope (3.6 points a bit) cannot show, Auto starting a stream as the
// app's does: on the frame on screen.
// TRLC-LINKS: REQ-SDS-214
func newEnvRig(t *testing.T) (*Controller, *envEng, *recStream) {
	t.Helper()
	_, base, fe := newC(t)
	eng := &envEng{fakeEng: base}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	c.detectSync = true
	uart := uartFrame([]string{"Hi U\\x55\\xaa"}, 115200, 300e-6, 20e-9, 200000)
	env := envelope2(uart, 244) // 2.44 us a point: 500us/div's live readout
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		f := *env
		if eng.leased.Load() {
			f = *uart
		}
		f.TdivS = eng.Snapshot().TdivS
		fn(&f)
	})
	st := &recStream{eng: eng}
	c.SetDecodeStream(st, func() (decode.Result, float64, bool) {
		var res decode.Result
		var sampleS float64
		c.frameFn(func(f *engine.Frame) {
			if f.PeakDetect && !f.Ordered || f.IsEnv {
				return
			}
			res, sampleS = decode.AutodetectFast(f.C1[:f.Valid], f.C2[:f.Valid], f.SampleS, "hex")
		})
		return res, sampleS, res.OK
	})
	c.setTdivNearest(500e-6)
	if _, ok := c.lastAuto(); ok {
		t.Fatal("Auto read the live envelope: the rig shows nothing")
	}
	return c, eng, st
}

// Picking Protocol = Auto finds a bus its live envelope cannot show, on a
// sampled record as BUS does, and steps to the timebase that reads it (a
// 115200 Bd UART plain on screen at 500us/div matches no protocol on the
// envelope); decode stays Auto.
// TRLC-LINKS: REQ-SDS-214
func TestAutoFindsTheBusItsEnvelopeHides(t *testing.T) {
	c, eng, _ := newEnvRig(t)
	set(t, c, "bus.protocol", "Auto")
	if tdiv := eng.Snapshot().TdivS; tdiv != 200e-6 || c.snapProto() != framecalc.ProtoAuto ||
		!strings.Contains(c.View().Notice, "AUTO: UART 115200 8N1 on C1, 200us/div to read it") {
		t.Fatalf("Auto at 500us/div: tdiv %g protocol %d notice %q", tdiv, c.snapProto(), c.View().Notice)
	}
}

// Stream On with Protocol = Auto starts from the bus found on a sampled
// record, not "Auto found no protocol on screen" with a UART plain on screen
// at 500us/div.
// TRLC-LINKS: REQ-SDS-018
func TestAutoStreamStartsOnTheBusItsEnvelopeHides(t *testing.T) {
	c, _, st := newEnvRig(t)
	c.mu.Lock()
	c.decProto = framecalc.ProtoAuto
	c.mu.Unlock()
	set(t, c, "bus.stream", "On")
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.started != 1 || st.label != "UART 115200 b/s C1" || st.refused != "" {
		t.Fatalf("stream: started %d label %q refused %q", st.started, st.label, st.refused)
	}
}

// "Trigger on it" with Protocol = Auto arms on the bus found on a sampled
// record when the frame on screen cannot show it, fixing decode to it and
// ending the stream Auto ran (not "Auto decode found no bus" with a UART
// plain on screen at 500us/div, the stream still running).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-214
func TestAutoTriggerArmsOnTheBusItsEnvelopeHides(t *testing.T) {
	c, eng, st := newEnvRig(t)
	c.mu.Lock()
	c.decProto = framecalc.ProtoAuto
	c.mu.Unlock()
	set(t, c, "bus.stream", "On")
	eng.leased.Store(false) // the stream is running: the live envelope is back
	set(t, c, "bus.trigger_on", "On")
	if eng.Snapshot().SerialMode == 0 || c.snapProto() != framecalc.ProtoUART || eng.Snapshot().TdivS != 200e-6 ||
		!strings.Contains(c.View().Notice, "Decode fixed to UART 115200 8N1 on C1 for the serial trigger, 200us/div to read it") {
		t.Fatalf("armed %d protocol %d tdiv %g notice %q", eng.Snapshot().SerialMode, c.snapProto(), eng.Snapshot().TdivS, c.View().Notice)
	}
	for i := 0; ; i++ { // the stream stops off the panel goroutine
		if !st.Active() {
			break
		}
		if i == 100 {
			t.Fatal("the stream kept running under the serial trigger")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c.streaming() {
		t.Fatal("still in stream mode")
	}
	runsAgain(t, eng)
}

// runsAgain waits for RUN after a stream's end stopped the scope.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-214
func runsAgain(t *testing.T, eng *envEng) {
	t.Helper()
	for i := 0; !eng.Snapshot().Running; i++ {
		if i == 100 {
			t.Fatal("left stopped: the stream's end came after RUN")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Leaving stream mode runs the scope again once the stream has ended: the
// engine ends a stream stopped, after a RUN that would follow Stop at once.
// TRLC-LINKS: REQ-SDS-018
func TestLeavingAStreamRunsAgain(t *testing.T) {
	for _, leave := range []struct {
		name string
		do   func(*Controller)
	}{
		{"Stream Off", func(c *Controller) { set(t, c, "bus.stream", "Off") }},
		{"Protocol UART", func(c *Controller) { set(t, c, "bus.protocol", "UART") }},
		{"Default setup", func(c *Controller) { c.DefaultSetup() }},
	} {
		t.Run(leave.name, func(t *testing.T) {
			c, eng, st := newEnvRig(t)
			c.mu.Lock()
			c.decProto = framecalc.ProtoAuto
			c.mu.Unlock()
			set(t, c, "bus.stream", "On")
			if !st.Active() {
				t.Fatal("no stream")
			}
			leave.do(c)
			for i := 0; st.Active(); i++ {
				if i == 100 {
					t.Fatal("the stream kept running")
				}
				time.Sleep(10 * time.Millisecond)
			}
			runsAgain(t, eng)
		})
	}
}

// "Trigger on it" with Protocol = Auto on a sampled frame on screen (a
// stream's record) fixes decode to the bus there and steps to the timebase
// that reads it live: armed at 500us/div, the decode strip cannot show the
// bytes it triggers on.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-214
func TestAutoTriggerStepsToATimebaseThatReadsIt(t *testing.T) {
	c, eng, _ := newEnvRig(t)
	eng.leased.Store(true) // the frame on screen is sampled
	c.mu.Lock()
	c.decProto = framecalc.ProtoAuto
	c.mu.Unlock()
	set(t, c, "bus.trigger_on", "On")
	if eng.Snapshot().SerialMode == 0 || eng.Snapshot().TdivS != 200e-6 ||
		!strings.Contains(c.View().Notice, "Decode fixed to UART 115200 8N1 on C1 for the serial trigger, 200us/div to read it") {
		t.Fatalf("armed %d tdiv %g notice %q", eng.Snapshot().SerialMode, eng.Snapshot().TdivS, c.View().Notice)
	}
}

// A timebase asked for while a stream ran applies when the stream ends: the
// readable-timebase step judges that one, not the running one ("Trigger on it"
// from a stream at 200us/div with 500us/div pending would arm at 500us/div,
// too coarse to show the bus).
// TRLC-LINKS: REQ-SDS-214
func TestReadableTimebaseJudgesTheTimebaseAskedFor(t *testing.T) {
	c, eng, _ := newEnvRig(t)
	eng.leased.Store(true)
	c.setTdivNearest(200e-6)
	eng.set(func(st *engine.Stats) { st.TdivSetS = 500e-6 }) // asked for, not yet applied
	c.mu.Lock()
	c.decProto = framecalc.ProtoAuto
	c.mu.Unlock()
	set(t, c, "bus.trigger_on", "On")
	if st := eng.Snapshot(); st.SerialMode == 0 || st.TdivSet() != 200e-6 {
		t.Fatalf("armed %d, timebase asked for %g", st.SerialMode, st.TdivSet())
	}
}

// Auto on a live envelope too coarse for the bus says so, as a fixed protocol
// does, not "AUTO: no protocol matched" with a 115200 Bd UART on screen at
// 500us/div; the sampled record finds the UART.
// TRLC-LINKS: REQ-SDS-214
func TestCoarseEnvelopeAutoSaysWhy(t *testing.T) {
	uart := uartFrame([]string{"Hi U\\x55\\xaa"}, 115200, 300e-6, 20e-9, 200000)
	p := framecalc.DecodeParams{Proto: framecalc.ProtoAuto, Format: "hex"}
	if res := framecalc.Decode(envelope2(uart, 244), p); res.OK || res.Error != "no protocol resolved at this timebase - S/DIV faster, or SINGLE" {
		t.Fatalf("coarse envelope: ok %v proto %q error %q", res.OK, res.Proto, res.Error)
	}
	if res := framecalc.Decode(uart, p); !res.OK || res.Proto != "uart" {
		t.Fatalf("sampled: ok %v proto %q error %q", res.OK, res.Proto, res.Error)
	}
}

// A search whose caller no longer wants the bus gives the timebase back:
// Auto picked after Autoset's 10us/div (about one UART byte) and changed
// while the search looked at 200us/div must not leave the scope at 200us/div.
// TRLC-LINKS: REQ-SDS-214
func TestAutoSearchGivesTheTimebaseBackWhenLeft(t *testing.T) {
	c, eng, _ := newC(t)
	c.detectSync = true
	idle := make([]uint8, 1000)
	for i := range idle {
		idle[i] = 210
	}
	uart := uartFrame([]string{"Hi U\x55\xaa"}, 115200, 300e-6, 20e-9, 200000)
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		td := eng.Snapshot().TdivS
		if td < 100e-6 {
			fn(&engine.Frame{C1: idle, C2: idle, Valid: len(idle), WinCols: len(idle), SampleS: 2e-9, TdivS: td})
			return
		}
		c.mu.Lock()
		c.decProto = framecalc.ProtoOff // picked meanwhile
		c.mu.Unlock()
		u := *uart
		u.TdivS = td
		fn(&u)
	})
	c.setTdivNearest(10e-6)
	set(t, c, "bus.protocol", "Auto")
	if td := eng.Snapshot().TdivS; td != 10e-6 {
		t.Fatalf("timebase %g after a search no longer wanted", td)
	}
}

// BUS's auto-detect leaves a protocol set while it searched (seconds at slow
// timebases): neither its "no bus found - decode stays off" nor a bus it found
// replaces a UART picked on the phone meanwhile.
// TRLC-LINKS: REQ-SDS-214
func TestBusDetectKeepsAProtocolPickedMeanwhile(t *testing.T) {
	idle := make([]uint8, 1000)
	for i := range idle {
		idle[i] = 210
	}
	uart := uartFrame([]string{"Hi U\x55\xaa"}, 115200, 300e-6, 20e-9, 200000)
	for _, tc := range []struct {
		name   string
		frame  *engine.Frame
		picked int
	}{
		{"no bus", &engine.Frame{C1: idle, C2: idle, Valid: len(idle), WinCols: len(idle), SampleS: 2e-9}, framecalc.ProtoUART},
		{"a bus found", uart, framecalc.ProtoI2C},
	} {
		c, eng, _ := newC(t)
		picked := false
		c.SetFrameSource(func(fn func(*engine.Frame)) {
			if !picked { // the phone picks a protocol while BUS searches
				picked = true
				if err := c.setDecProto(tc.picked); err != nil {
					t.Error(err)
				}
			}
			f := *tc.frame
			f.TdivS = eng.Snapshot().TdivS
			fn(&f)
		})
		c.setTdivNearest(500e-6)
		c.runDetect()
		if p := c.snapProto(); p != tc.picked {
			t.Fatalf("%s: protocol %d after the search, picked %d meanwhile", tc.name, p, tc.picked)
		}
	}
}

// slowStream starts as the scope's decoded stream does: Starting for a while
// (an image load), refusing a second start meanwhile, and ending at once when
// a stop was asked while it started.
type slowStream struct {
	recStream
	starting, stopReq bool
	begin             func() // a stream that begins stops RUN, as the engine's does
}

// TRLC-LINKS: REQ-SDS-214
func (s *slowStream) Start(_ engine.SerialParams, _ engine.StreamTrigger, _ int, label string) error {
	s.mu.Lock()
	if s.starting {
		s.mu.Unlock()
		return fmt.Errorf("a stream is already starting")
	}
	s.starting, s.stopReq, s.label = true, false, label
	s.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starting, s.started = false, s.started+1
	if !s.stopReq {
		s.active = 1
		if s.begin != nil {
			s.begin()
		}
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-214
func (s *slowStream) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.starting {
		s.stopReq = true
	}
	s.active = 0
}

// TRLC-LINKS: REQ-SDS-214
func (s *slowStream) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starting || s.active == 1
}

// Stream On, Off and On again while the first stream still starts ends with a
// stream running: the second start is refused ("a stream is already
// starting") and the first then stops as the Off asked, which must not leave
// Stream On with none running.
// TRLC-LINKS: REQ-SDS-018
func TestStreamOnOffOnRuns(t *testing.T) {
	c, eng, _ := newC(t)
	uart := uartFrame([]string{"Hi U\x55\xaa"}, 115200, 300e-6, 20e-9, 200000)
	res, sampleS := decode.AutodetectFast(uart.C1, uart.C2, uart.SampleS, "hex")
	if !res.OK {
		t.Fatal("no UART in the frame")
	}
	st := &slowStream{begin: func() { eng.SetRunning(false) }}
	c.SetDecodeStream(st, func() (decode.Result, float64, bool) { return res, sampleS, true })
	c.mu.Lock()
	c.decProto = framecalc.ProtoAuto
	c.mu.Unlock()
	for _, on := range []string{"On", "Off", "On"} {
		set(t, c, "bus.stream", on)
		time.Sleep(20 * time.Millisecond)
	}
	for i := 0; !st.Active() || st.isStarting(); i++ {
		if i == 200 {
			t.Fatalf("Stream %s with no stream running (started %d)", rowText(c, "bus.stream"), st.started)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond) // the Off's wait for its stream's end is over
	if !c.streaming() || !st.Active() || eng.Snapshot().Running {
		t.Fatalf("streaming %v, stream running %v, RUN %v: the Off's RUN came after the new stream began", c.streaming(), st.Active(), eng.Snapshot().Running)
	}
}

// TRLC-LINKS: REQ-SDS-018
func (s *slowStream) isStarting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starting
}

// waitEng is a scope in Normal waiting for a trigger: nothing published for
// since.
type waitEng struct {
	*fakeEng
	since time.Duration
}

// TRLC-LINKS: REQ-SDS-018
func (e *waitEng) SincePublish() time.Duration { return e.since }

// A bus search that gets no new capture because the scope waits for a trigger
// (Normal, the level off the signal) says so, not that no bus is there (nor,
// on the web, names an I2C bus Manchester from the live envelope).
// TRLC-LINKS: REQ-SDS-214
func TestBusSearchSaysTheScopeWaits(t *testing.T) {
	_, base, fe := newC(t)
	eng := &waitEng{fakeEng: base, since: 10 * time.Second}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	c.detectSync = true
	eng.set(func(st *engine.Stats) { st.Norm, st.Running = true, true })
	idle := make([]uint8, 1000)
	for i := range idle {
		idle[i] = 210
	}
	seen := map[float64]bool{}
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		seen[eng.Snapshot().TdivS] = true
		fn(&engine.Frame{C1: idle, C2: idle, Valid: len(idle), WinCols: len(idle), SampleS: 2e-9, TdivS: eng.Snapshot().TdivS})
	})
	const why = "no new capture - the scope waits for a trigger: Auto or Force, then again"
	c.runDetect()
	if n := c.View().Notice; n != "AUTO: "+why || len(seen) != 1 {
		t.Fatalf("BUS entry: %q, timebases searched %v (the full-rate one captures nothing either)", n, seen)
	}
	set(t, c, "bus.protocol", "Auto")
	if n := c.View().Notice; n != "AUTO: "+why {
		t.Fatalf("Auto picked: %q", n)
	}
	eng.since = 0 // running free again: no bus is no bus
	c.runDetect()
	if n := c.View().Notice; !strings.Contains(n, "no bus found") {
		t.Fatalf("not waiting: %q", n)
	}
}
