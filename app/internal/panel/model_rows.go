// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// The sheet tabs and their rows (REQ-SDS-212): every setting the instrument
// has, in Settings Sheet's tabs. Where the prototype's catalogue differs from
// the instrument, the instrument wins: no trigger coupling and no fine V/div
// (the hardware has neither), and the real extras are rows too (pulse level,
// ETS, memory depth, roll mode, super-res channel and gate, decode stream,
// the extra decoders, mask frames and tolerance, the master).

// Tab indexes, in the standard order.
const (
	tabTrig = iota
	tabCh1
	tabCh2
	tabTime
	tabAcq
	tabView
	tabMeas
	tabCurs
	tabCmp
	tabBus
	tabFile
	tabSys
	numTabs
)

var onOff = []opt{{0, "Off"}, {1, "On"}}
var chans = []opt{{0, "CH1"}, {1, "CH2"}}

// TRLC-LINKS: REQ-SDS-212
func staticOpts(o []opt) func(*sv) []opt { return func(*sv) []opt { return o } }

// measParam is one measurement parameter.
type measParam struct{ id, label, short, unit string }

// measParams are the parameters the MEAS tab offers (measure.Result's set).
// Saved setups hold a choice by its index here: the order stays. The short
// names are the web's (MEAS_ALL), as both surfaces' tables show them.
var measParams = []measParam{
	{"freq", "Frequency", "Freq", "Hz"}, {"pkpk", "Peak-peak", "Vpp", "V"}, {"period", "Period", "Period", "s"},
	{"duty", "Duty", "Duty", "%"}, {"rise", "Rise", "Rise", "s"}, {"fall", "Fall", "Fall", "s"},
	{"pwid", "+Width", "+Width", "s"}, {"nwid", "-Width", "-Width", "s"}, {"ampl", "Amplitude", "Vampl", "V"},
	{"max", "Maximum", "Vmax", "V"}, {"min", "Minimum", "Vmin", "V"}, {"top", "Top", "Vtop", "V"},
	{"base", "Base", "Vbase", "V"}, {"mean", "Mean", "Vmean", "V"}, {"rms", "RMS", "Vrms", "V"},
	{"acrms", "AC RMS", "ACrms", "V"}, {"ovsh", "Overshoot", "Overshoot", "%"},
	{"prsh", "Preshoot", "Preshoot", "%"}, // last: saved setups keep the indexes before it
}

// measAllOrder is the all-values view's order, the web's (MEAS_ALL: its
// MEAS_CORE, then MEAS_MORE; measAllSplit rows in the first column).
var measAllOrder = []string{"pkpk", "max", "min", "mean", "rms", "freq", "period", "duty",
	"acrms", "top", "base", "ampl", "rise", "fall", "pwid", "nwid", "ovsh", "prsh"}

// measAllSplit is the first column's row count in the all-values view: half
// of the 18, as on the web.
const measAllSplit = 9

// measIndex is a parameter's index in measParams (-1 unknown).
// TRLC-LINKS: REQ-SDS-212
func measIndex(id string) int {
	for i, p := range measParams {
		if p.id == id {
			return i
		}
	}
	return -1
}

// Measurement parameter indexes used by the workspaces.
const (
	mFreq = 0
	mPkpk = 1
)

var sheetTabs [numTabs]*tab
var rowByID = map[string]*row{}
var rowTab = map[string]int{}

// TRLC-LINKS: REQ-SDS-212
func init() {
	sheetTabs = [numTabs]*tab{
		{id: "trig", name: "TRIG", title: "Trigger", key: "Trig Menu", rows: trigRows()},
		{id: "ch1", name: "CH1", title: "Channel 1", key: "1", rows: chanRows(0)},
		{id: "ch2", name: "CH2", title: "Channel 2", key: "2", rows: chanRows(1)},
		{id: "time", name: "TIME", title: "Horizontal", key: "Hori Menu", rows: timeRows()},
		{id: "acq", name: "ACQ", title: "Acquire", key: "Acquire", rows: acqRows()},
		{id: "view", name: "VIEW", title: "Display", key: "Display", rows: viewRows()},
		{id: "meas", name: "MEAS", title: "Measure", key: "Measure", rows: measRows(), home: 2},
		{id: "curs", name: "CURS", title: "Cursors", key: "Cursors", rows: cursRows()},
		{id: "cmp", name: "MATH/REF", title: "Math, references, mask, master", key: "Math or Ref", rows: cmpRows()},
		{id: "bus", name: "BUS", title: "Bus decode, serial trigger", key: "Utility", rows: busTabRows()},
		{id: "file", name: "FILE", title: "Setups", key: "Save/Recall", rows: fileRows()},
		{id: "sys", name: "SYS", title: "System and help", key: "Help", rows: sysRows()},
	}
	for ti, t := range sheetTabs {
		for _, r := range t.rows {
			if _, dup := rowByID[r.id]; dup {
				panic("panel: duplicate row " + r.id)
			}
			rowByID[r.id], rowTab[r.id] = r, ti
		}
	}
}

// tabIndex finds a tab by id.
// TRLC-LINKS: REQ-SDS-212
func tabIndex(id string) int {
	for i, t := range sheetTabs {
		if t.id == id {
			return i
		}
	}
	return -1
}

// rowIndex is r's index in its tab.
// TRLC-LINKS: REQ-SDS-212
func rowIndex(id string) int {
	for i, r := range sheetTabs[rowTab[id]].rows {
		if r.id == id {
			return i
		}
	}
	return 0
}

// ---- helpers ----

// TRLC-LINKS: REQ-SDS-212
func notSerial(s *sv) bool { return s.st.SerialMode == 0 }

// TRLC-LINKS: REQ-SDS-212
func trigTypeIs(t int) func(*sv) bool {
	return func(s *sv) bool { return s.st.SerialMode == 0 && s.st.TrigType == t }
}

// numRow builds a continuous number row.
// TRLC-LINKS: REQ-SDS-212
func numRow(id, label, unit string, val func(*sv) float64, lim func(*sv) (float64, float64), step func(*sv) float64,
	setN func(*Controller, float64) error, fmtN func(float64) string) *row {
	r := &row{id: id, label: label, kind: wsview.Number, unit: unit, val: val, lim: lim, step: step, setN: setN, fmtN: fmtN}
	r.stepN = func(c *Controller, n int) error {
		s := c.snap()
		lo, hi := r.lim(s)
		return r.setN(c, clampF(r.val(s)+float64(n)*r.step(s), lo, hi))
	}
	return r
}

// TRLC-LINKS: REQ-SDS-212
func constStep(v float64) func(*sv) float64 { return func(*sv) float64 { return v } }

// TRLC-LINKS: REQ-SDS-212
func constLim(lo, hi float64) func(*sv) (float64, float64) {
	return func(*sv) (float64, float64) { return lo, hi }
}

// TRLC-LINKS: REQ-SDS-212
func siFmt(unit string) func(float64) string { return func(v float64) string { return si(v, unit) } }

// nsLadder is the 1-2-5 ladder of pulse widths and slope times (10 ns..10 s),
// what the old qualifier page stepped through.
// TRLC-LINKS: REQ-SDS-212
func nsLadderStep(cur float64, n int) float64 {
	for ; n > 0; n-- {
		cur = stepNs(cur, 1, false)
	}
	for ; n < 0; n++ {
		cur = stepNs(cur, -1, false)
	}
	return cur
}

// timeQualRow is a pulse-width or slope-time row: seconds, 10 ns..10 s,
// stepped on the 1-2-5 ladder, typed values exact.
// TRLC-LINKS: REQ-SDS-212
func timeQualRow(id, label string, val func(*sv) float64, setNs func(*Controller, float64) error) *row {
	r := &row{id: id, label: label, kind: wsview.Number, unit: "s", fmtN: siFmt("s"),
		val:  func(s *sv) float64 { return val(s) * 1e-9 },
		lim:  constLim(10e-9, 10),
		step: constStep(0),
		setN: func(c *Controller, v float64) error { return setNs(c, v*1e9) },
	}
	r.stepN = func(c *Controller, n int) error { return setNs(c, nsLadderStep(val(c.snap()), n)) }
	r.ladder = func(*sv) []float64 {
		var l []float64
		for d := 10e-9; d <= 10.0001; d *= 10 {
			l = append(l, d, 2*d, 5*d)
		}
		return l[:len(l)-2]
	}
	return r
}

// ---- TRIG ----

var condOpts = []opt{{0, "Any"}, {1, "< min"}, {2, "> max"}, {3, "In range"}}

// holdoffLadder is the holdoff the knob and Down/Up step through.
// holdoffLadder is Off, then 1-2-5 steps up to the row's 10 s, so 200 ms or
// 5 s are reached by the knob, not only typed.
var holdoffLadder = []float64{0, 100e-6, 200e-6, 500e-6, 1e-3, 2e-3, 5e-3, 10e-3, 20e-3, 50e-3, 100e-3, 200e-3, 500e-3, 1, 2, 5, 10}

