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
	press(t, c, "acquire")
	goRow(t, c, "acq.superres")
	pickKey(t, c, "On")
	if c.SuperresView().Active {
		t.Fatal("super-res armed on the flat C1")
	}
	goRow(t, c, "acq.sr_channel")
	pickKey(t, c, "C2")
	goRow(t, c, "acq.sr_stop")
	pickKey(t, c, "FPGA rec")
	if rowText(c, "acq.sr_target") != "20" {
		t.Fatalf("FPGA target %q", rowText(c, "acq.sr_target"))
	}
	goRow(t, c, "acq.sr_restart")
	pickKey(t, c, "Run FPGA")
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
	want := c.srStack.Result(false, 1)
	for i, v := range want.Mean {
		if sv.Mean[i] != v {
			t.Fatalf("FPGA mean altered at %d: %g != %g", i, sv.Mean[i], v)
		}
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
	press(t, c, "acquire")
	goRow(t, c, "acq.sr_stop")
	pickKey(t, c, "FPGA+match")
	goRow(t, c, "acq.sr_restart")
	pickKey(t, c, "Run FPGA")
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
	r := c.SheetSet("acq.superres", nil, "On")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("arming a deep window blocked the panel for %v", d)
	}
	if r.OK || !strings.Contains(r.Error, "too deep") || c.SuperresView().Active || !strings.Contains(c.SuperresView().Status, "too deep") {
		t.Fatalf("deep window: %+v status %q", r, c.SuperresView().Status)
	}
}

type cancellableFPGAEng struct {
	*fakeEng
	started chan context.Context
}

// TRLC-LINKS: REQ-SDS-141
func (f *cancellableFPGAEng) FPGAStack(ctx context.Context, _ engine.FPGAStackRequest) (engine.FPGAStackResult, error) {
	f.started <- ctx
	<-ctx.Done()
	return engine.FPGAStackResult{}, ctx.Err()
}

// TRLC-LINKS: REQ-SDS-141
func TestSuperresFPGACancel(t *testing.T) {
	_, base, fe := newC(t)
	eng := &cancellableFPGAEng{base, make(chan context.Context, 1)}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		fn(&engine.Frame{C1: []uint8{40, 200, 40, 200, 40, 200, 40, 200}, Valid: 8, EdgeX: 4, SampleS: 2e-9})
	})
	c.srFPGARun()
	ctx := <-eng.started
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("progressing stacks must not have a fixed session deadline")
	}
	c.srCancel("cancelled")
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("native cancel did not reach FPGA job")
	}
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		busy := c.srFPGABusy
		c.mu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled job remained busy")
		}
		time.Sleep(time.Millisecond)
	}
	if v := c.SuperresView(); v.Active || v.Focus != 0 || v.Status != "cancelled" {
		t.Fatalf("cancelled completion restored stale review: %+v", v)
	}
}

// With Stop on an FPGA mode, Super-res On runs an FPGA session: it refused
// with the software stack's depth limit ("window too deep for software
// stacking - use FPGA"), and only Restart's Run FPGA started it (bug hunt #3).
// TRLC-LINKS: REQ-SDS-141
func TestSuperresOnInFPGAModeRunsTheFPGA(t *testing.T) {
	_, base, fe := newC(t)
	eng := &fpgaFakeEng{fakeEng: base, requests: make(chan engine.FPGAStackRequest, 1)}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	n := 200000 // a deep window, past the software stack's limit
	sig := make([]uint8, n)
	for i := range sig {
		sig[i] = uint8(40 + (i*7)%160)
	}
	fr := &engine.Frame{C1: sig, C2: sig, Valid: n, EdgeX: 32, SampleS: 2e-9}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(fr) })
	if res := c.SheetSet("acq.sr_stop", nil, "FPGA rec"); !res.OK {
		t.Fatalf("stop on: %+v", res)
	}
	if res := c.SheetSet("acq.superres", nil, "On"); !res.OK {
		t.Fatalf("Super-res On in FPGA mode: %+v", res)
	}
	select {
	case req := <-eng.requests:
		if req.Records != 20 {
			t.Fatalf("request %+v", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no FPGA session")
	}
}

// Super-res On on a window too deep for the software stack says why and what
// to do within the ACQ tab's status line: the remedy was cut off the LCD
// ("...(156378 samples) - Stop o..", bug hunt #3).
// TRLC-LINKS: REQ-SDS-141
func TestSuperresTooDeepSaysTheRemedyOnTheLCD(t *testing.T) {
	c, _, _ := newC(t)
	n := 156378
	sig := make([]uint8, n)
	for i := range sig {
		sig[i] = uint8(40 + (i*7)%160)
	}
	fr := &engine.Frame{C1: sig, C2: sig, Valid: n, EdgeX: 32, SampleS: 32e-9}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(fr) })
	if res := c.SheetSet("acq.superres", nil, "On"); res.OK {
		t.Fatal("a 156k-sample window stacked in software")
	}
	press(t, c, "acquire")
	for _, sp := range c.View().Sheet.Status {
		if strings.HasPrefix(sp.Text, "Super-res:") {
			if len(sp.Text) > 77 || !strings.Contains(sp.Text, "Stop on: FPGA rec, or a faster S/DIV") {
				t.Fatalf("the ACQ status line (%d characters): %q", len(sp.Text), sp.Text)
			}
			return
		}
	}
	t.Fatal("no super-res status line")
}
