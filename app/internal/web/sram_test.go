// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"open-sds/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-166
type sramTestSource struct {
	m             sramcapture.Metadata
	cfg           sramcapture.Config
	offset, count uint32
	calls         int
	err           error
}

// TRLC-LINKS: REQ-SDS-166
func (s *sramTestSource) Status() (sramcapture.Metadata, error)             { return s.m, nil }
// TRLC-LINKS: REQ-SDS-166
func (s *sramTestSource) Arm(_ context.Context, c sramcapture.Config) error { s.cfg = c; return s.err }
// TRLC-LINKS: REQ-SDS-166
func (s *sramTestSource) Force(context.Context) error                       { return s.err }
// TRLC-LINKS: REQ-SDS-166
func (s *sramTestSource) Halt(context.Context) error                        { return s.err }
// TRLC-LINKS: REQ-SDS-166
func (s *sramTestSource) Snapshot(context.Context) ([10]uint8, error) {
	return [10]uint8{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, nil
}
// TRLC-LINKS: REQ-SDS-166
func (s *sramTestSource) Recall(_ context.Context, off, n uint32, w io.Writer) (int64, error) {
	s.calls++
	s.offset = off
	s.count = n
	if s.err != nil {
		return 0, s.err
	}
	return io.CopyN(w, strings.NewReader(strings.Repeat("abcd", int(n))), int64(n)*4)
}
// TRLC-LINKS: REQ-SDS-166
func TestSRAMFullRecordAndWindowAPI(t *testing.T) {
	s := &sramTestSource{m: sramcapture.Metadata{Ready: true, Frozen: true, Length: sramcapture.Words}}
	h := SRAMHandler(s)
	for _, q := range []struct {
		url       string
		offset, n uint32
	}{{"/api/sram/record.bin", 0, sramcapture.Words}, {"/api/sram/record.bin?offset=524280&words=8", 524280, 8}} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", q.url, nil))
		if rr.Code != 200 || rr.Body.Len() != int(q.n)*4 || s.offset != q.offset || s.count != q.n {
			t.Fatalf("window truncated or changed: status=%d bytes=%d off=%d count=%d", rr.Code, rr.Body.Len(), s.offset, s.count)
		}
	}
	before := s.calls
	for _, url := range []string{"/api/sram/record.bin?offset=524288&words=1", "/api/sram/record.bin?words=4294967295", "/api/sram/record.bin?offset=-1"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", url, nil))
		if rr.Code != 400 {
			t.Fatalf("invalid window: %s -> %d", url, rr.Code)
		}
	}
	if s.calls != before {
		t.Fatal("invalid window reached acquisition owner")
	}
	s.m.Frozen = false
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/sram/record.bin", nil))
	if rr.Code != 409 {
		t.Fatal("served live record")
	}
}
// TRLC-LINKS: REQ-SDS-166
func TestSRAMArmIsExplicitAndErrorsRemainJSON(t *testing.T) {
	s := &sramTestSource{m: sramcapture.Metadata{Ready: true, Frozen: true, Length: 1}}
	h := SRAMHandler(s)
	body := `{"source":0,"pair":4,"pre_words":262144,"post_words":262144,"normal":true,"trigger_level":128}`
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/sram/arm", strings.NewReader(body)))
	if rr.Code != 200 || s.cfg.PreWords != 262144 || s.cfg.Pair != 4 || !s.cfg.Normal {
		t.Fatal("configuration changed or truncated")
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/sram/arm", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatal("GET armed capture")
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/sram/arm", strings.NewReader(body+`{}`)))
	if rr.Code != 400 {
		t.Fatal("accepted trailing JSON")
	}
	s.err = errors.New("record changed")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/sram/record.bin", nil))
	if rr.Code != 409 || rr.Header().Get("Content-Type") != "application/json" || rr.Header().Get("Content-Length") != "" || rr.Header().Get("Content-Disposition") != "" {
		t.Fatal("error advertised as a binary download")
	}
}
