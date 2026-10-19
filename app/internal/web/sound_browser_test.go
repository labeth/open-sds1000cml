// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
	"open-sds/app/internal/testenv"
)

// soundScope is a scope with an audio stream: A4 three cents sharp on C1
// (2 V peak), C2 the same tone at half amplitude 0.1 ms later, generated as
// real time passes, with the FPGA's marks on C1's rising zero crossings.
// TRLC-LINKS: REQ-SDS-274
type soundScope struct {
	*fakeScope
	t0     time.Time
	fs     float64
	leases int
	mu     sync.Mutex
}

// TRLC-LINKS: REQ-SDS-274
func (s *soundScope) code(ch int, i uint64) uint16 {
	t := float64(i) / s.fs
	f := 440 * math.Pow(2, 3.0/1200)
	v := 2 * math.Sin(2*math.Pi*f*t)
	if ch == 1 {
		v = math.Sin(2*math.Pi*f*(t-1e-4)) + 0.02*math.Sin(4*math.Pi*f*t)
	}
	return uint16(math.Round(v/0.04*256) + 32768)
}

// TRLC-LINKS: REQ-SDS-274
func (s *soundScope) nextOrd() uint64 { return uint64(time.Since(s.t0).Seconds() * s.fs) }

// TRLC-LINKS: REQ-SDS-274
func (s *soundScope) LeaseSound(time.Duration) { s.mu.Lock(); s.leases++; s.mu.Unlock() }

// TRLC-LINKS: REQ-SDS-274
func (s *soundScope) SoundInfo() engine.SoundInfo {
	return engine.SoundInfo{State: "streaming", Active: true, RateHz: s.fs, Next: s.nextOrd(), Marks: true,
		MarkCfg: engine.SoundMarkConfig{Ch: 0, Level: 128, Hyst: 2, Rising: true}}
}

// TRLC-LINKS: REQ-SDS-274
func (s *soundScope) SoundRead(from uint64, n int, q1, q2 []uint16) engine.SoundBlock {
	next := s.nextOrd()
	if next > 1<<20 && from < next-1<<20 {
		from = next - 1<<20
	}
	if from > next {
		from = next
	}
	m := int(min(uint64(n), next-from))
	b := engine.SoundBlock{First: from, N: m, RateHz: s.fs, Next: next, Scales: []engine.SoundScale{{VdivV: [2]float64{1, 1}}}}
	for i := 0; i < m; i++ {
		o := from + uint64(i)
		b.Q1 = append(b.Q1, s.code(0, o))
		b.Q2 = append(b.Q2, s.code(1, o))
		if i > 0 && b.Q1[i-1] < 32768 && b.Q1[i] >= 32768 {
			b.Marks = append(b.Marks, engine.SoundMark{At: o, Ch: 0, Rising: true})
		}
	}
	return b
}

// TRLC-LINKS: REQ-SDS-274
func (s *soundScope) SoundWait(after uint64, timeout time.Duration) uint64 {
	end := time.Now().Add(timeout)
	for s.nextOrd() <= after && time.Now().Before(end) {
		time.Sleep(5 * time.Millisecond)
	}
	return s.nextOrd()
}

// soundPanelFake keeps SOUND's settings and answers the actions.
// TRLC-LINKS: REQ-SDS-274
type soundPanelFake struct {
	mu      sync.Mutex
	set     panel.SoundSettings
	actions []string
}

// TRLC-LINKS: REQ-SDS-274
func (p *soundPanelFake) InjectButton(string) bool { return true }

// TRLC-LINKS: REQ-SDS-274
func (p *soundPanelFake) InjectKnob(string, int, int) bool { return true }

// TRLC-LINKS: REQ-SDS-274
func (p *soundPanelFake) SoundSettings() panel.SoundSettings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.set
}

// TRLC-LINKS: REQ-SDS-274
func (p *soundPanelFake) SetSoundSettings(s panel.SoundSettings) panel.SoundSettings {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.set = s
	return s
}

// TRLC-LINKS: REQ-SDS-274
func (p *soundPanelFake) SoundAction(a string) error {
	p.mu.Lock()
	p.actions = append(p.actions, a)
	p.mu.Unlock()
	return nil
}

// TRLC-LINKS: REQ-SDS-274
func (p *soundPanelFake) SoundReport(api, marks bool) panel.SoundReport {
	return panel.SoundReport{Settings: p.SoundSettings(), ADSR: "idle"}
}

// TRLC-LINKS: REQ-SDS-274
func (p *soundPanelFake) SoundWant(api, marks bool) {}

// TestSoundBrowser drives the web SOUND workspace in headless Chromium: Alt+6,
// the stream arriving, the tuner (A4 +3 cents), the meters, the cycle trace
// from the marks, Listen playing through the AudioWorklet (primed, no
// underruns), each view, A/B's gain and phase, the settings posted to the
// scope, and leaving SOUND stopping the stream. Skips without node/browser
// (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-274, REQ-SDS-265
func TestSoundBrowser(t *testing.T) {
	testenv.NeedNode(t)
	gen := func() *engine.Frame {
		const N = 1024
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := range c1 {
			c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*8*float64(i)/N))
			c2[i] = 128
		}
		return &engine.Frame{C1: c1, C2: c2, Seq: 1, Valid: N, WinCols: N, EdgeX: -1, TdivS: 1e-3, DisplayedS: 1e-3, SampleS: 1e-5, Trigd: true}
	}
	sc := &soundScope{fakeScope: &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}, t0: time.Now(), fs: 500e6 / 8192}
	pp := &soundPanelFake{set: panel.SoundSettings{A4: 440, Ref: "dbu", Cycles: 2, Gate: -1, GateV: 0.1}}
	srv := httptest.NewServer(New(sc, nil, pp, nil).Handler())
	defer srv.Close()
	out, err := exec.Command("node", "sound_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("sound_browser.mjs:\n%s", out)
	if strings.Contains(string(out), "SKIP:") {
		testenv.SkipBrowser(t, "browser driver skipped: %s", firstLine(out))
	}
	if err != nil {
		t.Fatalf("browser e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("browser e2e did not report ALL PASS")
	}
	pp.mu.Lock()
	defer pp.mu.Unlock()
	if pp.set.A4 != 442 || pp.set.Ch != 0 {
		t.Fatalf("the page's settings did not reach the scope: %+v", pp.set)
	}
	if len(pp.actions) == 0 || pp.actions[0] != "voct_capture" {
		t.Fatalf("actions %v", pp.actions)
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.leases < 5 {
		t.Fatalf("the page leased the stream %d times", sc.leases)
	}
}