// TRLC-LINKS: REQ-SDS-212
func trigRows() []*row {
	edge := &row{id: "trig.edge", label: "Source/slope", kind: wsview.Enum,
		help: "Channel the trigger watches and its edge in one choice. Pulse trigger: Rise = positive pulse.",
		knob: "The Level knob moves the level; its push = 0 V.",
		rel:  notSerial, why: "Type other than Serial (Serial uses the BUS lines)",
		opts: staticOpts([]opt{{0, "C1 Rise"}, {1, "C1 Fall"}, {2, "C2 Rise"}, {3, "C2 Fall"}}),
		get:  func(s *sv) int { return (s.st.TrigSource&1)*2 + b2ic(!s.st.TrigRising) },
		set: func(c *Controller, v int) error {
			c.eng.SetTrigSource(v / 2)
			c.eng.SetTrigSlope(v%2 == 0)
			return nil
		},
	}
	edge.def = func(c *Controller) error { return edge.set(c, 0) }

	level := numRow("trig.level", "Level", "V", func(s *sv) float64 { return s.trigV },
		func(s *sv) (float64, float64) { return s.trigLo, s.trigHi }, func(s *sv) float64 { return s.trigStep },
		func(c *Controller, v float64) error { return c.setTrigLevelV(v) }, siFmt("V"))
	level.help = "Voltage the trigger source must cross."
	level.knob = "Also the Level knob (any time); its push = 0 V."
	level.rel, level.why = notSerial, "Type other than Serial"
	level.stepN = func(c *Controller, n int) error { return c.stepTrigLevel(n) }
	level.presets = func(*sv) []preset {
		return []preset{{t: "0 V", v: 0}, {t: "50 %", run: func(c *Controller) error { c.setTrigMid(); return nil }}}
	}
	level.def = func(c *Controller) error { return c.zeroTrigLevel() }

	mode := &row{id: "trig.mode", label: "Mode", kind: wsview.Enum,
		help: "Auto free-runs when nothing triggers; Normal waits for a trigger.",
		knob: "Single also switches to Normal.",
		opts: staticOpts([]opt{{0, "Auto"}, {1, "Normal"}}),
		get:  func(s *sv) int { return b2ic(s.st.Norm) },
		set:  func(c *Controller, v int) error { return c.setNorm(v == 1) },
		def:  func(c *Controller) error { return c.setNorm(false) },
	}
	typ := &row{id: "trig.type", label: "Type", kind: wsview.Enum,
		help: "What triggers: an edge, a pulse width, an edge speed, a video line or decoded bus bytes.",
		knob: "Serial uses the bytes and bus of the BUS tab (or pick them in the BUS workspace).",
		opts: staticOpts([]opt{{0, "Edge"}, {1, "Pulse"}, {2, "Slope"}, {3, "Video"}, {4, "Serial"}}),
		get:  func(s *sv) int { return trigTypeIndex(s.st) },
		set:  func(c *Controller, v int) error { return c.setTrigType(v) },
		def:  func(c *Controller) error { return c.setTrigType(0) },
	}
	holdoff := &row{id: "trig.holdoff", label: "Holdoff", kind: wsview.Number, unit: "s",
		help: "Minimum time between triggers; steadies bursts.",
		val:  func(s *sv) float64 { return s.st.HoldoffS },
		lim:  constLim(0, 10), step: constStep(0),
		setN: func(c *Controller, v float64) error { return c.setHoldoff(v) },
		fmtN: func(v float64) string {
			if v == 0 {
				return "Off"
			}
			return si(v, "s") // a negative one typed reads as itself in the refusal
		},
		ladder: func(*sv) []float64 { return holdoffLadder },
		presets: func(*sv) []preset {
			return []preset{{t: "Off", v: 0}, {t: "100 us", v: 100e-6}, {t: "1 ms", v: 1e-3}}
		},
		def: func(c *Controller) error { return c.setHoldoff(0) },
	}
	holdoff.stepN = func(c *Controller, n int) error {
		v := c.eng.Snapshot().HoldoffS
		for ; n > 0; n-- {
			v = nextHoldoff(v, 1, false)
		}
		for ; n < 0; n++ {
			v = nextHoldoff(v, -1, false)
		}
		return c.setHoldoff(v)
	}
	force := &row{id: "trig.force", label: "Force trigger", kind: wsview.Action,
		help: "Trigger once now (also the Force key).",
		acts: func(*sv) []act { return []act{{id: "force", t: "Force now", run: (*Controller).force}} },
	}

	isPulse, isSlope, isVideo := trigTypeIs(1), trigTypeIs(2), trigTypeIs(3)
	pulseWhen := &row{id: "trig.pulse_when", label: "Pulse when", kind: wsview.Enum, hdr: "PULSE  (used when Type = Pulse)",
		help: "Which pulse widths trigger.", rel: isPulse, why: "Type = Pulse", opts: staticOpts(condOpts),
		get: func(s *sv) int { return int(s.pulse[3]) & 3 },
		set: func(c *Controller, v int) error { return c.editPulse(func() { c.pulseCond = v & 3 }, false) },
		def: func(c *Controller) error { return c.editPulse(func() { c.pulseCond = 0 }, false) },
	}
	pulseLevel := numRow("trig.pulse_level", "Pulse level", "%", func(s *sv) float64 { return math.Round(s.pulse[0] * 100) },
		constLim(5, 95), constStep(5),
		func(c *Controller, v float64) error { return c.editPulse(func() { c.pulseLvl = v / 100 }, false) }, fmtPct)
	pulseLevel.help, pulseLevel.rel, pulseLevel.why = "The level the pulse width is measured at, % of the swing.", isPulse, "Type = Pulse"
	pulseLevel.def = func(c *Controller) error { return pulseLevel.setN(c, 50) }
	pulseMin := timeQualRow("trig.pulse_min", "Min width", func(s *sv) float64 { return s.pulse[1] },
		func(c *Controller, ns float64) error { return c.editPulse(func() { c.pulseMin = ns }, false) })
	pulseMin.help, pulseMin.rel, pulseMin.why = "Lower pulse-width limit.", isPulse, "Type = Pulse"
	pulseMin.presets = func(*sv) []preset {
		return []preset{{t: "100 ns", v: 100e-9}, {t: "1 us", v: 1e-6}, {t: "10 us", v: 10e-6}}
	}
	pulseMin.def = func(c *Controller) error { return pulseMin.setN(c, 100e-9) }
	pulseMax := timeQualRow("trig.pulse_max", "Max width", func(s *sv) float64 { return s.pulse[2] },
		func(c *Controller, ns float64) error { return c.editPulse(func() { c.pulseMax = ns }, true) })
	pulseMax.help, pulseMax.rel, pulseMax.why = "Upper pulse-width limit.", isPulse, "Type = Pulse"
	pulseMax.presets = func(*sv) []preset {
		return []preset{{t: "1 us", v: 1e-6}, {t: "10 us", v: 10e-6}, {t: "100 us", v: 100e-6}}
	}
	pulseMax.def = func(c *Controller) error { return pulseMax.setN(c, 1e-6) }

	slopeWhen := &row{id: "trig.slope_when", label: "Edge time when", kind: wsview.Enum, hdr: "SLOPE  (used when Type = Slope)",
		help: "Which transition times trigger.", rel: isSlope, why: "Type = Slope", opts: staticOpts(condOpts),
		get: func(s *sv) int { return int(s.slope[4]) & 3 },
		set: func(c *Controller, v int) error { return c.editSlope(func() { c.slopeCond = v & 3 }, false, false) },
		def: func(c *Controller) error { return c.editSlope(func() { c.slopeCond = 0 }, false, false) },
	}
	slopeMin := timeQualRow("trig.slope_min", "Min time", func(s *sv) float64 { return s.slope[2] },
		func(c *Controller, ns float64) error { return c.editSlope(func() { c.slopeMin = ns }, false, false) })
	slopeMin.help, slopeMin.rel, slopeMin.why = "Lower transition-time limit.", isSlope, "Type = Slope"
	slopeMin.presets = func(*sv) []preset {
		return []preset{{t: "10 ns", v: 10e-9}, {t: "100 ns", v: 100e-9}, {t: "1 us", v: 1e-6}}
	}
	slopeMin.def = func(c *Controller) error { return slopeMin.setN(c, 100e-9) }
	slopeMax := timeQualRow("trig.slope_max", "Max time", func(s *sv) float64 { return s.slope[3] },
		func(c *Controller, ns float64) error { return c.editSlope(func() { c.slopeMax = ns }, false, true) })
	slopeMax.help, slopeMax.rel, slopeMax.why = "Upper transition-time limit.", isSlope, "Type = Slope"
	slopeMax.presets = func(*sv) []preset {
		return []preset{{t: "20 ns", v: 20e-9}, {t: "200 ns", v: 200e-9}, {t: "2 us", v: 2e-6}}
	}
	slopeMax.def = func(c *Controller) error { return slopeMax.setN(c, 1e-6) }
	slopeLow := numRow("trig.slope_low", "Low level", "%", func(s *sv) float64 { return math.Round(s.slope[0] * 100) },
		constLim(5, 95), constStep(5),
		func(c *Controller, v float64) error { return c.editSlope(func() { c.slopeLo = v / 100 }, false, false) }, fmtPct)
	slopeLow.help, slopeLow.rel, slopeLow.why = "Lower threshold, % of the signal swing.", isSlope, "Type = Slope"
	slopeLow.presets = func(*sv) []preset { return []preset{{t: "10 %", v: 10}, {t: "20 %", v: 20}, {t: "30 %", v: 30}} }
	slopeLow.def = func(c *Controller) error { return slopeLow.setN(c, 20) }
	slopeHigh := numRow("trig.slope_high", "High level", "%", func(s *sv) float64 { return math.Round(s.slope[1] * 100) },
		constLim(5, 95), constStep(5),
		func(c *Controller, v float64) error { return c.editSlope(func() { c.slopeHi = v / 100 }, true, false) }, fmtPct)
	slopeHigh.help, slopeHigh.rel, slopeHigh.why = "Upper threshold, % of the signal swing.", isSlope, "Type = Slope"
	slopeHigh.presets = func(*sv) []preset { return []preset{{t: "70 %", v: 70}, {t: "80 %", v: 80}, {t: "90 %", v: 90}} }
	slopeHigh.def = func(c *Controller) error { return slopeHigh.setN(c, 80) }

	videoStd := &row{id: "trig.video_std", label: "Standard", kind: wsview.Enum, hdr: "VIDEO  (used when Type = Video)",
		help: "Video standard of the input.", rel: isVideo, why: "Type = Video",
		opts: staticOpts([]opt{{0, "PAL"}, {1, "NTSC"}}),
		get:  func(s *sv) int { return s.video[0] & 1 },
		set:  func(c *Controller, v int) error { return c.editVideo(func() { c.videoStd = v & 1 }) },
		def:  func(c *Controller) error { return c.editVideo(func() { c.videoStd = 0 }) },
	}
	videoLine := numRow("trig.video_line", "Line", "", func(s *sv) float64 { return float64(s.video[1]) },
		constLim(0, 625), constStep(1),
		func(c *Controller, v float64) error { return c.editVideo(func() { c.videoLine = int(math.Round(v)) }) },
		func(v float64) string { return strconv.Itoa(int(math.Round(v))) })
	videoLine.help, videoLine.rel, videoLine.why = "Video line to trigger on.", isVideo, "Type = Video"
	videoLine.presets = func(*sv) []preset { return []preset{{t: "1", v: 1}, {t: "10", v: 10}, {t: "100", v: 100}} }
	videoLine.def = func(c *Controller) error { return videoLine.setN(c, 0) }
	videoSync := &row{id: "trig.video_sync", label: "Sync", kind: wsview.Enum,
		help: "Sync pulse polarity.", rel: isVideo, why: "Type = Video",
		opts: staticOpts([]opt{{0, "Positive"}, {1, "Negative"}}),
		get:  func(s *sv) int { return s.video[2] },
		set:  func(c *Controller, v int) error { return c.editVideo(func() { c.videoNeg = v == 1 }) },
		def:  func(c *Controller) error { return c.editVideo(func() { c.videoNeg = true }) },
	}
	return []*row{edge, level, mode, typ, holdoff, force, pulseWhen, pulseLevel, pulseMin, pulseMax,
		slopeWhen, slopeMin, slopeMax, slopeLow, slopeHigh, videoStd, videoLine, videoSync}
}

