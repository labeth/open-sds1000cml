// ENGMODEL-OWNER-UNIT: FU-APP-DIAG
package diag

import (
	"fmt"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/iface"
)

// CrankOptions runs ONE rung of the stage-1 SRAM-clock crank: one accepted arm,
// one budgeted burst, one snapshot record.
//
// F2 and J2 are the two remaining SRAM-clock candidates. Every null this branch
// has recorded on the SRAM interface was taken with both held statically low, and
// a pipelined SyncBurst part with no clock can never present a new word. The
// emitter is hardware-budgeted: an edge exists only because a keyed arm loaded a
// finite count, and the whole configuration has a ceiling no register write raises.
// TRLC-LINKS: REQ-SDS-151
type CrankOptions struct {
	Ball  string `json:"ball"`  // "f2" | "j2" | "d1" | "none" (none = the sham arm)
	NEdge int    `json:"nedge"` // 0..1023
	Div   int    `json:"div"`   // 0..31; f_pad = 100 MHz / 2^DIV
	Dly   int    `json:"dly"`   // clk100 ticks from arm to the first edge
	Guard int    `json:"guard"` // 0 off (only with NEDGE=1), 1 lanes, 2 all
	High  int    `json:"high"`  // pulse HIGH time in clk100 ticks, independent of the period; 0 = half-period
	Snap  bool   `json:"snap"`  // arm the mode-7 record on the accepted arm

	// Cs3Sel/Cs3Vals put the MAX V in a chosen posture immediately BEFORE the arm, so the
	// counted edges go out while that posture is held. Stage 1 cranked with the MAX V idle;
	// the CS3 sweep poked the MAX V with no clock running. A SyncBurst part needs both — a
	// command asserted AND clock edges — so neither experiment alone could produce data, and
	// the conjunction is what stage 1's fourth surviving explanation ("the part needs a
	// command we cannot issue because ADSC is the MAX V's") actually calls for.
	//
	// CS3 0x08 is REFUSED here: writing it wedges the whole device (the app dies, then the
	// network), recoverable only by a mains cycle. See 2026-09-07-sram-address-and-read-port.md.
	Cs3Sel  uint16   `json:"cs3_sel"`
	Cs3Vals []uint16 `json:"cs3_vals"`

	// Gen runs the 27-ball bus generator in the FACTORY's five-phase structure while the
	// counted edges go out. Every previous experiment had at most two of the three things the
	// port does at once: stage 1 clocked with the bus idle; the busgen sweeps drove the bus
	// with no clock. The decoded controller (see §12 of the SRAM report) does all three
	// together -- five phases of interleaved data, one shared output enable, launched on a
	// clock -- so this is what "the same thing the factory does" actually requires.
	//
	// Drive all-ones and release a phase: then any ball reading 0 came from something else.
	Gen       bool     `json:"gen"`
	DriveMask uint32   `json:"drive_mask"`
	OePhase   int      `json:"oe_phase"` // 5-bit mask, bit k = drive in phase k
	Pattern   []uint32 `json:"pattern"`
	Pre       int      `json:"pre"`     // phase-advance prescaler; (PRE+1) x 20 ns at RATE 0
	GenRate   int      `json:"genrate"` // 0 clk50, 1 clk100, 2 cap_clk, 3 clk
}

// cs3Wedge is the one CS3 selector known to take the whole board down.
const cs3Wedge uint16 = 0x08

// CrankLane is one monitored signal's sticky state over the arm window.
// TRLC-LINKS: REQ-SDS-151
type CrankLane struct {
	Idx   int  `json:"idx"`
	Level int  `json:"level"`
	Ever1 bool `json:"ever1"`
	Ever0 bool `json:"ever0"`
	Moved bool `json:"moved"`
}

