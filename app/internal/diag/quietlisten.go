package diag

import (
	"fmt"
	"strings"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/iface"
)

// QuietOptions listens to the 80 registered ADC lanes with our own converters
// switched OFF.
//
// Why this exists. [INFERRED, not decoded] The 36-bit DQ bus of the SyncBurst
// SRAM and the ADC's output bus are believed to be the same wires. Nothing on
// this branch has decoded that: 01-CONTRACT s4 records that the prior called the
// five single balls "DQ" and then RETRACTED the label, and no claim anywhere puts
// the SRAM's data pins on our 80 lanes. The argument is indirect. The cone decode
// found that no bus cone contains any of the 80 registered lane inputs, so the
// Cyclone never forwards lane data anywhere -- it cannot be the path by which
// samples reach the SRAM. The vendor demonstrably stores 2 Mpts. So the samples
// must travel ADC -> SRAM directly, and our lanes tap that same net. If that
// inference is wrong, this probe can never hear the SRAM no matter what it does,
// and every null it produces is uninformative rather than evidence.
//
// Every witness surface this branch has built was
// blind for one reason -- the ADC drives those wires continuously at 12.5..200
// MHz, so anything the SRAM might do is buried under our own traffic. We could
// only ever ask "did something change?", never "what value is there?", and the
// change test drowned.
//
// We own the encode clocks. ACQ_CTRL.ENC_EN gates all five encode pairs and
// PAIR_EN gates them individually; with both clear the converters are static and
// stop driving new words. The lanes then settle to a constant, and the lane
// monitors' ever-1/ever-0 flags (cleared by DIAG_CTRL.LANE_GATE_RST) say so
// per signal. Once they are quiet, ANY lane that moves afterwards is not ours.
// On this board there is exactly one other thing that can drive those wires.
//
// That converts the ADC bus from a blind surface into a read-back detector, and
// with the SRAM primed to a known nibble by the vendor firmware (coldstart.sh
// PRIME) it is a VALUE test: a lane that moves must move toward a predicted
// pattern. This is the only reader we have. The vendor firmware is the other
// one, and it is out of reach: restore-factory dies at "open keyboard interrupt"
// because the agent still holds the boot fds, this unit's warm reboot is a no-op
// (2026-09-06, agent uptime unbroken across reboot --confirm), and the only reset
// that returns the vendor is a mains cycle -- which destroys the contents we
// would be reading. See the acq2 analysis branch
//
// EncOn is the control rung: the identical measurement with the converters left
// running. It must show the lanes loud. Without it a null means nothing, because
// "the lanes were quiet" and "the lane monitors are broken" look the same.
type QuietOptions struct {
	DwellMs int    `json:"dwell_ms"` // listen time after the gate reset (0..5000)
	PairEn  uint16 `json:"pair_en"`  // PAIR_EN bits to keep on (0 = all five pairs off)
	EncOn   bool   `json:"enc_on"`   // control rung: leave ENC_EN set
	Rate    int    `json:"rate"`     // ENC_RATE to program (only meaningful with EncOn)

	// Break lists ADC control lines to flip AWAY from the proven recipe, by name:
	// "F1" "L4" "T2" "T7" (recipe 1, flipped to 0) and "G1" "G2" "K1" (recipe 0,
	// flipped to 1). Stopping the encode clock only makes the converters static --
	// their output drivers can still own the DQ bus, and a bus we still own is a
	// bus the SRAM cannot drive. These seven static lines are the ADC's control
	// surface (01-CONTRACT s2 adc_ctl_hi[0..3] = F1 L4 T2 T7, adc_ctl_lo[0..2] =
	// G1 K1 G2, all named from owned-fpga c8ec66d, the recipe that made the
	// converters work). None has ever been moved off its recipe value on this
	// branch. If any of them is an output-enable or a power-down, flipping it is
	// what releases DQ -- and a released bus is the precondition for hearing the
	// SRAM at all.
	Break []string `json:"break"`

	// Wide scans the 27 bus balls, the 5 singles and P6/A2/B1/K2 as well as the
	// 80 ADC lanes. SRAM_ENGINE.md s8 proves the Cyclone cannot be the SRAM's
	// CONTROLLER on these pins (45 in scope against 67 required, and no address
	// counter in the image reaches a pad), but 27 bus balls + 5 singles = 32,
	// which is exactly a x36 SyncBurst part used 32 bits wide with the four
	// parity lines unused. [INFERRED] the Cyclone drives DQ only and the MAX V
	// owns address, clock and control. If that is right, the SRAM's data is
	// visible on THESE balls when we release them -- not on the ADC lanes.
	Wide bool `json:"wide"`

	// Gen runs the bus generator during the listening window with the drive mask
	// and per-phase OE both ZERO -- it emits no data and never owns the group, so
	// the 27 balls stay released the whole time. Its only purpose is to make the
	// clock-shaped balls carry something: BUSGEN_AUX codes 3 (forward busclk) and
	// 4 (busclk/2) need a running generator.
	//
	// G2 is the reason. SRAM_ENGINE.md s10.1 reads D1, D2, G2 and K2 as DDIO clock
	// FORWARDS (both data lines silent, one edge inverted). D1 and D2 both reach
	// the MAX V -- that is the P6 mirror -- and K2 is the 50 MHz forward, but G2's
	// destination (line 2 of tile X0Y18) has never been identified, and the ADC
	// recipe pins it at 0. CTRL_G2 was dropped from the crank engine on timing
	// (-0.735 ns), so G2 has never been toggled as a clock on this branch. A
	// pipelined SyncBurst SRAM with no clock can never present a word, which would
	// explain every null this branch has recorded; and one left mid-burst with ADV#
	// asserted advances and drives DQ on each clock edge.
	Gen     bool `json:"gen"`
	GenRate int  `json:"gen_rate"` // BUSGEN_CTL.RATE: 0 clk50, 1 clk100, 2 cap_clk, 3 clk
	G2Code  int  `json:"g2_code"`  // BUSGEN_AUX.G2 source, 0..7 (3 = forward busclk)
	D1Code  int  `json:"d1_code"`  // BUSGEN_AUX.D1 source
	K2Code  int  `json:"k2_code"`  // BUSGEN_AUX.K2 source

	// Clock software-toggles one DIAG_CTRL-reachable ball ("F1" "G1" "G2" "K1")
	// during the listening window: Cycles square-wave periods, each half HalfMs
	// milliseconds long, written over GPMC from this goroutine.
	//
	// Why software rather than the fabric. Every clock this branch has emitted
	// came from the fabric and was fast -- the aux forwards run at busclk (20 ns)
	// and the SRCLK emitter tops out well short of a millisecond. But the only
	// far-end path we have ever characterised passes a pulse ONLY if it is wider
	// than about 9.4 us (2026-09-06-minimum-width.md), rate-independent. A GPMC
	// write is microseconds and a sleep is milliseconds, so software toggling
	// lands naturally in the band the far end actually responds to, and it needs
	// no RTL and no timing closure -- which is what killed CTRL_G2 in the fabric.
	//
	// It also sidesteps the readback problem: G2, D2 and K2 are DDIO outputs whose
	// pads cannot be read back (Quartus 15873, and mon's K2 bit is the DIAG_CTRL
	// enable level, not the pin), so a fabric-emitted clock on them is
	// unfalsifiable from inside. Here the level we write IS the level we assert.
	Clock  string `json:"clock"`
	Cycles int    `json:"cycles"`
	HalfMs int    `json:"half_ms"`

	// Drive makes the generator actually own the 27 balls instead of only carrying the
	// clock-shaped ones: DriveMask is the per-ball drive mask (27 bits, ball order J6 K5 L1 L2
	// L3 L4 N1 N2 P1 P2 R1 M6 N6 N3 N5 P3 R3 R4 R5 R6 R7 T2 T3 T4 T5 T6 T7), OePhase the
	// per-phase enable, Pattern the word driven in each of the five phases, and Pre the
	// phase-advance prescaler.
	//
	// Why, now. MAXV-INTERFACE.md decodes the 32-ball group as a synchronous transmit port:
	// K2 the forwarded clock, K1 the enable (K1 = !Q, so LOW while the window is open), G1 a
	// qualifier, and the data rotating mod 5. Our ADC recipe pins K1 at 0 permanently -- which
	// in that language asserts "window open" forever, so the far end has never seen the K1
	// TRANSITION at all. The one dynamic K1 sweep this branch ran (2026-09-05-k1-enable.md)
	// toggled it at bus-clock rate, and the only far-end path we have ever characterised
	// passes nothing narrower than ~9.4 us. Software toggling plus BUSGEN_PRE puts both the
	// enable edge and the data rotation in that band for the first time.
	// Cs3Sel/Cs3Vals poke the MAX V *inside* the listening window. The MAX V drives all six
	// SRAM command strobes and nothing else on this board can; if a poke makes it assert the
	// SRAM's output enable, the SRAM drives the 32-ball DQ group and the Cyclone can see it on
	// bus_i50. With Gen false the fabric drives nothing, so the group floats to all-ones
	// (diag.v's rest50 = &bus_i50) and any bit reading 0 means something else drove it. That is
	// the project's open question -- "is there any ball that can show us SRAM data at all" --
	// asked in the one direction never tried.
	Cs3Sel  uint16   `json:"cs3_sel"`
	Cs3Vals []uint16 `json:"cs3_vals"`
	Cs3Reps int      `json:"cs3_reps"`

	DriveMask uint32   `json:"drive_mask"`
	OePhase   int      `json:"oe_phase"`
	Pattern   []uint32 `json:"pattern"`
	Pre       int      `json:"pre"`
}

