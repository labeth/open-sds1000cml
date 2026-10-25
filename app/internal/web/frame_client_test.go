// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"net/http/httptest"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// A page counts as showing the scope once it asks for a frame, and for as
// long as it keeps asking; a server no page asked is not shown.
// TRLC-LINKS: REQ-SDS-163, REQ-SDS-168
func TestFrameClient(t *testing.T) {
	c1, c2 := make([]uint8, 800), make([]uint8, 800)
	s := New(&fakeScope{frame: &engine.Frame{C1: c1, C2: c2, Seq: 3, Valid: 800, WinCols: 800, EdgeX: -1, TdivS: 1e-6, SampleS: 2e-9}, fresh: true}, nil, nil, nil)
	if s.FrameClient(time.Hour) {
		t.Fatal("a page shows the scope before any asked for a frame")
	}
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/frame.bin?since=0&cols=800", nil))
	if !s.FrameClient(time.Second) {
		t.Fatal("no page after a frame request")
	}
	s.frameAt.Store(time.Now().Add(-5 * time.Second).UnixNano())
	if s.FrameClient(3 * time.Second) {
		t.Fatal("a page that stopped asking 5 s ago still shows the scope")
	}
}
