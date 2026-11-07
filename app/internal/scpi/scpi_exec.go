// ENGMODEL-OWNER-UNIT: FU-APP-SCPI

package scpi

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/labeth/open-dso-core/sample"
	core "github.com/labeth/open-dso-core/scpi"
	"github.com/labeth/open-dso-core/scpi/meas"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// cmd is a dialect command whose handler needs nothing but the call.
type cmd = core.Command

// dialect is the legacy short-form set this scope answers: the core's Line
// grammar with C1/C2 prefixes, no status model (the status queries answer
// 0) and no long headers.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) dialect() core.Dialect {
	d := core.Dialect{
		Name:    "SDS1000CML+ legacy",
		Grammar: core.Line,
		Sources: []core.Source{{Prefix: "C", Min: 1, Max: 2}},
		Status:  core.NoStatus,
	}
	commons := core.Commons{
		IDN: func() string { return fmt.Sprintf("Siglent,SDS1102CML+,%s,8.01.01.99R9", h.serial) },
		Reset: func(c *core.Call) error {
			h.reset(c)
			return nil
		},
		Save:   func(c *core.Call, n int) error { return h.setup(c, "*SAV", n) },
		Recall: func(c *core.Call, n int) error { return h.setup(c, "*RCL", n) },
		Wait:   h.settle,
	}
	d.Commands = append(d.Commands, commons.Commands(
		"*IDN?", "*RST", "*SAV", "*RCL", "*WAI", "*CLS", "*OPC", "*ESE", "*SRE", "*OPC?", "*STB?", "*ESR?", "*TST?")...)
	d.Commands = append(d.Commands, core.LegacyStatus("INR?", "CMR?")...)
	d.Commands = append(d.Commands, core.CommHeader()...)
	fixed := func(header, value string) cmd {
		return cmd{Pattern: header + "?", Handler: func(c *core.Call) error { c.Reply(header, value); return nil }}
	}
	d.Commands = append(d.Commands,
		fixed("*CAL", "0"),
		fixed("SRLN", "Default"),
		fixed("BUZZ", "OFF"),
		fixed("GRDS", "FULL"),
		fixed("INTS", "GRID,100,TRACE,100"),
		fixed("TRCP", "DC"),
		fixed("HCSU", "DEV,BMP"),
		// No buzzer driver exists in this firmware: OFF is the fixed truth,
		// BUZZ ON must error rather than silently succeed (the BWL rule).
		cmd{Pattern: "BUZZ", Handler: only("OFF", "ON")},
		// Grid display is fixed FULL — the renderer has no half/off
		// graticule mode. HALF/OFF are real vendor values with no
		// implementation here → error, never silent (the BWL rule).
		cmd{Pattern: "GRDS", Handler: only("FULL", "HALF", "OFF")},
		// Trigger coupling is fixed DC on this build (no engine control).
		cmd{Pattern: "TRCP", Handler: only("DC", "AC", "HFREJ", "LFREJ")},
		// No intensity control exists (grid and trace render at full
		// drive): only a request for that state is accepted.
		cmd{Pattern: "INTS", Handler: h.setINTS},
		cmd{Pattern: "HCSU", Handler: h.setHCSU},
		cmd{Pattern: "MENU", Handler: h.setMenu},
		cmd{Pattern: "MENU?", Handler: h.onOff("MENU", func() bool { return h.disp != nil && h.disp.MenuOpen() })},
		cmd{Pattern: "PESU", Handler: h.setPersist},
		cmd{Pattern: "PESU?", Handler: func(c *core.Call) error {
			v := "OFF"
			if h.disp != nil && h.disp.PersistOn() {
				v = "INFINITE"
			}
			c.Reply("PESU", v)
			return nil
		}},
		cmd{Pattern: "XYDS", Handler: h.setXY},
		cmd{Pattern: "XYDS?", Handler: h.onOff("XYDS", func() bool { return h.disp != nil && h.disp.ViewXY() })},
		cmd{Pattern: "TDIV", Handler: h.setTdiv},
		cmd{Pattern: "TDIV?", Handler: func(c *core.Call) error {
			c.Reply("TDIV", sciS(h.tdivNow(h.sc.Snapshot())))
			return nil
		}},
		cmd{Pattern: "TRDL", Handler: h.setTRDL},
		cmd{Pattern: "TRDL?", Handler: func(c *core.Call) error {
			st := h.sc.Snapshot()
			frac := st.TrigPosFrac
			if !(frac >= 0 && frac <= 1) {
				frac = .5
			}
			c.Reply("TRDL", sciS((frac-.5)*10*st.TdivS))
			return nil
		}},
		cmd{Pattern: "TRMD", Handler: h.setTRMD},
		cmd{Pattern: "TRMD?", Handler: h.trmdQuery},
		cmd{Pattern: "TRLV", Handler: h.setTRLV},
		cmd{Pattern: "TRLV?", Handler: func(c *core.Call) error { return h.trlvQuery(c, "TRLV") }},
		cmd{Pattern: "TRSL", Handler: h.setTRSL},
		cmd{Pattern: "TRSL?", Handler: func(c *core.Call) error { return h.trslQuery(c, "TRSL") }},
		cmd{Pattern: "TRSE", Handler: h.setTRSE},
		cmd{Pattern: "TRSE?", Handler: func(c *core.Call) error {
			src := "C1"
			if h.sc.Snapshot().TrigSource == 1 {
				src = "C2"
			}
			c.Reply("TRSE", "EDGE,SR,"+src+",HT,OFF")
			return nil
		}},
		// ARM_ACQUISITION: one single acquisition, as TRMD SINGLE.
		cmd{Pattern: "ARM", Handler: func(*core.Call) error {
			h.sc.SetSingle()
			h.trmd = "SINGLE"
			return nil
		}},
		// FORCE_TRIGGER: capture now, even while NORMAL or SINGLE waits.
		cmd{Pattern: "FRTR", Handler: func(*core.Call) error { return instrument{h}.Force() }},
		cmd{Pattern: "STOP", Handler: func(*core.Call) error {
			h.sc.SetRunning(false)
			h.trmd = "STOP"
			return nil
		}},
		cmd{Pattern: "ACQW", Handler: h.setACQW},
		cmd{Pattern: "ACQW?", Handler: func(c *core.Call) error {
			modes := [...]string{"SAMPLING", "AVERAGE", "ERES", "PEAK_DETECT", "PRECISION"}
			m := h.sc.Snapshot().AcqMode
			if m < 0 || m >= len(modes) {
				return c.Error(core.ErrDataOutOfRange)
			}
			c.Reply("ACQW", modes[m])
			return nil
		}},
		cmd{Pattern: "AVGA", Handler: func(c *core.Call) error {
			n, err := strconv.Atoi(c.Arg())
			if err != nil || n < 1 || n > 256 {
				return c.Error(core.ErrDataOutOfRange)
			}
			h.sc.SetAvgCount(n)
			return nil
		}},
		cmd{Pattern: "AVGA?", Handler: func(c *core.Call) error {
			c.Reply("AVGA", strconv.Itoa(h.sc.Snapshot().AvgCount))
			return nil
		}},
		cmd{Pattern: "SARA?", Handler: func(c *core.Call) error {
			var rate float64
			h.sc.WithFrame(func(f *sample.Frame) {
				if f != nil && f.SampleS > 0 {
					rate = 1 / f.SampleS
				}
			})
			c.Reply("SARA", saraStr(rate))
			return nil
		}},
		// Stop, Ready (NORMAL/SINGLE armed, nothing triggered lately), or
		// Trig'd - the same WAIT rule the LCD and web status use.
		cmd{Pattern: "SAST?", Handler: func(c *core.Call) error {
			c.Reply("SAST", instrument{h}.Acquisition().String())
			return nil
		}},
		cmd{Pattern: "SANU?", Handler: func(c *core.Call) error {
			n := 0
			h.sc.WithFrame(func(f *sample.Frame) {
				if f != nil {
					n = f.Len()
				}
			})
			c.Reply("SANU", strconv.Itoa(n))
			return nil
		}},
		cmd{Pattern: "WFSU", Handler: h.setWFSU},
		cmd{Pattern: "WFSU?", Handler: func(c *core.Call) error {
			c.Reply("WFSU", fmt.Sprintf("SP,%d,NP,%d,FP,%d,SN,%d", h.wfSP, h.wfNP, h.wfFP, h.wfSN))
			return nil
		}},
		cmd{Pattern: "SCDP", Handler: func(c *core.Call) error {
			if h.shot == nil {
				return c.Error(core.ErrUndefinedHeader)
			}
			// Rendering reads display state through Inverted(); external
			// callbacks run outside the command lock.
			var b []byte
			c.Unlocked(func() { b = h.shot() })
			c.Raw(b)
			return nil
		}},
		cmd{Pattern: "STPN", Handler: func(c *core.Call) error { return h.setupFile(c, "STPN") }},
		cmd{Pattern: "RCPN", Handler: func(c *core.Call) error { return h.setupFile(c, "RCPN") }},
		cmd{Pattern: "CRMS", Handler: h.setCRMS},
		cmd{Pattern: "CRMS?", Handler: func(c *core.Call) error {
			v := "OFF"
			if cm, ok := h.disp.(cursorMeasure); ok && cm.CursorsOn() {
				v = "MANUAL"
			}
			c.Reply("CRMS", v)
			return nil
		}},
		cmd{Pattern: "PACU", Handler: h.setPACU},
		// Cursor positions in vendor divisions and the binary panel-setup
		// block have no equivalent here: refuse rather than silently accept.
		cmd{Pattern: "CRST", Handler: refuse},
		cmd{Pattern: "PNSU", Handler: refuse},
	)
	d.Commands = append(d.Commands, h.channelCommands()...)
	d.Commands = append(d.Commands, cmd{Pattern: "C#-C#:MEAD?", Handler: func(c *core.Call) error {
		return h.mead(c, c.N(0)-1, c.N(1)-1, c.Arg())
	}})
	return d
}

