package scpi

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// fakeScope is a truthful mini-instrument: every setter updates the stats
// snapshot the way the real engine eventually would, so set→query
// round-trips can be asserted exactly.
type fakeScope struct {
	stats engine.Stats
	frame *engine.Frame
	calls []string
}

func (f *fakeScope) Snapshot() engine.Stats           { return f.stats }
func (f *fakeScope) WithFrame(fn func(*engine.Frame)) { fn(f.frame) }
func (f *fakeScope) SetRunning(on bool) {
	f.calls = append(f.calls, "run")
	f.stats.Running = on
}
func (f *fakeScope) SetNorm(on bool) {
	f.calls = append(f.calls, "norm")
	f.stats.Norm = on
}
func (f *fakeScope) SetSingle() { f.calls = append(f.calls, "single") }
func (f *fakeScope) SetTdiv(t float64) (engine.Band, bool) {
	f.calls = append(f.calls, "tdiv")
	b, ok := engine.PlanTdiv(t)
	if ok {
		f.stats.TdivS = b.TdivS
	}
	return b, ok
}
func (f *fakeScope) SetTrigLevelCode(c uint16) uint16 {
	f.calls = append(f.calls, "trlv")
	// Mirror the real engine: codes clamp to the operational window and the
	// CLAMPED code is returned (TRLV? must reflect the effective level).
	if c < engine.TrigCodeMin {
		c = engine.TrigCodeMin
	}
	if c > engine.TrigCodeMax {
		c = engine.TrigCodeMax
	}
	f.stats.TrigCode = c
	return c
}
func (f *fakeScope) SetTrigSlope(r bool) {
	f.calls = append(f.calls, "slope")
	f.stats.TrigRising = r
}
func (f *fakeScope) SetTrigSource(ch int) {
	f.calls = append(f.calls, "src")
	f.stats.TrigSource = ch
}
func (f *fakeScope) SetOffsetDAC(ch int, c uint16) {
	f.calls = append(f.calls, "ofst")
	if ch == 0 {
		f.stats.OffC1 = c
	} else {
		f.stats.OffC2 = c
	}
}
func (f *fakeScope) SetAcqMode(m int) {
	f.calls = append(f.calls, "acq")
	f.stats.AcqMode = m
}
func (f *fakeScope) SetAvgCount(n int) {
	f.calls = append(f.calls, "avg")
	f.stats.AvgCount = n
}

// fakeFE is a truthful vertical front end: SetVdiv tracks the detent index
// and SetOffset stages the DAC code into the scope stats exactly like the
// real analog front end does through the engine.
type fakeFE struct {
	fs    *fakeScope
	idx   [2]int
	cpl   [2]int
	probe [2]float64
}

func (f *fakeFE) SetVdiv(ch, idx int) error      { f.idx[ch] = idx; return nil }
func (f *fakeFE) Snapshot() ([2]int, bool)       { return f.idx, true }
func (f *fakeFE) SetProbe(ch int, x float64)     { f.probe[ch] = x }
func (f *fakeFE) SetCoupling(ch, mode int) error { f.cpl[ch] = mode; return nil }
func (f *fakeFE) OffsetVolts(ch int, code uint16) float64 {
	return analog.OffsetVolts(ch, code)
}
func (f *fakeFE) SetOffset(ch int, volts float64) uint16 {
	code := analog.OffsetCode(ch, volts)
	f.fs.SetOffsetDAC(ch, code)
	return code
}

// fakeDisplay is a truthful device-display double (the panel controller's
// scpi.Display surface): plain state the XYDS/PESU/MENU handlers read+write.
type fakeDisplay struct {
	xy, persist, menu bool
}

func (d *fakeDisplay) ViewXY() bool        { return d.xy }
func (d *fakeDisplay) SetViewXY(on bool)   { d.xy = on }
func (d *fakeDisplay) PersistOn() bool     { return d.persist }
func (d *fakeDisplay) SetPersist(on bool)  { d.persist = on }
func (d *fakeDisplay) MenuOpen() bool      { return d.menu }
func (d *fakeDisplay) SetMenuOpen(on bool) { d.menu = on }

