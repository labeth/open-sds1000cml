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
	WinCols     int     `json:"win_cols"`       // display-window width in raw samples
	MemDepth    int     `json:"mem_depth"`      // configured decimated drain depth
	Stream      bool    `json:"stream"`         // stitched streaming decode mode on
	GapMs       float64 `json:"gap_ms"`         // stream: blackout between windows

	// Zone trigger + mask testing (docs/zonemask-plan.md)
	ZoneMode    int   `json:"zone_mode,omitempty"`  // 0 off, 1 trigger
	ZoneCount   int   `json:"zone_count,omitempty"` // installed zones
	ZoneSkip    int64 `json:"zone_skip,omitempty"`  // zone-armed publishes that were untestable (env/roll)
	MaskMode    int   `json:"mask_mode,omitempty"`  // 0 off, 1 test, 2 stop-on-fail
	MaskPass    int64 `json:"mask_pass,omitempty"`
	MaskFail    int64 `json:"mask_fail,omitempty"`
	MaskSkip    int64 `json:"mask_skip,omitempty"`    // frames not comparable (scale/mode changed)
	MaskRing    int   `json:"mask_ring,omitempty"`    // captured failures available
	MaskStopped bool  `json:"mask_stopped,omitempty"` // stop-on-fail latched
	MaskSet     bool  `json:"mask_set,omitempty"`     // an envelope mask is installed

	// Serial / protocol trigger (serialtrig.go)
	SerialMode    int   `json:"serial_mode,omitempty"`    // 0 off, 1 armed
	SerialMatches int64 `json:"serial_matches,omitempty"` // frames that matched the pattern
	SerialSet     bool  `json:"serial_set,omitempty"`     // a match pattern is configured

	// FRA / Bode plot (bode.go)
	BodeMode     int     `json:"bode_mode,omitempty"`      // 0 off, 1 armed
	BodePoints   int     `json:"bode_points,omitempty"`    // accumulated curve size
	BodeValid    bool    `json:"bode_valid,omitempty"`     // the live point is valid
	BodeFreqHz   float64 `json:"bode_freq_hz,omitempty"`   // live point frequency
	BodeGainDB   float64 `json:"bode_gain_db,omitempty"`   // live point magnitude (dB)
	BodePhaseDeg float64 `json:"bode_phase_deg,omitempty"` // live point phase (deg)
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

	// zone trigger + mask testing (zonemask.go)
	zoneMode    atomic.Int32
	maskMode    atomic.Int32
	maskPass    atomic.Int64
	maskFail    atomic.Int64
	maskSkip    atomic.Int64
	maskStopped atomic.Bool
	maskCap     atomic.Uint64 // capture ordinal: counts every mask-tested frame
	beatN       atomic.Uint64 // liveness heartbeat (see Beats)
	zoneSkip    atomic.Int64  // zone-armed publishes that could not be tested (env/roll)
	zoneHeld    int           // engine goroutine only: AUTO liveness fallback counter
	zm          zoneMaskState
	// serial / protocol trigger (serialtrig.go)
	serialMode    atomic.Int32
	serialMatches atomic.Int64
	serialHeld    int // engine goroutine only: AUTO liveness fallback counter
	ser           serialState
	bodeMode      atomic.Int32 // FRA (Bode) accumulation armed (bode.go)
	bode          bodeState

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
	e.stats.TrigPosFrac = 0.5 // mirror the atomic's boot default (readers normalize 0, but don't lie)
	e.stats.MmapDrain = cfg.Bus.MmapDrain()
	e.stats.AvgCount, e.stats.EresLen = 16, 1
	e.syncBandStatsLocked()
	e.mu.Unlock()
	return e
}

// ---- trigger-type / acq-mode setters (spec 09 §1.2: software refinement,
// zero bus access; effective next frame) ----

// ---- staging setters (any goroutine) ----

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
	// zone/mask live state (atomics + ring size)
	s.WinCols = e.band.WinCols()
	s.ZoneMode = int(e.zoneMode.Load())
	s.ZoneSkip = e.zoneSkip.Load()
	s.MaskMode = int(e.maskMode.Load())
	s.MaskPass = e.maskPass.Load()
	s.MaskFail = e.maskFail.Load()
	s.MaskSkip = e.maskSkip.Load()
	s.MaskStopped = e.maskStopped.Load()
	e.zm.mu.Lock()
	s.ZoneCount = len(e.zm.zones)
	s.MaskRing = len(e.zm.ring)
	s.MaskSet = e.zm.mask != nil
	e.zm.mu.Unlock()
	s.SerialMode = int(e.serialMode.Load())
	s.SerialMatches = e.serialMatches.Load()
	e.ser.mu.Lock()
	s.SerialSet = !e.ser.params.empty()
	e.ser.mu.Unlock()
	s.BodeMode = int(e.bodeMode.Load())
	e.bode.mu.Lock()
	s.BodePoints = len(e.bode.bins)
	s.BodeValid = e.bode.liveValid
	if e.bode.liveValid {
		s.BodeFreqHz = e.bode.live.FreqHz
		s.BodeGainDB = e.bode.live.GainDB
		s.BodePhaseDeg = e.bode.live.PhaseDeg
	}
	e.bode.mu.Unlock()
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

// TrigLevelVolts converts a DAC code to approximate volts using the linear
// fit exact at 1 V/2 V-div (spec 05 §1.2): code = 31434 − 938·V.
func TrigLevelVolts(code uint16) float64 { return (31434 - float64(code)) / 938 }
