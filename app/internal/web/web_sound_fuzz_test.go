// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
)

// soundFakeScope is a scope with an audio stream of n samples from 1000 on:
// a read gives what the engine's ring would (clamped to what it holds).
type soundFakeScope struct {
	*fakeScope
	first, next uint64
}

// TRLC-LINKS: REQ-SDS-265
func (f *soundFakeScope) LeaseSound(time.Duration) {}

// TRLC-LINKS: REQ-SDS-265
func (f *soundFakeScope) SoundInfo() engine.SoundInfo {
	return engine.SoundInfo{State: "streaming", Active: true, RateHz: 61035.15625, Next: f.next, Oldest: f.first,
		MarkCfg: engine.SoundMarkConfig{Ch: 3, Level: 200}}
}

// TRLC-LINKS: REQ-SDS-265
func (f *soundFakeScope) SoundRead(from uint64, max int, q1, q2 []uint16) engine.SoundBlock {
	from = min(f.next, from)
	from = maxU(from, f.first)
	n := int(min(uint64(max), f.next-from))
	b := engine.SoundBlock{First: from, N: n, Next: f.next, RateHz: 61035.15625, Q1: make([]uint16, n), Q2: make([]uint16, n),
		Scales: []engine.SoundScale{{From: f.first, VdivV: [2]float64{1, 0.5}, OffV: [2]float64{math.NaN(), 0}}},
		Marks:  []engine.SoundMark{{At: from, Ch: 1, Rising: true}}}
	return b
}

// TRLC-LINKS: REQ-SDS-265
func maxU(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// TRLC-LINKS: REQ-SDS-265
func (f *soundFakeScope) SoundWait(after uint64, timeout time.Duration) uint64 { return f.next }

// soundFakePanel is the panel's SOUND service with a report full of the
// values JSON refuses.
type soundFakePanel struct {
	set panel.SoundSettings
}

// TRLC-LINKS: REQ-SDS-274
func (p *soundFakePanel) InjectButton(string) bool { return true }

// TRLC-LINKS: REQ-SDS-274
func (p *soundFakePanel) InjectKnob(string, int, int) bool { return true }

// TRLC-LINKS: REQ-SDS-274
func (p *soundFakePanel) SoundSettings() panel.SoundSettings { return p.set }

// TRLC-LINKS: REQ-SDS-274
func (p *soundFakePanel) SetSoundSettings(s panel.SoundSettings) panel.SoundSettings {
	p.set = s
	return s
}

// TRLC-LINKS: REQ-SDS-274
func (p *soundFakePanel) SoundAction(a string) error {
	if a != "adsr_arm" {
		return io.EOF
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-274
func (p *soundFakePanel) SoundReport(api, marks bool) panel.SoundReport {
	r := panel.SoundReport{Settings: p.set, Hz: math.NaN(), Cents: math.Inf(-1), CV: math.Inf(1)}
	r.ADSRRes.Start = math.NaN()
	for i := 0; i < 40; i++ {
		r.ADSREnv = append(r.ADSREnv, float64(i))
	}
	r.ADSREnv[20] = math.NaN()
	return r
}

// TRLC-LINKS: REQ-SDS-274
func (p *soundFakePanel) SoundWant(api, marks bool) {}

// TestSoundAPIHostile asks /api/sound.bin for any stretch and posts any
// body to /api/sound: no server error, a well-formed reply every time - a
// header that parses, its N samples of each channel after it - and NaN or
// infinite readings as null.
// TRLC-LINKS: REQ-SDS-265, REQ-SDS-274
func TestSoundAPIHostile(t *testing.T) {
	sc := &soundFakeScope{fakeScope: &fakeScope{}, first: 1000, next: 1000 + 70000}
	srv := httptest.NewServer(New(sc, nil, &soundFakePanel{}, nil).Handler())
	defer srv.Close()
	cl := srv.Client()
	for _, q := range []string{"", "from=0", "from=999", "from=1000", "from=71000", "from=99999999", "from=18446744073709551615",
		"from=-1", "from=x", "max=-1", "max=0", "max=99999999", "max=65536", "min=-5", "min=99999999&wait=1",
		"wait=-1", "wait=999999999", "wait=10&from=71000&min=1000", "marks=1&max=1", "from=18446744073709551000&min=60000&wait=5",
		"who=phone", "who=", "who=%00%ff", "who=" + strings.Repeat("p", 5000), "who=phone&who=web&marks=1"} {
		r, err := cl.Get(srv.URL + "/api/sound.bin?" + q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || len(b) < 8 || string(b[:3]) != "SB\x01" {
			t.Fatalf("%s: status %d, %d bytes", q, r.StatusCode, len(b))
		}
		hl := int(binary.LittleEndian.Uint32(b[4:8]))
		var h soundBinHeader
		if hl == 0 || 8+hl > len(b) || json.Unmarshal(b[8:8+hl], &h) != nil {
			t.Fatalf("%s: header of %d bytes does not parse: %q", q, hl, b[8:min(len(b), 8+hl)])
		}
		if len(b) != 8+hl+4*h.N || h.N > 65536 {
			t.Fatalf("%s: %d bytes for N %d", q, len(b), h.N)
		}
	}
	for _, body := range []string{``, `{`, `null`, `[]`, `{"settings":7}`, `{"settings":{"a4":"x"}}`, `{"settings":{"a4":1e999}}`,
		`{"settings":{"ch":99,"cycles":-4,"page":2147483648}}`, `{"action":"nope"}`, `{"action":"adsr_arm","settings":{}}`,
		`{"settings":{"ref":"` + strings.Repeat("A", 9000) + `"}}`, `{"action":"take_scope"}`, `{"action":"take_scope","settings":{"drift":true}}`,
		`{"settings":{"pre_s":-1,"post_s":1e308}}`, `{"settings":{"pre_s":1e308,"post_s":-1e308,"trig_gate":true}}`, `{"settings":{"pre_s":"x"}}`} {
		r, err := cl.Post(srv.URL+"/api/sound?who=phone", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		var out map[string]any
		if r.StatusCode >= 500 || json.Unmarshal(b, &out) != nil {
			t.Fatalf("%q: status %d, reply %q", body[:min(len(body), 60)], r.StatusCode, b[:min(len(b), 200)])
		}
	}
	for _, q := range []string{"", "?who=phone", "?x=%zz"} {
		r, err := cl.Get(srv.URL + "/api/sound/take.bin" + q)
		if err != nil {
			t.Fatalf("take.bin%s: %v", q, err)
		}
		r.Body.Close()
		if r.StatusCode >= 500 {
			t.Fatalf("take.bin%s: status %d", q, r.StatusCode)
		}
	}
}
