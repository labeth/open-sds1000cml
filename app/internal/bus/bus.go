// Package bus is the clean-room app's GPMC register access layer (spec 01 §1,
// spec 02 §0). It owns the write side of the wire protocol — the agent's
// internal/gpmc stays read-only by design — and the /dev/mem mmap fast path
// used to drain frozen sample ports.
//
// Every method on *Dev must be called from the single engine-owner goroutine
// only (spec 01 §3). The package does not enforce that; the engine does.
package bus

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Bus is the register surface the acquisition engine drives. Implementations:
// *Dev (real hardware) and the test fake.
type Bus interface {
	// Read reads one 16-bit register. plane is 1 (CS1) or 3 (CS3).
	Read(plane uint8, sel uint16) (uint16, error)
	// Write writes one 16-bit register. plane is 1 (CS1) or 3 (CS3).
	Write(plane uint8, sel, val uint16) error
	// DrainRead reads one frozen CS1 sample port (0x30–0x34) post capture-halt.
	// One bus transaction per call — the port auto-increments per transaction.
	DrainRead(sel uint16) uint16
	// DrainWrite writes one CS1 register via the /dev/mem fast path when
	// available (falls back to ioctl). Used by the continuous-stream loop to
	// pulse the roll-FIFO latch without a syscall per sample. Refuses the same
	// forbidden registers as Write.
	DrainWrite(sel, val uint16) error
	// MmapDrain reports whether DrainRead uses the /dev/mem fast path.
	MmapDrain() bool
}

const (
	reqRead  = 0x80026700 // ioctl request: register read (spec 01 §1.2)
	reqWrite = 0x40026701 // ioctl request: register write

	PlaneCS1 = 1
	PlaneCS3 = 3

	cs1PhysBase = 0x01000000 // CS1 /dev/mem physical base (spec 02 §0.4)
	mmapLen     = 4096

	SelVersion   = 0x12
	VersionMagic = 0x0052
)

// Dev drives the real GPMC through the boot-inherited /dev/Gpmc fd. The fd is
// held as a raw int and is never closed (closing frees the FPGA chip select
// for the whole process tree, spec 01 §5).
type Dev struct {
	fd   int
	mem  []byte // /dev/mem mapping of the CS1 window; nil → ioctl drain
	regs *[mmapLen / 2]uint16
}

// New wraps the inherited /dev/Gpmc fd. If mmapDrain is set it tries to map
// the CS1 window from /dev/mem and verifies it (version selector must read
// 0x0052); on any failure it silently falls back to ioctl drains.
func New(fd int, mmapDrain bool) (*Dev, error) {
	if fd < 0 {
		return nil, fmt.Errorf("bus: no inherited gpmc fd")
	}
	d := &Dev{fd: fd}
	if _, err := d.Read(PlaneCS1, SelVersion); err != nil {
		return nil, fmt.Errorf("bus: probe read: %w", err)
	}
	if mmapDrain {
		if err := d.mapCS1(); err != nil {
			fmt.Printf("[app] mmap drain unavailable, using ioctl drain: %v\n", err)
		}
	}
	return d, nil
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
	if v := load16(&regs[SelVersion]); v != VersionMagic {
		syscall.Munmap(mem)
		return fmt.Errorf("mmap verify: version reads %#04x, want %#04x", v, VersionMagic)
	}
	d.mem, d.regs = mem, regs
	return nil
}

// load16 performs exactly one aligned 16-bit load. noinline keeps the compiler
// from splitting, hoisting, or CSE-ing the access — a sample port pops its
// FIFO once per bus transaction (spec 02 §0.4).
//
//go:noinline
func load16(p *uint16) uint16 { return *p }

// store16 is the write dual of load16: noinline so the compiler can't hoist,
// split, or drop the volatile register write.
//
//go:noinline
func store16(p *uint16, v uint16) { *p = v }

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

func (d *Dev) Read(plane uint8, sel uint16) (uint16, error) {
	if plane != PlaneCS1 && plane != PlaneCS3 {
		// plane 0 underflows the driver's base index and stalls the bus for
		// seconds — reject before the syscall (spec 01 §1A).
		return 0, fmt.Errorf("bus: invalid plane %d", plane)
	}
	b := encode(plane, sel, 0)
	if err := d.ioctl(reqRead, &b); err != nil {
		return 0, fmt.Errorf("bus: read cs%d sel %#04x: %w", plane, sel, err)
	}
	return uint16(b[4]) | uint16(b[5])<<8, nil
}

func (d *Dev) Write(plane uint8, sel, val uint16) error {
	if plane != PlaneCS1 && plane != PlaneCS3 {
		return fmt.Errorf("bus: invalid plane %d", plane)
	}
	if forbiddenWrite(plane, sel) {
		return fmt.Errorf("bus: write to forbidden register cs%d sel %#04x", plane, sel)
	}
	b := encode(plane, sel, val)
	if err := d.ioctl(reqWrite, &b); err != nil {
		return fmt.Errorf("bus: write cs%d sel %#04x: %w", plane, sel, err)
	}
	return nil
}

// forbiddenWrite guards the registers the app must never write at runtime
// (spec 02 §5): the CS3 config/nCONFIG port and the calibration banks.
func forbiddenWrite(plane uint8, sel uint16) bool {
	if plane == PlaneCS3 {
		return sel == 0x07 // config-status / nCONFIG — writing collapses the engine
	}
	switch {
	case sel >= 0x01 && sel <= 0x0f: // cal-coefficient bank
		return true
	case sel == 0x16: // cal latch strobe
		return true
	case sel >= 0x27 && sel <= 0x2a: // gain-cal words
		return true
	case sel >= 0x5a && sel <= 0x7f: // cal-coefficient bank
		return true
	}
	return false
}

func (d *Dev) DrainRead(sel uint16) uint16 {
	if d.regs != nil {
		return load16(&d.regs[sel])
	}
	v, _ := d.Read(PlaneCS1, sel)
	return v
}

func (d *Dev) DrainWrite(sel, val uint16) error {
	if forbiddenWrite(PlaneCS1, sel) {
		return fmt.Errorf("bus: write to forbidden register cs1 sel %#04x", sel)
	}
	if d.regs != nil && int(sel) < len(d.regs) {
		store16(&d.regs[sel], val)
		return nil
	}
	return d.Write(PlaneCS1, sel, val)
}

func (d *Dev) MmapDrain() bool { return d.regs != nil }
