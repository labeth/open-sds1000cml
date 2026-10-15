// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"sync"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/streamview"
)

// BUS's Stream view (REQ-SDS-228): F2 again cycles Packets, the eye diagram
// and the Stream view; entering the Stream view streams the bus the settings
// name with the BUS sheet's stop, and leaving it (F2, another workspace, the
// Stream row Off) ends the stream and runs the scope again.

// viewStart is one Start a viewStream took.
type viewStart struct {
	trig  engine.StreamTrigger
	value int
	label string
}

// viewStream records the starts and stops it is asked for; it streams until
// stopped.
type viewStream struct {
	mu     sync.Mutex
	active bool
	starts []viewStart
	stops  int
	state  streamview.State
}

// TRLC-LINKS: REQ-SDS-228
func (s *viewStream) Start(p engine.SerialParams, trig engine.StreamTrigger, value int, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, viewStart{trig, value, label})
	s.active, s.state = true, streamview.Streaming
	return nil
}

// TRLC-LINKS: REQ-SDS-228
func (s *viewStream) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		s.stops++
	}
	s.active, s.state = false, streamview.Stopped
}

// TRLC-LINKS: REQ-SDS-228
func (s *viewStream) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// TRLC-LINKS: REQ-SDS-228
func (s *viewStream) Snapshot() streamview.View {
	s.mu.Lock()
	defer s.mu.Unlock()
	return streamview.View{State: s.state, TriggerLine: -1}
}

// TRLC-LINKS: REQ-SDS-228
func (s *viewStream) Refuse(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = streamview.Failed
}

// TRLC-LINKS: REQ-SDS-228
func (s *viewStream) Lines(int, int) streamview.LinesPage { return streamview.LinesPage{} }

// TRLC-LINKS: REQ-SDS-228
func (s *viewStream) LinesBinary(int, []byte) int { return 0 }

// counts is how many starts and stops the stream took.
// TRLC-LINKS: REQ-SDS-228
func (s *viewStream) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.starts), s.stops
}

// last is the newest start.
// TRLC-LINKS: REQ-SDS-228
func (s *viewStream) last() viewStart {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts[len(s.starts)-1]
}

// streamRig is a view rig with a recording stream and a UART on screen.
// TRLC-LINKS: REQ-SDS-228
func streamRig(t *testing.T) (*viewRig, *viewStream, func()) {
	r := newViewRig(t)
	rs := &viewStream{}
	r.c.SetDecodeStream(rs)
	uart := func() { r.show(uartFrame([]string{"Hi U\x55\xaa\r\n"}, 460800, 60e-6, 100e-9, 20000)) }
	uart()
	return r, rs, uart
}

// TRLC-LINKS: REQ-SDS-228
func TestBusStreamViewStartsAndEndsTheStream(t *testing.T) {
	r, rs, uart := streamRig(t)
	c := r.c
	press(t, c, "f2")
	uart()
	if d, _ := c.Snapshot(); d.DecProto != 2 {
		t.Fatalf("BUS entry decoded protocol %d, want UART", d.DecProto)
	}
	set(t, c, "bus.stream_trigger", "On value")
	if res := c.SheetSet("bus.stream_value", nil, "0x48"); !res.OK {
		t.Fatal(res.Error)
	}
	press(t, c, "f2") // the eye: no stream
	if n, _ := rs.counts(); n != 0 || mustState(t, c, "BUS", "workspace").View != "Eye diagram" {
		t.Fatalf("the eye started %d streams", n)
	}
	press(t, c, "f2") // the Stream view: the stream starts with the sheet's stop
	st := mustState(t, c, "BUS", "workspace")
	if n, _ := rs.counts(); n != 1 || st.View != "Stream" || st.AgainNext != "Packets" {
		t.Fatalf("Stream view: %d starts, view %s next %s", n, st.View, st.AgainNext)
	}
	if s := rs.last(); s.trig != engine.StreamOnValue || s.value != 0x48 || !strings.HasPrefix(s.label, "UART 460") {
		t.Fatalf("start %+v", s)
	}
	if got := rowText(c, "bus.stream"); got != "On" {
		t.Fatalf("Stream row %q in the Stream view", got)
	}
	v := c.View()
	if v.Bus == nil || !strings.HasPrefix(v.Bus.Caption, "STREAM  UART") || v.Bus.Again != "F2 again: Packets" || v.Bus.Msg[0].Text != "STREAMING" {
		t.Fatalf("Stream pane %+v", v.Bus)
	}
	if tl := toolText(c); !strings.Contains(tl, "Adjust:stops on value push:stop Position:trigger position push:transcript") {
		t.Fatalf("Stream tool line %q", tl)
	}
	// Adjust: what stops it; a stream under way starts again with it
	turn(c, "adjust", 1)
	if n, _ := rs.counts(); n != 2 || rs.last().trig != engine.StreamManual || rowText(c, "bus.stream_trigger") != "By hand" {
		t.Fatalf("Adjust: %d starts, last %+v", n, rs.last())
	}
	// the Adjust push stops it (the record to the display), again streams again
	press(t, c, "adjustpush")
	if rs.Active() || !strings.Contains(toolText(c), "push:stream again") || !strings.Contains(toolText(c), "Zoom:list") {
		t.Fatalf("Adjust push did not stop the stream: %q", toolText(c))
	}
	press(t, c, "adjustpush")
	if n, _ := rs.counts(); n != 3 || !rs.Active() {
		t.Fatalf("Adjust push again: %d starts", n)
	}
	// Position's push: the transcript
	press(t, c, "horizpospush")
	if tl := toolText(c); !strings.Contains(tl, "Position:transcript push:trigger position") {
		t.Fatalf("Position push: %q", tl)
	}
	// F2 again: the packets, the stream ended and the scope running
	r.eng.SetRunning(false) // as a stopped stream leaves the engine
	press(t, c, "f2")
	_, stops := rs.counts()
	if st := mustState(t, c, "BUS", "workspace"); st.View != "Packets" || rs.Active() || stops < 3 || !r.eng.Snapshot().Running {
		t.Fatalf("back to Packets: view %s active %v stops %d running %v", st.View, rs.Active(), stops, r.eng.Snapshot().Running)
	}
	if got := rowText(c, "bus.stream"); got != "Off" {
		t.Fatalf("Stream row %q on the packets", got)
	}
	uart()
	if v := c.View(); v.Bus == nil || v.Bus.Off || len(v.Bus.Lines) == 0 {
		t.Fatalf("the packets after the stream: %+v", v.Bus)
	}
}

