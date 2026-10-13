// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

// Seeded fuzz of the workspace and sheet state machine: random button
// presses (every workspace key, F-key, printed key and push) and knob turns,
// with captures analysed between them, and the structural invariants checked
// after EVERY step:
//
//   - never panics (a panic kills the whole app on the device)
//   - the workspace, tab, row and sub-mode are real ones, and the modes
//     agree (a sub-mode only in a sheet, the BUS picker only in BUS, an edit
//     only on an enum or number row)
//   - the view agrees with the mode: the rail with no menu open, the F-key
//     column and five labels at most with one, one pane per workspace
//   - decode channel-role complementarity: I2C SCL/SDA and SPI CLK/DATA
//     never share a channel
//   - every shadow the controller owns stays within its legal range, and
//     every command it sends the engine carries a legal argument
//
// Every 25th step the screen is also rendered through the LCD renderer. The
// random walk must reach every workspace, every sheet tab and every
// sub-mode, or the invariants were vacuous for what it missed. On failure
// the exact action sequence is printed. AUTO is left out (its sweep blacks
// out input; TestPanelChaos covers it).

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/lcd"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// fuzzAction is one scripted step: a button press, a knob turn or a frame.
// TRLC-LINKS: REQ-SDS-136
type fuzzAction struct {
	name string // for the failure log
	run  func(r *viewRig)
}

// TRLC-LINKS: REQ-SDS-136
func fuzzAlphabet() []fuzzAction {
	var acts []fuzzAction
	for _, n := range []string{"menu", "f1", "f2", "f3", "f4", "f5", "trigmenu", "acquire", "display", "horizmenu",
		"cursors", "ref", "math", "measure", "ch1", "ch2", "utility", "saverecall", "help", "adjustpush",
		"runstop", "single", "ch1vdivpush", "ch2vdivpush", "triglvlpush", "ch1pospush", "ch2pospush",
		"tdivpush", "horizpospush", "set50", "force", "print"} {
		code, _ := nameCode(n)
		acts = append(acts, fuzzAction{"press:" + n, func(r *viewRig) { r.c.button(code) }})
	}
	for name := range knobNames {
		for _, dir := range []int{+1, -1} {
			n, d := name, dir
			acts = append(acts, fuzzAction{fmt.Sprintf("knob:%s%+d", n, d), func(r *viewRig) {
				r.c.resync()
				r.c.dispatch(n, d, 1) // same path InjectKnob runs
			}})
		}
	}
	// Knob turns dominate real use: weight the two tool knobs and S/DIV.
	for _, n := range []string{"adjust", "horizpos", "tdiv"} {
		for _, d := range []int{1, -1, 1, -1} {
			n, d := n, d
			acts = append(acts, fuzzAction{fmt.Sprintf("knob:%s%+d", n, d), func(r *viewRig) { r.c.resync(); r.c.dispatch(n, d, 1) }})
		}
	}
	acts = append(acts,
		fuzzAction{"frame:uart", func(r *viewRig) {
			r.show(uartFrame([]string{"Hello\r\n", "OK\r\n"}, 460800, 80e-6, 100e-9, 8000))
		}},
		fuzzAction{"frame:tone", func(r *viewRig) { r.show(toneFrame("")) }},
		fuzzAction{"frame:small", func(r *viewRig) { r.show(smallFrame()) }},
		// Composite navigation, by the real keys: the sub-modes hide behind a
		// row and a key, which a uniform walk reaches too rarely.
		fuzzAction{"nav:match-editor", func(r *viewRig) {
			if reachRow(r.c, "bus.match") {
				r.c.button(btnF1)
			}
		}},
		fuzzAction{"nav:sheet-picker", func(r *viewRig) {
			if reachRow(r.c, "bus.match") {
				r.c.button(btnF2)
			}
		}},
		fuzzAction{"nav:bus-picker", func(r *viewRig) {
			for i := 0; i < 2 && (r.c.ui.open || r.c.ui.wpick); i++ {
				r.c.button(btnMenuOnOff)
			}
			if r.c.ui.ws != wsview.Bus || r.c.ui.busEye {
				r.c.button(btnF2)
			}
			r.show(uartFrame([]string{"Hello\r\n", "OK\r\n"}, 460800, 80e-6, 100e-9, 8000))
			r.c.button(btnAdjustPsh)
		}})
	// F1-F5 switch workspaces only while no sheet or picker is open, which a
	// walk this full of menu keys leaves too rarely to reach every workspace:
	// close what is open with Menu On/Off first, as a user would.
	for i, key := range []int{btnF1, btnF2, btnF3, btnF4, btnF5} {
		key := key
		acts = append(acts, fuzzAction{fmt.Sprintf("nav:ws%d", i+1), func(r *viewRig) {
			for j := 0; j < 3 && (r.c.ui.open || r.c.ui.wpick || r.c.ui.sub != subNone); j++ {
				r.c.button(btnMenuOnOff)
			}
			r.c.button(key)
		}})
	}
	return acts
}

