// Package panel is the front-panel controller (spec 08): SIGIO-driven key
// matrix + hardware-quadrature knobs + LED latch. The panel worker NEVER
// touches the GPMC bus — all matrix reads and LED/offset/level writes go
// through the engine's command surface and are applied by the bus owner at
// the frame boundary. The analog V/div front end (SPI) is off-bus and is
// driven directly.
package panel

import (
	"sync"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/superres"
)

// Engine is the command surface the controller drives (spec 08 §4).
type Engine interface {
	ReadMatrix() ([5]uint16, bool)
	SetLEDs(word uint16)
	SetOffsetDAC(ch int, code uint16)
	SetTrigLevelCode(code uint16) uint16
	SetTdiv(tdivS float64) (engine.Band, bool)
	SetNorm(on bool)
	SetRunning(on bool)
	SetSingle()
	SetTrigSlope(rising bool)
	SetTrigSource(ch int)
	SetTrigType(typ int)
	SetAcqMode(m int)
	SetAvgCount(n int)
	SetEresLen(l int)
	SetETS(on bool)
	SetTrigPosFrac(frac float64)
	SetHoldoff(sec float64) float64
	SetMemDepth(samples int) int
	SetPulseParams(lvlFrac, wMinNs, wMaxNs float64, cond int)
	SetSlopeParams(loFrac, hiFrac, tMinNs, tMaxNs float64, cond int)
	SetVideoParams(std, line int, neg bool)
	SetMask(m *engine.Mask)
	SetMaskMode(m int)
	ClearMaskFails()
	Snapshot() engine.Stats // authoritative state to resync knob shadows
	// AcqLog exposes the per-capture instrumentation ring; autoset uses it to
	// verify its chosen trigger level actually fires the comparator.
	AcqLog(n int) ([]engine.AcqSample, float64)
}

// Analog is the off-bus V/div front end; nil → V/div knobs claim-and-ignore.
type Analog interface {
	SetVdiv(ch, idx int) error
	Snapshot() ([2]int, bool)
	SetOffset(ch int, volts float64) uint16
	OffsetReqV(ch int) float64
	OffsetVolts(ch int, code uint16) float64 // calibrated per-detent DAC code → volts
	OffsetK(ch int) float64                  // offset slope (codes/volt) for the current detent
	SetCoupling(ch, mode int) error
	Coupling(ch int) int
	SetProbe(ch int, x float64)
	ProbeFactor(ch int) float64
}

// LED shadow-word bits — spec 02 §7.5 "LED shadow-word bit map". This is the
// corroborated PCB wiring; the boot firmware's internal LED-index order does NOT
// match it, so this map is authoritative. (There is no TRIG'd latch LED —
// trigger-armed is a read-only HW status shown on-screen, not a lamp.)
const (
	ledCursors = 0x0002
	ledIntens  = 0x0004 // INTENSITY / ADJUST-knob (not driven)
	ledCH1     = 0x0010
	ledMath    = 0x0020
	ledCH2     = 0x0040
	ledRef     = 0x0080
	ledMeasure = 0x0100
	ledAcquire = 0x0200
	ledDisplay = 0x0400
	ledSaveRec = 0x0800 // SAVE/RECALL (not driven)
	ledUtility = 0x1000 // UTILITY — lit while super-res is active (toggle like SINGLE)
	ledRun     = 0x2000 // RUN (green element of the bicolor RUN/STOP lamp)
	ledStop    = 0x4000 // STOP (red element)
	ledSingle  = 0x8000
)

const knobPhaseMask = 0xC0C0 // encoder phase bits in every selector word

// Button codes are sel-index<<8 | bit.
func bcode(selIdx, bit int) int { return selIdx<<8 | bit }

// Wired buttons (spec 08 §2 map; selIdx: 0=0x64, 1=0x65, 2=0x66, 3=0x67).
var (
	btnRunStop = bcode(1, 2)
	btnSingle  = bcode(1, 10)
	btnAuto    = bcode(3, 10)

	// Knob push-switches (spec 08 §6.3), repurposed as trigger shortcuts:
	btnCh1VdivPush = bcode(1, 9) // 0x65:9 — push CH1 V/DIV → trigger source C1
	btnCh2VdivPush = bcode(2, 1) // 0x66:1 — push CH2 V/DIV → trigger source C2
	btnTrigLvlPush = bcode(0, 9) // 0x64:9 — push TRIG LEVEL → flip slope rise/fall

	btnUtility   = bcode(2, 3) // 0x66:3 (spec 08 §6.4) — toggle super-res like SINGLE
	btnAdjustPsh = bcode(1, 1) // 0x65:1 (spec 08 §6.3) — ADJUST/intensity push: toggle SR review
)

