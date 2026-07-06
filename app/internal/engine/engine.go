// Package engine is the single-owner acquisition engine (spec 03): one
// goroutine owns the inherited GPMC fd and is the only code that touches the
// bus. HTTP handlers stage commands into coalescing shadows and consume frame
// copies; the owner applies commands at the frame boundary and runs the
// per-frame arm → wait → capture-halt → drain → re-arm FSM.
package engine

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"open-sds/app/internal/bus"
)

// Register selectors (CS1 unless noted). Spec 02.
const (
	selPreamble = 0x00 // 0x80 ×2 only in the trigger-level recommit
	selVersion  = 0x12
	selClass    = 0x19
	selDivLo    = 0x1a
	selDivHi    = 0x1b
	selArm      = 0x21 // 0xC0 reset-head, 0xC3 go, 0xC8 capture-halt
	selRunWord  = 0x35 // 0x0001 AUTO, 0x0003 NORM
	selReset2   = 0x36
	selStatus   = 0x39 // bit1 trig, bit2 done
	selTrigLo   = 0x3a
	selTrigHi   = 0x3b
	selResetHd  = 0x44
	selFill     = 0x46
	selWrPtr    = 0x57
	drainBase   = 0x30 // 0x30–0x34 round-robin sample ports

	// CS3 trigger-level DAC lanes (spec 05 §1). High bytes self-latch.
	cs3LevelALo = 0x14
	cs3LevelAHi = 0x34
	cs3LevelBLo = 0x15
	cs3LevelBHi = 0x35

	cs3ConfStatus = 0x07 // CS3 config-status: bit7 = CONF_DONE. READ ONLY.

	// CS3 vertical-offset DAC lanes per channel (spec 06 §5.1): low byte
	// first, high byte self-latches. These selectors ALIAS live acquisition
	// ports on CS1 — the plane must be explicit on every write.
	cs3OffC1Lo = 0x10
	cs3OffC1Hi = 0x30
	cs3OffC2Lo = 0x11
	cs3OffC2Hi = 0x31

	opResetHead = 0x00c0
	opGo        = 0x00c3
	opHalt      = 0x00c8

	runAuto = 0x0001
	runNorm = 0x0003

	statValid = 0x0001 // AUTO free-run: acquisition completed without a trigger
	statTrig  = 0x0002
	statDone  = 0x0004

	nativeEdgeMinPtp  = 40    // codes; flat rail ≈ 5, real cal edge ≈ 150
	nativeFlatFallbck = 60    // held frames before one honest flat publish
	fillFull          = 0x7f0 // fill counter near the 11-bit max = record full

	// TrigCodeMin/Max clamp the UI trigger-level DAC range (spec 05 §1.2).
	TrigCodeMin = 27000
	TrigCodeMax = 35000
)

// Clock abstracts time for tests. Sleep must also advance Now in fakes.
type Clock struct {
	Now   func() time.Time
	Sleep func(time.Duration)
}

func realClock() Clock { return Clock{Now: time.Now, Sleep: time.Sleep} }

// Config wires an Engine.
type Config struct {
	Bus         bus.Bus
	Clock       Clock         // zero → real clock
	FramePeriod time.Duration // publish pacing floor; default 50 ms
	ArmSettle   time.Duration // default 2 ms
	PollEvery   time.Duration // wait-gate poll pace; default 150 µs
	Logf        func(format string, a ...any)
}

// Stats is the exported snapshot for the health writer and the web UI.
// Field meanings follow the spec 09 stats shape.
type Stats struct {
	Frames      uint64  `json:"frames"`       // FSM heartbeat: +1 per loop iteration, publish or not
	Coherent    uint64  `json:"coherent"`     // frames that latched+drained coherently
	Published   uint64  `json:"published"`    // frames handed to the arena
	Held        uint64  `json:"held"`         // display-hold cycles
	HaltConfirm uint64  `json:"halt_confirm"` // halts with fill frozen across the double read
	BusErrors   uint64  `json:"bus_errors"`
	DeadRuns    int     `json:"dead_runs"` // consecutive fill-frozen + flat-drain frames
	Wedged      bool    `json:"wedged"`
	FPS         float64 `json:"fps"`
	Running     bool    `json:"running"`
	Norm        bool    `json:"norm"`
	Single      bool    `json:"single"`        // a single-shot is armed/waiting
	TrigPosFrac float64 `json:"trig_pos_frac"` // horizontal trigger position 0..1
	TdivS       float64 `json:"tdiv_s"`
	DisplayedS  float64 `json:"displayed_sdiv_s"`
	TrigCode    uint16  `json:"trig_code"` // 0 = boot-inherited comparator untouched
	OffC1       uint16  `json:"off_c1"`    // 0 = boot-inherited offset untouched
	OffC2       uint16  `json:"off_c2"`
	TrigRising  bool    `json:"trig_rising"`
	TrigSource  int     `json:"trig_source"` // 0=C1, 1=C2
	LastPtp     int     `json:"last_ptp"`
	LastTrigPos int     `json:"last_trigpos"`
	ArmToLatch  float64 `json:"arm_to_latch_ms"`
	DrainMs     float64 `json:"drain_ms"`
	HoldoffS    float64 `json:"holdoff_s"` // trigger holdoff (0 = off)
	Seq         uint64  `json:"seq"`
	MmapDrain   bool    `json:"mmap_drain"`
	ETS         bool    `json:"ets"`
	BandKind    string  `json:"band"`      // native-fast | decimated | envelope | roll
	HaltMode    string  `json:"halt_mode"` // capture-halt | latch-no-halt
	TrigType    int     `json:"trig_type"` // 0=edge 1=pulse 2=slope 3=video
	AcqMode     int     `json:"acq_mode"`  // 0=normal 1=average 2=eres 3=peak
	AvgCount    int     `json:"avg_count"`
	EresLen     int     `json:"eres_len"`
	WinColStd   float64 `json:"wincol_std"`     // centred cross-frame uniformity
	WinColRaw   float64 `json:"wincol_std_raw"` // fixed-position variant
	WinColMax   float64 `json:"wincol_max"`     // worst centred column
	ValidDepth  int     `json:"valid_depth"`    // real-signal samples in the drain
	MemDepth    int     `json:"mem_depth"`      // configured decimated drain depth
	Stream      bool    `json:"stream"`         // stitched streaming decode mode on
	GapMs       float64 `json:"gap_ms"`         // stream: blackout between windows
}

