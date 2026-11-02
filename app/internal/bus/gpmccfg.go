// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"fmt"
	"os"
	"syscall"
)

// GPMC controller registers that the drain needs (AM335x TRM 7.1.5; fpga-specs
// 12 §5.5, §6.1). Only the CS1 config words are ever touched — never NAND CS0
// and never the shared prefetch/ECC engine (fpga-specs 10 §2.4).
const (
	gpmcBase     = 0x50000000
	gpmcLen      = 0x1000
	gpmcConfig6  = 0xA4 // GPMC_CONFIG6_1: CYCLE2CYCLEDELAY[11:8], CYCLE2CYCLESAMECSEN[7]
	gpmcConfig7  = 0xA8 // GPMC_CONFIG7_1: CSVALID[6]
	c6GapMask    = 0x00000F80
	c6SameCSEn   = 1 << 7
	c7CSValid    = 1 << 6
	cs1CycleGap  = 5 // shipped gap (fpga-specs 12 §5.5: 4 = first clean, 5 = margin)
	cs1GapMaxDly = 15
)

// cycleGapWords computes the CONFIG6/CONFIG7 write sequence for a same-CS
// cycle-to-cycle gap of delay GPMC clocks: (old6 &^ gap bits) | delay<<8 |
// SAMECSEN, with CSVALID cleared around the retiming and restored exactly.
// TRLC-LINKS: REQ-SDS-131
func cycleGapWords(old6, old7, delay uint32) (new6, quiesced7 uint32) {
	new6 = (old6 &^ uint32(c6GapMask)) | (delay << 8) | c6SameCSEn
	quiesced7 = old7 &^ uint32(c7CSValid)
	return new6, quiesced7
}

// applyCS1CycleGap performs the sequence against a register block.
// TRLC-LINKS: REQ-SDS-131
func applyCS1CycleGap(r regs32, delay uint32) error {
	if delay > cs1GapMaxDly {
		return fmt.Errorf("gpmc: cycle gap %d exceeds the 4-bit field", delay)
	}
	old6, old7 := r.R(gpmcConfig6), r.R(gpmcConfig7)
	new6, q7 := cycleGapWords(old6, old7, delay)
	r.W(gpmcConfig7, q7) // clear CSVALID before retiming
	r.W(gpmcConfig6, new6)
	_ = r.R(gpmcConfig6)   // readback barrier
	r.W(gpmcConfig7, old7) // restore CSVALID exactly
	_ = r.R(gpmcConfig7)
	if got := r.R(gpmcConfig6); got != new6 {
		return fmt.Errorf("gpmc: CONFIG6_1 reads %#08x after write, want %#08x", got, new6)
	}
	return nil
}

// programCS1CycleGap maps the GPMC block and applies the gap.
// TRLC-LINKS: REQ-SDS-131
func programCS1CycleGap(delay uint32) error {
	f, err := os.OpenFile("/dev/mem", os.O_RDWR|syscall.O_SYNC, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	m, err := syscall.Mmap(int(f.Fd()), gpmcBase, gpmcLen, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return err
	}
	defer syscall.Munmap(m)
	return applyCS1CycleGap(memRegs{m}, delay)
}

// ---- read-only survey of the GPMC chip-select regions ----

// CSRegion is one chip select's decoded CONFIG7 plus its raw CONFIG1..7.
// Read-only: the survey never writes. Which regions exist, where they are
// mapped and how wide they are is a fact about the running system, not about
// our fabric, and it is the only way to find out whether anything besides the
// Cyclone (CS1) and its configuration port (CS3) sits on this bus.
// TRLC-LINKS: REQ-SDS-133
type CSRegion struct {
	CS      int       `json:"cs"`
	Valid   bool      `json:"valid"`    // CONFIG7 CSVALID
	Base    uint32    `json:"base"`     // BASEADDRESS[5:0] << 24
	MaskRaw uint32    `json:"mask_raw"` // MASKADDRESS[11:8]
	SizeMB  int       `json:"size_mb"`  // decoded from the mask nibble
	Config  [7]uint32 `json:"config"`   // CONFIG1..CONFIG7 verbatim
}

// ---- read-only peek at an arbitrary GPMC chip-select region ----
