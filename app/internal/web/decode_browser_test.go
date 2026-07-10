package web

import (
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// i2cWave renders one I2C transaction (START, addr 0x50 W, ACK, 0x00, ACK, 0xFF,
// NAK, STOP) onto SCL/SDA code arrays of length n. SPB=40 cols/clock keeps it
// well above the decoder's resolution guard.
func i2cWave(n int) (scl, sda []uint8) {
	const lo, hi, SPB = 40, 210, 40
	half := SPB / 2
	c := func(b bool) uint8 {
		if b {
			return hi
		}
		return lo
	}
	seg := func(cols int, s, d bool) {
		for k := 0; k < cols; k++ {
			scl = append(scl, c(s))
			sda = append(sda, c(d))
		}
	}
	bit := func(b bool) { seg(half, false, b); seg(half, true, b) } // SDA set low-phase, sampled on rising
	emit := func(v int) {
		for i := 7; i >= 0; i-- {
			bit((v>>uint(i))&1 == 1)
		}
	}
	seg(SPB*2, true, true) // idle
	seg(half, true, true)
	seg(half, true, false) // START: SDA falls while SCL high
	emit((0x50 << 1) | 0)
	bit(false) // addr 0x50 W + ACK
	emit(0x00)
	bit(false) // data 0x00 + ACK
	emit(0xFF)
	bit(true) // data 0xFF + NAK
	seg(half, false, false)
	seg(half, true, false)
	seg(half, true, true) // STOP
	for len(scl) < n {
		scl = append(scl, hi)
		sda = append(sda, hi)
	}
	return scl[:n], sda[:n]
}

// TestDecodeBrowser drives the real ui.html in headless Chromium against a local
// server serving a synthetic I2C frame, and checks the decode transcript, the
// byte count, the Copy button, and the navigator wheel-zoom + reset. Fully
// device-independent; self-skips when node/Playwright are unavailable.
func TestDecodeBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const N = 2048
	scl, sda := i2cWave(N)
	var seq atomic.Int64
	gen := func() *engine.Frame {
		n := seq.Add(1)
		c1 := make([]uint8, N)
		copy(c1, scl)
		c2 := make([]uint8, N)
		copy(c2, sda)
		return &engine.Frame{
			C1: c1, C2: c2, Seq: uint64(n), Valid: N, WinCols: N,
			EdgeX: -1, TdivS: 100e-6, DisplayedS: 100e-6, SampleS: 100e-6 * 10 / N,
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "decode_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("decode_browser.mjs:\n%s", out)
	if strings.Contains(string(out), "SKIP:") {
		testenv.SkipBrowser(t, "browser driver skipped: %s", firstLine(out))
	}
	if err != nil {
		t.Fatalf("decode browser e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("decode browser e2e did not report ALL PASS")
	}
}
