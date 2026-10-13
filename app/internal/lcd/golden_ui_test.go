// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"math"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
)

// Golden coverage for the task-workspace UI (ADR-TASK-WORKSPACES): the rail,
// each workspace's pane, a settings sheet, the byte picker, the match editor
// and the eye. The views are hand-built snapshots of what the panel
// produces (panel's TestWorkspaceScreens drives the real controller through
// the same renderer); the harness is golden_test.go's.

// goldenBusFrame is a UART capture: two messages repeating on C1, C2 idle.
// TRLC-LINKS: REQ-SDS-214
func goldenBusFrame() *engine.Frame {
	const n, spb = 4096, 8
	var bytes []int
	for k := 0; k < 6; k++ {
		for _, b := range []byte("Hi\x55\xAA") {
			bytes = append(bytes, int(b))
		}
	}
	f := &engine.Frame{C1: goldenUARTWave(bytes, spb, n), C2: make([]uint8, n), Valid: n, WinCols: n, EdgeX: n / 2,
		Ptp: 170, TdivS: 100e-6, SampleS: 1e-6 / spb * 4, Trigd: true}
	for i := range f.C2 {
		f.C2[i] = 210
	}
	return f
}

// goldenUIHUD is the HUD the workspace goldens share.
// TRLC-LINKS: REQ-SDS-211
func goldenUIHUD(v wsview.View) HUD {
	h := goldenYTHUD()
	h.UI = v
	h.Notice = v.Notice
	return h
}

// goldenRender renders f with view v and compares the golden name.
// TRLC-LINKS: REQ-SDS-211
func goldenRender(t *testing.T, name string, f *engine.Frame, h HUD) {
	t.Helper()
	sf := NewMemSurface()
	Render(sf, f, h, true)
	goldenAssert(t, name, sf)
}

// paneLayout is a workspace layout: the rail and a pane above the plot.
// TRLC-LINKS: REQ-SDS-211
func paneLayout(top int) wsview.Layout { return wsview.Layout{MenuW: 30, PlotTop: top} }

// TestGoldenScopeRail pins SCOPE: the full-height trace beside the
// pictogram rail (SCOPE active, inverted amber), the label after F1, the
// workspace's name in the top bar and the Adjust tag.
// TRLC-LINKS: REQ-SDS-211, REQ-SDS-213
func TestGoldenScopeRail(t *testing.T) {
	v := wsview.View{WS: wsview.Scope, Layout: paneLayout(0), Rail: true,
		RailNote: &wsview.Note{WS: wsview.Scope, Head: "F1 SCOPE", Text: "again: Y-T / X-Y"},
		Scope:    &wsview.ScopePane{AdjustTag: "Adjust: Level 200mV", Hot: true}}
	goldenRender(t, "ws_scope", goldenYTFrame(), goldenUIHUD(v))
}

// TestGoldenBusPackets pins BUS: the packet list as a hex dump with its
// string box, the selected packet, the match column, the tool line, and
// the strip's packet box under the trace.
// TRLC-LINKS: REQ-SDS-214
func TestGoldenBusPackets(t *testing.T) {
	line := func(num int, sel bool) wsview.BusLine {
		return wsview.BusLine{Num: num, Sel: sel, Hex: []string{"48", "69", "55", "AA"}, Err: make([]bool, 4), Str: "Hi..",
			Bytes: 4, Match: "55 AA"}
	}
	b := &wsview.BusPane{Pane: wsview.Pane{Title: "BUS", Caption: "UART 31250 8N1 on C1  -  6 packets", Right: "match 55 AA: 6 packets",
		Tool:  []wsview.Seg{{Key: "Adjust", Job: "packet"}, {Key: "push", Job: "pick bytes"}, {Key: "Position", Job: "next match"}, {Key: "push", Job: "to trigger"}, {Key: "Zoom", Job: "on packet"}},
		Again: "F2 again: Eye diagram"}}
	for i := 1; i <= 6; i++ {
		b.Lines = append(b.Lines, line(i, i == 2))
	}
	b.Lines[3].Err[1], b.Lines[3].Hex[1] = true, "ER"
	b.Marks = []wsview.PacketMark{{I0: 800, I1: 1150, Sel: true, Match: true}, {I0: 1700, I1: 2050, Match: true}}
	b.SelNum = 2
	v := wsview.View{WS: wsview.Bus, Layout: paneLayout(136), Rail: true, Bus: b}
	h := goldenUIHUD(v)
	h.TdivS, h.SampleS, h.DecProto, h.DecBaud = 100e-6, 0.5e-6, 2, 250000
	goldenRender(t, "ws_bus_packets", goldenBusFrame(), h)
}

