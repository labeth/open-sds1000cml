// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"math"
	"os/exec"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/testenv"
	"github.com/labeth/open-dso-core/webserver/wire"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
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
	s := newServer(sc, nil, nil, nil)
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
	f := &sample.Frame{
		Seq:       3,
		WinPoints: 4065,
		Depth:     1 << 20,
		TriggerAt: 2048,
		SampleS:   488e-9,
		Content:   sample.PeakPairs,
		Flags:     sample.Ordered,
		Snapshot:  sample.Settings{TdivS: 200e-6, DisplayedS: 200e-6},
		Status:    sample.Status{Triggered: true},
		Traces:    []sample.Trace{{Codes8: make([]uint8, n), Valid: n}, {Ch: 1, Codes8: make([]uint8, n), Valid: n}},
	}
	for i := range n {
		f.Codes8(0)[i], f.Codes8(1)[i] = 150, 100
	}
	vf := &sample.Frame{Content: sample.PeakPairs, Traces: []sample.Trace{{Codes8: make([]uint8, vn), Valid: vn}, {Ch: 1, Codes8: make([]uint8, vn), Valid: vn}}}
	for i := range vn {
		vf.Codes8(0)[i], vf.Codes8(1)[i] = uint8(60+i), uint8(200-i)
	}
	v := sample.Detail{Role: sample.RoleView(int(engine.ViewWeb)), From: 1998, To: 2098, Frame: vf}
	f.Details = []sample.Detail{v}
	fe := &fakeAnalog{}
	s := newServer(&fakeScope{frameGen: func() *sample.Frame { return f }}, fe, nil, nil)
	rep, flags, pay := getBin(t, s, "/api/frame.bin?cols=2048&full=1")
	if flags&wire.FlagDeep == 0 || rep.ViewN != vn || math.Abs(rep.ViewA-1998.0/4095) > 1e-12 || math.Abs(rep.ViewB-2097.0/4095) > 1e-12 {
		t.Fatalf("flags %#x view_n %d a %v b %v", flags, rep.ViewN, rep.ViewA, rep.ViewB)
	}
	if len(pay) != 2*n+2*vn {
		t.Fatalf("payload %d bytes, want %d + the view's %d", len(pay), 2*n, 2*vn)
	}
	for i := range vn {
		if pay[2*n+i] != vf.Codes8(0)[i] || pay[2*n+vn+i] != vf.Codes8(1)[i] {
			t.Fatalf("view value %d: %d %d, want %d %d", i, pay[2*n+i], pay[2*n+vn+i], vf.Codes8(0)[i], vf.Codes8(1)[i])
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
	v.Role = sample.RoleView(int(engine.ViewScope))
	f.Details = []sample.Detail{v}
	f.Seq++
	if rep, _, pay = getBin(t, s, "/api/frame.bin?cols=2048&full=1"); rep.ViewN != 0 || len(pay) != 2*n {
		t.Fatalf("the scope's view served: view_n %d, %d bytes", rep.ViewN, len(pay))
	}
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "view_window.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("view window (page): %v\n%s", err, out)
	}
}
