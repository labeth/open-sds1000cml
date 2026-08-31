package analog

import (
	"fmt"
	"math"
	"sync"
	"time"

	"open-sds/app/internal/cal"
)

// Detent is one V/div step of the vertical ladder (spec 06 §4.3). The 2 mV
// and 5 mV detents run the 10 mV analog range with ×5/×2 display zoom — the
// code→volts mapping stays on the analog range; the zoom is display-only.
type Detent struct {
	VdivV float64 // requested (displayed) volts/div
	Gain  uint8   // fine-gain DAC code
	Atten bool    // relay bit2: coarse range (true = attenuated 500 mV–10 V)
	Zoom  int     // display magnification (1, 2 or 5)
}

// Detents is the 12-step ladder; index is the detent number.
var Detents = []Detent{
	{0.002, 146, false, 5},
	{0.005, 146, false, 2},
	{0.010, 146, false, 1},
	{0.020, 63, false, 1},
	{0.050, 25, false, 1},
	{0.100, 12, false, 1},
	{0.200, 6, false, 1},
	{0.500, 115, true, 1},
	{1.0, 57, true, 1},
	{2.0, 28, true, 1},
	{5.0, 11, true, 1},
	{10.0, 6, true, 1},
}

// BootDetent is the factory bring-up detent (1 V/div) both channels sit at.
const BootDetent = 8

// PlanVdiv resolves requested volts/div to a detent index (1e-6 rel tol).
func PlanVdiv(v float64) (int, bool) {
	for i, d := range Detents {
		if math.Abs(d.VdivV-v) <= d.VdivV*1e-6 {
			return i, true
		}
	}
	return 0, false
}

// AnalogVdiv is the electrical volts/div of a detent (the 10 mV range for
// the zoomed detents): volts per sample code = AnalogVdiv/50 (spec 06 §7.1).
func AnalogVdiv(idx int) float64 {
	d := Detents[idx]
	return d.VdivV * float64(d.Zoom)
}

// Coupling modes. They name a state, not a transport: the SAME three constants
// drive two DELIBERATELY INDEPENDENT controls.
//
//   - SetCoupling / CoupleDisplay — the shipped SOFTWARE path. It never touches
//     the relay: AC = software DC-removal (mean → mid-scale), GND = a flat
//     ground trace. The reason is spec 06 §6's reading that the relay's coupling
//     bit selects a different offset-cal baseline (~36 codes) rather than a
//     series capacitor, so there would be no hardware high-pass to gain.
//   - SetCouplingHW — the real relay bits (relay.go). That reading is UNTESTED:
//     no measurement stands behind "there is no hardware high-pass", and the
//     trigger path's AC/LFREJ relays on this same board DO behave like one, so
//     DC-blocking parts exist on this front end (src: fpga-specs/takeover/
//     13-analog-frontend.md §3.3 AFE-3; fpga-specs/26-trigger.md §2.4).
//     AF-2.2/AF-2.3 measure it; SetCouplingHW is what lets them.
//
// Relay encoding, per-channel byte — all three are CAPTURED vendor words, not
// decompile inference (src: fpga-specs/takeover/13-analog-frontend.md §2.3;
// fpga-specs/40-level-dac-and-analog-control.md §6.5):
//
//	DC  = bit3 set, bit1 clear  (CH1 byte 0x2d, word 0x70ad2d)  ← C1:CPL D1M
//	AC  = bit3 clear, bit1 clear (CH1 byte 0x25, word 0x70ad25) ← C1:CPL A1M
//	GND = bit1 set, bit3 clear  (CH1 byte 0x27, word 0x70ad27)  ← C1:CPL GND
//
// GND looks inert only under a read-modify-write that leaves bit3 set — which is
// exactly what applyLocked's absolute-word discipline makes impossible here
// (src: fpga-specs/40-… §8.2, "the GND relay is inert/unpopulated" retracted).
const (
	CplDC  = 0
	CplAC  = 1
	CplGND = 2
)

