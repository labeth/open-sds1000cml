// ENGMODEL-OWNER-UNIT: FU-WEB-APP-SERIALTRIG
package web

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// A serial trigger armed elsewhere is shown by a page, not replaced by its
// settings: serialadopt_browser.mjs. The scope keeps its match until a match
// is typed on the page.
// TRLC-LINKS: REQ-SDS-206
func TestSerialTrigAdoptBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const N = 2048
	var seq uint64
	gen := func() *engine.Frame {
		seq++
		c1 := make([]uint8, N)
		for i := range c1 {
			c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*8*float64(i)/N))
		}
		return &engine.Frame{C1: c1, C2: c1, Seq: seq, Valid: N, WinCols: N, EdgeX: N / 2,
			TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	fs.SetSerialParams(engine.SerialParams{Proto: 1, Baud: 115200, Bits: 8, Bytes: []int{0x55, 0xAA}, Addr: -1, RW: 2})
	fs.SetSerialMode(engine.SerialTrigger)
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "serialadopt_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("serialadopt_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		testenv.SkipBrowser(t, "browser unavailable")
	}
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("serial adopt e2e failed: %v", err)
	}
	if p := fs.SerialConfig(); !slices.Equal(p.Bytes, []int{0x48, 0x69}) {
		t.Fatalf("the match typed on the page did not reach the scope: %+v", p)
	}
}

// The scope keeps its match while the serial trigger is off: a page shows it
// and its Trigger on match arms it, not "any byte" from the page's empty field
// (serialstored_browser.mjs); disarmed, the page follows a match set
// elsewhere.
// TRLC-LINKS: REQ-SDS-206
func TestSerialStoredMatchBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const N = 2048
	var seq uint64
	gen := func() *engine.Frame {
		seq++
		c1 := make([]uint8, N)
		for i := range c1 {
			c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*8*float64(i)/N))
		}
		return &engine.Frame{C1: c1, C2: c1, Seq: seq, Valid: N, WinCols: N, EdgeX: N / 2,
			TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9}
	}
	// The FPGA has no matcher yet (it is learning the line): the page says so.
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5,
		SerialBackend: "none: learning the line levels on C1"}}
	fs.SetSerialParams(engine.SerialParams{Proto: 1, Baud: 115200, Bits: 8, Bytes: []int{0x55, 0xAA}, Addr: -1, RW: 2})
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "serialstored_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("serialstored_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		testenv.SkipBrowser(t, "browser unavailable")
	}
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("serial stored-match e2e failed: %v", err)
	}
	if p := fs.SerialConfig(); !slices.Equal(p.Bytes, []int{0x12}) || fs.Snapshot().SerialMode != 0 {
		t.Fatalf("the match set elsewhere, disarmed: %+v mode %d", p, fs.Snapshot().SerialMode)
	}
}