// ---- CH1 / CH2 ----

// TRLC-LINKS: REQ-SDS-212
func chanRows(ch int) []*row {
	p := fmt.Sprintf("ch%d.", ch+1)
	C := fmt.Sprintf("CH%d", ch+1)
	hasFE := func(s *sv) bool { return s.fe }
	coupling := &row{id: p + "coupling", label: "Coupling", kind: wsview.Enum,
		help: "DC, AC (blocks DC) or ground.", rel: hasFE, why: "an analog front end",
		opts: staticOpts([]opt{{analog.CplDC, "DC"}, {analog.CplAC, "AC"}, {analog.CplGND, "GND"}}),
		get:  func(s *sv) int { return s.cpl[ch] },
		set:  func(c *Controller, v int) error { return c.setCoupling(ch, v) },
		def:  func(c *Controller) error { return c.setCoupling(ch, analog.CplDC) },
	}
	probe := &row{id: p + "probe", label: "Probe", kind: wsview.Enum,
		help: "Probe attenuation; scales every reading.", knob: "Readouts, V/div and offset scale with the probe.",
		rel: hasFE, why: "an analog front end",
		opts: staticOpts([]opt{{1, "1x"}, {10, "10x"}, {100, "100x"}}),
		get:  func(s *sv) int { return int(s.probe[ch] + 0.5) },
		set:  func(c *Controller, v int) error { return c.setProbe(ch, v) },
		def:  func(c *Controller) error { return c.setProbe(ch, 1) },
	}
	vdiv := &row{id: p + "vdiv", label: "V/div", kind: wsview.Number, unit: "V",
		help: "Vertical scale of the channel.", knob: "Also the " + C + " V/DIV knob (clockwise toward mV).",
		rel: hasFE, why: "an analog front end",
		val:  func(s *sv) float64 { return s.vdivTip(ch) },
		fmtN: siFmt("V"),
		lim: func(s *sv) (float64, float64) {
			return analog.Detents[0].VdivV * s.probe[ch], analog.Detents[len(analog.Detents)-1].VdivV * s.probe[ch]
		},
		step: constStep(0),
		ladder: func(s *sv) []float64 {
			l := make([]float64, len(analog.Detents))
			for i, d := range analog.Detents {
				l[i] = d.VdivV * s.probe[ch]
			}
			return l
		},
		stepN: func(c *Controller, n int) error { return c.stepVdiv(ch, n) },
		def:   func(c *Controller) error { return c.setVdivIdx(ch, analog.BootDetent) },
	}
	vdiv.setN = func(c *Controller, v float64) error { return c.setVdivIdx(ch, nearestLadder(vdiv.ladder(c.snap()), v)) }
	offset := &row{id: p + "offset", label: "Offset", kind: wsview.Number, unit: "V",
		help: "Vertical position of the channel (volts).", knob: "Also the " + C + " Position knob; its push = 0 V.",
		rel: hasFE, why: "an analog front end",
		val:  func(s *sv) float64 { return s.offV[ch] * s.probe[ch] },
		fmtN: func(v float64) string { return sgnSI(v, "V") },
		lim: func(s *sv) (float64, float64) {
			r := offsetRangeV(s.vidx[ch]) * s.probe[ch]
			return -r, r
		},
		step:    func(s *sv) float64 { return panelOffsetStepDiv * s.vdivTip(ch) },
		setN:    func(c *Controller, v float64) error { return c.setOffsetTip(ch, v) },
		stepN:   func(c *Controller, n int) error { return c.stepOffset(ch, n) },
		presets: func(*sv) []preset { return []preset{{t: "0 V", v: 0}} },
		def:     func(c *Controller) error { return c.setOffsetTip(ch, 0) },
	}
	bwl := &row{id: p + "bwl", label: "BW limit 20M", kind: wsview.Enum,
		help: "Bandwidth limit: smoother, less noise.",
		rel:  func(s *sv) bool { return s.hasBWL }, why: "a front end with the 20 MHz limit",
		opts: staticOpts(onOff),
		get:  func(s *sv) int { return b2ic(s.bwl[ch]) },
		set:  func(c *Controller, v int) error { return c.setBWL(ch, v == 1) },
		def:  func(c *Controller) error { return c.setBWL(ch, false) },
	}
	invert := &row{id: p + "invert", label: "Invert", kind: wsview.Enum,
		help: "Display the channel upside down (the measurements keep the true polarity).",
		rel:  func(s *sv) bool { return s.hasInv }, why: "the display invert",
		opts: staticOpts(onOff),
		get:  func(s *sv) int { return b2ic(s.inv[ch]) },
		set:  func(c *Controller, v int) error { return c.setInvert(ch, v == 1) },
		def:  func(c *Controller) error { return c.setInvert(ch, false) },
	}
	on := &row{id: p + "on", label: "Channel", kind: wsview.Enum,
		help: "Show or hide the channel's trace.", knob: "The " + fmt.Sprint(ch+1) + " key opens this tab and switches the channel on.",
		opts: staticOpts(onOff),
		get:  func(s *sv) int { return b2ic(s.chDisp[ch]) },
		set:  func(c *Controller, v int) error { return c.setChanShown(ch, v == 1) },
		def:  func(c *Controller) error { return c.setChanShown(ch, true) },
	}
	return []*row{coupling, probe, vdiv, offset, bwl, invert, on}
}

// ---- TIME ----

// TRLC-LINKS: REQ-SDS-212
func timeRows() []*row {
	tdiv := &row{id: "time.tdiv", label: "Time/div", kind: wsview.Number, unit: "s",
		help: "Seconds per horizontal division.", knob: "No sheet open: the S/DIV knob (clockwise toward nS).",
		val:    func(s *sv) float64 { return s.st.TdivSet() }, // asked for: the engine takes it at its next capture
		fmtN:   siFmt("s"),
		step:   constStep(0),
		ladder: func(s *sv) []float64 { return s.tdivs },
		lim: func(s *sv) (float64, float64) {
			if len(s.tdivs) == 0 {
				return 0, 0
			}
			return s.tdivs[0], s.tdivs[len(s.tdivs)-1]
		},
		stepN: func(c *Controller, n int) error { return c.stepTdiv(n) },
	}
	tdiv.setN = func(c *Controller, v float64) error { return c.setTdivIdx(nearestLadder(c.tdivs, v)) }
	tdiv.def = func(c *Controller) error { return tdiv.setN(c, 500e-6) }
	pos := numRow("time.trig_pos", "Trigger pos", "%", func(s *sv) float64 {
		f := s.st.TrigPosFrac
		if !(f > 0 && f <= 1) {
			f = 0.5
		}
		return math.Round(f * 100)
	}, constLim(2, 100), constStep(1), func(c *Controller, v float64) error { return c.setTrigPos(v / 100) }, fmtPct)
	pos.help = "Where the trigger sits across the screen (50 % = centre)."
	pos.knob = "SCOPE and COMPARE: the Position knob; its push = centre."
	pos.presets = func(*sv) []preset {
		return []preset{{t: "Centre", run: (*Controller).centreTrigger}, {t: "10 %", v: 10}, {t: "90 %", v: 90}}
	}
	pos.def = (*Controller).centreTrigger
	zoom := &row{id: "time.zoom", label: "Zoom", kind: wsview.Enum,
		help: "Magnify a window of the record.",
		knob: "The S/DIV push (Zoom) toggles the zoom; S/DIV then sets the factor (clockwise = more zoom).",
		opts: staticOpts([]opt{{1, "Off"}, {2, "2x"}, {5, "5x"}, {10, "10x"}, {20, "20x"}, {50, "50x"}}),
		get:  func(s *sv) int { return max(s.zoom, 1) },
		set:  func(c *Controller, v int) error { return c.setZoom(v) },
		def:  func(c *Controller) error { return c.setZoom(1) },
	}
	pan := numRow("time.zoom_pos", "Zoom pos", "%", func(s *sv) float64 { return math.Round(s.zoomOff*1000) / 10 },
		constLim(-50, 50), constStep(2), func(c *Controller, v float64) error { return c.setZoomOff(v / 100) },
		func(v float64) string { return fmtPct(math.Round(v*10) / 10) })
	pan.help = "Move the zoom window along the record (% of the record from the trigger)."
	pan.knob = "Zoomed: the Position knob (SCOPE, COMPARE); BUS moves it packet to packet."
	pan.rel, pan.why = func(s *sv) bool { return s.zoom > 1 }, "Zoom on"
	pan.presets = func(*sv) []preset { return []preset{{t: "On trigger", v: 0}} }
	pan.def = func(c *Controller) error { return c.setZoomOff(0) }
	roll := &row{id: "time.roll", label: "Roll mode", kind: wsview.Enum,
		help: "How the trace scrolls at 10 ms/div and slower: chunked (instant) or a gap-free stream.",
		rel:  func(s *sv) bool { return s.st.TdivSet() >= engine.RollMinTdiv*0.999 }, why: "Time/div 10 ms or slower",
		opts: staticOpts([]opt{{0, "Chunked"}, {1, "Stream"}}),
		get:  func(s *sv) int { return b2ic(s.st.RollStream) },
		set:  func(c *Controller, v int) error { return c.setRollStream(v == 1) },
		def:  func(c *Controller) error { return c.setRollStream(false) },
	}
	return []*row{tdiv, pos, zoom, pan, roll}
}