// TestGoldenBusPicker pins the byte picker: the pane framed in amber, the
// first byte filled, the next three underlined, a bar at a message start,
// and the F-key column of choices.
// TRLC-LINKS: REQ-SDS-214
func TestGoldenBusPicker(t *testing.T) {
	var toks []wsview.PickTok
	for k := 0; k < 3; k++ {
		for i, b := range []string{"48", "69", "55", "AA"} {
			toks = append(toks, wsview.PickTok{Text: b, Start: i == 0})
		}
	}
	toks[6].Text = "ERR"
	pk := &wsview.Picker{Caption: "to trigger on  (12 decoded on screen)", Toks: toks, Cur: 4,
		Lines: []wsview.Span{{Text: "| = idle gap (a message starts).  Underlined: the bytes the F-keys would add.", Col: wsview.Dim},
			{Text: "Turn Adjust or Position: the first byte.  F1-F4: trigger on 1-4 bytes from it (type Serial).", Col: wsview.Text}},
		Now: "Now: any byte"}
	keys := &wsview.Keys{Title: "PICK BYTES", Top: 12, Items: [5]wsview.Key{{Label: "Trig on 1", Value: "48"}, {Label: "Trig on 2", Value: "48 69"},
		{Label: "Trig on 3", Value: "48 69 ERR"}, {Label: "Trig on 4", Value: "48 69 ERR AA"}, {Label: "Cancel", Value: "keep the match"}}}
	v := wsview.View{WS: wsview.Bus, Layout: wsview.Layout{MenuW: 116, PlotTop: 136}, Picker: pk, Keys: keys}
	h := goldenUIHUD(v)
	h.TdivS, h.SampleS = 100e-6, 0.5e-6
	goldenRender(t, "ws_bus_picker", goldenBusFrame(), h)
}

// goldenEyeGrid is a synthetic eye: the two levels as rails and the
// crossings at 0.5 and 1.5 UI as X-shaped edges with a little jitter.
// TRLC-LINKS: REQ-SDS-215
func goldenEyeGrid(cols, rows int) ([]uint32, uint32) {
	g := make([]uint32, cols*rows)
	add := func(r, c int, n uint32) {
		if r >= 0 && r < rows && c >= 0 && c < cols {
			g[r*cols+c] += n
		}
	}
	hi, lo := rows/10, rows-rows/10-1
	for c := 0; c < cols; c++ {
		for d := -1; d <= 1; d++ {
			add(hi+d, c, uint32(60-20*d*d))
			add(lo+d, c, uint32(60-20*d*d))
		}
	}
	for _, x0 := range []int{cols / 4, 3 * cols / 4} {
		for j := -6; j <= 6; j++ { // jitter: the edges spread over a few columns
			w := uint32(13 - 2*int(math.Abs(float64(j))))
			for r := hi; r <= lo; r++ {
				dx := (r - (hi+lo)/2) * cols / 16 / (lo - hi)
				add(r, x0+j+dx, w)
				add(r, x0+j-dx, w)
			}
		}
	}
	var mx uint32
	for _, v := range g {
		mx = max(mx, v)
	}
	return g, mx
}