func newH(t *testing.T) (*Handler, *fakeScope) {
	t.Helper()
	f := &engine.Frame{
		C1: make([]uint8, 2048), C2: make([]uint8, 2048),
		Valid: 2048, WinCols: 2048, Seq: 3, SampleS: 800e-9,
	}
	for i := range f.C1 {
		f.C1[i] = uint8(i % 200)
	}
	fs := &fakeScope{
		stats: engine.Stats{Running: true, TdivS: 500e-6, AvgCount: 16, TrigRising: true},
		frame: f,
	}
	return New(fs, nil, nil, nil, t.Logf), fs
}

// newHFE is newH plus a truthful fake front end (VDIV/OFST round-trips) and a
// fake display (XYDS/PESU/MENU round-trips).
func newHFE(t *testing.T) (*Handler, *fakeScope, *fakeFE) {
	t.Helper()
	h, fs := newH(t)
	fe := &fakeFE{fs: fs, probe: [2]float64{1, 1}}
	h.fe = fe
	h.disp = &fakeDisplay{}
	return h, fs, fe
}

func do(t *testing.T, h *Handler, cmd string) string {
	t.Helper()
	return string(h.HandleLine([]byte(cmd + "\n")))
}

func TestIDN(t *testing.T) {
	h, _ := newH(t)
	got := do(t, h, "*IDN?")
	parts := strings.Split(strings.TrimSpace(got), ",")
	if len(parts) != 4 || parts[0] != "Siglent" || parts[1] != "SDS1102CML+" {
		t.Fatalf("*IDN? = %q", got)
	}
}

func TestReplyFormats(t *testing.T) {
	h, _ := newH(t)
	// TDIV: %.2E + LOWER-case s, header echoed.
	if got := do(t, h, "TDIV?"); got != "TDIV 5.00E-04s\n" {
		t.Fatalf("TDIV? = %q", got)
	}
	// SARA: SI-prefix exception. 1/800ns = 1.25 MSa.
	if got := do(t, h, "SARA?"); got != "SARA 1.25MSa\n" {
		t.Fatalf("SARA? = %q", got)
	}
	if got := do(t, h, "SANU? C1"); got != "SANU 2048\n" {
		t.Fatalf("SANU? = %q", got)
	}
	// CHDR OFF strips the header.
	do(t, h, "CHDR OFF")
	if got := do(t, h, "TDIV?"); got != "5.00E-04s\n" {
		t.Fatalf("TDIV? with CHDR OFF = %q", got)
	}
}

func TestErrorTokens(t *testing.T) {
	h, _ := newH(t)
	if got := do(t, h, "BOGUS?"); got != "Undefined header\n" {
		t.Fatalf("unknown = %q", got)
	}
	if got := do(t, h, "C9:VDIV?"); got != "Header suffix out of range\n" {
		t.Fatalf("C9 = %q", got)
	}
	if got := do(t, h, "TDIV 3.3E-3"); got != "Data out of range\n" {
		t.Fatalf("off-ladder tdiv = %q", got)
	}
}

func TestSettersSilent(t *testing.T) {
	h, fs := newH(t)
	if got := do(t, h, "TDIV 1E-3"); got != "" {
		t.Fatalf("setter replied %q", got)
	}
	if got := do(t, h, "TRMD NORM"); got != "" {
		t.Fatalf("TRMD replied %q", got)
	}
	if got := do(t, h, "TRLV 1.0"); got != "" {
		t.Fatalf("TRLV replied %q", got)
	}
	want := []string{"tdiv", "norm", "run", "trlv"}
	if strings.Join(fs.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v", fs.calls)
	}
}

func TestCompoundLine(t *testing.T) {
	h, _ := newH(t)
	got := do(t, h, "CHDR?;TDIV?")
	if got != "CHDR SHORT\nTDIV 5.00E-04s\n" {
		t.Fatalf("compound = %q", got)
	}
}

