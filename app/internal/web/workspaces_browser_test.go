// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"fmt"
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// ---------------------------------------------------------------------------
// Browser tests for the task workspaces (SCOPE, BUS, SPECTRUM, MEASURE,
// COMPARE). wsScope is the control-responsive fuzzScope with a chosen signal,
// and it records the acquisition and mask calls the UI makes, so a driver's
// flow (BUS auto-decode, the COMPARE master) can be checked on both sides.
// ---------------------------------------------------------------------------

// wsScope serves sig(seq) on the fuzzScope state machine. With block set its
// average mode behaves like the deep-memory path: each capture names its
// progress ("block average k/N") and SINGLE completes on the full block; without
// it SINGLE stops after one capture, as on the fast path.
// TRLC-LINKS: REQ-SDS-219
type wsScope struct {
	*fuzzScope
	sig     func(seq uint64) (c1, c2 []uint8)
	sampleS float64 // fixed sample interval (0: follow the timebase)
	block   bool
	avgK    int
	cmu     sync.Mutex
	calls   []string
}

// TRLC-LINKS: REQ-SDS-219
func newWsScope(sig func(seq uint64) (c1, c2 []uint8), sampleS float64) *wsScope {
	return &wsScope{fuzzScope: newFuzzScope(), sig: sig, sampleS: sampleS}
}

// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) note(format string, a ...any) {
	w.cmu.Lock()
	w.calls = append(w.calls, fmt.Sprintf(format, a...))
	w.cmu.Unlock()
}

// Calls returns the recorded control calls in order.
// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) Calls() []string {
	w.cmu.Lock()
	defer w.cmu.Unlock()
	return append([]string(nil), w.calls...)
}

// WithFrame advances while running; SINGLE stops after one capture, or after a
// full block in block-average mode.
// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) WithFrame(fn func(*engine.Frame)) {
	z := w.fuzzScope
	z.mu.Lock()
	filter := ""
	if z.running {
		z.seq++
		done := true
		if z.acqMode == 1 && w.block {
			n := z.avgCount
			if n < 1 {
				n = 1
			}
			w.avgK++
			if w.avgK > n {
				w.avgK = 1
			}
			done = w.avgK >= n
		}
		if z.single && done {
			z.single, z.running = false, false
		}
	}
	if z.acqMode == 1 && w.block {
		filter = fmt.Sprintf("; block average %d/%d (waveform aligned)", w.avgK, z.avgCount)
	}
	f := z.frameLocked()
	z.mu.Unlock()
	f.C1, f.C2 = w.sig(f.Seq)
	f.Filter = filter
	if w.sampleS > 0 {
		f.SampleS = w.sampleS
		f.TdivS = w.sampleS * fuzzN / 10
		f.DisplayedS = f.TdivS
	}
	fn(f)
}

// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) SetAcqMode(m int) {
	w.note("acqmode %d", m)
	w.fuzzScope.SetAcqMode(m)
	w.fuzzScope.mu.Lock()
	w.avgK = 0
	w.fuzzScope.mu.Unlock()
}

// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) SetAvgCount(n int) {
	w.note("avgcount %d", n)
	w.fuzzScope.SetAvgCount(n)
	w.fuzzScope.mu.Lock()
	w.avgK = 0
	w.fuzzScope.mu.Unlock()
}

// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) SetSingle() { w.note("single"); w.fuzzScope.SetSingle() }

// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) SetNorm(on bool) { w.note("norm %t", on); w.fuzzScope.SetNorm(on) }

// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) SetRunning(on bool) { w.note("run %t", on); w.fuzzScope.SetRunning(on) }

// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) SetMask(m *engine.Mask) {
	if m != nil {
		w.note("mask win=%d", len(m.Lo))
	} else {
		w.note("mask nil")
	}
	w.fuzzScope.SetMask(m)
}

// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) SetMaskMode(m int) { w.note("maskmode %d", m); w.fuzzScope.SetMaskMode(m) }

// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) ClearMaskFails() { w.note("maskclear"); w.fuzzScope.ClearMaskFails() }

// TRLC-LINKS: REQ-SDS-219
func (w *wsScope) SetSerialMode(m int) { w.note("serialmode %d", m); w.fuzzScope.SetSerialMode(m) }

// ---- signals ----------------------------------------------------------------------