// knobDef is one row of the FIXED FPGA priority order (spec 08 §3): exactly
// one knob is serviced per interrupt — the first whose selector has a low
// phase bit. Direction comes from WHICH bit rests low (never from a phase
// change — that misses sustained rotation).
type knobDef struct {
	name    string
	selIdx  int
	bitLo   uint // low → CW (+1)
	bitHi   uint // low → CCW (−1)
	stepped bool // stepped: 1/detent; continuous: accel-clamped 0x69
}

var knobs = []knobDef{
	{"horizpos", 3, 14, 15, false},
	{"ch2pos", 3, 6, 7, false},
	{"tdiv", 2, 14, 15, true},
	{"ch2vdiv", 2, 6, 7, true},
	{"ch1vdiv", 1, 14, 15, true},
	{"adjust", 1, 6, 7, false},
	{"triglevel", 0, 14, 15, false},
	{"ch1pos", 0, 6, 7, false},
}

// accel maps the raw 0x69 magnitude to steps (continuous knobs): runaway
// guard at 200 FIRST (must be ≥100 or the ≥20→100 row is unreachable).
func accel(raw uint16) int {
	if raw > 200 {
		raw = 200
	}
	switch {
	case raw >= 20:
		return 100
	case raw >= 10:
		return 50
	default:
		return int(raw)
	}
}

type Controller struct {
	eng   Engine
	fe    Analog
	keyFD int
	logf  func(string, ...any)

	tdivs    []float64
	tdivIdx  int
	vIdx     [2]int
	offReqV  [2]float64
	trigCode uint16
	running  bool
	norm     bool
	single   bool // a single-shot is armed/waiting → SINGLE lamp

	prev     [5]uint16
	havePrev bool
	zoom     int     // horizontal magnification (1,2,5,10,20,50); 1 = no zoom
	zoomOff  float64 // pan offset of the zoom window, fraction of the record
	persist  bool    // display persistence (afterglow)
	// Protocol decode: proto 0=off,1=UART,2=I2C,3=SPI. chA/chB = channel roles
	// (0=C1,1=C2): UART uses chA (source); I2C chA=SCL,chB=SDA; SPI chA=CLK,chB=DATA.
	decProto         int
	decBaud          int
	decChA, decChB   int
	decCPOL, decCPHA bool
	decFormat        int // 0=hex, 1=ascii, 2=both

	// Menu state (spec 08 §6): written by the panel goroutine, read by the LCD
	// renderer, guarded by mu. inject runs API-driven panel events on the panel
	// goroutine so button/knob dispatch stays single-threaded.
	mu       sync.Mutex
	menuPage int
	menuSel  int
	chDisp   [2]bool
	showMeas bool
	viewMode int // 0 = Y-T, 1 = X-Y, 2 = FFT (DISPLAY menu "View")
	mathMode int // 0 = off, 1 = C1+C2, 2 = C1-C2, 3 = C1×C2 (DISPLAY menu "Math")
	// On-screen cursors (spec 08 §6): two X (time) and two Y (volts) cursors,
	// positions as screen fractions; ADJUST moves the selected one.
	curOn   bool
	curType int // 0 = X (time), 1 = Y (volts)
	curSel  int // 0 = A, 1 = B
	curX    [2]float64
	curY    [2]float64
	inject  chan func()

	// frameFn reads the latest published frame (fo.WithFrame) for autoset; nil
	// until SetFrameSource is called (AUTO then falls back to plain AUTO/run).
	frameFn func(func(*engine.Frame))
	// autoset runs as a background sweep; autosetStop cancels it (second AUTO).
	autosetBusy bool
	autosetStop chan struct{}
	autosetMsg  string     // banner text while busy ("AUTOSET…" / a result note)
	refs        [2]refWave // saved reference waveforms (REF A/B)

	// Super-res (device stack-and-crunch, reference-locked — SINGLE a frame, then
	// UTILITY). srActive gates the mode; a stacker goroutine feeds srStack OFF the
	// render lock. srFocus is the intensity-button cycle: 0=watch (live+gate),
	// 1=edit gate START, 2=edit gate END (ADJUST knob moves the edge), 3=review
	// the stacked trace. srManLo/srManHi are the manual gate (-1 = auto).
	srActive   bool
	srFocus    int
	srManLo    int
	srManHi    int
	srStack    *superres.Stack
	srStop     chan struct{}
	srStatus   string
	srK        int     // fine-grid factor
	srStopMode int     // 0=bits, 1=stacks, 2=time (menu order bit→stacks→time)
	srStopVal  float64 // target for the active stop mode
	srCh       int     // stacked/aligned channel (0=C1,1=C2)
	srT0       time.Time
	srMean     []float32 // latest crunched trace for the review render (guarded by mu)
	srMean2    []float32 // the OTHER channel's crunched trace (stacked X-Y / dual FFT)
	srBits     float64   // latest measured bits gained (guarded by mu)

	// Mask testing (device flow, docs/zonemask-plan.md §3): build a golden
	// envelope from N live frames on the trigger-source channel, dilate by the
	// selected tolerance preset, install in the engine. All guarded by mu.
	maskN        int    // frames per build
	maskTol      int    // index into maskTols presets
	maskBuilding bool   // a build goroutine is running
	maskMsg      string // build/status line for the LCD HUD
	srFrames     int    // stacked frame count (guarded by mu — srStack.Frames races)
	srRejected   int    // rejected (non-matching) frame count (guarded by mu)
	srResetReq   bool   // Reset softkey → srLoop clears the accumulation next tick
	srWinLo      int    // the selected span (frozen on-screen window / manual gate):
	srWinHi      int    // the review renders exactly this span, so the view is unchanged
	srPeriod     int    // detected period within the span (samples); >0 → the stack is
	// ONE period and the review TILES it across the span (fast multi-wave cheat)

	// Trigger-qualifier shadows (the engine has no getters for these), edited on
	// the TRIGGER-qualifier sub-page. ns for widths/times; fractions 0..1.
	pulseLvl, pulseMin, pulseMax         float64
	pulseCond                            int
	slopeLo, slopeHi, slopeMin, slopeMax float64
	slopeCond                            int
	videoStd, videoLine                  int
	videoNeg                             bool
}