// Trigger-path coupling — relay byte 2's HIGH nibble (spec 06 §3;
// fpga-specs/40-level-dac-and-analog-control.md §6.5). All four nibble values
// are captured vendor words in the reglog corpus, each sitting under its own
// `TRCP` command marker (src: fpga-specs/takeover/13-analog-frontend.md §2.3):
// byte2 = 0x70 DC, 0x50 AC, 0xf0 HFREJ, 0x40 LFREJ. Our stack has hardcoded
// 0x70 since it was written; SetTrigCoupling (relay.go) is AF-2.4's actuator.
const (
	TrigCplDC    = 0
	TrigCplAC    = 1
	TrigCplHFREJ = 2
	TrigCplLFREJ = 3
)

// trigCplNibble maps a TrigCpl* mode to relay byte 2's high nibble.
var trigCplNibble = [4]uint8{TrigCplDC: 0x7, TrigCplAC: 0x5, TrigCplHFREJ: 0xf, TrigCplLFREJ: 0x4}

// channelByte builds one channel's relay byte (spec 06 §3;
// fpga-specs/40-level-dac-and-analog-control.md §6.5):
//
//	bit0  bandwidth limit — 1 = BWL OFF (full bandwidth), 0 = 20 MHz limit ENGAGED
//	bit1  GND coupling select (GND = bit1 set WITH bit3 clear)
//	bit2  coarse range — 1 = attenuated (500 mV…10 V), 0 = sensitive (2…200 mV)
//	bit3  DC coupling select
//	bit5  constant enable, always 1
//	bit7  CH2 channel-address bit (CH1 base 0x20, CH2 base 0xa0) — it addresses
//	      the byte to a latch; it is NOT a coupling-polarity bit
//
// bwlOn means the 20 MHz limit is ENGAGED, which CLEARS bit0: the bit's sense is
// inverted on the wire, and which bit it even is has been wrong in the corpus
// before (fpga-specs/40-… §8.2 retires both "BWL is bit1 or bit3" and
// "CS1 0x2c is the bandwidth limit"). bit0 is the SPI-capture answer.
//
// bits 4 and 6 are UNASSIGNED and are set in NO captured vendor word
// (src: fpga-specs/takeover/13-analog-frontend.md §2.3), so nothing here ever
// sets them — SetRelayRaw is the escape hatch that walks them (AF-2.5).
func channelByte(idx int, ch2, bwlOn bool, cpl int) uint8 {
	b := uint8(0x20) // bit5, constant enable
	if !bwlOn {
		b |= 0x01 // bit0 set = limit OFF = full bandwidth
	}
	switch cpl {
	case CplAC:
		// bit1 and bit3 both clear
	case CplGND:
		b |= 0x02 // bit1, with bit3 clear
	default: // CplDC
		b |= 0x08 // bit3
	}
	if Detents[idx].Atten {
		b |= 0x04
	}
	if ch2 {
		b |= 0x80
	}
	return b
}

// FrontEnd owns the SPI shadows. It is producer-direct (off the GPMC bus)
// and serializes itself; it never touches the acquisition engine — it stages
// the offset DAC through the injected hook so the code re-anchors to each
// detent's calibrated zero when V/div changes.
type FrontEnd struct {
	mu      sync.Mutex
	tr      Transport
	sleep   func(time.Duration)
	tab     *cal.Table
	idx     [2]int
	trigSrc int  // relay byte2 bits[3:2]; HW-REFUTED as a source selector — see relayWord
	emitted bool // seed-don't-emit: leave the inherited analog range alone

	stage   func(ch int, code uint16)              // offset-DAC stager (engine.SetOffsetDAC)
	onOffV  func(ch int, offV float64)             // applied-offset-volts hook (engine trigger reference)
	onVdiv  func(ch int, vdivV, zero, cpv float64) // V/div change hook (engine trigger map + per-detent cal)
	offReqV [2]float64                             // requested input-referred offset volts
	offSet  [2]bool                                // whether the user has set an offset
	probe   [2]float64                             // per-channel probe attenuation (display multiplier)
	cpl     [2]int                                 // per-channel SOFTWARE coupling (CplDC/CplAC/CplGND)
	trigCal [2][numDetents]TrigCal                 // per-channel, per-detent trigger DAC cal

	// Relay-word shadows for the actuators driven from relay.go. Their zero
	// values reproduce the shipped word exactly (BWL off, DC, trigger DC), so
	// the V/div path is byte-identical to before these controls existed.
	bwl     [2]bool // 20 MHz bandwidth limit ENGAGED (relay bit0 CLEARED)
	cplHW   [2]int  // per-channel HARDWARE coupling relay (CplDC/CplAC/CplGND)
	trigCpl int     // trigger-path coupling, relay byte 2 high nibble (TrigCpl*)
	rawDbg  bool    // raw escape hatches armed (RawDebugEnv / SetRawDebug)
}

