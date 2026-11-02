// GPMC CS1 read timing (AM335x TRM 7.1.5 GPMC_CONFIG2..6; fpga-specs 10 §4.3,
// 12 §5.5) and the timing sweep of rung R1 (06-TIERS §0, §6).
//
// The bootloader leaves CS1 at RDCYCLETIME 31 / RDACCESSTIME 13 (the "factory"
// timing, ~360 ns per word at the shipped cycle gap). The fabric's read
// through-path budget is 30 ns (default.sdc: RDACCESSTIME >= OEONTIME + 3
// ticks at 100 MHz), so the controller can go markedly faster — but only the
// ramp harness can prove it: a too-early latch is invisible on flat data. The
// sweep here lowers RDACCESSTIME, then RDCYCLETIME (with OEOFFTIME and
// CSRDOFFTIME tracking the cycle end), optionally CYCLE2CYCLEDELAY, draining
// a frozen TSRC ramp record at every setting in alternating blocks against the
// starting timing, and records the floor (last clean value) of every knob.
// The chosen timing is one tick above the floor on every knob. Nothing here
// is persisted or kept applied: the sweep is a step machine (Sweeper.Step),
// every step is one bounded Engine.Exec that restores the timing it started
// from before it returns, so the health token keeps beating and no step can
// strand the controller at a candidate; persistence and the boot-time gate
// are in gpmcpersist.go.
//
// The write-side fields, CONFIG1 (WAIT monitoring, device size) and CONFIG7
// (the CS1 base address) are never touched. Only CS1 is ever written — never
// NAND CS0 and never the shared prefetch engine (fpga-specs 10 §2.4).
// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

const (
	gpmcConfig1 = 0x90 // GPMC_CONFIG1_1 (read for the report only)
	gpmcConfig2 = 0x94 // CSONTIME[3:0] CSEXTRADELAY[7] CSRDOFFTIME[12:8] CSWROFFTIME[20:16]
	gpmcConfig3 = 0x98 // ADV timings (read for the report only)
	gpmcConfig4 = 0x9C // OEONTIME[3:0] OEEXTRADELAY[7] OEOFFTIME[12:8] WEONTIME[19:16] WEEXTRADELAY[23] WEOFFTIME[28:24]
	gpmcConfig5 = 0xA0 // RDCYCLETIME[4:0] WRCYCLETIME[12:8] RDACCESSTIME[20:16] PAGEBURSTACCESSTIME[27:24]
	// gpmcConfig6 (0xA4): BUSTURNAROUND[3:0] CYCLE2CYCLEDIFFCSEN[6] CYCLE2CYCLESAMECSEN[7]
	// CYCLE2CYCLEDELAY[11:8] WRDATAONADMUXBUS[19:16] WRACCESSTIME[28:24]; gpmcConfig7 (0xA8): CSVALID[6].

	// fabricReadTicks is the fabric's read through-path budget in GPMC FCLK
	// ticks (default.sdc: 30 ns at the assumed 100 MHz FCLK): RDACCESSTIME
	// must sit at least this many ticks after OEONTIME.
	fabricReadTicks = 3
)

// CS1Timing is the CS1 timing configuration: the four words the sweep may
// change (CONFIG2/4/5/6) plus CONFIG1/3/7 as read, for the report.
// TRLC-LINKS: REQ-SDS-131
type CS1Timing struct {
	Config1 uint32 `json:"config1"`
	Config2 uint32 `json:"config2"`
	Config3 uint32 `json:"config3"`
	Config4 uint32 `json:"config4"`
	Config5 uint32 `json:"config5"`
	Config6 uint32 `json:"config6"`
	Config7 uint32 `json:"config7"`
}

// FactoryCS1Timing is the bootloader's CS1 timing on this unit (fpga-specs
// 10 §4.3 table) with the shipped cycle gap 5 (EnableEDMA). Used only as the
// fallback when the boot-time read is unavailable in a report.
var FactoryCS1Timing = CS1Timing{Config1: 0x00001001, Config2: 0x00141400, Config3: 0x00020201,
	Config4: 0x10041004, Config5: 0x010d141f, Config6: 0x060005c1, Config7: 0x00000F41}

// TRLC-LINKS: REQ-SDS-131
func bits(w uint32, shift, width uint) uint32 { return (w >> shift) & (1<<width - 1) }

// TRLC-LINKS: REQ-SDS-131
func setBits(w *uint32, shift, width uint, v uint32) {
	m := uint32(1<<width-1) << shift
	*w = (*w &^ m) | (v << shift & m)
}

