package panel

import (
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// flatFrame builds a perfectly FLAT display frame (every sample == v). This is what the
// app drain/decode collapses the fabric triangle to in the native-fast band — the frame
// SeedRefGate rejects (hi-lo < 12) and the frame that used to block device super-res.
func flatFrame(n int, v uint8) *engine.Frame {
	s := make([]uint8, n)
	for i := range s {
		s[i] = v
	}
	return &engine.Frame{C1: s, C2: s, Valid: n, EdgeX: float64(n / 2), SampleS: 1e-9}
}

// TestDeviceSelfSeedBypassesRefGate proves the DEVICE self-seed: with srDevice=true,
// srSeedAndStart arms on a FLAT frame that the HOST reference-lock rejects (no
// SeedRefGate gate), seeds the fixed fabric geometry (GridL=64,K=4, whole-grid gate,
// Align=srCh, SampleS=band), and — driven by a scripted CombineDrain grid — populates
// a review SuperresView. The same flat frame is shown to REJECT under host mode, pinning
// that the two paths diverge exactly at SeedRefGate.
func TestDeviceSelfSeedBypassesRefGate(t *testing.T) {
	c, eng, _ := newC(t)
	flat := flatFrame(1000, 128)
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(flat) })

	// HOST mode: the flat frame has no feature → SeedRefGate rejects, arm fails.
	c.mu.Lock()
	c.srDevice, c.srCh = false, 0
	c.mu.Unlock()
	if c.srSeedAndStart() {
		t.Fatalf("HOST srSeedAndStart armed on a flat frame — SeedRefGate should reject it")
	}
	c.mu.Lock()
	hostStatus, hostActive := c.srStatus, c.srActive
	c.mu.Unlock()
	if hostActive {
		t.Fatalf("HOST arm left srActive=true on a rejected flat frame")
	}
	if hostStatus == "" || hostStatus[:3] != "ref" {
		t.Fatalf("HOST reject status = %q, want the 'ref unusable ...' rejection", hostStatus)
	}

	// DEVICE mode: SAME flat frame must arm (SeedRefGate skipped) and self-seed the
	// fabric geometry. Scripted 3072-word grid (max bin count 100) with a stacks target
	// of 1 → the first drained grid reaches review and publishes the mean.
	words, _ := deviceGrid(srDevGridL, srDevK)
	eng.combineOut = engine.CombineOut{Words: words, Frames: 1, SampleS: 1e-9}
	eng.combineOK = true
	c.mu.Lock()
	c.srDevice, c.srCh, c.srStopMode, c.srStopVal = true, 0, 1, 1
	c.mu.Unlock()

	if !c.srSeedAndStart() {
		c.mu.Lock()
		st := c.srStatus
		c.mu.Unlock()
		t.Fatalf("DEVICE srSeedAndStart REJECTED a flat frame (status %q) — self-seed must bypass SeedRefGate", st)
	}

	// The self-seed is on the fixed fabric geometry, not the host srK/frame width.
	v0 := c.SuperresView()
	if !v0.Active {
		t.Fatalf("device arm did not set srActive")
	}
	if v0.Align != 0 || v0.K != srDevK || v0.N != srDevGridL {
		t.Fatalf("self-seed geometry = {Align:%d K:%d N:%d}, want {0 %d %d}", v0.Align, v0.K, v0.N, srDevK, srDevGridL)
	}
	if v0.GateLo != 0 || v0.GateHi != srDevGridL {
		t.Fatalf("self-seed gate = [%d,%d], want [0,%d] (whole crunched grid)", v0.GateLo, v0.GateHi, srDevGridL)
	}
	if v0.WinLo != 0 || v0.WinHi != srDevGridL {
		t.Fatalf("device review span = [%d,%d], want [0,%d]", v0.WinLo, v0.WinHi, srDevGridL)
	}
	if v0.Status == "ref unusable (flat/clipped) - freeze a cleaner frame" {
		t.Fatalf("device path emitted the host reject status — SeedRefGate was NOT bypassed")
	}

	// The started srLoop drains the scripted grid and reaches review. Poll SuperresView
	// (c.mu-guarded snapshot) until the crunched mean is published.
	var v SuperresView
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v = c.SuperresView()
		if v.Focus == 3 && len(v.Mean) == srDevGridL*srDevK {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.srCancel("") // stop the stacker goroutine before the test returns

	if v.Focus != 3 {
		t.Fatalf("SuperresView never reached review (Focus=%d) — device loop did not populate", v.Focus)
	}
	if len(v.Mean) != srDevGridL*srDevK {
		t.Fatalf("SuperresView.Mean len = %d, want %d", len(v.Mean), srDevGridL*srDevK)
	}
	// The published review trace (falloff-compensated, like the web) is fully filled
	// from the injected grid: no gap sentinels, real signal — the self-seeded stack
	// rendered the drained grid. (Byte-exact crunch is pinned by TestDeviceSuperresFullPath.)
	var sum float64
	for b, m := range v.Mean {
		if m < 0 {
			t.Fatalf("SuperresView.Mean[%d] = %v is a gap — self-seeded grid not fully populated", b, m)
		}
		sum += float64(m)
	}
	if sum <= 0 {
		t.Fatalf("SuperresView.Mean is all zero — no signal rendered from the drained grid")
	}
}
