// EDMA/sDMA fast drain for the owned fabric's frozen-record BURST port.
//
// The auto-inc BURST port (SEL_BURST, 0x40) pops one 16-bit word per real GPMC
// read cycle. The CPU can't drive those cycles fast: a plain /dev/mem mmap read
// of the fixed address is served from the GPMC read buffer WITHOUT re-strobing
// (never pops — see BurstInto), and the ioctl path is a syscall per word (~2.5us,
// ~0.8 MB/s). EDMA is the answer: the DMA engine is a bus master, so each of its
// reads is a real GPMC cycle that pops the port, at the async bus rate with ZERO
// per-word CPU. SRC = the BURST port (fixed), DST = a pinned RAM buffer, AB-sync,
// one transfer per physical destination page (the burst pointer advances
// continuously across chunks). Validated byte-correct (advancement + structure)
// at ~21 MB/s CPU-free on this AM3352 vs ~0.8 MB/s ioctl.
//
// EDMA is a shared engine; the kernel's davinci-edma driver is idle on this build
// (0 interrupts). We use channel 40 and trigger via shadow region 0 (DRAE-granted).
// If anything fails to initialize, the drainer is nil and BurstInto stays on ioctl.
package bus

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"open-sds/app/internal/iface"
)

const (
	edmaCCBase = 0x49000000 // EDMA3CC (TPCC) physical base (AM335x)
	edmaCCLen  = 0x8000
	edmaChan   = 40 // DMA channel (idle on this build; DMAQNUM -> queue1 -> TC1)
	pageSize   = 4096
	wordsPage  = pageSize / 2 // 2048 words (16-bit) per physical page
)

// burstPortPhys is the physical address of the auto-inc BURST port: CS1 base +
// (SEL_BURST << 1). EDMA reads a fixed source, so no fabric alias is needed.
var burstPortPhys = uint32(cs1PhysBase + int(iface.SelBURST)<<1)

type edmaDrainer struct {
	cc       []byte   // /dev/mem map of the EDMA3CC register block
	buf      []byte   // pinned DMA destination (contiguous virtual, scattered phys)
	physes   []uint32 // physical base of each destination page
	maxWords int
}

func (e *edmaDrainer) ew(off, v uint32) { *(*uint32)(unsafe.Pointer(&e.cc[off])) = v }
func (e *edmaDrainer) er(off uint32) uint32 {
	return *(*uint32)(unsafe.Pointer(&e.cc[off]))
}

// physOf resolves a pinned page's physical address via /proc/self/pagemap.
func physOf(pm *os.File, p unsafe.Pointer) uint32 {
	v := uintptr(p)
	var b [8]byte
	if _, err := pm.ReadAt(b[:], int64(v/pageSize)*8); err != nil {
		return 0
	}
	ent := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
	if ent&(1<<63) == 0 { // page not present
		return 0
	}
	return uint32((ent&((1<<55)-1))*pageSize + uint64(v%pageSize))
}

// newEDMADrainer maps the EDMA controller and allocates+pins a maxWords-word DMA
// buffer, precomputing each page's physical address. Any failure returns an error
// and the caller stays on the ioctl drain.
func newEDMADrainer(maxWords int) (*edmaDrainer, error) {
	f, err := os.OpenFile("/dev/mem", os.O_RDWR|syscall.O_SYNC, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cc, err := syscall.Mmap(int(f.Fd()), edmaCCBase, edmaCCLen,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("edma cc mmap: %w", err)
	}
	nbytes := (maxWords*2 + pageSize - 1) &^ (pageSize - 1)
	buf, err := syscall.Mmap(-1, 0, nbytes, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		syscall.Munmap(cc)
		return nil, fmt.Errorf("edma buf mmap: %w", err)
	}
	for i := range buf { // fault in every page before locking/resolving
		buf[i] = 0
	}
	if err := syscall.Mlock(buf); err != nil {
		syscall.Munmap(cc)
		syscall.Munmap(buf)
		return nil, fmt.Errorf("edma mlock: %w", err)
	}
	pm, err := os.Open("/proc/self/pagemap")
	if err != nil {
		syscall.Munmap(cc)
		syscall.Munmap(buf)
		return nil, err
	}
	defer pm.Close()
	npages := nbytes / pageSize
	physes := make([]uint32, npages)
	for pg := 0; pg < npages; pg++ {
		physes[pg] = physOf(pm, unsafe.Pointer(&buf[pg*pageSize]))
		if physes[pg] == 0 {
			syscall.Munmap(cc)
			syscall.Munmap(buf)
			return nil, fmt.Errorf("edma pagemap resolve failed (need root)")
		}
	}
	return &edmaDrainer{cc: cc, buf: buf, physes: physes, maxWords: maxWords}, nil
}

// runParam programs the channel PaRAM for a fixed-source (bcnt x 2-byte) transfer
// into dst, triggers it via shadow region 0, and waits for completion. Returns
// false on timeout.
func (e *edmaDrainer) runParam(src, dst, bcnt uint32) bool {
	const ch = edmaChan
	pb := uint32(0x4000 + ch*0x20)
	opt := uint32(0x4) | (ch << 12) | (1 << 20) // SYNCDIM(AB) | TCC=ch | TCINTEN
	e.ew(pb+0x00, opt)
	e.ew(pb+0x04, src)
	e.ew(pb+0x08, 2|(bcnt<<16)) // ACNT=2, BCNT=bcnt
	e.ew(pb+0x0C, dst)
	e.ew(pb+0x10, 0|(2<<16)) // SRCBIDX=0 (fixed src), DSTBIDX=2
	e.ew(pb+0x14, 0x0000FFFF)
	e.ew(pb+0x18, 0)
	e.ew(pb+0x1C, 1) // CCNT=1
	e.ew(0x0100+ch*4, ch<<5) // DCHMAP: channel -> PaRAM set ch (PAENTRY bits[13:5])
	hi := uint32(0)
	if ch >= 32 {
		hi = 4
	}
	cb := uint32(1) << (ch % 32)
	if hi == 4 {
		e.ew(0x0344, e.er(0x0344)|cb) // DRAEH0: grant region-0 access
	} else {
		e.ew(0x0340, e.er(0x0340)|cb)
	}
	r0 := uint32(0x2000) // shadow region 0 (the MPU region)
	e.ew(r0+0x70+hi, cb) // ICR: clear stale completion
	e.ew(r0+0x30+hi, cb) // EESR: enable event
	e.ew(r0+0x10+hi, cb) // ESR: trigger
	for i := 0; i < 500000; i++ {
		if e.er(r0+0x68+hi)&cb != 0 { // IPR: complete
			e.ew(r0+0x70+hi, cb)
			return true
		}
	}
	return false
}

// drain drains n frozen record words from the BURST port into the pinned buffer
// via EDMA (one transfer per physical page), then splits into c1 (hi byte) and c2
// (lo byte). Returns false if EDMA fails; the caller falls back to ioctl.
func (e *edmaDrainer) drain(c1, c2 []uint8, n int) bool {
	if n > e.maxWords {
		return false
	}
	for base := 0; base < n; base += wordsPage {
		w := wordsPage
		if base+w > n {
			w = n - base
		}
		if !e.runParam(burstPortPhys, e.physes[base/wordsPage], uint32(w)) {
			return false
		}
	}
	for i := 0; i < n; i++ {
		c1[i] = e.buf[i*2+1] // hi byte = C1
		c2[i] = e.buf[i*2]   // lo byte = C2
	}
	return true
}