// New seeds both channels' shadows to the boot detent WITHOUT emitting —
// the inherited analog state stays untouched until the first user change
// (spec 06 §4.4 startup rule; an unseeded emit collapses the other
// channel's gain). tab may be nil (→ compiled defaults).
func New(tr Transport, sleep func(time.Duration), tab *cal.Table) *FrontEnd {
	if sleep == nil {
		sleep = time.Sleep
	}
	if tab == nil {
		tab = cal.Defaults()
	}
	return &FrontEnd{tr: tr, sleep: sleep, tab: tab, idx: [2]int{BootDetent, BootDetent},
		probe: [2]float64{1, 1}, rawDbg: rawDebugFromEnv()}
}

// SetProbe sets a channel's probe attenuation factor (1, 10 or 100). It is a
// pure display/readout multiplier — the analog gain is untouched; every volts
// readout (measurements, cursors, CSV, trigger level, offset) scales by it so
// the numbers reflect the signal at the probe tip.
func (f *FrontEnd) SetProbe(ch int, x float64) {
	if x < 1 {
		x = 1
	}
	f.mu.Lock()
	f.probe[ch&1] = x
	f.mu.Unlock()
}

// ProbeFactor returns a channel's probe attenuation (default 1).
func (f *FrontEnd) ProbeFactor(ch int) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p := f.probe[ch&1]; p >= 1 {
		return p
	}
	return 1
}

// SetCoupling records a channel's input coupling (CplDC/CplAC/CplGND) for the
// SOFTWARE display transform. It never touches the relay (see the Coupling
// constants and CoupleDisplay), so it is always safe and takes effect on the
// next served/rendered frame. SetCouplingHW is the hardware-relay counterpart;
// the two are independent by design and neither reads the other.
func (f *FrontEnd) SetCoupling(ch, mode int) error {
	if mode < CplDC || mode > CplGND {
		return fmt.Errorf("analog: bad coupling ch=%d mode=%d", ch, mode)
	}
	f.mu.Lock()
	f.cpl[ch&1] = mode
	f.mu.Unlock()
	return nil
}

// Coupling returns a channel's coupling mode (default CplDC).
func (f *FrontEnd) Coupling(ch int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cpl[ch&1]
}

// CoupleDisplay applies a coupling mode's software transform to a copy of sig
// for the display and measurements: DC passes through unchanged; AC removes the
// DC component (mean → mid-scale — there is no hardware high-pass, spec 06 §6);
// GND shows a flat trace at mid-scale (the ground reference). The input is never
// mutated (it is shared with trigger/decode). Callers zero the channel's offset
// for AC/GND so the ground marker sits at the centred baseline.
func CoupleDisplay(sig []uint8, mode int) []uint8 {
	switch mode {
	case CplAC:
		return RemoveDC(sig)
	case CplGND:
		out := make([]uint8, len(sig))
		for i := range out {
			out[i] = 128
		}
		return out
	default:
		return sig
	}
}

// RemoveDC returns a copy of sig with its DC component removed: the record mean
// is shifted to mid-scale (code 128), clamped to [0,255]. The input is never
// mutated.
func RemoveDC(sig []uint8) []uint8 {
	if len(sig) == 0 {
		return sig
	}
	var sum int
	for _, v := range sig {
		sum += int(v)
	}
	shift := 128 - int(math.Round(float64(sum)/float64(len(sig))))
	out := make([]uint8, len(sig))
	for i, v := range sig {
		c := int(v) + shift
		if c < 0 {
			c = 0
		} else if c > 255 {
			c = 255
		}
		out[i] = uint8(c)
	}
	return out
}

// OnOffset wires the offset-DAC stager (engine.SetOffsetDAC). Until set,
// SetOffset just records the requested volts.
func (f *FrontEnd) OnOffset(fn func(ch int, code uint16)) { f.stage = fn }

