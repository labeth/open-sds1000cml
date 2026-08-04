// Package bus is the owned FPGA's GPMC register-access layer (specs 01 §1,
// 02). It drives ONLY our fabric through the generated iface bindings: the
// register semantics (which selectors exist, which are writable, which
// auto-increment) come from iface, never from hand-packed masks or magic
// ranges. It keeps the mmap/ioctl mechanics — the boot-inherited /dev/Gpmc fd,
// the /dev/mem O_SYNC fast path, the single 16-bit load16/store16, and the
// 6-byte ioctl encode — but the wire protocol's meaning is schema-derived.
//
// Every method on *Dev must be called from the single engine-owner goroutine
// only (spec 01 §3). The package does not enforce that; the engine does.
package bus

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"open-sds/app/internal/iface"
)

// Bus is the register surface the acquisition engine drives. Implementations:
// *Dev (real hardware) and the offline fake acquisition fabric.
type Bus interface {
	// Read reads one 16-bit register through the given plane window.
	Read(plane iface.Plane, sel uint16) (uint16, error)
	// Write writes one 16-bit register. Schema-read-only selectors are refused
	// by iface.Writable (CONF_DONE, every status/drain port, unknown sels).
	Write(plane iface.Plane, sel, val uint16) error
	// BurstInto drains n frozen record words from the single fixed auto-inc
	// BURST port in one tight pass (hi byte = C1, lo byte = C2). Post-halt only;
	// the port pops one word per read. c1,c2 must each have len >= n.
	BurstInto(c1, c2 []uint8, n int)
	// ChannelInto reads n words from a result-channel auto-inc DATA port
	// (e.g. ENV_DATA) into dst. Post-halt only; the port pops one word per read.
	ChannelInto(sel uint16, dst []uint16, n int)
	// MmapDrain reports whether the auto-inc drain uses the /dev/mem fast path.
	// Always false now: the fast path is disabled (GPMC prefetch serves repeated
	// reads of the fixed auto-inc address without re-strobing CS1, so it never
	// pops). Drains are always ioctl. The /dev/mem mapping, if present, is retained
	// only for the boot-time VERSION self-check, not for I/O.
	MmapDrain() bool
}

const (
	reqRead  = 0x80026700 // ioctl request: register read (spec 01 §1.2)
	reqWrite = 0x40026701 // ioctl request: register write

	cs1PhysBase = 0x01000000 // CS1 /dev/mem physical base (spec 02 §0.4)
	mmapLen     = 4096
)

// Dev drives the real GPMC through the boot-inherited /dev/Gpmc fd. The fd is
// held as a raw int and is never closed (closing frees the FPGA chip select
// for the whole process tree, spec 01 §5).
type Dev struct {
	fd   int
	mem  []byte // /dev/mem mapping of the CS1 window; nil → ioctl drain
	regs *[mmapLen / 2]uint16
	edma *edmaDrainer // EDMA/sDMA fast drain; nil → ioctl drain
}

// New wraps the inherited /dev/Gpmc fd. It ONLY constructs the driver — it does
// NOT verify the fabric identity and does NOT map the fast-path drain. At cold
// boot the fabric still holds the factory NAND image, which must be reconfigured
// to the owned build first (fpgaload.Bringup); verifying identity here would trip
// before that reconfiguration could run. The boot sequence therefore verifies
// identity via Bringup (which reloads on mismatch) and then calls EnableMmap once
// the owned fabric is confirmed. Refusing to drive a mispaired build is preserved
// — it just lives in Bringup / the boot gate now, not in construction.
func New(fd int) (*Dev, error) {
	if fd < 0 {
		return nil, fmt.Errorf("bus: no inherited gpmc fd")
	}
	return &Dev{fd: fd}, nil
}

// EnableMmap maps the CS1 window from /dev/mem for the fast-path drain when want
// is true. It MUST be called only after the fabric is confirmed to be the owned
// build: mapCS1 checks the VERSION magic to catch the addressing double-shift
// trap, and a factory (or unconfigured) fabric would fail that check. On any
// failure it stays on ioctl drains. Returns whether the mmap fast path is active;
// idempotent.
func (d *Dev) EnableMmap(want bool) bool {
	if !want || d.regs != nil {
		return d.regs != nil
	}
	if err := d.mapCS1(); err != nil {
		fmt.Printf("[app] mmap drain unavailable, using ioctl drain: %v\n", err)
	}
	return d.regs != nil
}

// EnableEDMA sets up the EDMA/sDMA fast drain, sized for maxWords record words.
// MUST be called only after the owned fabric is confirmed (same gate as EnableMmap):
// EDMA reads the auto-inc BURST port, which only exists on the owned build. On any
// failure it stays on the ioctl drain (logged, non-fatal). Returns whether EDMA is
// active; idempotent.
func (d *Dev) EnableEDMA(maxWords int) bool {
	if d.edma != nil {
		return true
	}
	e, err := newEDMADrainer(maxWords)
	if err != nil {
		fmt.Printf("[app] EDMA drain unavailable, using ioctl drain: %v\n", err)
		return false
	}
	d.edma = e
	fmt.Printf("[app] EDMA drain enabled (channel %d, %d words)\n", edmaChan, maxWords)
	return true
}

