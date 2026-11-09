// Package engine is the single-owner acquisition engine (spec 03): one
// goroutine owns the inherited GPMC fd and is the only code that touches the
// bus. HTTP handlers stage commands into coalescing shadows and consume frame
// copies; the owner applies commands at the frame boundary and runs the
// per-frame arm → wait → capture-halt → drain → re-arm FSM.
// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/instrument"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/iface"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// Register bindings: every fabric selector, opcode and field of the
// register-drained path comes from the iface package (schema
// sds1000cml-default v3); the SRAM images' come from sramcapture. The
// only hand-written addresses are MAX V (CS3) front-end registers, which are
// not part of the fabric schema and keep working under any Cyclone image.
const (
	// CS3 trigger-level comparator DAC lanes (app spec 05 §1). High bytes
	// self-latch. They set the threshold of the A12 comparator the fabric can
	// select with TRIG_LEVEL.HW_SEL.
	cs3LevelALo = 0x14
	cs3LevelAHi = 0x34
	cs3LevelBLo = 0x15
	cs3LevelBHi = 0x35

	cs3ConfStatus = bus.CS3ConfigPort // CS3 configuration port: bit7 = CONF_DONE. READ ONLY here.

	// CS3 vertical-offset DAC lanes per channel (app spec 06 §5.1): low byte
	// first, high byte self-latches. These selectors ALIAS fabric registers on
	// CS1 — the plane must be explicit on every write.
	cs3OffC1Lo = 0x10
	cs3OffC1Hi = 0x30
	cs3OffC2Lo = 0x11
	cs3OffC2Hi = 0x31

	statValid = iface.StatusAValidMask // a coherent record is present (AUTO completion)
	statTrig  = iface.StatusATrigMask  // a trigger was accepted this record
	statDone  = iface.StatusADoneMask  // post-trigger record complete

	// trigLevelUnset is the fabric trigger level (sample codes) while the app
	// has no trigger code yet: mid-scale. trigHyst is ACQ_CTRL.TRIG_HYST.
	trigLevelUnset = 128
	trigHyst       = 2

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
	// native-fast re-capture cap is the tunable tuneMaxRetry (default 8); see engine.New

	// TrigCodeMin/Max clamp the UI trigger-level code. Spec 05 §1.2's
	// 27000-35000 was the analog comparator DAC's window, about +4.9/-3.9 V:
	// the SRAM images trigger on sample codes (trigDispLevel), and that window
	// held the level inside ±2 div at 2 V/div and ±0.4 div at 10 V/div. The
	// full 16 bits reach about +34.5/-37.4 V; 0 still means no comparator.
	TrigCodeMin = 1
	TrigCodeMax = 65535

	// Measured global trigger fit (docs/trigcal-notes.md), used until the front
	// end pushes its cal: code = trigZeroDefault − trigCPVDefault·V. CPV is
	// constant across the whole V/div ladder (proven by FPGA-DAC bench cal).
	trigZeroDefault = 31437.0
	trigCPVDefault  = 911.0
)

// defaultFramePeriod is the publish pacing floor. It was 50 ms (20 fps), which
// was well below what the hardware sustains: measured on the instrument, the
// frame rate rises with the floor down to 10 ms and saturates
// there at 62 fps, because a frame costs about 16 ms of real work (a 4.2 ms
// drain, a 5.8 ms arm-to-latch, and ~6 ms of everything else). Nothing is
// gained below 10 ms and the floor still bounds the publish rate, so 10 ms it
// is: 3.2x the delivered samples per second, measured over a 90 s soak at
// 62.0 fps with 0 incoherent frames, 0 bus errors, 0 held, 0 short drains and
// the ARM at 0.00 load average. See the acq2 analysis branch
const defaultFramePeriod = 10 * time.Millisecond

// defaultArmSettle is the pause between re-programming and OPCODE = GO
// (acq.DefaultArmSettle, measured on the instrument).
const defaultArmSettle = acq.DefaultArmSettle

// Config wires an Engine.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-007, REQ-SDS-008
type Config struct {
	InterleaveCalibration *InterleaveCalibration
	// Instrument is the instrument state the engine's run control lives
	// in; nil: one of its own, running AUTO.
	Instrument *instrument.Model
	// Board is the acquisition board the composition built; nil: the
	// engine builds one over SRAM and Bus.
	Board       *acq.Board
	SRAM        *sramcapture.Capture // non-nil selects the external SRAM ABI
	Bus         bus.Bus
	Clock       Clock         // zero → real clock
	FramePeriod time.Duration // publish pacing floor; default defaultFramePeriod
	ArmSettle   time.Duration // default defaultArmSettle
	PollEvery   time.Duration // wait-gate poll pace; default 150 µs
	Logf        func(format string, a ...any)
}