// reachRow moves to a row with the panel's keys and knobs; it reports
// whether it got there (the fuzz takes whatever state it is in).
// TRLC-LINKS: REQ-SDS-136
func reachRow(c *Controller, id string) bool {
	if c.ui.wpick {
		c.button(btnMenuOnOff) // closes the BUS picker
	}
	if !c.ui.open {
		c.button(btnMenuOnOff)
	}
	ti := rowTab[id]
	for i := 0; i <= numTabs && c.ui.tab != ti; i++ {
		c.dispatch("tdiv", 1, 1)
	}
	for i := 0; i <= len(sheetTabs[ti].rows) && c.ui.open && curRow(&c.ui).id != id; i++ {
		c.dispatch("adjust", 1, 1)
	}
	return c.ui.open && c.ui.sub == subNone && !c.ui.edit && curRow(&c.ui).id == id
}

// checkUIInvariants inspects the controller and its view and returns every
// violated invariant.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-211
func checkUIInvariants(c *Controller) []string {
	var bad []string
	fail := func(format string, a ...any) { bad = append(bad, fmt.Sprintf(format, a...)) }

	c.mu.Lock()
	u := c.ui
	if u.ws < 0 || u.ws >= wsview.Count {
		fail("workspace %d", u.ws)
	}
	if u.tab < 0 || u.tab >= numTabs {
		fail("tab %d", u.tab)
	} else if r := u.rowAt[u.tab]; r < 0 || r >= len(sheetTabs[u.tab].rows) {
		fail("row %d of tab %s", r, sheetTabs[u.tab].id)
	}
	if u.sub != subNone && !u.open {
		fail("sub-mode %d with no sheet", u.sub)
	}
	if u.wpick && (u.open || u.ws != wsview.Bus) {
		fail("BUS picker open=%v ws=%d", u.open, u.ws)
	}
	if u.edit && (!u.open || u.sub != subNone || curRow(&u).kind != wsview.Enum && curRow(&u).kind != wsview.Number) {
		fail("edit on %s (open %v sub %d)", curRow(&u).id, u.open, u.sub)
	}
	if c.kb.open != (u.open && u.sub == subMatch) {
		fail("match editor open %v, sub %d", c.kb.open, u.sub)
	}
	if !inIntRange(c.decProto, framecalc.ProtoOff, framecalc.NumProtos-1) || !inIntRange(c.decFormat, 0, 2) {
		fail("decProto %d decFormat %d", c.decProto, c.decFormat)
	}
	if c.decBaud < 50 || c.decBaud > 20000000 {
		fail("decBaud %d", c.decBaud)
	}
	if !inIntRange(c.decChA, 0, 1) || !inIntRange(c.decChB, 0, 1) {
		fail("decode channels A=%d B=%d", c.decChA, c.decChB)
	}
	if (c.decProto == framecalc.ProtoI2C || c.decProto == framecalc.ProtoSPI) && c.decChA == c.decChB {
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
	if !inInts(c.zoom, zoomFactors...) || c.zoomOff < -0.5 || c.zoomOff > 0.5 {
		fail("zoom %d off %g", c.zoom, c.zoomOff)
	}
	if !inIntRange(c.curType, 0, 1) || !inIntRange(c.curSel, 0, 1) {
		fail("cursor type/sel %d/%d", c.curType, c.curSel)
	}
	for i := 0; i < 2; i++ {
		if c.curX[i] < 0 || c.curX[i] > 1 || c.curY[i] < 0 || c.curY[i] > 1 {
			fail("cursor %d off-screen x=%g y=%g", i, c.curX[i], c.curY[i])
		}
	}
	if !inIntRange(c.viewMode, 0, 4) || !inIntRange(c.mathMode, 0, 4) || !inIntRange(c.fftSrc, 0, 1) {
		fail("view %d math %d fft source %d", c.viewMode, c.mathMode, c.fftSrc)
	}
	if c.pulseLvl < 0.05 || c.pulseLvl > 0.95 || c.slopeLo < 0.05 || c.slopeHi > 0.95 || c.slopeLo >= c.slopeHi {
		fail("pulse level %g slope %g/%g", c.pulseLvl, c.slopeLo, c.slopeHi)
	}
	window := func(mn, mx float64) bool { return mn < mx || mn == 0 && mx == 0 } // 0..0: the engine's unset window
	if !window(c.pulseMin, c.pulseMax) || !window(c.slopeMin, c.slopeMax) {
		fail("qualifier windows pulse %g..%g slope %g..%g", c.pulseMin, c.pulseMax, c.slopeMin, c.slopeMax)
	}
	if !inInts(c.srK, 8, 16, 32, 64) || !inIntRange(c.srStopMode, 0, srStopFPGAMatch) || !inIntRange(c.srCh, 0, 1) {
		fail("superres k %d mode %d ch %d", c.srK, c.srStopMode, c.srCh)
	}
	if !inInts(c.maskN, 16, 32, 64, 128) || c.maskTol < 0 || c.maskTol >= len(maskTols) || !inInts(c.stackN, stackDepths...) {
		fail("mask n %d tol %d master depth %d", c.maskN, c.maskTol, c.stackN)
	}
	if len(c.meas) > maxMeasItems {
		fail("%d readouts", len(c.meas))
	}
	if c.trigCode < engine.TrigCodeMin || c.trigCode > engine.TrigCodeMax {
		fail("trigCode %d", c.trigCode)
	}
	c.mu.Unlock()

	// The view agrees with the mode.
	v := c.View()
	menu := u.open || u.wpick
	if v.Rail == menu || (v.Keys != nil) != menu || (v.Sheet != nil) != u.open || (v.Picker != nil) != u.wpick {
		fail("view rail %v keys %v sheet %v picker %v for open %v picker %v", v.Rail, v.Keys != nil, v.Sheet != nil, v.Picker != nil, u.open, u.wpick)
	}
	if !menu {
		panes := 0
		for _, p := range []bool{v.Scope != nil, v.Bus != nil, v.Eye != nil, v.Spec != nil, v.Meas != nil, v.Cmp != nil} {
			if p {
				panes++
			}
		}
		if panes != 1 {
			fail("%d panes in workspace %d", panes, v.WS)
		}
	}
	st := c.PanelStatus()
	if (st.Mode == "workspace") == menu {
		fail("status mode %s with open %v picker %v", st.Mode, u.open, u.wpick)
	}
	return bad
}

// TRLC-LINKS: REQ-SDS-136, REQ-SDS-211, REQ-SDS-212
func TestUIFuzz(t *testing.T) {
	alphabet := fuzzAlphabet()
	const steps = 2500
	for _, seed := range []int64{1, 1337, 20260710} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			r := newViewRig(t)
			c := r.c
			eng := r.eng
			r.show(uartFrame([]string{"Hello\r\n"}, 460800, 80e-6, 100e-9, 8000))
			rng := rand.New(rand.NewSource(seed))
			actions := make([]string, 0, steps)
			tabs, wss, subs := map[int]bool{}, map[wsview.WS]bool{}, map[int]bool{}
			wpick := false
			dump := func() string {
				return fmt.Sprintf("reproducing sequence (%d steps):\n%s", len(actions), strings.Join(actions, " "))
			}
			sf := lcd.NewMemSurface()
			for i := 0; i < steps; i++ {
				a := alphabet[rng.Intn(len(alphabet))]
				actions = append(actions, a.name)
				func() {
					defer func() {
						if p := recover(); p != nil {
							t.Logf("%s", dump())
							t.Fatalf("seed %d step %d (%s) panicked: %v", seed, i, a.name, p)
						}
					}()
					a.run(r)
					if i%25 == 0 {
						r.c.OnFrame(r.f)
						lcd.Render(sf, r.f, r.hud(), true)
					}
				}()
				r.clk.advance(100e6)
				if bad := checkUIInvariants(c); len(bad) > 0 {
					t.Logf("%s", dump())
					t.Fatalf("seed %d step %d (%s) violated:\n  %s", seed, i, a.name, strings.Join(bad, "\n  "))
				}
				if ill := eng.takeIllegal(); len(ill) > 0 {
					t.Logf("%s", dump())
					t.Fatalf("seed %d step %d (%s) sent illegal engine commands:\n  %s", seed, i, a.name, strings.Join(ill, "\n  "))
				}
				c.mu.Lock()
				wss[c.ui.ws] = true
				if c.ui.open {
					tabs[c.ui.tab] = true
					subs[c.ui.sub] = true
				}
				wpick = wpick || c.ui.wpick
				c.mu.Unlock()
			}
			c.srCancel("cancelled")
			if len(wss) != wsview.Count || len(tabs) != numTabs || !subs[subMatch] || !subs[subPick] || !wpick {
				t.Errorf("seed %d left parts unvisited: workspaces %d/%d tabs %d/%d editor %v sheet picker %v BUS picker %v",
					seed, len(wss), wsview.Count, len(tabs), numTabs, subs[subMatch], subs[subPick], wpick)
			}
		})
	}
}

