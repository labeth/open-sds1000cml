// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// AUTO in BUS scans for the bus anew: the bus decoded before is forgotten
// (an I2C set by hand), the UART on the inputs is filled in, the trigger goes
// on the start of its messages (the hardware pulse qualifier on the idle
// before the first character) near the left of the screen, and the notice
// says what it chose.
// TRLC-LINKS: REQ-SDS-223
func TestAutoInBusScansAndFramesTheBus(t *testing.T) {
	c, eng, _ := newEnvRig(t)
	eng.leased.Store(true) // sampled captures, as the sweep's lease gives
	set(t, c, "bus.protocol", "I2C")
	c.mu.Lock()
	c.ui.ws = wsview.Bus
	c.mu.Unlock()
	press(t, c, "auto")
	s := c.snap()
	st := eng.Snapshot()
	if s.decProto != framecalc.ProtoUART || s.decBaud != 115200 || !s.decAuto || c.AutosetBusy() {
		t.Fatalf("AUTO in BUS: protocol %d baud %d filled %v busy %v", s.decProto, s.decBaud, s.decAuto, c.AutosetBusy())
	}
	if st.TrigType != 1 || st.TrigPosFrac != busTrigPos || !strings.Contains(s.ui.busNote, "TRIG: UART first byte (idle >") {
		t.Fatalf("framing: trigger type %d at %g, note %q", st.TrigType, st.TrigPosFrac, s.ui.busNote)
	}
	if q := st.TrigQual; q.PulseCond != engine.CondGreater || !(q.PulseMaxNs > 87e3 && q.PulseMaxNs < 300e3) {
		t.Fatalf("idle qualifier %+v: over a message's longest high time (10 bits, 87 us) and under its 300 us idle", q)
	}
}

// AUTO in the other workspaces stays the autoset.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-223
func TestAutoOutsideBusIsAutoset(t *testing.T) {
	c, _, _ := newC(t)
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(fastUART()) })
	press(t, c, "auto")
	c.mu.Lock()
	msg := c.autosetMsg
	c.mu.Unlock()
	if msg != "AUTOSET..." {
		t.Fatalf("AUTO in SCOPE runs %q, not the autoset", msg)
	}
	press(t, c, "auto") // cancel
	for i := 0; i < 400 && c.AutosetBusy(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
}

// The frame start each bus's hardware matcher takes: I2C's own matcher with
// any address and no bytes, SOF then any unit where the decoder reports a
// frame's start, an ARINC 429 word, a MIL-1553 command word; none for UART.
// TRLC-LINKS: REQ-SDS-223
func TestFrameStartPatterns(t *testing.T) {
	for proto, want := range map[int]string{framecalc.ProtoI2C: "", framecalc.ProtoSPI: "SOF ??", framecalc.ProtoCAN: "SOF ??",
		framecalc.ProtoManchester: "SOF ??", framecalc.ProtoSENT: "SOF ??", framecalc.ProtoUSB: "SOF ??", framecalc.ProtoFlexRay: "SOF ??",
		framecalc.ProtoARINC429: "??", framecalc.ProtoMIL1553: "CMD"} {
		b, p, name, ok := sofPattern(proto)
		if got := strings.Join(tokensOf(engine.SerialParams{Bytes: b, Pattern: p}), " "); !ok || got != want || name == "" {
			t.Fatalf("%s: %q (%v %q), want %q", protoName(proto), got, ok, name, want)
		}
		if !defaultMatch(engine.SerialParams{Bytes: b, Pattern: p}) {
			t.Fatalf("%s: its own frame-start match reads as the user's", protoName(proto))
		}
		bb, pp, err := compileTokens(tokensOf(engine.SerialParams{Bytes: b, Pattern: p}))
		if err != nil || len(bb) != len(b) || len(pp) != len(p) {
			t.Fatalf("%s: the tokens do not read back: %v %v %v", protoName(proto), bb, pp, err)
		}
	}
	if _, _, _, ok := sofPattern(framecalc.ProtoUART); ok {
		t.Fatal("UART's matcher fires on every character: no frame start")
	}
	for _, toks := range [][]string{{"AA"}, {"55", "??"}, {"SOF", "12"}, {"SOF", "ERR"}, {"SOF", "??", "34"}, {"SOF"}} {
		b, p, err := compileTokens(toks)
		if err != nil {
			t.Fatal(err)
		}
		if user := (engine.SerialParams{Bytes: b, Pattern: p}); defaultMatch(user) {
			t.Fatalf("the match %q the user typed reads as the frame-start trigger's: AUTO replaces it", toks)
		}
	}
	for _, user := range []engine.SerialParams{{Bytes: []int{0xAA}}, {Pattern: []engine.PatternElem{{Kind: 2, Value: 0x55, Mask: 0xff}, {Kind: 2}}}} {
		if defaultMatch(user) {
			t.Fatalf("a match the user set reads as the default: %+v", user)
		}
	}
}

