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

	// Per-frame completion tail (reference-device trace: written after every
	// drain, before the next arm). 0x16=1 is the load-bearing re-trigger
	// strobe — omitting it starves the trigger engine into permanent
	// half-records (saw_trig never asserts again).
	selTailA  = 0x3c // written 0x0002 after each drain
	selTailB  = 0x3d // written 0x0008 after each drain
	selTailC  = 0x3e // written 0x0000 after each drain
	selTailD  = 0x58 // written 0x0000 after each drain
	selRetrig = 0x16 // 0x0001 = frame-completion / re-trigger strobe
	selForce  = 0x2c // 0→1 pulse = AUTO force-trigger (untriggered frames)

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

	nativeEdgeMinPtp  = 40 // codes; flat rail ≈ 5, real cal edge ≈ 150
	nativeFlatFallbck = 60 // held frames before one honest flat publish
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
	fillFull            = 0x7f0 // fill counter near the 11-bit max = record full
	// native-fast re-capture cap is the tunable tuneMaxRetry (default 8); see engine.New

	// TrigCodeMin/Max clamp the UI trigger-level DAC range (spec 05 §1.2).
	TrigCodeMin = 27000
	TrigCodeMax = 35000

	// Pre-calibration global trigger fit (spec 05 §1.2), used until the front
	// end pushes a per-detent cal: code = trigZeroDefault − trigCPVDefault·V.
	trigZeroDefault = 31434.0
	trigCPVDefault  = 938.0
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
	HaltMode     string  `json:"halt_mode"` // capture-halt | latch-no-halt
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

	// Live tuning knobs (debug /api/debug/tune) for the framerate/CPU/success
	// optimization campaign — atomics so the web handler sets them lock-free and
	// the owner loop reads them each frame. Defaults mirror the compile-time
	// constants; gated on armBusy (device) so tests are unaffected.
	tuneArmSettleUs  atomic.Int64 // arm settle duration (µs)
	tuneArmSpin      atomic.Bool  // busy-wait the settle vs Sleep
	tuneBusyFillUs   atomic.Int64 // native-fast fill busy-poll window (µs; 0 = off)
	tuneGcCtl        atomic.Bool  // controlled GC (SetGCPercent(-1)+manual) vs stock
	tuneMaxRetry     atomic.Int64 // native-fast re-capture cap
	tuneRenderMs     atomic.Int64 // LCD render period (ms)
	tuneFillExtraUs  atomic.Int64 // native-fast: extra fill time after done, before halt (µs)
	tuneHaltSettleUs atomic.Int64 // native-fast: post-halt settle before deep-port reads (µs)
	tuneFrameTail    atomic.Bool  // per-frame completion tail + 0x16 re-trigger strobe (reference-device op)
	tuneForceMode    atomic.Int64 // AUTO force-trigger op: 0 off, bit0 = 0x2c pulse, bit1 = 0x16 strobe
	tuneForceAfterUs atomic.Int64 // µs after arm without a comparator edge before forcing
	tuneMatureUs     atomic.Int64 // native-fast maturation floor before halt (µs)
	tuneTail3c       atomic.Int64 // acq-control pair value for 0x3c (band-dependent; native-fast 0x00fd)
	tuneTail3d       atomic.Int64 // acq-control pair value for 0x3d (band-dependent; native-fast 0x0007)
	reinitReq        atomic.Int64 // staged FSM re-init level (debug/recovery); serviced at the loop boundary

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
	bodeMode      atomic.Int32 // FRA (Bode) accumulation armed (bode.go)
	bode          bodeState

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
	band          Band
	prevKind      Kind
	lastNorm      bool
	seq           uint64
	flatHeld      int
	lastPubAt     time.Time // engine goroutine only: instant of the last oneFrame publish
	degradedRun   int       // engine goroutine only: consecutive dead-tail captures
	lastFirstHalf bool      // the last frame's FIRST drain was a half record (pre re-capture)
	deadRuns      int
	streamSeq     uint64    // stitch-mode window counter
	lastHalt      time.Time // wall-clock of the previous window's halt (for GapNs)
	done          chan struct{}

	// Realtime acquisition checker (instrumentation only, spec: diagnose HALF
	// records). acqRing/cmdRing are guarded by e.mu (the status handler reads
	// them off the HTTP goroutine). lastFillAtHalt is written only from the
	// engine goroutine inside halt() and copied into the ring under e.mu.
	acqRing        [128]AcqSample
	acqHead        int
	lastFillAtHalt int
	cmdRing        [64]CmdNote
	cmdHead        int

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
	// Tuning defaults. ROOT-CAUSE FIX for the native-fast half-record: the free-run
	// wait returned the instant done+fill asserted and halted immediately, catching
	// the deep record half-filled (one memory bank). Holding ~2 ms more before halt
	// lets the second bank fill — verified by a content checker (real_depth, which
	// detects the drain's period-5 dead tail that fooled valid_depth): base full
	// rate 7.5%→100%, so re-capture is no longer needed. Combined with the CPU/fps
	// tuning (no busy spins, slowed change-detected render, stock GC) this beats the
	// original defaults on all three: CPU ~96%→~86%, fps ~13.4→~18, frame_success
	// (real content, not valid_depth) ~5%→100%.
	e.tuneArmSettleUs.Store(cfg.ArmSettle.Microseconds()) // spec-safe 2 ms
	// The settle busy-spin and manual GC control were workarounds tuned against
	// the UNTRIGGERED half-record state. With captures gated on trigger
	// evidence, HW A/B under web-stream load shows a plain Sleep settle and
	// stock GC are strictly better (spin: 16 fps / 20% idle → sleep: 18-19 fps
	// / 27% idle, both 20/20 full records, half_rate 0).
	e.tuneArmSpin.Store(false)
	e.tuneBusyFillUs.Store(0) // no fill busy-poll (pure CPU cost)
	e.tuneGcCtl.Store(false)
	e.tuneMaxRetry.Store(2)   // light backstop; full records are now the norm
	e.tuneRenderMs.Store(120) // ~8 Hz LCD: big CPU win, still smooth enough
	// fill-extra and halt-settle were workarounds tuned against what turned out
	// to be the UNTRIGGERED half-record state (level outside the signal band);
	// with triggered captures they buy nothing (HW A/B: 15/15 full without
	// them) and cost 4 ms/frame. Knobs kept at 0 for experiments.
	e.tuneFillExtraUs.Store(0)
	e.tuneHaltSettleUs.Store(0)
	// Reference-device experiment knobs, OFF by default: A/B on hardware showed
	// neither the per-frame tail (0x3e/0x58/0x3c/0x3d/0x16) nor any placement
	// of the 0x2c force pulse completes an untriggered record or affects a
	// triggered one. Kept as live knobs for further vendor-op experiments.
	e.tuneFrameTail.Store(false)
	e.tuneForceMode.Store(0)
	e.tuneForceAfterUs.Store(10000)
	// HW sweep 40→2 ms: full records at every step once captures are gated on
	// trigger evidence (the historical 40 ms bound was measured in the
	// untriggered parked state). 3 ms keeps margin over the proven 2 ms.
	e.tuneMatureUs.Store(3000)
	e.tuneTail3c.Store(0x00fd) // reference-device native-fast acq-control pair
	e.tuneTail3d.Store(0x0007)
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

