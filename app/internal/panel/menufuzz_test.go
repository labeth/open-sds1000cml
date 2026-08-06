package panel

// Seeded menu-state-machine fuzz: random button presses (menu, F1..F5, every
// per-page cycle key) and knob turns across every page, with the structural
// invariants checked after EVERY step:
//
//   - never panics (a panic kills the whole app on the device)
//   - the current page is always a real page
//   - the ADJUST highlight never sits on a slot the page doesn't populate
//     (menuSel < pageSlots(page))
//   - decode channel-role complementarity (TestDecodeMenu documents it):
//     I2C SCL/SDA and SPI CLK/DATA never share a channel
//   - every shadow the controller owns stays within its legal range, and
//     every command it sends the engine carries a legal argument
//
// Unlike TestPanelChaos (whole-panel storm, end-state checks only) this test
// drives a STATEFUL engine fake — Set* calls are reflected back through
// Snapshot() like the real engine, so the st.X+dir cycle paths (trigger type,
// acq mode, qualifier pages...) are actually exercised — and checks after
// every step, printing the exact reproducing action sequence on failure.
// AUTO/autoset is deliberately excluded: it blacks out all input while its
// sweep runs (TestPanelChaos covers it).

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// fuzzEng is a stateful, mutex-guarded engine fake: every setter validates its
// argument (recording violations) and updates the Stats snapshot the panel
// resyncs from, mirroring the real engine's authoritative-state contract.
type fuzzEng struct {
	mu      sync.Mutex
	stats   engine.Stats
	illegal []string
}

func newFuzzEng() *fuzzEng {
	return &fuzzEng{stats: engine.Stats{
		Running: true, TrigCode: 31434, TdivS: 500e-6, TrigPosFrac: 0.5,
		AvgCount: 4, EresLen: 1, MemDepth: 6144,
	}}
}

func (f *fuzzEng) bad(format string, a ...any) {
	f.illegal = append(f.illegal, fmt.Sprintf(format, a...))
}

func (f *fuzzEng) takeIllegal() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.illegal
	f.illegal = nil
	return out
}

func (f *fuzzEng) ReadMatrix() ([5]uint16, bool) { return idle(), true }
func (f *fuzzEng) CombineDrain(engine.CombineReq) (engine.CombineOut, bool) {
	return engine.CombineOut{}, false // ok=false → srDeviceTick skips (no HW in the fuzz)
}
func (f *fuzzEng) SetLEDs(uint16) {}

func (f *fuzzEng) AcqLog(n int) ([]engine.AcqSample, float64) { return nil, 0 }
func (f *fuzzEng) Snapshot() engine.Stats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stats
}

func (f *fuzzEng) SetOffsetDAC(ch int, code uint16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch != 0 && ch != 1 {
		f.bad("SetOffsetDAC ch=%d", ch)
	}
}

func (f *fuzzEng) SetTrigLevelCode(code uint16) uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if code < engine.TrigCodeMin || code > engine.TrigCodeMax {
		f.bad("SetTrigLevelCode %d outside [%d,%d]", code, engine.TrigCodeMin, engine.TrigCodeMax)
	}
	f.stats.TrigCode = code
	return code
}

func (f *fuzzEng) SetTdiv(t float64) (engine.Band, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := engine.PlanTdiv(t)
	if !ok {
		f.bad("SetTdiv %g not a ladder detent", t)
		return engine.Band{}, false
	}
	f.stats.TdivS = b.TdivS
	f.stats.DisplayedS = b.DisplayedSdivS()
	return b, true
}

func (f *fuzzEng) SetNorm(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats.Norm = on
}

func (f *fuzzEng) SetRunning(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats.Running = on
	if !on {
		f.stats.Single = false
	}
}

func (f *fuzzEng) SetSingle() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats.Single, f.stats.Norm, f.stats.Running = true, true, true
}

func (f *fuzzEng) SetTrigSlope(r bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats.TrigRising = r
}

func (f *fuzzEng) SetTrigSource(ch int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch != 0 && ch != 1 {
		f.bad("SetTrigSource ch=%d", ch)
		return
	}
	f.stats.TrigSource = ch
}

func (f *fuzzEng) SetTrigType(t int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t < 0 || t > 3 {
		f.bad("SetTrigType %d", t)
		return
	}
	f.stats.TrigType = t
}

func (f *fuzzEng) SetAcqMode(m int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m < 0 || m > 3 {
		f.bad("SetAcqMode %d", m)
		return
	}
	f.stats.AcqMode = m
}

func (f *fuzzEng) SetAvgCount(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !inInts(n, 4, 16, 32, 64, 128, 256) {
		f.bad("SetAvgCount %d", n)
		return
	}
	f.stats.AvgCount = n
}