// OnOffsetV wires the applied-offset-volts hook (engine.SetChannelOffsetV) so
// the trigger discrimination level rides the same offset reference as the
// samples. Called immediately with both channels' current applied offset.
func (f *FrontEnd) OnOffsetV(fn func(ch int, offV float64)) {
	f.onOffV = fn
	if fn != nil {
		fn(0, f.appliedOffV(0))
		fn(1, f.appliedOffV(1))
	}
}

// appliedOffV is the input-referred offset volts currently applied to a channel
// (0 if the user has never set one — the boot offset is left inherited and the
// display treats it as 0, matching vertScales).
func (f *FrontEnd) appliedOffV(ch int) float64 {
	f.mu.Lock()
	set, req := f.offSet[ch&1], f.offReqV[ch&1]
	f.mu.Unlock()
	if !set {
		return 0
	}
	return f.OffsetVolts(ch, f.OffsetCode(ch, req))
}

// OnVdiv wires a V/div-change hook (engine.SetChannelVdiv) so the trigger
// level maps to the right display code. Called immediately with the seeded
// detents so the engine starts consistent.
func (f *FrontEnd) OnVdiv(fn func(ch int, vdivV, zero, cpv float64)) {
	f.onVdiv = fn
	if fn != nil {
		f.mu.Lock()
		i0, i1 := f.idx[0], f.idx[1]
		f.mu.Unlock()
		z0, c0 := f.TrigCalActive(0)
		z1, c1 := f.TrigCalActive(1)
		fn(0, Detents[i0].VdivV, z0, c0)
		fn(1, Detents[i1].VdivV, z1, c1)
	}
}

// SetOffset records a requested input-referred offset in volts, derives the
// DAC code against the CURRENT detent's calibrated zero, and stages it.
// Being volts-based, the offset survives a V/div change (SetVdiv re-anchors).
func (f *FrontEnd) SetOffset(ch int, volts float64) uint16 {
	ch &= 1
	code := f.OffsetCode(ch, volts)
	f.mu.Lock()
	f.offReqV[ch], f.offSet[ch] = volts, true
	f.mu.Unlock()
	if f.stage != nil {
		f.stage(ch, code)
	}
	if f.onOffV != nil {
		f.onOffV(ch, f.OffsetVolts(ch, code)) // keep the trigger reference current
	}
	return code
}

// OffsetReqV returns the last requested offset volts for a channel.
func (f *FrontEnd) OffsetReqV(ch int) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.offReqV[ch&1]
}

// CalSource reports what calibration is loaded: file | backup | defaults.
func (f *FrontEnd) CalSource() string { return f.tab.Source }

// gainFor picks the per-unit gain-DAC code when a real cal file is loaded;
// the compiled-default case keeps the spec 06 ladder codes (validated on
// this hardware) rather than the firmware boot ladder.
func (f *FrontEnd) gainFor(ch, idx int) uint8 {
	if f.tab.Source != "defaults" {
		return uint8(f.tab.Rec[ch&1][idx].GainDAC)
	}
	return Detents[idx].Gain
}

// offsetZeroAndDetent returns the channel's current detent index and its
// calibrated 0 V offset-DAC code (per-(ch,vd) cal record; fallback boot
// default 10223). f.tab is immutable after New, so only f.idx needs the lock.
func (f *FrontEnd) offsetZeroAndDetent(ch int) (int, float64) {
	f.mu.Lock()
	vd := f.idx[ch&1]
	f.mu.Unlock()
	return vd, float64(f.tab.Rec[ch&1][vd].Zero)
}

// OffsetCode converts an input-referred offset in volts to a DAC code using the
// per-tier calibrated zero and the vendor per-division law (spec 06 §5.2):
// code = clamp(round(zero − 50·(V/VDIV))). The slope is 50 DAC codes per
// division (codes/volt = 50/VDIV); the coarse attenuator sets only the tier
// clamp (±1.6 V ×1 / ±40 V ×25), not the slope. Inverting: +V → lower code.
func (f *FrontEnd) OffsetCode(ch int, volts float64) uint16 {
	vd, zero := f.offsetZeroAndDetent(ch)
	return offsetCode(zero, vd, volts)
}

