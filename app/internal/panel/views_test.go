// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/lcd"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/streamview"
)

// Every workspace, view, sheet tab and picker drawn end to end: the panel's
// view snapshot through the LCD renderer, from synthetic captures. Each
// screen must render and show its frame's analysis. With PANEL_VIEWS_DIR set
// the screens are also written there as PNGs to look at.

// bodeEng is the fake engine with an FRA sweep; it counts the leases of
// full-rate raw frames and of spectrum frames.
type bodeEng struct {
	*fakeEng
	pts          []engine.BodePoint
	leases       atomic.Int32 // raw frames (LeaseDecodeView)
	specLeases   atomic.Int32 // spectrum frames (LeaseSpectrumView)
	specReleases atomic.Int32
}

// BodePoints is the accumulated sweep.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (e *bodeEng) BodePoints() []engine.BodePoint { return e.pts }

// LeaseDecodeView counts a lease of raw frames.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (e *bodeEng) LeaseDecodeView(d time.Duration) {
	if d > 0 {
		e.leases.Add(1)
	}
}

// SetBodeMode arms the frequency response on ref / dut (the engine's own).
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (e *bodeEng) SetBodeMode(on bool, ref, dut int) {
	e.set(func(st *engine.Stats) { st.BodeMode, st.BodeRef, st.BodeDut = b2ic(on), ref, dut })
}

// ClearBode forgets the points.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (e *bodeEng) ClearBode() {
	e.pts = nil
	e.set(func(st *engine.Stats) { st.BodePoints = 0 })
}

// LeaseSpectrumView counts a lease of spectrum frames, and its release.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (e *bodeEng) LeaseSpectrumView(d time.Duration) {
	if d > 0 {
		e.specLeases.Add(1)
	} else {
		e.specReleases.Add(1)
	}
}

// viewRig drives a controller with captures and renders its screens.
type viewRig struct {
	t     *testing.T
	c     *Controller
	eng   *bodeEng
	fe    *fakeFE
	clk   *testClock
	fmu   sync.Mutex // f: the super-res loop reads it from its goroutine
	f     *engine.Frame
	seq   uint64
	spect *lcd.Spectrogram
}

// fakeStream is a decoded stream that never starts (Auto finds nothing).
type fakeStream struct{ mu sync.Mutex }

// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (s *fakeStream) Start(engine.SerialParams, engine.StreamTrigger, int, string) error { return nil }

// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (s *fakeStream) Stop() {}

// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (s *fakeStream) Active() bool { return false }

// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (s *fakeStream) Snapshot() streamview.View { return streamview.View{} }

// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (s *fakeStream) Refuse(string) {}

// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (s *fakeStream) Lines(int, int) streamview.LinesPage { return streamview.LinesPage{} }

// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (s *fakeStream) LinesBinary(int, []byte) int { return 0 }

// newViewRig is a controller with every optional part: the FRA sweep, the
// serial trigger, the bandwidth limit, the display invert, a decoded
// stream, a frame source and a manual clock.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func newViewRig(t *testing.T) *viewRig {
	t.Helper()
	fe := &fakeFE{idx: [2]int{8, 8}}
	eng := &bodeEng{fakeEng: newEng()}
	c := New(eng, bwlFE{fe}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	c.detectSync = true
	r := &viewRig{t: t, c: c, eng: eng, fe: fe, clk: withClock(c), spect: lcd.NewSpectrogram()}
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		r.fmu.Lock()
		f := r.f
		r.fmu.Unlock()
		fn(f)
	})
	c.SetInverter(&fakeInv{})
	c.SetDecodeStream(&fakeStream{}, func() (decode.Result, float64, bool) { return decode.Result{}, 0, false })
	return r
}

// show publishes capture f (a new Seq) and lets the panel analyse it.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (r *viewRig) show(f *engine.Frame) {
	r.seq++
	f.Seq = r.seq
	r.fmu.Lock()
	r.f = f
	r.fmu.Unlock()
	r.eng.set(func(st *engine.Stats) { st.Seq, st.TdivS = r.seq, f.TdivS })
	r.c.OnFrame(f)
	if f.SampleS > 0 && !f.IsEnv {
		d, _ := r.c.Snapshot()
		r.spect.Push(f, d.FFTSrc, 0.5/f.SampleS)
	}
}

