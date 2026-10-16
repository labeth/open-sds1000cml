// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"open-sds/app/internal/engine"
)

// TRLC-LINKS: REQ-SDS-162
func TestSerialTriggerEndpoints(t *testing.T) {
	fs := &fakeScope{}
	s := New(fs, nil, nil, nil)

	// config upload → SetSerialParams (with clamping)
	body, _ := json.Marshal(map[string]any{
		"proto": 2, "chA": 0, "chB": 1, "addr": 0x50, "rw": 0,
		"bytes": []int{0xDE, 0x1FF /*→clamped 255*/, -3 /*→0*/},
	})
	req := httptest.NewRequest("POST", "/api/serial", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("/api/serial code=%d", rec.Code)
	}
	p := fs.serialParams
	if p.Proto != 2 || p.ChA != 0 || p.ChB != 1 || p.Addr != 0x50 || p.RW != 0 {
		t.Fatalf("serial params not applied: %+v", p)
	}
	if !reflect.DeepEqual(p.Bytes, []int{0xDE, 255, 0}) {
		t.Fatalf("bytes not clamped: %v", p.Bytes)
	}
	for _, tc := range []struct{ proto, bits, max int }{
		{1, 9, 511}, {4, 16, 65535}, {5, 0, 15}, {7, 0, 65535}, {8, 0, 0x7ffff},
	} {
		body, _ = json.Marshal(map[string]any{"proto": tc.proto, "bits": tc.bits, "bytes": []int{tc.max, tc.max + 1, -1}})
		rec = httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/serial", bytes.NewReader(body)))
		if rec.Code != 200 || !reflect.DeepEqual(fs.serialParams.Bytes, []int{tc.max, tc.max, 0}) {
			t.Fatalf("protocol %d lost word width: %v", tc.proto, fs.serialParams.Bytes)
		}
	}

	// a UART's rate reaches the fast image's receiver (62.5 MBd); past any
	// receiver it is cleared
	for _, tc := range []struct{ baud, want int }{{62_500_000, 62_500_000}, {25_000_000, 25_000_000}, {65_000_000, 0}} {
		body, _ = json.Marshal(map[string]any{"proto": 1, "baud": tc.baud, "bytes": []int{0x55}})
		rec = httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/serial", bytes.NewReader(body)))
		if got := fs.serialParams.Baud; rec.Code != 200 || got != tc.want {
			t.Fatalf("baud %d posted: the trigger has %d, want %d", tc.baud, got, tc.want)
		}
	}

	// the SPI clock rate, the data delay and the receiver stay the scope's
	// (its BUS rows set them): a post, a stale page's too, keeps them
	fs.serialParams.SPIClockHz, fs.serialParams.SPIDelayNs, fs.serialParams.Fine = 104_000_000, 7.5, true
	body, _ = json.Marshal(map[string]any{"proto": 3, "spiClockHz": 10_400_000, "bytes": []int{0x9F}})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/serial", bytes.NewReader(body)))
	if p := fs.serialParams; p.SPIClockHz != 104_000_000 || p.SPIDelayNs != 7.5 || !p.Fine || p.Proto != 3 || !reflect.DeepEqual(p.Bytes, []int{0x9F}) {
		t.Fatalf("a post replaced the scope's SPI timing or lost its match: %+v", p)
	}

	// arm via /api/set
	post(t, s, "serialmode", 1)
	if fs.serialMode != 1 {
		t.Fatalf("serialmode not armed: %d", fs.serialMode)
	}
	post(t, s, "serialmode", 0)
	if fs.serialMode != 0 {
		t.Fatalf("serialmode not disarmed: %d", fs.serialMode)
	}

	// bad json → ok:false, no panic
	req = httptest.NewRequest("POST", "/api/serial", bytes.NewReader([]byte("{garbage")))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["ok"] != false {
		t.Fatalf("bad json should return ok:false, got %v", out)
	}

	// GET reads the trigger as armed, so a page can show it rather than
	// replace it; another method is refused
	fs.SetSerialParams(engine.SerialParams{Proto: 1, Bytes: []int{0xAA}, Addr: -1, RW: 2})
	req = httptest.NewRequest("GET", "/api/serial", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var got struct {
		OK     bool                `json:"ok"`
		Params engine.SerialParams `json:"params"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.OK || got.Params.Proto != 1 || len(got.Params.Bytes) != 1 || got.Params.Bytes[0] != 0xAA {
		t.Fatalf("GET /api/serial: %s", rec.Body.String())
	}
	req = httptest.NewRequest("PUT", "/api/serial", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatalf("PUT /api/serial code=%d, want 405", rec.Code)
	}
}