// AcqSample is one capture as the realtime acquisition checker recorded
// it: the core engine's record.
type AcqSample = core.AcqSample

// CmdNote records one web set-control invocation (name + numeric value +
// the published Seq at the time), so the acq log can be read against "what we
// did just before". Pure instrumentation.
// TRLC-LINKS: REQ-SDS-127
type CmdNote struct {
	Name string  `json:"name"`
	Val  float64 `json:"val"`
	Seq  uint64  `json:"at_seq"`
}

// Engine is the acquisition engine: its owner goroutine (Run) is the only code
// that touches the bus; the exported methods stage commands for it and read
// the frames and status it publishes.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-007, REQ-SDS-008
type Engine struct {
	interleaveCalibration *InterleaveCalibration
	b                     bus.Bus
	clk                   Clock
	lastPublish           time.Time // when the newest frame was published (guarded by mu)
	logf                  func(string, ...any)
	arena                 *core.Arena
	sram                  acq.Memory // the board's record memory, for the paths the HAL does not cover yet
	board                 *acq.Board // the acquisition board: the owner loop drives it through the HAL facets below
	acqr                  hal.Acquirer
	trg                   hal.Trigger // the board's trigger
	draft                 *acq.Draft  // the capture drafted at a look at the settings (owner)
	qdraft                *acq.Draft  // the qualifier drafted for the status (owner)
	halTrig               halTrigger  // the trigger's HAL terms, reused (owner)
	plat                  hal.Platform
	readYield             func()          // the readout's yield: the board's Service
	rdx                   hal.Readout     // the board's readout
	readWins              []hal.Window    // a read's windows, kept between reads (owner)
	readFrames            []*sample.Frame // and their frames
	// protocolImage names the loaded protocol image ("" for general);
	// failedImage stops reloading one after a failed load until the serial
	// choice changes.
	protocolImage, failedImage string
	// streamReload: a stream faulted before its first word; the next stream
	// of any kind (roll, decoded, audio) loads the stream image first.
	streamReload bool
	// arincAux caches the ARINC slicer levels from a recent record for the
	// current serial parameters; zero when unknown.
	arincAux atomic.Uint32

	framePeriodNs atomic.Int64 // publish pacing floor (ns); 0 = back-to-back (stream)
	holdoffNs     atomic.Int64 // minimum time after a triggered frame before re-arm; 0 = off
	armSettle     time.Duration
	armBusy       bool // busy-wait the settle (real clock) instead of Sleep — the native-fast root-cause fix
	pollEvery     time.Duration

	// The live tuning knobs (debug /api/debug/tune) are the board's
	// (acq.Tuning): the web handler sets them lock-free and the owner loop
	// reads them each frame. Defaults mirror the compile-time constants;
	// gated on armBusy (device) so tests are unaffected.
	tune *acq.Tuning

	// Lock-free control reads by the owner.
	running       atomic.Bool
	trigRising    atomic.Bool
	trigSrc       atomic.Int32
	stopReq       atomic.Bool
	acqMode       atomic.Int32
	avgCount      atomic.Int32
	precisionLog  atomic.Int32
	eresLen       atomic.Int32
	avgGen        atomic.Uint32                    // bumped on acq-mode/depth changes → ring clear
	singleArmed   atomic.Bool                      // SINGLE: stop after the next triggered frame
	singleGen     atomic.Uint64                    // bumped per SINGLE press: a capture armed before the press never consumes it
	forceReq      atomic.Bool                      // FORCE: complete the armed capture once without a trigger
	decodeView    atomic.Bool                      // the display decodes the frame: send samples, not peak-detect pairs
	decodeLease   atomic.Int64                     // raw live frames until this time (unix ns; see LeaseDecodeView)
	rawMu         sync.Mutex                       // guards rawHolders and rawCancel
	rawHolders    map[string]int64                 // named raw leases: each holder's end (unix ns; LeaseRawFor)
	rawCancel     func()                           // abandons the live raw read in flight for the leases (nil when none)
	recallCancel  func()                           // abandons any record read in flight (SOUND starting; nil when none)
	sampleLease   atomic.Int64                     // evenly sampled live frames until this time (LeaseSampleView)
	spectrumLease atomic.Int64                     // live frames for a spectrum until this time (LeaseSpectrumView)
	displayPaceNs atomic.Int64                     // the least time a live SRAM capture takes, for the display (SetDisplayPace)
	searchLease   atomic.Int64                     // whole decimated records for a bus search until this time (LeaseSearchView)
	busLease      atomic.Int64                     // records sampled every busSampleS for a bus decode until this time (LeaseBusSamples)
	busSampleS    atomic.Uint64                    // the bus lease's coarsest sample interval (float64 bits)
	views         [core.ViewSurfaces]core.ViewSlot // view windows at full detail (LeaseViewWindow)
	memDepth      atomic.Int32                     // decimated drain depth (samples): fps↔data tradeoff
	streamMode    atomic.Bool                      // stitched high-bandwidth streaming decode mode
	chVdivBits    [2]atomic.Uint64                 // per-channel V/div (float64 bits) for the
	//                              trigger-level → display-code mapping
	trigZero    [2]atomic.Uint64 // per-channel active trig-cal Zero (float64 bits)
	trigCPV     [2]atomic.Uint64 // per-channel active trig-cal CPV (float64 bits)
	trigOffV    [2]atomic.Uint64 // per-channel applied offset volts (float64 bits)
	scaleGen    atomic.Uint64    // bumped on every V/div or offset change (stopped replay)
	feGen       atomic.Uint64    // bumped on every front-end write (relays, gain, offset DAC), changed or not
	learnFE     uint64           // feGen when the SRAM loop's previous capture was armed (level learning waits a steady capture)
	pubIdent    core.FrameIdent  // geometry of the latest published frame (guarded by mu)
	chCoupling  [2]atomic.Int32  // per-channel coupling (analog.CplDC/AC/GND); AC is software
	chMeanCode  [2]atomic.Uint64 // per-channel smoothed raw mean code of published frames (float64 bits; 0 = none)
	hintReset   atomic.Bool      // clear the phase-continuity hint (config changed)
	trigPosFrac atomic.Uint64    // horizontal trigger position, fraction of screen

	// zone trigger + mask testing (zonemask.go)
	zoneMode    atomic.Int32
	maskMode    atomic.Int32
	maskPass    atomic.Int64
	maskFail    atomic.Int64
	maskSkip    atomic.Int64
	maskStopped atomic.Bool
	// instModel holds the run control (running, the single shot and the
	// mode it replaced, the trigger mode); the atomics above mirror it for
	// the owner's hot path. inst() makes one for an engine built without New.
	instModel *instrument.Model
	instOnce  sync.Once
	staged    staging       // the controllers' settings waiting for the owner (Controls)
	maskCap   atomic.Uint64 // capture ordinal: counts every mask-tested frame
	beatN     atomic.Uint64 // liveness heartbeat (see Beats)
	zoneSkip  atomic.Int64  // zone-armed publishes that could not be tested (env/roll)
	zoneHeld  core.Liveness // engine goroutine only: AUTO liveness for the zone gate
	zm        core.ZoneMaskState
	maskMap   core.MaskMapState // engine goroutine only: the mask read through time (maskThroughTime)
	// serial / protocol trigger (serialtrig.go)
	serialMode    atomic.Int32
	serialMatches atomic.Int64
	serialHeld    core.Liveness // engine goroutine only: AUTO liveness for the serial gate
	ser           core.SerialState
	serLv         core.SerialLevels // the lines' levels and SPI clock, for the hardware matcher (serial_levels.go)
	qualExt       core.QualExtent   // the channels' extents, for the hardware qualifiers (qualify_hardware.go)
	bodeMode      atomic.Int32      // FRA (Bode) accumulation armed (bode.go)
	bode          core.BodeState

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
	quiet    sync.RWMutex
	norm     bool
	pendBand Band
	pendSet  bool
	trigCode uint16
	trigInit bool
	offCode  [2]uint16 // vertical-offset DAC codes asked (the board stages their writes)
	// cpuStage is held by the engine except across a CPU-only stage (SRAM frame
	// conditioning) in which it touches no bus, so a lamp staged then can be
	// latched at once by the staging goroutine instead of waiting it out.
	cpuStage   sync.Mutex
	busyWhat   string    // current long operation (Busy), "" when idle; under mu
	busyAt     time.Time // when it began
	busyDone   atomic.Int64
	busyAll    atomic.Int64
	contentGen uint64 // Frame.Content source; engine goroutine only
	// viewHeld/shownScaleGen mirror the SRAM loop's retained record and the
	// front-end scale it is shown at, for ViewPending.
	viewHeld      atomic.Bool
	rolling       atomic.Bool // the SRAM loop is in roll mode (Stats.Roll)
	holdQualify   atomic.Bool // an edge trigger keeps a loaded qualifier image (HoldQualifyImage)
	shownScaleGen atomic.Uint64
	etsWant       bool // staged ETS opt-in; applied at the frame boundary
	tp            core.TrigParams
	stats         Stats
	pubTimes      [64]int64 // recent publish times for the FPS window (a ring: notePublish), ns after pubEpoch
	pubAt         int       // the ring's next slot
	pubEpoch      time.Time // an hour before the first publish: unused slots (0) are long past

	// sramJobs carries FPGA image-switching work (fpga_stack.go) to the owner.
	sramJobs chan core.OwnerJob
	images   ImageSwitcher

	// ds are the decoded streams (decoded.go) the owner loop steps.
	ds *DecodedStreams
	// snd is the audio stream (sound_stream.go), made on first use by a web
	// request or the panel and read by the acquisition owner: published
	// atomically.
	snd       atomic.Pointer[soundState]
	soundOnce sync.Once

	// Owner-private state (no locking needed).
	acqShadow, trigShadow uint16 // last ACQ_CTRL / TRIG_LEVEL words written (compare-on-change)
	shadowInit            bool
	lastCapCols           int // record depth programmed by the last bringUp (PRETRIG+POSTTRIG)
	lastDrainN            int // words the last drain actually popped (≤ requested)
	rollBuf1, rollBuf2    []uint8
	band                  Band
	prevKind              Kind
	lastNorm              bool
	seq                   uint64
	flatHeld              int
	lastPubAt             time.Time // engine goroutine only: instant of the last oneFrame publish
	lastEdgeX             float64   // engine goroutine only: previous frame's edge (phase-continuity hint); <0 = none
	degradedRun           int       // engine goroutine only: consecutive dead-tail captures
	lastFirstHalf         bool      // the last frame's FIRST drain was a half record (pre re-capture)
	deadRuns              int
	streamSeq             uint64    // stitch-mode window counter
	lastHalt              time.Time // wall-clock of the previous window's halt (for GapNs)
	done                  chan struct{}

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
	etsSum1, etsSum2         []int64
	etsCnt                   []int32
	etsCov                   []bool
	etsAccFrames             int
	etsScratch1, etsScratch2 []uint8

	// Acq-mode state (spec 03 §7.4): AVERAGE ring, uniformity telemetry,
	// ERES scratch. avgKey tracks when the ring must clear.
	avg    core.AvgRing
	uni    core.UniRing
	avgKey struct {
		gen   uint32
		width int
		norm  bool
	}
	eresScratch []uint16
}

