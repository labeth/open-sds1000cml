// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"

	core "github.com/labeth/open-dso-core/supervisor/diag"
)

// MemRegion is a block of AM335x registers the active stage reads.
type MemRegion struct {
	Name string
	Base uint32
	Len  uint32
}

// memRegions are always-clocked blocks only: the control module (the chip's
// device id and feature fuses, and every ball's pad configuration: the board's
// pin population), the GPMC chip-select configuration and the clock modules.
// A gated block (a GPIO bank whose clock is off) aborts the read with a bus
// error, so none is read here; debugfs gpio has the lines.
var memRegions = []MemRegion{
	{"control_status_devid", 0x44E10000, 0x800},
	{"control_padconf", 0x44E10800, 0x240},
	{"gpmc_config", 0x50000000, 0x2D0},
	{"cm_per", 0x44E00000, 0x400},
	{"cm_wkup", 0x44E00400, 0x100},
}

// collectMem reads each region through /dev/mem in a child process of its
// own (a bus error ends that child, not the collection) and stores the words.
// TRLC-LINKS: REQ-SDS-285
func collectMem(a *Archive, self string) {
	if !strings.Contains(strings.ToLower(core.ReadStr("/proc/cpuinfo")), "am335") {
		a.Errorf("mem: not an AM335x: registers not read")
		return
	}
	for _, r := range memRegions {
		out, err := exec.Command(self, "memread", strconv.FormatUint(uint64(r.Base), 16), strconv.FormatUint(uint64(r.Len), 16)).CombinedOutput() //nolint:gosec,noctx // G204: this program, re-run for one region; it ends by itself
		if err != nil {
			out = append(out, []byte(fmt.Sprintf("\n[%v]\n", err))...)
		}
		a.Add("mem/"+r.Name+".txt", out, fmt.Sprintf("/dev/mem %#x+%#x", r.Base, r.Len), "")
	}
}

// MemRead prints the 32-bit words of n bytes at physical address base, read
// through a read-only mapping of /dev/mem.
// TRLC-LINKS: REQ-SDS-285
func MemRead(base, n uint32) error {
	f, err := os.Open("/dev/mem")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	page := base &^ 0xfff
	span := int(base-page) + int(n)
	m, err := unix.Mmap(int(f.Fd()), int64(page), (span+0xfff)&^0xfff, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Munmap(m) }()
	for off := uint32(0); off+4 <= n; off += 4 {
		// one 32-bit load: device registers take no byte reads
		v := atomic.LoadUint32((*uint32)(unsafe.Pointer(&m[base-page+off]))) //nolint:gosec // G103: a word of the register mapping, 4-byte aligned
		fmt.Printf("%08x %08x\n", base+off, v)
	}
	return nil
}
