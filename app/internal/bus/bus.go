// Package bus is the app's GPMC register-access layer for the acq2 default
// fabric (the acq2 analysis branch §2, fpga-specs 10/12). It owns the
// boot-inherited /dev/Gpmc fd, the 6-byte ioctl wire encoding, and the EDMA
// fast drain of the pop-on-read BURST port. Register MEANING comes from the
// generated iface package: which selectors exist and which are writable is
// schema-derived, never a hand-maintained range.
//
// Planes: CS1 is the Cyclone (our fabric, 128 selectors at CS1 base + 2N);
// CS3 is the MAX V CPLD (front-end DACs, the LED latch and the configuration
// port 0x07 — fpga-specs 05 §2.2: nCS3 reaches no Cyclone ball). CS3 0x07 is
// never written here: only fpgaload's reload path may touch it (workplan §4.4).
//
// Every method on *Dev must be called from the single engine-owner goroutine
// only. The package does not enforce that; the engine does (Engine.Exec is the
// door for everyone else).
package bus

import (
	"fmt"
	"syscall"
	"unsafe"

	"open-sds/app/internal/iface"
)

// Bus is the register surface the acquisition engine (and, through
// Engine.Exec, the diagnostic block) drives. Implementations: *Dev (real
// hardware) and the engine's offline fake fabric.
type Bus interface {
	// Read reads one 16-bit register. plane is PlaneCS1 or PlaneCS3.
	Read(plane uint8, sel uint16) (uint16, error)
	// Write writes one 16-bit register. Refused (error, nothing sent) for any
	// CS1 selector the schema marks read-only or does not define, and for the
	// CS3 configuration port.
	Write(plane uint8, sel, val uint16) error
	// RawWrite writes a CS1 selector WITHOUT the schema guard — the diagnostic
	// vendor-sequence path (06-TIERS §2: the vendor arm/halt words 0x21/0x57
	// stay undecoded, so the replay is inert on our map). Never CS3.
	RawWrite(sel, val uint16) error
	// BurstInto pops n record words from the BURST port in one pass (hi byte =
	// CH1, lo byte = CH2). Post-HALT only; every read pops one word.
	BurstInto(c1, c2 []uint8, n int)
	// PopWords pops n raw words from a pop-on-read port (BURST, SNAP_POP,
	// ENV_DATA) into dst. One real GPMC transaction per word.
	PopWords(sel uint16, dst []uint16, n int)
	// FastDrain reports whether BurstInto runs CPU-free over EDMA.
	FastDrain() bool
}

const (
	reqRead  = 0x80026700 // ioctl request: register read (6-byte record)
	reqWrite = 0x40026701 // ioctl request: register write

	PlaneCS1 = 1
	PlaneCS3 = 3

	// CS3ConfigPort is the MAX V configuration port (DCLK/DATA0/nCONFIG write
	// bits, nSTATUS/CONF_DONE read bits). Reading it is always allowed; a write
	// with bit 1 low collapses the running fabric — only fpgaload writes it.
	CS3ConfigPort uint16 = 0x07

	cs1PhysBase = 0x01000000 // CS1 window physical base (fpga-specs 05 §2.3)
)

// Dev drives the real GPMC through the boot-inherited /dev/Gpmc fd. The fd is
// held as a raw int and is never closed (closing frees the FPGA chip select
// for the whole process tree).
type Dev struct {
	fd   int
	edma *edmaDrainer // nil → ioctl drains
}

// New wraps the inherited /dev/Gpmc fd. It only constructs: at cold boot the
// fabric holds the factory image (or nothing), so nothing about the register
// map can be verified here — fpgaload.Bringup does that and reloads on
// mismatch, and EnableEDMA runs only after the identity check passed.
func New(fd int) (*Dev, error) {
	if fd < 0 {
		return nil, fmt.Errorf("bus: no inherited gpmc fd")
	}
	return &Dev{fd: fd}, nil
}

// Writable is the write guard: CS1 selectors are writable exactly when the
// schema says so (iface.BySel masks the selector like the fabric does); CS3
// selectors are the MAX V front-end registers, all writable except the
// configuration port.
func Writable(plane uint8, sel uint16) bool {
	switch plane {
	case PlaneCS1:
		r, ok := iface.BySel(sel)
		return ok && r.Access.CanWrite()
	case PlaneCS3:
		return sel != CS3ConfigPort
	}
	return false
}

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

func validPlane(plane uint8) bool { return plane == PlaneCS1 || plane == PlaneCS3 }

