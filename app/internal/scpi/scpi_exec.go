// ENGMODEL-OWNER-UNIT: FU-APP-SCPI
package scpi

import (
	"fmt"
	"math"
	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"strconv"
	"strings"
	"time"
)

// TRLC-LINKS: REQ-SDS-024
func (h *Handler) execGlobal(head, arg string) []byte {
	st := h.sc.Snapshot()
	switch head {
	case "*IDN?":
		// No header prefix; exactly 4 comma-separated fields.
		return []byte(fmt.Sprintf("Siglent,SDS1102CML+,%s,8.01.01.99R9\n", h.serial))
	case "*RST":
		// Default Setup (spec 11 §3.3): every shadow returns to its power-on
		// state — the same values New() seeds — and anything a shadow fronts
		// (front-end coupling/probe, panel display state) is pushed back too,
		// so the post-reset queries describe the REAL instrument, not just
		// re-initialised bookkeeping.
		h.sc.SetNorm(false)
		h.sc.SetRunning(true)
		h.sc.SetTdiv(500e-6)
		if sc, ok := h.sc.(interface{ SetTrigPosFrac(float64) }); ok {
			sc.SetTrigPosFrac(.5) // TRDL 0
		}
		h.trmd = "AUTO"
		h.chdr = "SHORT"
		h.wfSP, h.wfNP, h.wfFP, h.wfSN = 1, 0, 0, 0
		h.invs = [2]bool{}
		h.unit = [2]string{"V", "V"}
		h.skew = [2]float64{}
		h.tra = [2]bool{true, true}
		h.cpl = [2]string{"D1M", "D1M"}
		h.attn = [2]float64{1, 1}
		if h.fe != nil {
			for ch := 0; ch < 2; ch++ {
				_ = h.fe.SetCoupling(ch, analog.CplDC) // D1M
				h.fe.SetProbe(ch, 1)
			}
		}
		if h.disp != nil { // default display: Y-T view, persistence off
			h.disp.SetViewXY(false)
			h.disp.SetPersist(false)
		}
		return nil
	case "*CLS", "*OPC", "*WAI", "*SAV", "*RCL", "*ESE", "*SRE":
		return nil // accepted, silent (status/setup-memory stubs)
	case "BUZZ":
		// No buzzer driver exists in this firmware: OFF is the fixed truth,
		// BUZZ ON must error rather than silently succeed (the BWL rule).
		switch arg {
		case "OFF":
			return nil
		case "ON":
			return errTok(errOutOfRange)
		default:
			return errTok(errHeader)
		}
	case "BUZZ?":
		return h.reply("BUZZ", "OFF")
	case "MENU":
		// The softkey menu state lives in the panel controller — wire the
		// set/query to it so SCPI and the LCD agree. Without a panel there
		// is no menu at all: OFF matches reality, ON errors.
		switch arg {
		case "ON", "OFF":
			if h.disp == nil {
				if arg == "ON" {
					return errTok(errOutOfRange)
				}
				return nil
			}
			h.disp.SetMenuOpen(arg == "ON")
			return nil
		default:
			return errTok(errHeader)
		}
	case "MENU?":
		v := "OFF"
		if h.disp != nil && h.disp.MenuOpen() {
			v = "ON"
		}
		return h.reply("MENU", v)
	case "GRDS":
		// Grid display is fixed FULL — the renderer has no half/off
		// graticule mode. FULL round-trips; HALF/OFF are real vendor values
		// with no implementation here → error, never silent (the BWL rule).
		switch arg {
		case "FULL":
			return nil
		case "HALF", "OFF":
			return errTok(errOutOfRange)
		default:
			return errTok(errHeader)
		}
	case "GRDS?":
		return h.reply("GRDS", "FULL")
	case "INTS":
		// No intensity control exists (grid and trace render at full drive),
		// so the fixed truth is GRID,100,TRACE,100. A set is accepted only
		// when it requests exactly that state; any other level errors.
		return h.setINTS(arg)
	case "INTS?":
		return h.reply("INTS", "GRID,100,TRACE,100")
	case "PESU":
		// Persistence: the panel's afterglow is a boolean (OFF/INFINITE);
		// the vendor's timed decays (1/2/5 s...) do not exist here → error.
		// Without a panel there is no persistence at all: OFF is the fixed
		// truth, INFINITE errors.
		switch arg {
		case "OFF":
			if h.disp != nil {
				h.disp.SetPersist(false)
			}
			return nil
		case "INFINITE":
			if h.disp == nil {
				return errTok(errOutOfRange)
			}
			h.disp.SetPersist(true)
			return nil
		case "1", "2", "5", "10", "20":
			return errTok(errOutOfRange)
		default:
			return errTok(errHeader)
		}
	case "PESU?":
		v := "OFF"
		if h.disp != nil && h.disp.PersistOn() {
			v = "INFINITE"
		}
		return h.reply("PESU", v)
	case "*OPC?":
		return []byte("1\n")
	case "*STB?", "*ESR?", "INR?", "CMR?":
		return h.reply(strings.TrimSuffix(head, "?"), "0")
	case "*TST?", "*CAL?":
		return h.reply(strings.TrimSuffix(head, "?"), "0")
	case "CHDR":
		switch arg {
		case "OFF":
			h.chdr = "OFF"
		case "ON", "SHORT", "LONG":
			h.chdr = "SHORT"
		default:
			return errTok(errHeader)
		}
		return nil
	case "CHDR?":
		return h.reply("CHDR", h.chdr)
	case "TDIV":
		v, err := parseNum(arg)
		if err != nil {
			return errTok(errHeader)
		}
		if _, ok := h.sc.SetTdiv(v); !ok {
			return errTok(errOutOfRange)
		}
		return nil
	case "TDIV?":
		return h.reply("TDIV", sciS(st.TdivS))
	case "TRDL":
		// TRDL is the trigger's offset from the screen centre: the guide's
		// time formula t = -TRDL - 5·TDIV + i/SARA puts a positive delay's
		// trigger right of centre. It drives the real trigger position,
		// which holds the screen's span only.
		v, err := parseNum(arg)
		if err != nil {
			return errTok(errHeader)
		}
		sc, ok := h.sc.(interface{ SetTrigPosFrac(float64) })
		frac := .5 + v/(10*st.TdivS)
		if !ok || !(frac >= -1e-9 && frac <= 1+1e-9) {
			return errTok(errOutOfRange)
		}
		sc.SetTrigPosFrac(math.Min(1, math.Max(0, frac)))
		return nil
	case "TRDL?":
		frac := st.TrigPosFrac
		if !(frac >= 0 && frac <= 1) {
			frac = .5
		}
		return h.reply("TRDL", sciS((frac-.5)*10*st.TdivS))
	case "TRMD":
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
			return errTok(errHeader)
		}
		h.trmd = arg
		return nil
	case "TRMD?":
		// From the engine: RUN/STOP, SINGLE and AUTO/NORMAL change on the
		// panel and the web too, which never touch h.trmd.
		v := "AUTO"
		switch {
		case st.Single && st.Running:
			v = "SINGLE"
		case !st.Running:
			v = "STOP"
		case st.Norm:
			v = "NORM"
		}
		return h.reply("TRMD", v)
	case "TRLV":
		v, err := parseNum(arg)
		if err != nil {
			return errTok(errHeader)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errTok(errOutOfRange)
		}
		src := 0
		if h.sc.Snapshot().TrigSource == 1 {
			src = 1
		}
		codeF := 31437 - 911*v // measured global fit; overridden by the front-end cal below
		if h.fe != nil {
			codeF = h.fe.TrigCode(v, src)
		}
		code := int(math.Round(codeF))
		if code < engine.TrigCodeMin {
			code = engine.TrigCodeMin
		}
		if code > engine.TrigCodeMax {
			code = engine.TrigCodeMax
		}
		// The shadow holds the EFFECTIVE level — the volts the (possibly
		// clamped, always quantized) DAC code the engine accepted maps back
		// to — so TRLV? never echoes a level the comparator isn't at.
		if eff := h.sc.SetTrigLevelCode(uint16(code)); eff != 0 {
			if h.fe != nil {
				h.trlvV = h.fe.TrigVolts(eff, src)
			} else {
				h.trlvV = engine.TrigLevelVolts(eff)
			}
		}
		return nil
	case "TRLV?":
		// Answer from the engine's live code: the level also changes from the
		// panel and the web UI, which never touch this handler's shadow.
		v := h.trlvV
		if st.TrigCode != 0 {
			src := 0
			if st.TrigSource == 1 {
				src = 1
			}
			if h.fe != nil {
				v = h.fe.TrigVolts(st.TrigCode, src)
			} else {
				v = engine.TrigLevelVolts(st.TrigCode)
			}
		}
		return h.reply("TRLV", sciV(v))
	case "TRSL":
		switch arg {
		case "POS":
			h.sc.SetTrigSlope(true)
		case "NEG":
			h.sc.SetTrigSlope(false)
		default:
			return errTok(errHeader)
		}
		return nil
	case "TRSL?":
		if st.TrigRising {
			return h.reply("TRSL", "POS")
		}
		return h.reply("TRSL", "NEG")
	case "TRSE":
		// EDGE,SR,<src>,... — the source is the token after "SR". C1/C2 are
		// the only routable sources on this build (two ADC lanes; EXT has no
		// software-visible path — spec 05 §6), so a real-but-unroutable
		// vendor source (EX/EX5/LINE) returns the §3.4 range error instead
		// of silently keeping the current source; an unknown token is a
		// grammar error. Everything besides the SR pair is accepted as-is.
		toks := strings.Split(arg, ",")
		for i, tk := range toks {
			if strings.TrimSpace(tk) != "SR" {
				continue
			}
			if i+1 >= len(toks) {
				return errTok(errHeader)
			}
			switch strings.TrimSpace(toks[i+1]) {
			case "C1":
				h.sc.SetTrigSource(0)
			case "C2":
				h.sc.SetTrigSource(1)
			case "EX", "EX5", "EX10", "LINE":
				return errTok(errOutOfRange)
			default:
				return errTok(errHeader)
			}
			break
		}
		return nil
	case "TRSE?":
		src := "C1"
		if st.TrigSource == 1 {
			src = "C2"
		}
		return h.reply("TRSE", "EDGE,SR,"+src+",HT,OFF")
	case "TRCP":
		// Trigger coupling is fixed DC on this build (no engine control).
		// Accept only the state that is true; any other request must error,
		// never silently succeed while TRCP? keeps answering DC.
		switch arg {
		case "DC":
			return nil
		case "AC", "HFREJ", "LFREJ":
			return errTok(errOutOfRange)
		default:
			return errTok(errHeader)
		}
	case "TRCP?":
		return h.reply("TRCP", "DC")
	case "ARM":
		// ARM_ACQUISITION: one single acquisition, as TRMD SINGLE.
		h.sc.SetSingle()
		h.trmd = "SINGLE"
		return nil
	case "FRTR":
		// FORCE_TRIGGER: capture now, even while NORMAL or SINGLE waits.
		if sc, ok := h.sc.(interface{ ForceTrigger() }); ok {
			sc.ForceTrigger()
			return nil
		}
		h.sc.SetRunning(true)
		return nil
	case "STOP":
		h.sc.SetRunning(false)
		h.trmd = "STOP"
		return nil
	case "ACQW":
		switch strings.Split(arg, ",")[0] {
		case "SAMPLING", "SAMPLE":
			h.sc.SetAcqMode(engine.AcqNormal)
		case "PEAK_DETECT", "PEAK":
			h.sc.SetAcqMode(engine.AcqPeak)
		case "AVERAGE", "AVG":
			h.sc.SetAcqMode(engine.AcqAverage)
		case "ERES":
			h.sc.SetAcqMode(engine.AcqEres)
		default:
			return errTok(errHeader)
		}
		return nil
	case "ACQW?":
		modes := [...]string{"SAMPLING", "AVERAGE", "ERES", "PEAK_DETECT", "PRECISION"}
		if st.AcqMode < 0 || st.AcqMode >= len(modes) {
			return errTok(errOutOfRange)
		}
		return h.reply("ACQW", modes[st.AcqMode])
	case "AVGA":
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 || n > 256 {
			return errTok(errOutOfRange)
		}
		h.sc.SetAvgCount(n)
		return nil
	case "AVGA?":
		return h.reply("AVGA", strconv.Itoa(st.AvgCount))
	case "SARA?":
		var rate float64
		h.sc.WithFrame(func(f *engine.Frame) {
			if f != nil && f.SampleS > 0 {
				rate = 1 / f.SampleS
			}
		})
		return h.reply("SARA", saraStr(rate))
	case "SAST?":
		// Stop, Ready (NORMAL/SINGLE armed, nothing triggered lately), or
		// Trig'd - the same WAIT rule the LCD and web status use.
		s := "Trig'd"
		if !st.Running {
			s = "Stop"
		} else if sp, ok := h.sc.(interface{ SincePublish() time.Duration }); ok && st.Norm &&
			sp.SincePublish() > time.Duration(math.Max(1, 25*st.TdivS)*float64(time.Second)) {
			s = "Ready"
		}
		return h.reply("SAST", s)
	case "SANU?":
		n := 0
		h.sc.WithFrame(func(f *engine.Frame) {
			if f != nil {
				n = f.Valid
			}
		})
		return h.reply("SANU", strconv.Itoa(n))
	case "WFSU":
		return h.setWFSU(arg)
	case "WFSU?":
		return h.reply("WFSU", fmt.Sprintf("SP,%d,NP,%d,FP,%d,SN,%d", h.wfSP, h.wfNP, h.wfFP, h.wfSN))
	case "SCDP":
		if h.shot == nil {
			return errTok(errUndefined)
		}
		// Rendering reads display state through Inverted(), which takes mu.
		// External callbacks must run outside the command-state lock.
		return func() []byte {
			h.mu.Unlock()
			defer h.mu.Lock()
			return h.shot()
		}()
	case "XYDS":
		// X-Y display: the state lives in the panel controller (DISPLAY menu
		// "View") — wire set/query there so the LCD and SCPI agree. Without
		// a panel the view is fixed Y-T: OFF matches reality, ON errors.
		switch arg {
		case "ON":
			if h.disp == nil {
				return errTok(errOutOfRange)
			}
			h.disp.SetViewXY(true)
			return nil
		case "OFF":
			if h.disp != nil {
				h.disp.SetViewXY(false)
			}
			return nil
		default:
			return errTok(errHeader)
		}
	case "XYDS?":
		v := "OFF"
		if h.disp != nil && h.disp.ViewXY() {
			v = "ON"
		}
		return h.reply("XYDS", v)
	case "PACU", "CRMS", "CRST", "PNSU", "STPN", "RCPN", "HCSU":
		return nil // accepted stubs (measure/cursor/panel-memory: no query form to contradict)
	case "SRLN?":
		return h.reply("SRLN", "Default")
	}
	if strings.HasPrefix(head, "SGLT") || strings.HasPrefix(head, "IDN-SGLT") ||
		strings.HasPrefix(head, "MD5_") || head == "MAC_GET" || strings.HasPrefix(head, "LOAD:") {
		return errTok(errUndefined) // maintenance/upgrade: out of scope, never implement
	}
	return errTok(errUndefined)
}

