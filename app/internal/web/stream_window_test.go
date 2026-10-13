// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"context"
	"fmt"
	"testing"

	"open-sds/app/internal/streamview"
)

// streamPanel is a panel streaming its bus: the status's stream and the LCD's
// transcript list.
type streamPanel struct {
	panelFixture
	on   bool
	rows int // the rows last asked for
}

// TRLC-LINKS: REQ-SDS-018
func (p *streamPanel) StreamStatus() (streamview.View, bool) {
	return streamview.View{State: streamview.Streaming, Label: "UART 115200 b/s C1", Units: 50174, UnitsPerSecond: 8778,
		Lines: 3212, TriggerLine: -1}, p.on
}

// TRLC-LINKS: REQ-SDS-018
func (p *streamPanel) StreamWindow(rows int) (streamview.Page, bool) {
	p.rows = rows
	pg := streamview.Page{Header: "         t ms        hex", Total: 3212, Top: 3212 - rows, TriggerLine: -1}
	for i := 0; i < rows; i++ {
		pg.Lines = append(pg.Lines, fmt.Sprintf("  %+11.4f        48 69 20 55 AA 0F F0 0A", 1.8227*float64(i)))
	}
	return pg, p.on
}

// /api/stream/window serves the scope's newest transcript lines as its LCD
// list writes them, for the web's BUS; rows is bounded by the LCD's list; no
// stream is a 404.
// TRLC-LINKS: REQ-SDS-018
func TestStreamWindowServesTheLCDList(t *testing.T) {
	p := &streamPanel{on: true}
	s := New(&fakeScope{}, nil, p, nil)
	code, out := httpCall(t, s, "GET", "/api/stream/window?rows=3", nil)
	lines, _ := out["Lines"].([]any)
	if code != 200 || p.rows != 3 || len(lines) != 3 || out["Header"] != "         t ms        hex" || lines[2] != fmt.Sprintf("  %+11.4f        48 69 20 55 AA 0F F0 0A", 3.6454) {
		t.Fatalf("window: %d %v (rows %d)", code, out, p.rows)
	}
	if _, _ = httpCall(t, s, "GET", "/api/stream/window?rows=999", nil); p.rows != streamview.ListRows {
		t.Fatalf("rows not bounded by the LCD's list: %d", p.rows)
	}
	_, st := httpCall(t, s, "GET", "/api/status", nil)
	if sm, _ := st["stream"].(map[string]any); sm == nil || sm["state"] != "STREAM" || sm["label"] != "UART 115200 b/s C1" || sm["units"] != 50174.0 {
		t.Fatalf("the status's stream: %v", st["stream"])
	}
	p.on = false
	if code, _ := httpCall(t, s, "GET", "/api/stream/window", nil); code != 404 {
		t.Fatalf("no stream: %d", code)
	}
	if code, _ := httpCall(t, New(&fakeScope{}, nil, &panelStatusOnly{}, nil), "GET", "/api/stream/window", nil); code != 404 {
		t.Fatalf("a panel without a stream: %d", code)
	}
}

// webCaptureScope holds a web page's decoded capture until EndWebDecodedCapture.
type webCaptureScope struct {
	*fakeScope
	ends int
}

// TRLC-LINKS: REQ-SDS-013
func (s *webCaptureScope) EndWebDecodedCapture(context.Context) (bool, error) {
	s.ends++
	s.mu.Lock()
	open := s.stats.DecodedWeb
	s.stats.DecodedWeb = false
	s.mu.Unlock()
	return open, nil
}

// A page's Run, Run/Stop and Single end a web page's decoded capture first, as
// the scope's keys do (a page closed mid-session must not leave the
// acquisition waiting); without one they do not ask.
// TRLC-LINKS: REQ-SDS-013
func TestWebRunEndsAWebCapture(t *testing.T) {
	for _, c := range []struct {
		control string
		value   float64
	}{{"run", 1}, {"run", 0}, {"runtoggle", 0}, {"single", 0}} {
		f := &webCaptureScope{fakeScope: &fakeScope{}}
		f.stats.DecodedWeb = true
		s := New(f, nil, nil, nil)
		httpCall(t, s, "POST", "/api/set", map[string]any{"control": c.control, "value": c.value})
		if f.ends != 1 || f.Snapshot().DecodedWeb {
			t.Fatalf("%s %g: %d ends, open %v", c.control, c.value, f.ends, f.Snapshot().DecodedWeb)
		}
		httpCall(t, s, "POST", "/api/set", map[string]any{"control": c.control, "value": c.value})
		if f.ends != 1 {
			t.Fatalf("%s without a web capture asked: %d", c.control, f.ends)
		}
	}
}