// Bytes the user set win over the frame start: the serial trigger arms on
// them.
// TRLC-LINKS: REQ-SDS-223
func TestUserMatchWinsOverFrameStart(t *testing.T) {
	r := newViewRig(t)
	r.show(fastUART())
	set(t, r.c, "bus.protocol", "UART")
	if err := r.c.setMatchTokens([]string{"4F", "4B"}); err != nil {
		t.Fatal(err)
	}
	note := r.c.frameTrigger(busMsgs{})
	if p := r.eng.SerialConfig(); r.eng.stats.SerialMode == 0 || len(p.Bytes) != 2 || p.Bytes[0] != 0x4F || note != "TRIG: your match 4F 4B" {
		t.Fatalf("armed %d on %+v: %q", r.eng.stats.SerialMode, p, note)
	}
}

// BUS entry with decode off detects the bus and arms the frame start: a
// UART's first character after the idle between its messages.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-223
func TestBusEntryArmsTheFrameStart(t *testing.T) {
	r := newViewRig(t)
	r.show(fastUART())
	press(t, r.c, "f2")
	r.show(fastUART())
	if r.c.snapProto() != framecalc.ProtoUART || !strings.Contains(r.c.snap().ui.busNote, "TRIG: UART first byte") {
		t.Fatalf("entry: protocol %d note %q", r.c.snapProto(), r.c.snap().ui.busNote)
	}
}

// The idle the UART trigger starts after: midway between a message's longest
// high time and the shortest idle between two; none when the messages run
// into each other.
// TRLC-LINKS: REQ-SDS-223
func TestUARTIdleWidth(t *testing.T) {
	if w := uartIdle(busMsgs{highRun: 100e-6, idle: 300e-6}); w < 199.9e-6 || w > 200.1e-6 {
		t.Fatalf("idle %g", w)
	}
	if w := uartIdle(busMsgs{highRun: 100e-6, idle: 120e-6}); w != 0 {
		t.Fatalf("no clear idle, but %g", w)
	}
}

// A message longer than the screen after the trigger moves the timebase on
// as long as the bus's bits stay resolved.
// TRLC-LINKS: REQ-SDS-223
func TestMessageTimebase(t *testing.T) {
	c, _, _ := newC(t)
	if td := c.messageTimebase(1e-3, 8.7e-6, 50e-6); td != 200e-6 {
		t.Fatalf("a 1 ms message at 50us/div: %g", td)
	}
	if td := c.messageTimebase(1e-3, 0.1e-6, 50e-6); td != 50e-6 {
		t.Fatalf("bits a slower timebase cannot resolve: %g", td)
	}
}

// AUTO pressed while a detection runs (BUS entry's, seconds at the full
// rate) sweeps once it has ended: the two set the timebase and the vertical
// each for itself, and the one ending last left its own.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-223
func TestAutoWaitsForTheDetectionUnderWay(t *testing.T) {
	c, eng, _ := newEnvRig(t)
	eng.leased.Store(true)
	c.mu.Lock()
	c.ui.ws = wsview.Bus
	c.ui.busDetecting = true // BUS entry's detection, under way
	c.mu.Unlock()
	tdiv0 := eng.Snapshot().TdivS
	done := make(chan struct{})
	go func() {
		defer close(done)
		press(t, c, "auto")
	}()
	time.Sleep(300 * time.Millisecond)
	if td := eng.Snapshot().TdivS; td != tdiv0 {
		t.Fatalf("AUTO swept during the detection: timebase %g -> %g", tdiv0, td)
	}
	c.mu.Lock()
	c.ui.busDetecting = false // the detection ends
	c.mu.Unlock()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("AUTO never ran after the detection ended")
	}
	if s := c.snap(); s.decProto != framecalc.ProtoUART || c.AutosetBusy() {
		t.Fatalf("AUTO after the detection: protocol %d busy %v", s.decProto, c.AutosetBusy())
	}
}