// useMemory builds the acquisition board over the record memory m (nil:
// none, the register path) and the panel plane, and takes its facets.
// TRLC-LINKS: REQ-SDS-001
func (e *Engine) useMemory(m acq.Memory) {
	e.useBoard(acq.New(acq.Config{Memory: m, Panel: e.b}))
}

// useBoard drives board b: the owner hears its panel plane's lamp lag and
// write errors, and takes its facets.
// TRLC-LINKS: REQ-SDS-001
func (e *Engine) useBoard(b *acq.Board) {
	e.sram = b.Memory()
	e.board = b
	e.tune = b.Tuning()
	b.SetReporting(func(d time.Duration) {
		e.mu.Lock()
		e.stats.LEDLagMs = float64(d) / float64(time.Millisecond)
		e.mu.Unlock()
	}, e.busErr)
	b.SetImages(e.images)
	b.SetInterleaveCalibration(e.interleaveCalibration)
	b.SetRawObserver(e.noteRecordCores)
	b.SetLog(func(format string, args ...any) { e.logf(format, args...) }, func() time.Time { return e.clk.Now() })
	b.SetSleep(func(d time.Duration) { e.clk.Sleep(d) })
	e.acqr, e.plat, e.trg = b.Acquirer(), b.Platform(), b.Trigger()
	e.readYield = e.plat.Service
	e.rdx = b.Readout()
}

