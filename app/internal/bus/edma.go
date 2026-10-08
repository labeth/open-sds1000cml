// EDMA fast drain of the pop-on-read BURST port (fpga-specs 12 §5.5–5.7;
// idea and the two bug fixes from owned-fpga app/internal/bus/edma.go,
// commits 538e855 4770a81 25ff779 4e5ea99 — re-implemented here with the
// register block, the page allocator and the cache invalidate injected so the
// programming sequence is unit-tested against a fake mapping).
//
// Why EDMA: the BURST port pops one word per real GPMC read cycle. A CPU
// /dev/mem read of the fixed address is served from the GPMC read buffer
// without re-strobing (never pops), and the ioctl path costs a syscall per
// word (~0.8 MB/s). The EDMA engine is a bus master: each of its reads is a
// real CS1 cycle, at ~11 MB/s with zero per-word CPU.
//
// Two correctness rules, both bench-proven on this AM3352:
//  1. nOE never de-asserts between pipelined reads unless GPMC_CONFIG6_1
//     carries a same-CS cycle-to-cycle gap (gpmccfg.go, CYCLE2CYCLEDELAY=5).
//  2. The EDMA is not cache-coherent: a reused buffer reads back stale lines.
//     Either invalidate through /dev/dcinv (persistent buffer) or use a fresh,
//     never-read buffer per drain.
//
// ENGMODEL-OWNER-UNIT: FU-APP-BUS
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
	edmaChan   = 40 // idle on this kernel build; DMAQNUM → queue1 → TC1
	pageSize   = 4096
	wordsPage  = pageSize / 2 // 16-bit words per physical page

	// TPCC register offsets (AM335x TRM 11.4).
	offDCHMAP   = 0x0100 // DCHMAP[ch] = PaRAM set << 5 (PAENTRY bits [13:5])
	offDRAE0    = 0x0340 // shadow region 0 access enable, channels 0..31
	offDRAEH0   = 0x0344 // channels 32..63
	offShadow0  = 0x2000 // shadow region 0 (the MPU region; the global region silently no-ops)
	offESR      = 0x10   // event set
	offEESR     = 0x30   // event enable set
	offIPR      = 0x68   // interrupt pending
	offICR      = 0x70   // interrupt clear
	offPaRAM    = 0x4000 // PaRAM base; set n at +n*0x20
	paramSize   = 0x20
	optSyncAB   = 1 << 2
	optTCINTEN  = 1 << 20
	linkNone    = 0x0000FFFF
	pollMaxDflt = 500000

	dcinvIOC = 0x40084401 // _IOW('D', 1, struct{unsigned long addr,len})
)

// burstPortPhys is the physical address of the BURST port: CS1 base +
// (selector << 1). The fabric decodes A3..A7, so the alias at 0x00 would work
// too; the schema port is used so the source is the documented one.
var burstPortPhys = uint32(cs1PhysBase + int(iface.SelBurst)<<1)

// regs32 is a 32-bit register block (the TPCC mapping, or a fake in tests).
// TRLC-LINKS: REQ-SDS-081
type regs32 interface {
	R(off uint32) uint32
	W(off, v uint32)
}

// memRegs is a /dev/mem mapping.
// TRLC-LINKS: REQ-SDS-081
type memRegs struct{ m []byte }

// TRLC-LINKS: REQ-SDS-081
func (r memRegs) R(off uint32) uint32 { return *(*uint32)(unsafe.Pointer(&r.m[off])) }

// TRLC-LINKS: REQ-SDS-081
func (r memRegs) W(off, v uint32) { *(*uint32)(unsafe.Pointer(&r.m[off])) = v }

// TRLC-LINKS: REQ-SDS-081
func (r memRegs) close() { syscall.Munmap(r.m) }

// dmaBuf is a pinned buffer with the physical address of every page.
// TRLC-LINKS: REQ-SDS-081
type dmaBuf struct {
	buf     []byte
	phys    []uint32 // per page
	release func()
}

// pager allocates pinned, physically-resolved buffers.
// TRLC-LINKS: REQ-SDS-081
type pager interface {
	alloc(nbytes int) (*dmaBuf, error)
}

// TRLC-LINKS: REQ-SDS-081
type edmaDrainer struct {
	cc       regs32
	pg       pager
	maxWords int
	pollMax  int
	// inv invalidates the D-cache lines of a buffer before readback (the
	// /dev/dcinv path). nil → no invalidate available → a fresh buffer per drain.
	inv        func(b []byte)
	persistent *dmaBuf // reused buffer, only with inv
	closeFn    func()
}