// TestDecodeChannelComplementarityAcrossProtoSwitch is the deterministic
// regression for a violation the fuzzer hunts statistically: UART only owns
// the Source role, so switching it used to leave the (invisible) data-role
// shadow on the same channel, and the next switch to I2C/SPI surfaced
// SCL/SDA (CLK/DATA) sharing one channel.
// TRLC-LINKS: REQ-SDS-136
func TestDecodeChannelComplementarityAcrossProtoSwitch(t *testing.T) {
	c, _, _ := newC(t)
	press(t, c, "utility")
	goRow(t, c, "bus.protocol")
	pickKey(t, c, "UART")
	goRow(t, c, "bus.source")
	pickKey(t, c, "CH2")
	goRow(t, c, "bus.protocol")
	pickKey(t, c, "I2C")
	if d, _ := c.Snapshot(); d.DecProto != framecalc.ProtoI2C || d.DecChA == d.DecChB {
		t.Fatalf("I2C entered with SCL/SDA sharing a channel: proto=%d A=%d B=%d", d.DecProto, d.DecChA, d.DecChB)
	}
	pickKey(t, c, "SPI") // I2C -> SPI must stay complementary too
	if d, _ := c.Snapshot(); d.DecProto != framecalc.ProtoSPI || d.DecChA == d.DecChB {
		t.Fatalf("SPI entered with CLK/DATA sharing a channel: proto=%d A=%d B=%d", d.DecProto, d.DecChA, d.DecChB)
	}
}