// inst is the instrument model holding the run control.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-011
func (e *Engine) inst() *instrument.Model {
	e.instOnce.Do(func() {
		if e.instModel == nil {
			e.instModel = instrument.New(instrument.State{Channels: make([]hal.Channel, 2), Running: true, Sweep: hal.Auto}, nil)
		}
	})
	return e.instModel
}

// hw is the acquisition board, built over the record memory on first use
// by an engine made without New (a test's).
// TRLC-LINKS: REQ-SDS-001
func (e *Engine) hw() *acq.Board {
	if e.board == nil {
		e.useMemory(e.sram)
	}
	return e.board
}

// acquirer, platform and yield are the board's acquirer, its platform and
// the readout's yield (the platform's Service).
// TRLC-LINKS: REQ-SDS-001
func (e *Engine) acquirer() hal.Acquirer { e.hw(); return e.acqr }

// TRLC-LINKS: REQ-SDS-001
func (e *Engine) platform() hal.Platform { e.hw(); return e.plat }

// TRLC-LINKS: REQ-SDS-011
func (e *Engine) trigger() hal.Trigger { e.hw(); return e.trg }

// setInterleaveCalibration uses converter calibration cal: the board
// corrects its records with it, and its trigger re-arm band spans its
// offsets.
// TRLC-LINKS: REQ-SDS-016
func (e *Engine) setInterleaveCalibration(cal *InterleaveCalibration) {
	e.interleaveCalibration = cal
	e.hw().SetInterleaveCalibration(cal)
}

