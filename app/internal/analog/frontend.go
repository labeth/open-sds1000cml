package analog

import (
	"fmt"
	"math"
	"os"
	"strconv"
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

// channelByte builds a relay channel byte (spec 06 §4.2): bit0 BWL(1=off,
// full bandwidth), bit1 GND, bit2 coarse range, bit3 DC, bit5 always 1,
// bit7 CH2 address bit. v1 ships DC coupling, BWL off, no GND — inherited
// boot values (coupling changes are deferred, spec 06 §7 verdict).
func channelByte(idx int, ch2 bool) uint8 {
	b := uint8(0x20 | 0x01 | 0x08) // bit5 | BWL-off | DC
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
	return &FrontEnd{tr: tr, sleep: sleep, tab: tab, idx: [2]int{BootDetent, BootDetent}}
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

// offsetZero is the calibrated 0 V offset-DAC code for the channel's
// CURRENT detent (per-(ch,vd) cal record; fallback boot default 10223).
func (f *FrontEnd) offsetZero(ch int) float64 {
	f.mu.Lock()
	vd := f.idx[ch&1]
	f.mu.Unlock()
	return float64(f.tab.Rec[ch&1][vd].Zero)
}

// OffsetCode converts input-referred volts to a DAC code using the
// calibrated zero: code = zero − V·K, clamped to the linear region.
func (f *FrontEnd) OffsetCode(ch int, volts float64) uint16 {
	return clampOffset(f.offsetZero(ch) - volts*offsetK)
}

// OffsetVolts is the inverse for labels/readback.
func (f *FrontEnd) OffsetVolts(ch int, code uint16) float64 {
	return (f.offsetZero(ch) - float64(code)) / offsetK
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

// ---- vertical offset DAC value mapping (spec 06 §5) ----
//
// The offset DAC itself is on CS3 (GPMC) and is flushed by the engine owner;
// this is only the volts↔code math. The transfer is inverting and
// input-referred with a FIXED slope — never scale K by V/div.

var offsetK = func() float64 {
	if v := os.Getenv("SCOPE_OFFSET_K"); v != "" {
		if k, err := strconv.ParseFloat(v, 64); err == nil && k > 0 {
			return k
		}
	}
	return 262 // codes per input-referred volt
}()

// offsetZeroFallback is the boot-default 0 V code (0x27ef, spec 10 §4) used
// only when no FrontEnd/cal table exists.
const offsetZeroFallback = 10223

// Offset DAC linear region clamp (spec 09 §4).
const (
	OffsetCodeMin = 9600
	OffsetCodeMax = 11600
)

func clampOffset(c float64) uint16 {
	c = math.Round(c)
	if c < OffsetCodeMin {
		c = OffsetCodeMin
	}
	if c > OffsetCodeMax {
		c = OffsetCodeMax
	}
	return uint16(c)
}

// OffsetCode is the table-less fallback mapping (front end unavailable).
func OffsetCode(ch int, volts float64) uint16 {
	return clampOffset(offsetZeroFallback - volts*offsetK)
}

// OffsetVolts is the fallback inverse for labels/readback.
func OffsetVolts(ch int, code uint16) float64 {
	return (offsetZeroFallback - float64(code)) / offsetK
}
