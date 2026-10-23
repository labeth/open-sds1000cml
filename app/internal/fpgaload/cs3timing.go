// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
)

// The MAX V port's chip select (CS3) runs at the vendor's timing: a write
// cycle of 20 GPMC clocks (10 ns each) and a 15-clock gap after every
// access. Each of an image's three million port writes took 430 ns; with a
// write cycle of 8 and a gap of 2 one takes 280 ns, where the interconnect
// sets the floor (a gap of 0 is no faster). A shift runs at that timing
// (fastWrite) and puts the vendor's back after; the read fields stay as
// they are.
const (
	gpmcPhys      = 0x50000000
	gpmcLen       = 4096
	cs3Config2    = 0x60 + 3*0x30 + 4  // CSWROFFTIME 20:16
	cs3Config4    = 0x60 + 3*0x30 + 12 // WEONTIME 19:16, WEOFFTIME 28:24
	cs3Config5    = 0x60 + 3*0x30 + 16 // WRCYCLETIME 12:8
	cs3Config6    = 0x60 + 3*0x30 + 20 // CYCLE2CYCLEDELAY 11:8
	fastWrCycle   = 8                  // clocks: WE low 1..6, data held 2 more
	fastWEOn      = 1
	fastWEOff     = 6
	fastCSWrOff   = 8
	fastGap       = 2
	vendorWrCycle = 20 // what the vendor programs (checked before changing it)
	vendorGap     = 15
)

// cs3Timing is the timing words of CS3 a shift changes.
type cs3Timing struct{ c2, c4, c5, c6 uint32 }

// field sets bits [shift, shift+width) of w to v.
// TRLC-LINKS: REQ-SDS-005
func field(w uint32, shift, width uint, v uint32) uint32 {
	m := uint32(1)<<width - 1
	return w&^(m<<shift) | (v&m)<<shift
}

// fastWrite is t with the short write cycle; ok is false when t is not the
// timing it was made for (the write cycle the vendor sets), and it is left.
// TRLC-LINKS: REQ-SDS-005
func fastWrite(t cs3Timing) (cs3Timing, bool) {
	if t.c5>>8&31 != vendorWrCycle || t.c6>>8&15 != vendorGap {
		return t, false
	}
	t.c2 = field(t.c2, 16, 5, fastCSWrOff)
	t.c4 = field(t.c4, 16, 4, fastWEOn)
	t.c4 = field(t.c4, 24, 5, fastWEOff)
	t.c5 = field(t.c5, 8, 5, fastWrCycle)
	t.c6 = field(t.c6, 8, 4, fastGap)
	return t, true
}

// cs3Regs is the GPMC register block mapped through /dev/mem.
type cs3Regs struct{ m []byte }

// TRLC-LINKS: REQ-SDS-005
func (r cs3Regs) get(off int) uint32 { return binary.LittleEndian.Uint32(r.m[off:]) }

// TRLC-LINKS: REQ-SDS-005
func (r cs3Regs) set(off int, v uint32) { binary.LittleEndian.PutUint32(r.m[off:], v) }

// TRLC-LINKS: REQ-SDS-005
func (r cs3Regs) timing() cs3Timing {
	return cs3Timing{r.get(cs3Config2), r.get(cs3Config4), r.get(cs3Config5), r.get(cs3Config6)}
}

// apply writes t and reads it back.
// TRLC-LINKS: REQ-SDS-005
func (r cs3Regs) apply(t cs3Timing) error {
	r.set(cs3Config2, t.c2)
	r.set(cs3Config4, t.c4)
	r.set(cs3Config5, t.c5)
	r.set(cs3Config6, t.c6)
	if got := r.timing(); got != t {
		return fmt.Errorf("cs3 timing reads %#08x after writing %#08x", got, t)
	}
	return nil
}

// withFastWrites runs load with CS3's short write cycle and puts the
// vendor's timing back after. ok is false, and load did not run, when the
// timing could not be changed (err says why).
// TRLC-LINKS: REQ-SDS-005
func withFastWrites(load func() error) (ok bool, err error) {
	f, err := os.OpenFile("/dev/mem", os.O_RDWR|syscall.O_SYNC, 0)
	if err != nil {
		return false, fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	m, err := syscall.Mmap(int(f.Fd()), gpmcPhys, gpmcLen, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return false, fmt.Errorf("mmap: %w", err)
	}
	defer syscall.Munmap(m)
	r := cs3Regs{m}
	vendor := r.timing()
	fast, ok := fastWrite(vendor)
	if !ok {
		return false, fmt.Errorf("cs3 timing %#08x is not the vendor's", vendor)
	}
	if err := r.apply(fast); err != nil {
		_ = r.apply(vendor)
		return false, err
	}
	err = load()
	if e := r.apply(vendor); e != nil && err == nil {
		err = e
	}
	return true, err
}