// ---- ACQ ----

// directOnly runs a setting the SRAM image does not have (ETS, the
// decimated memory depth) only on the direct-capture image; on SRAM it
// refuses without an engine command.
// TRLC-LINKS: REQ-SDS-212
func directOnly(c *Controller, set func()) error {
	if c.eng.Snapshot().BandKind == "sram" {
		return fmt.Errorf("not on the SRAM capture image")
	}
	set()
	return nil
}

// TRLC-LINKS: REQ-SDS-212
func acqRows() []*row {
	isSRAM := func(s *sv) bool { return s.st.BandKind == "sram" }
	mode := &row{id: "acq.mode", label: "Mode", kind: wsview.Enum,
		help: "Normal, averaging, enhanced resolution, peak detect or precision.",
		opts: func(s *sv) []opt {
			o := []opt{{0, "Normal"}, {1, "Average"}, {2, "ERes"}, {3, "Peak"}}
			if isSRAM(s) {
				o = append(o, opt{4, "Precision"})
			}
			return o
		},
		get: func(s *sv) int { return s.st.AcqMode },
		set: func(c *Controller, v int) error { return c.setAcqMode(v) },
		def: func(c *Controller) error { return c.setAcqMode(0) },
	}
	avg := &row{id: "acq.averages", label: "Averages", kind: wsview.Enum,
		help: "Waveforms averaged; picking a count also selects Average.",
		knob: "Picking a count sets Mode = Average. 256: Position < >.",
		rel:  func(s *sv) bool { return s.st.AcqMode == engine.AcqAverage }, why: "Mode = Average (picking a count sets it)",
		opts:  staticOpts([]opt{{4, "4"}, {16, "16"}, {32, "32"}, {64, "64"}, {128, "128"}, {256, "256"}}),
		get:   func(s *sv) int { return s.st.AvgCount },
		quick: []int{4, 16, 32, 64, 128},
		set: func(c *Controller, v int) error {
			c.eng.SetAvgCount(v)
			if c.eng.Snapshot().AcqMode != engine.AcqAverage {
				c.eng.SetAcqMode(engine.AcqAverage)
			}
			return nil
		},
		def: func(c *Controller) error { c.eng.SetAvgCount(16); return nil }, // the count only, not the mode
	}
	eres := &row{id: "acq.eres", label: "ERes length", kind: wsview.Enum,
		help: "Enhanced-resolution filter length; picking a length also selects ERes.",
		knob: "Picking a length sets Mode = ERes. 63: Position < >.",
		rel:  func(s *sv) bool { return s.st.AcqMode == 2 }, why: "Mode = ERes (picking a length sets it)",
		opts:  staticOpts([]opt{{1, "1"}, {3, "3"}, {7, "7"}, {15, "15"}, {31, "31"}, {63, "63"}}),
		get:   func(s *sv) int { return s.st.EresLen },
		quick: []int{1, 3, 7, 15, 31},
		set: func(c *Controller, v int) error {
			c.eng.SetEresLen(v)
			if c.eng.Snapshot().AcqMode != 2 {
				c.eng.SetAcqMode(2)
			}
			return nil
		},
		def: func(c *Controller) error { c.eng.SetEresLen(1); return nil },
	}
	ets := &row{id: "acq.ets", label: "ETS", kind: wsview.Enum,
		help: "Equivalent-time sampling of repetitive signals on the fastest timebases.",
		rel:  func(s *sv) bool { return !isSRAM(s) }, why: "the direct-capture image (not SRAM)",
		opts: staticOpts(onOff),
		get:  func(s *sv) int { return b2ic(s.st.ETS) },
		set:  func(c *Controller, v int) error { return directOnly(c, func() { c.eng.SetETS(v == 1) }) },
		def:  func(c *Controller) error { return directOnly(c, func() { c.eng.SetETS(false) }) },
	}
	depth := &row{id: "acq.depth", label: "Memory depth", kind: wsview.Enum,
		help: "Decimated record depth: frame rate against detail.",
		rel:  func(s *sv) bool { return !isSRAM(s) }, why: "the direct-capture image (on SRAM the timebase sets the depth)",
		// On the SRAM image the timebase sets the record's depth: the row
		// names it, not the direct path's unused setting (6k beside 1M).
		opts: func(s *sv) []opt {
			if isSRAM(s) {
				return []opt{{s.st.MemDepth, depthText(s.st.MemDepth) + " (auto)"}}
			}
			return []opt{{2048, "2k"}, {6144, "6k"}, {14336, "14k"}, {20480, "20k"}}
		},
		get: func(s *sv) int {
			if isSRAM(s) {
				return s.st.MemDepth
			}
			return s.memDepth
		},
		set: func(c *Controller, v int) error { return directOnly(c, func() { c.eng.SetMemDepth(v) }) },
		def: func(c *Controller) error { return directOnly(c, func() { c.eng.SetMemDepth(20480) }) },
	}
	srOn := func(s *sv) bool { return s.srActive || s.srSeeding }
	sr := &row{id: "acq.superres", label: "Super-res", kind: wsview.Enum, hdr: "SUPER-RES  (stacking for extra vertical bits)",
		help: "Stack matching captures of the frame on screen for extra vertical bits (freeze one with Single first).",
		opts: staticOpts(onOff),
		get:  func(s *sv) int { return b2ic(s.srActive || s.srSeeding) },
		set: func(c *Controller, v int) error {
			if v == 1 {
				return c.srArm()
			}
			c.srCancel("cancelled")
			return nil
		},
		def: func(c *Controller) error { c.srCancel("cancelled"); return nil },
	}
	srCh := &row{id: "acq.sr_channel", label: "Channel", kind: wsview.Enum,
		help: "The channel the stack aligns on.", opts: staticOpts([]opt{{0, "C1"}, {1, "C2"}}),
		get: func(s *sv) int { return s.srCh },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.srCh = v & 1
			c.mu.Unlock()
			c.srRearm()
			return nil
		},
	}
	srCh.def = func(c *Controller) error { return srCh.set(c, 0) }
	srGrid := &row{id: "acq.sr_grid", label: "Grid", kind: wsview.Enum,
		help: "Fine time-grid factor.", opts: staticOpts([]opt{{8, "x8"}, {16, "x16"}, {32, "x32"}, {64, "x64"}}),
		get: func(s *sv) int { return s.srK },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.srK = v
			c.mu.Unlock()
			c.srRearm()
			return nil
		},
	}
	srGrid.def = func(c *Controller) error { return srGrid.set(c, 32) }
	srStop := &row{id: "acq.sr_stop", label: "Stop on", kind: wsview.Enum,
		help: "What ends the stacking: extra bits, a stack count, a time, or a session of FPGA records.",
		opts: staticOpts([]opt{{0, "Bits"}, {1, "Stacks"}, {2, "Time"}, {srStopFPGA, "FPGA rec"}, {srStopFPGAMatch, "FPGA+match"}}),
		get:  func(s *sv) int { return s.srStopMode },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.srStopMode = clampInt(v, 0, srStopFPGAMatch)
			c.srStopVal = []float64{4, 500, 60, 20, 20}[c.srStopMode]
			c.mu.Unlock()
			return nil
		},
	}
	srStop.def = func(c *Controller) error { return srStop.set(c, 0) }
	srTarget := &row{id: "acq.sr_target", label: "Target", kind: wsview.Number,
		help: "The stop target: bits gained, stacks, seconds or FPGA records.",
		val:  func(s *sv) float64 { return s.srStopVal },
		lim:  func(s *sv) (float64, float64) { return srTargetLimits(s.srStopMode) },
		step: func(s *sv) float64 { return []float64{0.5, 50, 10, 10, 10}[clampInt(s.srStopMode, 0, 4)] },
		fmtN: func(v float64) string { return g3(v) },
		setN: func(c *Controller, v float64) error {
			c.mu.Lock()
			c.srStopVal = v
			c.mu.Unlock()
			return nil
		},
	}
	srTarget.stepN = func(c *Controller, n int) error {
		s := c.snap()
		lo, hi := srTarget.lim(s)
		return srTarget.setN(c, clampF(s.srStopVal+float64(n)*srTarget.step(s), lo, hi))
	}
	srTarget.def = func(c *Controller) error {
		s := c.snap()
		return srTarget.setN(c, []float64{4, 500, 60, 20, 20}[clampInt(s.srStopMode, 0, 4)])
	}
	gate := func(end bool) *row {
		id, label := "acq.sr_gate_start", "Gate start"
		if end {
			id, label = "acq.sr_gate_end", "Gate end"
		}
		r := &row{id: id, label: label, kind: wsview.Number, unit: "",
			help: "The stacked span, in samples of the frame (Auto: the screen window).",
			knob: "SCOPE: Adjust keeps tuning it against the full-width trace.",
			rel:  func(s *sv) bool { return s.srActive && !s.srFPGABusy && s.srN > 0 }, why: "Super-res on",
			val: func(s *sv) float64 {
				if end {
					if s.srManHi >= 0 {
						return float64(s.srManHi)
					}
					return float64(s.srGateHi)
				}
				if s.srManLo >= 0 {
					return float64(s.srManLo)
				}
				return float64(s.srGateLo)
			},
			lim:     func(s *sv) (float64, float64) { return 0, float64(max(s.srN, 8)) },
			step:    constStep(4),
			fmtN:    func(v float64) string { return strconv.Itoa(int(v)) },
			presets: func(*sv) []preset { return []preset{{t: "Auto", run: (*Controller).srAutoGate}} },
			def:     (*Controller).srAutoGate,
		}
		r.setN = func(c *Controller, v float64) error { return c.srSetGate(end, int(math.Round(v))) }
		r.stepN = func(c *Controller, n int) error {
			s := c.snap()
			return c.srSetGate(end, int(r.val(s))+4*n)
		}
		return r
	}
	srShow := &row{id: "acq.sr_show", label: "Show", kind: wsview.Enum,
		help: "The live trace with the gate, or the stacked trace (review).", rel: srOn, why: "Super-res on",
		opts: staticOpts([]opt{{0, "Live"}, {1, "Stack"}}),
		get:  func(s *sv) int { return b2ic(s.srFocus == 3) },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			if c.srActive {
				c.srFocus = 3 * (v & 1)
			}
			c.mu.Unlock()
			return nil
		},
	}
	srRestart := &row{id: "acq.sr_restart", label: "Restart", kind: wsview.Action,
		help: "Start the stack over (FPGA modes: run a session).",
		acts: func(s *sv) []act {
			if s.srStopMode >= srStopFPGA {
				return []act{{id: "run", t: "Run FPGA", run: func(c *Controller) error { c.srFPGARun(); return nil }}}
			}
			return []act{{id: "restart", t: "Restart stack", dim: !s.srActive, run: func(c *Controller) error { c.srReqReset(); return nil }}}
		},
		status: func(s *sv) string { return s.srStatus },
	}
	run := &row{id: "acq.run", label: "Acquisition", kind: wsview.Enum, hdr: "RUN CONTROL",
		help: "Run continuously, Stop (freeze), or Single: Normal mode, one capture, then stop.",
		knob: "Also the Run/Stop and Single keys.",
		opts: staticOpts([]opt{{0, "Run"}, {1, "Stop"}, {2, "Single"}}),
		get: func(s *sv) int {
			switch {
			case s.st.Single && s.st.Running:
				return 2
			case s.st.Running:
				return 0
			}
			return 1
		},
		set: func(c *Controller, v int) error { return c.setRun(v) },
		def: func(c *Controller) error { return c.setRun(0) },
	}
	return []*row{mode, avg, eres, ets, depth, sr, srCh, srGrid, srStop, srTarget, gate(false), gate(true), srShow, srRestart, run}
}