// TuneVals is the live-tunable knob set (see the tune* atomics).
type TuneVals struct {
	ArmSettleUs  int64 `json:"arm_settle_us"`
	ArmSpin      bool  `json:"arm_spin"`
	BusyFillUs   int64 `json:"busy_fill_us"`
	GcCtl        bool  `json:"gc_ctl"`
	MaxRetry     int64 `json:"max_retry"`
	RenderMs     int64 `json:"render_ms"`
	FillExtraUs  int64 `json:"fill_extra_us"`
	HaltSettleUs int64 `json:"halt_settle_us"`
	FrameTail    bool  `json:"frame_tail"`       // per-frame completion tail + 0x16 re-trigger strobe
	ForceMode    int64 `json:"force_mode"`       // AUTO force-trigger: 0 off, bit0 0x2c pulse, bit1 0x16 strobe
	ForceAfterUs int64 `json:"force_after_us"`   // µs after arm without a trigger before forcing
	MatureUs     int64 `json:"mature_us"`        // native-fast maturation floor before halt (µs)
	Tail3c       int64 `json:"tail_3c"`          // acq-control 0x3c value (band-dependent)
	Tail3d       int64 `json:"tail_3d"`          // acq-control 0x3d value (band-dependent)
	Reinit       int64 `json:"reinit,omitempty"` // one-shot: stage an FSM re-init at this level (1=bringUp, 2=+runword/reset pulses)
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
	if t.BusyFillUs >= 0 {
		e.tuneBusyFillUs.Store(t.BusyFillUs)
	}
	if t.MaxRetry >= 0 {
		e.tuneMaxRetry.Store(t.MaxRetry)
	}
	if t.RenderMs > 0 {
		e.tuneRenderMs.Store(t.RenderMs)
	}
	if t.FillExtraUs >= 0 {
		e.tuneFillExtraUs.Store(t.FillExtraUs)
	}
	if t.HaltSettleUs >= 0 {
		e.tuneHaltSettleUs.Store(t.HaltSettleUs)
	}
	if t.ForceMode >= 0 {
		e.tuneForceMode.Store(t.ForceMode)
	}
	if t.ForceAfterUs > 0 {
		e.tuneForceAfterUs.Store(t.ForceAfterUs)
	}
	if t.MatureUs > 0 {
		e.tuneMatureUs.Store(t.MatureUs)
	}
	if t.Tail3c >= 0 {
		e.tuneTail3c.Store(t.Tail3c)
	}
	if t.Tail3d >= 0 {
		e.tuneTail3d.Store(t.Tail3d)
	}
	if t.Reinit > 0 {
		e.reinitReq.Store(t.Reinit) // one-shot; the owner loop consumes it
	}
	e.tuneArmSpin.Store(t.ArmSpin)
	e.tuneGcCtl.Store(t.GcCtl)
	e.tuneFrameTail.Store(t.FrameTail)
	return e.TuneSnapshot()
}