// refuse is a header with no equivalent here: data out of range.
// TRLC-LINKS: REQ-SDS-024
func refuse(c *core.Call) error { return c.Error(core.ErrDataOutOfRange) }

// only accepts the first value (the fixed truth), refuses the other real
// values as out of range and anything else as a header error.
// TRLC-LINKS: REQ-SDS-024
func only(truth string, others ...string) core.Handler {
	return func(c *core.Call) error {
		arg := c.Arg()
		if arg == truth {
			return nil
		}
		if slices.Contains(others, arg) {
			return c.Error(core.ErrDataOutOfRange)
		}
		return c.Error(core.ErrHeader)
	}
}

// onOff answers ON/OFF under header.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) onOff(header string, on func() bool) core.Handler {
	return func(c *core.Call) error {
		v := "OFF"
		if on() {
			v = "ON"
		}
		c.Reply(header, v)
		return nil
	}
}

// reset is the Default Setup (spec 11 §3.3): every shadow returns to its
// power-on state — the same values New() seeds — and anything a shadow
// fronts (front-end coupling/probe, panel display state) is pushed back too,
// so the post-reset queries describe the REAL instrument, not just
// re-initialised bookkeeping.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) reset(c *core.Call) {
	h.sc.SetNorm(false)
	h.sc.SetRunning(true)
	h.sc.SetTdiv(500e-6)
	h.sc.SetTrigPosFrac(.5) // TRDL 0
	h.trmd = "AUTO"
	c.SetHeaderMode(core.HeaderShort)
	h.wfSP, h.wfNP, h.wfFP, h.wfSN = 1, 0, 0, 0
	h.invs.Store(0)
	h.unit = [2]string{"V", "V"}
	h.skew = [2]float64{}
	h.tra = [2]bool{true, true}
	h.cpl = [2]string{"D1M", "D1M"}
	h.attn = [2]float64{1, 1}
	if h.fe != nil {
		for ch := range 2 {
			_ = h.fe.SetCoupling(ch, analog.CplDC) // D1M
			h.fe.SetProbe(ch, 1)
		}
	}
	if h.disp != nil { // default display: Y-T view, persistence off
		h.disp.SetViewXY(false)
		h.disp.SetPersist(false)
	}
	// The rest of the default setup (V/div, offset, trigger, acquire mode,
	// math, cursors, zoom...) is the panel's DEFAULT SETUP, run on the
	// panel's own goroutine. It runs to completion before the next command:
	// queued, it would land after them ("*RST;TDIV 2US" ending at 500 us).
	// The panel may read display state through Inverted(), so wait unlocked.
	if p, ok := h.disp.(interface {
		InjectButtonWait(string, time.Duration) bool
	}); ok {
		c.Unlocked(func() { p.InjectButtonWait("defaultsetup", 5*time.Second) })
	}
	h.pendTdiv = 500e-6
}