func TestWFSUAndDAT2(t *testing.T) {
	h, _ := newH(t)
	do(t, h, "WFSU SP,4,NP,100,FP,8")
	if got := do(t, h, "WFSU?"); got != "WFSU SP,4,NP,100,FP,8,SN,0\n" {
		t.Fatalf("WFSU? = %q", got)
	}
	out := h.HandleLine([]byte("C1:WF? DAT2\n"))
	head := "C1:WF ALL,#9000000100"
	if !bytes.HasPrefix(out, []byte(head)) {
		t.Fatalf("DAT2 header = %q", out[:24])
	}
	payload := out[len(head) : len(head)+100]
	// Sample i comes from FP + i·SP = 8, 12, 16... of (i%200).
	for i := 0; i < 100; i++ {
		if payload[i] != uint8((8+i*4)%200) {
			t.Fatalf("payload[%d] = %d", i, payload[i])
		}
	}
	if out[len(out)-1] != '\n' {
		t.Fatal("missing trailing newline")
	}

	// FP beyond the record: reject, never crash (factory bug).
	do(t, h, "WFSU SP,1,NP,0,FP,3000")
	if got := do(t, h, "C1:WF? DAT2"); got != "Data out of range\n" {
		t.Fatalf("FP beyond record = %q", got)
	}
}

func TestWavedesc(t *testing.T) {
	h, _ := newH(t)
	out := h.HandleLine([]byte("C1:WF? DESC\n"))
	head := "C1:WF ALL,#9000000346"
	if !bytes.HasPrefix(out, []byte(head)) {
		t.Fatalf("DESC header = %q", out[:24])
	}
	d := out[len(head) : len(head)+346]
	if string(d[0:8]) != "WAVEDESC" || string(d[16:19]) != "DSO" {
		t.Fatal("names")
	}
	if binary.LittleEndian.Uint16(d[34:]) != 1 {
		t.Fatal("COMM_ORDER")
	}
	if int32(binary.LittleEndian.Uint32(d[116:])) != 2048 {
		t.Fatal("WAVE_ARRAY_COUNT")
	}
	gain := math.Float32frombits(binary.LittleEndian.Uint32(d[156:]))
	if math.Abs(float64(gain)-1.0/50) > 1e-9 { // 1 V/div default / 50
		t.Fatalf("VERTICAL_GAIN = %v", gain)
	}
	hi := math.Float32frombits(binary.LittleEndian.Uint32(d[176:]))
	if math.Abs(float64(hi)-800e-9) > 1e-12 { // float32 precision
		t.Fatalf("HORIZ_INTERVAL = %v", hi)
	}
	ho := math.Float64frombits(binary.LittleEndian.Uint64(d[180:]))
	if math.Abs(ho-(-2048*800e-9/2)) > 1e-12 {
		t.Fatalf("HORIZ_OFFSET = %v", ho)
	}
}

// ofstRoundTrip is the expected OFST? value after OFST <v>: the set stages
// the DAC code, the query inverts it — same quantizer both ways.
func ofstRoundTrip(ch int, v float64) string {
	code := analog.OffsetCode(ch, v)
	w := 0.0
	if code != 0 {
		w = analog.OffsetVolts(ch, code)
	}
	return sciV(w)
}

