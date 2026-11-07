// ENGMODEL-OWNER-UNIT: FU-APP-SCPI

package scpi

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/replies.golden")

// goldenDisplay is every display surface the handler asserts on: X-Y,
// persistence, menu, trace visibility, cursors (state and values), the
// measurement panel and the panel's default setup.
type goldenDisplay struct {
	fakeDisplay
	on            [2]bool
	cursors, meas bool
	x, y          [2]float64
	buttons       []string
}

func (d *goldenDisplay) TraceOn(ch int) bool        { return d.on[ch] }
func (d *goldenDisplay) SetTraceOn(ch int, on bool) { d.on[ch] = on }
func (d *goldenDisplay) CursorsOn() bool            { return d.cursors }
func (d *goldenDisplay) SetCursorsOn(on bool)       { d.cursors = on }
func (d *goldenDisplay) SetMeasOn(on bool)          { d.meas = on }
func (d *goldenDisplay) CursorState() (bool, int, [2]float64, [2]float64) {
	return d.cursors, 0, d.x, d.y
}
func (d *goldenDisplay) InjectButtonWait(b string, _ time.Duration) bool {
	d.buttons = append(d.buttons, b)
	return true
}

// goldenScope is a scope whose frame is two phase-shifted squares, so the
// measurement and waveform replies carry real values.
func goldenScope() *fakeScope {
	f := &sample.Frame{
		WinPoints: 2000,
		Seq:       7,
		SampleS:   1e-8,
		TriggerAt: 1000.25,
		Traces: []sample.Trace{
			{Codes8: square(2000, 100, 0), Valid: 2000},
			{Ch: 1, Codes8: square(2000, 100, 10), Valid: 2000},
		},
	}
	return &fakeScope{
		stats: engine.Stats{Running: true, TdivS: 2e-6, AvgCount: 16, TrigRising: true, TrigPosFrac: .5},
		frame: f,
	}
}

// goldenFull is a handler with every optional part present.
func goldenFull(tb testing.TB) *Handler {
	fs := goldenScope()
	fe := &fakeFE{fs: fs, probe: [2]float64{1, 1}}
	h := New(fs, liveFE{fe}, board.New(fs, fe), nil, func() []byte { return []byte("BMPDATA") }, tb.Logf)
	h.disp = &goldenDisplay{on: [2]bool{true, true}, x: [2]float64{.25, .75}, y: [2]float64{.25, .75}}
	h.SetSetupMemory(&fakeSetupFiles{files: map[string]bool{}})
	return h
}

// goldenBare is a handler without front end, display, screenshot or setups.
func goldenBare(tb testing.TB) *Handler {
	fs := goldenScope()
	return New(fs, nil, board.New(fs, nil), nil, nil, tb.Logf)
}

