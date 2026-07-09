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

// Coupling modes. On this clone coupling is modelled in SOFTWARE (the display
// path), not driven on the relay — deliberately, because the relay buys nothing
// here: its coupling bit selects a different offset-cal baseline (~36 codes), NOT
// a coupling cap, so there is no hardware high-pass to gain (spec 06 §6); GND
// grounds only with a companion CS3 config-plane write. Both are reproducible
// with the usual disciplines (emit the relay carrying BOTH channels' seeded bytes
// — a gain collapse only happens re-emitting from the un-seeded boot state; stage
// the CS3 companion single-owner), but pointless without a real DC block. So the
// relay stays DC; AC = software DC-removal, GND = a flat ground trace. See
// CoupleDisplay. A physical series cap is the route to a true hardware high-pass.
const (
	CplDC  = 0
	CplAC  = 1
	CplGND = 2
)

// channelByte builds a relay channel byte (spec 06 §4.2): bit0 BWL(1=off,
// full bandwidth), bit2 coarse range, bit3 DC, bit5 always 1, bit7 CH2 address
// bit. Coupling is software-only (see the Coupling constants), so the relay is
// always DC — never the GND (bit1) path, which is unsafe/ineffective here.
func channelByte(idx int, ch2 bool) uint8 {
	b := uint8(0x20 | 0x01 | 0x08) // bit5 | BWL off | bit3 DC
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
	trigSrc int  // relay byte2 source nibble: 0=C1, 1=C2, 2=EXT
	emitted bool // seed-don't-emit: leave the inherited analog range alone

	stage   func(ch int, code uint16)   // offset-DAC stager (engine.SetOffsetDAC)
	onVdiv  func(ch int, vdivV float64) // V/div change hook (engine trigger map)
	offReqV [2]float64                  // requested input-referred offset volts
	offSet  [2]bool                     // whether the user has set an offset
	probe   [2]float64                  // per-channel probe attenuation (display multiplier)
	cpl     [2]int                      // per-channel coupling (CplDC/CplAC/CplGND)
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
	return &FrontEnd{tr: tr, sleep: sleep, tab: tab, idx: [2]int{BootDetent, BootDetent}, probe: [2]float64{1, 1}}
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

// SetCoupling records a channel's input coupling (CplDC/CplAC/CplGND). It never
// touches the relay — coupling is a pure display transform on this clone (see
// the Coupling constants and CoupleDisplay), so it is always safe and takes
// effect on the next served/rendered frame.
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

// OnVdiv wires a V/div-change hook (engine.SetChannelVdiv) so the trigger
// level maps to the right display code. Called immediately with the seeded
// detents so the engine starts consistent.
func (f *FrontEnd) OnVdiv(fn func(ch int, vdivV float64)) {
	f.onVdiv = fn
	if fn != nil {
		f.mu.Lock()
		i0, i1 := f.idx[0], f.idx[1]
		f.mu.Unlock()
		fn(0, Detents[i0].VdivV)
		fn(1, Detents[i1].VdivV)
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

func (f *FrontEnd) relayWord() uint32 {
	b0 := channelByte(f.idx[0], false)
	b1 := channelByte(f.idx[1], true)
	b2 := uint8(0x70 | (f.trigSrc&3)<<2) // DC trigger-coupling nibble
	return uint32(b0) | uint32(b1)<<8 | uint32(b2)<<16
}

// apply emits the exact spec 06 §4.4 sequence: the FULL absolute relay word
// (never read-modify-write), a ~400 µs relay settle, then BOTH gain bytes,
// CH2 first. Caller holds f.mu.
func (f *FrontEnd) applyLocked() error {
	if err := f.tr.WriteRelay(f.relayWord()); err != nil {
		return err
	}
	f.sleep(400 * time.Microsecond)
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
		f.onVdiv(ch, Detents[idx].VdivV) // keep the engine's trigger map current
	}
	if reSet && f.stage != nil {
		f.stage(ch, f.OffsetCode(ch, reReq)) // OffsetCode uses the new detent zero
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