// TestSetNeverLies is the automation-correctness contract over the settable
// command surface (spec 11 §3.3/§3.4): every set either round-trips through
// its query, or returns an explicit §3.4 error token — NEVER a silent
// success that the query then contradicts.
func TestSetNeverLies(t *testing.T) {
	cases := []struct {
		set     string
		setWant string // "" = silent success; otherwise the exact error line
		query   string
		want    string // exact query reply (CHDR SHORT grammar)
	}{
		// The four formerly-lying stubs.
		{"C1:UNIT A", "", "C1:UNIT?", "C1:UNIT A\n"},
		{"C2:UNIT V", "", "C2:UNIT?", "C2:UNIT V\n"},
		{"C1:UNIT W", "Command header error\n", "C1:UNIT?", "C1:UNIT V\n"},
		{"C1:SKEW 100NS", "", "C1:SKEW?", "C1:SKEW 1.00E-07s\n"},
		{"C2:SKEW -2.5E-9", "", "C2:SKEW?", "C2:SKEW -2.50E-09s\n"},
		{"C1:SKEW ABC", "Command header error\n", "C1:SKEW?", "C1:SKEW 0.00E+00s\n"},
		{"C1:INVS ON", "", "C1:INVS?", "C1:INVS ON\n"},
		{"C1:INVS OFF", "", "C1:INVS?", "C1:INVS OFF\n"},
		{"C2:INVS 1", "Command header error\n", "C2:INVS?", "C2:INVS OFF\n"},
		{"C1:BWL OFF", "", "C1:BWL?", "C1:BWL OFF\n"},
		{"C1:BWL ON", "Data out of range\n", "C1:BWL?", "C1:BWL OFF\n"},
		{"C2:BWL X", "Command header error\n", "C2:BWL?", "C2:BWL OFF\n"},
		// Same bug class: trigger coupling is fixed DC on this build.
		{"TRCP DC", "", "TRCP?", "TRCP DC\n"},
		{"TRCP AC", "Data out of range\n", "TRCP?", "TRCP DC\n"},
		{"TRCP JUNK", "Command header error\n", "TRCP?", "TRCP DC\n"},
		// TRSE: C1/C2 are the only routable sources (spec 05 §6 — EXT has no
		// software path). A real-but-unroutable vendor source errors and the
		// source stays put; a garbage token is a grammar error.
		{"TRSE EDGE,SR,EX,HT,OFF", "Data out of range\n", "TRSE?", "TRSE EDGE,SR,C1,HT,OFF\n"},
		{"TRSE EDGE,SR,LINE,HT,OFF", "Data out of range\n", "TRSE?", "TRSE EDGE,SR,C1,HT,OFF\n"},
		{"TRSE EDGE,SR,BOGUS,HT,OFF", "Command header error\n", "TRSE?", "TRSE EDGE,SR,C1,HT,OFF\n"},
		// CPL: only the couplings the hardware has (A1M/D1M/GND — 1 MΩ only);
		// the 50 Ω vendor forms error, garbage no longer echoes back.
		{"C2:CPL GND", "", "C2:CPL?", "C2:CPL GND\n"},
		{"C1:CPL A50", "Data out of range\n", "C1:CPL?", "C1:CPL D1M\n"},
		{"C1:CPL D50", "Data out of range\n", "C1:CPL?", "C1:CPL D1M\n"},
		{"C1:CPL GARBAGE", "Command header error\n", "C1:CPL?", "C1:CPL D1M\n"},
		// TRLV: the query reflects the CLAMPED effective level, never a
		// request past the DAC window (±(31437−27000)/911 … measured global fit).
		{"TRLV 100", "", "TRLV?", "TRLV 4.87E+00V\n"},
		{"TRLV -100", "", "TRLV?", "TRLV -3.91E+00V\n"},
		{"TRLV 5E-4", "", "TRLV?", "TRLV 0.00E+00V\n"}, // quantized to code 31437 = 0 V
		// Display commands: XYDS/PESU/MENU wire to the REAL panel state
		// (fakeDisplay here); GRDS/INTS/BUZZ are fixed truths (BWL rule).
		{"XYDS ON", "", "XYDS?", "XYDS ON\n"},
		{"XYDS OFF", "", "XYDS?", "XYDS OFF\n"},
		{"XYDS MAYBE", "Command header error\n", "XYDS?", "XYDS OFF\n"},
		{"PESU INFINITE", "", "PESU?", "PESU INFINITE\n"},
		{"PESU OFF", "", "PESU?", "PESU OFF\n"},
		{"PESU 2", "Data out of range\n", "PESU?", "PESU OFF\n"},
		{"PESU FOREVER", "Command header error\n", "PESU?", "PESU OFF\n"},
		{"MENU ON", "", "MENU?", "MENU ON\n"},
		{"MENU OFF", "", "MENU?", "MENU OFF\n"},
		{"GRDS FULL", "", "GRDS?", "GRDS FULL\n"},
		{"GRDS OFF", "Data out of range\n", "GRDS?", "GRDS FULL\n"},
		{"GRDS X", "Command header error\n", "GRDS?", "GRDS FULL\n"},
		{"INTS GRID,100,TRACE,100", "", "INTS?", "INTS GRID,100,TRACE,100\n"},
		{"INTS TRACE,100", "", "INTS?", "INTS GRID,100,TRACE,100\n"},
		{"INTS GRID,50,TRACE,80", "Data out of range\n", "INTS?", "INTS GRID,100,TRACE,100\n"},
		{"INTS GRID,100,TRACE", "Command header error\n", "INTS?", "INTS GRID,100,TRACE,100\n"},
		{"BUZZ OFF", "", "BUZZ?", "BUZZ OFF\n"},
		{"BUZZ ON", "Data out of range\n", "BUZZ?", "BUZZ OFF\n"},
		// The rest of the settable surface with query forms.
		{"CHDR SHORT", "", "CHDR?", "CHDR SHORT\n"},
		{"TDIV 1E-3", "", "TDIV?", "TDIV 1.00E-03s\n"},
		{"TRDL 2E-6", "", "TRDL?", "TRDL 2.00E-06s\n"},
		{"TRMD NORM", "", "TRMD?", "TRMD NORM\n"},
		{"TRLV 0.5", "", "TRLV?", "TRLV 4.99E-01V\n"},
		{"TRSL NEG", "", "TRSL?", "TRSL NEG\n"},
		{"TRSE EDGE,SR,C2,HT,OFF", "", "TRSE?", "TRSE EDGE,SR,C2,HT,OFF\n"},
		{"ACQW AVERAGE", "", "ACQW?", "ACQW AVERAGE\n"},
		{"ACQW PEAK_DETECT", "", "ACQW?", "ACQW PEAK_DETECT\n"},
		{"AVGA 64", "", "AVGA?", "AVGA 64\n"},
		{"WFSU SP,4,NP,100,FP,8", "", "WFSU?", "WFSU SP,4,NP,100,FP,8,SN,0\n"},
		{"C1:VDIV 0.5", "", "C1:VDIV?", "C1:VDIV 5.00E-01V\n"},
		{"C2:VDIV 0.02", "", "C2:VDIV?", "C2:VDIV 2.00E-02V\n"},
		{"C1:OFST 1.0", "", "C1:OFST?", "C1:OFST " + ofstRoundTrip(0, 1.0) + "\n"},
		{"C1:CPL A1M", "", "C1:CPL?", "C1:CPL A1M\n"},
		{"C1:ATTN 10", "", "C1:ATTN?", "C1:ATTN 10\n"},
		{"C1:TRA OFF", "", "C1:TRA?", "C1:TRA OFF\n"},
	}
	for _, c := range cases {
		t.Run(c.set, func(t *testing.T) {
			h, _, _ := newHFE(t)
			if got := do(t, h, c.set); got != c.setWant {
				t.Fatalf("set %q replied %q, want %q", c.set, got, c.setWant)
			}
			if got := do(t, h, c.query); got != c.want {
				t.Fatalf("after %q, %q = %q, want %q", c.set, c.query, got, c.want)
			}
		})
	}
}

