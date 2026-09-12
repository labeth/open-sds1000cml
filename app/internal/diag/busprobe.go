package diag

import (
	"fmt"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/iface"
)

// BusProbeOptions drives the 27-ball generator (BUSGEN) and the capture
// (BUSCAP). The generator emits a programmable version of the phase-multiplexed
// word the factory image drives on those balls; the capture records what the
// balls actually read, at the same rate, so a reaction that only exists while
// the bus is moving is visible. Static postures cannot see one.
type BusProbeOptions struct {
	Rate    int      `json:"rate"`     // 0 = clk50, 1 = clk100, 2 = 200 MHz, 3 = C2
	Phases  int      `json:"phases"`   // 1..5 phases per rotation
	Pattern []uint32 `json:"pattern"`  // per-phase 27-bit word, ball order BusBalls
	OePhase uint8    `json:"oe_phase"` // bit k drives the group in phase k
	OeInv   bool     `json:"oe_inv"`
	OeMask  uint32   `json:"oe_mask"` // per-ball drive mask, 0 = the whole group (27 ones)
	OneShot bool     `json:"one_shot"`
	K2      int      `json:"k2"` // aux source codes, 0 = this image's legacy source
	D1      int      `json:"d1"`
	D2      int      `json:"d2"`
	G2      int      `json:"g2"`
	K1      int      `json:"k1"`      // 6 = the factory posture: !OE, low while the group is driven
	Trigger bool     `json:"trigger"` // record on the first departure instead of at once
	Confirm int      `json:"confirm"` // samples a departure must hold: 0=1, 1=2, 2=4, 3=8
	Vendor  int      `json:"vendor"`  // repeat the vendor CS1 arm/halt words this many times while armed
	SnapClk int      `json:"snap_clk"`
	Samples int      `json:"samples"` // 0 = all 1024
	Raw     bool     `json:"raw"`     // include every sample, not just the summary
	Pre     int      `json:"pre"`     // phase-advance prescaler: a phase lasts (Pre+1) bus ticks
}

// BallActivity is what one ball did over the recorded window.
type BallActivity struct {
	Ball    string `json:"ball"`
	First   uint8  `json:"first"`
	Last    uint8  `json:"last"`
	High    int    `json:"high"`  // samples read 1
	Edges   int    `json:"edges"` // level changes
	Driven  bool   `json:"driven"`
	Changed bool   `json:"changed"`
}

// BusProbeResult is one generator run seen from the capture side.
type BusProbeResult struct {
	Options   BusProbeOptions `json:"options"`
	Triggered bool            `json:"triggered"`
	Waiting   bool            `json:"waiting"`
	Samples   int             `json:"samples"`
	Balls     []BallActivity  `json:"balls"`
	Singles   []BallActivity  `json:"singles"`
	P6        BallActivity    `json:"p6"`
	Moving    []string        `json:"moving"` // balls with an edge that the generator did not drive
	Bus       []uint32        `json:"bus,omitempty"`
	Aux       []uint16        `json:"aux,omitempty"` // {sgl[3:0], p6} per sample
	Ctl       uint16          `json:"busgen_ctl"`
	Cap       uint16          `json:"buscap_ctl"`
}