// TRLC-LINKS: REQ-SDS-024
func (h *Handler) execChannel(ch int, head, arg string) []byte {
	switch head {
	case "TRLV", "TRLV?", "TRSL", "TRSL?":
		// The programming guide's form is <source>:TRLV / <source>:TRSL. One
		// level and slope exist, the trigger source's; another channel has
		// none to set or report.
		src := 0
		if h.sc.Snapshot().TrigSource == 1 {
			src = 1
		}
		if ch != src {
			return errTok(errOutOfRange)
		}
		out := h.execGlobal(head, arg)
		if strings.HasSuffix(head, "?") && h.chdr != "OFF" {
			out = append([]byte(fmt.Sprintf("C%d:", ch+1)), out...)
		}
		return out
	case "VDIV":
		v, err := parseNum(arg)
		if err != nil {
			return errTok(errHeader)
		}
		if h.fe == nil {
			return errTok(errOutOfRange)
		}
		// VDIV and OFST are probe-tip volts, like TRLV, PAVA? and the WF?
		// descriptor: with ATTN 10, "VDIV 5" is the 0.5 V/div BNC range.
		idx, ok := analog.PlanVdiv(v / h.probe(ch))
		if !ok {
			return errTok(errOutOfRange)
		}
		if err := h.fe.SetVdiv(ch, idx); err != nil {
			return errTok(errOutOfRange)
		}
		return nil
	case "VDIV?":
		v := 1.0
		if h.fe != nil {
			idx, _ := h.fe.Snapshot()
			v = analog.Detents[idx[ch]].VdivV
		}
		return h.reply(fmt.Sprintf("C%d:VDIV", ch+1), sciV(v*h.probe(ch)))
	case "OFST":
		v, err := parseNum(arg)
		if err != nil {
			return errTok(errHeader)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errTok(errOutOfRange)
		}
		v /= h.probe(ch) // probe-tip volts → BNC volts
		if v < -40 || v > 40 {
			return errTok(errOutOfRange)
		}
		if h.fe != nil {
			h.fe.SetOffset(ch, v) // stages the DAC + re-anchors on V/div change
		} else {
			h.sc.SetOffsetDAC(ch, analog.OffsetCode(ch, v))
		}
		return nil
	case "OFST?":
		st := h.sc.Snapshot()
		code := st.OffC1
		if ch == 1 {
			code = st.OffC2
		}
		v := 0.0
		if code != 0 {
			if h.fe != nil {
				v = h.fe.OffsetVolts(ch, code)
			} else {
				v = analog.OffsetVolts(ch, code)
			}
		}
		return h.reply(fmt.Sprintf("C%d:OFST", ch+1), sciV(v*h.probe(ch)))
	case "TRA":
		// The trace's visibility is the panel's (CH1/CH2 keys): set and
		// report that, not a shadow the screen never saw.
		switch arg {
		case "ON":
			h.tra[ch] = true
		case "OFF":
			h.tra[ch] = false
		default:
			return errTok(errHeader)
		}
		if d, ok := h.disp.(traceDisplay); ok {
			d.SetTraceOn(ch, h.tra[ch])
		}
		return nil
	case "TRA?":
		on := h.tra[ch]
		if d, ok := h.disp.(traceDisplay); ok {
			on = d.TraceOn(ch)
		}
		v := "OFF"
		if on {
			v = "ON"
		}
		return h.reply(fmt.Sprintf("C%d:TRA", ch+1), v)
	case "CPL":
		// Only the couplings the front end really has (spec 06 §6): A1M→AC,
		// D1M→DC, GND→GND. This is a 1 MΩ-only input, so the 50 Ω vendor
		// forms are real values with no hardware here → range error; any
		// other token is a grammar error. The shadow (and thus CPL?) only
		// ever holds a value that was actually applied — garbage no longer
		// echoes back while the front end silently ran DC.
		mode := analog.CplDC
		switch arg {
		case "A1M":
			mode = analog.CplAC
		case "GND":
			mode = analog.CplGND
		case "D1M":
		case "A50", "D50":
			return errTok(errOutOfRange)
		default:
			return errTok(errHeader)
		}
		h.cpl[ch] = arg
		if h.fe != nil {
			_ = h.fe.SetCoupling(ch, mode)
		}
		return nil
	case "CPL?":
		// Live: the panel's CHANNEL page changes coupling too.
		v := h.cpl[ch]
		if _, ok := h.fe.(liveFront); ok {
			v = [...]string{"D1M", "A1M", "GND"}[min(max(h.coupling(ch), 0), 2)]
		}
		return h.reply(fmt.Sprintf("C%d:CPL", ch+1), v)
	case "ATTN":
		v, err := parseNum(arg)
		if err != nil || v <= 0 {
			return errTok(errOutOfRange)
		}
		h.attn[ch] = v
		if h.fe != nil {
			h.fe.SetProbe(ch, v) // probe attenuation is a display multiplier
		}
		return nil
	case "ATTN?":
		return h.reply(fmt.Sprintf("C%d:ATTN", ch+1), strconv.FormatFloat(h.probe(ch), 'g', -1, 64)) // live: the panel sets it too
	case "BWL":
		// The 20 MHz limit is the BWL relay (spec 06 §6); a front end without
		// it keeps BWL fixed OFF, and ON errors rather than silently passing.
		bw, can := h.fe.(interface {
			SetBWL(ch int, on bool) error
			BWL(ch int) bool
		})
		switch arg {
		case "ON", "OFF":
			if !can {
				if arg == "OFF" {
					return nil
				}
				return errTok(errOutOfRange)
			}
			if err := bw.SetBWL(ch, arg == "ON"); err != nil {
				return errTok(errOutOfRange)
			}
			return nil
		default:
			return errTok(errHeader)
		}
	case "BWL?":
		v := "OFF"
		if bw, can := h.fe.(interface{ BWL(ch int) bool }); can && bw.BWL(ch) {
			v = "ON"
		}
		return h.reply(fmt.Sprintf("C%d:BWL", ch+1), v)
	case "UNIT":
		// Vertical unit label (display/bookkeeping): real shadow state.
		switch arg {
		case "V", "A":
			h.unit[ch] = arg
			return nil
		default:
			return errTok(errHeader)
		}
	case "UNIT?":
		return h.reply(fmt.Sprintf("C%d:UNIT", ch+1), h.unit[ch])
	case "SKEW":
		// Channel deskew (display/bookkeeping): real shadow state, echoed
		// in the §3.1 float grammar by SKEW?.
		v, err := parseNum(arg)
		if err != nil {
			return errTok(errHeader)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errTok(errOutOfRange)
		}
		h.skew[ch] = v
		return nil
	case "SKEW?":
		return h.reply(fmt.Sprintf("C%d:SKEW", ch+1), sciS(h.skew[ch]))
	case "INVS":
		// Trace invert (display-level): this shadow is the single source of
		// truth — Inverted() feeds the web status snapshot and the LCD HUD,
		// and both render paths mirror the trace about the display centre.
		// Deliberately display-only: measurements, decode, math, X-Y/FFT and
		// the mask/zone tests keep the true captured polarity (hardware
		// scopes vary here; this clone pins the narrow, unsurprising meaning
		// — what you SEE flips, what is measured does not).
		switch arg {
		case "ON":
			h.invs[ch] = true
			return nil
		case "OFF":
			h.invs[ch] = false
			return nil
		default:
			return errTok(errHeader)
		}
	case "INVS?":
		v := "OFF"
		if h.invs[ch] {
			v = "ON"
		}
		return h.reply(fmt.Sprintf("C%d:INVS", ch+1), v)
	case "WF?":
		return h.waveform(ch, arg)
	case "PAVA?":
		return h.pava(ch, arg)
	}
	return errTok(errUndefined)
}