// hud is the LCD state for the controller's snapshot, as the app builds it.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (r *viewRig) hud() lcd.HUD {
	c, st := r.c, r.eng.Snapshot()
	d, v := c.Snapshot()
	h := lcd.HUD{C1VdivV: analog.Detents[r.fe.idx[0]].VdivV, C2VdivV: analog.Detents[r.fe.idx[1]].VdivV,
		TdivS: st.TdivS, TrigSrc: st.TrigSource, TrigRising: st.TrigRising, Running: st.Running, Norm: st.Norm,
		Single: st.Single, TrigPosFrac: st.TrigPosFrac, TwoChan: true, URL: "http://192.168.1.209:8080",
		ShowC1: d.ShowC1, ShowC2: d.ShowC2, ChanExplicit: true, ShowMeas: d.ShowMeas, MeasItems: d.MeasItems,
		ViewMode: d.ViewMode, MathMode: d.MathMode, FFTSrc: d.FFTSrc, AutosetBusy: d.AutosetBusy, AutosetMsg: d.AutosetMsg,
		Notice: v.Notice, Remote: d.Remote, UI: v, Match: lcd.MatchView(c.MatchEditor()), Zoom: d.Zoom, ZoomOff: d.ZoomOff,
		Persist: d.Persist, DecProto: d.DecProto, DecBaud: d.DecBaud, DecChA: d.DecChA, DecChB: d.DecChB,
		DecCPOL: d.DecCPOL, DecCPHA: d.DecCPHA, DecFormat: d.DecFormat, CurOn: d.CurOn, CurType: d.CurType,
		CurSel: d.CurSel, CurX: d.CurX, CurY: d.CurY, MaskMode: st.MaskMode, MaskPass: st.MaskPass, MaskFail: st.MaskFail,
		MaskMsg: c.MaskStatus(), Spect: r.spect}
	h.TrigLvlV = (31437 - float64(st.TrigCode)) / 911
	h.TrigLvlDiv = h.TrigLvlV / h.C1VdivV
	for i, rw := range c.RefView() {
		h.RefC1[i], h.RefC2[i], h.RefShow[i], h.RefVdiv[i], h.RefOff[i], h.RefProbe[i] = rw.C1, rw.C2, rw.Show, rw.Vdiv, rw.Off, rw.Probe
	}
	for _, p := range r.eng.pts {
		h.BodeFreq, h.BodeGain, h.BodePhase = append(h.BodeFreq, p.FreqHz), append(h.BodeGain, p.GainDB), append(h.BodePhase, p.PhaseDeg)
	}
	if r.f != nil {
		h.SampleS, h.Trigd = r.f.SampleS, r.f.Trigd
	}
	return h
}

// shot renders the screen; with PANEL_VIEWS_DIR set it is saved as name.png.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (r *viewRig) shot(name string) wsview.View {
	r.t.Helper()
	if r.f != nil {
		r.c.OnFrame(r.f) // as the LCD loop does each tick
	}
	v := r.c.View()
	sf := lcd.NewMemSurface()
	func() {
		defer func() {
			if p := recover(); p != nil {
				r.t.Fatalf("%s: render panicked: %v", name, p)
			}
		}()
		lcd.Render(sf, r.f, r.hud(), true)
	}()
	if dir := os.Getenv("PANEL_VIEWS_DIR"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, name+".png"), lcd.EncodePNG(sf), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	return v
}

// uartFrame is a capture of a UART bus on C1 (C2 idle high): msgs sent
// one after another at baud, gapS of idle between them, n samples sampleS
// apart, triggered at the record's middle.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func uartFrame(msgs []string, baud, gapS, sampleS float64, n int) *engine.Frame {
	c1, c2 := make([]uint8, n), make([]uint8, n)
	for i := range c1 {
		c1[i], c2[i] = 210, 210
	}
	spb := 1 / baud / sampleS
	t := 20 * spb
	put := func(level uint8, bits float64) {
		a, b := int(t), int(t+bits*spb)
		for i := max(a, 0); i < min(b, n); i++ {
			c1[i] = level
		}
		t += bits * spb
	}
	for k := 0; t < float64(n); k++ {
		for _, ch := range []byte(msgs[k%len(msgs)]) {
			put(40, 1)
			for bit := 0; bit < 8; bit++ {
				put(map[bool]uint8{true: 210, false: 40}[ch>>bit&1 == 1], 1)
			}
			put(210, 2) // the stop bit and one idle bit: every byte on the bit grid
		}
		t += gapS / sampleS
	}
	for i := range c1 { // a little ringing on the edges' flat tops
		c1[i] = uint8(int(c1[i]) + int(2*math.Sin(float64(i)*0.7)))
	}
	return &engine.Frame{C1: c1, C2: c2, Valid: n, WinCols: n, EdgeX: float64(n) / 2, Ptp: 170,
		TdivS: float64(n) * sampleS / 10, SampleS: sampleS, Trigd: true}
}