// setup runs *SAV/*RCL: an error for anything the setup memory cannot do,
// never a silent success a controller takes for a restore.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setup(c *core.Call, head string, n int) error {
	if n < 1 || n > setupSlots || h.setups == nil {
		return c.Error(core.ErrDataOutOfRange)
	}
	var err error
	if head == "*SAV" {
		err = h.setups.SaveSetup(n)
	} else {
		err = h.setups.RecallSetup(n)
	}
	if err != nil {
		h.logf("scpi: %s %d: %v", head, n, err)
		return c.Error(core.ErrDataOutOfRange)
	}
	return nil
}

// setMenu: the softkey menu state lives in the panel controller, so SCPI
// and the LCD agree. Without a panel there is no menu at all: OFF matches
// reality, ON errors.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setMenu(c *core.Call) error {
	switch arg := c.Arg(); arg {
	case "ON", "OFF":
		if h.disp == nil {
			if arg == "ON" {
				return c.Error(core.ErrDataOutOfRange)
			}
			return nil
		}
		h.disp.SetMenuOpen(arg == "ON")
		return nil
	}
	return c.Error(core.ErrHeader)
}

// setPersist: the panel's afterglow is a boolean (OFF/INFINITE); the
// vendor's timed decays (1/2/5 s...) do not exist here → error. Without a
// panel there is no persistence at all: OFF is the fixed truth, INFINITE
// errors.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setPersist(c *core.Call) error {
	switch c.Arg() {
	case "OFF":
		if h.disp != nil {
			h.disp.SetPersist(false)
		}
		return nil
	case "INFINITE":
		if h.disp == nil {
			return c.Error(core.ErrDataOutOfRange)
		}
		h.disp.SetPersist(true)
		return nil
	case "1", "2", "5", "10", "20":
		return c.Error(core.ErrDataOutOfRange)
	}
	return c.Error(core.ErrHeader)
}

