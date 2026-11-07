// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/sample"
)

// A page counts as showing the scope once it asks for a frame, and for as
// long as it keeps asking; a server no page asked is not shown.
// TRLC-LINKS: REQ-SDS-163, REQ-SDS-168
func TestFrameClient(t *testing.T) {
	c1, c2 := make([]uint8, 800), make([]uint8, 800)
	s := newServer(&fakeScope{frame: &sample.Frame{Seq: 3, WinPoints: 800, TriggerAt: -1, SampleS: 2e-9, Snapshot: sample.Settings{TdivS: 1e-6}, Traces: []sample.Trace{{Codes8: c1, Valid: 800}, {Ch: 1, Codes8: c2, Valid: 800}}}, fresh: true}, nil, nil, nil)
	if s.FrameClient(time.Hour) {
		t.Fatal("a page shows the scope before any asked for a frame")
	}
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/frame.bin?since=0&cols=800", nil))
	if !s.FrameClient(time.Second) {
		t.Fatal("no page after a frame request")
	}
}