// Another workspace ends the stream; back in BUS, its Stream view streams again.
// TRLC-LINKS: REQ-SDS-228
func TestBusStreamViewEndsWithTheWorkspace(t *testing.T) {
	r, rs, uart := streamRig(t)
	c := r.c
	press(t, c, "f2")
	uart()
	press(t, c, "f2", "f2")
	if n, _ := rs.counts(); n != 1 {
		t.Fatalf("%d starts", n)
	}
	press(t, c, "f1")
	if rs.Active() || c.streaming() {
		t.Fatal("SCOPE kept the stream")
	}
	press(t, c, "f2")
	if n, _ := rs.counts(); n != 2 || !rs.Active() || mustState(t, c, "BUS", "workspace").View != "Stream" {
		t.Fatalf("BUS again: %d starts, active %v", n, rs.Active())
	}
	// another protocol: the stream ends and the Stream view streams that bus
	set(t, c, "bus.protocol", "I2C")
	if n, _ := rs.counts(); n != 2 || rs.Active() {
		t.Fatalf("I2C (nothing to decode on screen): %d starts, active %v", n, rs.Active())
	}
	set(t, c, "bus.protocol", "Off")
	if v := c.View(); v.Bus == nil || !v.Bus.Off || v.Bus.Msg[0].Text != "NO BUS DECODED" || c.streaming() {
		t.Fatalf("decode off in the Stream view: %+v", v.Bus)
	}
}

// With no bus the Stream view says so and how to find one; the Adjust push
// detects it and the stream starts on it.
// TRLC-LINKS: REQ-SDS-228
func TestBusStreamViewWithNoBus(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	rs := &viewStream{}
	c.SetDecodeStream(rs)
	r.show(smallFrame())
	press(t, c, "f2") // finds no bus
	press(t, c, "f2", "f2")
	v := c.View()
	if v.Bus == nil || !v.Bus.Off || v.Bus.Msg[0].Text != "NO BUS FOUND" || !strings.HasPrefix(v.Bus.Msg[1].Text, "Push Adjust: detect the bus") {
		t.Fatalf("no-bus Stream view %+v", v.Bus)
	}
	if tl := toolText(c); !strings.HasPrefix(tl, "Adjust push:detect the bus") {
		t.Fatalf("no-bus tool line %q", tl)
	}
	if n, _ := rs.counts(); n != 0 || rowText(c, "bus.stream") != "Off" {
		t.Fatalf("a stream with no bus: %d", n)
	}
	r.show(uartFrame([]string{"Hi U\x55\xaa\r\n"}, 460800, 60e-6, 100e-9, 20000))
	press(t, c, "adjustpush")
	if n, _ := rs.counts(); n != 1 || mustState(t, c, "BUS", "workspace").View != "Stream" {
		t.Fatalf("after the detection: %d starts", n)
	}
}

// The BUS sheet's Stream row is the view's: On from another workspace puts
// BUS on its Stream view (a page or the phone follows), Off back on the
// packets.
// TRLC-LINKS: REQ-SDS-228
func TestStreamRowShowsTheStreamView(t *testing.T) {
	r, rs, uart := streamRig(t)
	c := r.c
	press(t, c, "f2")
	uart()
	press(t, c, "f1")
	set(t, c, "bus.stream", "On")
	if st := mustState(t, c, "BUS", "workspace"); st.View != "Stream" || !rs.Active() {
		t.Fatalf("Stream On: %s, active %v", st.View, rs.Active())
	}
	set(t, c, "bus.stream", "Off")
	if st := mustState(t, c, "BUS", "workspace"); st.View != "Packets" || rs.Active() || !r.eng.Snapshot().Running {
		t.Fatalf("Stream Off: %s, active %v", st.View, rs.Active())
	}
	press(t, c, "f2") // the eye: Stream Off leaves it there
	set(t, c, "bus.stream", "Off")
	if st := mustState(t, c, "BUS", "workspace"); st.View != "Eye diagram" {
		t.Fatalf("Stream Off on the eye: %s", st.View)
	}
}
