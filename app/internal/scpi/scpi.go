// Package scpi is this scope's SCPI dialect over the core engine: the
// LeCroy/Siglent legacy short-form set (spec 11 §3) in the core's Line
// grammar, and the adapter that binds it to the engine, the front end, the
// board's levels and the panel. One \n-terminated line in, reply bytes out;
// VXI-11 feeds it. Setters stage, queries snapshot; nothing here touches
// the GPMC bus.
// ENGMODEL-OWNER-UNIT: FU-APP-SCPI
package scpi

import (
	"fmt"
	"math"
	"sync/atomic"
	"time"

	inst "github.com/labeth/open-dso-core/instrument"
	"github.com/labeth/open-dso-core/sample"
	core "github.com/labeth/open-dso-core/scpi"
	scope "github.com/labeth/open-sds1000cml/app"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// Scope is the instrument surface: the instrument's controls and the
// fan-out's frames.
// TRLC-LINKS: REQ-SDS-024
type Scope interface {
	inst.Controls
	WithFrame(fn func(*sample.Frame))
}

// Analog is the vertical front end, in volts at the BNC; may be nil.
// TRLC-LINKS: REQ-SDS-024
type Analog interface {
	SetVdivV(ch int, v float64) error
	VdivV(ch int) float64
	RangeV(ch int) float64 // the analog range's V/div the codes are on
	SetProbe(ch int, x float64)
	SetCoupling(ch, mode int) error
}

// Levels is the trigger level and the offsets in volts, over the DACs the
// board stages: the trigger level at the probe tip, the offsets at the BNC.
// TRLC-LINKS: REQ-SDS-024
type Levels interface {
	SetTrigLevel(v float64, src int) float64
	TrigLevel(src int) (float64, bool)
	SetOffset(ch int, v float64) float64
	OffsetIn(ch int, st engine.Stats) float64
}

// Display is the device display/panel surface (implemented by
// *panel.Controller); may be nil. It backs the SCPI display commands whose
// state genuinely lives in the panel — X-Y view (XYDS), persistence (PESU),
// the softkey menu (MENU) — so their set→query round-trips reflect the REAL
// on-screen state, never a private shadow that could drift from the LCD.
// TRLC-LINKS: REQ-SDS-024
type Display interface {
	ViewXY() bool
	SetViewXY(on bool)
	PersistOn() bool
	SetPersist(on bool)
	MenuOpen() bool
	SetMenuOpen(on bool)
}

// traceDisplay is a Display that also owns the channels' trace visibility.
type traceDisplay interface {
	TraceOn(ch int) bool
	SetTraceOn(ch int, on bool)
}

// Screenshot returns the SCDP hardcopy payload (BMP). Wired by main to a
// headless render of the current frame; may be nil.
// TRLC-LINKS: REQ-SDS-024
type Screenshot func() []byte

// The error texts (spec 11 §3.4) the legacy grammar answers in place.
var (
	errUndefined  = core.StandardErrors[core.ErrUndefinedHeader]
	errHeader     = core.StandardErrors[core.ErrHeader]
	errSuffix     = core.StandardErrors[core.ErrHeaderSuffixRange]
	errOutOfRange = core.StandardErrors[core.ErrDataOutOfRange]
)

// Handler answers SCPI lines against the scope, its front end and display.
// TRLC-LINKS: REQ-SDS-024
type Handler struct {
	sc   Scope
	fe   Analog
	lv   Levels
	disp Display
	shot Screenshot
	logf func(string, ...any)
	// setups backs *SAV/*RCL; nil leaves them unavailable (an error, never a
	// silent success).
	setups SetupMemory

	// eng runs the dialect; its lock serializes the commands against each
	// other and guards the shadows below.
	eng *core.Engine

	// invs is the per-channel INVS (display invert) state, bit ch. It is
	// atomic, not under the engine's lock: the web status snapshot, the LCD
	// HUD builder and the panel (whose CH tab writes it) read it while a
	// command holds the lock and calls into the panel (MENU, Cn:TRA).
	invs atomic.Uint32

	wfSP int
	wfNP int
	wfFP int
	wfSN int

	// Instrument-state shadows for echo-back of controls with no engine
	// representation yet. Every shadowed set MUST round-trip through its
	// query (spec 11 §3.3): a silent success that the query then
	// contradicts corrupts automation. BWL has no shadow on purpose: it
	// reads the front end's relay state (spec 06 §6), and a front end
	// without the relay keeps it fixed OFF (ON errors).
	tra    [2]bool
	cpl    [2]string
	attn   [2]float64
	unit   [2]string
	skew   [2]float64
	trmd   string
	trlvV  float64
	serial string
	// pendTdiv is the timebase TDIV (or *RST) set and the engine has not
	// applied yet: a band switch takes seconds, and TDIV? must not answer the
	// old value meanwhile. 0 when none is pending.
	pendTdiv float64
}

// tdivNow is the instrument's timebase: a pending setting until the engine
// reports it applied.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) tdivNow(st engine.Stats) float64 {
	if h.pendTdiv > 0 && math.Abs(st.TdivS-h.pendTdiv) > h.pendTdiv*1e-6 {
		return h.pendTdiv
	}
	h.pendTdiv = 0
	return st.TdivS
}

