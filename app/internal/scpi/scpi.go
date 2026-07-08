// Package scpi implements the LeCroy/Siglent short-form SCPI set (spec 11
// §3): one \n-terminated line in, reply bytes out. Transport-agnostic —
// VXI-11 feeds it today; a USB-TMC pump can feed the same HandleLine later.
// The parser is a pure producer/consumer: setters stage, queries snapshot;
// it NEVER touches the GPMC bus.
package scpi

import (
	"fmt"
	"os"
	"strconv"
	"strings"

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
	SetCoupling(ch, mode int) error
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