// AUTO pressed on a page frames for the page's workspace: with the LCD on
// SCOPE, a page on BUS gets BUS's scan and framing, not SCOPE's autoset, and
// the LCD stays on SCOPE. An unknown workspace is refused.
// TRLC-LINKS: REQ-SDS-226
func TestPageAutoFramesThePagesWorkspace(t *testing.T) {
	c, eng, _ := newEnvRig(t)
	eng.leased.Store(true)
	set(t, c, "bus.protocol", "I2C")
	if c.InjectAutoFor("nowhere") {
		t.Fatal("AUTO for an unknown workspace was taken")
	}
	if !c.InjectAutoFor("bus") {
		t.Fatal("AUTO for BUS was not queued")
	}
	(<-c.inject)()
	s := c.snap()
	if s.ui.ws != wsview.Scope {
		t.Fatalf("the LCD moved to workspace %d", s.ui.ws)
	}
	if s.decProto != framecalc.ProtoUART || !s.decAuto || eng.Snapshot().TrigPosFrac != busTrigPos {
		t.Fatalf("a page's AUTO on BUS: protocol %d filled %v trigger at %g, want BUS's scan and framing", s.decProto, s.decAuto, eng.Snapshot().TrigPosFrac)
	}
	if c.autoFor != 0 {
		t.Fatalf("the page's workspace outlived its press: %d", c.autoFor)
	}
}

// With the LCD on BUS, a page on SPECTRUM gets SPECTRUM's framing (fifty
// cycles of the FFT source), and the LCD key keeps the LCD's workspace.
// TRLC-LINKS: REQ-SDS-226, REQ-SDS-224
func TestPageAutoOnSpectrumWhileTheLCDIsOnBus(t *testing.T) {
	t.Parallel()
	sq := square(100e3, 0, 3.3)
	b := newSimBench(t, func(tt, _ float64, _ int) float64 { return sq(tt) })
	b.c.mu.Lock()
	b.c.ui.ws = wsview.Bus
	b.c.ui.views[wsview.Spectrum] = viewFFT
	b.c.mu.Unlock()
	if !b.c.InjectAutoFor("spectrum") {
		t.Fatal("AUTO for SPECTRUM was not queued")
	}
	(<-b.c.inject)()
	for end := time.Now().Add(20 * time.Second); b.c.AutosetBusy() && time.Now().Before(end); {
		time.Sleep(20 * time.Millisecond)
	}
	b.c.mu.Lock()
	n, lcd := b.c.ui.notice, b.c.ui.ws
	b.c.mu.Unlock()
	if !strings.HasPrefix(n, "AUTO (SPECTRUM): 50 cycles") || lcd != wsview.Bus {
		t.Fatalf("notice %q, LCD on %d: want SPECTRUM's framing with the LCD left on BUS", n, lcd)
	}
}

// A stream that needs another FPGA image says so while it starts: a UART
// framed on the qualifier image streams on the general image's receiver.
// TRLC-LINKS: REQ-SDS-018
func TestStreamImageNote(t *testing.T) {
	if n := streamImageNote(framecalc.ProtoUART, "qualify"); n != "loading the general FPGA image, about 4 s: the qualifier image has no UART receiver" {
		t.Fatalf("UART on the qualifier image: %q", n)
	}
	if n := streamImageNote(framecalc.ProtoSENT, "general"); !strings.HasPrefix(n, "loading the packet FPGA image") {
		t.Fatalf("SENT on the general image: %q", n)
	}
	for _, c := range []struct {
		proto int
		image string
	}{{framecalc.ProtoUART, "general"}, {framecalc.ProtoCAN, "packet"}, {framecalc.ProtoUSB, "line"}, {framecalc.ProtoI2C, ""}} {
		if n := streamImageNote(c.proto, c.image); n != "" {
			t.Fatalf("proto %d on %q: %q, want none", c.proto, c.image, n)
		}
	}
}