func (f *fuzzEng) SetEresLen(l int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !inInts(l, 1, 3, 7, 15, 31, 63) {
		f.bad("SetEresLen %d", l)
		return
	}
	f.stats.EresLen = l
}

func (f *fuzzEng) SetETS(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats.ETS = on
}

func (f *fuzzEng) SetSiggen(on, ramp bool) {} // proving-only hook; no fuzz invariant

func (f *fuzzEng) SetTrigPosFrac(fr float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if fr < 0.02-1e-9 || fr > 1+1e-9 {
		f.bad("SetTrigPosFrac %g outside [0.02,1]", fr)
		return
	}
	f.stats.TrigPosFrac = fr
}

func (f *fuzzEng) SetHoldoff(s float64) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s < 0 {
		f.bad("SetHoldoff %g < 0", s)
		return f.stats.HoldoffS
	}
	f.stats.HoldoffS = s
	return s
}

func (f *fuzzEng) SetMemDepth(n int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !inInts(n, 2048, 6144, 14336, 20480) {
		f.bad("SetMemDepth %d", n)
		return f.stats.MemDepth
	}
	f.stats.MemDepth = n
	return n
}

func (f *fuzzEng) SetPulseParams(lvl, mn, mx float64, cond int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if lvl < 0 || lvl > 1 || mn < 0 || mx < 0 || cond < 0 || cond > 3 {
		f.bad("SetPulseParams lvl=%g w=[%g,%g] cond=%d", lvl, mn, mx, cond)
	}
}

func (f *fuzzEng) SetSlopeParams(lo, hi, mn, mx float64, cond int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if lo < 0 || lo > 1 || hi < 0 || hi > 1 || mn < 0 || mx < 0 || cond < 0 || cond > 3 {
		f.bad("SetSlopeParams lo=%g hi=%g t=[%g,%g] cond=%d", lo, hi, mn, mx, cond)
	}
}

func (f *fuzzEng) SetVideoParams(std, line int, neg bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if std != 0 && std != 1 || line < 0 || line > 625 {
		f.bad("SetVideoParams std=%d line=%d", std, line)
	}
}

func (f *fuzzEng) SetMask(m *engine.Mask) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m == nil || m.WinCols <= 0 || len(m.Lo) < m.WinCols || len(m.Hi) < m.WinCols {
		f.bad("SetMask malformed")
		return
	}
	f.stats.MaskSet = true
}

func (f *fuzzEng) SetMaskMode(m int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m < 0 || m > 2 {
		f.bad("SetMaskMode %d", m)
		return
	}
	f.stats.MaskMode = m
}

func (f *fuzzEng) ClearMaskFails() {}

func inInts(v int, opts ...int) bool {
	for _, o := range opts {
		if v == o {
			return true
		}
	}
	return false
}

func inIntRange(v, lo, hi int) bool { return v >= lo && v <= hi }

// fuzzAction is one scripted step: a button press or a knob turn.
type fuzzAction struct {
	name string // for the failure log
	run  func(c *Controller)
}

func fuzzAlphabet() []fuzzAction {
	var acts []fuzzAction
	buttons := []struct {
		name string
		code int
	}{
		{"menu", btnMenuOnOff},
		{"f1", btnF1}, {"f2", btnF2}, {"f3", btnF3}, {"f4", btnF4}, {"f5", btnF5},
		{"trigmenu", btnTrigMenu}, {"acquire", btnAcquire}, {"display", btnDisplay},
		{"horizmenu", btnHorizMenu}, {"cursors", btnCursors}, {"ref", btnRef},
		{"math", btnMath}, {"measure", btnMeasure}, {"ch1", btnCh1}, {"ch2", btnCh2},
		{"utility", btnUtility}, {"adjustpush", btnAdjustPsh},
		{"runstop", btnRunStop}, {"single", btnSingle},
		{"ch1vdivpush", btnCh1VdivPush}, {"ch2vdivpush", btnCh2VdivPush},
		{"triglvlpush", btnTrigLvlPush},
	}
	for _, b := range buttons {
		code := b.code
		acts = append(acts, fuzzAction{"press:" + b.name, func(c *Controller) { c.button(code) }})
	}
	for name := range knobNames {
		for _, dir := range []int{+1, -1} {
			n, d := name, dir
			acts = append(acts, fuzzAction{fmt.Sprintf("knob:%s%+d", n, d), func(c *Controller) {
				c.resync()
				c.dispatch(n, d, 1) // same path InjectKnob runs
			}})
		}
	}
	// Composite navigation: pgDecode hides behind MENU ▸ F5, which a uniform
	// random walk reaches too rarely for its per-proto slot logic to get dense
	// coverage. Still driven through the real button entry points.
	acts = append(acts, fuzzAction{"nav:decode", func(c *Controller) {
		for i := 0; i < 2; i++ {
			c.mu.Lock()
			pg := c.menuPage
			c.mu.Unlock()
			if pg == pgMain {
				break
			}
			c.button(btnMenuOnOff)
		}
		c.button(btnF5)
	}})
	return acts
}

