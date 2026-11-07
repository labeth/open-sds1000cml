// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/testenv"
	"github.com/labeth/open-dso-core/webserver/wire"
)

// recordScope is a fake scope whose bus-sample lease turns its frames from
// envelopes into records sampled at the interval leased, a new one each read
// (never: the lease changes nothing).
type recordScope struct {
	*fakeScope
	mu     sync.Mutex
	leased float64
	seq    uint64
	never  bool
}

// TRLC-LINKS: REQ-SDS-280
func (r *recordScope) LeaseBusSamples(sampleS float64, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.never {
		r.leased = sampleS
	}
}

// reads is the frames read so far.
// TRLC-LINKS: REQ-SDS-280
func (r *recordScope) reads() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

// TRLC-LINKS: REQ-SDS-280
func (r *recordScope) leasedAt() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.leased
}

// TRLC-LINKS: REQ-SDS-280
func (r *recordScope) init() {
	r.frameGen = func() *sample.Frame {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.seq++
		f := &sample.Frame{Seq: r.seq, SampleS: 1e-6, Content: sample.PeakPairs, Snapshot: sample.Settings{TdivS: 1e-3}, Traces: []sample.Trace{{Valid: 2048, Codes8: make([]uint8, 2048)}, {Ch: 1, Valid: 2048, Codes8: make([]uint8, 2048)}}}
		if r.leased > 0 {
			n := 200000
			f.Content, f.SampleS = sample.RawDecimated, r.leased
			f.SetLen(n)
			f.Traces[0].Codes8, f.Traces[1].Codes8 = make([]uint8, n), make([]uint8, n)
			for i := range f.Codes8(0) {
				f.Codes8(0)[i], f.Codes8(1)[i] = uint8(i), uint8(255-i%256)
			}
		}
		return f
	}
}

// TestRecordServesAWholeSampledRecord: /api/record.bin leases the bus samples
// at the interval asked, waits out the envelope frames and serves the first
// record sampled that finely whole: its header, then C1's and C2's codes.
// TRLC-LINKS: REQ-SDS-280
func TestRecordServesAWholeSampledRecord(t *testing.T) {
	sc := &recordScope{fakeScope: &fakeScope{}}
	sc.init()
	s := newServer(sc, nil, nil, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/record.bin?sample_s=2e-8&wait_ms=2000", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	b := rec.Body.Bytes()
	hl := int(binary.LittleEndian.Uint32(b))
	var h wire.RecordReply
	if err := json.Unmarshal(b[4:4+hl], &h); err != nil {
		t.Fatal(err)
	}
	if h.N != 200000 || h.SampleS != 2e-8 || len(b) != 4+hl+2*h.N {
		t.Fatalf("header %+v, %d bytes", h, len(b))
	}
	if b[4+hl+300] != uint8(300%256) || b[4+hl+h.N+300] != uint8(255-300%256) {
		t.Fatal("the codes are not the record's, C1 then C2")
	}
	if sc.leasedAt() != 2e-8 {
		t.Fatalf("leased %g, want 2e-8", sc.leasedAt())
	}
	// no record that fine within the wait: a conflict naming why
	sc2 := &recordScope{fakeScope: &fakeScope{}, never: true}
	sc2.init()
	rec = httptest.NewRecorder()
	newServer(sc2, nil, nil, nil).Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/record.bin?sample_s=2e-8&wait_ms=300", nil))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "err") {
		t.Fatalf("no record: code %d %s", rec.Code, rec.Body.String())
	}
	// one record at a time: a second request meanwhile is told so
	s2 := newServer(sc2, nil, nil, nil)
	before := sc2.reads()
	first := make(chan int)
	go func() {
		r := httptest.NewRecorder()
		s2.Handler().ServeHTTP(r, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/record.bin?sample_s=2e-8&wait_ms=600", nil))
		first <- r.Code
	}()
	for sc2.reads() < before+2 { // the first is taking its record
		time.Sleep(time.Millisecond)
	}
	rec = httptest.NewRecorder()
	s2.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/record.bin?sample_s=2e-8&wait_ms=100", nil))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "one at a time") {
		t.Fatalf("a second record meanwhile: code %d %s", rec.Code, rec.Body.String())
	}
	if c := <-first; c != 409 {
		t.Fatalf("the first: code %d", c)
	}
	rec = httptest.NewRecorder()
	s2.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/record.bin?sample_s=2e-8&wait_ms=1", nil))
	if strings.Contains(rec.Body.String(), "one at a time") {
		t.Fatalf("still busy after the first: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	newServer(sc2, nil, nil, nil).Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/record.bin?sample_s=0", nil))
	if rec.Code != 400 {
		t.Fatalf("a bad sample_s: code %d", rec.Code)
	}
}

// TestVisualStateJS runs visual.test.cjs: the Visual view's state and the
// digital audio decoded from the page's own decoders.
// TRLC-LINKS: REQ-SDS-278, REQ-SDS-279, REQ-SDS-280
func TestVisualStateJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "visual.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("the Visual view's state: %v\n%s", err, out)
	}
}
