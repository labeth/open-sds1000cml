// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// BUS trades the frame rate for the decode: a live envelope too coarse for
// the bus leases sampled frames, renewed by each frame (a sampled one too)
// while the bus's settings stay; they changing, or BUS left, end the lease.
// TRLC-LINKS: REQ-SDS-214
func TestBusLeasesSampledFramesToDecode(t *testing.T) {
	eng := &namedLeaseEng{fakeEng: newEng()}
	c := New(eng, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	c.mu.Lock()
	c.decProto = framecalc.ProtoUART
	c.mu.Unlock()
	c.switchWS(wsview.Bus)
	eng.take()
	s := c.snap()
	s.st.Running = true
	env := &engine.Frame{PeakDetect: true, Ordered: true}
	raw := &engine.Frame{}
	a := &anaState{}
	a.res = decode.Result{Proto: "uart", Error: "UART" + " not resolved at this timebase - S/DIV faster, or SINGLE"}
	if !framecalc.Unresolved(a.res) {
		t.Fatal("the envelope's refusal not recognised")
	}
	c.busRawFrames(a, s, env)
	if got := eng.take(); len(got) != 1 || got[0] != rawForBus+" 10s" {
		t.Fatalf("coarse envelope: leases %v", got)
	}
	if !strings.Contains(a.res.Error, "sampled records") {
		t.Fatalf("notice %q", a.res.Error)
	}
	a.res = decode.Result{Proto: "uart", OK: true}
	c.busRawFrames(a, s, raw)
	if got := eng.take(); len(got) != 1 || got[0] != rawForBus+" 10s" {
		t.Fatalf("sampled frame decoded: leases %v, want renewed", got)
	}
	s.st.TdivS *= 2 // the timebase changed: its envelope is tried first
	c.busRawFrames(a, s, raw)
	if got := eng.take(); len(got) != 1 || got[0] != rawForBus+" 0s" {
		t.Fatalf("settings changed: leases %v, want ended", got)
	}
	s.st.TdivS /= 2
	a.res = decode.Result{Proto: "uart", Error: "UART" + " not resolved at this timebase - S/DIV faster, or SINGLE"}
	c.busRawFrames(a, s, env)
	eng.take()
	c.switchWS(wsview.Measure)
	c.View()
	if got := eng.take(); len(got) != 1 || got[0] != rawForBus+" 0s" {
		t.Fatalf("BUS left: leases %v, want ended", got)
	}
}