// QuietLane is one ADC lane over the listening window.
type QuietLane struct {
	Idx   int    `json:"idx"`
	Name  string `json:"name"`
	Tog   int    `json:"tog"`
	Level int    `json:"level"`
	Ever1 bool   `json:"ever1"`
	Ever0 bool   `json:"ever0"`
}

// QuietResult is one listening window.
type QuietResult struct {
	Options   QuietOptions   `json:"options"`
	Broke     []string       `json:"broke"`      // control lines actually flipped
	CtrlSaved uint16         `json:"ctrl_saved"` // DIAG_CTRL before
	CtrlSet   uint16         `json:"ctrl_set"`   // DIAG_CTRL programmed
	HoldSaved uint16         `json:"hold_saved"` // ADC_HOLD before
	HoldSet   uint16         `json:"hold_set"`   // ADC_HOLD programmed
	AcqSaved  uint16         `json:"acq_saved"`  // ACQ_CTRL as the engine left it
	AcqSet    uint16         `json:"acq_set"`    // what we programmed
	AcqRead   uint16         `json:"acq_read"`   // read back, to prove the write landed
	Moved     []QuietLane    `json:"moved"`      // only the lanes that saw both levels
	NMoved    int            `json:"n_moved"`    // of 80
	NToggling int            `json:"n_toggling"` // lanes with a nonzero windowed toggle count
	Level     []int          `json:"level"`      // the settled level of all 80 lanes, in index order
	Tog       []int          `json:"tog"`        // the windowed toggle count, same order
	Names     []string       `json:"names"`      // the signal name at each position
	GenCtl    uint16         `json:"gen_ctl"`
	GenAux    uint16         `json:"gen_aux"`
	BusRD     uint32         `json:"bus_rd"`    // the 27 balls read back DURING the window
	Cs3BusRD  []uint32       `json:"cs3_bus_rd"` // one bus sample after each CS3 poke
	CtrlRead  uint16         `json:"ctrl_read"` // DIAG_CTRL read back after programming
	Nibbles   map[string]int `json:"nibbles"`   // top-nibble histogram over the 5 pairs, if quiet
	Verdict   string         `json:"verdict"`
}

