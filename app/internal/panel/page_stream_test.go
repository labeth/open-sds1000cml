package panel

import (
	"strings"
	"testing"
	"time"
)

// A stream the web page asked for stays while the page reads it; a page
// gone without a word (no reads for pageStreamAbandon) has it ended and the
// scope back on the view it showed. A key on the scope, or a stream the
// scope ran already, keeps the stream the scope's.
// TRLC-LINKS: REQ-SDS-228, REQ-SDS-278
func TestPageStreamEndsWhenThePageIsGone(t *testing.T) {
	r, rs, uart := streamRig(t)
	c := r.c
	press(t, c, "f2")
	uart()
	press(t, c, "f2", "f2", "f2")
	if st := mustState(t, c, "BUS", "workspace"); st.View != "Visual" {
		t.Fatalf("view %s, want Visual", st.View)
	}
	streams := func() bool { return rs.Active() && mustState(t, c, "BUS", "workspace").View == "Stream" }
	if res := c.PageStream(true); !res.OK || !streams() {
		t.Fatalf("the page's stream: %+v, streaming %v", res, streams())
	}
	r.clk.advance(90 * time.Second)
	c.StreamLines(0, 10) // the page reads
	r.clk.advance(90 * time.Second)
	c.checkPageStream()
	if !streams() {
		t.Fatal("a page that reads lost its stream")
	}
	r.clk.advance(pageStreamAbandon)
	c.checkPageStream()
	if st := mustState(t, c, "BUS", "workspace"); rs.Active() || st.View != "Visual" || !strings.Contains(c.View().Notice, "is gone") {
		t.Fatalf("a gone page's stream: active %v view %s notice %q", rs.Active(), st.View, c.View().Notice)
	}

	// the page ends its stream: the scope back on Visual too
	c.PageStream(true)
	if res := c.PageStream(false); !res.OK || rs.Active() || mustState(t, c, "BUS", "workspace").View != "Visual" {
		t.Fatalf("the page ending its stream: %+v active %v view %s", res, rs.Active(), mustState(t, c, "BUS", "workspace").View)
	}

	// a key on the scope: the stream is the scope's
	c.PageStream(true)
	c.noteInput(true)
	r.clk.advance(2 * pageStreamAbandon)
	c.checkPageStream()
	if !streams() {
		t.Fatal("a stream taken at the scope was ended")
	}
	// the scope's own stream: the page asking too does not make it the page's
	c.PageStream(true)
	r.clk.advance(2 * pageStreamAbandon)
	c.checkPageStream()
	if !streams() {
		t.Fatal("the scope's own stream was ended")
	}
	// the page ends it: no claim left behind
	if res := c.PageStream(false); !res.OK || rs.Active() {
		t.Fatalf("the page ending it: %+v active %v", res, rs.Active())
	}
	if c.page.on {
		t.Fatal("a claim outlived the stream")
	}
}

// The page's leave beacon ends a stream it still holds, the scope back on
// its view; a stream a key on the scope took over stays.
// TRLC-LINKS: REQ-SDS-228, REQ-SDS-278
func TestPageLeaveEndsOnlyItsStream(t *testing.T) {
	r, rs, uart := streamRig(t)
	c := r.c
	press(t, c, "f2")
	uart()
	press(t, c, "f2", "f2", "f2")
	c.PageStream(true)
	if res := c.PageLeave(); !res.OK || rs.Active() || mustState(t, c, "BUS", "workspace").View != "Visual" {
		t.Fatalf("leaving with its stream: %+v active %v view %s", res, rs.Active(), mustState(t, c, "BUS", "workspace").View)
	}
	c.PageStream(true)
	c.noteInput(true) // taken over at the scope
	if res := c.PageLeave(); !res.OK || !rs.Active() {
		t.Fatalf("leaving a stream the scope took: %+v active %v", res, rs.Active())
	}
}

// A key pressed on the scope itself - through the key matrix's decode, the
// path a real press takes - makes a stream the page held the scope's: the
// page's leave then keeps it.
// TRLC-LINKS: REQ-SDS-228, REQ-SDS-278
func TestPhysicalKeyTakesThePagesStream(t *testing.T) {
	r, rs, uart := streamRig(t)
	c := r.c
	press(t, c, "f2")
	uart()
	c.decode(idle(), true) // the matrix at rest
	c.PageStream(true)
	if !c.page.on || !rs.Active() {
		t.Fatalf("the page's stream: held %v active %v", c.page.on, rs.Active())
	}
	m := idle()
	m[3] &^= 1 << 11 // Help pressed on the front panel
	c.decode(m, true)
	c.decode(idle(), true) // and released
	if c.page.on {
		t.Fatal("a front-panel key left the stream the page's")
	}
	if res := c.PageLeave(); !res.OK || !rs.Active() {
		t.Fatalf("the page leaving a stream the scope took: %+v active %v", res, rs.Active())
	}
}
