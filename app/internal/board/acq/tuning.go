// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"sync/atomic"
	"time"
)

// The live tuning knobs (the debug tune route) are the board's: the arm and
// halt timings of the fabric, the native-fast fill and maturation, the
// trigger comparator's select, the decimated small-signal gate, the render
// period and the collector's control. The register-path loop reads them each
// frame; the SRAM record path takes none of them, so on the instrument they
// are a document the bench reads and sets.

// DefaultArmSettle is the pause between re-programming and OPCODE = GO. It was
// 2 ms, copied from the vendor's own arm sequence ("~2 ms settle",
// fpga-specs/22 §4.2 step 2). Measured on the instrument, halving
// it takes the frame rate from 62 to 72.6 fps and going further buys almost
// nothing (73.8 at 100 us, 75.0 at 50 us), so 1 ms keeps the largest margin
// among the settings that actually help — still half the vendor's, not a
// quarter. 90 s soak at 1 ms: 6,540 frames at 72.7 fps, 0 incoherent, 0 bus
// errors, 0 held, 0 dead runs, 0 short drains, and a TSRC ramp read back
// afterwards with 0 breaks over 256 distinct codes.
const DefaultArmSettle = 1 * time.Millisecond

// DefaultMatureUs is the native-fast maturation floor: how long a frame must
// run before it may halt once the record is full. It was 3000 us. The gate only
// shortcuts AUTO and anchored frames — NORM without a trigger waits the full
// budget either way (see engine_capture.go) — so this is a liveness knob, not a
// trigger-sensitivity one. Measured on the instrument: 72.7 fps
// at 3000 us, 80.0 at 1500, and 89.2 at 750 where it saturates (300 us and
// 100 us give the same 89). 750 us it is, the knee. 90 s soak: 8,025 frames at
// 89.2 fps, 0 incoherent, 0 bus errors, 0 held, 0 dead runs, 0 short drains,
// TSRC ramp afterwards 0 breaks over 256 distinct codes.
const DefaultMatureUs = 750

// TuneVals is the live-tunable knob set.
type TuneVals struct {
	ArmSettleUs  int64 `json:"arm_settle_us"`
	ArmSpin      bool  `json:"arm_spin"`
	BusyFillUs   int64 `json:"busy_fill_us"`
	GcCtl        bool  `json:"gc_ctl"`
	MaxRetry     int64 `json:"max_retry"`
	RenderMs     int64 `json:"render_ms"`
	FillExtraUs  int64 `json:"fill_extra_us"`
	HaltSettleUs int64 `json:"halt_settle_us"`
	MatureUs     int64 `json:"mature_us"`        // native-fast maturation floor before halt (µs)
	HwTrig       bool  `json:"hw_trig"`          // trigger on the A12 hardware comparator (TRIG_LEVEL.HW_SEL) instead of the software compare
	SigK         int64 `json:"sig_k"`            // decimated small-signal gate: min ptp/noiseFloor ratio to lock (default 8)
	Reinit       int64 `json:"reinit,omitempty"` // one-shot: stage an FSM re-init at this level (1=bringUp, 2=+runword/reset pulses)
}

// Tuning is the knobs as atomics: the web handler sets them lock-free and
// the owner reads them each frame. Live gates Tune: off (a fake-clock
// engine's tests), the knobs stay as they are.
type Tuning struct {
	ArmSettleUs  atomic.Int64 // arm settle duration (µs)
	ArmSpin      atomic.Bool  // busy-wait the settle vs Sleep
	BusyFillUs   atomic.Int64 // native-fast fill busy-poll window (µs; 0 = off)
	GcCtl        atomic.Bool  // controlled GC (SetGCPercent(-1)+manual) vs stock
	MaxRetry     atomic.Int64 // native-fast re-capture cap
	RenderMs     atomic.Int64 // LCD render period (ms)
	FillExtraUs  atomic.Int64 // native-fast: extra fill time after done, before halt (µs)
	HaltSettleUs atomic.Int64 // native-fast: post-halt settle before deep-port reads (µs)
	MatureUs     atomic.Int64 // native-fast maturation floor before halt (µs)
	HwTrig       atomic.Bool  // TRIG_LEVEL.HW_SEL: trigger on the A12 comparator (MAX V DAC) instead of the fabric's software compare
	SigK         atomic.Int64 // decimated small-signal gate: min ptp / noiseFloor ratio to lock
	Reinit       atomic.Int64 // staged FSM re-init level (debug/recovery); the register-path loop takes it at its boundary
	Live         atomic.Bool
}