// Per-channel shadows must not leak across channels.
func TestChannelShadowIndependence(t *testing.T) {
	h, _, _ := newHFE(t)
	do(t, h, "C1:INVS ON;C1:UNIT A;C1:SKEW 5NS")
	got := do(t, h, "C2:INVS?;C2:UNIT?;C2:SKEW?")
	if got != "C2:INVS OFF\nC2:UNIT V\nC2:SKEW 0.00E+00s\n" {
		t.Fatalf("C2 shadows leaked: %q", got)
	}
}

// *RST is Default Setup: the new channel shadows return to power-on state.
func TestRSTResetsChannelShadows(t *testing.T) {
	h, _ := newH(t)
	do(t, h, "C1:INVS ON;C1:UNIT A;C1:SKEW 5NS")
	do(t, h, "*RST")
	got := do(t, h, "C1:INVS?;C1:UNIT?;C1:SKEW?")
	if got != "C1:INVS OFF\nC1:UNIT V\nC1:SKEW 0.00E+00s\n" {
		t.Fatalf("*RST left shadows: %q", got)
	}
}

// *RST also restores the pre-existing tra/cpl/attn shadows to the power-on
// state (TRA ON, D1M, ×1 — the New() defaults), pushes the coupling/probe
// reset through the front end, and returns the display to Y-T/persist-off —
// so the post-reset queries describe the real instrument.
func TestRSTResetsTraCplAttn(t *testing.T) {
	h, _, fe := newHFE(t)
	do(t, h, "C1:TRA OFF;C2:TRA OFF;C1:CPL A1M;C2:CPL GND;C1:ATTN 100;C2:ATTN 10")
	do(t, h, "XYDS ON;PESU INFINITE")
	do(t, h, "*RST")
	got := do(t, h, "C1:TRA?;C2:TRA?;C1:CPL?;C2:CPL?;C1:ATTN?;C2:ATTN?;XYDS?;PESU?")
	want := "C1:TRA ON\nC2:TRA ON\nC1:CPL D1M\nC2:CPL D1M\nC1:ATTN 1\nC2:ATTN 1\nXYDS OFF\nPESU OFF\n"
	if got != want {
		t.Fatalf("*RST left shadows:\n got %q\nwant %q", got, want)
	}
	// The front end really was reset, not just the bookkeeping.
	if fe.cpl != [2]int{analog.CplDC, analog.CplDC} {
		t.Fatalf("*RST left front-end coupling %v", fe.cpl)
	}
	if fe.probe != [2]float64{1, 1} {
		t.Fatalf("*RST left probe factors %v", fe.probe)
	}
}