// BusProbe programs the generator, arms the capture and reads it back.
func (d *Diag) BusProbe(o BusProbeOptions) (*BusProbeResult, error) {
	if o.Rate < 0 || o.Rate > 3 {
		return nil, fmt.Errorf("diag: bus probe rate %d out of range (0..3)", o.Rate)
	}
	if o.Pre < 0 || o.Pre > 65535 {
		return nil, fmt.Errorf("diag: bus probe pre %d out of range (0..65535)", o.Pre)
	}
	if o.Phases < 1 || o.Phases > 5 {
		return nil, fmt.Errorf("diag: bus probe phases %d out of range (1..5)", o.Phases)
	}
	if len(o.Pattern) > 5 {
		return nil, fmt.Errorf("diag: bus probe pattern has %d phases, at most 5", len(o.Pattern))
	}
	for _, c := range []int{o.K2, o.D1, o.D2, o.G2, o.K1} {
		if c < 0 || c > 7 {
			return nil, fmt.Errorf("diag: bus probe aux code %d out of range (0..7)", c)
		}
	}
	snapClk := o.SnapClk
	if snapClk == 0 {
		snapClk = o.Rate // the natural pairing: one recorded sample per bus tick
	}
	if snapClk < 0 || snapClk > 3 {
		return nil, fmt.Errorf("diag: bus probe snap_clk %d out of range (0..3)", snapClk)
	}
	res := &BusProbeResult{Options: o}

	ctl := uint16(0)
	ctl |= iface.DiagBusgenCtlEnMask
	ctl |= uint16(o.Rate) << iface.DiagBusgenCtlRateShift
	ctl |= uint16(o.Phases-1) << iface.DiagBusgenCtlNphaseShift
	if o.OneShot {
		ctl |= iface.DiagBusgenCtlOneshotMask
	}
	ctl |= (uint16(o.OePhase) << iface.DiagBusgenCtlOePhShift) & iface.DiagBusgenCtlOePhMask
	if o.OeInv {
		ctl |= iface.DiagBusgenCtlOeInvMask
	}
	aux := uint16(o.K2)<<iface.DiagBusgenAuxK2Shift |
		uint16(o.D1)<<iface.DiagBusgenAuxD1Shift |
		uint16(o.D2)<<iface.DiagBusgenAuxD2Shift |
		uint16(o.G2)<<iface.DiagBusgenAuxG2Shift |
		uint16(o.K1)<<iface.DiagBusgenAuxK1Shift
	mask := o.OeMask & 0x07ffffff
	if o.OeMask == 0 {
		mask = 0x07ffffff
	}
	cap := iface.DiagBuscapCtlEnMask
	if o.Trigger {
		cap |= iface.DiagBuscapCtlTrigMask
	}
	if o.Confirm < 0 || o.Confirm > 3 {
		return nil, fmt.Errorf("diag: bus probe confirm %d out of range (0..3)", o.Confirm)
	}
	cap |= uint16(o.Confirm) << iface.DiagBuscapCtlConfirmShift
	res.Ctl = ctl | iface.DiagBusgenCtlRunMask
	res.Cap = cap

	// step 1: load the pattern and the aux sources, generator still stopped.
	err := d.run.Exec(func(b bus.Bus) error {
		for p := 0; p < 5; p++ {
			var w uint32
			if p < len(o.Pattern) {
				w = o.Pattern[p] & 0x07ffffff
			}
			if err := winWrite(b, iface.DiagBusgenPatBase+uint16(2*p), uint16(w)); err != nil {
				return err
			}
			if err := winWrite(b, iface.DiagBusgenPatBase+uint16(2*p+1), uint16(w>>16)); err != nil {
				return err
			}
		}
		if err := winWrite(b, iface.DiagBusgenAux, aux); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagBusgenPre, uint16(o.Pre)); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagBusgenOeLo, uint16(mask)); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagBusgenOeHi, uint16(mask>>16)); err != nil {
			return err
		}
		return winWrite(b, iface.DiagBusgenCtl, ctl) // EN, not RUN
	}, d.timeout)
	if err != nil {
		return nil, err
	}

	// step 2: start the generator, THEN arm the capture, wait, read it back.
	// The record is 2048 bus ticks — 41 us at 50 MHz — and one register write over
	// GPMC can take longer than that, so arming first would finish the record
	// before the generator ever started. That is exactly what the first hardware
	// run showed: 1024 samples of the idle bus.
	var words []uint16
	err = d.run.Exec(func(b bus.Bus) error {
		if err := winWrite(b, iface.DiagBuscapCtl, cap); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagBusgenCtl, ctl|iface.DiagBusgenCtlRunMask); err != nil {
			return err
		}
		c, err := b.Read(bus.PlaneCS1, iface.SelDiagCtrl)
		if err != nil {
			return err
		}
		base := c&^(iface.DiagCtrlSnapClkMask|iface.DiagCtrlSnapArmMask) | uint16(snapClk)<<iface.DiagCtrlSnapClkShift
		if err := b.Write(bus.PlaneCS1, iface.SelDiagCtrl, base|iface.DiagCtrlSnapArmMask); err != nil {
			return err
		}
		if err := b.Write(bus.PlaneCS1, iface.SelDiagCtrl, base); err != nil {
			return err
		}
		// The MAX V snoops the GPMC bus, so the words the vendor firmware writes are a
		// stimulus this probe can apply while the trigger is armed. VendorSequence only
		// ever compared a before and an after; a departure trigger sees what happens
		// DURING them. The words alias onto OPCODE (ignored) and FILL (read-only) on our
		// map, so they change nothing on our side.
		for n := 0; n < o.Vendor; n++ {
			for _, w := range vendorWords {
				if err := b.RawWrite(w.sel, w.val); err != nil {
					return err
				}
			}
		}
		var rem uint16
		for i := 0; i < 400; i++ {
			rem, _ = b.Read(bus.PlaneCS1, iface.SelSnapRemain)
			if rem&iface.SnapRemainReadyMask != 0 {
				break
			}
			d.sleep(time.Millisecond)
		}
		st, _ := winRead(b, iface.DiagBuscapCtl)
		res.Triggered = st&iface.DiagBuscapCtlTriggeredMask != 0
		res.Waiting = st&iface.DiagBuscapCtlWaitingMask != 0
		if rem&iface.SnapRemainReadyMask == 0 {
			if res.Waiting {
				return nil // armed, never departed: that is the answer, not an error
			}
			return fmt.Errorf("diag: bus capture never became ready (SNAP_REMAIN %#04x)", rem)
		}
		n := int(rem & iface.SnapRemainRemainMask)
		words = make([]uint16, n)
		b.PopWords(iface.SelSnapPop, words, n)
		return nil
	}, d.timeout)
	if err != nil {
		return nil, err
	}

	// step 3: stop the generator and release the balls.
	if err := d.run.Exec(func(b bus.Bus) error {
		if err := winWrite(b, iface.DiagBusgenCtl, 0); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagBuscapCtl, 0); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagBusgenOeLo, 0xffff); err != nil {
			return err
		}
		return winWrite(b, iface.DiagBusgenOeHi, 0x07ff)
	}, d.timeout); err != nil {
		return nil, err
	}

	res.decode(words, o)
	return res, nil
}