// checkMenuInvariants inspects the controller under its own lock and returns
// every violated invariant. pageSlots reads decProto, so it MUST run under mu.
func checkMenuInvariants(c *Controller) []string {
	var bad []string
	fail := func(format string, a ...any) { bad = append(bad, fmt.Sprintf(format, a...)) }

	c.mu.Lock()
	if c.menuPage < pgNone || c.menuPage > pgMask {
		fail("menuPage %d outside [pgNone,pgMask]", c.menuPage)
	} else if c.menuPage != pgNone {
		if slots := c.pageSlots(c.menuPage); c.menuSel < 0 || c.menuSel >= slots {
			fail("highlight slot %d outside page %d's %d populated slots", c.menuSel, c.menuPage, slots)
		}
	}
	if !inIntRange(c.decProto, 0, 4) {
		fail("decProto %d", c.decProto)
	}
	if !inIntRange(c.decFormat, 0, 2) {
		fail("decFormat %d", c.decFormat)
	}
	if !inInts(c.decBaud, 9600, 19200, 38400, 57600, 115200, 230400) {
		fail("decBaud %d", c.decBaud)
	}
	if !inIntRange(c.decChA, 0, 1) || !inIntRange(c.decChB, 0, 1) {
		fail("decode channels A=%d B=%d", c.decChA, c.decChB)
	}
	// The documented complementarity invariant (TestDecodeMenu): on I2C and
	// SPI, clock and data must never share a channel.
	if (c.decProto == 3 || c.decProto == 4) && c.decChA == c.decChB {
		fail("decode proto %d clock/data share channel C%d", c.decProto, c.decChA+1)
	}
	if c.tdivIdx < 0 || c.tdivIdx >= len(c.tdivs) {
		fail("tdivIdx %d", c.tdivIdx)
	}
	for ch := 0; ch < 2; ch++ {
		if c.vIdx[ch] < 0 || c.vIdx[ch] >= len(analog.Detents) {
			fail("vIdx[%d]=%d", ch, c.vIdx[ch])
		}
	}
	if !inInts(c.zoom, 1, 2, 5, 10, 20, 50) {
		fail("zoom %d", c.zoom)
	}
	if c.zoomOff < -0.5 || c.zoomOff > 0.5 {
		fail("zoomOff %g", c.zoomOff)
	}
	if !inIntRange(c.curType, 0, 1) || !inIntRange(c.curSel, 0, 1) {
		fail("cursor type/sel %d/%d", c.curType, c.curSel)
	}
	for i := 0; i < 2; i++ {
		if c.curX[i] < 0 || c.curX[i] > 1 || c.curY[i] < 0 || c.curY[i] > 1 {
			fail("cursor %d off-screen x=%g y=%g", i, c.curX[i], c.curY[i])
		}
	}
	if !inIntRange(c.viewMode, 0, 4) {
		fail("viewMode %d", c.viewMode)
	}
	if !inIntRange(c.mathMode, 0, 4) {
		fail("mathMode %d", c.mathMode)
	}
	if c.pulseLvl < 0.05 || c.pulseLvl > 0.95 {
		fail("pulseLvl %g", c.pulseLvl)
	}
	if c.slopeLo < 0.05 || c.slopeLo > 0.95 || c.slopeHi < 0.05 || c.slopeHi > 0.95 {
		fail("slope lo/hi %g/%g", c.slopeLo, c.slopeHi)
	}
	if !inIntRange(c.pulseCond, 0, 3) || !inIntRange(c.slopeCond, 0, 3) {
		fail("pulse/slope cond %d/%d", c.pulseCond, c.slopeCond)
	}
	if !inIntRange(c.videoStd, 0, 1) || c.videoLine < 0 || c.videoLine > 625 {
		fail("video std=%d line=%d", c.videoStd, c.videoLine)
	}
	if !inInts(c.srK, 8, 16, 32, 64) {
		fail("srK %d", c.srK)
	}
	if !inIntRange(c.srStopMode, 0, 2) || !inIntRange(c.srCh, 0, 1) || !inIntRange(c.srFocus, 0, 3) {
		fail("superres mode/ch/focus %d/%d/%d", c.srStopMode, c.srCh, c.srFocus)
	}
	if !inInts(c.maskN, 16, 32, 64, 128) {
		fail("maskN %d", c.maskN)
	}
	if c.maskTol < 0 || c.maskTol >= len(maskTols) {
		fail("maskTol %d", c.maskTol)
	}
	if c.trigCode < engine.TrigCodeMin || c.trigCode > engine.TrigCodeMax {
		fail("trigCode %d", c.trigCode)
	}
	page := c.menuPage
	c.mu.Unlock()

	// The render snapshot must agree (and must not panic).
	v := c.MenuView()
	if v.Open != (page != pgNone) {
		fail("MenuView.Open=%v but page=%d", v.Open, page)
	}
	if len(v.Items) > 5 || v.Sel < 0 || v.Sel > 4 {
		fail("MenuView items=%d sel=%d", len(v.Items), v.Sel)
	}
	if (v.DecProto == 3 || v.DecProto == 4) && v.DecChA == v.DecChB {
		fail("MenuView decode proto %d channels share C%d", v.DecProto, v.DecChA+1)
	}
	return bad
}

