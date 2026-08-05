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
	"open-sds/app/internal/iface"
)

// Owned FPGA register selectors and field values (fpga/standard/docs/REGISTER-MAP.md;
// specs 02/03). The engine drives the fabric ONLY through these iface bindings —
// there is no vendor register map, no divisor/class/run-word opcodes, no
// round-robin drain, no re-trigger strobe, no force pulse.
const (
	// meta / identity (CS1)
	selVersion   = iface.SelVERSION
	selBuildIDLo = iface.SelBUILDID_LO
	selBuildIDHi = iface.SelBUILDID_HI

	// capture control (CS1)
	selOpcode  = iface.SelOPCODE // GO / HALT / RESET strobe
	selRun     = iface.SelRUN    // MODE + RUN
	selDecimLo = iface.SelDECIM_LO
	selDecimHi = iface.SelDECIM_HI
	selPreLo   = iface.SelPRETRIG_LO
	selPreHi   = iface.SelPRETRIG_HI
	selPostLo  = iface.SelPOSTTRIG_LO
	selPostHi  = iface.SelPOSTTRIG_HI

	// drain (CS1): single fixed auto-inc BURST port + remaining/DMA-ready.
	selBurst    = iface.SelBURST
	selBurstRem = iface.SelBURST_REMAIN

	// status (CS1)
	selStatus    = iface.SelSTATUS_A
	selTrigPosLo = iface.SelTRIGPOS_LO
	selTrigPosHi = iface.SelTRIGPOS_HI
	selFill      = iface.SelFILL

	// streaming spine + envelope result channel (CS1)
	selXformCtrl = iface.SelXFORM_CTRL
	selEnvCols   = iface.SelENV_COLS
	selEnvData   = iface.SelENV_DATA
	selEnvCount  = iface.SelENV_COUNT
	selEnvReset  = iface.SelENV_RESET

	// config / analog front end (CS3). CONF_DONE is READ ONLY.
	cs3ConfStatus = iface.SelCONF_DONE
	cs3LedLo      = iface.SelLED_LO
	cs3LedHi      = iface.SelLED_HI
	cs3LedStrobe  = iface.SelLED_STROBE
	cs3OffC1Lo    = iface.SelOFF_C1_LO
	cs3OffC1Hi    = iface.SelOFF_C1_HI
	cs3OffC2Lo    = iface.SelOFF_C2_LO
	cs3OffC2Hi    = iface.SelOFF_C2_HI
	cs3LevelALo   = iface.SelLVL_A_LO
	cs3LevelAHi   = iface.SelLVL_A_HI // self-latches + loads the serializer
	cs3LevelBLo   = iface.SelLVL_B_LO
	cs3LevelBHi   = iface.SelLVL_B_HI

	// OPCODE strobe values (spec 03 §5): sourced from the generated interface so
	// the app and the fabric decode the same literal (the encoding is folded into
	// the build-ID; a divergence fails the drift gate and the identity handshake).
	opGo    = iface.OP_GO    // arm / re-arm
	opHalt  = iface.OP_HALT  // freeze the record
	opReset = iface.OP_RESET // idle

	// RUN.MODE values (spec 03 §5.1).
	modeAuto uint8 = 0
	modeNorm uint8 = 1

	// STATUS_A level bits (iface field masks).
	statValid = iface.Mask_STATUS_A_VALID // AUTO free-run: completed without a real trigger
	statTrig  = iface.Mask_STATUS_A_TRIG
	statDone  = iface.Mask_STATUS_A_DONE

	nativeEdgeMinPtp  = 40 // codes; flat rail ≈ 5, real cal edge ≈ 150
	nativeFlatFallbck = 60 // held frames before one honest flat publish
	// centerCross confirmed-crossing hysteresis (discern.go): a crossing must
	// transit ±hystK·noiseFloor to anchor. hystMinCodes floors it for clean
	// synthetic signals; hystMaxReach bounds the outward search (a real edge
	// reaches the far state within a fraction of a period; a flat region never
	// does and stops here). Chosen so a signal that clears signalPresent
	// (ptp ≥ 8·noiseFloor) can always transit ±4·noiseFloor around a mid level.
	hystK        = 4.0
	hystMinCodes = 2
	hystMaxReach = 2048
	// stuckSuspectRuns: consecutive degraded (dead-tail-after-retries) captures
	// before the persistent stuck-FSM state is assumed. The intermittent half-
	// record never survives the re-capture retries many frames in a row; the
	// stuck state survives ALL of them, indefinitely (bench: 100% for hours,
	// cured only by a power-cycle). ~2 s at the native-fast frame rate.
	stuckSuspectRuns = 40
	// autoLivenessMaxWait bounds the AUTO liveness fallback by WALL CLOCK as well
	// as by held-frame count: at slow decimated bands one hold cycle costs the
	// full 40-80 ms wait budget, so 60 held frames is 5-8 s of frozen screen —
	// far past what an AUTO display may freeze (fuzz-found at 500 µs/div).
	autoLivenessMaxWait = 1500 * time.Millisecond
	// fillFull is the AUTO free-run completion FALLBACK: only consulted when the
	// fabric has NOT reported TRIG (a flat/wedged fabric), where early completion
	// is harmless. FILL.COUNT is 11-bit (wrote_count[10:0], wraps every 2048), so
	// this is a fill-progress fraction within that range, NOT a true record-full
	// mark for a deep (20480) record — the engine deliberately caps fill-gated
	// windows to envFillCap < 2048 (bands.go). Widening FILL.COUNT to a full
	// sample count is a future enhancement (would allow deep fill-gating).
	fillFull = 0x7f0
	// native-fast re-capture cap is the tunable tuneMaxRetry (default 8); see engine.New

	// TrigCodeMin/Max clamp the UI trigger-level DAC range (spec 05 §1.2).
	TrigCodeMin = 27000
	TrigCodeMax = 35000

	// Measured global trigger fit (docs/trigcal-notes.md), used until the front
	// end pushes its cal: code = trigZeroDefault − trigCPVDefault·V. CPV is
	// constant across the whole V/div ladder (proven by FPGA-DAC bench cal).
	trigZeroDefault = 31437.0
	trigCPVDefault  = 911.0
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
	Frames       uint64  `json:"frames"`                  // FSM heartbeat: +1 per loop iteration, publish or not
	Coherent     uint64  `json:"coherent"`                // frames that latched+drained coherently
	Published    uint64  `json:"published"`               // frames handed to the arena
	Held         uint64  `json:"held"`                    // display-hold cycles
	Degraded     bool    `json:"degraded,omitempty"`      // last native-fast capture kept a dead tail through the retries
	DegradedRun  int     `json:"degraded_run,omitempty"`  // consecutive degraded captures
	StuckSuspect bool    `json:"stuck_suspect,omitempty"` // the run crossed the stuck-FSM threshold: power-cycle likely needed
	HaltConfirm  uint64  `json:"halt_confirm"`            // halts with fill frozen across the double read
	BusErrors    uint64  `json:"bus_errors"`
	DeadRuns     int     `json:"dead_runs"` // consecutive fill-frozen + flat-drain frames
	Wedged       bool    `json:"wedged"`
	FPS          float64 `json:"fps"`
	Running      bool    `json:"running"`
	Norm         bool    `json:"norm"`
	Single       bool    `json:"single"`        // a single-shot is armed/waiting
	TrigPosFrac  float64 `json:"trig_pos_frac"` // horizontal trigger position 0..1
	TdivS        float64 `json:"tdiv_s"`
	DisplayedS   float64 `json:"displayed_sdiv_s"`
	TrigCode     uint16  `json:"trig_code"` // 0 = boot-inherited comparator untouched
	OffC1        uint16  `json:"off_c1"`    // 0 = boot-inherited offset untouched
	OffC2        uint16  `json:"off_c2"`
	TrigRising   bool    `json:"trig_rising"`
	TrigSource   int     `json:"trig_source"` // 0=C1, 1=C2
	LastPtp      int     `json:"last_ptp"`
	LastTrigPos  int     `json:"last_trigpos"`
	ArmToLatch   float64 `json:"arm_to_latch_ms"`
	DrainMs      float64 `json:"drain_ms"`
	HoldoffS     float64 `json:"holdoff_s"` // trigger holdoff (0 = off)
	Seq          uint64  `json:"seq"`
	MmapDrain    bool    `json:"mmap_drain"`
	ETS          bool    `json:"ets"`
	BandKind     string  `json:"band"`      // native-fast | decimated | envelope | roll
	HaltMode     string  `json:"halt_mode"` // capture-halt | halt-per-update
	TrigType     int     `json:"trig_type"` // 0=edge 1=pulse 2=slope 3=video
	AcqMode      int     `json:"acq_mode"`  // 0=normal 1=average 2=eres 3=peak
	AvgCount     int     `json:"avg_count"`
	EresLen      int     `json:"eres_len"`
	WinColStd    float64 `json:"wincol_std"`     // centred cross-frame uniformity
	WinColRaw    float64 `json:"wincol_std_raw"` // fixed-position variant
	WinColMax    float64 `json:"wincol_max"`     // worst centred column
	ValidDepth   int     `json:"valid_depth"`    // real-signal samples in the drain
	WinCols      int     `json:"win_cols"`       // display-window width in raw samples
	MemDepth     int     `json:"mem_depth"`      // configured decimated drain depth
	Stream       bool    `json:"stream"`         // stitched streaming decode mode on
	GapMs        float64 `json:"gap_ms"`         // stream: blackout between windows

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

	// In-fabric decode+trigger status mirror (fabrictrig.go). Populated only
	// while the fabric path is armed; the FPGA does the decode+trigger.
	SerialFabric      bool  `json:"serial_fabric,omitempty"`       // fabric path requested
	SerialFabArmed    bool  `json:"serial_fab_armed,omitempty"`    // decode+trigger registers are armed
	SerialFabProtoOK  bool  `json:"serial_fab_proto_ok,omitempty"` // proto is fabric-decodable (else software fallback)
	SerialFabMode     int   `json:"serial_fab_mode,omitempty"`     // resolved trig_mode 0 byte/1 err/2 seq/3 addr
	SerialFabMatched  bool  `json:"serial_fab_matched,omitempty"`  // sticky: pattern fired since arm
	SerialFabByte     int   `json:"serial_fab_byte,omitempty"`     // anchoring byte at the match
	SerialFabFill     int   `json:"serial_fab_fill,omitempty"`     // decoded bytes queued at last poll
	SerialFabOverflow bool  `json:"serial_fab_overflow,omitempty"` // FIFO dropped bytes since arm
	SerialFabBytes    []int `json:"serial_fab_bytes,omitempty"`    // most-recent drained decoded bytes

	// FRA / Bode plot (bode.go)
	BodeMode     int     `json:"bode_mode,omitempty"`      // 0 off, 1 armed
	BodePoints   int     `json:"bode_points,omitempty"`    // accumulated curve size
	BodeValid    bool    `json:"bode_valid,omitempty"`     // the live point is valid
	BodeFreqHz   float64 `json:"bode_freq_hz,omitempty"`   // live point frequency
	BodeGainDB   float64 `json:"bode_gain_db,omitempty"`   // live point magnitude (dB)
	BodePhaseDeg float64 `json:"bode_phase_deg,omitempty"` // live point phase (deg)
}