// goldenLines is every header the handler answers, in its set and query
// forms, with good, refused and malformed arguments. Lines run in order on
// one handler, so later queries read earlier settings.
var goldenLines = []string{
	"*IDN?", "*OPC?", "*OPC", "*CLS", "*ESE 5", "*SRE 5", "*STB?", "*ESR?", "*TST?", "*CAL?", "*WAI",
	"INR?", "CMR?", "SRLN?", "*SAV 1", "*RCL 1", "*SAV 0", "*RCL 21", "*SAV X", "*RCL 2",
	"CHDR?", "CHDR OFF", "CHDR?", "TDIV?", "C1:VDIV?", "CHDR LONG", "CHDR?", "CHDR ON", "CHDR SHORT", "CHDR BOGUS",
	"TDIV?", "TDIV 1US", "TDIV?", "TDIV 5MS", "TDIV?", "TDIV 2E-6", "TDIV 1E9", "TDIV ABC", "TDIV?",
	"TRDL?", "TRDL 2US", "TRDL?", "TRDL -10US", "TRDL?", "TRDL 1", "TRDL X", "TRDL 0",
	"TRMD?", "TRMD NORM", "TRMD?", "TRMD SINGLE", "TRMD?", "TRMD STOP", "TRMD?", "TRMD AUTO", "TRMD?", "TRMD BOGUS",
	"TRLV?", "TRLV 0.5", "TRLV?", "TRLV 1E3", "TRLV?", "TRLV X", "TRLV NAN", "TRLV -0.25V", "TRLV?",
	"C1:TRLV?", "C2:TRLV?", "C1:TRLV 0.1", "C1:TRLV?", "C2:TRLV 0.1",
	"TRSL?", "TRSL NEG", "TRSL?", "C1:TRSL?", "TRSL POS", "TRSL?", "TRSL X", "C2:TRSL NEG",
	"TRSE?", "TRSE EDGE,SR,C2,HT,OFF", "TRSE?", "C2:TRLV?", "TRSE EDGE,SR,EX", "TRSE EDGE,SR,C9", "TRSE EDGE,SR",
	"TRSE EDGE,SR,C1", "TRSE EDGE", "TRSE?",
	"TRCP?", "TRCP DC", "TRCP AC", "TRCP X",
	"ARM", "TRMD?", "FRTR", "STOP", "TRMD?", "TRMD AUTO",
	"ACQW?", "ACQW PEAK_DETECT", "ACQW?", "ACQW AVERAGE,16", "ACQW?", "ACQW ERES", "ACQW?", "ACQW SAMPLING", "ACQW?", "ACQW X",
	"AVGA?", "AVGA 64", "AVGA?", "AVGA 0", "AVGA 300", "AVGA X",
	"SARA?", "SAST?", "SANU?",
	"WFSU?", "WFSU SP,4,NP,10,FP,3,SN,0", "WFSU?", "C1:WF? DAT2", "C2:WF? DAT2", "C1:WF? DESC", "C2:WF? ALL",
	"WFSU SP,1,NP,0,FP,0", "C1:WF? DAT2", "C1:WF? BOGUS", "WFSU FP,5000", "C1:WF? DAT2", "WFSU FP,0",
	"WFSU SP,300", "WFSU NP,-1", "WFSU XX,1", "WFSU SP", "WFSU TYPE,1",
	"SCDP", "XYDS?", "XYDS ON", "XYDS?", "XYDS OFF", "XYDS X",
	"PESU?", "PESU INFINITE", "PESU?", "PESU 5", "PESU OFF", "PESU X",
	"MENU?", "MENU ON", "MENU?", "MENU OFF", "MENU X",
	"GRDS?", "GRDS FULL", "GRDS HALF", "GRDS X",
	"INTS?", "INTS GRID,100,TRACE,100", "INTS GRID,50", "INTS GRID,200", "INTS GRID", "INTS X,1", "INTS GRID,A",
	"BUZZ?", "BUZZ OFF", "BUZZ ON", "BUZZ X",
	"HCSU?", "HCSU DEV,BMP", "HCSU DEV,PNG", "HCSU DEV", "HCSU",
	"STPN DISK,UDSK,FILE,'G1.SET'", "RCPN DISK,UDSK,FILE,'G1.SET'", "RCPN DISK,UDSK,FILE,'NONE.SET'",
	"STPN DISK,UDSK,FILE,'../X'", "STPN UDSK,FILE,'A.SET'",
	"CRMS?", "CRMS MANUAL", "CRMS?", "C1:CRVA? HREL", "C2:CRVA? VREL", "C1:CRVA? XREL", "CRMS TRACK", "CRMS X", "CRMS OFF",
	"C1:CRVA? HREL",
	"PACU PKPK,C1", "PACU BOGUS,C1", "PACU PKPK,C3", "PACU PKPK", "CRST HREF,1", "PNSU 1",
	"C1:VDIV?", "C1:VDIV 0.5", "C1:VDIV?", "C1:VDIV 0.3", "C1:VDIV X", "C2:VDIV 2V", "C2:VDIV?",
	"C1:OFST?", "C1:OFST 0.5", "C1:OFST?", "C1:OFST 100", "C1:OFST X", "C1:OFST NAN", "C2:OFST -1", "C2:OFST?",
	"C1:TRA?", "C1:TRA OFF", "C1:TRA?", "C1:TRA ON", "C1:TRA X",
	"C1:CPL?", "C1:CPL A1M", "C1:CPL?", "C1:CPL GND", "C1:CPL?", "C1:CPL D50", "C1:CPL X", "C1:CPL D1M",
	"C1:ATTN?", "C1:ATTN 10", "C1:ATTN?", "C1:VDIV?", "C1:OFST?", "C1:ATTN 0", "C1:ATTN X", "C1:ATTN 1",
	"C1:BWL?", "C1:BWL ON", "C1:BWL OFF", "C1:BWL X",
	"C1:UNIT?", "C1:UNIT A", "C1:UNIT?", "C1:UNIT X", "C1:UNIT V",
	"C1:SKEW?", "C1:SKEW 1E-9", "C1:SKEW?", "C1:SKEW X", "C1:SKEW INF",
	"C1:INVS?", "C1:INVS ON", "C1:INVS?", "C1:PAVA? PKPK", "C1:INVS X", "C1:INVS OFF",
	"C1:PAVA? PKPK", "C1:PAVA? MAX", "C1:PAVA? MIN", "C1:PAVA? AMPL", "C1:PAVA? TOP", "C1:PAVA? BASE",
	"C1:PAVA? MEAN", "C1:PAVA? RMS", "C1:PAVA? OVSP", "C1:PAVA? RPRE", "C1:PAVA? PER", "C1:PAVA? FREQ",
	"C1:PAVA? PWID", "C1:PAVA? NWID", "C1:PAVA? RISE", "C1:PAVA? FALL", "C1:PAVA? DUTY", "C1:PAVA? NDUTY",
	"C2:PAVA? ALL", "C1:PAVA? BOGUS",
	"C1-C2:MEAD? FRR", "C1-C2:MEAD? FRF", "C1-C2:MEAD? FFR", "C1-C2:MEAD? FFF", "C1-C2:MEAD? LRR",
	"C1-C2:MEAD? LRF", "C1-C2:MEAD? LFR", "C1-C2:MEAD? LFF", "C2-C1:MEAD? PHA", "C1-C2:MEAD? SKEW",
	"C1-C2:MEAD? BOGUS", "C1-C3:MEAD? FRR", "C1-C1:MEAD? FRR", "C1-C2:PAVA? PKPK",
	"C3:VDIV?", "C0:VDIV?", "C10:VDIV?", "C1:TDIV?", "C1:", "C1: VDIV", "FOO", "FOO?", "SGLT_X", "MAC_GET", "LOAD:X",
	"", ";;", " tdiv? ", "c1:vdiv?", "TDIV?;C1:VDIV?;TRMD?", "CHDR OFF;TDIV?;C1:TRA?;TRMD?;MSIZ?", "CHDR SHORT",
	"TDIV?\r", "*IDN?;*OPC?", "TDIV 1US;*OPC?;TDIV?",
	"*RST", "TDIV?", "TRMD?", "CHDR?", "WFSU?", "C1:INVS?", "C1:CPL?", "C1:ATTN?", "XYDS?", "PESU?",
}