// setINTS validates an INTS set against the fixed truth (GRID,100,TRACE,100 —
// there is no intensity control on this build). keyword,value pairs in any
// order/subset, WFSU-style: a request for the fixed levels is a no-op success,
// any other level is a §3.4 range error, malformed input a grammar error.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setINTS(arg string) []byte {
	parts := strings.Split(arg, ",")
	if len(parts)%2 != 0 {
		return errTok(errHeader)
	}
	for i := 0; i+1 < len(parts); i += 2 {
		v, err := strconv.Atoi(strings.TrimSpace(parts[i+1]))
		if err != nil {
			return errTok(errHeader)
		}
		switch strings.TrimSpace(parts[i]) {
		case "GRID", "TRACE":
			if v < 0 || v > 100 {
				return errTok(errOutOfRange)
			}
			if v != 100 {
				return errTok(errOutOfRange) // dimming isn't implemented — never silently "succeed"
			}
		default:
			return errTok(errHeader)
		}
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-024
func (h *Handler) setWFSU(arg string) []byte {
	parts := strings.Split(arg, ",")
	if len(parts)%2 != 0 {
		return errTok(errHeader)
	}
	for i := 0; i+1 < len(parts); i += 2 {
		v, err := strconv.Atoi(strings.TrimSpace(parts[i+1]))
		if err != nil {
			return errTok(errHeader)
		}
		switch strings.TrimSpace(parts[i]) {
		case "SP":
			if v < 0 || v > 255 {
				return errTok(errOutOfRange)
			}
			if v < 1 {
				v = 1
			}
			h.wfSP = v
		case "NP":
			if v < 0 || v > 81920 {
				return errTok(errOutOfRange)
			}
			h.wfNP = v
		case "FP":
			if v < 0 || v > 81920 {
				return errTok(errOutOfRange)
			}
			h.wfFP = v
		case "SN":
			if v < 0 {
				return errTok(errOutOfRange)
			}
			h.wfSN = v
		case "TYPE":
			// accepted flag, no behavior pinned
		default:
			return errTok(errHeader)
		}
	}
	return nil
}