// TRLC-LINKS: REQ-SDS-001
func (e *Engine) yield() func() { e.hw(); return e.readYield }

// TRLC-LINKS: REQ-SDS-001
func (e *Engine) reader() hal.Readout { e.hw(); return e.rdx }

// New builds an engine over cfg's bus, filling in the defaults for the zero
// fields; Run starts its owner loop.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-007, REQ-SDS-008
func New(cfg Config) *Engine {
	// A nil clock means the real monotonic clock (production). Only then may the
	// arm-settle busy-wait, which spins on time.Now(); a fake clock's Now advances
	// only via Sleep, so tests must keep the sleep path (see armEngine).
	realTime := cfg.Clock.Now == nil
	if realTime {
		cfg.Clock = core.RealClock()
	}
	if cfg.FramePeriod == 0 {
		cfg.FramePeriod = defaultFramePeriod
	}
	if cfg.ArmSettle == 0 {
		cfg.ArmSettle = defaultArmSettle
	}
	if cfg.PollEvery == 0 {
		cfg.PollEvery = 150 * time.Microsecond
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	hasSRAM := cfg.SRAM != nil || cfg.Board != nil && cfg.Board.HasMemory()
	capacity := deepRecord
	if hasSRAM {
		capacity = int(sramcapture.SamplesPerChannel)
	}
	start, _ := PlanTdiv(500e-6) // decimated start detent: shows the cal edge fast
	e := &Engine{
		b:                     cfg.Bus,
		clk:                   cfg.Clock,
		logf:                  cfg.Logf,
		bode:                  core.BodeState{DutCh: 1}, // the frequency response's reference on C1, the DUT on C2
		arena:                 newFrameArena(capacity),
		interleaveCalibration: cfg.InterleaveCalibration,
		instModel:             cfg.Instrument,
		armSettle:             cfg.ArmSettle,
		armBusy:               realTime,
		pollEvery:             cfg.PollEvery,
		band:                  start,
		prevKind:              start.Kind(),
		done:                  make(chan struct{}),
		sramJobs:              make(chan core.OwnerJob, 1),
	}
	e.ds = newDecodedStreams(engineHost{e}, e.done)
	if cfg.Board != nil {
		e.useBoard(cfg.Board)
	} else {
		var mem acq.Memory
		if cfg.SRAM != nil { // a nil *Capture must leave the interface nil
			mem = cfg.SRAM
		}
		e.useMemory(mem)
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
	e.tune.Defaults(cfg.ArmSettle, hasSRAM, realTime)
	e.running.Store(true)
	e.trigRising.Store(true)
	e.precisionLog.Store(4)
	e.stats.PrecisionRateHz = 31.25e6
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
	e.tp = core.DefaultTrigParams()
	e.stats.TrigQual = core.QualOf(e.tp)
	e.eresScratch = make([]uint16, deepRecord)
	e.mu.Lock()
	e.stats.Running, e.stats.TrigRising = true, true
	e.stats.TrigPosFrac = 0.5 // mirror the atomic's boot default (readers normalize 0, but don't lie)
	e.stats.MmapDrain = cfg.Bus.FastDrain()
	e.stats.AvgCount, e.stats.EresLen = 16, 1
	e.syncBandStatsLocked()
	e.mu.Unlock()
	e.cpuStage.Lock() // released only inside runSRAM's conditioning stage
	return e
}

// ---- trigger-type / acq-mode setters (spec 09 §1.2: software refinement,
// zero bus access; effective next frame) ----

// ---- staging setters (any goroutine) ----

// Stop asks the owner to exit at the next frame boundary (never mid-halt) and
// waits for it. The engine is left armed and filling — a safe state to be
// SIGKILLed in; only a mid-frame kill wedges the bus, and boundaries are the
// only place we stop.
// TRLC-LINKS: REQ-SDS-008
func (e *Engine) Stop(timeout time.Duration) bool {
	e.stopReq.Store(true)
	select {
	case <-e.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// QuietRLock and QuietRUnlock bracket a concurrent CPU consumer's work (LCD render,
// web serialize, panel). It blocks while the engine is in a load-sensitive
// window (arm-settle or drain), so the consumer runs only during the loop's
// ~90ms of dead time — never during the ~19ms that corrupts the HW capture.
// TRLC-LINKS: REQ-SDS-001
func (e *Engine) QuietRLock() { e.quiet.RLock() }

// QuietRUnlock ends a QuietRLock bracket.
// TRLC-LINKS: REQ-SDS-001
func (e *Engine) QuietRUnlock() { e.quiet.RUnlock() }

// TuneVals is the live-tunable knob set (the board's tuning).
type TuneVals = acq.TuneVals

// Tune applies a knob set (debug /api/debug/tune) to the board's tuning and
// returns the effective values. Ignored on a non-device (fake clock) engine
// so tests stay stock.
// TRLC-LINKS: REQ-SDS-127
func (e *Engine) Tune(t TuneVals) TuneVals { return e.tune.Tune(t) }

// TuneSnapshot reports the current knob values.
// TRLC-LINKS: REQ-SDS-127
func (e *Engine) TuneSnapshot() TuneVals { return e.tune.Snapshot() }

// RenderPeriod is the LCD loop's tunable tick (read each render tick).
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-127
func (e *Engine) RenderPeriod() time.Duration {
	ms := max(e.tune.RenderMs.Load(), 1)
	return time.Duration(ms) * time.Millisecond
}

// Snapshot returns a copy of the stats. Never touches the bus.
// TRLC-LINKS: REQ-SDS-009, REQ-SDS-127
func (e *Engine) Snapshot() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.stats
	e.streams().fillStats(&s)
	s.Roll = e.rolling.Load() && s.Running
	s.RollStream = e.hw().RollStream()
	s.ViewPending = e.sram != nil && !s.Running && e.viewHeld.Load() && (e.pendSet || e.scaleGen.Load() != e.shownScaleGen.Load())
	s.TdivSetS = s.TdivS
	if e.pendSet {
		s.TdivSetS = e.pendBand.TdivS
	}
	if e.busyWhat != "" {
		s.Busy, s.BusyMs, s.BusyPct = e.busyWhat, float64(time.Since(e.busyAt))/float64(time.Millisecond), -1
		if all := e.busyAll.Load(); all > 0 {
			s.BusyPct = math.Min(100, 100*float64(e.busyDone.Load())/float64(all))
		}
	}
	// zone/mask live state (atomics + ring size)
	if e.sram == nil {
		s.WinCols = e.band.WinCols()
	}
	s.ZoneMode = int(e.zoneMode.Load())
	s.ZoneSkip = e.zoneSkip.Load()
	s.MaskMode = int(e.maskMode.Load())
	s.MaskPass = e.maskPass.Load()
	s.MaskFail = e.maskFail.Load()
	s.MaskSkip = e.maskSkip.Load()
	s.MaskStopped = e.maskStopped.Load()
	e.zm.Mu.Lock()
	s.ZoneCount = len(e.zm.Zones)
	s.MaskRing = len(e.zm.Ring)
	s.MaskSet, s.MaskGen = e.zm.Mask != nil, e.zm.MaskGen
	e.zm.Mu.Unlock()
	s.SerialMode = int(e.serialMode.Load())
	s.SerialMatches = e.serialMatches.Load()
	e.ser.Mu.Lock()
	s.SerialSet = !e.ser.Params.Empty()
	e.ser.Mu.Unlock()
	s.BodeMode = int(e.bodeMode.Load())
	e.bode.Mu.Lock()
	s.BodeRef, s.BodeDut = e.bode.RefCh, e.bode.DutCh
	s.BodePoints = len(e.bode.Bins)
	s.BodeValid = e.bode.LiveValid
	if e.bode.LiveValid {
		s.BodeFreqHz = e.bode.Live.FreqHz
		s.BodeGainDB = e.bode.Live.GainDB
		s.BodePhaseDeg = e.bode.Live.PhaseDeg
	}
	if s.BodeMode != 0 {
		s.BodeWhy = e.bode.Why
	}
	e.bode.Mu.Unlock()
	// FPS = publishes within the trailing second.
	// now-t <= 1 s is t at or after now-1 s: one subtraction, not one a slot
	// Times are integers on one epoch: a time.Time comparison a slot was a
	// fifth of a snapshot on the device.
	cutoff := int64(e.clk.Now().Sub(e.pubEpoch) - time.Second)
	n := 0
	for _, t := range e.pubTimes { // the ring in any order (unused slots are zero: long past)
		if t >= cutoff {
			n++
		}
	}
	s.FPS = float64(n)
	return s
}

// Consume hands the newest published frame to a consumer (the web layer).
// TRLC-LINKS: REQ-SDS-007
func (e *Engine) Consume() (*sample.Frame, bool) { return e.arena.Consume() }

// AcqLog returns the last n acquisition samples (most-recent-last) plus the
// HALF rate over the last up-to-64 recorded samples. Instrumentation only; the
// ring is read under e.mu since it is written by the engine goroutine. n is
// clamped to the ring size.
// TRLC-LINKS: REQ-SDS-127
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
	for i := range len(e.acqRing) {
		idx := (e.acqHead - 1 - i + len(e.acqRing)) % len(e.acqRing)
		if e.acqRing[idx].Cols == 0 {
			break
		}
		avail++
	}
	// HALF rate over the last up-to-64 recorded samples.
	rateN := min(avail, 64)
	half := 0
	for i := range rateN {
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
	for i := range n { // most-recent-last: fill from the tail back
		idx := (e.acqHead - n + i + len(e.acqRing)) % len(e.acqRing)
		out[i] = e.acqRing[idx]
	}
	return out, rate
}

// CmdLog returns the last n command notes (most-recent-last). Instrumentation
// only; read under e.mu since NoteCmd writes from the HTTP goroutine.
// TRLC-LINKS: REQ-SDS-127
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
	for i := range len(e.cmdRing) {
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
	for i := range n {
		idx := (e.cmdHead - n + i + len(e.cmdRing)) % len(e.cmdRing)
		out[i] = e.cmdRing[idx]
	}
	return out
}

// NoteCmd records a web set-control invocation into the command ring, stamping
// the current published Seq. Called from the HTTP goroutine → locks e.mu.
// Instrumentation only; it never touches the bus or the FSM.
// TRLC-LINKS: REQ-SDS-127
func (e *Engine) NoteCmd(name string, val float64) {
	e.mu.Lock()
	e.cmdRing[e.cmdHead] = CmdNote{Name: name, Val: val, Seq: e.stats.Seq}
	e.cmdHead = (e.cmdHead + 1) % len(e.cmdRing)
	e.mu.Unlock()
}

// ---- owner goroutine ----

// TrigLevelVolts converts a DAC code to approximate volts using the measured
// global fit (docs/trigcal-notes.md): code = 31437 − 911·V.
// TRLC-LINKS: REQ-SDS-016
func TrigLevelVolts(code uint16) float64 { return (31437 - float64(code)) / 911 }

// NoteLCDFrame counts distinct acquisition frames actually presented to the LCD.
// TRLC-LINKS: REQ-SDS-009
func (e *Engine) NoteLCDFrame() { e.mu.Lock(); e.stats.LCDFrames++; e.mu.Unlock() }

// SincePublish is how long ago the newest frame was published; zero before
// the first. A running NORMAL acquisition that has published nothing for a
// while is waiting for a trigger, whatever the held frame's own state.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) SincePublish() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastPublish.IsZero() {
		return 0
	}
	return e.clk.Now().Sub(e.lastPublish)
}

// ForceTrigger completes the capture now armed without waiting for a
// trigger, once: the FORCE key. In NORMAL or SINGLE it shows what the input
// is doing while no trigger arrives.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) ForceTrigger() { e.forceReq.Store(true) }

// setBusy marks a long operation for the loading indicator: what is shown,
// total is its size in samples for progress (0 = unknown). "" clears it.
// TRLC-LINKS: REQ-SDS-168
func (e *Engine) setBusy(what string, total int) {
	e.busyAll.Store(int64(total))
	e.busyDone.Store(0)
	e.mu.Lock()
	if what != e.busyWhat {
		if e.busyWhat == "" {
			e.busyAt = time.Now()
		}
		e.busyWhat = what
	}
	e.mu.Unlock()
}

// SetDecodeView records that the display decodes a protocol from the published
// frame. Live frames serve decoding as they are: a live envelope's (min, max)
// pairs are put in time order (orderEnvelope): unordered pairs misread from 5
// to 200 us/div.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-010
func (e *Engine) SetDecodeView(on bool) { e.decodeView.Store(on) }

// LeaseDecodeView asks for full-rate raw live frames for d from now (eye
// diagram, super-res stacking): no envelope, no decimation, at the cost of
// the frame rate on a deep screen. Renewed while in use, so a closed tab
// cannot leave it on. d <= 0 ends it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-010
func (e *Engine) LeaseDecodeView(d time.Duration) {
	e.lease(&e.decodeLease, d)
	if d <= 0 {
		e.abandonRawRead()
	}
}

// LeaseRawFor is LeaseDecodeView for one holder (the LCD's eye, super-res, a
// bus search): its lease is its own, renewed and ended without touching the
// others'. Ending the last one (d <= 0) abandons a live raw read still in
// flight for the leases (at 200us/div a megasample takes seconds), so the
// next capture is the fast live view at once; a read anything else needs (a
// stopped or SINGLE record, averaging, a still-leased consumer) is left be.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-010
func (e *Engine) LeaseRawFor(holder string, d time.Duration) {
	e.rawMu.Lock()
	if d <= 0 {
		delete(e.rawHolders, holder)
	} else {
		if e.rawHolders == nil {
			e.rawHolders = map[string]int64{}
		}
		e.rawHolders[holder] = e.clk.Now().Add(min(d, 10*time.Second)).UnixNano()
	}
	e.rawMu.Unlock()
	if d <= 0 {
		e.abandonRawRead()
	}
}

// rawLeased reports whether any raw lease is current: the shared one
// (LeaseDecodeView) or a holder's (LeaseRawFor).
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) rawLeased() bool {
	if e.leased(&e.decodeLease) {
		return true
	}
	e.rawMu.Lock()
	defer e.rawMu.Unlock()
	if e.clk.Now == nil {
		return false
	}
	now := e.clk.Now().UnixNano()
	for h, end := range e.rawHolders {
		if now < end {
			return true
		}
		delete(e.rawHolders, h)
	}
	return false
}