// CrankResult is one rung.
// TRLC-LINKS: REQ-SDS-151
type CrankResult struct {
	Options   CrankOptions `json:"options"`
	Preflight []string     `json:"preflight"` // gate name = readback, in order
	Armed     bool         `json:"armed"`
	Refuse    int          `json:"refuse"` // SRCLK_STAT.REFUSE; 0 = accepted
	RefuseWhy string       `json:"refuse_why"`
	Busy      bool         `json:"busy"`
	Remain    int          `json:"remain"`
	Aborted   bool         `json:"aborted"`
	AbortEdge int          `json:"abort_edge"`
	DepGrp    int          `json:"dep_grp"`
	EmittedF2 int          `json:"emitted_f2"`
	EmittedJ2 int          `json:"emitted_j2"`
	EmittedD1 int          `json:"emitted_d1"`
	ArrivedD1 int          `json:"arrived_d1"` // at our own ball
	ArrivedP6 int          `json:"arrived_p6"` // at the far die
	Life      int          `json:"life"`
	Cs3Held   uint16       `json:"cs3_held"` // the last CS3 value written before the arm
	BusRDPre  uint32       `json:"bus_rd_pre"`
	BusRDPost uint32       `json:"bus_rd_post"`
	Moved     []CrankLane  `json:"moved"`   // only the signals that moved
	NMoved    int          `json:"n_moved"` // how many of the 128 monitored signals moved
	Record    []uint16     `json:"record,omitempty"`
	Verdict   string       `json:"verdict"` // PASS | NULL | ABORT | REFUSED | VOID:<why>
}

var refuseWhy = map[int]string{
	0: "accepted",
	1: "no CRANK source, or DIAG_CTRL.F2_SEL/J2_SEL not both 0",
	2: "the 27-ball group is not in its released-high rest posture",
	3: "NEDGE = 0",
	4: "busy, or the guard baseline is not ready",
	5: "ABORTED is set — write CLR first",
	6: "would exceed the lifetime ceiling of 4096 edges",
	7: "GUARD off with NEDGE > 1, or CTRL_G2 (unimplemented in this build)",
}

const srclkKey = 0x00a5

