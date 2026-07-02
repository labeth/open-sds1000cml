// Package gpmc provides READ-ONLY access to the FPGA registers over the
// inherited /dev/Gpmc descriptor, exactly as spec 01 §1.2 defines the 6-byte
// ioctl encoding.
//
// The agent deliberately implements no write path: every GPMC write belongs to
// the app's single bus owner (spec 01 §3). The agent reads only plain,
// always-complete registers (version 0x12, fill 0x46, status 0x38/0x39) during
// the takeover idle-confirm, which is explicitly done while the factory app is
// still alive, post-STOP (spec 01 §6). Sample ports are never touched.
package gpmc

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	reqRead = 0x80026700 // ioctl request code: register read (spec 01 §1.2)

	PlaneCS1 = 1

	SelVersion = 0x12 // FPGA version register; must read 0x0052
	SelStatus  = 0x38 // held state shows ~0x8a after factory STOP (spec 01 §6)
	SelFill    = 0x46 // 11-bit sample-write counter (mask 0x07ff)

	VersionMagic = 0x0052
	FillMask     = 0x07ff
)

// Reader reads CS1 registers on a raw inherited fd. It never closes the fd.
type Reader struct {
	fd int
}

// NewReader wraps an inherited descriptor number. fd < 0 is allowed and makes
// every read fail cleanly (dev machine / fd not inherited).
func NewReader(fd int) *Reader { return &Reader{fd: fd} }

func (r *Reader) OK() bool { return r.fd >= 0 }

// EncodeAccess builds the 6-byte ioctl struct for a register access
// (spec 01 §1.2): b[0]=plane (1=CS1, 3=CS3 — NEVER 0: index underflow stalls
// the bus for seconds), b[1]=0, selector un-shifted in b[2..3], value
// little-endian in b[4..5] (writes only; 0 for reads).
func EncodeAccess(plane uint8, sel, val uint16) [6]byte {
	return [6]byte{plane, 0, byte(sel), byte(sel >> 8), byte(val), byte(val >> 8)}
}

// DecodeValue extracts the little-endian value a read returns in b[4..5].
func DecodeValue(b [6]byte) uint16 { return uint16(b[4]) | uint16(b[5])<<8 }

// Read reads one 16-bit register from the given plane. b[0]=plane (1=CS1,
// 3=CS3 — NEVER 0: index underflow stalls the bus for seconds), selector
// un-shifted in b[2..3], value returned little-endian in b[4..5].
func (r *Reader) Read(plane uint8, sel uint16) (uint16, error) {
	if r.fd < 0 {
		return 0, fmt.Errorf("gpmc: no inherited fd")
	}
	if plane != 1 && plane != 3 {
		return 0, fmt.Errorf("gpmc: invalid plane %d", plane)
	}
	b := EncodeAccess(plane, sel, 0)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(r.fd), reqRead, uintptr(unsafe.Pointer(&b[0])))
	if errno != 0 {
		return 0, fmt.Errorf("gpmc: ioctl read sel=0x%02x: %v", sel, errno)
	}
	return uint16(b[4]) | uint16(b[5])<<8, nil
}

// VerifyVersion confirms the CS1 window responds (selector 0x12 == 0x0052).
func (r *Reader) VerifyVersion() (uint16, bool) {
	v, err := r.Read(PlaneCS1, SelVersion)
	return v, err == nil && v == VersionMagic
}

// FillFrozen reports whether the fill counter 0x46 holds still across
// `pairs` consecutive read pairs spaced `gap` apart — the reliable
// engine-halted signal (spec 01 §6: a frozen 0x46, not a status bit).
func (r *Reader) FillFrozen(pairs int, gap time.Duration) (bool, []uint16, error) {
	var seen []uint16
	prev, err := r.Read(PlaneCS1, SelFill)
	if err != nil {
		return false, nil, err
	}
	prev &= FillMask
	seen = append(seen, prev)
	for i := 0; i < pairs; i++ {
		time.Sleep(gap)
		v, err := r.Read(PlaneCS1, SelFill)
		if err != nil {
			return false, seen, err
		}
		v &= FillMask
		seen = append(seen, v)
		if v != prev {
			return false, seen, nil
		}
		prev = v
	}
	return true, seen, nil
}
