package panel

import (
	"testing"

	"open-sds/app/internal/combine"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/superres"
)

// deviceGrid builds a known mean-only combine grid (bin-major, LSW-first, 12 words/bin)
// and the per-bin align codes it encodes, so the crunched mean can be checked exactly.
func deviceGrid(gridL, k int) (words []uint16, codeA []int) {
	nb := gridL * k
	words = make([]uint16, nb*combine.WordsPerBin)
	codeA = make([]int, nb)
	const cnt = 100
	for b := 0; b < nb; b++ {
		ca := 50 + b%100
		cb := 30 + b%120
		codeA[b] = ca
		w := words[b*combine.WordsPerBin:]
		w[0] = uint16(cnt)
		w[1] = uint16((ca * cnt) & 0xffff)
		w[2] = uint16((ca * cnt) >> 16)
		w[9] = uint16(cnt)
		w[10] = uint16((cb * cnt) & 0xffff)
		w[11] = uint16((cb * cnt) >> 16)
	}
	return
}

// TestDeviceSuperresFullPath drives the FULL app consumer path with a mock engine
// returning a known 3072-word device-combine grid: srDeviceTick → engine.CombineDrain
// → combine.Unpack → superres.Stack.InjectBins → Result → the SuperresView fields. It
// asserts a real crunched mean trace (Fill=1, 256 bins) whose per-bin value equals the
// injected code — proving the app consumes the drain byte-exact with NO prime read (a
// dropped/added word would misframe every bin), and that the review path surfaces the
// crunched trace through SuperresView.
func TestDeviceSuperresFullPath(t *testing.T) {
	const gridL, k = srDevGridL, srDevK // 64, 4 → 256 bins / 3072 words
	if combine.DrainWords(gridL, k) != 3072 {
		t.Fatalf("grid geometry drifted: DrainWords(%d,%d)=%d", gridL, k, combine.DrainWords(gridL, k))
	}
	words, codeA := deviceGrid(gridL, k)

	c, eng, _ := newC(t)
	eng.combineOut = engine.CombineOut{Words: words, Frames: 1, SampleS: 1e-9}
	eng.combineOK = true

	// Arm device super-res with a stacks target of 1 so the first drained grid (whose
	// max bin count is 100) immediately reaches review and publishes the mean.
	st := superres.New(gridL*k, k)
	c.mu.Lock()
	c.srActive, c.srDevice = true, true
	c.srStack = st
	c.srWinLo, c.srWinHi = 0, srDevGridL        // device review span (as srSeedAndStart sets it)
	c.srCh, c.srStopMode, c.srStopVal = 0, 1, 1 // align C1, stop on 1 stack
	c.mu.Unlock()

	reached := false
	c.srDeviceTick(st, &reached)
	if !reached {
		t.Fatalf("srDeviceTick did not reach the stop target (Hits should be 100 ≥ 1)")
	}

	// Byte-exact crunch on the injected stack: every bin populated (Fill=1), 256 bins,
	// mean == the encoded code. A prime read would shift the grid and break this.
	res := st.Result(false, 1)
	if res.Fill != 1 {
		t.Fatalf("Fill = %v, want 1 (all 256 bins populated)", res.Fill)
	}
	if len(res.Mean) != gridL*k {
		t.Fatalf("crunched mean len = %d, want %d", len(res.Mean), gridL*k)
	}
	for b := range res.Mean {
		if res.Mean[b] != float32(codeA[b]) {
			t.Fatalf("Mean[%d] = %v, want %v (misframed / not byte-exact)", b, res.Mean[b], codeA[b])
		}
	}

	// The device tick must have consumed exactly one CombineDrain (owner req/reply).
	drains := 0
	for _, cl := range eng.calls {
		if cl.what == "combinedrain" {
			drains++
		}
	}
	if drains != 1 {
		t.Fatalf("CombineDrain called %d times, want 1", drains)
	}

	// SuperresView must surface the review trace: 256-bin mean, no gaps (Fill=1).
	v := c.SuperresView()
	if v.Focus != 3 {
		t.Fatalf("SuperresView.Focus = %d, want 3 (review)", v.Focus)
	}
	if len(v.Mean) != gridL*k {
		t.Fatalf("SuperresView.Mean len = %d, want %d", len(v.Mean), gridL*k)
	}
	for b, m := range v.Mean {
		if m < 0 {
			t.Fatalf("SuperresView.Mean[%d] = %v is a gap (Fill<1)", b, m)
		}
	}
	if v.WinLo != 0 || v.WinHi != srDevGridL {
		t.Fatalf("device review span = [%d,%d], want [0,%d]", v.WinLo, v.WinHi, srDevGridL)
	}
}

// TestDeviceSuperresSkipsOnDrainFailure proves a failed drain (ok=false) skips the
// tick without touching the stack — the invariant that a partial grid is never
// crunched. Default fakeEng.combineOK is false.
func TestDeviceSuperresSkipsOnDrainFailure(t *testing.T) {
	c, eng, _ := newC(t)
	eng.combineOK = false // drain fails (timeout / overflow / queue-full)

	st := superres.New(256, srDevK)
	c.mu.Lock()
	c.srActive, c.srDevice = true, true
	c.srStack = st
	c.srCh, c.srStopMode, c.srStopVal = 0, 1, 1
	c.mu.Unlock()

	reached := false
	c.srDeviceTick(st, &reached)
	if reached {
		t.Fatalf("srDeviceTick reached review on a failed drain — must skip")
	}
	if st.Hits != 0 {
		t.Fatalf("stack mutated on a failed drain: Hits=%d", st.Hits)
	}
}

// TestDeviceDefaultOffHostUnchanged pins the byte-unchanged invariant: a fresh
// controller is in HOST mode (srDevice=false), so no combine request is ever issued.
func TestDeviceDefaultOffHostUnchanged(t *testing.T) {
	c, _, _ := newC(t)
	c.mu.Lock()
	dev := c.srDevice
	c.mu.Unlock()
	if dev {
		t.Fatalf("srDevice default = true, want false (host-drizzle path must be the default)")
	}
}
