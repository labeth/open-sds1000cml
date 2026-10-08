// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"open-sds/app/internal/sramcapture"
	"strings"
	"testing"
)

type decodedWebScope struct {
	*fakeScope
	reads, starts, recalls int
	id                     sramcapture.RecordIdentity
}

type decodedReusableScope struct {
	*decodedWebScope
	storage *sramcapture.DecodedEvent
	reused  bool
}

// TRLC-LINKS: REQ-SDS-013
func (f *decodedReusableScope) ReadDecodedBatch(context.Context, sramcapture.RecordIdentity) ([]sramcapture.DecodedEvent, error) {
	return nil, fmt.Errorf("allocating fallback used")
}

// TRLC-LINKS: REQ-SDS-013
func (f *decodedReusableScope) ReadDecodedBatchInto(ctx context.Context, id sramcapture.RecordIdentity, dst []sramcapture.DecodedEvent) (int, error) {
	if f.storage != nil {
		f.reused = f.storage == &dst[0]
	}
	f.storage = &dst[0]
	batch, err := f.decodedWebScope.ReadDecodedBatch(ctx, id)
	return copy(dst, batch), err
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedHTTPReusesConsumerStorage(t *testing.T) {
	f := &decodedReusableScope{decodedWebScope: &decodedWebScope{fakeScope: &fakeScope{}, id: sramcapture.RecordIdentity{Epoch: 7, Record: 9}}}
	rr := httptest.NewRecorder()
	New(f, nil, nil, nil).Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/decoded/events.bin?epoch=7&record=9", nil))
	if rr.Code != 200 || rr.Body.Len() != 64 || !f.reused {
		t.Fatal("reusable transport", rr.Code, rr.Body.Len(), f.reused)
	}
	if rr.Result().Trailer.Get("X-Decoded-Error") != "transcript stopped" {
		t.Fatal("missing terminal error")
	}
}

// TRLC-LINKS: REQ-SDS-013
func (f *decodedWebScope) BeginDecodedCapture(_ context.Context, _ sramcapture.Config) (sramcapture.RecordIdentity, error) {
	f.starts++
	return f.id, nil
}

// TRLC-LINKS: REQ-SDS-013
func (f *decodedWebScope) ReadDecodedBatch(_ context.Context, id sramcapture.RecordIdentity) ([]sramcapture.DecodedEvent, error) {
	if id != f.id {
		return nil, fmt.Errorf("stale identity")
	}
	f.reads++
	if f.reads > 1 {
		return nil, fmt.Errorf("transcript stopped")
	}
	return []sramcapture.DecodedEvent{{Epoch: id.Epoch, Protocol: 1, Kind: sramcapture.EventData, Value: 0xa5}, {Epoch: id.Epoch, Sequence: 1, Kind: sramcapture.EventLoss, Count: 3}}, nil
}

// TRLC-LINKS: REQ-SDS-013
func (f *decodedWebScope) DecodedCaptureStatus(_ context.Context, id sramcapture.RecordIdentity) (sramcapture.Metadata, error) {
	return sramcapture.Metadata{RecordID: id.Record, EventEpoch: id.Epoch, Frozen: true}, nil
}

// TRLC-LINKS: REQ-SDS-013
func (f *decodedWebScope) RecallDecodedRecord(_ context.Context, id sramcapture.RecordIdentity, offset, count uint32) ([]byte, error) {
	f.recalls++
	if id != f.id {
		return nil, fmt.Errorf("stale identity")
	}
	return make([]byte, count*4), nil
}

// TRLC-LINKS: REQ-SDS-013
func (f *decodedWebScope) EndDecodedCapture(_ context.Context, _ sramcapture.RecordIdentity) error {
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedHTTPEventsAndLoss(t *testing.T) {
	f := &decodedWebScope{fakeScope: &fakeScope{}, id: sramcapture.RecordIdentity{Epoch: 7, Record: 9}}
	h := New(f, nil, nil, nil).Handler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/decoded/events.bin?epoch=7&record=9", nil))
	if rr.Code != 200 || rr.Body.Len() != 64 {
		t.Fatalf("response %d %s", rr.Code, rr.Body.String())
	}
	e, err := sramcapture.ParseDecodedEvent(rr.Body.Bytes()[32:])
	if err != nil || e.Kind != sramcapture.EventLoss || e.Count != 3 {
		t.Fatalf("loss %+v %v", e, err)
	}
	if rr.Result().Trailer.Get("X-Decoded-Error") != "transcript stopped" {
		t.Fatal("missing terminal stream error")
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedHTTPGuards(t *testing.T) {
	f := &decodedWebScope{fakeScope: &fakeScope{}, id: sramcapture.RecordIdentity{Epoch: 7, Record: 9}}
	h := New(f, nil, nil, nil).Handler()
	for _, url := range []string{"/api/decoded/record.bin?epoch=7&words=1&offset=0", "/api/decoded/record.bin?epoch=7&record=9&words=4294967295&offset=1"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", url, nil))
		if rr.Code != 400 {
			t.Fatal(rr.Code)
		}
	}
	if f.recalls != 0 {
		t.Fatal("invalid request reached acquisition")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/api/decoded/start", strings.NewReader(`{} {}`)))
	if rr.Code != 400 || f.starts != 0 {
		t.Fatal("multiple JSON objects accepted")
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/decoded/record.bin?epoch=6&record=9&offset=0&words=1", nil))
	if rr.Code != 409 {
		t.Fatal("stale record accepted")
	}
}
