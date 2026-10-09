// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"reflect"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/superres"
)

// Native review must show the accumulated mean without implicit deconvolution.
// Bandwidth compensation is an explicit browser option, not a default filter.
// TRLC-LINKS: REQ-SDS-140
func TestNativeStackReviewPreservesAccumulatedMean(t *testing.T) {
	c, _, _ := newC(t)
	st := superres.New(32, 8)
	st.SampleS = 2e-9
	sig := make([]uint8, 32)
	for i := range sig {
		sig[i] = uint8(60 + i*4)
	}
	for i := 0; i < 20; i++ {
		st.Feed(sig, sig, 16+float64(i)*.02)
	}
	want := st.Result(false, 1)
	c.srStack, c.srActive = st, true
	c.srReachReview(st, "done")
	if len(want.Mean) == 0 || !reflect.DeepEqual(c.srMean, want.Mean) || !reflect.DeepEqual(c.srMean2, want.Mean2) {
		t.Fatal("native review altered accumulated means")
	}
}

// TRLC-LINKS: REQ-SDS-140
func TestCancelledOrReplacedStackCannotRestoreReview(t *testing.T) {
	c, _, _ := newC(t)
	old := superres.New(32, 8)
	c.srStack, c.srActive, c.srStop = old, true, make(chan struct{})
	c.srCancel("cancelled")
	c.srReachReview(old, "late result")
	if c.srFocus != 0 || c.srStatus != "cancelled" {
		t.Fatal("cancelled stack restored review")
	}
	c.srStack, c.srActive, c.srFocus = superres.New(32, 8), true, 1
	c.srReachReview(old, "old stack result")
	if c.srFocus != 1 || c.srStatus != "cancelled" {
		t.Fatal("old stack overwrote replacement")
	}
}

// TRLC-LINKS: REQ-SDS-141
func TestSoftwareReviewCannotOverwriteFPGAProgress(t *testing.T) {
	c, _, _ := newC(t)
	st := superres.New(32, 8)
	c.srStack, c.srActive, c.srFPGABusy = st, true, true
	c.srStatus = "FPGA: stacking 20 records..."
	c.srReachReview(st, "stale software result")
	if c.srStatus != "FPGA: stacking 20 records..." || c.srFocus != 0 {
		t.Fatalf("FPGA progress overwritten: %s", c.srStatus)
	}
}

// TRLC-LINKS: REQ-SDS-140
func TestNativeStackRefusesPeakEnvelopeSeed(t *testing.T) {
	c, _, _ := newC(t)
	c.frameFn = func(fn func(*engine.Frame)) {
		fn(&engine.Frame{C1: []uint8{50, 200, 50, 200, 50, 200, 50, 200}, Valid: 8, PeakDetect: true})
	}
	if c.srSeedAndStart() || c.srActive || c.srStack != nil {
		t.Fatal("min/max envelope became a chronological stack reference")
	}
}