// ---- VIEW ----

// TRLC-LINKS: REQ-SDS-212
func viewRows() []*row {
	view := &row{id: "view.view", label: "View", kind: wsview.Enum,
		help: "Time view, X-Y, spectrum (FFT), frequency response (Bode) or spectrogram.",
		knob: "F1 again in SCOPE: Y-T / X-Y. F3 again in SPECTRUM: FFT / Spectrogram / Bode.",
		opts: staticOpts([]opt{{viewYT, "Y-T"}, {viewXY, "X-Y"}, {viewFFT, "FFT"}, {viewBode, "Bode"}, {viewSpectrogram, "Spectrogram"}}),
		get:  func(s *sv) int { return s.viewMode },
		set:  func(c *Controller, v int) error { return c.setView(v) },
		def:  func(c *Controller) error { return c.setView(viewYT) },
	}
	src := &row{id: "view.fft_source", label: "FFT source", kind: wsview.Enum,
		help: "The channel the spectrum, the SPECTRUM peak table and the spectrogram analyse.",
		knob: "The FFT view draws both shown channels; this one on top.",
		opts: staticOpts(chans),
		get:  func(s *sv) int { return s.fftSrc },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.fftSrc = v & 1
			c.mu.Unlock()
			return nil
		},
	}
	src.def = func(c *Controller) error { return src.set(c, 0) }
	persist := &row{id: "view.persist", label: "Persistence", kind: wsview.Enum,
		help: "Keep old traces fading on screen (Y-T).",
		opts: staticOpts(onOff),
		get:  func(s *sv) int { return b2ic(s.persist) },
		set: func(c *Controller, v int) error {
			c.SetPersist(v == 1)
			return nil
		},
		def: func(c *Controller) error { c.SetPersist(false); return nil },
	}
	bodeRef := bodeChanRow("view.bode_ref", "Bode REF", "The frequency response's reference: the channel on the DUT's input.", false)
	bodeDut := bodeChanRow("view.bode_dut", "Bode DUT", "The frequency response's measured channel: the DUT's output.", true)
	bodeClear := &row{id: "view.bode_clear", label: "Bode points", kind: wsview.Action,
		help: "Forget the frequency-response points measured so far (a new DUT or sweep).",
		acts: func(*sv) []act { return []act{{id: "clear", t: "Clear", run: (*Controller).bodeClear}} },
		status: func(s *sv) string {
			return fmt.Sprintf("%d %s", s.st.BodePoints, plural(s.st.BodePoints, "point"))
		},
	}
	return []*row{view, src, persist, bodeRef, bodeDut, bodeClear}
}

// bodeChanRow is the Bode reference or DUT channel row: the engine's own
// setting, which the web's selectors set too, so the Bode view arms the
// channels chosen on either surface. Picking the other row's channel swaps
// the two.
// TRLC-LINKS: REQ-SDS-066
func bodeChanRow(id, label, help string, dut bool) *row {
	r := &row{id: id, label: label, kind: wsview.Enum, help: help,
		knob: "SPECTRUM again (F3) shows Bode, which measures with these.",
		opts: staticOpts([]opt{{0, "C1"}, {1, "C2"}}),
		get: func(s *sv) int {
			if dut {
				return s.st.BodeDut & 1
			}
			return s.st.BodeRef & 1
		},
		set: func(c *Controller, v int) error { return c.setBodeChannel(dut, v&1) },
	}
	r.def = func(c *Controller) error { return c.setBodeChannel(dut, b2ic(dut)) }
	return r
}

// ---- MEAS ----

var measOpts = []opt{{0, "Off"}, {1, "C1"}, {2, "C2"}, {3, "Both"}}

// TRLC-LINKS: REQ-SDS-066
func measRows() []*row {
	readouts := &row{id: "meas.readouts", label: "Readouts", kind: wsview.Enum,
		help: "Show the chosen measurements in boxes on the trace.",
		opts: staticOpts(onOff),
		get:  func(s *sv) int { return b2ic(s.showMeas) },
		set: func(c *Controller, v int) error {
			c.SetMeasOn(v == 1)
			return nil
		},
		def: func(c *Controller) error { c.SetMeasOn(false); return nil },
	}
	clear := &row{id: "meas.clear", label: "Clear all", kind: wsview.Action,
		help: "Remove every chosen measurement.",
		acts: func(s *sv) []act {
			return []act{{id: "clear", t: "Clear all", dim: len(s.meas) == 0, run: func(c *Controller) error { c.clearMeas(); return nil }}}
		},
		status: func(s *sv) string { return fmt.Sprintf("%d of at most %d", len(s.meas), maxMeasItems) },
	}
	rows := []*row{readouts, clear}
	for i, p := range measParams {
		i, p := i, p
		r := &row{id: "meas." + p.id, label: p.label, kind: wsview.Enum, meas: i + 1,
			help: "Show the " + strings.ToLower(p.label) + " of CH1, CH2 or both in the readouts and the MEASURE statistics.",
			opts: staticOpts(measOpts),
			get: func(s *sv) int {
				v := 0
				for _, it := range s.meas {
					if it.param == i {
						v |= 1 << it.ch
					}
				}
				return v
			},
			set: func(c *Controller, v int) error { c.setMeasParam(i, v); return nil },
			def: func(c *Controller) error { c.setMeasParam(i, 0); return nil },
		}
		rows = append(rows, r)
	}
	return rows
}

// ---- CURS ----

// TRLC-LINKS: REQ-SDS-066
func cursRows() []*row {
	mode := &row{id: "curs.mode", label: "Cursors", kind: wsview.Enum,
		help: "Off, two time cursors or two voltage cursors.",
		knob: "MEASURE workspace: the Adjust push cycles Off / Time / Volts.",
		opts: staticOpts([]opt{{0, "Off"}, {1, "Time"}, {2, "Volts"}}),
		get: func(s *sv) int {
			if !s.curOn {
				return 0
			}
			return 1 + s.curType
		},
		set: func(c *Controller, v int) error { c.setCursorMode(v); return nil },
		def: func(c *Controller) error { c.setCursorMode(0); return nil },
	}
	on := func(s *sv) bool { return s.curOn }
	pos := func(k int) *row {
		name := "a"
		if k == 1 {
			name = "b"
		}
		r := &row{id: "curs." + name, label: "Cursor " + strings.ToUpper(name), kind: wsview.Number, unit: "div",
			help: "Position of the cursor (divisions from the left, or from the bottom for volts).",
			rel:  on, why: "Cursors on",
			val: func(s *sv) float64 { return cursorDiv(s, k) },
			lim: func(s *sv) (float64, float64) {
				if s.curType == 1 {
					return 0, 8
				}
				return 0, 10
			},
			step: func(s *sv) float64 {
				if s.curType == 1 {
					return 0.08
				}
				return 0.1
			},
			fmtN: fmtDiv,
			setN: func(c *Controller, v float64) error { c.setCursorDiv(k, v); return nil },
			presets: func(*sv) []preset {
				return []preset{{t: "Centre", run: func(c *Controller) error { c.centreCursor(k); return nil }},
					{t: "At trigger", run: func(c *Controller) error { c.cursorAtTrigger(k); return nil }}}
			},
			def: func(c *Controller) error { c.centreCursor(k); return nil },
		}
		r.stepN = func(c *Controller, n int) error { // a step a detent, accelerated by a fast spin
			s := c.snap()
			lo, hi := r.lim(s)
			return r.setN(c, clampF(r.val(s)+float64(n)*r.step(s), lo, hi))
		}
		if k == 0 {
			r.knob = "MEASURE workspace: the Adjust knob."
		} else {
			r.knob = "MEASURE workspace: the Position knob."
		}
		return r
	}
	active := &row{id: "curs.active", label: "Adjust moves", kind: wsview.Enum,
		help: "Which cursor Adjust moves in SCOPE when no sheet value is tagged.",
		rel:  on, why: "Cursors on",
		opts: staticOpts([]opt{{0, "Cursor A"}, {1, "Cursor B"}}),
		get:  func(s *sv) int { return s.curSel },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.curSel = v & 1
			c.ui.adj = "curs." + string(rune('a'+v&1))
			c.mu.Unlock()
			return nil
		},
		def: func(c *Controller) error {
			c.mu.Lock()
			c.curSel = 0
			c.mu.Unlock()
			return nil
		},
	}
	return []*row{mode, pos(0), pos(1), active}
}

