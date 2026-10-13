// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"math"
	"os/exec"
	"testing"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// viewLeaseScope records the page's view-window lease.
type viewLeaseScope struct {
	*fakeScope
	s    engine.ViewSurface
	a, b float64
	cols int
	d    time.Duration
	n    int
}

// TRLC-LINKS: REQ-SDS-163
func (v *viewLeaseScope) LeaseViewWindow(s engine.ViewSurface, a, b float64, cols int, d time.Duration) {
	v.s, v.a, v.b, v.cols, v.d = s, a, b, cols, d
	v.n++
}

// The page's poll leases its zoom's view window (view=a,b,cols) as the web's,
// renews it with each poll, and ends it with view=off; a poll without view=
// leaves it alone (the analyzers' feeds).
// TRLC-LINKS: REQ-SDS-163, REQ-SDS-202
func TestFramePollLeasesTheViewWindow(t *testing.T) {
	sc := &viewLeaseScope{fakeScope: &fakeScope{}}
	s := New(sc, nil, nil, nil)
	getBin(t, s, "/api/frame.bin?cols=2048&full=1&view=-0.02,0.03,1200")
	if sc.s != engine.ViewWeb || sc.a != -0.02 || sc.b != 0.03 || sc.cols != 1200 || sc.d <= 0 {
		t.Fatalf("lease %+v", *sc)
	}
	getBin(t, s, "/api/frame.bin?cols=2048&full=1&view=off")
	if sc.d != 0 {
		t.Fatalf("view=off kept the lease for %v", sc.d)
	}
	n := sc.n
	getBin(t, s, "/api/frame.bin?raw=1&lease=raw")
	if sc.n != n {
		t.Fatal("a poll without view= touched the lease")
	}
}

// A live envelope frame with the web's view window carries it: where its
// first and last values lie among the served values (as the page draws them,
// i/(n-1)), coupled as the trace is, and on the binary transport after the
// payload.
// TRLC-LINKS: REQ-SDS-163, REQ-SDS-202
func TestBinFrameCarriesTheViewWindow(t *testing.T) {
	const n, vn = 4096, 100
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Seq: 3, Valid: n, WinCols: 4065, CaptureDepth: 1 << 20,
		EdgeX: 2048, PeakDetect: true, Ordered: true, Trigd: true, TdivS: 200e-6, DisplayedS: 200e-6, SampleS: 488e-9}
	for i := range n {
		f.C1[i], f.C2[i] = 150, 100
	}
	v := &engine.ViewWindow{C1: make([]uint8, vn), C2: make([]uint8, vn), Valid: vn, From: 1998, To: 2098, PeakDetect: true}
	for i := range vn {
		v.C1[i], v.C2[i] = uint8(60+i), uint8(200-i)
	}
	f.Views[engine.ViewWeb] = v
	fe := &fakeAnalog{}
	s := New(&fakeScope{frameGen: func() *engine.Frame { return f }}, fe, nil, nil)
	rep, flags, pay := getBin(t, s, "/api/frame.bin?cols=2048&full=1")
	if flags&binDeep == 0 || rep.ViewN != vn || math.Abs(rep.ViewA-1998.0/4095) > 1e-12 || math.Abs(rep.ViewB-2097.0/4095) > 1e-12 {
		t.Fatalf("flags %#x view_n %d a %v b %v", flags, rep.ViewN, rep.ViewA, rep.ViewB)
	}
	if len(pay) != 2*n+2*vn {
		t.Fatalf("payload %d bytes, want %d + the view's %d", len(pay), 2*n, 2*vn)
	}
	for i := range vn {
		if pay[2*n+i] != v.C1[i] || pay[2*n+vn+i] != v.C2[i] {
			t.Fatalf("view value %d: %d %d, want %d %d", i, pay[2*n+i], pay[2*n+vn+i], v.C1[i], v.C2[i])
		}
	}
	// AC on C1: the detail moves by the trace's DC (150 -> 128), C2 stays.
	fe.cpl[0] = analog.CplAC
	f.Seq++
	rep, _, pay = getBin(t, s, "/api/frame.bin?cols=2048&full=1")
	if pay[2*n] != 60-22 || pay[2*n+vn] != 200 || rep.VC1 != nil {
		t.Fatalf("AC: first values %d %d, want 38 200 (and no arrays in the header)", pay[2*n], pay[2*n+vn])
	}
	// The page's view is the web's: the scope's zoom view is not served.
	f.Views[engine.ViewWeb], f.Views[engine.ViewScope] = nil, v
	f.Seq++
	if rep, _, pay = getBin(t, s, "/api/frame.bin?cols=2048&full=1"); rep.ViewN != 0 || len(pay) != 2*n {
		t.Fatalf("the scope's view served: view_n %d, %d bytes", rep.ViewN, len(pay))
	}
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "view_window.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("view window (page): %v\n%s", err, out)
	}
}