// toneFrame is a 1 kHz square on C1 and a 3 kHz sine on C2 over 5 ms.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func toneFrame(filter string) *engine.Frame {
	const n = 20000
	sampleS := 5e-3 / n
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, WinCols: n, EdgeX: n / 2, Ptp: 150,
		TdivS: 500e-6, SampleS: sampleS, Trigd: true, Filter: filter}
	for i := 0; i < n; i++ {
		ph := float64(i-n/2) * sampleS * 1e3
		v := 60.0
		if ph-math.Floor(ph) < 0.5 {
			v = 196
		}
		f.C1[i] = uint8(v + 2*math.Sin(float64(i)*1.3))
		f.C2[i] = uint8(128 + 50*math.Sin(2*math.Pi*3*ph))
	}
	return f
}

// TestWorkspaceScreens walks every workspace, view, sheet and picker.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-212, REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func TestWorkspaceScreens(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	tone := func() { r.show(toneFrame("")) }
	tone()

	// SCOPE: the rail and its label, X-Y, the sheet with its own tabs first.
	press(t, c, "f2", "f1")
	if v := r.shot("scope_rail"); !v.Rail || v.RailNote == nil || v.Scope == nil {
		t.Fatalf("SCOPE rail %+v", v)
	}
	r.clk.advance(2 * time.Second)
	press(t, c, "f1")
	if v := r.shot("scope_xy"); c.viewMode != viewXY || v.Layout.MenuW != 30 {
		t.Fatalf("SCOPE again: view %d", c.viewMode)
	}
	press(t, c, "f1", "menu")
	r.clk.advance(2 * time.Second)
	if v := r.shot("scope_sheet_trig"); v.Sheet == nil || v.Keys == nil || v.Sheet.Tabs[0].Name != "TRIG" || !v.Sheet.Tabs[0].Active {
		t.Fatalf("SCOPE sheet %+v", v.Sheet)
	}
	for _, tab := range []string{"ch1", "ch2", "time", "acq", "view", "meas", "curs", "cmp", "bus", "file", "sys"} {
		turn(c, "tdiv", 1)
		if got := c.PanelStatus().Sheet.Tab; got != tab {
			t.Fatalf("S/DIV reached %s, want %s", got, tab)
		}
		r.shot("sheet_" + tab)
	}
	goRow(t, c, "trig.level")
	pickKey(t, c, "Up")
	press(t, c, "adjustpush") // edit mode on the row
	r.shot("sheet_trig_edit")
	press(t, c, "adjustpush", "menu")
	if v := r.shot("scope_adjust"); v.Scope == nil || !strings.HasPrefix(v.Scope.AdjustTag, "Adjust: Level") {
		t.Fatalf("SCOPE Adjust tag %+v", v.Scope)
	}

	// BUS: auto-detects the UART and lists its packets.
	uart := func() {
		r.show(uartFrame([]string{"Hello, scope!\r\n", "T=21.5C\r\n"}, 460800, 120e-6, 100e-9, 20000))
	}
	uart()
	press(t, c, "f2")
	uart()
	v := r.shot("bus_packets")
	if v.Bus == nil || v.Bus.Off || len(v.Bus.Lines) == 0 {
		t.Fatalf("BUS pane %+v", v.Bus)
	}
	turn(c, "adjust", 2)
	press(t, c, "adjustpush")
	if v := r.shot("bus_picker"); v.Picker == nil || len(v.Picker.Toks) == 0 || v.Keys == nil {
		t.Fatalf("BUS picker %+v", v.Picker)
	}
	pickKey(t, c, "Trig on 2")
	if got := matchText(c.snap()); got == "any byte" {
		t.Fatal("picker set no match")
	}
	uart()
	r.shot("bus_match")
	press(t, c, "f2")
	for i := 0; i < 6; i++ {
		uart()
	}
	if v := r.shot("bus_eye"); v.Eye == nil || v.Eye.Why != "" || len(v.Eye.Grid) == 0 {
		t.Fatalf("BUS eye %+v", v.Eye)
	}
	press(t, c, "f2", "utility")
	goRow(t, c, "bus.match")
	r.shot("bus_sheet")
	pickKey(t, c, "Edit bytes")
	if v := r.shot("bus_editor"); !c.MatchEditor().Open || v.Keys == nil {
		t.Fatal("match editor not open")
	}
	press(t, c, "menu")
	goRow(t, c, "bus.match")
	pickKey(t, c, "From screen")
	if v := r.shot("bus_sheet_pick"); v.Sheet == nil || v.Sheet.Sub == nil {
		t.Fatalf("sheet picker %+v", v.Sheet)
	}
	press(t, c, "menu")

	// SPECTRUM: the FFT with its peak table, the spectrogram, Bode.
	tone()
	press(t, c, "f3")
	for i := 0; i < 3; i++ {
		tone()
	}
	r.clk.advance(2 * time.Second)
	if v := r.shot("spectrum_fft"); v.Spec == nil || len(v.Spec.Rows) == 0 || v.Spec.Marker == "" {
		t.Fatalf("SPECTRUM %+v", v.Spec)
	}
	turn(c, "adjust", 2)
	r.shot("spectrum_marker")
	press(t, c, "f3")
	r.shot("spectrum_spgm")
	r.eng.pts = []engine.BodePoint{{FreqHz: 100, GainDB: 0, PhaseDeg: -2}, {FreqHz: 1e3, GainDB: -0.4, PhaseDeg: -20},
		{FreqHz: 3e3, GainDB: -3.1, PhaseDeg: -45}, {FreqHz: 1e4, GainDB: -10.5, PhaseDeg: -73}, {FreqHz: 1e5, GainDB: -30, PhaseDeg: -88}}
	press(t, c, "f3")
	if v := r.shot("spectrum_bode"); v.Spec == nil || !v.Spec.Bode || len(v.Spec.Rows) != 5 {
		t.Fatalf("SPECTRUM Bode %+v", v.Spec)
	}

	// MEASURE: statistics, then all values.
	press(t, c, "f4")
	for i := 0; i < 5; i++ {
		tone()
	}
	r.clk.advance(2 * time.Second)
	if v := r.shot("measure_stats"); v.Meas == nil || len(v.Meas.Stats) == 0 || v.Meas.Stats[0].N != "5" {
		t.Fatalf("MEASURE %+v", v.Meas)
	}
	press(t, c, "f4")
	r.shot("measure_all")
	press(t, c, "f4")

	// COMPARE: the rows; stacking a master; the master tested.
	press(t, c, "f5")
	for i := 0; i < 8; i++ {
		tone()
	}
	r.clk.advance(2 * time.Second)
	if v := r.shot("compare"); v.Cmp == nil {
		t.Fatal("COMPARE pane missing")
	}
	press(t, c, "f5")
	r.show(toneFrame("block average 20/64 (waveform aligned)"))
	if v := r.shot("compare_stacking"); !v.Cmp.Rows[cmpMaster].Bar {
		t.Fatalf("stacking row %+v", v.Cmp.Rows[cmpMaster])
	}
	r.show(toneFrame("block average 64/64 (waveform aligned)"))
	for range 3 { // live captures: the master's mask is built in their geometry
		r.show(toneFrame(""))
	}
	r.clk.advance(3 * time.Second)
	r.eng.set(func(st *engine.Stats) { st.MaskPass = 120 })
	if v := r.shot("compare_master"); !c.RefView()[0].Show || r.eng.Snapshot().MaskMode != 1 || v.Cmp == nil {
		t.Fatalf("master: ref %v mask %d", c.RefView()[0].Show, r.eng.Snapshot().MaskMode)
	}

	// Help.
	press(t, c, "help")
	r.shot("help")
	press(t, c, "help")
}