func (d *Dev) mapCS1() error {
	f, err := os.OpenFile("/dev/mem", os.O_RDWR|syscall.O_SYNC, 0)
	if err != nil {
		return err
	}
	// The fd may be closed after mmap; the mapping stays valid.
	defer f.Close()
	mem, err := syscall.Mmap(int(f.Fd()), cs1PhysBase, mmapLen,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return err
	}
	regs := (*[mmapLen / 2]uint16)(unsafe.Pointer(&mem[0]))
	// Verify addressing before trusting the map (double-shift trap, spec 01 §1B).
	// Uses the schema-derived iface.ExpectVERSION — the same const iface.Verify
	// checks — so the mmap self-check can't drift from the fabric's VERSION magic.
	if v := load16(&regs[iface.SelVERSION]); v != iface.ExpectVERSION {
		syscall.Munmap(mem)
		return fmt.Errorf("mmap verify: version reads %#04x, want %#04x", v, iface.ExpectVERSION)
	}
	d.mem, d.regs = mem, regs
	return nil
}

// load16 performs exactly one aligned 16-bit load. noinline keeps the compiler
// from splitting, hoisting, or CSE-ing the access — an auto-inc port pops its
// FIFO once per bus transaction (spec 02 §0.4, iface.IsAutoInc).
//
//go:noinline
func load16(p *uint16) uint16 { return *p }

func encode(plane uint8, sel, val uint16) [6]byte {
	return [6]byte{plane, 0, byte(sel), byte(sel >> 8), byte(val), byte(val >> 8)}
}

func (d *Dev) ioctl(req uintptr, b *[6]byte) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(d.fd), req, uintptr(unsafe.Pointer(&b[0])))
	if errno != 0 {
		return errno
	}
	return nil
}

func validPlane(plane iface.Plane) bool { return plane == iface.CS1 || plane == iface.CS3 }

func (d *Dev) Read(plane iface.Plane, sel uint16) (uint16, error) {
	if !validPlane(plane) {
		// plane 0 underflows the driver's base index and stalls the bus for
		// seconds — reject before the syscall (spec 01 §1A).
		return 0, fmt.Errorf("bus: invalid plane %d", plane)
	}
	b := encode(uint8(plane), sel, 0)
	if err := d.ioctl(reqRead, &b); err != nil {
		return 0, fmt.Errorf("bus: read cs%d sel %#04x: %w", plane, sel, err)
	}
	return uint16(b[4]) | uint16(b[5])<<8, nil
}

func (d *Dev) Write(plane iface.Plane, sel, val uint16) error {
	if !validPlane(plane) {
		return fmt.Errorf("bus: invalid plane %d", plane)
	}
	// The write guard is entirely schema-derived: iface.Writable is false for
	// every read-only register (CONF_DONE, all status/drain/channel ports) and
	// for any selector the schema does not define. No hand-maintained ranges.
	if !iface.Writable(plane, sel) {
		return fmt.Errorf("bus: write to non-writable register cs%d sel %#04x", plane, sel)
	}
	b := encode(uint8(plane), sel, val)
	if err := d.ioctl(reqWrite, &b); err != nil {
		return fmt.Errorf("bus: write cs%d sel %#04x: %w", plane, sel, err)
	}
	return nil
}

// BurstInto drains the frozen record in one tight pass from the single fixed
// auto-inc BURST port — no per-sample interface dispatch, no modulo, no port
// cycling. Each read pops one word (hi byte C1, lo byte C2); the port
// auto-increments through samples 0..n-1 (iface.IsAutoInc(CS1, SelBURST)).
func (d *Dev) BurstInto(c1, c2 []uint8, n int) {
	// FAST PATH: EDMA/sDMA. Unlike a CPU mmap read (served from the GPMC read buffer
	// WITHOUT re-strobing → never pops), the EDMA engine is a bus master, so each of
	// its reads is a real GPMC cycle that pops the port — CPU-free, ~21 MB/s, byte-
	// validated. Falls through to ioctl on any failure.
	if d.edma != nil && d.edma.drain(c1, c2, n) {
		return
	}
	// IOCTL FALLBACK: the /dev/mem mmap CPU path is DISABLED for auto-inc drains — on
	// this AM3352 GPMC, repeated CPU reads of the fixed auto-inc address are served
	// from the GPMC read buffer WITHOUT re-strobing CS1, so the port never pops (the
	// whole record collapses to sample 0 replicated — the "ADC-dead / flat trace"
	// root cause). The ioctl path issues one real GPMC transaction per read, popping
	// correctly. Correct but ~0.8 MB/s (syscall per word).
	for i := 0; i < n; i++ {
		v, _ := d.Read(iface.CS1, iface.SelBURST)
		c1[i] = uint8(v >> 8)
		c2[i] = uint8(v)
	}
}

// ChannelInto reads n words from a result-channel auto-inc DATA port (packed
// record words) into dst. Same auto-inc discipline as BurstInto.
func (d *Dev) ChannelInto(sel uint16, dst []uint16, n int) {
	// Same GPMC-prefetch auto-inc hazard as BurstInto: always drain via ioctl so
	// each read is a real CS1 transaction that pops the port.
	for i := 0; i < n; i++ {
		v, _ := d.Read(iface.CS1, sel)
		dst[i] = v
	}
}

// MmapDrain is always false: BurstInto/ChannelInto always drain via ioctl (the
// mmap fast path never popped the auto-inc port under GPMC prefetch). d.regs may
// still be non-nil (mapped for the boot VERSION check) but is not a drain path.
func (d *Dev) MmapDrain() bool { return false }