// settle waits, unlocked and bounded, until a pending timebase is applied
// (*OPC?, *WAI): it answered at once while the band switch still ran.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) settle(c *core.Call) {
	if h.pendTdiv == 0 {
		return
	}
	c.Unlocked(func() {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if math.Abs(h.sc.Snapshot().TdivS-h.pendTdiv) <= h.pendTdiv*1e-6 {
				return
			}
		}
	})
}

// SetupMemory stores and recalls whole instrument setups in numbered slots
// (*SAV/*RCL).
// TRLC-LINKS: REQ-SDS-024
type SetupMemory interface {
	SaveSetup(slot int) error
	RecallSetup(slot int) error
}

// SetupFiles stores and recalls setups as named files (STPN/RCPN).
type SetupFiles interface {
	SaveSetupFile(name string) error
	RecallSetupFile(name string) error
}

// cursorMeasure is a Display that owns the manual cursors and the
// measurement panel (CRMS, PACU).
type cursorMeasure interface {
	CursorsOn() bool
	SetCursorsOn(on bool)
	SetMeasOn(on bool)
}

// SetSetupMemory wires *SAV/*RCL (and STPN/RCPN when the store keeps named
// files) to a setup store.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) SetSetupMemory(m SetupMemory) {
	h.eng.Do(func() { h.setups = m })
}

// setupSlots is the vendor's *SAV/*RCL range.
const setupSlots = 20

// New is a handler for sc; fe, disp and shot may be nil, and the commands
// that need them then answer with an error.
// TRLC-LINKS: REQ-SDS-024
func New(sc Scope, fe Analog, lv Levels, disp Display, shot Screenshot, logf func(string, ...any)) *Handler {
	h := &Handler{
		sc: sc, fe: fe, lv: lv, disp: disp, shot: shot, logf: logf,
		wfSP:   1,
		tra:    [2]bool{true, true},
		cpl:    [2]string{"D1M", "D1M"},
		attn:   [2]float64{1, 1},
		unit:   [2]string{"V", "V"},
		trmd:   "AUTO",
		serial: scope.UnitSerial(),
	}
	h.eng = core.New(h.dialect(), instrument{h})
	return h
}

// Inverted reports the per-channel INVS (display invert) state. The SCPI
// shadow is the single source of truth for display-level inversion: the web
// status snapshot and the LCD HUD both read it here.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) Inverted() [2]bool {
	b := h.invs.Load()
	return [2]bool{b&1 != 0, b&2 != 0}
}

// SetInverted sets a channel's INVS state: the panel's CH tab Invert row
// writes the same shadow as Cn:INVS.
// TRLC-LINKS: REQ-SDS-024, REQ-SDS-212
func (h *Handler) SetInverted(ch int, on bool) {
	if ch < 0 || ch > 1 {
		return
	}
	for {
		old := h.invs.Load()
		nw := old &^ (1 << ch)
		if on {
			nw |= 1 << ch
		}
		if h.invs.CompareAndSwap(old, nw) {
			return
		}
	}
}

// inverted reports one channel's INVS state.
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) inverted(ch int) bool { return ch >= 0 && ch < 2 && h.invs.Load()>>ch&1 != 0 }

// HandleLine executes one \n-terminated line (possibly ';'-separated
// compound commands) and returns the concatenated reply bytes. Pure setters
// are silent (spec 11: no OK acknowledgements).
// TRLC-LINKS: REQ-SDS-024
func (h *Handler) HandleLine(line []byte) []byte { return h.eng.Exec(line) }

// sciV formats volts per the reply grammar: %.2E + upper-case V.
// TRLC-LINKS: REQ-SDS-024
func sciV(v float64) string { return fmt.Sprintf("%.2EV", v) }

// sciS formats seconds: %.2E + LOWER-case s.
// TRLC-LINKS: REQ-SDS-024
func sciS(v float64) string { return fmt.Sprintf("%.2Es", v) }

// saraStr is the SI-prefix exception: e.g. 12500 → "12.50KSa".
// TRLC-LINKS: REQ-SDS-024
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

// parseNum is a legacy number with its unit suffix.
// TRLC-LINKS: REQ-SDS-024
func parseNum(s string) (float64, error) { return core.ParseLegacyNumber(s) }

// chHeader is a channel reply's header, "C1:VDIV".
// TRLC-LINKS: REQ-SDS-024
func chHeader(ch int, h string) string { return fmt.Sprintf("C%d:%s", ch+1, h) }