// TestGoldenBusEye pins the eye diagram: the density in four shades, the UI
// grid and labels, the opening box, the two cursors, and the figures beside
// a small time trace.
// TRLC-LINKS: REQ-SDS-215
func TestGoldenBusEye(t *testing.T) {
	const cols, rows = 384, 166
	grid, mx := goldenEyeGrid(cols, rows)
	e := &wsview.EyePane{Pane: wsview.Pane{Title: "BUS", Caption: "EYE  UART 31250 8N1 on C1", Right: "212 UI folded from 9 captures",
		Tool:  []wsview.Seg{{Key: "Adjust", Job: "threshold"}, {Key: "push", Job: "clear eye"}, {Key: "Position", Job: "sample point"}, {Key: "push", Job: "centre both"}, {Key: "Zoom", Job: "finer samples"}},
		Again: "F2 again: Packets"},
		Figures: [3][4][2]string{{{"BIT RATE", "31.25 kbit/s"}, {"UI", "32us"}, {"CROSSING", "50 %"}, {"FOLDED", "212 UI"}},
			{{"EYE HEIGHT", "6.4V"}, {"EYE WIDTH", "0.84 UI"}, {"RISE", "120ns"}, {"FALL", "118ns"}},
			{{"TIE RMS", "410ns"}, {"TIE PK-PK", "2.1us"}, {"SAMPLES/UI", "64.0"}, {"dt", "500ns"}}},
		Cursor: "SAMPLE PT +0.00 UI: opening 6.4V     THRESHOLD 0V: opening 0.84 UI",
		Grid:   grid, Cols: cols, Rows: rows, Max: mx,
		Box: [4]int{cols/4 + 14, rows/10 + 3, 3*cols/4 - 14, rows - rows/10 - 4}, HasBox: true,
		SampleCol: cols / 2, ThreshRow: rows / 2, TimeLabel: "TIME"}
	v := wsview.View{WS: wsview.Bus, Layout: wsview.Layout{MenuW: 30, PlotTop: 136, HideTraces: true, HideBottom: true}, Rail: true, Eye: e}
	h := goldenUIHUD(v)
	h.TdivS, h.SampleS = 100e-6, 0.5e-6
	goldenRender(t, "ws_bus_eye", goldenBusFrame(), h)
}

// TestGoldenSpectrumPeaks pins SPECTRUM: the FFT under its pane with the
// peak table, readout, marker line and its label in the plot.
// TRLC-LINKS: REQ-SDS-216
func TestGoldenSpectrumPeaks(t *testing.T) {
	const n = 4096
	f := goldenFrame(n,
		func(i int) float64 {
			th := 2 * math.Pi * float64(i) / n
			return 128 + 70*math.Sin(205*th) + 18*math.Sin(615*th) + 7*math.Sin(1025*th)
		},
		func(i int) float64 { return 128 + 50*math.Sin(2*math.Pi*float64(i)*410/n) })
	f.SampleS = 50e-9
	s := &wsview.SpecPane{Pane: wsview.Pane{Title: "SPECTRUM", Caption: "FFT C1  span 0-10MHz  RBW 4.88kHz  floor -71dBV", Right: "S/DIV: span",
		Tool:  []wsview.Seg{{Key: "Adjust", Job: "peak"}, {Key: "push", Job: "delta"}, {Key: "Position", Job: "fine"}, {Key: "push", Job: "largest peak"}},
		Again: "F3 again: Spectrogram"},
		Head: [4]string{"FREQ", "LEVEL", "VS TOP", "HARM"},
		Rows: []wsview.SpecRow{{Num: 1, Freq: "1MHz", Level: "8.9dBV", Rel: "0dB", Harm: "H1", Sel: true},
			{Num: 2, Freq: "3MHz", Level: "-2.9dBV", Rel: "-11.8dB", Harm: "H3"}, {Num: 3, Freq: "5MHz", Level: "-11.1dBV", Rel: "-20dB", Harm: "H5"}},
		Readout:   []wsview.Span{{Text: "top 1MHz", Col: wsview.Text}, {Text: "    8.9dBV", Col: wsview.Text}, {Text: "THD 27.4% (2h)", Col: wsview.Text}},
		Marker:    "MKR 1MHz  8.9dBV  H1  (largest peak)",
		TimeLabel: "TIME", HasMark: true, MarkHz: 1.001e6, MarkRel: 0, MarkText: "M 1MHz 8.9dBV", BodeIdx: -1, BodeRef: -1}
	v := wsview.View{WS: wsview.Spectrum, Layout: wsview.Layout{MenuW: 30, PlotTop: 150, HideViewCaptions: true, HideBottom: true}, Rail: true, Spec: s}
	h := goldenUIHUD(v)
	h.ViewMode, h.SampleS = 2, 50e-9
	goldenRender(t, "ws_spectrum", f, h)
}