// TuneSnapshot reports the current knob values.
func (e *Engine) TuneSnapshot() TuneVals {
	return TuneVals{
		ArmSettleUs:  e.tuneArmSettleUs.Load(),
		ArmSpin:      e.tuneArmSpin.Load(),
		BusyFillUs:   e.tuneBusyFillUs.Load(),
		GcCtl:        e.tuneGcCtl.Load(),
		MaxRetry:     e.tuneMaxRetry.Load(),
		RenderMs:     e.tuneRenderMs.Load(),
		FillExtraUs:  e.tuneFillExtraUs.Load(),
		HaltSettleUs: e.tuneHaltSettleUs.Load(),
		FrameTail:    e.tuneFrameTail.Load(),
		ForceMode:    e.tuneForceMode.Load(),
		ForceAfterUs: e.tuneForceAfterUs.Load(),
		MatureUs:     e.tuneMatureUs.Load(),
		Tail3c:       e.tuneTail3c.Load(),
		Tail3d:       e.tuneTail3d.Load(),
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

// TrigLevelVolts converts a DAC code to approximate volts using the linear
// fit exact at 1 V/2 V-div (spec 05 §1.2): code = 31434 − 938·V.
func TrigLevelVolts(code uint16) float64 { return (31434 - float64(code)) / 938 }