func (d *Dev) Read(plane uint8, sel uint16) (uint16, error) {
	if !validPlane(plane) {
		// plane 0 underflows the driver's base index and stalls the bus for
		// seconds — reject before the syscall.
		return 0, fmt.Errorf("bus: invalid plane %d", plane)
	}
	b := encode(plane, sel, 0)
	if err := d.ioctl(reqRead, &b); err != nil {
		return 0, fmt.Errorf("bus: read cs%d sel %#04x: %w", plane, sel, err)
	}
	return uint16(b[4]) | uint16(b[5])<<8, nil
}

func (d *Dev) Write(plane uint8, sel, val uint16) error {
	if !validPlane(plane) {
		return fmt.Errorf("bus: invalid plane %d", plane)
	}
	if !Writable(plane, sel) {
		return fmt.Errorf("bus: write to non-writable register cs%d sel %#04x", plane, sel)
	}
	return d.rawWrite(plane, sel, val)
}

// RawWrite bypasses the schema guard on CS1 only: the E1 vendor-word replay.
// The fabric decodes A1..A7 (schema v3, 128 selectors) and keeps the vendor
// arm/halt words 0x21 / 0x57 undecoded, so the replay changes nothing.
func (d *Dev) RawWrite(sel, val uint16) error { return d.rawWrite(PlaneCS1, sel, val) }

func (d *Dev) rawWrite(plane uint8, sel, val uint16) error {
	b := encode(plane, sel, val)
	if err := d.ioctl(reqWrite, &b); err != nil {
		return fmt.Errorf("bus: write cs%d sel %#04x: %w", plane, sel, err)
	}
	return nil
}

// BurstInto drains the frozen record from the pop-on-read BURST port. The
// EDMA path is CPU-free and byte-exact once the CS1 cycle-to-cycle gap is in
// place (edma.go); it falls through to one ioctl per word on any failure.
// A /dev/mem CPU mmap of the port is deliberately NOT a drain path: repeated
// CPU reads of one address are served from the GPMC read buffer without a
// fresh nOE strobe, so the port never pops (fpga-specs 12 §5.5, owned-fpga
// 4770a81).
func (d *Dev) BurstInto(c1, c2 []uint8, n int) {
	if n <= 0 {
		return
	}
	if d.edma != nil && d.edma.drain(c1, c2, n) {
		return
	}
	for i := 0; i < n; i++ {
		v, _ := d.Read(PlaneCS1, iface.SelBurst)
		c1[i] = uint8(v >> 8)
		c2[i] = uint8(v)
	}
}

// PopWords pops n words from any pop-on-read port; the BURST port (and its
// alias) takes the EDMA path when available.
func (d *Dev) PopWords(sel uint16, dst []uint16, n int) {
	if n <= 0 {
		return
	}
	if d.edma != nil && (iface.MaskSel(sel) == iface.SelBurst || iface.MaskSel(sel) == iface.SelBurstAlias) &&
		d.edma.drainWords(burstPortPhys, dst, n) {
		return
	}
	for i := 0; i < n; i++ {
		v, _ := d.Read(PlaneCS1, sel)
		dst[i] = v
	}
}

// FastDrain reports whether the EDMA drain is active.
func (d *Dev) FastDrain() bool { return d.edma != nil }

// EnableEDMA sets up the EDMA fast drain sized for maxWords record words and
// programs the CS1 cycle-to-cycle gap it depends on. MUST be called only after
// the fabric identity is verified (the BURST port exists only on our image).
// Any failure keeps the ioctl drain (logged through logf, non-fatal). Returns
// whether EDMA is active; idempotent.
func (d *Dev) EnableEDMA(maxWords int, logf func(string, ...any)) bool {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if d.edma != nil {
		return true
	}
	e, err := newEDMADrainer(maxWords, logf)
	if err != nil {
		logf("bus: EDMA drain unavailable, using ioctl drain: %v", err)
		return false
	}
	// REQUIRED for correctness: without an inactive nOE interval between
	// back-to-back GPMC reads the pop port sees no fresh strobe (dups/reorders,
	// fpga-specs 12 §5.5). Gap 5 is the shipped value (4 is the first clean
	// one; 5 adds one clock of margin). If the gap cannot be programmed, drop
	// EDMA rather than drain corrupt records.
	if err := programCS1CycleGap(cs1CycleGap); err != nil {
		e.close()
		logf("bus: CS1 cycle-gap setup failed, using ioctl drain: %v", err)
		return false
	}
	d.edma = e
	logf("bus: EDMA drain enabled (channel %d, %d words, CS1 cycle-gap=%d, coherency=%s)",
		edmaChan, maxWords, cs1CycleGap, e.coherency())
	return true
}
