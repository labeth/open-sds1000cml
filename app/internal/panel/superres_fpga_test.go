// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"context"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/superres"
)

type fpgaFakeEng struct {
	*fakeEng
	requests chan engine.FPGAStackRequest
}

// TRLC-LINKS: REQ-SDS-141
func (f *fpgaFakeEng) FPGAStack(_ context.Context, req engine.FPGAStackRequest) (engine.FPGAStackResult, error) {
	f.requests <- req
	moments := make([]superres.FPGAStackMoments, 2*req.Stack.Bins)
	for i := range moments {
		v := uint64(100+i/2) << 24
		moments[i] = superres.FPGAStackMoments{Sum: [2]uint64{8 * v, 0}, Count: 8, SumOdd: [2]uint64{4 * v, 0}, CountOdd: 4,
			SumSquares: [2]uint64{0, 8 * (v >> 16) * (v >> 16) >> 32}}
	}
	st, err := superres.FPGAStack(moments, req.Stack.Factor, int(req.Stack.Channel), 8, req.Records, 2e-9)
	if err != nil {
		return engine.FPGAStackResult{}, err
	}
	return engine.FPGAStackResult{Result: st.Result(false, 1), Stack: st, Hits: 8, Records: req.Records, RawWords: 131072, BusReads: 4096}, nil
}

// TRLC-LINKS: REQ-SDS-141
func TestSuperresFPGARun(t *testing.T) {
	_, base, fe := newC(t)
	eng := &fpgaFakeEng{fakeEng: base, requests: make(chan engine.FPGAStackRequest, 1)}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	n := 256
	sig := make([]uint8, n)
	for i := range sig {
		sig[i] = uint8(40 + (i*7)%160) // swing 40..199
	}
	// Signal on C2 only: the software reference on C1 is unusable, but the
	// page must still open so C2 and FPGA can be chosen.
	flat := make([]uint8, n)
	for i := range flat {
		flat[i] = 128
	}
	fr := &engine.Frame{C1: flat, C2: sig, Valid: n, EdgeX: 32, SampleS: 2e-9}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(fr) })
	c.button(btnUtility)
	if v := c.MenuView(); v.Title != "SUPER-RES" || c.SuperresView().Active {
		t.Fatalf("flat-channel UTILITY: title %q active %v", v.Title, c.SuperresView().Active)
	}
	c.menuButton(btnF1)      // Channel C1 -> C2
	for i := 0; i < 3; i++ { // bits → stacks → time → FPGA
		c.menuButton(btnF3)
	}
	v := c.MenuView()
	if v.Items[2].Value != "FPGA" || v.Items[3].Value != "20 rec" || v.Items[4].Label != "Run" {
		t.Fatalf("FPGA menu: %+v", v.Items)
	}
	c.menuButton(btnF5)
	req := <-eng.requests
	s := req.Stack
	if req.Records != 20 || req.RecordWords != fpgaRecordWords || s.Factor != 32 || s.Bins != fpgaWindow*32 ||
		s.PreSamples != fpgaWindow/2 || s.Level != 119 || s.Hysteresis != 19 || s.Channel != 1 || s.ChannelMask != 3 {
		t.Fatalf("request %+v", req)
	}
	deadline := time.Now().Add(2 * time.Second)
	for c.SuperresView().Focus != 3 {
		if time.Now().After(deadline) {
			t.Fatalf("FPGA result never reached review: %q", c.SuperresView().Status)
		}
		time.Sleep(time.Millisecond)
	}
	sv := c.SuperresView()
	if len(sv.Mean) != fpgaWindow*32 || sv.WinLo != 0 || sv.WinHi != fpgaWindow || sv.Period != 0 || sv.K != 32 ||
		!strings.HasPrefix(sv.Status, "FPGA 20 rec 8 hits") || !strings.HasSuffix(sv.Status, "bus/64") {
		t.Fatalf("review %+v", sv)
	}
	if sv.Mean[0] < 99 || sv.Mean[0] > 101 {
		t.Fatalf("mean %v", sv.Mean[:4])
	}
}

// TRLC-LINKS: REQ-SDS-141
func TestSuperresFPGAMatchRun(t *testing.T) {
	_, base, fe := newC(t)
	eng := &fpgaFakeEng{fakeEng: base, requests: make(chan engine.FPGAStackRequest, 1)}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	n := 256
	sig := make([]uint8, n)
	for i := range sig {
		sig[i] = uint8(40 + (i*7)%160)
	}
	fr := &engine.Frame{C1: sig, C2: sig, Valid: n, EdgeX: 60, SampleS: 2e-9}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(fr) })
	c.button(btnUtility)
	for i := 0; i < 4; i++ { // bits → stacks → time → FPGA → FPGA+match
		c.menuButton(btnF3)
	}
	if v := c.MenuView(); v.Items[2].Value != "FPGA+match" || v.Items[4].Label != "Run" {
		t.Fatalf("match menu: %+v", v.Items)
	}
	c.menuButton(btnF5)
	req := <-eng.requests
	s := req.Stack
	if len(s.Template) != fpgaMatchPoints || s.TemplateStride != 1 || s.TemplatePre != fpgaMatchPoints/4 || s.TemplateThreshold <= 0 {
		t.Fatalf("template request: len %d stride %d pre %d threshold %d", len(s.Template), s.TemplateStride, s.TemplatePre, s.TemplateThreshold)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(c.SuperresView().Status, " rej ") {
		if time.Now().After(deadline) {
			t.Fatalf("status %q", c.SuperresView().Status)
		}
		time.Sleep(time.Millisecond)
	}
}

// TRLC-LINKS: REQ-SDS-140
func TestSuperresArmDeepWindowStaysResponsive(t *testing.T) {
	c, _, _ := newC(t)
	n := 200000
	sig := make([]uint8, n)
	for i := range sig {
		sig[i] = uint8(60 + 80*((i/37)%2)) // square wave, period 74
	}
	fr := &engine.Frame{C1: sig, C2: sig, Valid: n, EdgeX: float64(n / 2), SampleS: 3.2e-8, WinCols: 156314}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(fr) })
	start := time.Now()
	c.button(btnUtility)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("arming a deep window blocked the panel for %v", d)
	}
	if c.MenuView().Title != "SUPER-RES" || c.SuperresView().Active || !strings.Contains(c.SuperresView().Status, "too deep") {
		t.Fatalf("deep window: title %q status %q", c.MenuView().Title, c.SuperresView().Status)
	}
}