// abandonRawRead cancels the live raw read in flight for the leases when no
// lease wants it any more.
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) abandonRawRead() {
	if e.rawLeased() {
		return
	}
	e.rawMu.Lock()
	cancel := e.rawCancel
	e.rawCancel = nil
	e.rawMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// LeaseSampleView asks for evenly sampled live frames for d from now (FFT,
// autoset): an anti-aliased decimated capture serves, so the frame rate holds.
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) LeaseSampleView(d time.Duration) { e.lease(&e.sampleLease, d) }

// LeaseSearchView asks for live frames a bus search can read whole frames of
// slow buses in, for d from now: decimated captures of SearchLiveSamples a
// screen in records of leaseRecord screens, and no roll - a roll view's chunks leave gaps of
// held samples that cut an IR remote's 67 ms frame or a CEC message apart.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-214
func (e *Engine) LeaseSearchView(d time.Duration) { e.lease(&e.searchLease, d) }

// LeaseBusSamples asks for live frames a bus decode reads where the envelope
// or roll is too coarse for it, for d from now: records sampled every
// sampleS at the coarsest (decimated, filtered by the capture's decimation;
// the full rate where that is coarser), of leaseRecord screens, and no roll.
// The fewer words read the faster the frames: a 115200 Bd UART at 10ms/div
// read 390,000 words a frame at the full record's rate. d <= 0 ends it.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-214
func (e *Engine) LeaseBusSamples(sampleS float64, d time.Duration) {
	if !(sampleS > 0) {
		d = 0
	}
	e.busSampleS.Store(math.Float64bits(sampleS))
	e.lease(&e.busLease, d)
}

// LeaseSpectrumView asks for live frames a spectrum can be taken of, for d
// from now (the FFT and the spectrogram, on the LCD and the web alike): the
// full-rate record while the screen holds at most SpectrumRawSamples samples,
// else the anti-aliased decimated capture, whose frames carry the filter's
// bandwidth - the spectrum ends there (a capture filtered at 4.2 MHz is not
// drawn to 15.6 MHz, and no surface transforms the envelope).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-070
func (e *Engine) LeaseSpectrumView(d time.Duration) { e.lease(&e.spectrumLease, d) }

// TRLC-LINKS: REQ-SDS-010
func (e *Engine) lease(l *atomic.Int64, d time.Duration) {
	if d <= 0 {
		l.Store(0)
		return
	}
	l.Store(e.clk.Now().Add(min(d, 10*time.Second)).UnixNano())
}

// leased reports whether lease l is current.
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) leased(l *atomic.Int64) bool {
	v := l.Load()
	return v != 0 && e.clk.Now != nil && e.clk.Now().UnixNano() < v
}