// broke renders the break list for a verdict line.
func broke(b []string) string {
	if len(b) == 0 {
		return "the ADC recipe untouched"
	}
	return "the recipe broken at " + strings.Join(b, "+")
}

// QuietListen freezes the converters, clears the lane gate, waits, and reports
// which of the 80 ADC lanes moved. Everything happens inside ONE Exec: the
// engine rewrites ACQ_CTRL on every arm (engine_bus.go acqCtrlWord), so a
// freeze that spans two Exec calls would be undone between them.
func (d *Diag) QuietListen(o QuietOptions) (*QuietResult, error) {
	if o.DwellMs < 0 || o.DwellMs > 5000 {
		return nil, fmt.Errorf("diag: quiet dwell_ms %d out of range (0..5000)", o.DwellMs)
	}
	if o.PairEn&^(iface.AcqCtrlPairEnMask>>iface.AcqCtrlPairEnShift) != 0 {
		return nil, fmt.Errorf("diag: quiet pair_en %#x has bits outside the five pairs", o.PairEn)
	}
	if o.Rate < 0 || o.Rate > 3 {
		return nil, fmt.Errorf("diag: quiet rate %d out of range (0..3)", o.Rate)
	}
	res := &QuietResult{Options: o, Nibbles: map[string]int{}}

	set := o.PairEn << iface.AcqCtrlPairEnShift
	if o.EncOn {
		set |= iface.AcqCtrlEncEnMask | uint16(o.Rate)<<iface.AcqCtrlEncRateShift
	}
	res.AcqSet = set

	// The recipe: adc_ctl_hi (F1 L4 T2 T7) are held 1, adc_ctl_lo (G1 G2 K1) are
	// held 0. "Breaking" a line means driving it to the other value. L4/T2/T7 are
	// members of the 27-ball bus held static by ADC_HOLD, so breaking one clears
	// its ADC_HOLD bit -- which returns it to the bus, where the default posture
	// is tri-stated. That is a RELEASE, not a drive to 0, and it is the honest
	// thing to do: we do not know the far end well enough to fight it.
	var ctrlSet, ctrlClr, holdClr uint16
	for _, name := range o.Break {
		switch name {
		case "F1", "f1":
			ctrlClr |= iface.DiagCtrlF1Mask // recipe 1 -> 0
		case "G1", "g1":
			ctrlSet |= iface.DiagCtrlG1Mask // recipe 0 -> 1
		case "G2", "g2":
			ctrlSet |= iface.DiagCtrlG2Mask
		case "K1", "k1":
			ctrlSet |= iface.DiagCtrlK1Mask
		case "L4", "l4":
			holdClr |= iface.DiagAdcHoldL4Mask
		case "T2", "t2":
			holdClr |= iface.DiagAdcHoldT2Mask
		case "T7", "t7":
			holdClr |= iface.DiagAdcHoldT7Mask
		default:
			return nil, fmt.Errorf("diag: quiet break %q is not an ADC control line (F1 L4 T2 T7 G1 G2 K1)", name)
		}
		res.Broke = append(res.Broke, name)
	}

	var clockMask uint16
	switch o.Clock {
	case "":
	case "F1", "f1":
		clockMask = iface.DiagCtrlF1Mask
	case "G1", "g1":
		clockMask = iface.DiagCtrlG1Mask
	case "G2", "g2":
		clockMask = iface.DiagCtrlG2Mask
	case "K1", "k1":
		clockMask = iface.DiagCtrlK1Mask
	default:
		return nil, fmt.Errorf("diag: quiet clock %q must be F1, G1, G2 or K1", o.Clock)
	}
	if clockMask != 0 {
		if o.Cycles < 1 || o.Cycles > 2000 {
			return nil, fmt.Errorf("diag: quiet cycles %d out of range (1..2000)", o.Cycles)
		}
		if o.HalfMs < 1 || o.HalfMs > 50 {
			return nil, fmt.Errorf("diag: quiet half_ms %d out of range (1..50)", o.HalfMs)
		}
	}

	err := d.run.Exec(func(b bus.Bus) error {
		saved, err := b.Read(bus.PlaneCS1, iface.SelAcqCtrl)
		if err != nil {
			return err
		}
		res.AcqSaved = saved
		// Whatever happens below, the engine gets its acquisition back.
		defer func() { _ = b.Write(bus.PlaneCS1, iface.SelAcqCtrl, saved) }()

		if err := b.Write(bus.PlaneCS1, iface.SelAcqCtrl, set); err != nil {
			return err
		}
		got, err := b.Read(bus.PlaneCS1, iface.SelAcqCtrl)
		if err != nil {
			return err
		}
		res.AcqRead = got

		dc0, err := b.Read(bus.PlaneCS1, iface.SelDiagCtrl)
		if err != nil {
			return err
		}
		res.CtrlSaved = dc0
		hold0, err := winRead(b, iface.DiagAdcHold)
		if err != nil {
			return err
		}
		res.HoldSaved = hold0
		// Whatever happens below, the ADC recipe goes back exactly as we found it.
		defer func() {
			_ = winWrite(b, iface.DiagAdcHold, hold0)
			_ = b.Write(bus.PlaneCS1, iface.SelDiagCtrl, dc0)
		}()
		res.CtrlSet = dc0&^ctrlClr | ctrlSet
		res.HoldSet = hold0 &^ holdClr
		if res.HoldSet != hold0 {
			if err := winWrite(b, iface.DiagAdcHold, res.HoldSet); err != nil {
				return err
			}
		}
		if res.CtrlSet != dc0 {
			if err := b.Write(bus.PlaneCS1, iface.SelDiagCtrl, res.CtrlSet); err != nil {
				return err
			}
		}

		if o.Gen {
			aux0, _ := winRead(b, iface.DiagBusgenAux)
			ctl0, _ := winRead(b, iface.DiagBusgenCtl)
			oel0, _ := winRead(b, iface.DiagBusgenOeLo)
			oeh0, _ := winRead(b, iface.DiagBusgenOeHi)
			defer func() {
				_ = winWrite(b, iface.DiagBusgenCtl, 0)
				_ = winWrite(b, iface.DiagBusgenAux, aux0)
				_ = winWrite(b, iface.DiagBusgenOeLo, oel0)
				_ = winWrite(b, iface.DiagBusgenOeHi, oeh0)
				_ = winWrite(b, iface.DiagBusgenCtl, ctl0&^iface.DiagBusgenCtlRunMask)
			}()
			pre0, _ := winRead(b, iface.DiagBusgenPre)
			defer func() { _ = winWrite(b, iface.DiagBusgenPre, pre0) }()
			if err := winWrite(b, iface.DiagBusgenOeLo, uint16(o.DriveMask)); err != nil {
				return err
			}
			if err := winWrite(b, iface.DiagBusgenOeHi, uint16(o.DriveMask>>16)); err != nil {
				return err
			}
			if err := winWrite(b, iface.DiagBusgenPre, uint16(o.Pre)); err != nil {
				return err
			}
			for i := 0; i < iface.DiagBusgenPatCount && i < 2*len(o.Pattern); i++ {
				w := o.Pattern[i/2]
				if i%2 == 1 {
					w >>= 16
				}
				if err := winWrite(b, iface.DiagBusgenPatBase+uint16(i), uint16(w)); err != nil {
					return err
				}
			}
			aux := uint16(o.K2Code)<<iface.DiagBusgenAuxK2Shift |
				uint16(o.D1Code)<<iface.DiagBusgenAuxD1Shift |
				uint16(o.G2Code)<<iface.DiagBusgenAuxG2Shift
			if err := winWrite(b, iface.DiagBusgenAux, aux); err != nil {
				return err
			}
			ctl := iface.DiagBusgenCtlEnMask |
				uint16(o.GenRate)<<iface.DiagBusgenCtlRateShift |
				uint16(4)<<iface.DiagBusgenCtlNphaseShift | // the factory's 5 phases
				uint16(o.OePhase)<<iface.DiagBusgenCtlOePhShift |
				iface.DiagBusgenCtlRunMask
			if err := winWrite(b, iface.DiagBusgenCtl, ctl); err != nil {
				return err
			}
			res.GenCtl = ctl
			res.GenAux = aux
		}

		// Let the converters coast to a stop before the gate opens, or the last
		// live words land inside our own listening window and every lane "moves".
		d.sleep(2 * time.Millisecond)

		dc, err := b.Read(bus.PlaneCS1, iface.SelDiagCtrl)
		if err != nil {
			return err
		}
		if err := b.Write(bus.PlaneCS1, iface.SelDiagCtrl, dc|iface.DiagCtrlLaneGateRstMask); err != nil {
			return err
		}
		if err := b.Write(bus.PlaneCS1, iface.SelDiagCtrl, dc&^iface.DiagCtrlLaneGateRstMask); err != nil {
			return err
		}

		if clockMask != 0 {
			for i := 0; i < o.Cycles; i++ {
				if err := b.Write(bus.PlaneCS1, iface.SelDiagCtrl, res.CtrlSet|clockMask); err != nil {
					return err
				}
				d.sleep(time.Duration(o.HalfMs) * time.Millisecond)
				if err := b.Write(bus.PlaneCS1, iface.SelDiagCtrl, res.CtrlSet&^clockMask); err != nil {
					return err
				}
				d.sleep(time.Duration(o.HalfMs) * time.Millisecond)
			}
		}
		// Poke the MAX V inside the window, sampling the bus after every write so a response
		// that only lasts as long as the strobe is not missed by a single end-of-dwell read.
		if len(o.Cs3Vals) > 0 {
			reps := o.Cs3Reps
			if reps <= 0 || reps > 64 {
				reps = 1
			}
			seen := make([]uint32, 0, len(o.Cs3Vals)*reps)
			for r := 0; r < reps; r++ {
				for _, v := range o.Cs3Vals {
					if err := b.Write(bus.PlaneCS3, o.Cs3Sel, v); err != nil {
						return fmt.Errorf("cs3 poke %#04x=%#04x: %w", o.Cs3Sel, v, err)
					}
					lo, _ := winRead(b, iface.DiagBusRdLo)
					hi, _ := winRead(b, iface.DiagBusRdHi)
					seen = append(seen, uint32(lo)|uint32(hi&0x07ff)<<16)
				}
			}
			res.Cs3BusRD = seen
		}
		d.sleep(time.Duration(o.DwellMs) * time.Millisecond)
		{
			lo, _ := winRead(b, iface.DiagBusRdLo)
			hi, _ := winRead(b, iface.DiagBusRdHi)
			res.BusRD = uint32(lo) | uint32(hi&0x07ff)<<16
			res.CtrlRead, _ = b.Read(bus.PlaneCS1, iface.SelDiagCtrl)
		}

		n := 80
		idxs := make([]int, 0, 124)
		for i := 0; i < 80; i++ {
			idxs = append(idxs, i)
		}
		if o.Wide {
			for i := 0; i < 0x7e; i++ {
				if i >= 0x50 && LaneName(i) != "" {
					idxs = append(idxs, i)
				}
			}
			n = len(idxs)
		}
		res.Level = make([]int, n)
		res.Tog = make([]int, n)
		res.Names = make([]string, n)
		for pos, idx := range idxs {
			if err := winWrite(b, iface.DiagLaneIdx, uint16(idx)); err != nil {
				return err
			}
			// LANE_TOG is windowed over 65536 bus-clock cycles; a reading is
			// only valid two windows after the index moves (Census, same wait).
			d.sleep(laneTogSettle)
			tog, _ := winRead(b, iface.DiagLaneTog)
			lvl, _ := winRead(b, iface.DiagLaneLvl)
			l := QuietLane{Idx: idx, Name: LaneName(idx), Tog: int(tog),
				Level: int(lvl & iface.DiagLaneLvlLevelMask),
				Ever1: lvl&iface.DiagLaneLvlEver1Mask != 0,
				Ever0: lvl&iface.DiagLaneLvlEver0Mask != 0}
			res.Level[pos] = l.Level
			res.Tog[pos] = l.Tog
			res.Names[pos] = l.Name
			if l.Tog > 0 {
				res.NToggling++
			}
			if l.Ever0 && l.Ever1 {
				res.NMoved++
				res.Moved = append(res.Moved, l)
			}
		}
		return nil
	}, d.timeout+time.Duration(o.DwellMs)*time.Millisecond+
		time.Duration(2*o.Cycles*o.HalfMs)*time.Millisecond+800*time.Millisecond)
	if err != nil {
		return nil, err
	}

	switch {
	case res.AcqRead != res.AcqSet:
		res.Verdict = fmt.Sprintf("ACQ_CTRL did not take: wrote %#04x read %#04x — measurement void", res.AcqSet, res.AcqRead)
	case o.EncOn && res.NMoved == 0:
		res.Verdict = "CONTROL FAILED: the converters were left running and no lane moved — the lane monitors are not reporting, so any quiet result is meaningless"
	case o.EncOn:
		res.Verdict = fmt.Sprintf("control rung OK: %d/80 lanes moved with the encode on", res.NMoved)
	case res.NMoved == 0:
		res.Verdict = fmt.Sprintf("the ADC bus is QUIET (0/80 lanes moved) with the converters static and %s — a usable listening surface", broke(res.Broke))
	default:
		// A moving lane with the encode off is NOT yet evidence of the SRAM. With
		// the recipe broken the ADC may have released the pin, and these balls carry
		// no pull-up and no bus-hold (default.qsf s1), so an undriven input floats
		// and a floating input toggles. Live data, float noise and a real external
		// driver are told apart by the toggle counts and by whether the settled
		// levels match the primed nibble — not by the fact that something moved.
		res.Verdict = fmt.Sprintf("%d/80 lanes moved with the converters static and %s — live data, a floating input or another driver; check the toggle counts and the levels against the prime", res.NMoved, broke(res.Broke))
	}
	return res, nil
}