// SramCrank runs one rung. It never calls the register restore on an abort: the
// restore re-arms the acquisition engine, and after an abort the world is rebuilt
// by tools/hw/coldstart.sh, not by a register replay.
// TRLC-LINKS: REQ-SDS-151
func (d *Diag) SramCrank(o CrankOptions) (*CrankResult, error) {
	const crank = 2 // SRCLK_CTRL.*_SRC: 0 LEGACY, 1 ZERO, 2 CRANK
	src := 0
	switch o.Ball {
	case "f2":
		src = crank << iface.DiagSrclkCtrlF2SrcShift
	case "j2":
		src = crank << iface.DiagSrclkCtrlJ2SrcShift
	case "d1":
		src = crank << iface.DiagSrclkCtrlD1SrcShift
	case "none", "":
		// the sham arm: every gate exercised, no ball selected, so the fabric refuses
	default:
		return nil, fmt.Errorf("diag: crank ball %q must be f2, j2, d1 or none", o.Ball)
	}
	if o.NEdge < 0 || o.NEdge > 1023 {
		return nil, fmt.Errorf("diag: crank nedge %d out of range (0..1023)", o.NEdge)
	}
	if o.Div < 0 || o.Div > 31 {
		return nil, fmt.Errorf("diag: crank div %d out of range (0..31)", o.Div)
	}
	if o.Dly < 0 || o.Dly > 1023 {
		return nil, fmt.Errorf("diag: crank dly %d out of range (0..1023)", o.Dly)
	}
	if o.High < 0 || o.High > 1023 {
		return nil, fmt.Errorf("diag: crank high %d out of range (0..1023)", o.High)
	}
	if o.Guard < 0 || o.Guard > 2 {
		return nil, fmt.Errorf("diag: crank guard %d out of range (0..2)", o.Guard)
	}
	res := &CrankResult{Options: o}

	ctrl := uint16(src) | uint16(o.Guard)<<iface.DiagSrclkCtrlGuardShift

	nword := uint16(o.NEdge) | uint16(o.Div)<<iface.DiagSrclkNDivShift

	err := d.run.Exec(func(b bus.Bus) error {
		add := func(name string, v uint16) { res.Preflight = append(res.Preflight, fmt.Sprintf("%s=%#04x", name, v)) }
		// --- pre-flight: these are gates, and every one is read back, not assumed.
		dc, err := b.Read(bus.PlaneCS1, iface.SelDiagCtrl)
		if err != nil {
			return err
		}
		add("DIAG_CTRL", dc)
		rdlo, _ := winRead(b, iface.DiagBusRdLo)
		rdhi, _ := winRead(b, iface.DiagBusRdHi)
		res.BusRDPre = uint32(rdlo) | uint32(rdhi&0x07ff)<<16
		add("BUS_RD_LO", rdlo)
		add("BUS_RD_HI", rdhi)
		gen, _ := winRead(b, iface.DiagBusgenCtl)
		add("BUSGEN_CTL", gen)

		// --- run the bus generator in the factory's five-phase structure, if asked.
		if o.Gen {
			aux0, _ := winRead(b, iface.DiagBusgenAux)
			ctl0, _ := winRead(b, iface.DiagBusgenCtl)
			oel0, _ := winRead(b, iface.DiagBusgenOeLo)
			oeh0, _ := winRead(b, iface.DiagBusgenOeHi)
			pre0, _ := winRead(b, iface.DiagBusgenPre)
			defer func() {
				_ = winWrite(b, iface.DiagBusgenCtl, 0)
				_ = winWrite(b, iface.DiagBusgenAux, aux0)
				_ = winWrite(b, iface.DiagBusgenOeLo, oel0)
				_ = winWrite(b, iface.DiagBusgenOeHi, oeh0)
				_ = winWrite(b, iface.DiagBusgenPre, pre0)
				_ = winWrite(b, iface.DiagBusgenCtl, ctl0&^iface.DiagBusgenCtlRunMask)
			}()
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
			gctl := iface.DiagBusgenCtlEnMask |
				uint16(o.GenRate)<<iface.DiagBusgenCtlRateShift |
				uint16(4)<<iface.DiagBusgenCtlNphaseShift | // the factory's 5 phases
				uint16(o.OePhase)<<iface.DiagBusgenCtlOePhShift |
				iface.DiagBusgenCtlRunMask
			if err := winWrite(b, iface.DiagBusgenCtl, gctl); err != nil {
				return err
			}
		}

		// --- hold the MAX V in the requested posture while the edges go out.
		if len(o.Cs3Vals) > 0 {
			if o.Cs3Sel == cs3Wedge {
				return fmt.Errorf("crank: CS3 selector %#04x wedges the device; refused", o.Cs3Sel)
			}
			for _, v := range o.Cs3Vals {
				if err := b.Write(bus.PlaneCS3, o.Cs3Sel, v); err != nil {
					return fmt.Errorf("crank: cs3 %#04x=%#04x: %w", o.Cs3Sel, v, err)
				}
			}
			res.Cs3Held = o.Cs3Vals[len(o.Cs3Vals)-1]
		}

		// --- configure, then arm with the key in the same word.
		if err := winWrite(b, iface.DiagSrclkCtrl, ctrl); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagSrclkN, nword); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagSrclkDly, uint16(o.Dly)); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagSrclkW, uint16(o.High)); err != nil {
			return err
		}
		if err := winWrite(b, iface.DiagSrclkArm, srclkKey|iface.DiagSrclkArmClrMask); err != nil {
			return err
		}
		// SRCLK_CTRL.SNAP_ON_ARM is RESERVED AND NOT WIRED, so setting it arms nothing.
		// Arm the snapshot here instead, immediately before the crank arm, or the record
		// popped below is whatever the RAM last held.
		if o.Snap {
			c, err := b.Read(bus.PlaneCS1, iface.SelDiagCtrl)
			if err != nil {
				return err
			}
			base := c &^ iface.DiagCtrlSnapArmMask
			if err := b.Write(bus.PlaneCS1, iface.SelDiagCtrl, base|iface.DiagCtrlSnapArmMask); err != nil {
				return err
			}
			if err := b.Write(bus.PlaneCS1, iface.SelDiagCtrl, base); err != nil {
				return err
			}
		}
		if err := winWrite(b, iface.DiagSrclkArm, srclkKey|iface.DiagSrclkArmArmMask); err != nil {
			return err
		}

		// --- wait for the burst to finish, bounded.
		var st uint16
		for i := 0; i < 400; i++ {
			st, _ = winRead(b, iface.DiagSrclkStat)
			if st&iface.DiagSrclkStatBusyMask == 0 {
				break
			}
			d.sleep(time.Millisecond)
		}
		res.Remain = int(st & iface.DiagSrclkStatRemainMask)
		res.Busy = st&iface.DiagSrclkStatBusyMask != 0
		res.Aborted = st&iface.DiagSrclkStatAbortedMask != 0
		res.Refuse = int(st&iface.DiagSrclkStatRefuseMask) >> iface.DiagSrclkStatRefuseShift
		res.Armed = st&iface.DiagSrclkStatRefusedMask == 0
		res.RefuseWhy = refuseWhy[res.Refuse]

		cd1, _ := winRead(b, iface.DiagSrclkCntD1)
		ad1, _ := winRead(b, iface.DiagSrclkArrD1)
		ap6, _ := winRead(b, iface.DiagSrclkArrP6)
		res.EmittedD1, res.ArrivedD1, res.ArrivedP6 = int(cd1), int(ad1), int(ap6)
		f2, _ := winRead(b, iface.DiagSrclkCntF2)
		j2, _ := winRead(b, iface.DiagSrclkCntJ2)
		life, _ := winRead(b, iface.DiagSrclkLife)
		dep, _ := winRead(b, iface.DiagSrclkDep)
		res.EmittedF2, res.EmittedJ2, res.Life = int(f2), int(j2), int(life)
		res.DepGrp = int(dep & iface.DiagSrclkDepGrpMask)
		res.AbortEdge = int(dep&iface.DiagSrclkDepAbortEdgeMask) >> iface.DiagSrclkDepAbortEdgeShift

		rdlo2, _ := winRead(b, iface.DiagBusRdLo)
		rdhi2, _ := winRead(b, iface.DiagBusRdHi)
		res.BusRDPost = uint32(rdlo2) | uint32(rdhi2&0x07ff)<<16

		// --- the per-signal truth: the sticky ever-flags over the arm window.
		for i := 0; i < 128; i++ {
			if err := winWrite(b, iface.DiagLaneIdx, uint16(i)); err != nil {
				return err
			}
			lv, _ := winRead(b, iface.DiagLaneLvl)
			l := CrankLane{Idx: i, Level: int(lv & 1), Ever1: lv&2 != 0, Ever0: lv&4 != 0}
			l.Moved = l.Ever1 && l.Ever0
			if l.Moved {
				res.Moved = append(res.Moved, l)
				res.NMoved++
			}
		}

		// --- the record, if one was armed.
		if o.Snap {
			var rem uint16
			for i := 0; i < 200; i++ {
				rem, _ = b.Read(bus.PlaneCS1, iface.SelSnapRemain)
				if rem&iface.SnapRemainReadyMask != 0 {
					break
				}
				d.sleep(time.Millisecond)
			}
			if rem&iface.SnapRemainReadyMask != 0 {
				n := int(rem & iface.SnapRemainRemainMask)
				res.Record = make([]uint16, n)
				b.PopWords(iface.SelSnapPop, res.Record, n)
			}
		}

		// --- park the emitter. Not a restore: this only returns the sources to LEGACY.
		return winWrite(b, iface.DiagSrclkCtrl, uint16(o.Guard)<<iface.DiagSrclkCtrlGuardShift)
	}, d.timeout)
	if err != nil {
		return nil, err
	}

	switch {
	case !res.Armed:
		res.Verdict = "REFUSED"
	case res.Aborted:
		res.Verdict = "ABORT"
	case res.NMoved > 0:
		res.Verdict = "PASS"
	default:
		res.Verdict = "NULL"
	}
	return res, nil
}