// Read-side fields (getters).
// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) CSOn() uint32 { return bits(t.Config2, 0, 4) }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) CSRdOff() uint32 { return bits(t.Config2, 8, 5) }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) CSWrOff() uint32 { return bits(t.Config2, 16, 5) }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) OEOn() uint32 { return bits(t.Config4, 0, 4) }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) OEOff() uint32 { return bits(t.Config4, 8, 5) }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) WEOn() uint32 { return bits(t.Config4, 16, 4) }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) WEOff() uint32 { return bits(t.Config4, 24, 5) }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) RdCycle() uint32 { return bits(t.Config5, 0, 5) }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) WrCycle() uint32 { return bits(t.Config5, 8, 5) }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) RdAccess() uint32 { return bits(t.Config5, 16, 5) }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) Gap() uint32 { return bits(t.Config6, 8, 4) } // CYCLE2CYCLEDELAY
// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) SameCSEn() bool { return bits(t.Config6, 7, 1) == 1 }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) WrAccess() uint32 { return bits(t.Config6, 24, 5) }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) Turnaround() uint32 { return bits(t.Config6, 0, 4) }

// Setters return a copy with one field replaced (values masked to the field).
// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) WithRdAccess(v uint32) CS1Timing { setBits(&t.Config5, 16, 5, v); return t }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) WithRdCycle(v uint32) CS1Timing { setBits(&t.Config5, 0, 5, v); return t }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) WithOEOn(v uint32) CS1Timing { setBits(&t.Config4, 0, 4, v); return t }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) WithOEOff(v uint32) CS1Timing { setBits(&t.Config4, 8, 5, v); return t }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) WithCSRdOff(v uint32) CS1Timing { setBits(&t.Config2, 8, 5, v); return t }

// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) WithGap(v uint32) CS1Timing {
	setBits(&t.Config6, 8, 4, v)
	setBits(&t.Config6, 7, 1, 1) // the same-CS gap is what makes the pop port see a fresh nOE
	return t
}

// TimingFields is the decoded read-side view (JSON / logs).
// TRLC-LINKS: REQ-SDS-131
type TimingFields struct {
	RdCycle  uint32 `json:"rd_cycle"`
	RdAccess uint32 `json:"rd_access"`
	OEOn     uint32 `json:"oe_on"`
	OEOff    uint32 `json:"oe_off"`
	CSOn     uint32 `json:"cs_on"`
	CSRdOff  uint32 `json:"cs_rd_off"`
	Gap      uint32 `json:"gap"`
	SameCSEn bool   `json:"same_cs_en"`
	WrCycle  uint32 `json:"wr_cycle"`
	WrAccess uint32 `json:"wr_access"`
	WEOn     uint32 `json:"we_on"`
	WEOff    uint32 `json:"we_off"`
	CSWrOff  uint32 `json:"cs_wr_off"`
}

// Fields decodes the timing.
// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) Fields() TimingFields {
	return TimingFields{RdCycle: t.RdCycle(), RdAccess: t.RdAccess(), OEOn: t.OEOn(), OEOff: t.OEOff(),
		CSOn: t.CSOn(), CSRdOff: t.CSRdOff(), Gap: t.Gap(), SameCSEn: t.SameCSEn(),
		WrCycle: t.WrCycle(), WrAccess: t.WrAccess(), WEOn: t.WEOn(), WEOff: t.WEOff(), CSWrOff: t.CSWrOff()}
}

// String is the compact read-side summary used in logs.
// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) String() string {
	u := func(b []byte, v uint32) []byte { return strconv.AppendUint(b, uint64(v), 10) }
	b := make([]byte, 0, 64)
	b = u(append(b, "rdcycle="...), t.RdCycle())
	b = u(append(b, " rdaccess="...), t.RdAccess())
	b = u(append(b, " oe="...), t.OEOn())
	b = u(append(b, ".."...), t.OEOff())
	b = u(append(b, " cs="...), t.CSOn())
	b = u(append(b, ".."...), t.CSRdOff())
	b = u(append(b, " gap="...), t.Gap())
	return string(b)
}

// ReadTicks is the nominal FCLK ticks per read cycle including the gap (the
// quantity the ns/word measurement scales with).
// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) ReadTicks() uint32 { return t.RdCycle() + t.Gap() }