// AcqSample is one per-frame acquisition record captured by the realtime
// acquisition checker (pure instrumentation — it never influences the FSM). It
// snapshots the hardware/wait state of a single halt+drain frame so a HALF
// record (valid_depth ≈ half of cols on a native-fast band) can be correlated
// with the arm/wait/halt outcome that produced it. Recorded for every frame
// that reaches halt+drain, published or held.
type AcqSample struct {
	Seq          uint64  `json:"seq"`          // FSM heartbeat (stats.Frames) at record time
	Band         string  `json:"band"`         // native-fast | decimated | envelope | roll
	ValidDepth   int     `json:"valid_depth"`  // live samples before the dead tail
	Cols         int     `json:"cols"`         // drained record width (effDrainCols)
	FillAtHalt   int     `json:"fill_at_halt"` // 0x46 & fillMask, final read inside halt()
	HaltOK       bool    `json:"halt_ok"`      // halt() confirmed the fill froze
	SawTrig      bool    `json:"saw_trig"`     // status bit1 asserted during the wait
	Anchored     bool    `json:"anchored"`     // wait anchored on done/valid
	Filled       bool    `json:"filled"`       // post-trigger record filled (0x46 ≥ LatchAt)
	ArmToLatchMs float64 `json:"arm_to_latch_ms"`
	TrigPos      int     `json:"trig_pos"`
	Half         bool    `json:"half"`       // published ValidDepth < 0.6*Cols (after re-capture)
	FirstHalf    bool    `json:"first_half"` // the FIRST drain was half (raw HW rate, before re-capture)
	Published    bool    `json:"published"`  // this capture became user-visible (vs held/discarded)
	Norm         bool    `json:"norm"`       // NORM (vs AUTO) at capture time
	TdivS        float64 `json:"tdiv_s"`     // band timebase at capture time
}