type Engine struct {
	b     bus.Bus
	clk   Clock
	logf  func(string, ...any)
	arena *arena

	framePeriodNs atomic.Int64 // publish pacing floor (ns); 0 = back-to-back (stream)
	holdoffNs     atomic.Int64 // minimum time after a triggered frame before re-arm; 0 = off
	armSettle     time.Duration
	pollEvery     time.Duration

	// Lock-free control reads by the owner.
	running     atomic.Bool
	trigRising  atomic.Bool
	trigSrc     atomic.Int32
	stopReq     atomic.Bool
	acqMode     atomic.Int32
	avgCount    atomic.Int32
	eresLen     atomic.Int32
	avgGen      atomic.Uint32    // bumped on acq-mode/depth changes → ring clear
	singleArmed atomic.Bool      // SINGLE: stop after the next triggered frame
	memDepth    atomic.Int32     // decimated drain depth (samples): fps↔data tradeoff
	streamMode  atomic.Bool      // stitched high-bandwidth streaming decode mode
	chVdivBits  [2]atomic.Uint64 // per-channel V/div (float64 bits) for the
	//                              trigger-level → display-code mapping
	trigPosFrac atomic.Uint64 // horizontal trigger position, fraction of screen

	// mu guards the command shadows and the stats mirror. Setters record and
	// return; only the owner touches the bus (spec 09 §1). Bus writes happen
	// with mu released.
	mu        sync.Mutex
	norm      bool
	pendBand  Band
	pendSet   bool
	trigCode  uint16
	trigDirty bool
	trigInit  bool
	offCode   [2]uint16 // vertical-offset DAC shadows (spec 09 §1.3:
	offDirty  [2]bool   // no compare-on-change, no init flag)
	ledWord   uint16    // panel LED latch shadow (compare-on-change + init)
	ledDirty  bool
	ledInit   bool
	etsWant   bool // staged ETS opt-in; applied at the frame boundary
	tp        trigParams
	stats     Stats
	pubTimes  []time.Time // recent publish timestamps for the FPS window

	// matrixReq is the panel's request/reply channel (spec 08 §4): the owner
	// drains every pending request at the frame boundary with one snapshot.
	matrixReq chan chan [5]uint16

	// Owner-private state (no locking needed).
	band      Band
	prevKind  Kind
	lastNorm  bool
	seq       uint64
	flatHeld  int
	deadRuns  int
	streamSeq uint64    // stitch-mode window counter
	lastHalt  time.Time // wall-clock of the previous window's halt (for GapNs)
	done      chan struct{}

	// Envelope band state (spec 04 §1): ring of phase-scattered windows.
	envRing1, envRing2     [][]uint8
	envRingPos, envRingCnt int

	// Roll band state (spec 04 §2): free-run raw ring + scroll snapshots.
	rollArmed              bool
	rollRing1, rollRing2   []uint8
	rollPos                int
	rollSnaps1, rollSnaps2 [][]uint8

	// ETS state (spec 04 §3): persistent phase-interleave accumulator.
	etsOn                    bool
	etsSum1, etsSum2         []float64
	etsCnt                   []int32
	etsCov                   []bool
	etsAccFrames             int
	etsScratch1, etsScratch2 []uint8

	// Acq-mode state (spec 03 §7.4): AVERAGE ring, uniformity telemetry,
	// ERES scratch. avgKey tracks when the ring must clear.
	avg    avgRing
	uni    uniRing
	avgKey struct {
		gen   uint32
		width int
		norm  bool
	}
	eresScratch []uint16
}