// OffsetVolts is the inverse for labels/readback (spec 06 §5.2):
// V = (zero − code)/offsetCodesPerVolt, honest against the code OffsetCode made.
func (f *FrontEnd) OffsetVolts(ch int, code uint16) float64 {
	_, zero := f.offsetZeroAndDetent(ch)
	return (zero - float64(code)) / offsetCodesPerVolt
}

// OffsetK returns the offset-DAC slope, codes per input-volt (spec 06 §5.2) —
// a FIXED constant (offsetCodesPerVolt), NOT scaled by V/div. The panel offset
// knob divides its fixed 20-code step by this (a constant 0.2 V/step).
func (f *FrontEnd) OffsetK(ch int) float64 {
	return offsetCodesPerVolt
}

// DCVolts is the calibrated detent-invariant DC diagnostic (spec 10 §3.3).
func (f *FrontEnd) DCVolts(ch int, meanCode float64) float64 {
	f.mu.Lock()
	vd := f.idx[ch&1]
	f.mu.Unlock()
	return f.tab.DCVolts(ch&1, vd, meanCode)
}

// relayWord composes the FULL absolute 24-bit relay word from the shadows.
// It is a pure function of state — it never reads a previous word back, so
// there is no read-modify-write path to leave a stale bit set. Caller holds
// f.mu.
func (f *FrontEnd) relayWord() uint32 {
	b0 := channelByte(f.idx[0], false, f.bwl[0], f.cplHW[0])
	b1 := channelByte(f.idx[1], true, f.bwl[1], f.cplHW[1])
	// byte2 = (trigger-coupling nibble << 4) | (trigSrc << 2). trigSrc is kept
	// only because it is part of the absolute word: it is HW-REFUTED as a source
	// selector on #716 (the boot word carries the C1 nibble while the inherited
	// source was C2), and the runtime source mux is CS1 0x22 in software
	// (src: fpga-specs/40-… §8.2; takeover/13-analog-frontend.md §1.1 A9 note).
	// So there is deliberately no named setter for it.
	b2 := trigCplNibble[f.trigCpl&3]<<4 | uint8(f.trigSrc&3)<<2
	return uint32(b0) | uint32(b1)<<8 | uint32(b2)<<16
}

// applyLocked emits the current shadows as one absolute word. Caller holds f.mu.
func (f *FrontEnd) applyLocked() error { return f.emitLocked(f.relayWord()) }

// emitLocked emits the exact spec 06 §4.4 sequence for ONE absolute relay word:
// the full word (never read-modify-write), a ~400 µs coarse-relay settle, then
// BOTH gain bytes, CH2 first. word is always a complete 24-bit word supplied by
// the caller, so nothing in this package can emit a partial update — that is the
// discipline that stops the 2026-07-24 failure mode where an emit from an
// unseeded shadow collapsed BOTH channels' gain. Every actuator here, named or
// raw, funnels through this one function. Caller holds f.mu.
func (f *FrontEnd) emitLocked(word uint32) error {
	if err := f.tr.WriteRelay(word); err != nil {
		return err
	}
	f.sleep(400 * time.Microsecond)
	// Both gain bytes, always: a relay change can re-trim the V/div gain (spec
	// 06 §6, the BWL path does exactly this), and the untouched channel's byte
	// must be re-asserted from its seeded shadow, never left to the latch.
	if err := f.tr.WriteGain(f.gainFor(1, f.idx[1]), f.gainFor(0, f.idx[0])); err != nil {
		return err
	}
	f.emitted = true
	return nil
}

// SetVdiv applies a detent to a channel (0=C1, 1=C2). Gain takes effect on
// the next frame; no readback or retry is needed. If a user offset is set,
// its DAC code is re-derived against the new detent's calibrated zero and
// re-staged (the input-referred offset must survive a range change).
func (f *FrontEnd) SetVdiv(ch, idx int) error {
	if ch < 0 || ch > 1 || idx < 0 || idx >= len(Detents) {
		return fmt.Errorf("analog: bad vdiv ch=%d idx=%d", ch, idx)
	}
	f.mu.Lock()
	f.idx[ch] = idx
	err := f.applyLocked()
	reReq, reSet := f.offReqV[ch], f.offSet[ch]
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if f.onVdiv != nil {
		z, c := f.TrigCalActive(ch)
		f.onVdiv(ch, Detents[idx].VdivV, z, c) // keep the engine's trigger map + per-detent cal current
	}
	if reSet && f.stage != nil {
		code := f.OffsetCode(ch, reReq) // OffsetCode uses the new detent zero
		f.stage(ch, code)
		if f.onOffV != nil {
			f.onOffV(ch, f.OffsetVolts(ch, code)) // detent change moves the offset code → refresh
		}
	}
	return nil
}