// New builds the controller. The timebase ladder is injected (the controller
// does not own it); startTdiv seeds the knob index. All shadows are seeded
// WITHOUT driving anything — the inherited analog state stays untouched
// until the user turns a knob (spec 08 §7).
func New(eng Engine, fe Analog, keyFD int, tdivs []float64, startTdiv float64, logf func(string, ...any)) *Controller {
	c := &Controller{
		eng: eng, fe: fe, keyFD: keyFD, logf: logf,
		tdivs:    tdivs,
		vIdx:     [2]int{analog.BootDetent, analog.BootDetent},
		trigCode: 31434, // 0 V threshold
		chDisp:   [2]bool{true, true},
		curX:     [2]float64{0.35, 0.65},
		curY:     [2]float64{0.35, 0.65},
		inject:   make(chan func(), 32),
		running:  true,
		zoom:     1,
		decBaud:  115200,
		decChA:   0, decChB: 1, // UART on C1; I2C/SPI clk on C1, data on C2
		// Super-res defaults: fine-grid ×32, stop on +4 bits (the user's example),
		// stacking C1. Menu stop-mode order is bit→stacks→time (0/1/2).
		srK: 32, srStopMode: 0, srStopVal: 4, srCh: 0,
		// Mask defaults: 32 build frames, tolerance preset ±5 samp / ±8 codes
		// (covers trigger-point jitter + 3σ noise — docs/zonemask-plan.md §1.4).
		maskN: 32, maskTol: 1,
		// Seed the qualifier shadows to the engine's defaultTrigParams so the
		// pgTrigQ page agrees with the engine from boot (slope 0.2/0.8, neg sync).
		pulseLvl: 0.5, pulseMin: 100, pulseMax: 1000,
		slopeLo: 0.2, slopeHi: 0.8, slopeMin: 100, slopeMax: 1000,
		videoLine: 0, videoNeg: true,
	}
	for i, t := range tdivs {
		if t >= startTdiv*(1-1e-6) {
			c.tdivIdx = i
			break
		}
	}
	return c
}