// TestGoldenMeasureStats pins MEASURE: the statistics table, the cursor
// block and the trace below with the time cursors.
// TRLC-LINKS: REQ-SDS-217
func TestGoldenMeasureStats(t *testing.T) {
	m := &wsview.MeasPane{Pane: wsview.Pane{Title: "MEASURE", Caption: "statistics  N 48  over 2.4 s", Right: "Measure key: choose",
		Tool:  []wsview.Seg{{Key: "Adjust", Job: "cursor A"}, {Key: "push", Job: "cursors Volts"}, {Key: "Position", Job: "cursor B"}, {Key: "push", Job: "zero statistics"}},
		Again: "F4 again: All values"},
		Stats: []wsview.StatRow{{Name: "C1 Vpp", Col: wsview.C1, Now: "5.68V", Mean: "5.68V", Min: "5.6V", Max: "5.76V", Sdev: "40mV", N: "48"},
			{Name: "C1 Freq", Col: wsview.C1, Now: "1kHz", Mean: "1kHz", Min: "999Hz", Max: "1kHz", Sdev: "0.4Hz", N: "48"},
			{Name: "C2 Vpp", Col: wsview.C2, Now: "4.8V", Mean: "4.8V", Min: "4.76V", Max: "4.84V", Sdev: "20mV", N: "48"}},
		Cursor: []wsview.Span{{Text: "CURSORS TIME", Col: wsview.Amber}, {Text: "A -1.5ms  B 1.5ms", Col: wsview.Text},
			{Text: "dt 3ms  1/dt 333Hz", Col: wsview.Text}, {Text: "C1 A 2.8V  B -2.8V", Col: wsview.C1}}}
	v := wsview.View{WS: wsview.Measure, Layout: wsview.Layout{MenuW: 30, PlotTop: 142, HideMeas: true}, Rail: true, Meas: m}
	h := goldenUIHUD(v)
	h.CurOn, h.CurType, h.CurSel, h.CurX = true, 0, 0, [2]float64{0.35, 0.65}
	goldenRender(t, "ws_measure", goldenYTFrame(), h)
}

// TestGoldenCompare pins COMPARE: the five rows with the stacking bar on
// MASTER, the selected row's action, and the plot below.
// TRLC-LINKS: REQ-SDS-218
func TestGoldenCompare(t *testing.T) {
	c := &wsview.CmpPane{Pane: wsview.Pane{Title: "COMPARE", Caption: "trigger source C1: averaging",
		Tool:  []wsview.Seg{{Key: "Adjust", Job: "row"}, {Key: "push", Job: "row action"}, {Key: "Position", Job: "trigger position"}, {Key: "push", Job: "centre"}},
		Again: "F5 again: cancel stack"}}
	c.Rows[0] = wsview.CmpRow{Name: "MASTER", Info: "STACK 40/64 of C1", InfoCol: wsview.Amber, Sel: true, Action: "F5: cancel",
		Bar: true, BarN: 40, BarOf: 64, BarNote: "averaging - then REF A + mask, test raw", BarNoteCol: wsview.Dim}
	c.Rows[1] = wsview.CmpRow{Name: "REF A", Info: "C1 1V 500us  shown", Extra: "max|d| 61.9mV  rms 18.4mV  100% in 0.5div"}
	c.Rows[2] = wsview.CmpRow{Name: "REF B", Info: "empty", InfoCol: wsview.Dim}
	c.Rows[3] = wsview.CmpRow{Name: "MASK", Info: "counting", Extra: "pass 91  fail 0  (0.0% fail)"}
	c.Rows[4] = wsview.CmpRow{Name: "MATH", Info: "Off", InfoCol: wsview.Dim}
	v := wsview.View{WS: wsview.Compare, Layout: paneLayout(104), Rail: true, Cmp: c}
	goldenRender(t, "ws_compare", goldenYTFrame(), goldenUIHUD(v))
}

