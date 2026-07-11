// gpmcprobe: READ-ONLY dump of the AM335x GPMC controller config registers via
// /dev/mem, to discover the chip-select layout (which CS maps the FPGA, which
// the "FPGA burst" window, NAND, CPLD), each CS's base/size, its CONFIG1 (sync
// vs async / burst), and whether the prefetch engine is configured/running.
//
// It never writes anything. It maps the GPMC config port (0x50000000, 4 KiB)
// PROT_READ and prints decoded registers. It also optionally maps a candidate
// data window (arg2 = phys base) read-only and dumps the first words, so the
// FPGA-burst region's contents can be compared to the async 0x30-0x34 stream.
//
// Usage:
//
//	gpmcprobe                      # dump GPMC controller config
//	gpmcprobe data 0x02000000 64   # also read-dump 64 words at that phys base
package main

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

const gpmcCfgBase = 0x50000000 // AM335x GPMC controller register port
const pageLen = 0x1000

func mapRO(base int64, n int) ([]byte, error) {
	f, err := os.OpenFile("/dev/mem", os.O_RDONLY|syscall.O_SYNC, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return syscall.Mmap(int(f.Fd()), base, n, syscall.PROT_READ, syscall.MAP_SHARED)
}

func r32(m []byte, off int) uint32 {
	return *(*uint32)(unsafe.Pointer(&m[off]))
}

// CONFIG7: BASEADDRESS[5:0]<<24 = base; MASKADDRESS[11:8] selects size; bit6 CSVALID.
func decodeCS7(v uint32) (base int64, sizeMB int, valid bool) {
	baseAddr := v & 0x3f
	mask := (v >> 8) & 0xf
	valid = v&0x40 != 0
	base = int64(baseAddr) << 24
	// mask 0xF=16MB,0xE=32,0xC=64,0x8=128?,... size = (16 << number-of-zero-low-bits)
	// AM335x: size selected so (base & mask) — enumerate the standard table.
	switch mask {
	case 0xf:
		sizeMB = 16
	case 0xe:
		sizeMB = 32
	case 0xc:
		sizeMB = 64
	case 0x8:
		sizeMB = 128
	case 0x0:
		sizeMB = 256
	default:
		sizeMB = -1
	}
	return
}

// CONFIG1 sync/async decode (key bits).
func decodeCS1(v uint32) string {
	readType := (v >> 29) & 1 // 1=sync, 0=async
	readMul := (v >> 30) & 1  // 1=multiple/burst read
	writeType := (v >> 27) & 1
	writeMul := (v >> 28) & 1
	dev := (v >> 12) & 3 // 0=8b,1=16b
	burst := (v >> 23) & 3
	gpmcFCLK := v & 3
	kind := "ASYNC"
	if readType == 1 {
		kind = "SYNC"
	}
	rm := ""
	if readMul == 1 {
		rm = " READMULTIPLE(burst)"
	}
	devsz := "8bit"
	if dev == 1 {
		devsz = "16bit"
	}
	return fmt.Sprintf("%s read%s write:%s%s dev:%s burstlen2^%d clkdiv:%d",
		kind, rm, map[uint32]string{0: "async", 1: "sync"}[writeType],
		map[uint32]string{0: "", 1: "+mul"}[writeMul], devsz, burst, gpmcFCLK+1)
}

func main() {
	m, err := mapRO(gpmcCfgBase, pageLen)
	if err != nil {
		fmt.Println("map GPMC cfg failed:", err)
		os.Exit(1)
	}
	rev := r32(m, 0x00)
	fmt.Printf("GPMC_REVISION   = 0x%08x\n", rev)
	fmt.Printf("GPMC_SYSCONFIG  = 0x%08x\n", r32(m, 0x10))
	fmt.Printf("GPMC_CONFIG     = 0x%08x\n", r32(m, 0x50))
	fmt.Printf("GPMC_STATUS     = 0x%08x  (bit0 WAIT0, bit8 EMPTYWRITEBUF)\n", r32(m, 0x54))
	fmt.Println("--- chip selects ---")
	for i := 0; i < 8; i++ {
		base := 0x60 + i*0x30
		c1 := r32(m, base+0x00)
		c7 := r32(m, base+0x18)
		pbase, sz, valid := decodeCS7(c7)
		if !valid {
			fmt.Printf("CS%d: (disabled)  CONFIG1=0x%08x CONFIG7=0x%08x\n", i, c1, c7)
			continue
		}
		fmt.Printf("CS%d: base=0x%08x size=%dMB  CONFIG1=0x%08x -> %s\n", i, pbase, sz, c1, decodeCS1(c1))
		fmt.Printf("      CONFIG2=0x%08x 3=0x%08x 4=0x%08x 5=0x%08x 6=0x%08x\n",
			r32(m, base+0x04), r32(m, base+0x08), r32(m, base+0x0c), r32(m, base+0x10), r32(m, base+0x14))
	}
	fmt.Println("--- prefetch engine ---")
	fmt.Printf("PREFETCH_CONFIG1 = 0x%08x  (bit0 ENGINEENABLE, bit7 FIFOTHRESHOLD.., bit24 PFPWENROUNDROBIN, bit30 SYNCHROMODE)\n", r32(m, 0x1e0))
	fmt.Printf("PREFETCH_CONFIG2 = 0x%08x  (TRANSFERCOUNT)\n", r32(m, 0x1e4))
	fmt.Printf("PREFETCH_CONTROL = 0x%08x  (bit0 STARTENGINE)\n", r32(m, 0x1ec))
	fmt.Printf("PREFETCH_STATUS  = 0x%08x  (COUNTVALUE / FIFOPOINTER)\n", r32(m, 0x1f0))
	syscall.Munmap(m)

	if len(os.Args) >= 3 && os.Args[1] == "data" {
		base, _ := strconv.ParseInt(os.Args[2], 0, 64)
		n := 32
		if len(os.Args) >= 4 {
			if v, e := strconv.Atoi(os.Args[3]); e == nil {
				n = v
			}
		}
		dm, err := mapRO(base, pageLen)
		if err != nil {
			fmt.Printf("map data window 0x%x failed: %v\n", base, err)
			return
		}
		fmt.Printf("--- read-only dump @0x%08x (%d x uint16) ---\n", base, n)
		for i := 0; i < n; i++ {
			fmt.Printf("0x%02x=0x%04x ", i, *(*uint16)(unsafe.Pointer(&dm[i*2])))
			if i%8 == 7 {
				fmt.Println()
			}
		}
		fmt.Println()
		syscall.Munmap(dm)
	}
}
