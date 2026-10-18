// Package panel is the front-panel controller (spec 08): SIGIO-driven key
// matrix + hardware-quadrature knobs + LED latch. The panel worker NEVER
// touches the GPMC bus — all matrix reads and LED/offset/level writes go
// through the engine's command surface and are applied by the bus owner at
// the frame boundary. The analog V/div front end (SPI) is off-bus and is
// driven directly.
// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"sync"
	"sync/atomic"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/settings"
	"open-sds/app/internal/superres"
)

// Engine is the command surface the controller drives (spec 08 §4).
// TRLC-LINKS: REQ-SDS-135
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
// TRLC-LINKS: REQ-SDS-135
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
	TrigCode(volts float64, srcCh int) float64
	TrigVolts(code uint16, srcCh int) float64
	SetTrigCalDetent(ch, detent int, c analog.TrigCal)
}

// LED shadow-word bits — spec 02 §7.5 "LED shadow-word bit map". This is the
// corroborated PCB wiring; the boot firmware's internal LED-index order does NOT
// match it, so this map is authoritative. (There is no TRIG'd latch LED —
// trigger-armed is a read-only HW status shown on-screen, not a lamp.)
const (
	ledCursors = 0x0002 // CURSORS — the cursors are on
	ledIntens  = 0x0004 // INTENSITY / ADJUST-knob (not driven: no firmware has lit it)
	ledCH1     = 0x0010
	ledMath    = 0x0020 // MATH — a math trace is on
	ledCH2     = 0x0040
	ledRef     = 0x0080 // REF — REF A or REF B is shown
	ledMeasure = 0x0100
	ledAcquire = 0x0200
	ledDisplay = 0x0400
	ledSaveRec = 0x0800 // SAVE/RECALL — the settings are those last saved or recalled
	ledUtility = 0x1000 // UTILITY — a bus is decoded
	ledRun     = 0x2000 // RUN (green element of the bicolor RUN/STOP lamp)
	ledStop    = 0x4000 // STOP (red element)
	ledSingle  = 0x8000
)

const knobPhaseMask = 0xC0C0 // encoder phase bits in every selector word

// Button codes are sel-index<<8 | bit.
// TRLC-LINKS: REQ-SDS-135
func bcode(selIdx, bit int) int { return selIdx<<8 | bit }

// Wired buttons (spec 08 §2 map; selIdx: 0=0x64, 1=0x65, 2=0x66, 3=0x67).
var (
	btnRunStop = bcode(1, 2)
	btnSingle  = bcode(1, 10)
	btnAuto    = bcode(3, 10)

	// Knob push-switches (spec 08 §6.3), with their printed jobs:
	btnCh1VdivPush = bcode(1, 9) // 0x65:9 — CH1 V/DIV push (Variable)
	btnCh2VdivPush = bcode(2, 1) // 0x66:1 — CH2 V/DIV push (Variable)
	btnTrigLvlPush = bcode(0, 9) // 0x64:9 — TRIG LEVEL push (Zero): level 0 V

	btnUtility   = bcode(2, 3) // 0x66:3 (spec 08 §6.4) — the BUS sheet tab
	btnAdjustPsh = bcode(1, 1) // 0x65:1 (spec 08 §6.3) — ADJUST push: the workspace's tool
)

// knobDef is one row of the FIXED FPGA priority order (spec 08 §3): exactly
// one knob is serviced per interrupt — the first whose selector has a low
// phase bit. Direction comes from WHICH bit rests low (never from a phase
// change — that misses sustained rotation).
// TRLC-LINKS: REQ-SDS-135
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
// TRLC-LINKS: REQ-SDS-135
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