// cursorDiv is a cursor's position in divisions.
// TRLC-LINKS: REQ-SDS-212
func cursorDiv(s *sv, k int) float64 {
	if s.curType == 1 {
		return math.Round((1-s.curY[k])*800) / 100
	}
	return math.Round(s.curX[k]*1000) / 100
}

// ---- MATH/REF (COMPARE) ----

// maskTolNames label the mask tolerance presets (±samples / ±codes).
// TRLC-LINKS: REQ-SDS-212
func maskTolOpts() []opt {
	var o []opt
	for i, t := range maskTols {
		o = append(o, opt{i, fmt.Sprintf("%dpt/%dcd", t[0], t[1])})
	}
	return o
}

// stackDepths are the master's stack depths (captures averaged).
var stackDepths = []int{16, 32, 64, 128, 256}

// TRLC-LINKS: REQ-SDS-212
func cmpRows() []*row {
	math_ := &row{id: "cmp.math", label: "Math", kind: wsview.Enum,
		help: "Show a computed trace from both channels.",
		knob: "COMPARE workspace: the MATH row, Adjust push = next.",
		opts: staticOpts([]opt{{0, "Off"}, {1, "C1+C2"}, {2, "C1-C2"}, {3, "C2-C1"}, {4, "C1xC2"}}),
		get:  func(s *sv) int { return s.mathMode },
		set:  func(c *Controller, v int) error { return c.setMath(v) },
		def:  func(c *Controller) error { return c.setMath(0) },
	}
	store := func(i int) *row {
		n := string(rune('A' + i))
		return &row{id: "cmp.store_" + strings.ToLower(n), label: "Store Ref " + n, kind: wsview.Action,
			help: "Store the screen as reference " + n + " and show it (COMPARE: the REF " + n + " row push), or delete it.",
			acts: func(s *sv) []act {
				return []act{{id: "store", t: "Store now", run: func(c *Controller) error { return c.storeRef(i) }},
					{id: "clear", t: "Clear", dim: !s.refHas[i], run: func(c *Controller) error { return c.clearRef(i) }}}
			},
			status: func(s *sv) string {
				if !s.refHas[i] {
					return "empty"
				}
				what := "holds C1"
				if s.refC2[i] {
					what = "holds C1 and C2"
				}
				return what + " at " + si(s.refTdiv[i], "s") + "/div"
			},
		}
	}
	show := func(i int) *row {
		n := string(rune('A' + i))
		r := &row{id: "cmp.show_" + strings.ToLower(n), label: "Show Ref " + n, kind: wsview.Enum,
			help: "Display the stored reference " + n + ".",
			rel:  func(s *sv) bool { return s.refHas[i] }, why: "Ref " + n + " stored",
			opts: staticOpts(onOff),
			get:  func(s *sv) int { return b2ic(s.refShow[i]) },
			set:  func(c *Controller, v int) error { return c.showRef(i, v == 1) },
		}
		return r
	}
	sa, sb := store(0), store(1)
	sa.hdr = "REFERENCES"
	clear := &row{id: "cmp.clear_refs", label: "Clear refs", kind: wsview.Action, help: "Delete both references.",
		acts: func(*sv) []act { return []act{{id: "clear", t: "Clear A + B", run: (*Controller).clearRefs}} },
	}
	maskMode := &row{id: "cmp.mask", label: "Mask test", kind: wsview.Enum, hdr: "MASK TEST",
		help: "Count captures that leave the mask, and optionally stop on the first.",
		knob: "COMPARE workspace: the MASK row push switches it once a mask exists.",
		opts: staticOpts([]opt{{0, "Off"}, {1, "Test"}, {2, "Stop on fail"}}),
		get:  func(s *sv) int { return s.st.MaskMode },
		set:  func(c *Controller, v int) error { return c.setMaskMode(v) },
		def:  func(c *Controller) error { return c.setMaskMode(0) },
	}
	build := &row{id: "cmp.build_mask", label: "Build mask", kind: wsview.Action,
		help: "Make a mask around the trigger source's live captures (the frames and tolerance below).",
		acts: func(s *sv) []act {
			return []act{{id: "build", t: "Build now", dim: s.maskBusy, run: func(c *Controller) error { c.maskBuildStart(); return nil }}}
		},
		status: func(s *sv) string {
			// the build's progress, or why it failed: the phone reads the
			// reason here, not only on the LCD
			msg := strings.TrimPrefix(s.maskMsg, "MASK: ")
			switch {
			case s.maskBusy && strings.HasPrefix(msg, "building"):
				return msg
			case s.maskBusy:
				return "building"
			case s.st.MaskSet:
				return "ready"
			case strings.HasPrefix(msg, "build failed"):
				return msg
			}
			return "none"
		},
	}
	clearMask := &row{id: "cmp.clear_mask", label: "Clear mask", kind: wsview.Action,
		help: "Remove the mask: its test goes off, its counts and captured failures are zeroed, and a build or master under way stops.",
		acts: func(s *sv) []act {
			return []act{{id: "clear", t: "Clear", dim: !s.st.MaskSet && !s.maskBusy && !s.stacking, run: (*Controller).clearMaskAction}}
		},
	}
	frames := &row{id: "cmp.mask_frames", label: "Build frames", kind: wsview.Enum,
		help: "Captures a mask build gathers.", opts: staticOpts([]opt{{16, "16"}, {32, "32"}, {64, "64"}, {128, "128"}}),
		get: func(s *sv) int { return s.maskN },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.maskN = v
			c.mu.Unlock()
			return nil
		},
	}
	frames.def = func(c *Controller) error { return frames.set(c, 32) }
	tol := &row{id: "cmp.mask_tol", label: "Tolerance", kind: wsview.Enum,
		help: "The mask's margin: +/- samples across, +/- codes up and down.", opts: func(*sv) []opt { return maskTolOpts() },
		get: func(s *sv) int { return s.maskTol },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.maskTol = clampInt(v, 0, len(maskTols)-1)
			c.mu.Unlock()
			return nil
		},
	}
	tol.def = func(c *Controller) error { return tol.set(c, 1) }
	counters := &row{id: "cmp.counters", label: "Counters", kind: wsview.Action, help: "Zero the pass/fail counts.",
		acts:   func(*sv) []act { return []act{{id: "reset", t: "Reset", run: (*Controller).resetMaskCounters}} },
		status: func(s *sv) string { return fmt.Sprintf("pass %d  fail %d", s.st.MaskPass, s.st.MaskFail) },
	}
	depthOpts := func(*sv) []opt {
		var o []opt
		for _, n := range stackDepths {
			o = append(o, opt{n, strconv.Itoa(n)})
		}
		return o
	}
	depth := &row{id: "cmp.master_depth", label: "Master depth", kind: wsview.Enum, hdr: "MASTER",
		help: "Captures a master averages (COMPARE: F5 again stacks it).",
		opts: depthOpts,
		get:  func(s *sv) int { return s.stackN },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.stackN = v
			c.mu.Unlock()
			return nil
		},
	}
	depth.def = func(c *Controller) error { return depth.set(c, 64) }
	master := &row{id: "cmp.stack_master", label: "Stack master", kind: wsview.Action,
		help: "Average the trigger source into REF A, build the mask from it, then test raw captures (COMPARE: F5 again).",
		acts: func(s *sv) []act {
			if s.stacking {
				return []act{{id: "cancel", t: "Cancel", run: func(c *Controller) error { c.stackCancel("", false); return nil }}}
			}
			return []act{{id: "stack", t: "Stack now", run: (*Controller).stackStart}}
		},
		status: func(s *sv) string {
			if s.stacking {
				return "stacking"
			}
			if s.ui.hasMaster {
				return fmt.Sprintf("REF A: %d x C%d", s.ui.masterN, s.ui.masterSrc+1)
			}
			return "none"
		},
	}
	return []*row{math_, sa, show(0), sb, show(1), clear, maskMode, build, clearMask, frames, tol, counters, depth, master}
}

// ---- BUS ----

// protoOpts are the decoders, the serial trigger takes every one, and Auto:
// not a decoder but the detection that picks one and fills in its settings
// (last, so Position < > steps through the decoders without detecting).
var protoOpts = []opt{{framecalc.ProtoOff, "Off"}, {framecalc.ProtoUART, "UART"},
	{framecalc.ProtoI2C, "I2C"}, {framecalc.ProtoSPI, "SPI"}, {framecalc.ProtoManchester, "Manch"},
	{framecalc.ProtoSENT, "SENT"}, {framecalc.ProtoCAN, "CAN"}, {framecalc.ProtoMIL1553, "1553"},
	{framecalc.ProtoARINC429, "ARINC"}, {framecalc.ProtoUSB, "USB LS"}, {framecalc.ProtoFlexRay, "FlexRay"},
	{framecalc.ProtoAuto, "Auto"}}

// protoName names a protocol number.
// TRLC-LINKS: REQ-SDS-214
func protoName(p int) string {
	for _, o := range protoOpts {
		if o.v == p {
			return o.t
		}
	}
	return "?"
}

// bitRates are the Bit rate row's knob steps: the rates the bit-rate buses
// run at.
var bitRates = []float64{10e3, 12.5e3, 50e3, 100e3, 125e3, 250e3, 500e3, 1e6, 1.5e6, 2e6, 2.5e6, 5e6, 10e6}