// setXY: the X-Y view lives in the panel controller (DISPLAY menu "View").
// Without a panel the view is fixed Y-T: OFF matches reality, ON errors.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setXY(c *core.Call) error {
	switch c.Arg() {
	case "ON":
		if h.disp == nil {
			return c.Error(core.ErrDataOutOfRange)
		}
		h.disp.SetViewXY(true)
		return nil
	case "OFF":
		if h.disp != nil {
			h.disp.SetViewXY(false)
		}
		return nil
	}
	return c.Error(core.ErrHeader)
}

// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setTdiv(c *core.Call) error {
	v, err := parseNum(c.Arg())
	if err != nil {
		return c.Error(core.ErrHeader)
	}
	b, ok := h.sc.SetTdiv(v)
	if !ok {
		return c.Error(core.ErrDataOutOfRange)
	}
	h.pendTdiv = b.TdivS
	return nil
}

// setTRDL: TRDL is the trigger's offset from the screen centre: the guide's
// time formula t = -TRDL - 5·TDIV + i/SARA puts a positive delay's trigger
// right of centre. It drives the real trigger position, which holds the
// screen's span only.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setTRDL(c *core.Call) error {
	v, err := parseNum(c.Arg())
	if err != nil {
		return c.Error(core.ErrHeader)
	}
	frac := .5 + v/(10*h.sc.Snapshot().TdivS)
	if !(frac >= -1e-9 && frac <= 1+1e-9) {
		return c.Error(core.ErrDataOutOfRange)
	}
	h.sc.SetTrigPosFrac(math.Min(1, math.Max(0, frac)))
	return nil
}

// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setTRMD(c *core.Call) error {
	arg := c.Arg()
	switch arg {
	case "AUTO":
		h.sc.SetNorm(false)
		h.sc.SetRunning(true)
	case "NORM":
		h.sc.SetNorm(true)
		h.sc.SetRunning(true)
	case "SINGLE":
		h.sc.SetSingle() // true single-shot
	case "STOP":
		h.sc.SetRunning(false)
	default:
		return c.Error(core.ErrHeader)
	}
	h.trmd = arg
	return nil
}

// trmdQuery answers from the engine: RUN/STOP, SINGLE and AUTO/NORMAL change
// on the panel and the web too, which never touch h.trmd.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) trmdQuery(c *core.Call) error {
	st := h.sc.Snapshot()
	v := "AUTO"
	switch {
	case st.Single && st.Running:
		v = "SINGLE"
	case !st.Running:
		v = "STOP"
	case st.Norm:
		v = "NORM"
	}
	c.Reply("TRMD", v)
	return nil
}

// trigSource is the trigger source channel, 0 or 1.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) trigSource() int {
	if h.sc.Snapshot().TrigSource == 1 {
		return 1
	}
	return 0
}

// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setTRLV(c *core.Call) error {
	v, err := parseNum(c.Arg())
	if err != nil {
		return c.Error(core.ErrHeader)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return c.Error(core.ErrDataOutOfRange)
	}
	// The shadow holds the EFFECTIVE level — the volts the (possibly
	// clamped, always quantized) DAC step the board applied — so TRLV?
	// never echoes a level the comparator isn't at.
	h.trlvV = h.lv.SetTrigLevel(v, h.trigSource())
	return nil
}

