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
	return
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

// gpmcCSConfig is CONFIG<n>_<cs>: the per-CS block starts at 0x60 and is
// 0x30 long, CONFIG1 first (AM335x TRM 7.5).
// TRLC-LINKS: REQ-SDS-133
func gpmcCSConfig(cs, n int) uint32 { return uint32(0x60 + cs*0x30 + (n-1)*4) }

// maskToMB decodes MASKADDRESS: the nibble is the run of 1s from bit 11 down,
// 0xF = 16 MB ... 0x0 = 256 MB.
// TRLC-LINKS: REQ-SDS-133
func maskToMB(mask uint32) int {
	mb := 256
	for i := 0; i < 4; i++ {
		if mask&(1<<uint(i)) != 0 {
			mb >>= 1
		}
	}
	return mb
}

// TRLC-LINKS: REQ-SDS-133
func surveyRegions(r regs32) []CSRegion {
	out := make([]CSRegion, 0, 7)
	for cs := 0; cs < 7; cs++ {
		reg := CSRegion{CS: cs}
		for n := 1; n <= 7; n++ {
			reg.Config[n-1] = r.R(gpmcCSConfig(cs, n))
		}
		c7 := reg.Config[6]
		reg.Valid = c7&c7CSValid != 0
		reg.Base = (c7 & 0x3F) << 24
		reg.MaskRaw = (c7 >> 8) & 0xF
		reg.SizeMB = maskToMB(reg.MaskRaw)
		out = append(out, reg)
	}
	return out
}

// SurveyCSRegions maps the GPMC controller read-only and reports every chip
// select's region. It writes nothing.
// TRLC-LINKS: REQ-SDS-133
func SurveyCSRegions() ([]CSRegion, error) {
	f, err := os.OpenFile("/dev/mem", os.O_RDONLY|syscall.O_SYNC, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := syscall.Mmap(int(f.Fd()), gpmcBase, gpmcLen, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("gpmc mmap: %w", err)
	}
	defer syscall.Munmap(m)
	return surveyRegions(memRegs{m}), nil
}

// ---- read-only peek at an arbitrary GPMC chip-select region ----

// PeekCS maps `n` 16-bit words at `off` inside the GPMC region of chip select
// `cs` and returns them. It opens /dev/mem O_RDONLY and maps PROT_READ, so it
// **cannot** write: this is a survey instrument, not an access path.
//
// Why it exists. SurveyCSRegions reports four VALID chip selects on this board —
// CS0 (NAND), CS1 (the Cyclone), CS3 (the MAX V) and **CS2 at 0x02000000, which
// nothing in this project has ever read** (the acq2 analysis branch
// item 6 lists "CS2: never read"). The whole SRAM campaign has assumed the external
// part is reached through the Cyclone or the MAX V, and the Cyclone's pad census
// leaves nobody to drive a 19-bit address (2026-09-06-sram-interface-decoded.md).
// An ARM-addressed region on its own chip select is the one supplier that resolves
// that, and it costs a read to find out.
// TRLC-LINKS: REQ-SDS-133
func PeekCS(cs int, off uint32, n int) ([]uint16, uint32, error) {
	if cs < 0 || cs > 6 {
		return nil, 0, fmt.Errorf("gpmc: cs %d out of range", cs)
	}
	if n <= 0 || n > 4096 {
		n = 256
	}
	regions, err := SurveyCSRegions()
	if err != nil {
		return nil, 0, err
	}
	r := regions[cs]
	if !r.Valid {
		return nil, 0, fmt.Errorf("gpmc: CS%d is not valid (CSVALID clear)", cs)
	}
	// page-align the mapping; the GPMC window is device memory, so O_SYNC.
	const pageSz = 4096
	start := (r.Base + off) &^ (pageSz - 1)
	skip := int((r.Base + off) - start)
	length := skip + n*2
	if length%pageSz != 0 {
		length += pageSz - length%pageSz
	}
	f, err := os.OpenFile("/dev/mem", os.O_RDONLY|syscall.O_SYNC, 0)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	m, err := syscall.Mmap(int(f.Fd()), int64(start), length, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, 0, fmt.Errorf("gpmc CS%d mmap at %#x: %w", cs, start, err)
	}
	defer syscall.Munmap(m)
	out := make([]uint16, n)
	for i := 0; i < n; i++ {
		j := skip + i*2
		out[i] = uint16(m[j]) | uint16(m[j+1])<<8
	}
	return out, r.Base + off, nil
}