// decode turns the 2-words-per-sample stream into per-ball activity.
func (r *BusProbeResult) decode(words []uint16, o BusProbeOptions) {
	n := len(words) / 2
	if o.Samples > 0 && o.Samples < n {
		n = o.Samples
	}
	r.Samples = n
	busv := make([]uint32, n)
	auxv := make([]uint16, n)
	for i := 0; i < n; i++ {
		lo := uint32(words[2*i])
		hi := uint32(words[2*i+1])
		busv[i] = lo | (hi&0x07ff)<<16
		auxv[i] = uint16(hi>>11) & 0x1f // {sgl[3:0], p6}
	}
	// which balls the generator drove: the OE is a group enable, so a ball is
	// "driven" whenever any selected phase was enabled.
	anyPhase := o.OePhase != 0 || o.OeInv
	mask := o.OeMask & 0x07ffffff
	if o.OeMask == 0 {
		mask = 0x07ffffff
	}
	track := func(name string, bit func(int) uint8, isDriven bool) BallActivity {
		a := BallActivity{Ball: name, Driven: isDriven}
		if n == 0 {
			return a
		}
		a.First = bit(0)
		prev := a.First
		for i := 0; i < n; i++ {
			v := bit(i)
			if v == 1 {
				a.High++
			}
			if i > 0 && v != prev {
				a.Edges++
			}
			prev = v
		}
		a.Last = prev
		a.Changed = a.Edges > 0
		return a
	}
	for k, ball := range BusBalls {
		k := k
		drivenK := anyPhase && mask&(1<<uint(k)) != 0
		a := track(ball, func(i int) uint8 { return uint8((busv[i] >> uint(k)) & 1) }, drivenK)
		r.Balls = append(r.Balls, a)
		if a.Changed && !drivenK {
			r.Moving = append(r.Moving, ball)
		}
	}
	for k := 0; k < 4; k++ {
		k := k
		r.Singles = append(r.Singles, track(SingleBalls[k], func(i int) uint8 { return uint8((auxv[i] >> uint(1+k)) & 1) }, false))
	}
	r.P6 = track("P6", func(i int) uint8 { return uint8(auxv[i] & 1) }, false)
	if o.Raw {
		r.Bus = busv
		r.Aux = auxv
	}
}

// CS3Cell is one MAX V selector as read twice.
type CS3Cell struct {
	Sel    uint16 `json:"sel"`
	A      uint16 `json:"a"`
	B      uint16 `json:"b"`
	Stable bool   `json:"stable"`
}