// trlvQuery answers from the engine's live code: the level also changes
// from the panel and the web UI, which never touch this handler's shadow.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) trlvQuery(c *core.Call, header string) error {
	v := h.trlvV
	if lv, ok := h.lv.TrigLevel(h.trigSource()); ok {
		v = lv
	}
	c.Reply(header, sciV(v))
	return nil
}

// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setTRSL(c *core.Call) error {
	switch c.Arg() {
	case "POS":
		h.sc.SetTrigSlope(true)
	case "NEG":
		h.sc.SetTrigSlope(false)
	default:
		return c.Error(core.ErrHeader)
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-024
func (h *Handler) trslQuery(c *core.Call, header string) error {
	v := "NEG"
	if h.sc.Snapshot().TrigRising {
		v = "POS"
	}
	c.Reply(header, v)
	return nil
}

// setTRSE: EDGE,SR,<src>,... — the source is the token after "SR". C1/C2
// are the only routable sources on this build (two ADC lanes; EXT has no
// software-visible path — spec 05 §6), so a real-but-unroutable vendor
// source (EX/EX5/LINE) returns the §3.4 range error instead of silently
// keeping the current source; an unknown token is a grammar error.
// Everything besides the SR pair is accepted as-is.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setTRSE(c *core.Call) error {
	toks := strings.Split(c.Arg(), ",")
	for i, tk := range toks {
		if strings.TrimSpace(tk) != "SR" {
			continue
		}
		if i+1 >= len(toks) {
			return c.Error(core.ErrHeader)
		}
		switch strings.TrimSpace(toks[i+1]) {
		case "C1":
			h.sc.SetTrigSource(0)
		case "C2":
			h.sc.SetTrigSource(1)
		case "EX", "EX5", "EX10", "LINE":
			return c.Error(core.ErrDataOutOfRange)
		default:
			return c.Error(core.ErrHeader)
		}
		break
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setACQW(c *core.Call) error {
	switch strings.Split(c.Arg(), ",")[0] {
	case "SAMPLING", "SAMPLE":
		h.sc.SetAcqMode(engine.AcqNormal)
	case "PEAK_DETECT", "PEAK":
		h.sc.SetAcqMode(engine.AcqPeak)
	case "AVERAGE", "AVG":
		h.sc.SetAcqMode(engine.AcqAverage)
	case "ERES":
		h.sc.SetAcqMode(engine.AcqEres)
	default:
		return c.Error(core.ErrHeader)
	}
	return nil
}

// setupFile stores or recalls the setup as a named file on the U-disk:
// DISK,UDSK,FILE,'NAME.SET'.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setupFile(c *core.Call, head string) error {
	files, ok := h.setups.(SetupFiles)
	if !ok {
		return c.Error(core.ErrDataOutOfRange)
	}
	name, bad := setupFileName(c.Arg())
	if bad != 0 {
		return c.Error(bad)
	}
	var err error
	if head == "STPN" {
		err = files.SaveSetupFile(name)
	} else {
		err = files.RecallSetupFile(name)
	}
	if err != nil {
		h.logf("scpi: %s %s: %v", head, name, err)
		return c.Error(core.ErrDataOutOfRange)
	}
	return nil
}

// setCRMS: OFF / MANUAL are the panel's cursors; the tracking modes do not
// exist here.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setCRMS(c *core.Call) error {
	cm, ok := h.disp.(cursorMeasure)
	switch arg := c.Arg(); {
	case !ok:
		return c.Error(core.ErrDataOutOfRange)
	case arg == "OFF":
		cm.SetCursorsOn(false)
	case arg == "MANUAL":
		cm.SetCursorsOn(true)
	case arg == "TRACK" || arg == "HREL" || arg == "VREL":
		return c.Error(core.ErrDataOutOfRange)
	default:
		return c.Error(core.ErrHeader)
	}
	return nil
}

// setPACU: PACU <param>,<source> puts a measurement on the display: the
// measurement panel shows every parameter of the channels.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setPACU(c *core.Call) error {
	f := strings.Split(c.Arg(), ",")
	cm, ok := h.disp.(cursorMeasure)
	if len(f) != 2 {
		return c.Error(core.ErrHeader)
	}
	if _, known := meas.Lookup(strings.TrimSpace(f[0])); !known {
		return c.Error(core.ErrDataOutOfRange)
	}
	if src := strings.TrimSpace(f[1]); src != "C1" && src != "C2" {
		return c.Error(core.ErrHeaderSuffixRange)
	}
	if !ok {
		return c.Error(core.ErrDataOutOfRange)
	}
	cm.SetMeasOn(true)
	return nil
}