// newEDMADrainer maps the TPCC, validates the pagemap path once, and picks the
// coherency strategy. Any failure returns an error and the caller stays on ioctl.
// TRLC-LINKS: REQ-SDS-081
func newEDMADrainer(maxWords int, logf func(string, ...any)) (*edmaDrainer, error) {
	if maxWords <= 0 {
		return nil, fmt.Errorf("edma: maxWords %d", maxWords)
	}
	f, err := os.OpenFile("/dev/mem", os.O_RDWR|syscall.O_SYNC, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := syscall.Mmap(int(f.Fd()), edmaCCBase, edmaCCLen,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("edma cc mmap: %w", err)
	}
	cc := memRegs{m}
	pm, err := os.Open("/proc/self/pagemap")
	if err != nil {
		cc.close()
		return nil, err
	}
	pg := &pagemapPager{pm: pm}
	// Validate the DMA path once with a throwaway page (pagemap needs root).
	probe, err := pg.alloc(pageSize)
	if err != nil {
		cc.close()
		pm.Close()
		return nil, err
	}
	probe.release()
	d := &edmaDrainer{cc: cc, pg: pg, maxWords: maxWords, pollMax: pollMaxDflt}
	d.closeFn = func() {
		if d.persistent != nil {
			d.persistent.release()
		}
		cc.close()
		pm.Close()
	}
	if fd, err := syscall.Open("/dev/dcinv", syscall.O_RDWR, 0); err == nil {
		if buf, err := pg.alloc(maxWords * 2); err == nil {
			d.persistent = buf
			d.inv = func(b []byte) {
				arg := [2]uint32{uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b))}
				// The local driver only invalidates this bounded buffer's cache
				// lines. It does not wait for DMA or any other device operation.
				syscall.RawSyscall(syscall.SYS_IOCTL, uintptr(fd), dcinvIOC, uintptr(unsafe.Pointer(&arg[0])))
			}
			prev := d.closeFn
			d.closeFn = func() { prev(); syscall.Close(fd) }
			logf("bus: EDMA coherency via /dev/dcinv (persistent %d-word buffer)", maxWords)
		} else {
			syscall.Close(fd)
			logf("bus: /dev/dcinv present but pinned buffer failed (%v) — fresh buffer per drain", err)
		}
	} else {
		logf("bus: no /dev/dcinv — fresh cache-cold buffer per drain")
	}
	return d, nil
}

// TRLC-LINKS: REQ-SDS-081
func (e *edmaDrainer) close() {
	if e.closeFn != nil {
		e.closeFn()
	}
}

// TRLC-LINKS: REQ-SDS-081
func (e *edmaDrainer) coherency() string {
	if e.inv != nil {
		return "dcinv"
	}
	return "fresh-buffer"
}

// pagemapPager: mmap(MAP_ANON) + mlock + /proc/self/pagemap PFN resolution.
// The buffer is cache-cold from the CPU's point of view (nothing has read it),
// which is what makes the no-dcinv path coherent.
// TRLC-LINKS: REQ-SDS-081
type pagemapPager struct{ pm *os.File }

// TRLC-LINKS: REQ-SDS-081
func (p *pagemapPager) alloc(nbytes int) (*dmaBuf, error) {
	nbytes = (nbytes + pageSize - 1) &^ (pageSize - 1)
	buf, err := syscall.Mmap(-1, 0, nbytes, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		return nil, fmt.Errorf("edma buf mmap: %w", err)
	}
	if err := syscall.Mlock(buf); err != nil { // fault + pin (zero-filled by MAP_ANON)
		syscall.Munmap(buf)
		return nil, fmt.Errorf("edma mlock: %w", err)
	}
	npages := nbytes / pageSize
	phys := make([]uint32, npages)
	for pg := 0; pg < npages; pg++ {
		phys[pg] = physOf(p.pm, unsafe.Pointer(&buf[pg*pageSize]))
		if phys[pg] == 0 {
			syscall.Munmap(buf)
			return nil, fmt.Errorf("edma pagemap resolve failed (need root)")
		}
	}
	return &dmaBuf{buf: buf, phys: phys, release: func() { syscall.Munmap(buf) }}, nil
}

// physOf resolves a pinned page's physical address via /proc/self/pagemap.
// TRLC-LINKS: REQ-SDS-081
func physOf(pm *os.File, p unsafe.Pointer) uint32 {
	v := uintptr(p)
	var b [8]byte
	if _, err := pm.ReadAt(b[:], int64(v/pageSize)*8); err != nil {
		return 0
	}
	var ent uint64
	for i := 7; i >= 0; i-- {
		ent = ent<<8 | uint64(b[i])
	}
	if ent&(1<<63) == 0 { // page not present
		return 0
	}
	return uint32((ent&((1<<55)-1))*pageSize + uint64(v%pageSize))
}