// standardRate is a bit rate a detection measured, moved onto the Bit rate
// row's rate within 3 % of it, as a UART's baud is (a 1 Mbit/s Manchester bus
// measures 998 kb/s on one record, a 500 kbit/s CAN bus 509 kb/s); otherwise
// it stands.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func standardRate(rate int) int {
	for _, r := range bitRates {
		if math.Abs(float64(rate)-r) <= 0.03*r {
			return int(r)
		}
	}
	return rate
}

// bauds are the standard UART rates the Baud row writes out.
var bauds = []int{1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600}

// rateProto reports a protocol whose bit rate is a setting (DecBaud).
// TRLC-LINKS: REQ-SDS-214
func rateProto(p int) bool {
	switch p {
	case framecalc.ProtoManchester, framecalc.ProtoCAN, framecalc.ProtoMIL1553, framecalc.ProtoARINC429,
		framecalc.ProtoUSB, framecalc.ProtoFlexRay:
		return true
	}
	return false
}

// lineProto reports a single-line protocol (one channel: the Source row).
// TRLC-LINKS: REQ-SDS-214
func lineProto(p int) bool {
	return p == framecalc.ProtoUART || p == framecalc.ProtoSENT || rateProto(p)
}

// defaultRate is a protocol's usual bit rate, seeded when it is chosen.
// TRLC-LINKS: REQ-SDS-214
func defaultRate(p int) int {
	switch p {
	case framecalc.ProtoCAN:
		return 500000
	case framecalc.ProtoMIL1553, framecalc.ProtoManchester:
		return 1000000
	case framecalc.ProtoARINC429:
		return 100000
	case framecalc.ProtoUSB:
		return 1500000
	case framecalc.ProtoFlexRay:
		return 10000000
	}
	return 115200
}

// TRLC-LINKS: REQ-SDS-214
func busTabRows() []*row {
	decOn := func(s *sv) bool { return s.decProto != framecalc.ProtoOff }
	proto := &row{id: "bus.protocol", label: "Protocol", kind: wsview.Enum,
		help:  "Protocol decoder for the bus on the inputs. Auto detects the bus once and fills in the protocol and its settings.",
		knob:  "F1..F5 hold Off, Auto, UART, I2C, SPI; Position < > reaches every decoder. Auto again detects again.",
		opts:  staticOpts(protoOpts),
		quick: []int{framecalc.ProtoOff, framecalc.ProtoAuto, framecalc.ProtoUART, framecalc.ProtoI2C, framecalc.ProtoSPI},
		get:   func(s *sv) int { return s.decProto },
		// the decoder in use stays until Auto fills in what it found: the row,
		// the phone and the web say a detection runs, not the old decoder
		busy: func(s *sv) string {
			if s.ui.busDetecting {
				return "Auto: detecting..."
			}
			return ""
		},
		long: func(s *sv) string {
			switch {
			case s.ui.busDetecting:
				return "Auto: detecting..."
			case s.decAuto:
				return protoName(s.decProto) + " (Auto)"
			}
			return protoName(s.decProto)
		},
		set: func(c *Controller, v int) error { return c.setDecProto(v) },
		def: func(c *Controller) error { return c.setDecProto(framecalc.ProtoOff) },
	}
	format := &row{id: "bus.strip_format", label: "Strip format", kind: wsview.Enum,
		help: "Format of the decode strip under the trace: Hex, String (text) or both. The BUS packet list always shows hex and string.",
		knob: "Set here only (default Hex). F2 again in BUS switches Packets / Eye diagram.",
		rel:  decOn, why: "a Protocol (decode on)",
		opts: staticOpts([]opt{{0, "Hex"}, {1, "String"}, {2, "Hex+String"}}),
		get:  func(s *sv) int { return s.decFormat },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.decFormat = mod3(v)
			c.mu.Unlock()
			return nil
		},
	}
	format.def = func(c *Controller) error { return format.set(c, 0) }
	isAuto := func(s *sv) bool { return serialProto(s.decProto) && s.hasStream }
	stream := &row{id: "bus.stream", label: "Stream", kind: wsview.Enum,
		help: "Decode the bus continuously in hardware into a transcript, with the settings below.",
		knob: "Run/Stop stops and restarts a stream.",
		rel:  isAuto, why: "a Protocol (decode on)",
		opts: staticOpts(onOff),
		get:  func(s *sv) int { return b2ic(s.decMode == decModeStream) },
		set:  func(c *Controller, v int) error { return c.setStreamMode(v == 1) },
		def:  func(c *Controller) error { return c.setStreamMode(false) },
	}
	streamTrig := &row{id: "bus.stream_trigger", label: "Stream stops", kind: wsview.Enum,
		help: "What ends a stream: a decode error, a value, or Run/Stop by hand.", rel: isAuto, why: "a Protocol (decode on)",
		opts: staticOpts([]opt{{0, "On error"}, {1, "On value"}, {2, "By hand"}}),
		get:  func(s *sv) int { return s.decTrig },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.decTrig = mod3(v)
			c.mu.Unlock()
			return nil
		},
	}
	streamTrig.def = func(c *Controller) error { return streamTrig.set(c, 0) }
	streamVal := numRow("bus.stream_value", "Stream value", "", func(s *sv) float64 { return float64(s.decValue) },
		constLim(0, 0xFFFF), constStep(1), func(c *Controller, v float64) error {
			c.mu.Lock()
			c.decValue = clampInt(int(math.Round(v)), 0, 0xFFFF)
			c.mu.Unlock()
			return nil
		}, hexText)
	streamVal.help = "The decoded value that ends a stream (Stream stops = On value)."
	streamVal.rel, streamVal.why = func(s *sv) bool { return isAuto(s) && s.decTrig == 1 }, "Stream stops = On value"
	streamVal.def = func(c *Controller) error { return streamVal.setN(c, 0) }
	streamScreen := &row{id: "bus.stream_screen", label: "Stream shows", kind: wsview.Enum,
		help: "A stopped stream: its record's waveform, or the transcript list (Position scrolls it).",
		rel:  func(s *sv) bool { return isAuto(s) && s.decMode == decModeStream && s.streamStopped }, why: "a stopped stream",
		opts: staticOpts([]opt{{0, "Wave"}, {1, "List"}}),
		get:  func(s *sv) int { return b2ic(s.streamList) },
		set: func(c *Controller, v int) error {
			if b2ic(c.snap().streamList) != v {
				c.toggleStreamList()
			}
			return nil
		},
	}
	baudOpts := func(s *sv) []opt {
		var o []opt
		std := false
		for _, b := range bauds {
			o = append(o, opt{b, baudName(b)})
			std = std || b == s.decBaud
		}
		if !std && s.decBaud > 0 && s.decProto == framecalc.ProtoUART {
			o = append(o, opt{s.decBaud, baudName(s.decBaud)})
		}
		return o
	}
	baud := &row{id: "bus.baud", label: "Baud", kind: wsview.Enum, hdr: "UART AND LINE BUSES",
		help: "UART bit rate.", knob: "F1..F5 hold the common rates; Position < > reaches all; typed entry takes any rate.",
		rel: func(s *sv) bool { return s.decProto == framecalc.ProtoUART }, why: "Protocol = UART",
		opts:  baudOpts,
		quick: []int{9600, 19200, 38400, 57600, 115200},
		get:   func(s *sv) int { return s.decBaud },
		long:  func(s *sv) string { return fmt.Sprintf("%d baud", s.decBaud) },
		parse: func(s *sv, t string) (int, bool) {
			v, err := parseNumber(t, "Bd")
			if err != nil || v < 50 || v > 20e6 {
				return 0, false
			}
			return int(math.Round(v)), true
		},
		set: func(c *Controller, v int) error { return c.setDecBaud(v) },
		def: func(c *Controller) error { return c.setDecBaud(115200) },
	}
	rate := numRow("bus.bit_rate", "Bit rate", "b/s", func(s *sv) float64 { return float64(s.decBaud) },
		constLim(1000, 20e6), constStep(0), func(c *Controller, v float64) error { return c.setDecBaud(int(math.Round(v))) },
		func(v float64) string { return si(v, "b/s") })
	rate.help = "Bit rate of Manchester, CAN, MIL-1553, ARINC 429, USB LS or FlexRay."
	rate.rel, rate.why = func(s *sv) bool { return rateProto(s.decProto) }, "a bit-rate protocol"
	rate.ladder = func(*sv) []float64 { return bitRates }
	rate.stepN = func(c *Controller, n int) error {
		l := rate.ladder(nil)
		i := nearestLadder(l, math.Max(1, rate.val(c.snap())))
		return c.setDecBaud(int(l[clampInt(i+n, 0, len(l)-1)]))
	}
	rate.def = func(c *Controller) error { return c.setDecBaud(defaultRate(c.snap().decProto)) }
	line := &row{id: "bus.source", label: "Source", kind: wsview.Enum,
		help: "Channel carrying the line.", rel: func(s *sv) bool { return lineProto(s.decProto) }, why: "a single-line protocol",
		opts: staticOpts(chans),
		get:  func(s *sv) int { return s.decChA },
		set:  func(c *Controller, v int) error { return c.setDecRole(true, v) },
		def:  func(c *Controller) error { return c.setDecRole(true, 0) },
	}
	isI2C := func(s *sv) bool { return s.decProto == framecalc.ProtoI2C }
	isSPI := func(s *sv) bool { return s.decProto == framecalc.ProtoSPI }
	scl := &row{id: "bus.scl", label: "SCL", kind: wsview.Enum, hdr: "I2C", help: "I2C clock channel.",
		rel: isI2C, why: "Protocol = I2C", opts: staticOpts(chans),
		get: func(s *sv) int { return s.decChA },
		set: func(c *Controller, v int) error { return c.setDecRole(true, v) },
	}
	sda := &row{id: "bus.sda", label: "SDA", kind: wsview.Enum, help: "I2C data channel.",
		rel: isI2C, why: "Protocol = I2C", opts: staticOpts(chans),
		get: func(s *sv) int { return s.decChB },
		set: func(c *Controller, v int) error { return c.setDecRole(false, v) },
	}
	clk := &row{id: "bus.clk", label: "CLK", kind: wsview.Enum, hdr: "SPI", help: "SPI clock channel.",
		rel: isSPI, why: "Protocol = SPI", opts: staticOpts(chans),
		get: func(s *sv) int { return s.decChA },
		set: func(c *Controller, v int) error { return c.setDecRole(true, v) },
	}
	data := &row{id: "bus.data", label: "DATA", kind: wsview.Enum, help: "SPI data channel.",
		rel: isSPI, why: "Protocol = SPI", opts: staticOpts(chans),
		get: func(s *sv) int { return s.decChB },
		set: func(c *Controller, v int) error { return c.setDecRole(false, v) },
	}
	spiMode := &row{id: "bus.spi_mode", label: "SPI mode", kind: wsview.Enum, help: "Clock polarity and phase (CPOL, CPHA).",
		rel: isSPI, why: "Protocol = SPI", opts: staticOpts([]opt{{0, "0"}, {1, "1"}, {2, "2"}, {3, "3"}}),
		get: func(s *sv) int { return b2ic(s.decCPOL)<<1 | b2ic(s.decCPHA) },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.decCPOL, c.decCPHA = v&2 != 0, v&1 != 0
			c.auto = autoWatch{} // set by hand
			c.mu.Unlock()
			c.syncSerialBus()
			return nil
		},
	}
	bitOrder := &row{id: "bus.bit_order", label: "Bit order", kind: wsview.Enum, help: "Which bit of an SPI word comes first.",
		rel: isSPI, why: "Protocol = SPI", opts: staticOpts([]opt{{0, "MSB first"}, {1, "LSB first"}}),
		get: func(s *sv) int { return b2ic(s.decLSB) },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.decLSB = v == 1
			c.auto = autoWatch{} // set by hand
			c.mu.Unlock()
			c.syncSerialBus()
			return nil
		},
	}
	bitOrder.def = func(c *Controller) error { return bitOrder.set(c, 0) }
	serialBus := func(s *sv) bool { return s.hasSerial && s.decProto != framecalc.ProtoOff }
	match := &row{id: "bus.match", label: "Match bytes", kind: wsview.Bytes, hdr: "SERIAL TRIGGER",
		help: "Bytes the serial trigger waits for: hex like 55 AA, A? = one nibble, ?? = any byte, ERR = a framing error.",
		knob: "From screen: pick bytes you see decoded.",
		rel:  serialBus, why: "a Protocol (decode on); the match can be preset",
		def: func(c *Controller) error { return c.setMatchTokens(nil) },
	}
	arm := &row{id: "bus.trigger_on", label: "Trigger on it", kind: wsview.Enum,
		help: "On: trigger type Serial, firing on the match with the protocol above.",
		rel:  serialBus, why: "a Protocol (decode on)",
		opts: staticOpts(onOff),
		get:  func(s *sv) int { return b2ic(s.st.SerialMode != 0) },
		set: func(c *Controller, v int) error {
			if v == 1 {
				return c.armSerial()
			}
			if c.eng.Snapshot().SerialMode != 0 {
				c.setTrigTypeIndex(c.eng.Snapshot(), 0)
			}
			return nil
		},
	}
	arm.def = func(c *Controller) error { return arm.set(c, 0) }
	addr := numRow("bus.i2c_address", "I2C address", "", func(s *sv) float64 {
		if !s.hasSerial {
			return -1
		}
		return float64(s.serial.Addr)
	}, constLim(-1, 127), constStep(1), func(c *Controller, v float64) error { return c.setI2CAddr(int(math.Round(v))) },
		func(v float64) string {
			if v == -1 {
				return "Any"
			}
			return hexText(v)
		})
	addr.help = "The I2C address the serial trigger requires (Any: every address)."
	addr.rel, addr.why = func(s *sv) bool { return serialBus(s) && s.decProto == framecalc.ProtoI2C }, "Protocol = I2C"
	addr.presets = func(*sv) []preset { return []preset{{t: "Any", v: -1}} }
	addr.def = func(c *Controller) error { return c.setI2CAddr(-1) }
	return []*row{proto, format, stream, streamTrig, streamVal, streamScreen, baud, rate, line, scl, sda, clk, data, spiMode, bitOrder, match, arm, addr}
}