// setHCSU: hardcopy setup. SCDP always returns the BMP screen: accept that,
// refuse anything else.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setHCSU(c *core.Call) error {
	arg := c.Arg()
	parts := strings.Split(strings.ReplaceAll(arg, " ", ""), ",")
	if len(parts)%2 != 0 || arg == "" {
		return c.Error(core.ErrHeader)
	}
	for i := 0; i < len(parts); i += 2 {
		if parts[i] != "DEV" || parts[i+1] != "BMP" {
			return c.Error(core.ErrDataOutOfRange)
		}
	}
	return nil
}

// channelCommands are the Cn: headers.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) channelCommands() []cmd {
	ch := func(c *core.Call) int { return c.N(0) - 1 }
	// The programming guide's form is <source>:TRLV / <source>:TRSL. One
	// level and slope exist, the trigger source's; another channel has none
	// to set or report.
	trigSrc := func(run func(c *core.Call, header string) error, header string) core.Handler {
		return func(c *core.Call) error {
			if ch(c) != h.trigSource() {
				return c.Error(core.ErrDataOutOfRange)
			}
			return run(c, chHeader(ch(c), header))
		}
	}
	setter := func(set core.Handler) func(*core.Call, string) error {
		return func(c *core.Call, _ string) error { return set(c) }
	}
	return []cmd{
		{Pattern: "C#:TRLV", Handler: trigSrc(setter(h.setTRLV), "TRLV")},
		{Pattern: "C#:TRLV?", Handler: trigSrc(h.trlvQuery, "TRLV")},
		{Pattern: "C#:TRSL", Handler: trigSrc(setter(h.setTRSL), "TRSL")},
		{Pattern: "C#:TRSL?", Handler: trigSrc(h.trslQuery, "TRSL")},
		{Pattern: "C#:VDIV", Handler: func(c *core.Call) error {
			v, err := parseNum(c.Arg())
			if err != nil {
				return c.Error(core.ErrHeader)
			}
			if h.fe == nil {
				return c.Error(core.ErrDataOutOfRange)
			}
			// VDIV and OFST are probe-tip volts, like TRLV, PAVA? and the
			// WF? descriptor: with ATTN 10, "VDIV 5" is the 0.5 V/div BNC
			// range.
			if err := h.fe.SetVdivV(ch(c), v/h.probe(ch(c))); err != nil {
				return c.Error(core.ErrDataOutOfRange)
			}
			return nil
		}},
		{Pattern: "C#:VDIV?", Handler: func(c *core.Call) error {
			v := 1.0
			if h.fe != nil {
				v = h.fe.VdivV(ch(c))
			}
			c.Reply(chHeader(ch(c), "VDIV"), sciV(v*h.probe(ch(c))))
			return nil
		}},
		{Pattern: "C#:OFST", Handler: func(c *core.Call) error {
			v, err := parseNum(c.Arg())
			if err != nil {
				return c.Error(core.ErrHeader)
			}
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return c.Error(core.ErrDataOutOfRange)
			}
			v /= h.probe(ch(c)) // probe-tip volts → BNC volts
			if v < -40 || v > 40 {
				return c.Error(core.ErrDataOutOfRange)
			}
			h.lv.SetOffset(ch(c), v) // stages the DAC; the front end re-anchors it on V/div changes
			return nil
		}},
		{Pattern: "C#:OFST?", Handler: func(c *core.Call) error {
			v := h.lv.OffsetIn(ch(c), h.sc.Snapshot())
			c.Reply(chHeader(ch(c), "OFST"), sciV(v*h.probe(ch(c))))
			return nil
		}},
		// The trace's visibility is the panel's (CH1/CH2 keys): set and
		// report that, not a shadow the screen never saw.
		{Pattern: "C#:TRA", Handler: func(c *core.Call) error {
			on, ok := core.ParseOnOff(c.Arg())
			if !ok {
				return c.Error(core.ErrHeader)
			}
			h.tra[ch(c)] = on
			if d, ok := h.disp.(traceDisplay); ok {
				d.SetTraceOn(ch(c), on)
			}
			return nil
		}},
		{Pattern: "C#:TRA?", Handler: h.onOffCh("TRA", func(n int) bool {
			if d, ok := h.disp.(traceDisplay); ok {
				return d.TraceOn(n)
			}
			return h.tra[n]
		})},
		{Pattern: "C#:CPL", Handler: h.setCPL},
		{Pattern: "C#:CPL?", Handler: func(c *core.Call) error {
			// Live: the panel's CHANNEL page changes coupling too.
			v := h.cpl[ch(c)]
			if _, ok := h.fe.(liveFront); ok {
				v = [...]string{"D1M", "A1M", "GND"}[min(max(h.coupling(ch(c)), 0), 2)]
			}
			c.Reply(chHeader(ch(c), "CPL"), v)
			return nil
		}},
		{Pattern: "C#:ATTN", Handler: func(c *core.Call) error {
			v, err := parseNum(c.Arg())
			if err != nil || !(v > 0) || math.IsInf(v, 0) {
				return c.Error(core.ErrDataOutOfRange)
			}
			h.attn[ch(c)] = v
			if h.fe != nil {
				h.fe.SetProbe(ch(c), v) // probe attenuation is a display multiplier
			}
			return nil
		}},
		{Pattern: "C#:ATTN?", Handler: func(c *core.Call) error {
			// Live: the panel sets it too.
			c.Reply(chHeader(ch(c), "ATTN"), strconv.FormatFloat(h.probe(ch(c)), 'g', -1, 64))
			return nil
		}},
		{Pattern: "C#:BWL", Handler: h.setBWL},
		{Pattern: "C#:BWL?", Handler: h.onOffCh("BWL", func(n int) bool {
			bw, can := h.fe.(interface{ BWL(ch int) bool })
			return can && bw.BWL(n)
		})},
		// Vertical unit label (display/bookkeeping): real shadow state.
		{Pattern: "C#:UNIT", Handler: func(c *core.Call) error {
			switch arg := c.Arg(); arg {
			case "V", "A":
				h.unit[ch(c)] = arg
				return nil
			}
			return c.Error(core.ErrHeader)
		}},
		{Pattern: "C#:UNIT?", Handler: func(c *core.Call) error {
			c.Reply(chHeader(ch(c), "UNIT"), h.unit[ch(c)])
			return nil
		}},
		// Channel deskew (display/bookkeeping): real shadow state, echoed in
		// the §3.1 float grammar by SKEW?.
		{Pattern: "C#:SKEW", Handler: func(c *core.Call) error {
			v, err := parseNum(c.Arg())
			if err != nil {
				return c.Error(core.ErrHeader)
			}
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return c.Error(core.ErrDataOutOfRange)
			}
			h.skew[ch(c)] = v
			return nil
		}},
		{Pattern: "C#:SKEW?", Handler: func(c *core.Call) error {
			c.Reply(chHeader(ch(c), "SKEW"), sciS(h.skew[ch(c)]))
			return nil
		}},
		// Trace invert (display-level): this shadow is the single source of
		// truth — Inverted() feeds the web status snapshot and the LCD HUD,
		// and both render paths mirror the trace about the display centre.
		// Deliberately display-only: measurements, decode, math, X-Y/FFT and
		// the mask/zone tests keep the true captured polarity (hardware
		// scopes vary here; this clone pins the narrow, unsurprising meaning
		// — what you SEE flips, what is measured does not).
		{Pattern: "C#:INVS", Handler: func(c *core.Call) error {
			on, ok := core.ParseOnOff(c.Arg())
			if !ok {
				return c.Error(core.ErrHeader)
			}
			h.SetInverted(ch(c), on)
			return nil
		}},
		{Pattern: "C#:INVS?", Handler: h.onOffCh("INVS", h.inverted)},
		{Pattern: "C#:WF?", Handler: func(c *core.Call) error { return h.waveform(c, ch(c), c.Arg()) }},
		{Pattern: "C#:CRVA?", Handler: func(c *core.Call) error { return h.crva(c, ch(c), c.Arg()) }},
		{Pattern: "C#:PAVA?", Handler: func(c *core.Call) error { return h.pava(c, ch(c), c.Arg()) }},
	}
}

