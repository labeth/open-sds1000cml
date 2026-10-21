// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
)

// /api/sound/take.bin: no take while the stream is live (404); STOPped, the
// take in /api/sound.bin's format with its trigger, and /api/sound's info
// names it (mode, take id).
// TRLC-LINKS: REQ-SDS-275
func TestSoundTakeBin(t *testing.T) {
	sc := &soundScope{fakeScope: &fakeScope{stats: engine.Stats{Running: true}}, t0: time.Now().Add(-5 * time.Second), fs: 500e6 / 8192}
	pp := &soundPanelFake{set: panel.SoundSettings{A4: 440, Ref: "dbu", Cycles: 2, Gate: -1, GateV: 0.1}}
	srv := httptest.NewServer(New(sc, nil, pp, nil).Handler())
	defer srv.Close()
	r, err := http.Get(srv.URL + "/api/sound/take.bin")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("live: status %d", r.StatusCode)
	}
	sc.SetRunning(false)
	r, err = http.Get(srv.URL + "/api/sound/take.bin")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusOK || len(body) < 8 || body[0] != 'S' || body[1] != 'B' {
		t.Fatalf("stopped: status %d, %d bytes", r.StatusCode, len(body))
	}
	hl := binary.LittleEndian.Uint32(body[4:8])
	var h soundTakeHeader
	if err := json.Unmarshal(body[8:8+hl], &h); err != nil {
		t.Fatal(err)
	}
	if h.ID != 7 || !h.HasTrig || h.Trig-h.First != uint64(sc.fs) || h.N != int(3*sc.fs) || len(body) != 8+int(hl)+4*h.N || len(h.Scales) == 0 {
		t.Fatalf("header %+v, %d bytes", h, len(body))
	}
	var rep struct {
		Report struct {
			Info engine.SoundInfo `json:"info"`
		} `json:"report"`
	}
	r, err = http.Get(srv.URL + "/api/sound")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if rep.Report.Info.Mode != "stopped" || rep.Report.Info.Take != 7 {
		t.Fatalf("info %+v", rep.Report.Info)
	}
}