// Snapshot returns the detent indices and whether the shadows were ever
// emitted (false = the instrument still runs the inherited boot range).
func (f *FrontEnd) Snapshot() (idx [2]int, emitted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.idx, f.emitted
}

// ---- vertical offset DAC value mapping (spec 06 §5.2 / spec 10 §7.4) ----
//
// The offset DAC is a CS3 (GPMC) register flushed by the engine owner; this is
// only the volts↔code math. The DAC injects a level shift AHEAD of the fine gain
// stage, so the offset is INPUT-REFERRED with a FIXED slope in volts — it does
// NOT scale by V/div. The per-range analog gain already scales the trace with
// V/div; scaling the code by V/div as well double-counts it.
//
//	code = clamp( round( zero − offsetCodesPerVolt·V ) )
//
// Inverting (+V → lower code; 0 V → zero). The coarse attenuator
// (Detents[idx].Atten) sets only the per-tier ±range clamp, not the slope.
// ⚠ On the sensitive ×1 ranges (≤200 mV/div) the offset DAC has NO trace
// authority (bench-dead — the datasheet's ±1.6 V is not reachable through this
// DAC); only the attenuated ranges (≥500 mV/div) actually offset.

// offsetCodesPerVolt is the offset DAC slope in DAC codes per INPUT-VOLT (FIXED,
// not per-division). BENCH-MEASURED = 100: at 1 V/div a −1.6 V offset (160 codes)
// moves the trace 1.6 div (1:1), and the SAME 160 codes gives the correct 0.8 div
// at 2 V/div and 3.2 div at 500 mV/div (the gain scales it). The RE passes' scaled
// `100·(V/VDIV)` form is WRONG — it moved the trace 0.5×/1×/2× across 2 V/1 V/
// 500 mV (double-counting the gain's 1/VDIV). A corrected RE pass may re-pin this.
const offsetCodesPerVolt = 100.0

// offsetTierRangeV is the nominal input-referred offset range for the detent's
// tier, set by the coarse attenuator (spec 06 §5.2.1 / datasheet): ±1.6 V on the
// sensitive ×1 tier, ±40 V on the attenuated ×25 tier. (The ×1 tier is nominal —
// the offset DAC is bench-dead there.)
func offsetTierRangeV(idx int) float64 {
	if idx >= 0 && idx < len(Detents) && Detents[idx].Atten {
		return 40.0
	}
	return 1.6
}

// offsetCode is the offset law (spec 06 §5.2): the excursion is
// offsetCodesPerVolt·V DAC codes (fixed, input-referred), clamped to the per-tier
// ±range about zero, then the final 16-bit rail [0, 0xFFFF].
func offsetCode(zero float64, vd int, volts float64) uint16 {
	code := math.Round(zero - offsetCodesPerVolt*volts)
	clamp := offsetCodesPerVolt * offsetTierRangeV(vd)
	if lo := zero - clamp; code < lo {
		code = lo
	}
	if hi := zero + clamp; code > hi {
		code = hi
	}
	if code < 0 {
		code = 0
	}
	if code > 0xFFFF {
		code = 0xFFFF
	}
	return uint16(code)
}

// offsetZeroFallback is the boot-default 0 V code (0x27ef, spec 10 §4). The
// table-less fallbacks have no detent context, so they assume the boot detent
// (1 V/div, idx 8, attenuated tier).
const offsetZeroFallback = 10223

// OffsetCode is the table-less fallback mapping (front end unavailable): the
// boot detent's 1 V/div slope and ×25 tier clamp about the boot-default zero.
func OffsetCode(ch int, volts float64) uint16 {
	return offsetCode(offsetZeroFallback, BootDetent, volts)
}

// OffsetVolts is the fallback inverse for labels/readback.
func OffsetVolts(ch int, code uint16) float64 {
	return (offsetZeroFallback - float64(code)) / offsetCodesPerVolt
}