// CmdNote records one web set-control invocation (name + numeric value +
// the published Seq at the time), so the acq log can be read against "what we
// did just before". Pure instrumentation.
type CmdNote struct {
	Name string  `json:"name"`
	Val  float64 `json:"val"`
	Seq  uint64  `json:"at_seq"`
}

type Engine struct {
	b     bus.Bus
	clk   Clock
	logf  func(string, ...any)
	arena *arena

	framePeriodNs atomic.Int64 // publish pacing floor (ns); 0 = back-to-back (stream)
	holdoffNs     atomic.Int64 // minimum time after a triggered frame before re-arm; 0 = off
	armSettle     time.Duration
	armBusy       bool // busy-wait the settle (real clock) instead of Sleep — the native-fast root-cause fix
	pollEvery     time.Duration

	// Live tuning knobs (debug /api/debug/tune) — atomics so the web handler
	// sets them lock-free and the owner loop reads them each frame. The vendor
	// half-record/force/maturation/re-capture knobs are DELETED (the owned
	// fabric's trustworthy trigger + static-freeze make them unnecessary, spec 03);
	// what remains is FPGA-agnostic loop/CPU tuning. Gated on armBusy (device) so
	// tests are unaffected.
	tuneArmSettleUs atomic.Int64 // arm settle duration (µs)
	tuneArmSpin     atomic.Bool  // busy-wait the settle vs Sleep
	tuneGcCtl       atomic.Bool  // controlled GC (SetGCPercent(-1)+manual) vs stock
	tuneRenderMs    atomic.Int64 // LCD render period (ms)
	tuneSigK        atomic.Int64 // decimated small-signal gate: min ptp / noiseFloor ratio to lock
	reinitReq       atomic.Int64 // staged FSM re-init level (debug/recovery); serviced at the loop boundary

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
	trigZero    [2]atomic.Uint64 // per-channel active trig-cal Zero (float64 bits)
	trigCPV     [2]atomic.Uint64 // per-channel active trig-cal CPV (float64 bits)
	trigOffV    [2]atomic.Uint64 // per-channel applied offset volts (float64 bits)
	hintReset   atomic.Bool      // clear the phase-continuity hint (config changed)
	trigPosFrac atomic.Uint64    // horizontal trigger position, fraction of screen

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
	// in-fabric decode+trigger (fabrictrig.go): when set, the FPGA decodes and
	// triggers; the engine arms it + mirrors its status instead of software-decoding.
	serialFabric atomic.Bool
	fabTrig      *FabricTrig
	bodeMode     atomic.Int32 // FRA (Bode) accumulation armed (bode.go)
	bode         bodeState

	// mu guards the command shadows and the stats mirror. Setters record and
	// return; only the owner touches the bus (spec 09 §1). Bus writes happen
	// with mu released.
	mu sync.Mutex
	// quiet gates the ~19ms load-sensitive windows (arm-settle + drain). The
	// engine takes the WRITE lock across those; the LCD render / web serialize /
	// panel take the READ lock around their CPU bursts. On this single core, a
	// concurrent CPU burst *during* the arm-settle or drain corrupts the HW
	// capture (proven: it freezes only the pre-trigger half). Pausing the other
	// goroutines for those ~19ms — a minority of the ~110ms loop — is enough; they
	// run freely during the ~90ms wait+pace. Cheaper than a busy-wait (no CPU
	// burned) and it kills the root cause instead of re-capturing after the fact.
	quiet     sync.RWMutex
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
	band        Band
	prevKind    Kind
	lastNorm    bool
	lastCapCols int // effDrainCols() the last bringUp programmed into PRE/POSTTRIG
	seq         uint64
	flatHeld    int
	lastPubAt   time.Time // engine goroutine only: instant of the last oneFrame publish
	lastEdgeX   float64   // engine goroutine only: previous frame's edge (phase-continuity hint); <0 = none
	deadRuns    int
	streamSeq   uint64    // stitch-mode window counter
	lastHalt    time.Time // wall-clock of the previous window's halt (for GapNs)
	done        chan struct{}

	// Realtime acquisition checker (instrumentation only, spec: diagnose HALF
	// records). acqRing/cmdRing are guarded by e.mu (the status handler reads
	// them off the HTTP goroutine). lastFillAtHalt is written only from the
	// engine goroutine inside halt() and copied into the ring under e.mu.
	acqRing        [128]AcqSample
	acqHead        int
	lastFillAtHalt int
	cmdRing        [64]CmdNote
	cmdHead        int

	// Envelope band state (spec 04 §1): fabric envelope-channel record buffer
	// (primary) + a ring of phase-scattered windows for the software fallback.
	envChanBuf             []uint16
	envRing1, envRing2     [][]uint8
	envRingPos, envRingCnt int

	// Roll band state (spec 04 §2): scrolled raw ring + scroll snapshots +
	// per-update burst-drain scratch.
	rollArmed                  bool
	rollRing1, rollRing2       []uint8
	rollScratch1, rollScratch2 []uint8
	rollPos                    int
	rollSnaps1, rollSnaps2     [][]uint8

	// In-fabric decode+trigger, owner-goroutine only (fabrictrig.go). Tracks the
	// last-armed register image so re-arm happens only on a config/band change
	// (a bare re-arm would wipe the sticky match + FIFO), plus the match-edge
	// detector and the drain scratch.
	fabArmed       bool
	fabRegs        fabricRegs
	fabSPBLo       uint16
	fabSPBHi       uint16
	fabPrevMatched bool
	fabBytes       []DecodedByte

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
	// A nil clock means the real monotonic clock (production). Only then may the
	// arm-settle busy-wait, which spins on time.Now(); a fake clock's Now advances
	// only via Sleep, so tests must keep the sleep path (see armEngine).
	realTime := cfg.Clock.Now == nil
	if realTime {
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
		armBusy:   realTime,
		pollEvery: cfg.PollEvery,
		band:      start,
		prevKind:  start.Kind(),
		done:      make(chan struct{}),
		matrixReq: make(chan chan [5]uint16, 4),
	}
	e.fabTrig = NewFabricTrig(cfg.Bus) // in-fabric decode+trigger driver (owner-goroutine only)
	// Tuning defaults. The owned FSM is a clean program → arm → wait-on-real-DONE →
	// halt → burst-drain → re-arm cycle: with a trustworthy HW trigger and a
	// static-freeze M9K there is no half-record to work around, so the vendor
	// maturation/re-capture/fill-extra/halt-settle/force knobs are gone. What
	// remains is FPGA-agnostic loop tuning.
	e.tuneArmSettleUs.Store(cfg.ArmSettle.Microseconds()) // spec-safe 2 ms
	e.tuneArmSpin.Store(false)
	e.tuneGcCtl.Store(false)
	e.tuneRenderMs.Store(120) // ~8 Hz LCD: big CPU win, still smooth enough
	// Decimated small-signal lock gate: a real signal has ptp ≥ SigK × noiseFloor
	// (period-independent 2nd-difference noise estimate), which separates a real
	// sub-1.6-div signal from a noisy flat rail at EVERY timebase — raw ptp alone
	// cannot (a noisy rail's ptp can exceed a small real signal's). Bench-tuned:
	// real signals ratio ≥16, flat rails ≤7 (σ up to 3.5). See docs/trigcal-notes.md.
	e.tuneSigK.Store(8)
	e.running.Store(true)
	e.trigRising.Store(true)
	e.avgCount.Store(16) // boot-firmware default; menu {4,16,32,64,128,256}
	e.eresLen.Store(1)
	e.chVdivBits[0].Store(math.Float64bits(1))
	e.chVdivBits[1].Store(math.Float64bits(1))
	e.trigZero[0].Store(math.Float64bits(trigZeroDefault))
	e.trigZero[1].Store(math.Float64bits(trigZeroDefault))
	e.trigCPV[0].Store(math.Float64bits(trigCPVDefault))
	e.trigCPV[1].Store(math.Float64bits(trigCPVDefault))
	e.lastEdgeX = -1
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

// QuietRLock/QuietRUnlock bracket a concurrent CPU consumer's work (LCD render,
// web serialize, panel). It blocks while the engine is in a load-sensitive
// window (arm-settle or drain), so the consumer runs only during the loop's
// ~90ms of dead time — never during the ~19ms that corrupts the HW capture.
func (e *Engine) QuietRLock()   { e.quiet.RLock() }
func (e *Engine) QuietRUnlock() { e.quiet.RUnlock() }

// TuneVals is the live-tunable knob set (see the tune* atomics). The vendor
// half-record/force/maturation/re-capture knobs are gone with the vendor path;
// what remains is FPGA-agnostic loop/CPU tuning.
type TuneVals struct {
	ArmSettleUs int64 `json:"arm_settle_us"`
	ArmSpin     bool  `json:"arm_spin"`
	GcCtl       bool  `json:"gc_ctl"`
	RenderMs    int64 `json:"render_ms"`
	SigK        int64 `json:"sig_k"`            // decimated small-signal gate: min ptp/noiseFloor ratio to lock (default 8)
	Reinit      int64 `json:"reinit,omitempty"` // one-shot: stage an FSM re-init at this level (1=bringUp, 2=+clean reset)
}

// Tune applies a knob set (debug /api/debug/tune) and returns the effective
// values. Ignored on a non-device (fake clock) engine so tests stay stock.
func (e *Engine) Tune(t TuneVals) TuneVals {
	if !e.armBusy {
		return e.TuneSnapshot()
	}
	if t.ArmSettleUs > 0 {
		e.tuneArmSettleUs.Store(t.ArmSettleUs)
	}
	if t.RenderMs > 0 {
		e.tuneRenderMs.Store(t.RenderMs)
	}
	if t.SigK > 0 {
		e.tuneSigK.Store(t.SigK)
	}
	if t.Reinit > 0 {
		e.reinitReq.Store(t.Reinit) // one-shot; the owner loop consumes it
	}
	e.tuneArmSpin.Store(t.ArmSpin)
	e.tuneGcCtl.Store(t.GcCtl)
	return e.TuneSnapshot()
}

// TuneSnapshot reports the current knob values.
func (e *Engine) TuneSnapshot() TuneVals {
	return TuneVals{
		ArmSettleUs: e.tuneArmSettleUs.Load(),
		ArmSpin:     e.tuneArmSpin.Load(),
		GcCtl:       e.tuneGcCtl.Load(),
		RenderMs:    e.tuneRenderMs.Load(),
		SigK:        e.tuneSigK.Load(),
	}
}

// RenderPeriod is the LCD loop's tunable tick (read each render tick).
func (e *Engine) RenderPeriod() time.Duration {
	ms := e.tuneRenderMs.Load()
	if ms < 10 {
		ms = 10
	}
	return time.Duration(ms) * time.Millisecond
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
	s.SerialFabric = e.serialFabric.Load() // reflect the flag even before the loop services it
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

// AcqLog returns the last n acquisition samples (most-recent-last) plus the
// HALF rate over the last up-to-64 recorded samples. Instrumentation only; the
// ring is read under e.mu since it is written by the engine goroutine. n is
// clamped to the ring size.
func (e *Engine) AcqLog(n int) ([]AcqSample, float64) {
	if n < 0 {
		n = 0
	}
	if n > len(e.acqRing) {
		n = len(e.acqRing)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Count how many valid (recorded) entries exist: acqHead has wrapped past
	// filled entries; a never-written slot has Seq==0 && Cols==0.
	avail := 0
	for i := 0; i < len(e.acqRing); i++ {
		idx := (e.acqHead - 1 - i + len(e.acqRing)) % len(e.acqRing)
		if e.acqRing[idx].Cols == 0 {
			break
		}
		avail++
	}
	// HALF rate over the last up-to-64 recorded samples.
	rateN := avail
	if rateN > 64 {
		rateN = 64
	}
	half := 0
	for i := 0; i < rateN; i++ {
		idx := (e.acqHead - 1 - i + len(e.acqRing)) % len(e.acqRing)
		if e.acqRing[idx].Half {
			half++
		}
	}
	rate := 0.0
	if rateN > 0 {
		rate = float64(half) / float64(rateN)
	}
	if n > avail {
		n = avail
	}
	out := make([]AcqSample, n)
	for i := 0; i < n; i++ { // most-recent-last: fill from the tail back
		idx := (e.acqHead - n + i + len(e.acqRing)) % len(e.acqRing)
		out[i] = e.acqRing[idx]
	}
	return out, rate
}

// CmdLog returns the last n command notes (most-recent-last). Instrumentation
// only; read under e.mu since NoteCmd writes from the HTTP goroutine.
func (e *Engine) CmdLog(n int) []CmdNote {
	if n < 0 {
		n = 0
	}
	if n > len(e.cmdRing) {
		n = len(e.cmdRing)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	avail := 0
	for i := 0; i < len(e.cmdRing); i++ {
		idx := (e.cmdHead - 1 - i + len(e.cmdRing)) % len(e.cmdRing)
		if e.cmdRing[idx].Name == "" {
			break
		}
		avail++
	}
	if n > avail {
		n = avail
	}
	out := make([]CmdNote, n)
	for i := 0; i < n; i++ {
		idx := (e.cmdHead - n + i + len(e.cmdRing)) % len(e.cmdRing)
		out[i] = e.cmdRing[idx]
	}
	return out
}

// NoteCmd records a web set-control invocation into the command ring, stamping
// the current published Seq. Called from the HTTP goroutine → locks e.mu.
// Instrumentation only; it never touches the bus or the FSM.
func (e *Engine) NoteCmd(name string, val float64) {
	e.mu.Lock()
	e.cmdRing[e.cmdHead] = CmdNote{Name: name, Val: val, Seq: e.stats.Seq}
	e.cmdHead = (e.cmdHead + 1) % len(e.cmdRing)
	e.mu.Unlock()
}

// ---- owner goroutine ----

// TrigLevelVolts converts a DAC code to approximate volts using the measured
// global fit (docs/trigcal-notes.md): code = 31437 − 911·V.
func TrigLevelVolts(code uint16) float64 { return (31437 - float64(code)) / 911 }
