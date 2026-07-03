// Package scpi implements the LeCroy/Siglent short-form SCPI set (spec 11
// §3): one \n-terminated line in, reply bytes out. Transport-agnostic —
// VXI-11 feeds it today; a USB-TMC pump can feed the same HandleLine later.
// The parser is a pure producer/consumer: setters stage, queries snapshot;
// it NEVER touches the GPMC bus.
package scpi

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// Scope is the instrument surface (engine + fan-out frames).
type Scope interface {
	Snapshot() engine.Stats
	WithFrame(fn func(*engine.Frame))
	SetRunning(on bool)
	SetNorm(on bool)
	SetSingle()
	SetTdiv(tdivS float64) (engine.Band, bool)
	SetTrigLevelCode(code uint16) uint16
	SetTrigSlope(rising bool)
	SetTrigSource(ch int)
	SetOffsetDAC(ch int, code uint16)
	SetAcqMode(m int)
	SetAvgCount(n int)
}

// Analog is the vertical front end; may be nil.
type Analog interface {
	SetVdiv(ch, idx int) error
	Snapshot() (idx [2]int, emitted bool)
	SetOffset(ch int, volts float64) uint16
	OffsetVolts(ch int, code uint16) float64
	SetProbe(ch int, x float64)
}

// Screenshot returns the SCDP hardcopy payload (BMP). Wired by main to a
// headless render of the current frame; may be nil.
type Screenshot func() []byte

// Error tokens (spec 11 §3.4) — emitted exactly, \n-terminated.
const (
	errUndefined  = "Undefined header"
	errHeader     = "Command header error"
	errSuffix     = "Header suffix out of range"
	errOutOfRange = "Data out of range"
)

type Handler struct {
	sc   Scope
	fe   Analog
	shot Screenshot
	logf func(string, ...any)

	chdr string // "SHORT" | "OFF" (power-on default SHORT)
	wfSP int
	wfNP int
	wfFP int
	wfSN int

	// Instrument-state shadows for echo-back of controls with no engine
	// representation yet.
	tra    [2]bool
	cpl    [2]string
	attn   [2]float64
	bwl    [2]bool
	invs   [2]bool
	trmd   string
	trlvV  float64
	trdlS  float64
	serial string
}

func New(sc Scope, fe Analog, shot Screenshot, logf func(string, ...any)) *Handler {
	return &Handler{
		sc: sc, fe: fe, shot: shot, logf: logf,
		chdr: "SHORT", wfSP: 1,
		tra:    [2]bool{true, true},
		cpl:    [2]string{"D1M", "D1M"},
		attn:   [2]float64{1, 1},
		trmd:   "AUTO",
		serial: loadSerial(),
	}
}

func loadSerial() string {
	// Best-effort: the serial lives in usr/system/system_info.dat as
	// printable ASCII (exact offset unpinned by spec 11).
	if b, err := os.ReadFile("/usr/bin/siglent/usr/system/system_info.dat"); err == nil {
		run := []byte{}
		for _, c := range b {
			if c >= 0x20 && c < 0x7f {
				run = append(run, c)
				if len(run) >= 14 && strings.HasPrefix(string(run), "SDS") {
					return string(run)
				}
			} else {
				run = run[:0]
			}
		}
	}
	return "SDS00000000000"
}

// HandleLine executes one \n-terminated line (possibly ';'-separated
// compound commands) and returns the concatenated reply bytes. Pure setters
// are silent (spec 11: no OK acknowledgements).
func (h *Handler) HandleLine(line []byte) []byte {
	var out []byte
	for _, part := range strings.Split(strings.TrimRight(string(line), "\r\n"), ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, h.exec(part)...)
	}
	return out
}

// reply formats a query answer honoring the CHDR state.
func (h *Handler) reply(header, value string) []byte {
	if h.chdr == "OFF" {
		return []byte(value + "\n")
	}
	return []byte(header + " " + value + "\n")
}

func errTok(tok string) []byte { return []byte(tok + "\n") }

// sciV formats volts per the reply grammar: %.2E + upper-case V.
func sciV(v float64) string { return fmt.Sprintf("%.2EV", v) }

// sciS formats seconds: %.2E + LOWER-case s.
func sciS(v float64) string { return fmt.Sprintf("%.2Es", v) }