// Without a panel (disp == nil) the display commands degrade to the BWL rule:
// the fixed state round-trips, anything else errors — never a silent no-op.
func TestDisplayStubsWithoutPanel(t *testing.T) {
	h, _ := newH(t)
	for _, c := range []struct{ cmd, want string }{
		{"XYDS OFF", ""},
		{"XYDS ON", "Data out of range\n"},
		{"XYDS?", "XYDS OFF\n"},
		{"PESU OFF", ""},
		{"PESU INFINITE", "Data out of range\n"},
		{"PESU?", "PESU OFF\n"},
		{"MENU OFF", ""},
		{"MENU ON", "Data out of range\n"},
		{"MENU?", "MENU OFF\n"},
	} {
		if got := do(t, h, c.cmd); got != c.want {
			t.Fatalf("%q = %q, want %q", c.cmd, got, c.want)
		}
	}
}

// Inverted() is the render surface's view of the INVS shadow (the web status
// snapshot and the LCD HUD read it) — it must track sets and *RST exactly.
func TestInvertedSnapshot(t *testing.T) {
	h, _ := newH(t)
	if h.Inverted() != [2]bool{} {
		t.Fatal("power-on invert state not OFF/OFF")
	}
	do(t, h, "C1:INVS ON")
	if h.Inverted() != [2]bool{true, false} {
		t.Fatalf("after C1:INVS ON: %v", h.Inverted())
	}
	do(t, h, "C2:INVS ON;C1:INVS OFF")
	if h.Inverted() != [2]bool{false, true} {
		t.Fatalf("after C2 ON / C1 OFF: %v", h.Inverted())
	}
	do(t, h, "C1:INVS ON;*RST")
	if h.Inverted() != [2]bool{} {
		t.Fatalf("*RST left invert: %v", h.Inverted())
	}
}

func TestRollRescale(t *testing.T) {
	h, fs := newH(t)
	fs.frame.RollCodes = true
	// Roll codes are HALF-scale: a +25 roll deviation is +50 deep codes.
	for i := range fs.frame.C1 {
		fs.frame.C1[i] = 153 // +25 from centre → deep +50 → 178
	}
	do(t, h, "WFSU SP,1,NP,4,FP,0")
	out := h.HandleLine([]byte("C1:WF? DAT2\n"))
	head := "C1:WF ALL,#9000000004"
	payload := out[len(head) : len(head)+4]
	if payload[0] != 178 {
		t.Fatalf("roll rescale: %d, want 178 (deviation doubled)", payload[0])
	}
	// Clamp: a +100 roll deviation would be +200 deep → clamp to 255.
	for i := range fs.frame.C1 {
		fs.frame.C1[i] = 228
	}
	out = h.HandleLine([]byte("C1:WF? DAT2\n"))
	if out[len(head)] != 255 {
		t.Fatalf("roll rescale clamp: %d, want 255", out[len(head)])
	}
}

func (f *fakeFE) TrigCode(volts float64, srcCh int) float64 { return 31437 - 911*volts }
func (f *fakeFE) TrigVolts(code uint16, srcCh int) float64  { return (31437 - float64(code)) / 911 }