// Validate checks the read-side relations the controller and the fabric need
// (TRM 7.1.3.3 read timing; default.sdc's through-path budget). The write
// side is not checked: the sweep never changes it.
// TRLC-LINKS: REQ-SDS-131
func (t CS1Timing) Validate() error {
	c, a, oeOn, oeOff, csOn, csOff := t.RdCycle(), t.RdAccess(), t.OEOn(), t.OEOff(), t.CSOn(), t.CSRdOff()
	switch {
	case c == 0 || a == 0 || oeOff == 0 || csOff == 0:
		return fmt.Errorf("gpmc timing: zero cycle/access/off time (%s)", t)
	case a >= c:
		return fmt.Errorf("gpmc timing: RDACCESSTIME %d must be below RDCYCLETIME %d", a, c)
	case a < oeOn+fabricReadTicks:
		return fmt.Errorf("gpmc timing: RDACCESSTIME %d below OEONTIME %d + %d (fabric read through-path)", a, oeOn, fabricReadTicks)
	case a >= oeOff:
		return fmt.Errorf("gpmc timing: RDACCESSTIME %d must be latched before OEOFFTIME %d", a, oeOff)
	case oeOff > c:
		return fmt.Errorf("gpmc timing: OEOFFTIME %d beyond RDCYCLETIME %d", oeOff, c)
	case csOff > c:
		return fmt.Errorf("gpmc timing: CSRDOFFTIME %d beyond RDCYCLETIME %d", csOff, c)
	case csOff < oeOff:
		return fmt.Errorf("gpmc timing: CSRDOFFTIME %d before OEOFFTIME %d (nCS must cover nOE)", csOff, oeOff)
	case csOn > oeOn:
		return fmt.Errorf("gpmc timing: CSONTIME %d after OEONTIME %d", csOn, oeOn)
	case !t.SameCSEn() || t.Gap() == 0:
		return fmt.Errorf("gpmc timing: the same-CS cycle gap is off (CONFIG6 %#08x) — the pop port needs it", t.Config6)
	}
	return nil
}

// TimingPort reads and writes the CS1 timing: the /dev/mem mapping on the
// device, a fake in tests.
// TRLC-LINKS: REQ-SDS-131
type TimingPort interface {
	Read() (CS1Timing, error)
	// Apply validates (CS1Timing.Validate) and writes CONFIG2/4/5/6.
	Apply(CS1Timing) error
	// Restore writes a timing previously read from the controller without
	// validation (the boot timing may predate the cycle gap).
	Restore(CS1Timing) error
}

// readTiming / applyTiming operate on a mapped register block.
// TRLC-LINKS: REQ-SDS-131
func readTiming(r regs32) CS1Timing {
	return CS1Timing{Config1: r.R(gpmcConfig1), Config2: r.R(gpmcConfig2), Config3: r.R(gpmcConfig3),
		Config4: r.R(gpmcConfig4), Config5: r.R(gpmcConfig5), Config6: r.R(gpmcConfig6), Config7: r.R(gpmcConfig7)}
}

// applyTiming writes CONFIG2/4/5/6 with CSVALID quiesced around the retiming
// and restored exactly (the sequence proven for the cycle gap). CONFIG1/3/7
// are left as they are. Every written word is read back.
// TRLC-LINKS: REQ-SDS-131
func applyTiming(r regs32, t CS1Timing, validate bool) error {
	if validate {
		if err := t.Validate(); err != nil {
			return err
		}
	}
	old7 := r.R(gpmcConfig7)
	r.W(gpmcConfig7, old7&^uint32(c7CSValid))
	_ = r.R(gpmcConfig7)
	writes := []struct {
		off uint32
		v   uint32
	}{{gpmcConfig2, t.Config2}, {gpmcConfig4, t.Config4}, {gpmcConfig5, t.Config5}, {gpmcConfig6, t.Config6}}
	for _, w := range writes {
		r.W(w.off, w.v)
	}
	_ = r.R(gpmcConfig6)
	r.W(gpmcConfig7, old7)
	_ = r.R(gpmcConfig7)
	for _, w := range writes {
		if got := r.R(w.off); got != w.v {
			return fmt.Errorf("gpmc: CONFIG at %#x reads %#08x after write, want %#08x", w.off, got, w.v)
		}
	}
	return nil
}

// MemTimingPort is the /dev/mem mapping of the GPMC block (the way fpgaload
// maps the CS3 port and programCS1CycleGap the gap).
// TRLC-LINKS: REQ-SDS-131
type MemTimingPort struct{ m []byte }

// OpenTimingPort maps the GPMC controller registers. Close when done.
// TRLC-LINKS: REQ-SDS-131
func OpenTimingPort() (*MemTimingPort, error) {
	f, err := os.OpenFile("/dev/mem", os.O_RDWR|syscall.O_SYNC, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := syscall.Mmap(int(f.Fd()), gpmcBase, gpmcLen, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("gpmc mmap: %w", err)
	}
	return &MemTimingPort{m: m}, nil
}

// TRLC-LINKS: REQ-SDS-131
func (p *MemTimingPort) Read() (CS1Timing, error) { return readTiming(memRegs{p.m}), nil }

// TRLC-LINKS: REQ-SDS-131
func (p *MemTimingPort) Apply(t CS1Timing) error { return applyTiming(memRegs{p.m}, t, true) }

// TRLC-LINKS: REQ-SDS-131
func (p *MemTimingPort) Restore(t CS1Timing) error { return applyTiming(memRegs{p.m}, t, false) }

// TRLC-LINKS: REQ-SDS-131
func (p *MemTimingPort) Close() { syscall.Munmap(p.m) }