// goldenBareLines are the headers whose replies depend on a missing front
// end, display, screenshot or setup memory.
var goldenBareLines = []string{
	"C1:VDIV?", "C1:VDIV 0.5", "C1:OFST?", "C1:CPL A1M", "C1:CPL?", "C1:ATTN 10", "C1:ATTN?", "C1:BWL ON", "C1:BWL OFF",
	"C1:BWL?", "C1:TRA OFF", "C1:TRA?", "XYDS ON", "XYDS OFF", "XYDS?", "PESU INFINITE", "PESU OFF", "PESU?",
	"MENU ON", "MENU OFF", "MENU?", "SCDP", "CRMS MANUAL", "CRMS?", "C1:CRVA? HREL", "PACU PKPK,C1",
	"STPN DISK,UDSK,FILE,'G1.SET'", "*SAV 1", "*RCL 1", "C1:PAVA? PKPK", "C1:WF? DESC", "*RST", "C1:CPL?",
}

// TestReplyGolden pins every reply byte of every header the handler answers.
// TRLC-LINKS: REQ-SDS-024
func TestReplyGolden(t *testing.T) {
	var b strings.Builder
	run := func(name string, h *Handler, lines []string) {
		fmt.Fprintf(&b, "== %s\n", name)
		for _, l := range lines {
			fmt.Fprintf(&b, "%q\t%q\n", l, h.HandleLine([]byte(l+"\n")))
		}
	}
	run("full", goldenFull(t), goldenLines)
	run("bare", goldenBare(t), goldenBareLines)
	path := filepath.Join("testdata", "replies.golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != string(want) {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := range min(len(gl), len(wl)) {
			if gl[i] != wl[i] {
				t.Fatalf("line %d:\n got  %s\n want %s", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("length differs: %d lines, want %d", len(gl), len(wl))
	}
}