// TRLC-LINKS: REQ-SDS-135
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
	// Protocol decode: a framecalc protocol number, never ProtoAuto (Auto
	// detects the bus and fills these in). chA/chB = channel roles
	// (0=C1,1=C2): UART uses chA (source); I2C chA=SCL,chB=SDA; SPI chA=CLK,chB=DATA.
	decProto         int
	decBaud          int
	decChA, decChB   int
	decCPOL, decCPHA bool
	decLSB           bool // SPI least significant bit first
	// decParity is Modbus RTU's framing ("even", "odd", "none"; "" the
	// decoder finds it); decInv says the IR or DALI line's marks are at its
	// high level (an emitter's drive), as detected, and for the protocols
	// whose decoders report it the polarity or line they found (Result.Inverted:
	// an RC, Morse or DCF77 line's marks low, a DSI line's high, an RFID
	// reader's carrier).
	decParity string
	decInv    bool
	// decBits is SIRC's, Wiegand's or a T55xx command's frame length (0:
	// not known), the widths of their units for the hardware matcher, or the
	// variant a detection found (Result.FrameBits).
	decBits int
	// decLayer is the bus's application layer (analysis.Layers' ID, ""
	// none); decLayerAddr and decLayerDir are the I2C filter layer's address
	// (-1 any) and direction (0 both, 1 read, 2 write).
	decLayer     string
	decLayerAddr int
	decLayerDir  int
	// SPI's data line is read decDelayNs after the clock's sampling edge, or
	// with decDelayAuto at the middle of the data's eye measured on the
	// full-rate records (spiEye); decFine asks for the FPGA's receivers on
	// the samples (the fast image) at any rate.
	decDelayNs   float64
	decDelayAuto bool
	decFine      bool
	spiEye       decode.SPIEye // the last full-rate SPI record's eye (PeriodS 0: none)
	// the bursts' ends and starts weighed on the records since the SPI bus
	// or its clock rate changed (spiEyeKey): the middle after the edge or
	// before it
	spiEyeKey   string
	spiEyeVotes decode.SPIEyeVotes
	decFormat   int // 0=hex, 1=ascii, 2=both
	// what Auto filled in, and whether the bus it found still decodes
	auto autoWatch
	// how the detection under way frames the bus: 0 not, busFrameEntry for
	// BUS's own detection, busFrameAuto for AUTO in BUS
	busFrameNext int
	// Decoded streaming (ADR-PANEL-DECODED-STREAM), guarded by mu.
	decMode  int         // decModeView / decModeStream
	decTrig  int         // index into decTrigNames
	decValue int         // StreamOnValue unit
	kb       matchEditor // SERIAL trigger match editor (full-screen keyboard)
	stream   Stream
	// a stopped stream's screen: transcript list or the record's waveform
	streamList bool
	streamTop  int // list's first line; -1 follows the newest
	streamGen  int // counts stream starts: a start superseded meanwhile gives way

	// UI state (ADR-TASK-WORKSPACES): written by the panel goroutine, read by
	// the LCD renderer and the remote, guarded by mu. inject runs API-driven
	// panel events on the panel goroutine so dispatch stays single-threaded.
	mu       sync.Mutex
	ui       uiState
	clock    func() time.Time // nil: time.Now (tests drive notices with their own)
	chDisp   [2]bool
	showMeas bool
	meas     []measItem // the chosen readouts (at most maxMeasItems)
	viewMode int        // 0 Y-T, 1 X-Y, 2 FFT, 3 Bode, 4 spectrogram (the VIEW tab's View)
	mathMode int        // 0 = off, 1 = C1+C2, 2 = C1-C2, 3 = C2-C1, 4 = C1×C2
	fftSrc   int        // the channel SPECTRUM and the spectrogram analyse
	inverter Inverter   // the display invert (SCPI Cn:INVS state); nil without one

	// Per-frame analysis for the panes (OnFrame), guarded by amu. Lock order:
	// amu may be taken without mu held only; mu may be taken under amu.
	amu sync.Mutex
	ana anaState

	// The remote's snapshot and long-poll (remote.go), guarded by rmu.
	rmu    sync.Mutex
	remote remoteState

	apiDepth       int    // a remote action is running: it leaves the LCD's menus alone
	ledWord        uint16 // the latched lamp word
	ledSet         bool
	lastSetup      int               // the slot last saved or recalled (Save/Recall lamp)
	lastSetupPrint string            // the settings then (a store without setupCurrent)
	lastSetupSt    settings.Settings // the setup then, as a save writes it
	setupSame      bool              // the settings are still lastSetupSt (cached)
	setupSameAt    time.Time         // when setupSame was taken; zero: take it again
	slotUsed       [setupSlots + 1]bool
	slotSum        [setupSlots + 1]string // the FILE rows' summaries
	slotWS         [setupSlots + 1]int    // the workspace each slot holds (-1 unknown)
	remoteNotices  bool                   // show REMOTE notes on the LCD
	saverMin       int                    // Screen saver after this many minutes without input (0 = off)
	offMin         int                    // Screen off after this many minutes without input (0 = off)
	lastInput      time.Time              // the last input run on the panel goroutine (zero: none yet)
	wakeUntil      time.Time              // until then the scope's keys and knobs only wake the screen
	stackN         int                    // the master's depth
	webCapture     bool                   // a web page's decoded capture holds the acquisition (seen by SyncLEDs)
	stack          *stackState            // a master being stacked
	maskAutoTest   bool                   // the COMPARE MASK row: count once built
	detectSync     bool                   // tests: run the bus detection inline
	searchMu       sync.Mutex             // one bus search at a time (findBus steps the timebase)
	autoSearch     atomic.Bool            // the next bus search is AUTO's own (runBusAuto)
	searchOwn      bool                   // the running bus search is AUTO's own (guarded by searchMu)
	decGen         uint64                 // counts the protocol settings (set, recalled, defaulted, locked): a search's result stands only if none came meanwhile
	// On-screen cursors (spec 08 §6): two X (time) and two Y (volts) cursors,
	// anchored to the signal: the time cursors' times from the trigger (s),
	// the voltage cursors' volts at the tip of channel curVCh; ADJUST moves
	// the selected one. curPend holds screen fractions (across, down) to
	// anchor at the next state read (the power-on and factory places, an older
	// settings file's).
	curOn   bool
	curType int // 0 = X (time), 1 = Y (volts)
	curSel  int // 0 = A, 1 = B
	curT    [2]float64
	curV    [2]float64
	curVCh  int
	curPend *[2][2]float64
	// the record's length in screens of the last frame analysed (a zoomed
	// screen's pan is a fraction of the record): float64 bits
	curRecords atomic.Uint64
	eyeLeased  atomic.Bool // the eye holds a raw lease (leaseEyeRaw, endEyeRaw)
	inject     chan func()
	looping    atomic.Bool // Run is serving inject (else a remote change runs inline)
	inlineMu   sync.Mutex  // orders inline remote changes without the loop

	// frameFn reads the latest published frame (fo.WithFrame) for autoset; nil
	// until SetFrameSource is called (AUTO then falls back to plain AUTO/run).
	frameFn func(func(*engine.Frame))
	// autoset runs as a background sweep; autosetStop cancels it (second AUTO).
	autosetBusy bool
	autosetStop chan struct{}
	autosetMsg  string      // banner text while busy ("AUTOSET…" / a result note)
	autoFor     int         // while a page's AUTO press runs: the workspace it frames for + 1 (0: the LCD's)
	qualHeld    atomic.Bool // AUTO in BUS holds a loaded qualifier image (edgeOnGeneralImage)
	// each sweep is numbered; when one ends, the rows it changed are kept for
	// the remote that started it (Status.AutosetDone, AutosetChanged)
	autosetRun     uint64
	autosetFrom    changeMark
	autosetDone    uint64
	autosetChanged []string
	// PRINT key: hardcopy saves a screenshot and names the file (shown as a
	// notice).
	hardcopy func() (string, error)
	refs     [2]refWave // saved reference waveforms (REF A/B)
	refGen   uint64     // counts reference stores (refWave.gen)

	maskSetup    cmpSetup // the setup the mask built here was installed at
	maskSetupGen uint64   // the engine's mask generation it names

	// Super-res (device stack-and-crunch, reference-locked — SINGLE a frame, then
	// UTILITY). srActive gates the mode; a stacker goroutine feeds srStack OFF the
	// render lock. srFocus is the intensity-button cycle: 0=watch (live+gate),
	// 1=edit gate START, 2=edit gate END (ADJUST knob moves the edge), 3=review
	// the stacked trace. srManLo/srManHi are the manual gate (-1 = auto).
	srActive     bool
	srFocus      int
	srManLo      int
	srManHi      int
	srStack      *superres.Stack
	srStop       chan struct{}
	srStatus     string
	srK          int     // fine-grid factor
	srStopMode   int     // 0=bits, 1=stacks, 2=time (menu order bit→stacks→time)
	srStopVal    float64 // target for the active stop mode
	srFPGACancel func()
	srFPGAGen    uint64
	// The seeder: the period search and the seed take seconds on the ARM, so
	// they run off the panel goroutine; a request's generation lets the
	// latest one win and Off discard one in flight.
	srSeedGen  uint64
	srSeeding  bool          // a seed requested or running: the mode reads On
	srSeederOn bool          // the seeder goroutine runs
	srRunAt    uint64        // runReq when the latest seed was asked for
	runReq     atomic.Uint64 // Run, Stop and Single asked for: a seed resumes RUN only when none came after it was asked for
	srFPGABusy bool          // an FPGA stacking session is running (superres_fpga.go)
	srCh       int           // stacked/aligned channel (0=C1,1=C2)
	srT0       time.Time
	srMean     []float32 // latest crunched trace for the review render (guarded by mu)
	srMean2    []float32 // the OTHER channel's crunched trace (stacked X-Y / dual FFT)
	srBits     float64   // latest measured bits gained (guarded by mu)

	// Mask testing (device flow, docs/zonemask-plan.md §3): build a golden
	// envelope from N live frames on the trigger-source channel, dilate by the
	// selected tolerance preset, install in the engine. All guarded by mu.
	maskN        int         // frames per build
	setups       SetupMemory // SAVE/RECALL slots (nil = unavailable)
	maskTol      int         // index into maskTols presets
	maskBuilding bool        // a build goroutine is running
	maskGen      uint64      // counts mask clears: a build running when the mask is cleared installs nothing
	masterPend   *masterSrc  // a finished master waiting for a live capture to build its mask in
	maskMsg      string      // build/status line for the LCD HUD
	srFrames     int         // stacked frame count (guarded by mu — srStack.Frames races)
	srRejected   int         // rejected (non-matching) frame count (guarded by mu)
	srResetReq   bool        // Reset softkey → srLoop clears the accumulation next tick
	srWinLo      int         // the selected span (frozen on-screen window / manual gate):
	srWinHi      int         // the review renders exactly this span, so the view is unchanged
	srPeriod     int         // detected period within the span (samples); >0 → the stack is
	// ONE period and the review TILES it across the span (fast multi-wave cheat)

	// Trigger-qualifier shadows (an engine without getters for these), edited
	// on the TRIG tab. ns for widths/times; fractions 0..1.
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
// TRLC-LINKS: REQ-SDS-135
func New(eng Engine, fe Analog, keyFD int, tdivs []float64, startTdiv float64, logf func(string, ...any)) *Controller {
	c := &Controller{
		eng: eng, fe: fe, keyFD: keyFD, logf: logf,
		tdivs:        tdivs,
		vIdx:         [2]int{analog.BootDetent, analog.BootDetent},
		trigCode:     31437, // 0 V threshold (measured global fit zero)
		chDisp:       [2]bool{true, true},
		curPend:      &cursorHome,
		inject:       make(chan func(), 32),
		running:      true,
		zoom:         1,
		decBaud:      115200,
		decLayerAddr: -1,
		decChA:       0, decChB: 1, // UART on C1; I2C/SPI clk on C1, data on C2
		// Super-res defaults: fine-grid ×32, stop on +4 bits (the user's example),
		// stacking C1. Menu stop-mode order is bit→stacks→time (0/1/2).
		srK: 32, srStopMode: 0, srStopVal: 4, srCh: 0,
		// Mask defaults: 32 build frames, tolerance preset ±5 samp / ±8 codes
		// (covers trigger-point jitter + 3σ noise — docs/zonemask-plan.md §1.4).
		maskN: 32, maskTol: 1,
		// Seed the qualifier shadows to the engine's defaultTrigParams so the
		// TRIG tab agrees with the engine from boot (slope 0.2/0.8, neg sync).
		pulseLvl: 0.5, pulseMin: powerOnLimitNs[0], pulseMax: powerOnLimitNs[1],
		slopeLo: 0.2, slopeHi: 0.8, slopeMin: powerOnLimitNs[0], slopeMax: powerOnLimitNs[1],
		videoLine: 0, videoNeg: true,
		ui:            freshUI(),
		stackN:        64,
		remoteNotices: true,
		saverMin:      defSaverMin,
		offMin:        defOffMin,
	}
	for i := range c.slotWS {
		c.slotWS[i] = -1
	}
	c.ui.statT = c.now()
	for i, t := range tdivs {
		if t >= startTdiv*(1-1e-6) {
			c.tdivIdx = i
			break
		}
	}
	// a serial trigger never set up takes any I2C address and direction: its
	// zero value reads address 0x00, write, and the address is kept across
	// buses (serialBus); a restored setup sets its own after this
	if se := c.serialTrig(); se != nil {
		if p := se.SerialConfig(); p.Proto == 0 && len(p.Bytes) == 0 && len(p.Pattern) == 0 && p.Addr == 0 && p.RW == 0 {
			p.Addr, p.RW = -1, 2
			se.SetSerialParams(p)
		}
	}
	return c
}

// Inverter is the display-level trace invert (the SCPI Cn:INVS state).
type Inverter interface {
	Inverted() [2]bool
	SetInverted(ch int, on bool)
}

// SetInverter wires the CH tabs' Invert rows to the display invert.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) SetInverter(i Inverter) {
	c.mu.Lock()
	c.inverter = i
	c.mu.Unlock()
}