// uartSig is an 8N1 UART on C1, idle high, spb samples per bit: msg repeated
// from sample `start` with `gap` idle samples between messages until the
// record ends, so the last message is cut by the record edge. Edges take eight
// samples. C2 is flat.
// TRLC-LINKS: REQ-SDS-214
func uartSig(msg []byte, spb, start, gap int) func(uint64) ([]uint8, []uint8) {
	lv := make([]float64, fuzzN)
	for i := range lv {
		lv[i] = 200
	}
	pos := start
	for pos < fuzzN {
		for _, b := range msg {
			bits := []int{0}
			for k := 0; k < 8; k++ {
				bits = append(bits, int(b>>k)&1)
			}
			bits = append(bits, 1)
			for _, bit := range bits {
				for s := 0; s < spb && pos+s < fuzzN; s++ {
					if bit == 0 {
						lv[pos+s] = 56
					}
				}
				pos += spb
			}
		}
		pos += gap
	}
	// edges ramp over 8 samples (an 8-tap moving average), crossing mid-level
	// on a sample as a real edge's samples do
	c1 := make([]uint8, fuzzN)
	c2 := make([]uint8, fuzzN)
	for i := range c1 {
		s := 0.0
		for k := i - 4; k < i+4; k++ {
			s += lv[min(max(k, 0), fuzzN-1)]
		}
		c1[i] = uint8(math.Round(s / 8))
		c2[i] = 128
	}
	return func(uint64) ([]uint8, []uint8) { return c1, c2 }
}

// squareSig is a 50 % square of `period` samples on C1 (rising at the frame's
// trigger point, mid-record) and a small sine on C2. With free set, every
// capture lands at another phase: a signal that does not repeat.
// TRLC-LINKS: REQ-SDS-216, REQ-SDS-218
func squareSig(period int, free bool) func(uint64) ([]uint8, []uint8) {
	c2 := make([]uint8, fuzzN)
	for i := range c2 {
		c2[i] = uint8(128 + 30*math.Sin(2*math.Pi*float64(i)/341))
	}
	return func(seq uint64) ([]uint8, []uint8) {
		ph := 0
		if free {
			ph = int(seq*37) % period
		}
		c1 := make([]uint8, fuzzN)
		for i := range c1 {
			if ((i+ph)%period+period)%period < period/2 {
				c1[i] = 188
			} else {
				c1[i] = 68
			}
		}
		return c1, c2
	}
}

// ---- harness ----------------------------------------------------------------------

// wsDriver runs a node driver against the URLs and requires ALL PASS (SKIP
// without node/Playwright; a failure under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-219
func wsDriver(t *testing.T, script string, urls ...string) {
	t.Helper()
	out, err := exec.Command("node", append([]string{script}, urls...)...).CombinedOutput()
	t.Logf("%s:\n%s", script, out)
	if strings.Contains(string(out), "SKIP:") {
		testenv.SkipBrowser(t, "browser driver skipped: %s", firstLine(out))
	}
	if err != nil {
		t.Fatalf("%s failed: %v", script, err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("%s did not report ALL PASS", script)
	}
}

// wsSetups is a goroutine-safe setup store for the Setups popover test.
// TRLC-LINKS: REQ-SDS-219
type wsSetups struct {
	mu              sync.Mutex
	saved, recalled []int
}

// TRLC-LINKS: REQ-SDS-219
func (f *wsSetups) SaveSetup(n int) error {
	f.mu.Lock()
	f.saved = append(f.saved, n)
	f.mu.Unlock()
	return nil
}

// TRLC-LINKS: REQ-SDS-219
func (f *wsSetups) RecallSetup(n int) error {
	f.mu.Lock()
	f.recalled = append(f.recalled, n)
	f.mu.Unlock()
	return nil
}

// SetupSlots: slot 20 holds a setup from the start, a saved slot another.
// TRLC-LINKS: REQ-SDS-219
func (f *wsSetups) SetupSlots() []SetupSlot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]SetupSlot, 20)
	for i := range out {
		out[i].Slot = i + 1
	}
	out[19].Used, out[19].Summary = true, "MEASURE  20us/div  C1^ 1.37V  C1 1V C2 1V  UART"
	for _, n := range f.saved {
		out[n-1].Used, out[n-1].Summary = true, "SCOPE  1ms/div  C1^ 0V  C1 1V C2 1V"
	}
	return out
}