// onOffCh answers a channel's ON/OFF under Cn:header.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) onOffCh(header string, on func(ch int) bool) core.Handler {
	return func(c *core.Call) error {
		n := c.N(0) - 1
		v := "OFF"
		if on(n) {
			v = "ON"
		}
		c.Reply(chHeader(n, header), v)
		return nil
	}
}

// setCPL takes only the couplings the front end really has (spec 06 §6):
// A1M→AC, D1M→DC, GND→GND. This is a 1 MΩ-only input, so the 50 Ω vendor
// forms are real values with no hardware here → range error; any other
// token is a grammar error. The shadow (and thus CPL?) only ever holds a
// value that was actually applied.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setCPL(c *core.Call) error {
	ch := c.N(0) - 1
	mode := analog.CplDC
	switch arg := c.Arg(); arg {
	case "A1M":
		mode = analog.CplAC
	case "GND":
		mode = analog.CplGND
	case "D1M":
	case "A50", "D50":
		return c.Error(core.ErrDataOutOfRange)
	default:
		return c.Error(core.ErrHeader)
	}
	h.cpl[ch] = c.Arg()
	if h.fe != nil {
		_ = h.fe.SetCoupling(ch, mode)
	}
	return nil
}

// setBWL: the 20 MHz limit is the BWL relay (spec 06 §6); a front end
// without it keeps BWL fixed OFF, and ON errors rather than silently
// passing.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setBWL(c *core.Call) error {
	bw, can := h.fe.(interface {
		SetBWL(ch int, on bool) error
		BWL(ch int) bool
	})
	on, ok := core.ParseOnOff(c.Arg())
	switch {
	case !ok:
		return c.Error(core.ErrHeader)
	case !can && on:
		return c.Error(core.ErrDataOutOfRange)
	case !can:
		return nil
	}
	if err := bw.SetBWL(c.N(0)-1, on); err != nil {
		return c.Error(core.ErrDataOutOfRange)
	}
	return nil
}