// hexText writes a whole number as the hex rows do: 0x50, and -0x02 for a
// negative one typed (it reads as itself in the refusal, not as 0x-2).
// TRLC-LINKS: REQ-SDS-212
func hexText(v float64) string {
	n := int(math.Round(v))
	if n < 0 {
		return fmt.Sprintf("-0x%02X", -n)
	}
	return fmt.Sprintf("0x%02X", n)
}

// baudName is a baud as the row writes it: 115k2, 9k6, 1k2.
// TRLC-LINKS: REQ-SDS-214
func baudName(b int) string {
	if b >= 1000 && b%1000 != 0 && b%100 == 0 {
		return fmt.Sprintf("%dk%d", b/1000, b%1000/100)
	}
	if b >= 1000 && b%1000 == 0 {
		return fmt.Sprintf("%dk", b/1000)
	}
	return strconv.Itoa(b)
}

// depthText is a record depth as the rows write it: 1M, 20k, 512.
// TRLC-LINKS: REQ-SDS-212
func depthText(n int) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%dM", n>>20)
	case n >= 1024 && n%1024 == 0:
		return fmt.Sprintf("%dk", n>>10)
	}
	return strconv.Itoa(n)
}

// ---- FILE ----

// TRLC-LINKS: REQ-SDS-212
func fileRows() []*row {
	var rows []*row
	for n := 1; n <= setupSlots; n++ {
		n := n
		rows = append(rows, &row{id: fmt.Sprintf("file.setup%d", n), label: fmt.Sprintf("Setup %2d", n), kind: wsview.Slot, slot: n,
			help: fmt.Sprintf("Setup memory %d: Save stores every setting and the workspace; Recall restores them.", n),
			acts: func(s *sv) []act {
				return []act{{id: "save", t: "Save", run: func(c *Controller) error { return c.saveSlot(n) }},
					{id: "recall", t: "Recall", dim: !s.slotUsed[n], run: func(c *Controller) error { return c.recallSlot(n) }}}
			},
			status: func(s *sv) string {
				if !s.slotUsed[n] {
					return "empty"
				}
				return s.slotSum[n]
			},
		})
	}
	return rows
}

// ---- SYS ----

var guideNames = []string{"This workspace", "Sheets", "Knobs, lamps"}

// TRLC-LINKS: REQ-SDS-212
func sysRows() []*row {
	help := &row{id: "sys.help", label: "Help", kind: wsview.Action,
		help: "The guide below: this workspace, the sheets, or the knobs and lamps (also the Help key).",
		acts: func(*sv) []act {
			var a []act
			for i, t := range guideNames {
				i := i
				a = append(a, act{id: []string{"workspace", "sheets", "knobs"}[i], t: t, run: func(c *Controller) error {
					c.mu.Lock()
					c.ui.guide = i
					c.mu.Unlock()
					return nil
				}})
			}
			return a
		},
		status: func(s *sv) string { return guideNames[clampInt(s.ui.guide, 0, 2)] + " below" },
	}
	print := &row{id: "sys.print", label: "Print screen", kind: wsview.Action,
		help: "Save a screenshot to the U-disk (also the Print key).",
		acts: func(s *sv) []act {
			return []act{{id: "print", t: "Save PNG", dim: !s.hasHardcopy, run: func(c *Controller) error { c.printScreen(); return nil }}}
		},
	}
	auto := &row{id: "sys.autoset", label: "Autoset", kind: wsview.Action,
		help: "Find the signals and set scales and trigger (also the Auto key).",
		acts: func(*sv) []act {
			return []act{{id: "autoset", t: "Run autoset", run: func(c *Controller) error { c.runAuto(); return nil }}}
		},
	}
	def := &row{id: "sys.default", label: "Default setup", kind: wsview.Action,
		help: "Factory settings; saved setups and references are kept (also the Default Setup key).",
		acts: func(*sv) []act {
			return []act{{id: "default", t: "Factory reset", run: func(c *Controller) error { c.defaultSetupUI(); return nil }}}
		},
	}
	remote := &row{id: "sys.remote_notices", label: "Remote notices", kind: wsview.Enum,
		help: "A one-line note on this screen when the phone remote changes a setting.",
		opts: staticOpts(onOff),
		get:  func(s *sv) int { return b2ic(s.remoteNotices) },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.remoteNotices = v == 1
			c.mu.Unlock()
			return nil
		},
	}
	remote.def = func(c *Controller) error { return remote.set(c, 1) }
	saver := &row{id: "sys.saver", label: "Screen saver", kind: wsview.Enum,
		help:  "With no input this long, only a small live scope drifts over black, so nothing burns in. A key wakes it.",
		opts:  staticOpts(minuteOpts(saverMins)),
		quick: saverQuick,
		get:   func(s *sv) int { return s.saverMin },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.saverMin = oneOf(v, saverMins, defSaverMin)
			c.mu.Unlock()
			return nil
		},
	}
	saver.def = func(c *Controller) error { return saver.set(c, defSaverMin) }
	off := &row{id: "sys.screen_off", label: "Screen off", kind: wsview.Enum,
		help:  "With no input this long, the screen goes black and its backlight off. A key wakes it.",
		opts:  staticOpts(minuteOpts(offMins)),
		quick: offQuick,
		get:   func(s *sv) int { return s.offMin },
		set: func(c *Controller, v int) error {
			c.mu.Lock()
			c.offMin = oneOf(v, offMins, defOffMin)
			c.mu.Unlock()
			return nil
		},
	}
	off.def = func(c *Controller) error { return off.set(c, defOffMin) }
	return []*row{help, print, auto, def, remote, saver, off}
}