// TestWorkspacesBrowser: switching (tabs, Alt+digit by key code, roving tab
// keys), "again" per workspace, the view each workspace remembers, the dots,
// each workspace's panes, maximize and collapse resizing the trace canvas,
// the help sheet, and the single-letter shortcuts (ignored while typing).
// TRLC-LINKS: REQ-SDS-219
func TestWorkspacesBrowser(t *testing.T) {
	testenv.NeedNode(t)
	sc := newWsScope(squareSig(64, false), 0)
	srv := httptest.NewServer(New(sc, newFuzzAnalog(), &fuzzPanel{z: sc.fuzzScope}, nil).Handler())
	defer srv.Close()
	wsDriver(t, "workspaces_browser.mjs", srv.URL)
}

// TestAllSettingsBrowser: every control of the page is in All settings (or
// named as navigation), each row's arrow shows its control where it lives, an
// edit in a row is the edit of the control, and Setups shows what each slot
// holds, saves and recalls.
// TRLC-LINKS: REQ-SDS-219
func TestAllSettingsBrowser(t *testing.T) {
	testenv.NeedNode(t)
	sc := newWsScope(squareSig(64, false), 0)
	mem := &wsSetups{}
	s := New(sc, newFuzzAnalog(), &fuzzPanel{z: sc.fuzzScope}, nil)
	s.SetSetupMemory(mem)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	wsDriver(t, "allsettings_browser.mjs", srv.URL)
	mem.mu.Lock()
	defer mem.mu.Unlock()
	if len(mem.saved) != 1 || mem.saved[0] != 3 || len(mem.recalled) != 1 || mem.recalled[0] != 3 {
		t.Fatalf("Setups popover: saved %v recalled %v, want [3] [3]", mem.saved, mem.recalled)
	}
}

// TestBusBrowser: entering BUS with decoding off finds the UART and turns
// decoding on; the packet table lists only complete packets in fixed columns
// (hex, then a left-aligned string), holds still while the message repeats,
// zooms onto a clicked packet; BUS again shows the eye with its figures.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-215
func TestBusBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const spb = 20
	sc := newWsScope(uartSig([]byte("Hi"), spb, 300, 300), 1/(115200.0*spb))
	srv := httptest.NewServer(New(sc, newFuzzAnalog(), newDecodePanel(sc, &fuzzPanel{z: sc.fuzzScope}), nil).Handler())
	defer srv.Close()
	wsDriver(t, "bus_browser.mjs", srv.URL)
}

// TestSpectrumBrowser: SPECTRUM's peak table names the square's odd harmonics
// with level and relative level, the readout its THD; the small time trace
// draws; SPECTRUM again cycles FFT, spectrogram and Bode.
// TRLC-LINKS: REQ-SDS-216
func TestSpectrumBrowser(t *testing.T) {
	testenv.NeedNode(t)
	sc := newWsScope(squareSig(64, false), 0)
	srv := httptest.NewServer(New(sc, newFuzzAnalog(), &fuzzPanel{z: sc.fuzzScope}, nil).Handler())
	defer srv.Close()
	wsDriver(t, "spectrum_browser.mjs", srv.URL)
}

// TestMeasureBrowser: MEASURE's statistics count every capture and reset; All
// values lists every parameter of both channels; the cursors read out.
// TRLC-LINKS: REQ-SDS-217
func TestMeasureBrowser(t *testing.T) {
	testenv.NeedNode(t)
	sc := newWsScope(squareSig(64, false), 0)
	srv := httptest.NewServer(New(sc, newFuzzAnalog(), &fuzzPanel{z: sc.fuzzScope}, nil).Handler())
	defer srv.Close()
	wsDriver(t, "measure_browser.mjs", srv.URL)
}

// TestCompareBrowser: the difference readout against a reference, and Stack
// master: the page has the scope stack its master and takes the scope's REF A
// (compare_browser.mjs mocks the scope's master row; the panel's master has
// its own tests). The page drives no acquisition of its own: averaging through
// SINGLE times out on the SRAM image.
// TRLC-LINKS: REQ-SDS-218
func TestCompareBrowser(t *testing.T) {
	testenv.NeedNode(t)
	sc := newWsScope(squareSig(64, false), 0)
	sc.block = true
	srv := httptest.NewServer(New(sc, newFuzzAnalog(), &fuzzPanel{z: sc.fuzzScope}, nil).Handler())
	defer srv.Close()
	wsDriver(t, "compare_browser.mjs", srv.URL)
	for _, c := range sc.Calls() {
		if strings.HasPrefix(c, "acqmode") || strings.HasPrefix(c, "avgcount") || c == "single" || strings.HasPrefix(c, "mask") {
			t.Fatalf("the page drove the acquisition or the mask itself: %v", sc.Calls())
		}
	}
}