// Defaults sets the knobs' starting values: armSettle the arm settle,
// sram whether the SRAM record path runs (a 2 ms render period, else the
// legacy capture cadence's 120 ms), live whether Tune applies.
// TRLC-LINKS: REQ-SDS-127
func (t *Tuning) Defaults(armSettle time.Duration, sram, live bool) {
	t.ArmSettleUs.Store(armSettle.Microseconds())
	// The settle busy-spin and manual GC control were workarounds tuned against
	// the untriggered half-record state; a plain Sleep settle and the stock
	// collector are better on the instrument.
	t.ArmSpin.Store(false)
	t.BusyFillUs.Store(0) // no fill busy-poll (pure CPU cost)
	t.GcCtl.Store(false)
	t.MaxRetry.Store(2) // light backstop; full records are the norm
	t.RenderMs.Store(120)
	if sram {
		t.RenderMs.Store(2)
	}
	// fill-extra and halt-settle buy nothing with triggered captures and cost
	// 4 ms a frame: kept at 0 for experiments.
	t.FillExtraUs.Store(0)
	t.HaltSettleUs.Store(0)
	t.HwTrig.Store(false) // the fabric's software level compare
	t.MatureUs.Store(DefaultMatureUs)
	t.SigK.Store(8) // real signals ratio ≥16, flat rails ≤7
	t.Live.Store(live)
}

// Tune applies a knob set and returns the effective values; fields omitted
// or negative are left as they are. Not live: nothing changes.
// TRLC-LINKS: REQ-SDS-127
func (t *Tuning) Tune(v TuneVals) TuneVals {
	if !t.Live.Load() {
		return t.Snapshot()
	}
	if v.ArmSettleUs > 0 {
		t.ArmSettleUs.Store(v.ArmSettleUs)
	}
	if v.BusyFillUs >= 0 {
		t.BusyFillUs.Store(v.BusyFillUs)
	}
	if v.MaxRetry >= 0 {
		t.MaxRetry.Store(v.MaxRetry)
	}
	if v.RenderMs > 0 {
		t.RenderMs.Store(v.RenderMs)
	}
	if v.FillExtraUs >= 0 {
		t.FillExtraUs.Store(v.FillExtraUs)
	}
	if v.HaltSettleUs >= 0 {
		t.HaltSettleUs.Store(v.HaltSettleUs)
	}
	if v.MatureUs > 0 {
		t.MatureUs.Store(v.MatureUs)
	}
	if v.SigK > 0 {
		t.SigK.Store(v.SigK)
	}
	if v.Reinit > 0 {
		t.Reinit.Store(v.Reinit) // one-shot; the owner loop consumes it
	}
	t.ArmSpin.Store(v.ArmSpin)
	t.GcCtl.Store(v.GcCtl)
	t.HwTrig.Store(v.HwTrig)
	return t.Snapshot()
}

// Snapshot is the knobs' values.
// TRLC-LINKS: REQ-SDS-127
func (t *Tuning) Snapshot() TuneVals {
	return TuneVals{
		ArmSettleUs:  t.ArmSettleUs.Load(),
		ArmSpin:      t.ArmSpin.Load(),
		BusyFillUs:   t.BusyFillUs.Load(),
		GcCtl:        t.GcCtl.Load(),
		MaxRetry:     t.MaxRetry.Load(),
		RenderMs:     t.RenderMs.Load(),
		FillExtraUs:  t.FillExtraUs.Load(),
		HaltSettleUs: t.HaltSettleUs.Load(),
		MatureUs:     t.MatureUs.Load(),
		HwTrig:       t.HwTrig.Load(),
		SigK:         t.SigK.Load(),
	}
}

// Tuning is the board's live tuning knobs. ANY.
// TRLC-LINKS: REQ-SDS-127
func (b *Board) Tuning() *Tuning { return &b.tune }

// Tune applies a knob set to the board's tuning (the debug tune route).
// ANY.
// TRLC-LINKS: REQ-SDS-127
func (b *Board) Tune(v TuneVals) TuneVals { return b.tune.Tune(v) }

// TuneSnapshot is the board's tuning knobs' values. ANY.
// TRLC-LINKS: REQ-SDS-127
func (b *Board) TuneSnapshot() TuneVals { return b.tune.Snapshot() }