// runParam programs channel edmaChan's PaRAM for a fixed-source (bcnt × 2-byte)
// AB-synchronised transfer into dst, triggers it through shadow region 0 and
// waits for completion. Returns false on timeout.
// TRLC-LINKS: REQ-SDS-081
func (e *edmaDrainer) runParam(src, dst, bcnt uint32) bool {
	const ch = uint32(edmaChan)
	pb := uint32(offPaRAM + ch*paramSize)
	e.cc.W(pb+0x00, optSyncAB|(ch<<12)|optTCINTEN) // OPT: SYNCDIM=AB, TCC=ch, TCINTEN
	e.cc.W(pb+0x04, src)                           // SRC (fixed)
	e.cc.W(pb+0x08, 2|(bcnt<<16))                  // ACNT=2, BCNT=bcnt
	e.cc.W(pb+0x0C, dst)                           // DST
	e.cc.W(pb+0x10, 0|(2<<16))                     // SRCBIDX=0, DSTBIDX=2
	e.cc.W(pb+0x14, linkNone)                      // LINK=none, BCNTRLD=0
	e.cc.W(pb+0x18, 0)                             // SRCCIDX=DSTCIDX=0
	e.cc.W(pb+0x1C, 1)                             // CCNT=1
	e.cc.W(offDCHMAP+ch*4, ch<<5)                  // DCHMAP: channel → PaRAM set ch
	hi := uint32(0)
	drae := uint32(offDRAE0)
	if ch >= 32 {
		hi, drae = 4, offDRAEH0
	}
	cb := uint32(1) << (ch % 32)
	e.cc.W(drae, e.cc.R(drae)|cb)     // grant region-0 access
	e.cc.W(offShadow0+offICR+hi, cb)  // clear a stale completion
	e.cc.W(offShadow0+offEESR+hi, cb) // enable the event
	e.cc.W(offShadow0+offESR+hi, cb)  // trigger
	for i := 0; i < e.pollMax; i++ {
		if e.cc.R(offShadow0+offIPR+hi)&cb != 0 {
			e.cc.W(offShadow0+offICR+hi, cb)
			return true
		}
	}
	return false
}

// drainWords drains n words from the fixed pop port at src into dst — one
// transfer per physical page (pages are not contiguous; the fabric pointer
// advances continuously across transfers). Returns false on any failure so the
// caller falls back to ioctl. dst must have len ≥ n.
// TRLC-LINKS: REQ-SDS-081
func (e *edmaDrainer) drainWords(src uint32, dst []uint16, n int) bool {
	if n <= 0 || n > e.maxWords || len(dst) < n {
		return false
	}
	b := e.persistent
	if b == nil {
		var err error
		if b, err = e.pg.alloc(n * 2); err != nil {
			return false
		}
		defer b.release()
	}
	for base := 0; base < n; base += wordsPage {
		w := wordsPage
		if base+w > n {
			w = n - base
		}
		if !e.runParam(src, b.phys[base/wordsPage], uint32(w)) {
			return false
		}
	}
	if e.inv != nil {
		e.inv(b.buf[:n*2])
	}
	for i := 0; i < n; i++ {
		dst[i] = uint16(b.buf[i*2]) | uint16(b.buf[i*2+1])<<8 // little-endian word
	}
	return true
}

// TRLC-LINKS: REQ-SDS-081
func (e *edmaDrainer) drainBytes(src uint32, dst []byte) bool {
	n := len(dst) / 2
	if len(dst)%2 != 0 || n <= 0 || n > e.maxWords {
		return false
	}
	b := e.persistent
	if b == nil {
		var err error
		if b, err = e.pg.alloc(n * 2); err != nil {
			return false
		}
		defer b.release()
	}
	for base := 0; base < n; base += wordsPage {
		w := wordsPage
		if base+w > n {
			w = n - base
		}
		if !e.runParam(src, b.phys[base/wordsPage], uint32(w)) {
			return false
		}
	}
	if e.inv != nil {
		e.inv(b.buf[:n*2])
	}
	copy(dst, b.buf[:n*2])
	return true
}

// drain drains n BURST words and splits them (hi byte = CH1, lo byte = CH2).
// TRLC-LINKS: REQ-SDS-081
func (e *edmaDrainer) drain(c1, c2 []uint8, n int) bool {
	if n <= 0 || n > e.maxWords || len(c1) < n || len(c2) < n {
		return false
	}
	b := e.persistent
	if b == nil {
		var err error
		if b, err = e.pg.alloc(n * 2); err != nil {
			return false
		}
		defer b.release()
	}
	for base := 0; base < n; base += wordsPage {
		w := wordsPage
		if base+w > n {
			w = n - base
		}
		if !e.runParam(burstPortPhys, b.phys[base/wordsPage], uint32(w)) {
			return false
		}
	}
	if e.inv != nil {
		e.inv(b.buf[:n*2])
	}
	for i := 0; i < n; i++ {
		c1[i] = b.buf[i*2+1]
		c2[i] = b.buf[i*2]
	}
	return true
}