// goldenSheet is the TRIG tab as SCOPE's sheet opens it: the tab strip with
// the workspace's own tabs first, enum rows written out, number rows with
// gauges, a header, an action row, the side panel and the hint line.
// TRLC-LINKS: REQ-SDS-212
func goldenSheet() *wsview.Sheet {
	opts := func(sel int, names ...string) []wsview.Opt {
		var o []wsview.Opt
		for i, n := range names {
			o = append(o, wsview.Opt{Text: n, Sel: i == sel})
		}
		return o
	}
	sh := &wsview.Sheet{WSName: "SCOPE", ThumbView: 0,
		Hint: wsview.Span{Text: "S/DIV: tab   Adjust: row   F1-F5: pick   Trig Menu or Menu On/Off: back to SCOPE   Help: guide", Col: wsview.Dim}}
	for i, n := range []string{"TRIG", "CH1", "CH2", "TIME", "ACQ", "VIEW", "MEAS", "CURS", "MATH/REF", "BUS", "FILE", "SYS"} {
		sh.Tabs = append(sh.Tabs, wsview.Tab{Name: n, Own: i < 5, Active: i == 0})
	}
	sh.Rows = []wsview.Row{
		{Kind: wsview.Enum, Label: "Source/slope", Rel: true, Opts: opts(0, "C1 Rise", "C1 Fall", "C2 Rise", "C2 Fall")},
		{Kind: wsview.Number, Label: "Level", Rel: true, Focus: true, Value: "200mV", Gauge: 0.53},
		{Kind: wsview.Enum, Label: "Mode", Rel: true, Opts: opts(0, "Auto", "Normal")},
		{Kind: wsview.Enum, Label: "Type", Rel: true, Opts: opts(0, "Edge", "Pulse", "Slope", "Video", "Serial")},
		{Kind: wsview.Number, Label: "Holdoff", Rel: true, Value: "Off", Gauge: 0},
		{Kind: wsview.Action, Label: "Force trigger", Rel: true, Acts: []wsview.Act{{Text: "Force now"}}},
		{Kind: wsview.Header, Header: "PULSE  (used when Type = Pulse)"},
		{Kind: wsview.Enum, Label: "Pulse when", Opts: []wsview.Opt{{Text: "Any", Sel: true, Quiet: true}, {Text: "< min"}, {Text: "> max"}, {Text: "In range"}}},
		{Kind: wsview.Number, Label: "Min width", Value: "100ns", Gauge: 0.2},
	}
	sh.Status = []wsview.Span{{Text: "Status: Trig'd", Col: wsview.Dim}}
	sh.Side = wsview.Side{Label: "Level", Value: "200mV", Big: true,
		Lines: []wsview.Span{{Text: "Voltage the trigger source must", Col: wsview.Dim}, {Text: "cross.", Col: wsview.Dim},
			{Text: "Range -6.4V .. 6.4V", Col: wsview.Dim}, {Text: "Also the Level knob (any time);", Col: wsview.Text}},
		Hints: []wsview.Span{{Text: "F1..F5: pick", Col: wsview.Dim}, {Text: "Push Adjust: edit", Col: wsview.Dim}}}
	return sh
}

// TestGoldenSheet pins a full-screen settings sheet with its F-key column
// (Down, Up and presets for a number row) and the live mini view.
// TRLC-LINKS: REQ-SDS-212
func TestGoldenSheet(t *testing.T) {
	keys := &wsview.Keys{Title: "LEVEL", Top: 30, Items: [5]wsview.Key{{Label: "Down", Value: "one step"}, {Label: "Up", Value: "one step"},
		{Label: "0 V"}, {Label: "50 %"}}}
	v := wsview.View{WS: wsview.Scope, Layout: wsview.Layout{MenuW: 116, Sheet: true, HideTraces: true, HideBottom: true}, Sheet: goldenSheet(), Keys: keys}
	goldenRender(t, "ws_sheet", goldenYTFrame(), goldenUIHUD(v))
}