func TestMenuFuzz(t *testing.T) {
	alphabet := fuzzAlphabet()
	const steps = 2000
	for _, seed := range []int64{1, 1337, 20260710} {
		seed := seed
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			eng := newFuzzEng()
			fe := &fakeFE{idx: [2]int{8, 8}}
			c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
			c.decode(idle(), true) // seed the decoder baseline

			// A constant live frame so UTILITY (super-res), REF save and the
			// mask build have something to snapshot — same pattern as
			// TestSuperresUX; a constant Seq keeps the background loops idle.
			n := 256
			sig := make([]uint8, n)
			for i := range sig {
				sig[i] = uint8(40 + (i*7)%160)
			}
			fr := &engine.Frame{C1: sig, C2: sig, Valid: n, EdgeX: 32, SampleS: 1e-9}
			c.SetFrameSource(func(fn func(*engine.Frame)) { fn(fr) })

			rng := rand.New(rand.NewSource(seed))
			actions := make([]string, 0, steps)
			visited := map[int]bool{}
			dump := func() string {
				return fmt.Sprintf("reproducing sequence (%d steps):\n%s",
					len(actions), strings.Join(actions, " "))
			}
			for i := 0; i < steps; i++ {
				a := alphabet[rng.Intn(len(alphabet))]
				actions = append(actions, a.name)
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Logf("%s", dump())
							t.Fatalf("seed %d step %d (%s) panicked: %v", seed, i, a.name, r)
						}
					}()
					a.run(c)
				}()
				if bad := checkMenuInvariants(c); len(bad) > 0 {
					t.Logf("%s", dump())
					t.Fatalf("seed %d step %d (%s) violated:\n  %s",
						seed, i, a.name, strings.Join(bad, "\n  "))
				}
				if ill := eng.takeIllegal(); len(ill) > 0 {
					t.Logf("%s", dump())
					t.Fatalf("seed %d step %d (%s) sent illegal engine commands:\n  %s",
						seed, i, a.name, strings.Join(ill, "\n  "))
				}
				c.mu.Lock()
				visited[c.menuPage] = true
				c.mu.Unlock()
			}

			// The random walk must actually have crossed every page, or the
			// invariants above were vacuous for the pages it missed.
			for pg := pgTrig; pg <= pgMask; pg++ {
				if !visited[pg] {
					t.Errorf("seed %d never visited page %d — widen the alphabet or steps", seed, pg)
				}
			}
		})
	}
}

// TestDecodeChannelComplementarityAcrossProtoSwitch is the deterministic
// regression for a violation the fuzzer's invariant hunts statistically: UART
// only owns the Source role, so toggling it used to leave the (invisible)
// data-role shadow on the same channel — and the next switch to I2C/SPI
// surfaced SCL/SDA (CLK/DATA) sharing one channel, which TestDecodeMenu
// documents as never allowed.
func TestDecodeChannelComplementarityAcrossProtoSwitch(t *testing.T) {
	c, _, _ := newC(t)
	c.menuButton(btnMenuOnOff) // MAIN
	c.menuButton(btnF5)        // ▸ Decode
	c.menuButton(btnF1)        // Off → Auto
	c.menuButton(btnF1)        // Auto → UART
	c.menuButton(btnF3)        // UART Source C1 → C2
	c.menuButton(btnF1)        // UART → I2C
	if v := c.MenuView(); v.DecProto != 3 || v.DecChA == v.DecChB {
		t.Fatalf("I2C entered with SCL/SDA sharing a channel: proto=%d A=%d B=%d",
			v.DecProto, v.DecChA, v.DecChB)
	}
	c.menuButton(btnF1) // I2C → SPI must stay complementary too
	if v := c.MenuView(); v.DecProto != 4 || v.DecChA == v.DecChB {
		t.Fatalf("SPI entered with CLK/DATA sharing a channel: proto=%d A=%d B=%d",
			v.DecProto, v.DecChA, v.DecChB)
	}
}
