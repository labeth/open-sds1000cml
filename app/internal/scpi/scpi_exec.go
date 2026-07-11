package scpi

import (
	"fmt"
	"math"
	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"strconv"
	"strings"
)

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
		v, err := parseNum(arg)
		if err != nil {
			return errTok(errHeader)
		}
		h.trdlS = v
		return nil
	case "TRDL?":
		return h.reply("TRDL", sciS(h.trdlS))
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
		return h.reply("TRMD", h.trmd)
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
		codeF := 31434 - 938*v // per-detent cal when the front end is present
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
		return h.reply("TRLV", sciV(h.trlvV))
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
	case "ARM", "FRTR":
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
		return h.reply("ACQW", [...]string{"SAMPLING", "AVERAGE", "ERES", "PEAK_DETECT"}[st.AcqMode&3])
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
		s := "Ready"
		if !st.Running {
			s = "Stop"
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
		return h.shot()
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

func (h *Handler) execChannel(ch int, head, arg string) []byte {
	switch head {
	case "VDIV":
		v, err := parseNum(arg)
		if err != nil {
			return errTok(errHeader)
		}
		if h.fe == nil {
			return errTok(errOutOfRange)
		}
		idx, ok := analog.PlanVdiv(v)
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
		return h.reply(fmt.Sprintf("C%d:VDIV", ch+1), sciV(v))
	case "OFST":
		v, err := parseNum(arg)
		if err != nil {
			return errTok(errHeader)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) || v < -40 || v > 40 {
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
		return h.reply(fmt.Sprintf("C%d:OFST", ch+1), sciV(v))
	case "TRA":
		switch arg {
		case "ON":
			h.tra[ch] = true
		case "OFF":
			h.tra[ch] = false
		default:
			return errTok(errHeader)
		}
		return nil
	case "TRA?":
		v := "OFF"
		if h.tra[ch] {
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
		return h.reply(fmt.Sprintf("C%d:CPL", ch+1), h.cpl[ch])
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
		return h.reply(fmt.Sprintf("C%d:ATTN", ch+1), strconv.FormatFloat(h.attn[ch], 'g', -1, 64))
	case "BWL":
		// This build cannot engage the 20 MHz limit: only the relay-bit
		// write is pinned, the roll-off itself is unvalidated (spec 06 §6),
		// and the handler has no front-end hook for it. BWL is therefore
		// fixed OFF: setting OFF matches reality (and round-trips through
		// BWL?), setting ON must error — never silent success — per the
		// spec 11 §3.4 convention (rejected argument → error token).
		switch arg {
		case "OFF":
			return nil
		case "ON":
			return errTok(errOutOfRange)
		default:
			return errTok(errHeader)
		}
	case "BWL?":
		return h.reply(fmt.Sprintf("C%d:BWL", ch+1), "OFF")
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
	}
	return errTok(errUndefined)
}

// setINTS validates an INTS set against the fixed truth (GRID,100,TRACE,100 —
// there is no intensity control on this build). keyword,value pairs in any
// order/subset, WFSU-style: a request for the fixed levels is a no-op success,
// any other level is a §3.4 range error, malformed input a grammar error.
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