// TestGoldenSheetPicker pins the sheet's byte picker box under the Match
// bytes row (the BUS tab's From screen).
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-214
func TestGoldenSheetPicker(t *testing.T) {
	sh := &wsview.Sheet{WSName: "BUS", Hint: wsview.Span{Text: "Menu On/Off: close and discard   S/DIV: other tab (discard)", Col: wsview.Dim}}
	for i, n := range []string{"BUS", "TRIG", "TIME", "CH1", "CH2", "ACQ", "VIEW", "MEAS", "CURS", "MATH/REF", "FILE", "SYS"} {
		sh.Tabs = append(sh.Tabs, wsview.Tab{Name: n, Own: i < 3, Active: i == 0})
	}
	sh.Rows = []wsview.Row{
		{Kind: wsview.Enum, Label: "Protocol", Rel: true, Opts: []wsview.Opt{{Text: "Off"}, {Text: "Auto"}, {Text: "UART", Sel: true}, {Text: "I2C"}, {Text: "SPI"}}},
		{Kind: wsview.Header, Header: "SERIAL TRIGGER"},
		{Kind: wsview.Bytes, Label: "Match bytes", Rel: true, Focus: true, Value: "55 AA", Armed: true},
		{Kind: wsview.Enum, Label: "Trigger on it", Rel: true, Opts: []wsview.Opt{{Text: "Off"}, {Text: "On", Sel: true}}},
	}
	var toks []wsview.PickTok
	for k := 0; k < 4; k++ {
		for i, b := range []string{"48", "69", "55", "AA"} {
			toks = append(toks, wsview.PickTok{Text: b, Start: i == 0})
		}
	}
	sh.Sub = &wsview.Sub{Title: "PICK FROM SCREEN  (16 bytes decoded)", Toks: toks, Cur: 6,
		Lines: []wsview.Span{{Text: "| = idle gap (message start).  Underlined: the next bytes.", Col: wsview.Dim},
			{Text: "F5 or push Adjust: cancel.   Now: 55 AA", Col: wsview.Text}}}
	sh.Side = wsview.Side{Title: "PICK FROM SCREEN", Lines: []wsview.Span{{Text: "Bytes decoded on screen,", Col: wsview.Text}}}
	keys := &wsview.Keys{Title: "PICK BYTES", Top: 30, Items: [5]wsview.Key{{Label: "Trig on 1", Value: "55"}, {Label: "Trig on 2", Value: "55 AA"},
		{Label: "Trig on 3", Value: "55 AA 48"}, {Label: "Trig on 4", Value: "55 AA 48 69"}, {Label: "Cancel", Value: "keep the match"}}}
	v := wsview.View{WS: wsview.Bus, Layout: wsview.Layout{MenuW: 116, Sheet: true, HideTraces: true, HideBottom: true}, Sheet: sh, Keys: keys}
	h := goldenUIHUD(v)
	h.TdivS = 100e-6
	goldenRender(t, "ws_sheet_picker", goldenBusFrame(), h)
}

// TestGoldenMatchEditor pins the serial match keyboard over the sheet with
// its F-key column.
// TRLC-LINKS: REQ-SDS-214
func TestGoldenMatchEditor(t *testing.T) {
	keys := &wsview.Keys{Title: "MATCH EDITOR", Top: 30, Items: [5]wsview.Key{{Label: "Keys", Value: "Hex"}, {Label: "Type", Value: "A"},
		{Label: "Delete", Value: "<"}, {Label: "OK", Value: "use these bytes"}, {Label: "Cancel", Value: "keep the match"}}}
	v := wsview.View{WS: wsview.Bus, Layout: wsview.Layout{MenuW: 116, Sheet: true, HideTraces: true, HideBottom: true}, Sheet: &wsview.Sheet{WSName: "BUS"}, Keys: keys}
	h := goldenUIHUD(v)
	h.TdivS = 100e-6
	h.Match = MatchView{Open: true, Set: "Hex", Proto: "UART", Toks: []string{"55", "A?", "ERR"}, Cur: 3,
		Keys: [][]string{{"0", "1", "2", "3", "4", "5"}, {"6", "7", "8", "9", "A", "B"}, {"C", "D", "E", "F", "?", "??"}}, Row: 1, Col: 4}
	goldenRender(t, "ws_match_editor", goldenBusFrame(), h)
}
