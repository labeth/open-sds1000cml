// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"context"
	"testing"

	"open-sds/app/internal/engine"
)

// fakeEng's web decoded capture: EndWebDecodedCapture ends it and counts the
// calls.
// TRLC-LINKS: REQ-SDS-013
func (f *fakeEng) EndWebDecodedCapture(context.Context) (bool, error) {
	f.webEnds++
	open := f.Snapshot().DecodedWeb
	f.set(func(st *engine.Stats) { st.DecodedWeb = false })
	return open, nil
}

// The scope's run-control keys and DEFAULT SETUP end a decoded capture a web
// page holds, and say so: a page closed mid-session must not leave the
// acquisition waiting for good. Without one they do not ask the engine (an
// owner round trip on every RUN).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-013
func TestScopeKeysEndAWebCapture(t *testing.T) {
	for _, key := range []string{"run", "single", "auto", "defaultsetup"} {
		c, eng, _ := newC(t)
		eng.set(func(st *engine.Stats) { st.DecodedWeb = true })
		press(t, c, key)
		if eng.webEnds != 1 || eng.Snapshot().DecodedWeb {
			t.Fatalf("%s: %d ends, a web capture still open %v", key, eng.webEnds, eng.Snapshot().DecodedWeb)
		}
		// (AUTO's sweep and DEFAULT SETUP's own notices follow it)
		if n := c.PanelStatus().Notice; n != "Ended the web page's decoded capture" && (key == "run" || key == "single") {
			t.Errorf("%s: notice %q", key, n)
		}
		press(t, c, key)
		if eng.webEnds != 1 {
			t.Fatalf("%s without a web capture asked the engine: %d", key, eng.webEnds)
		}
	}
}

// A web page taking the acquisition for its decoded capture says so on the
// LCD, once: the scope shows STOP meanwhile, and its keys end it.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-135
func TestWebCaptureNoticeOnTheLCD(t *testing.T) {
	c, eng, _ := newC(t)
	eng.set(func(st *engine.Stats) { st.DecodedWeb = true })
	c.SyncLEDs()
	if n := c.PanelStatus().Notice; n != "A web page streams decoded captures - RUN here ends it" {
		t.Fatalf("notice %q", n)
	}
	c.mu.Lock()
	c.ui.notice, c.ui.noticeUnt = "", c.now() // the notice gone
	c.mu.Unlock()
	c.SyncLEDs()
	if n := c.PanelStatus().Notice; n != "" {
		t.Fatalf("said again: %q", n)
	}
}