// setINTS validates an INTS set against the fixed truth (GRID,100,TRACE,100
// — there is no intensity control on this build). keyword,value pairs in
// any order/subset, WFSU-style: a request for the fixed levels is a no-op
// success, any other level is a §3.4 range error, malformed input a grammar
// error.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setINTS(c *core.Call) error {
	parts := strings.Split(c.Arg(), ",")
	if len(parts)%2 != 0 {
		return c.Error(core.ErrHeader)
	}
	for i := 0; i+1 < len(parts); i += 2 {
		v, err := strconv.Atoi(strings.TrimSpace(parts[i+1]))
		if err != nil {
			return c.Error(core.ErrHeader)
		}
		switch strings.TrimSpace(parts[i]) {
		case "GRID", "TRACE":
			if v != 100 {
				return c.Error(core.ErrDataOutOfRange) // dimming isn't implemented — never silently "succeed"
			}
		default:
			return c.Error(core.ErrHeader)
		}
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setWFSU(c *core.Call) error {
	parts := strings.Split(c.Arg(), ",")
	if len(parts)%2 != 0 {
		return c.Error(core.ErrHeader)
	}
	for i := 0; i+1 < len(parts); i += 2 {
		v, err := strconv.Atoi(strings.TrimSpace(parts[i+1]))
		if err != nil {
			return c.Error(core.ErrHeader)
		}
		switch strings.TrimSpace(parts[i]) {
		case "SP":
			if v < 0 || v > 255 {
				return c.Error(core.ErrDataOutOfRange)
			}
			h.wfSP = max(v, 1)
		case "NP":
			if v < 0 || v > 81920 {
				return c.Error(core.ErrDataOutOfRange)
			}
			h.wfNP = v
		case "FP":
			if v < 0 || v > 81920 {
				return c.Error(core.ErrDataOutOfRange)
			}
			h.wfFP = v
		case "SN":
			if v < 0 || v > math.MaxInt32 {
				return c.Error(core.ErrDataOutOfRange)
			}
			h.wfSN = v
		case "TYPE":
			// accepted flag, no behavior pinned
		default:
			return c.Error(core.ErrHeader)
		}
	}
	return nil
}

// setupFileName takes STPN/RCPN's DISK,UDSK,FILE,'NAME.SET' and returns a
// safe file name (base name only, letters/digits/._-, .SET), or the error.
// TRLC-LINKS: REQ-SDS-024
func setupFileName(arg string) (string, int) {
	f := strings.SplitN(arg, ",", 4)
	if len(f) != 4 || strings.TrimSpace(f[0]) != "DISK" || strings.TrimSpace(f[1]) != "UDSK" || strings.TrimSpace(f[2]) != "FILE" {
		return "", core.ErrHeader
	}
	name := strings.Trim(strings.TrimSpace(f[3]), "'\"")
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	if len(name) < 5 || len(name) > 64 || !strings.HasSuffix(strings.ToUpper(name), ".SET") {
		return "", core.ErrDataOutOfRange
	}
	for _, r := range name {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '.' {
			return "", core.ErrDataOutOfRange
		}
	}
	return name, 0
}