func New(cfg Config) *Engine {
	if cfg.Clock.Now == nil {
		cfg.Clock = realClock()
	}
	if cfg.FramePeriod == 0 {
		cfg.FramePeriod = 50 * time.Millisecond
	}
	if cfg.ArmSettle == 0 {
		cfg.ArmSettle = 2 * time.Millisecond
	}
	if cfg.PollEvery == 0 {
		cfg.PollEvery = 150 * time.Microsecond
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	start, _ := PlanTdiv(500e-6) // decimated start detent: shows the cal edge fast
	e := &Engine{
		b:         cfg.Bus,
		clk:       cfg.Clock,
		logf:      cfg.Logf,
		arena:     newArena(deepRecord),
		armSettle: cfg.ArmSettle,
		pollEvery: cfg.PollEvery,
		band:      start,
		prevKind:  start.Kind(),
		done:      make(chan struct{}),
		matrixReq: make(chan chan [5]uint16, 4),
	}
	e.running.Store(true)
	e.trigRising.Store(true)
	e.avgCount.Store(16) // boot-firmware default; menu {4,16,32,64,128,256}
	e.eresLen.Store(1)
	e.chVdivBits[0].Store(math.Float64bits(1))
	e.chVdivBits[1].Store(math.Float64bits(1))
	e.trigPosFrac.Store(math.Float64bits(0.5)) // trigger at screen centre
	e.memDepth.Store(decimDrain)               // default decimated depth (fps↔data)
	e.framePeriodNs.Store(int64(cfg.FramePeriod))
	e.tp = defaultTrigParams()
	e.eresScratch = make([]uint16, deepRecord)
	e.mu.Lock()
	e.stats.Running, e.stats.TrigRising = true, true
	e.stats.MmapDrain = cfg.Bus.MmapDrain()
	e.stats.AvgCount, e.stats.EresLen = 16, 1
	e.syncBandStatsLocked()
	e.mu.Unlock()
	return e
}

// ---- trigger-type / acq-mode setters (spec 09 §1.2: software refinement,
// zero bus access; effective next frame) ----

func (e *Engine) SetTrigType(t int) {
	if t < int(TrigEdge) || t > int(TrigVideo) {
		t = int(TrigEdge)
	}
	e.mu.Lock()
	e.tp.typ = TrigType(t)
	e.stats.TrigType = t
	e.mu.Unlock()
}

func (e *Engine) SetPulseParams(lvlFrac, wMinNs, wMaxNs float64, cond int) {
	e.mu.Lock()
	e.tp.pulseLvlFrac = clampFrac(lvlFrac)
	e.tp.pulseWMinNs, e.tp.pulseWMaxNs = wMinNs, wMaxNs
	e.tp.pulseCond = cond & 3
	e.mu.Unlock()
}

func (e *Engine) SetSlopeParams(loFrac, hiFrac, tMinNs, tMaxNs float64, cond int) {
	e.mu.Lock()
	e.tp.slopeLoFrac = clampFrac(loFrac)
	e.tp.slopeHiFrac = clampFrac(hiFrac)
	e.tp.slopeTMinNs, e.tp.slopeTMaxNs = tMinNs, tMaxNs
	e.tp.slopeCond = cond & 3
	e.mu.Unlock()
}

func (e *Engine) SetVideoParams(std, line int, neg bool) {
	if std != 1 {
		std = 0
	}
	if line < 0 {
		line = 0
	}
	e.mu.Lock()
	e.tp.videoStd, e.tp.videoLine, e.tp.videoNeg = std, line, neg
	e.mu.Unlock()
}

func clampFrac(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func (e *Engine) SetAcqMode(m int) {
	if m < AcqNormal || m > AcqPeak {
		m = AcqNormal
	}
	e.acqMode.Store(int32(m))
	e.avgGen.Add(1) // mode change clears the average ring (spec 09 §2.2)
	e.mu.Lock()
	e.stats.AcqMode = m
	e.mu.Unlock()
}

func (e *Engine) SetAvgCount(n int) {
	if n < 1 {
		n = 1
	}
	if n > 256 {
		n = 256
	}
	e.avgCount.Store(int32(n))
	e.avgGen.Add(1)
	e.mu.Lock()
	e.stats.AvgCount = n
	e.mu.Unlock()
}

func (e *Engine) SetEresLen(l int) {
	l = clampEresLen(l)
	e.eresLen.Store(int32(l))
	e.mu.Lock()
	e.stats.EresLen = l
	e.mu.Unlock()
}

// ---- staging setters (any goroutine) ----

func (e *Engine) SetRunning(on bool) {
	e.running.Store(on)
	e.singleArmed.Store(false) // an explicit RUN or STOP both cancel a pending single-shot
	e.mu.Lock()
	e.stats.Running = on
	e.stats.Single = false
	e.mu.Unlock()
}

// SetSingle arms a true single-shot (spec 05 §3 note): NORM-armed, and the
// engine STOPs itself after the next triggered frame publishes. RUN cancels
// it. This is the "capture one and hold" behaviour a scope SINGLE button
// gives — unlike plain NORM, which keeps re-publishing triggered frames.
func (e *Engine) SetSingle() {
	e.SetNorm(true)
	e.running.Store(true)
	e.singleArmed.Store(true)
	e.mu.Lock()
	e.stats.Running, e.stats.Single = true, true
	e.mu.Unlock()
}

// SetChannelVdiv records a channel's V/div (from the analog front end) so the
// trigger level maps to a display code for level-anchored centring.
func (e *Engine) SetChannelVdiv(ch int, vdivV float64) {
	if vdivV <= 0 {
		vdivV = 1
	}
	e.chVdivBits[ch&1].Store(math.Float64bits(vdivV))
}

// SetTrigPosFrac sets where the trigger sits horizontally on screen: 0=left,
// 0.5=centre (default), 1=right. Pure software — the display window is offset
// so the anchor lands at this fraction (spec 05 §8: position is software).
func (e *Engine) SetTrigPosFrac(frac float64) {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	e.trigPosFrac.Store(math.Float64bits(frac))
	e.mu.Lock()
	e.stats.TrigPosFrac = frac
	e.mu.Unlock()
}

func (e *Engine) chVdivV(ch int) float64 {
	b := e.chVdivBits[ch&1].Load()
	if b == 0 {
		return 1
	}
	return math.Float64frombits(b)
}

// trigDispLevel maps the HW trigger level (DAC code) to a display code
// (0..255) at the source channel's V/div — the same mapping the on-screen
// level line uses. Returns -1 when no level is set (boot comparator), so
// centring falls back to the mid-level crossing.
func (e *Engine) trigDispLevel(srcCh int) int {
	e.mu.Lock()
	code := e.trigCode
	e.mu.Unlock()
	if code == 0 {
		return -1
	}
	dc := int(math.Round(128 + TrigLevelVolts(code)*32/e.chVdivV(srcCh)))
	if dc < 0 {
		dc = 0
	}
	if dc > 255 {
		dc = 255
	}
	return dc
}

func (e *Engine) SetNorm(on bool) {
	e.mu.Lock()
	e.norm = on
	e.stats.Norm = on
	e.mu.Unlock()
}

// SetTdiv stages a timebase change; it is applied at the next frame boundary
// with a full bring-up. Returns the resolved band, or ok=false if the value
// is not a v1 ladder detent.
func (e *Engine) SetTdiv(tdivS float64) (Band, bool) {
	b, ok := PlanTdiv(tdivS)
	if !ok {
		return Band{}, false
	}
	e.mu.Lock()
	e.pendBand, e.pendSet = b, true
	e.mu.Unlock()
	return b, true
}

// SetMemDepth sets the decimated drain depth in samples — the fps↔data knob.
// Shallow (down to one screen, decimWin) = highest frame rate; deep (up to the
// physical deepRecord) = more captured record to scroll, at a lower frame rate
// (a deeper record spans proportionally more capture time). Clamped to a valid
// range; native-fast/envelope/roll are unaffected.
func (e *Engine) SetMemDepth(samples int) int {
	if samples < decimWin {
		samples = decimWin
	}
	if samples > deepRecord {
		samples = deepRecord
	}
	e.memDepth.Store(int32(samples))
	return samples
}

// SetFramePeriod sets the publish pacing floor in milliseconds. 0 = run
// captures back-to-back at the hardware rate (the stream/stitch basis). Returns
// the applied value (clamped to [0, 1000] ms).
func (e *Engine) SetFramePeriod(ms int) int {
	if ms < 0 {
		ms = 0
	}
	if ms > 1000 {
		ms = 1000
	}
	e.framePeriodNs.Store(int64(ms) * int64(time.Millisecond))
	return ms
}

// SetHoldoff sets the trigger holdoff in seconds: after a triggered frame the
// engine waits at least this long before re-arming, so it re-triggers on the
// same event in a complex/bursty waveform instead of an intermediate edge.
// 0 disables it. Clamped to [0, 10] s. Returns the applied value.
func (e *Engine) SetHoldoff(sec float64) float64 {
	if sec < 0 {
		sec = 0
	}
	if sec > 10 {
		sec = 10
	}
	e.holdoffNs.Store(int64(sec * float64(time.Second)))
	e.mu.Lock()
	e.stats.HoldoffS = sec
	e.mu.Unlock()
	return sec
}

// paceHold is pace() with the trigger holdoff folded in: after a genuinely
// triggered publish it raises the inter-frame floor to the holdoff, delaying
// the next arm. Untriggered/AUTO frames pace at the normal floor.
func (e *Engine) paceHold(start time.Time, triggered bool) {
	floor := time.Duration(e.framePeriodNs.Load())
	if triggered {
		if h := time.Duration(e.holdoffNs.Load()); h > floor {
			floor = h
		}
	}
	if d := floor - e.clk.Now().Sub(start); d > 0 {
		e.clk.Sleep(d)
	}
}

// SetStreamMode toggles the stitched high-bandwidth streaming decode mode: the
// FSM captures back-to-back deep records with a PURE TIMED wait (no trigger /
// saturation poll — that wait was the recoverable overhead), publishing every
// window with continuity metadata so the client stitches them. It forces the
// deep record, un-paces publishing, and only runs on decimated bands (native-fast
// is burst-only via SINGLE; roll/envelope are their own paths). Returns the
// applied state.
func (e *Engine) SetStreamMode(on bool) bool {
	if on {
		e.memDepth.Store(deepRecord)
		e.SetFramePeriod(0)
	} else {
		e.SetFramePeriod(50)
	}
	e.streamMode.Store(on)
	e.mu.Lock()
	e.stats.Stream = on
	e.mu.Unlock()
	return on
}

// effDrainCols is how many samples oneFrame actually drains: the configured
// memory depth on decimated bands, the band's own drain elsewhere. A SINGLE
// capture always drains the FULL deep record so the one frame you keep carries
// everything to zoom out into — frame rate is irrelevant for a single shot.
func (e *Engine) effDrainCols() int {
	if e.band.Kind() == KindDecimated {
		if e.singleArmed.Load() {
			return deepRecord
		}
		d := int(e.memDepth.Load())
		if d < decimWin {
			d = decimWin
		}
		if d > deepRecord {
			d = deepRecord
		}
		return d
	}
	return e.band.DrainCols()
}

// SetTrigLevelCode stages a trigger-level DAC recommit. Codes clamp to the
// operational window. Compare-on-change with an init flag so the first set
// applies even if equal to the default. Code 0 means "keep the boot-inherited
// comparator" (spec 05): nothing is staged and 0 is returned.
func (e *Engine) SetTrigLevelCode(code uint16) uint16 {
	if code == 0 {
		return 0
	}
	if code < TrigCodeMin {
		code = TrigCodeMin
	}
	if code > TrigCodeMax {
		code = TrigCodeMax
	}
	e.mu.Lock()
	if !e.trigInit || code != e.trigCode {
		e.trigCode, e.trigDirty, e.trigInit = code, true, true
		e.stats.TrigCode = code
	}
	e.mu.Unlock()
	return code
}

// SetOffsetDAC stages a vertical-offset DAC write for a channel (0=C1,
// 1=C2). Codes are producer-clamped (analog.OffsetCode); the shadow is
// last-write-wins with no compare-on-change — redundant-traffic suppression
// is the producer's job (spec 09 §1.3).
func (e *Engine) SetOffsetDAC(ch int, code uint16) {
	if ch != 1 {
		ch = 0
	}
	e.mu.Lock()
	e.offCode[ch], e.offDirty[ch] = code, true
	if ch == 0 {
		e.stats.OffC1 = code
	} else {
		e.stats.OffC2 = code
	}
	e.mu.Unlock()
}

func (e *Engine) SetTrigSlope(rising bool) {
	e.trigRising.Store(rising)
	e.mu.Lock()
	e.stats.TrigRising = rising
	e.mu.Unlock()
}

// SetETS stages the equivalent-time opt-in (spec 04 §3: never auto-routed;
// only effective at tdiv ≤ 50 ns). Applied at the frame boundary like a band
// change.
func (e *Engine) SetETS(on bool) {
	e.mu.Lock()
	e.etsWant = on
	e.stats.ETS = on
	e.mu.Unlock()
}

func (e *Engine) SetTrigSource(ch int) {
	if ch != 1 {
		ch = 0
	}
	e.trigSrc.Store(int32(ch))
	e.mu.Lock()
	e.stats.TrigSource = ch
	e.mu.Unlock()
}

// ReadMatrix requests a key-matrix snapshot from the bus owner (spec 08 §4):
// non-blocking enqueue (ok=false when the queue is full), 200 ms reply
// timeout — the panel worker simply retries on the next interrupt or tick.
func (e *Engine) ReadMatrix() ([5]uint16, bool) {
	reply := make(chan [5]uint16, 1)
	select {
	case e.matrixReq <- reply:
	default:
		return [5]uint16{}, false
	}
	select {
	case m := <-reply:
		return m, true
	case <-time.After(200 * time.Millisecond):
		return [5]uint16{}, false
	}
}

// SetLEDs stages the panel LED latch word (spec 08 §5): compare-on-change
// with an init flag; the owner flushes the 4-write strobe at the boundary.
func (e *Engine) SetLEDs(word uint16) {
	e.mu.Lock()
	if !e.ledInit || word != e.ledWord {
		e.ledWord, e.ledDirty, e.ledInit = word, true, true
	}
	e.mu.Unlock()
}

// Stop asks the owner to exit at the next frame boundary (never mid-halt) and
// waits for it. The engine is left armed and filling — a safe state to be
// SIGKILLed in; only a mid-frame kill wedges the bus, and boundaries are the
// only place we stop.
func (e *Engine) Stop(timeout time.Duration) bool {
	e.stopReq.Store(true)
	select {
	case <-e.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Snapshot returns a copy of the stats. Never touches the bus.
func (e *Engine) Snapshot() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.stats
	// FPS = publishes within the trailing second.
	now := e.clk.Now()
	n := 0
	for _, t := range e.pubTimes {
		if now.Sub(t) <= time.Second {
			n++
		}
	}
	s.FPS = float64(n)
	return s
}

// Consume hands the newest published frame to a consumer (the web layer).
func (e *Engine) Consume() (*Frame, bool) { return e.arena.Consume() }

// ---- owner goroutine ----

// Run is the engine owner loop. It must be the only goroutine that ever
// touches the Bus. A panic is contained: logged, marked wedged, owner parks
// (no process exit — a fast crash-loop would trigger slot rollback, and the
// inherited fd must survive).
func (e *Engine) Run() {
	defer close(e.done)
	defer func() {
		if r := recover(); r != nil {
			e.logf("engine: PANIC (wedged, parked): %v", r)
			e.mu.Lock()
			e.stats.Wedged = true
			e.mu.Unlock()
			// Park servicing nothing: health stops advancing, the agent
			// relaunches us on the still-live fd.
			for !e.stopReq.Load() {
				e.clk.Sleep(100 * time.Millisecond)
			}
		}
	}()

	if v, err := e.b.Read(bus.PlaneCS1, selVersion); err != nil || v != bus.VersionMagic {
		e.logf("engine: version gate failed (v=%#04x err=%v) — refusing to drive", v, err)
		e.mu.Lock()
		e.stats.Wedged = true
		e.mu.Unlock()
		for !e.stopReq.Load() {
			e.clk.Sleep(100 * time.Millisecond)
		}
		return
	}

	e.bringUp()
	e.lastNorm = e.normNow()

	for !e.stopReq.Load() {
		e.serviceCommands()
		e.bumpFrames() // heartbeat advances every iteration, stopped or not

		if !e.running.Load() {
			// STOP keeps the FSM alive and servicing; it never parks the
			// engine in a halted state (spec 03 §8).
			e.clk.Sleep(50 * time.Millisecond)
			continue
		}

		// Apply staged band/mode/ETS changes at the boundary.
		e.mu.Lock()
		bandChange := e.pendSet
		if e.pendSet {
			e.band = e.pendBand
			e.pendSet = false
			e.syncBandStatsLocked()
		}
		norm := e.norm
		etsWant := e.etsWant
		e.mu.Unlock()
		if bandChange || norm != e.lastNorm || etsWant != e.etsOn {
			e.transition(norm, etsWant)
		}

		switch {
		case e.streamMode.Load() && e.band.Kind() == KindDecimated:
			e.stitchFrame(norm)
		case e.band.Kind() == KindRoll:
			e.rollUpdate(norm)
		case e.band.Kind() == KindEnvelope:
			e.envFrame(norm)
		case e.etsOn && e.band.ETSEligible():
			e.etsFrame(norm)
		default:
			e.oneFrame(norm)
		}
	}
}

func (e *Engine) normNow() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.norm
}

func (e *Engine) bumpFrames() {
	e.mu.Lock()
	e.stats.Frames++
	e.mu.Unlock()
}

func (e *Engine) runWord() uint16 {
	if e.normNow() {
		return runNorm
	}
	return runAuto
}

func (e *Engine) w(sel, val uint16) {
	if err := e.b.Write(bus.PlaneCS1, sel, val); err != nil {
		e.busErr(err)
	}
}

func (e *Engine) w3(sel, val uint16) {
	if err := e.b.Write(bus.PlaneCS3, sel, val); err != nil {
		e.busErr(err)
	}
}

func (e *Engine) r(sel uint16) uint16 {
	v, err := e.b.Read(bus.PlaneCS1, sel)
	if err != nil {
		e.busErr(err)
	}
	return v
}

func (e *Engine) busErr(err error) {
	e.mu.Lock()
	e.stats.BusErrors++
	n := e.stats.BusErrors
	e.mu.Unlock()
	if n <= 5 || n%100 == 0 {
		e.logf("engine: bus error #%d: %v", n, err)
	}
}

// bringUp is the engine enable+divisor sequence (spec 03 §4.1), run once at
// start and again on every band or trigger-mode change — never per frame.
// Divisor-hi is cleared FIRST (a stale hi silently mis-clocks every hi=0
// band; live precisely on slow→fast transitions where hi ≠ 0). It programs
// Prog() — the envelope formula / roll divisor on slow bands, never the
// nominal table row. It writes no CS3 registers: the boot comparator is
// inherited.
func (e *Engine) bringUp() {
	class, lo, hi := e.band.Prog()
	e.w(selResetHd, 0x0001)
	e.w(selResetHd, 0x0000)
	e.w(selRunWord, e.runWord())
	e.w(selReset2, 0x0000)
	e.w(selDivHi, 0x0000)
	e.w(selClass, class)
	e.w(selDivLo, lo)
	e.w(selDivHi, hi)
}

// armEngine per spec 03 §5.1: reset-head ×2, write-pointer pulse, settle, go.
func (e *Engine) armEngine() {
	e.w(selArm, opResetHead)
	e.w(selArm, opResetHead)
	e.w(selWrPtr, 0x0001)
	e.w(selWrPtr, 0x0000)
	e.clk.Sleep(e.armSettle)
	e.w(selArm, opGo)
}

// waitCapture runs the bounded wait gate (spec 03 §5.2): poll 0x39 + 0x46
// every 150 µs within the band budget. Returns the gate results plus whether
// the fill counter advanced at all (wedge evidence when it never does).
//
// A frame is complete when it anchors on the comparator DONE bit (0x39 bit2,
// a real trigger) with the post-trigger record filled (0x46 ≥ LatchAt) — the
// triggered path, both modes. In AUTO it ALSO completes when the free-running
// record has simply filled to (near) the 11-bit counter max: an untriggered
// AUTO display then publishes a free-run frame at the full FSM rate instead
// of holding on every frame that fails to trigger. NORM never takes the
// free-run path — a quiet NORM screen legitimately holds until a real trigger.
// `full` is tracked independently of `anchored` (they were coupled before,
// which starved AUTO). bit0 (VALID/auto-timeout) is honoured too as an early
// AUTO completion.
func (e *Engine) waitCapture(norm bool) (anchored, sawTrig, filled, fillMoved bool, trigPos int) {
	start := e.clk.Now()
	deadline := start.Add(time.Duration(e.band.WaitBudgetNs()))
	// Decimated NORM needs a DENSE record — the buffer filled to drainCols — so
	// software centring locks a mid-record crossing instead of the jittery LAST
	// crossing at the rail boundary of a sparse (fill≥LatchAt) triggered capture
	// (that boundary drifts non-periodically frame-to-frame → the wave jitters).
	// The fill counter saturates at 11 bits well before drainCols, so gate on
	// TIME: the interval to clock drainCols samples. This is free (well under the
	// 50 ms publish pace floor) and scoped to decimated NORM — native-fast, AUTO,
	// envelope and roll are untouched. AUTO already fills densely via its budget.
	denseWait := norm && e.band.Kind() == KindDecimated
	denseNs := int64(float64(e.effDrainCols()) * e.band.CaptureIntervalNs())
	// Native-fast FREE RUN + TRIGGER HOLD (spec 04 §11): halt once the HW comparator has fired
	// (bit1) AND the deep record has FILLED — so the frozen record is coherent to ~20480 (spec
	// 04 §4) with the edge near record/2 (cross-frame std 1–2). The comparator fires almost
	// immediately on a continuous signal, so returning on bit1 ALONE freezes a half-filled
	// buffer whose unwritten tail drains as a flat dead repeat (display then centres on the
	// live/dead boundary; super-res sees the dead tail). The fill counter saturates at 11 bits
	// well before drainCols, so gate the fill on TIME — the interval to clock drainCols samples
	// (denseNs) — exactly as decimated NORM does above. On the budget timeout (no comparator
	// edge) AUTO free-runs a live refresh; NORM holds.
	nativeFast := e.band.NativeFast()
	fill0 := e.r(selFill) & fillMask
	for {
		s := e.r(selStatus)
		if s&statTrig != 0 && !sawTrig {
			sawTrig = true
			trigPos = int(e.r(selTrigHi))<<8 | int(e.r(selTrigLo)&0xff)
		}
		completed := s&statDone != 0
		if !norm && s&statValid != 0 {
			completed = true // AUTO free-run timeout
		}
		if completed && !anchored {
			anchored = true
			if !sawTrig {
				trigPos = int(e.r(selTrigHi))<<8 | int(e.r(selTrigLo)&0xff)
			}
		}
		fill := e.r(selFill) & fillMask
		if fill != fill0 {
			fillMoved = true
		}
		if fill >= latchAt {
			filled = true
		}
		if nativeFast {
			// Spec 04 §8.1/§8.2/§8.3: native-fast halts when the free-run fill
			// COMPLETES — bit2(done) AND fill ≥ LatchAt, both of which assert on the
			// untriggered free-run — NOT on bit1(trig), which "can lag or never
			// assert" (§8.3). Gating on bit1 (sawTrig) waits for a trigger that never
			// comes → the budget times out on a half-filled buffer whose unwritten
			// tail drains as a flat dead repeat. Halt is unconditional; content
			// discrimination (§8.2) then decides publish vs hold.
			if anchored && filled {
				return
			}
		} else {
			if anchored && filled {
				// Decimated NORM also waits for a dense buffer (see above); other
				// bands return as soon as the post-trigger record is filled.
				if !denseWait || e.clk.Now().Sub(start) >= time.Duration(denseNs) {
					return // triggered, post-trigger record filled (and dense in NORM)
				}
			}
			if !norm && fill >= fillFull {
				return // AUTO free-run: the record saturated, drain it now
			}
		}
		if e.stopReq.Load() {
			return // abandon armed+filling: safe; boundary handles shutdown
		}
		if !e.clk.Now().Before(deadline) {
			return // budget expired: AUTO free-runs a refresh, NORM holds
		}
		e.clk.Sleep(e.pollEvery)
	}
}

// halt latches the frozen record (0xC8) and confirms the fill froze. Freezing
// a free-running (untriggered AUTO) buffer can take a few bus cycles to
// settle, so poll a handful of times and accept the first pair of equal reads
// rather than demand the very first back-to-back pair match — a strict
// double-read spuriously fails on ~1/5 of AUTO frames and holds them.
func (e *Engine) halt() bool {
	e.w(selArm, opHalt)
	prev := e.r(selFill) & fillMask
	for i := 0; i < 5; i++ {
		cur := e.r(selFill) & fillMask
		if cur == prev {
			return true
		}
		prev = cur
	}
	return false
}

// drain reads the frozen deep record into the producer slot: sample i comes
// from port 0x30+(i mod 5); each word packs C1 in the high byte, C2 low.
func (e *Engine) drain(f *Frame, cols int) {
	for i := 0; i < cols; i++ {
		w := e.b.DrainRead(uint16(drainBase + i%5))
		f.C1[i] = uint8(w >> 8)
		f.C2[i] = uint8(w)
	}
}

// stitchFrame runs one STREAM window: arm → PURE TIMED wait of exactly N·dt
// (no trigger/saturation poll — that wait is the recoverable overhead the
// free-run/hold technique eliminates) → halt → mmap drain → re-arm → publish
// EVERY window raw + contiguous with continuity metadata. The client stitches
// consecutive windows on one axis, marking the GapNs blackout (the unavoidable
// drain+re-arm time) between them, and decodes per window.
func (e *Engine) stitchFrame(norm bool) {
	cols := e.effDrainCols()
	fillNs := int64(float64(cols) * e.band.CaptureIntervalNs())

	armStart := e.clk.Now()
	var gapNs int64
	if !e.lastHalt.IsZero() {
		gapNs = int64(armStart.Sub(e.lastHalt))
	}
	e.armEngine() // opGo: begin the free-run fill

	// Pure timed wait: the deep record is full after N·dt. No status/trigger
	// poll — a triggered wait was the ~11 ms/frame overhead we're removing.
	target := armStart.Add(time.Duration(fillNs))
	for {
		if e.interrupted() {
			return // armed+filling is a safe park
		}
		rem := target.Sub(e.clk.Now())
		if rem <= 0 {
			break
		}
		if rem > e.pollEvery {
			rem = e.pollEvery
		}
		e.clk.Sleep(rem)
	}
	if e.stopReq.Load() {
		return
	}

	haltOK := e.halt()
	e.lastHalt = e.clk.Now()
	f := e.arena.Write()
	drainStart := e.clk.Now()
	e.drain(f, cols)
	drainMs := e.clk.Now().Sub(drainStart)
	// No re-arm here — the next stitchFrame arms once. (A re-arm here would be
	// discarded by that arm's reset-head and just double the arm overhead/gap.)

	// Raw, contiguous, edge-agnostic — the stream is not trigger-centred. WinCols
	// stays the screen window so the web deep-serve path (Valid > WinCols) ships
	// the FULL raw record and the client navigator spans the whole window.
	f.Valid, f.WinCols = cols, decimWin
	f.EdgeX = -1
	f.Interp, f.IsEnv, f.EnvCols, f.RollCodes = false, false, 0, false
	f.Norm = norm
	f.TdivS = e.band.TdivS
	f.DisplayedS = e.band.DisplayedSdivS()
	f.SampleS = e.band.CaptureIntervalNs() * 1e-9
	_, _, p := ptp(f.C1[:cols])
	f.Ptp, f.Trigd, f.Coherent, f.HaltOK = p, false, true, haltOK
	e.streamSeq++
	f.StreamSeq, f.WindowNs, f.GapNs = e.streamSeq, fillNs, gapNs

	e.seq++
	f.Seq = e.seq
	e.arena.Publish()

	e.mu.Lock()
	e.stats.Published++
	e.stats.Seq = e.seq
	e.stats.Coherent++
	e.stats.LastPtp = p
	e.stats.ValidDepth = validDepth(f.C1[:cols])
	e.stats.MemDepth = int(e.memDepth.Load())
	e.stats.DrainMs = float64(drainMs) / float64(time.Millisecond)
	e.stats.GapMs = float64(gapNs) / float64(time.Millisecond)
	e.stats.Stream = true
	e.pubTimes = append(e.pubTimes, e.clk.Now())
	if len(e.pubTimes) > 64 {
		e.pubTimes = e.pubTimes[len(e.pubTimes)-64:]
	}
	e.mu.Unlock()
}

// oneFrame runs one arm→wait→halt→drain→re-arm→publish iteration.
func (e *Engine) oneFrame(norm bool) {
	start := e.clk.Now()

	e.armEngine()
	anchored, sawTrig, filled, fillMoved, trigPos := e.waitCapture(norm)
	armToLatch := e.clk.Now().Sub(start)
	if e.stopReq.Load() {
		return
	}

	nativeFast := e.band.NativeFast()
	// Decimated readiness: NORM requires a real trigger with the post-trigger
	// record filled (0x46 counts post-trigger samples, so it only advances
	// after a comparator edge). AUTO always drains — either the fast
	// triggered path, or, after the budget, the frozen free-running buffer
	// (which holds a coherent 2048-sample snapshot of the live signal). This
	// is what makes an untriggered AUTO display update at the full rate
	// instead of holding on every frame that fails to trigger.
	ready := (anchored && filled) || !norm
	if !nativeFast && !ready {
		e.holdFrame(fillMoved, norm)
		e.pace(start)
		return
	}

	haltOK := e.halt()
	f := e.arena.Write()
	cols := e.effDrainCols()
	drainStart := e.clk.Now()
	e.drain(f, cols)
	drainMs := e.clk.Now().Sub(drainStart)
	e.armEngine() // re-arm immediately: filling again before publish/render

	// ERES boxcar runs on the whole record BEFORE any discrimination, so the
	// anchor and the display see the same enhanced samples (spec 03 §7.4).
	mode := int(e.acqMode.Load())
	if mode == AcqEres {
		if l := clampEresLen(int(e.eresLen.Load())); l > 1 {
			eresBoxcar(f.C1[:cols], l, e.eresScratch)
			eresBoxcar(f.C2[:cols], l, e.eresScratch)
		}
	}

	// Set or clear ALL metadata — the arena frames are reused in place. A
	// decimated frame is coherent when triggered (anchored+filled) OR an
	// AUTO free-run full record; that is the `ready` gate that let us drain.
	coherent := haltOK && (nativeFast || ready)
	disc := f.C1[:cols]
	if int(e.trigSrc.Load()) == 1 {
		disc = f.C2[:cols]
	}
	lo, hi, p := ptp(disc)
	rising := e.trigRising.Load()

	// Qualifier dispatch (spec 05): PULSE/SLOPE/VIDEO REPLACE the EDGE
	// pipeline; their own polarity/monotonicity logic is the validation.
	e.mu.Lock()
	tp := e.tp
	e.mu.Unlock()
	interval := e.band.CaptureIntervalNs()
	edgeX := -1.0
	lvlOffSig := false // EDGE: trigger level is set but sits off the signal band
	switch tp.typ {
	case TrigPulse:
		edgeX = qualifyPulse(disc, interval, tp, rising)
	case TrigSlope:
		edgeX = qualifySlope(disc, interval, tp, rising)
	case TrigVideo:
		edgeX = qualifyVideo(disc, tp)
	default:
		// EDGE: anchor on the user's HW trigger level (WYSIWYG — the display
		// crosses where the level is set). A lock requires a right-slope crossing
		// AT that level (centerCross returns ONLY a crossing of the requested
		// slope, so edgeX ≥ 0 already means "a right-slope crossing exists").
		// If the level is SET but off the signal band, NO trigger is possible —
		// we must NOT fall back to a mid-level crossing and fabricate a lock the
		// user never asked for (spec 05 §5.1). Leaving edgeX = -1 means no lock:
		// AUTO free-runs unlocked, NORM holds. The mid-level fallback survives
		// only for an UNSET (boot) level, to keep the very first frames stable.
		if td := e.trigDispLevel(int(e.trigSrc.Load())); td >= 0 {
			edgeX = centerCross(disc, td, rising)
			if p >= nativeEdgeMinPtp { // a real signal the level can sit outside of
				margin := (hi - lo) / 16
				lvlOffSig = td < lo-margin || td > hi+margin
			}
		} else {
			edgeX = centerCross(disc, midLevel(disc), rising)
		}
	}

	f.Valid = cols
	f.WinCols = e.band.WinCols()
	f.Interp = nativeFast
	f.IsEnv, f.EnvCols = false, 0
	f.Ptp = p
	f.Trigd = sawTrig
	f.TrigPos = trigPos
	f.Coherent = coherent
	f.HaltOK = haltOK
	f.RollCodes = false
	f.TdivS = e.band.TdivS
	f.DisplayedS = e.band.DisplayedSdivS()
	f.SampleS = e.band.CaptureIntervalNs() * 1e-9
	f.Norm = norm

	// Publish policy — ONLY DISPLAY FRAMES THAT HAVE A LOCK (spec 03 §7.4, spec 05 §4,
	// spec 04 §8.2). A "lock" is a validated triggered event on the captured CONTENT:
	//   native-fast: real edge content (ptp ≥ nativeEdgeMinPtp) AND a right-slope crossing —
	//                the done-gate is unreliable here so content decides (spec 03 §6, §8.2)
	//   decimated:   a COHERENT capture AND a right-slope crossing (spec 05 §4.2)
	//   qualifier:   a qualifying PULSE/SLOPE/VIDEO event (its own edgeX) on the same basis
	// The gate is a right-slope crossing (edgeX ≥ 0) on a REAL signal (ptp ≥ nativeEdgeMinPtp
	// rejects a flat rail / noise), plus a COHERENT capture on decimated bands. We deliberately
	// do NOT fold in windowSlopeMatches here: that plateau test is reliable only while winCols/4
	// stays within one signal period, so at a dense multi-period window (1–2 ms) landing on a
	// non-integer phase it false-rejects a genuine edge and silently freezes the band. The
	// right-slope crossing + amplitude + coherence already define the lock. No lock → HOLD the
	// last locked frame; never flash a jittery un-anchored capture. A genuinely flat / no-signal
	// screen has no lock to be had: AUTO (and native-fast in either mode, spec §8.2) keeps it
	// live with one honest flat capture (EdgeX = -1) every nativeFlatFallbck held frames.
	// HOLDING — not free-running — between fallbacks re-presents the last edge, so an
	// intermittent-edge sub-period band (2–20 µs) shows a stable held edge, never an edge↔flat
	// flicker.
	qualifier := tp.typ != TrigEdge

	lock := edgeX >= 0 && (qualifier || p >= nativeEdgeMinPtp)
	if !nativeFast {
		lock = lock && coherent
	}

	publish := false
	switch {
	case lock:
		// A triggered / qualified edge is present — the native-fast comparator fired (sawTrig)
		// so the edge is in the record, or a coherent slope-valid decimated capture — publish
		// it centred.
		publish = true
		e.flatHeld = 0
	case !norm && !qualifier && lvlOffSig && coherent:
		// AUTO, EDGE: the trigger level is off the signal entirely — a lock is
		// impossible, so never claim a trig and never freeze. FREE-RUN an unlocked
		// live capture at the record centre (EdgeX = -1, Trigd = false). NORM
		// instead HOLDs (waits for a trigger that cannot come) via the default.
		publish = true
		edgeX = -1
		f.Trigd = false
		e.flatHeld = 0
	case nativeFast && !norm && !qualifier && !sawTrig:
		// AUTO native-fast, comparator did NOT fire within the budget (untriggered): FREE RUN a
		// live refresh at the record centre (spec 04 §3 routing + §11) instead of holding. This
		// is the different technique the ≤200 ns bands need — there the record spans ≪ one
		// period so the edge rarely aligns and a catch-and-HOLD would freeze (the ~0 fps case);
		// it keeps any quiet native-fast screen live at ~20 fps. Uncentred (EdgeX = -1, the
		// record centre where a caught edge is HW-positioned): no software anchor on noise.
		publish = true
		edgeX = -1
		e.flatHeld = 0
	case (nativeFast || !norm) && !qualifier && p < nativeEdgeMinPtp:
		// NORM native-fast flat (trigger-hold with an honest 60-frame refresh), or AUTO
		// decimated flat: publish one honest flat capture every nativeFlatFallbck held frames.
		e.flatHeld++
		if e.flatHeld >= nativeFlatFallbck {
			edgeX = -1 // one honest flat capture; never fabricate an edge
			publish = true
			e.flatHeld = 0
		}
	default:
		// NORM decimated quiet screen, an un-fired qualifier, or AUTO decimated signal-present-
		// but-not-locked this frame (it would jitter) → HOLD the last locked frame.
		publish = false
	}
	if !publish {
		edgeX = -1 // held frames never leave the arena; keep metadata sane
	}
	f.EdgeX = edgeX

	// AVERAGE (spec 03 §7.4): only published, coherent, edge-aligned frames
	// enter the ring; the published samples become the ring mean. Flat
	// fallbacks publish RAW. The ring clears on acq-mode/depth/band/NORM
	// changes (avgKey tracks all four).
	if publish && mode == AcqAverage && coherent && edgeX >= 0 {
		if n := int(e.avgCount.Load()); n > 1 {
			gen := e.avgGen.Load()
			width := e.band.WinCols()
			if e.avgKey.gen != gen || e.avgKey.width != width || e.avgKey.norm != norm {
				e.avg.reset(n, width)
				e.avgKey.gen, e.avgKey.width, e.avgKey.norm = gen, width, norm
			}
			e.avg.push(f, edgeX)
			e.avg.meanInto(f) // rewrites samples, Valid and EdgeX (centre)
			edgeX = f.EdgeX
		}
	}

	// Cross-frame uniformity telemetry over published frames (spec 03 §11).
	if publish {
		e.uni.push(disc, e.band.WinCols(), edgeX)
		std, raw, worst := e.uni.stats()
		e.mu.Lock()
		e.stats.WinColStd, e.stats.WinColRaw, e.stats.WinColMax = std, raw, worst
		e.mu.Unlock()
	}

	e.mu.Lock()
	if coherent {
		e.stats.Coherent++
	}
	if haltOK {
		e.stats.HaltConfirm++
	}
	e.stats.LastPtp = p
	e.stats.LastTrigPos = trigPos
	e.stats.ValidDepth = validDepth(disc)
	e.stats.MemDepth = int(e.memDepth.Load())
	e.stats.ArmToLatch = float64(armToLatch) / float64(time.Millisecond)
	e.stats.DrainMs = float64(drainMs) / float64(time.Millisecond)
	e.mu.Unlock()

	if publish {
		e.seq++
		f.Seq = e.seq
		e.arena.Publish()
		e.mu.Lock()
		e.stats.Published++
		e.stats.Seq = e.seq
		e.pubTimes = append(e.pubTimes, e.clk.Now())
		if len(e.pubTimes) > 64 {
			e.pubTimes = e.pubTimes[len(e.pubTimes)-64:]
		}
		e.mu.Unlock()
		// True single-shot: a real triggered frame just published — stop and
		// hold it. `coherent` here is a NORM/qualifier-gated capture (single
		// forces NORM), so this is a genuine trigger, not a free-run frame.
		if e.singleArmed.Load() && coherent {
			e.singleArmed.Store(false)
			e.running.Store(false)
			e.mu.Lock()
			e.stats.Running, e.stats.Single = false, false
			e.mu.Unlock()
			e.logf("single-shot captured seq=%d — stopped", e.seq)
		}
	} else {
		e.mu.Lock()
		e.stats.Held++
		e.mu.Unlock()
	}

	// Wedge evidence must survive the drain path too (spec 03 §11): a frozen
	// fill fakes both the halt confirmation (equal double-read) and
	// "coherent", so reset the ladder only on genuine activity — fill
	// advancing or a non-flat drain.
	if fillMoved || p >= nativeEdgeMinPtp {
		e.resetDeadRuns()
	} else {
		e.deadEvidence(false)
	}
	e.paceHold(start, publish && f.Trigd) // holdoff extends the floor after a real trigger
}

// holdFrame accounts a decimated hold and feeds the wedge ladder: a quiet
// NORM keeps the fill advancing; a dead bus does not. A frozen SMALL fill at
// a decimated band cannot be counter saturation (a saturated counter would
// have set the filled gate), so it is certain wedge evidence.
func (e *Engine) holdFrame(fillMoved, norm bool) {
	e.mu.Lock()
	e.stats.Held++
	e.mu.Unlock()
	if fillMoved {
		e.resetDeadRuns()
		return
	}
	e.deadEvidence(true)
}

func (e *Engine) resetDeadRuns() {
	e.deadRuns = 0
	e.mu.Lock()
	e.stats.DeadRuns = 0
	e.mu.Unlock()
}

// deadEvidence walks the wedge-recovery ladder (spec 03 §11): re-assert
// bring-up every 10 dead frames; at 50, mark Wedged — which stops the health
// token so the agent relaunches us on the still-live fd. On the drain path
// (certain=false) a healthy-but-flat input at a native-fast band is
// indistinguishable from a wedge by fill+ptp alone (the 11-bit counter can
// sit saturated between polls), so Wedged additionally requires a dead
// fabric: CONF_DONE (CS3 0x07 bit7) reading clear. Otherwise we keep
// re-asserting bring-up and surface DeadRuns instead of crash-looping a
// healthy app.
func (e *Engine) deadEvidence(certain bool) {
	e.deadRuns++
	e.mu.Lock()
	e.stats.DeadRuns = e.deadRuns
	e.mu.Unlock()
	if e.deadRuns%10 != 0 {
		return
	}
	e.logf("engine: %d dead frames (fill frozen, flat drain) — re-asserting bring-up", e.deadRuns)
	e.bringUp()
	if e.deadRuns%50 != 0 {
		return
	}
	if certain {
		e.logf("engine: %d dead frames at a decimated band — marking wedged (agent will relaunch)", e.deadRuns)
		e.mu.Lock()
		e.stats.Wedged = true
		e.mu.Unlock()
		return
	}
	if v, err := e.b.Read(bus.PlaneCS3, cs3ConfStatus); err == nil && v&0x80 == 0 {
		e.logf("engine: CONF_DONE lost after %d dead frames — marking wedged (agent will relaunch)", e.deadRuns)
		e.mu.Lock()
		e.stats.Wedged = true
		e.mu.Unlock()
		return
	}
	e.logf("engine: %d dead frames but CONF_DONE high — flat input or partial wedge; continuing with periodic bring-up", e.deadRuns)
}

// serviceCommands flushes staged panel/CS3 work at the frame boundary — the
// engine is armed+filling here, never inside a halt window. Snapshot+clear
// under the mutex; bus writes with it released (they sleep in the re-arm).
// Servicing order per spec 09 §4: matrix requests, LED latch, offset DACs,
// then the trigger level.
func (e *Engine) serviceCommands() {
	// Drain every pending matrix request with ONE snapshot (a CS1
	// config-plane read does not pop the sample FIFO — safe while filling;
	// and 0x69 is read exactly once per boundary).
	var matrixSnap [5]uint16
	matrixRead := false
drain:
	for {
		select {
		case r := <-e.matrixReq:
			if !matrixRead {
				for i, sel := range [5]uint16{0x64, 0x65, 0x66, 0x67, 0x69} {
					matrixSnap[i] = e.r(sel)
				}
				matrixRead = true
			}
			r <- matrixSnap
		default:
			break drain
		}
	}

	e.mu.Lock()
	trigDirty, code := e.trigDirty, e.trigCode
	offDirty, offCode := e.offDirty, e.offCode
	ledDirty, ledWord := e.ledDirty, e.ledWord
	e.trigDirty = false
	e.offDirty = [2]bool{}
	e.ledDirty = false
	e.mu.Unlock()

	// LED latch strobe (spec 08 §5): one indivisible 4-write burst, never
	// interleaved with any other CS3 write.
	if ledDirty {
		e.w3(0x0b, 0)
		e.w3(0x0a, ledWord>>8)
		e.w3(0x09, ledWord&0xff)
		e.w3(0x0b, 1)
	}

	// Vertical offset (spec 06 §5.3): low byte, then self-latching high
	// byte, then re-assert the CS1 run word to re-anchor the front-end
	// change on the once-armed engine.
	if offDirty[0] {
		e.w3(cs3OffC1Lo, offCode[0]&0xff)
		e.w3(cs3OffC1Hi, offCode[0]>>8)
	}
	if offDirty[1] {
		e.w3(cs3OffC2Lo, offCode[1]&0xff)
		e.w3(cs3OffC2Hi, offCode[1]>>8)
	}
	if offDirty[0] || offDirty[1] {
		e.w(selRunWord, e.runWord())
	}

	if !trigDirty {
		return
	}
	// The trigger-level safe recommit (spec 05 §1.3): level quad (both lanes
	// the same code, high bytes self-latch), comparator re-anchor preamble,
	// then a full re-arm. A bare level poke off this path wedges the display.
	lo, hi := code&0xff, code>>8
	e.w3(cs3LevelALo, lo)
	e.w3(cs3LevelAHi, hi)
	e.w3(cs3LevelBLo, lo)
	e.w3(cs3LevelBHi, hi)
	e.w(selPreamble, 0x0080)
	e.w(selPreamble, 0x0080)
	e.armEngine()
	e.logf("engine: trigger level recommitted, code=%#04x", code)
}

func (e *Engine) syncBandStatsLocked() {
	e.stats.TdivS = e.band.TdivS
	e.stats.DisplayedS = e.band.DisplayedSdivS()
	switch e.band.Kind() {
	case KindNativeFast:
		e.stats.BandKind = "native-fast"
	case KindDecimated:
		e.stats.BandKind = "decimated"
	case KindEnvelope:
		e.stats.BandKind = "envelope"
	case KindRoll:
		e.stats.BandKind = "roll"
	}
	if e.band.Kind() == KindRoll {
		e.stats.HaltMode = "latch-no-halt"
	} else {
		e.stats.HaltMode = "capture-halt"
	}
}

// pace enforces the ~50 ms frame-period floor (spec 03 §5.3): faster starves
// the single shared ARM core and lowers delivered fps.
func (e *Engine) pace(start time.Time) {
	if d := time.Duration(e.framePeriodNs.Load()) - e.clk.Now().Sub(start); d > 0 {
		e.clk.Sleep(d)
	}
}

// TrigLevelVolts converts a DAC code to approximate volts using the linear
// fit exact at 1 V/2 V-div (spec 05 §1.2): code = 31434 − 938·V.
func TrigLevelVolts(code uint16) float64 { return (31434 - float64(code)) / 938 }