// saraStr is the SI-prefix exception: e.g. 12500 → "12.50KSa".
func saraStr(rate float64) string {
	switch {
	case rate >= 1e9:
		return fmt.Sprintf("%.2fGSa", rate/1e9)
	case rate >= 1e6:
		return fmt.Sprintf("%.2fMSa", rate/1e6)
	case rate >= 1e3:
		return fmt.Sprintf("%.2fKSa", rate/1e3)
	default:
		return fmt.Sprintf("%.2fSa", rate)
	}
}

// exec runs one command (no ';').
func (h *Handler) exec(cmd string) []byte {
	up := strings.ToUpper(cmd)

	// Channel prefix Cn: (C1/C2 only; anything else is a suffix error).
	ch := -1
	rest := up
	if len(up) >= 3 && up[0] == 'C' && up[2] == ':' && up[1] >= '0' && up[1] <= '9' {
		n := int(up[1] - '0')
		if n != 1 && n != 2 {
			return errTok(errSuffix)
		}
		ch = n - 1
		rest = up[3:]
	}

	head, arg := rest, ""
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		head, arg = rest[:i], strings.TrimSpace(rest[i+1:])
	}
	if head == "" || strings.HasSuffix(head, ":") {
		return errTok("Header separator error")
	}

	if ch >= 0 {
		return h.execChannel(ch, head, arg)
	}
	return h.execGlobal(head, arg)
}

func (h *Handler) execGlobal(head, arg string) []byte {
	st := h.sc.Snapshot()
	switch head {
	case "*IDN?":
		// No header prefix; exactly 4 comma-separated fields.
		return []byte(fmt.Sprintf("Siglent,SDS1102CML+,%s,8.01.01.99R9\n", h.serial))
	case "*RST":
		h.sc.SetNorm(false)
		h.sc.SetRunning(true)
		h.sc.SetTdiv(500e-6)
		h.trmd = "AUTO"
		h.chdr = "SHORT"
		h.wfSP, h.wfNP, h.wfFP, h.wfSN = 1, 0, 0, 0
		return nil
	case "*CLS", "*OPC", "*WAI", "*SAV", "*RCL", "*ESE", "*SRE", "BUZZ", "MENU", "GRDS", "INTS", "PESU":
		return nil // accepted, silent (stubs where state is display-only)
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
		code := int(math.Round(31434 - 938*v))
		if code < engine.TrigCodeMin {
			code = engine.TrigCodeMin
		}
		if code > engine.TrigCodeMax {
			code = engine.TrigCodeMax
		}
		h.sc.SetTrigLevelCode(uint16(code))
		h.trlvV = v
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
		// EDGE,SR,C1,... — take the source if present, accept the rest.
		if strings.Contains(arg, "C2") {
			h.sc.SetTrigSource(1)
		} else if strings.Contains(arg, "C1") {
			h.sc.SetTrigSource(0)
		}
		return nil
	case "TRSE?":
		src := "C1"
		if st.TrigSource == 1 {
			src = "C2"
		}
		return h.reply("TRSE", "EDGE,SR,"+src+",HT,OFF")
	case "TRCP", "TRCP?":
		if head == "TRCP?" {
			return h.reply("TRCP", "DC")
		}
		return nil
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
	case "XYDS", "PACU", "CRMS", "CRST", "PNSU", "STPN", "RCPN", "HCSU":
		return nil // accepted stubs
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
		if math.IsNaN(v) || math.IsInf(v, 0) || v < -10 || v > 10 {
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
		h.cpl[ch] = arg // shadow only: coupling changes are deferred (spec 06 §7)
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
	case "BWL", "UNIT", "SKEW", "INVS":
		return nil // shadow-only stubs (BWL/coupling deferred per spec 06)
	case "WF?":
		return h.waveform(ch, arg)
	}
	return errTok(errUndefined)
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

func parseNum(s string) (float64, error) {
	s = strings.TrimSpace(s)
	// Strip a trailing SI unit (V, S, US, MS, NS...) if present.
	up := strings.ToUpper(s)
	mult := 1.0
	for _, suf := range []struct {
		s string
		m float64
	}{
		{"NS", 1e-9}, {"US", 1e-6}, {"MS", 1e-3},
		{"MV", 1e-3}, {"UV", 1e-6},
		{"V", 1}, {"S", 1},
	} {
		if strings.HasSuffix(up, suf.s) {
			up = strings.TrimSuffix(up, suf.s)
			mult = suf.m
			break
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(up), 64)
	return v * mult, err
}