// CS3Census reads the MAX V's register plane. READ ONLY: CS3 0x07 is the
// configuration port and is never written here, and nothing else is written
// either. The MAX V is the chip that owns the SRAM command pins
// (the acq2 analysis branch), so its register space is
// the one channel we have to the thing that can issue a write. Each selector is
// read twice so a floating or self-changing cell is distinguishable from a
// register that simply holds a value.
func (d *Diag) CS3Census(n int) ([]CS3Cell, error) {
	if n <= 0 || n > 0x100 {
		n = 0x80
	}
	out := make([]CS3Cell, 0, n)
	err := d.run.Exec(func(b bus.Bus) error {
		for sel := 0; sel < n; sel++ {
			a, err := b.Read(bus.PlaneCS3, uint16(sel))
			if err != nil {
				return fmt.Errorf("CS3 %#04x: %w", sel, err)
			}
			bb, err := b.Read(bus.PlaneCS3, uint16(sel))
			if err != nil {
				return fmt.Errorf("CS3 %#04x (second read): %w", sel, err)
			}
			out = append(out, CS3Cell{Sel: uint16(sel), A: a, B: bb, Stable: a == bb})
		}
		return nil
	}, d.timeout)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CS3PokeOptions drives one MAX V CS3 selector and reports what moved.
//
// The CS3 plane reads back almost nothing: of 256 selectors only 0x07 (the
// configuration port), 0x08 and 0x0f are non-zero, and everything else is 0.
// For a CPLD that is the signature of WRITE-ONLY control registers, so the only
// way to find the SRAM command strobes the MAX V drives is to write and observe.
//
// Safety: bus.Writable already refuses CS3ConfigPort (0x07) — reconfiguring the
// Cyclone mid-run is not a probe. Everything written here is a volatile CPLD
// register, never its configuration flash, so a mains cycle restores the board;
// nothing here can brick it. Restore puts the previous value back after Dwell.
type CS3PokeOptions struct {
	Sel     uint16 `json:"sel"`
	Val     uint16 `json:"val"`
	DwellMs int    `json:"dwell_ms"`
	Restore bool   `json:"restore"` // write the pre-poke value back afterwards
	Census  int    `json:"census"`  // re-read this many CS3 cells before and after
}

// CS3Poke writes one CS3 register and reports the CS3 cells that changed.
func (d *Diag) CS3Poke(o CS3PokeOptions) (map[string]any, error) {
	if o.Census <= 0 || o.Census > 0x100 {
		o.Census = 0x20
	}
	if o.DwellMs <= 0 || o.DwellMs > 2000 {
		o.DwellMs = 20
	}
	out := map[string]any{"sel": o.Sel, "val": o.Val, "dwell_ms": o.DwellMs, "restore": o.Restore}
	err := d.run.Exec(func(b bus.Bus) error {
		before := make([]uint16, o.Census)
		for i := range before {
			v, err := b.Read(bus.PlaneCS3, uint16(i))
			if err != nil {
				return fmt.Errorf("CS3 pre-read %#04x: %w", i, err)
			}
			before[i] = v
		}
		prev := before[0]
		if int(o.Sel) < len(before) {
			prev = before[o.Sel]
		} else {
			v, err := b.Read(bus.PlaneCS3, o.Sel)
			if err != nil {
				return err
			}
			prev = v
		}
		out["prev"] = prev
		if err := b.Write(bus.PlaneCS3, o.Sel, o.Val); err != nil {
			return fmt.Errorf("CS3 write %#04x=%#04x: %w", o.Sel, o.Val, err)
		}
		time.Sleep(time.Duration(o.DwellMs) * time.Millisecond)
		after := make([]uint16, o.Census)
		for i := range after {
			v, err := b.Read(bus.PlaneCS3, uint16(i))
			if err != nil {
				return fmt.Errorf("CS3 post-read %#04x: %w", i, err)
			}
			after[i] = v
		}
		changed := []map[string]any{}
		for i := range before {
			if before[i] != after[i] {
				changed = append(changed, map[string]any{"sel": i, "before": before[i], "after": after[i]})
			}
		}
		out["changed"] = changed
		out["n_changed"] = len(changed)
		if o.Restore {
			if err := b.Write(bus.PlaneCS3, o.Sel, prev); err != nil {
				return fmt.Errorf("CS3 restore %#04x=%#04x: %w", o.Sel, prev, err)
			}
			out["restored_to"] = prev
		}
		return nil
	}, d.timeout)
	if err != nil {
		return nil, err
	}
	return out, nil
}
