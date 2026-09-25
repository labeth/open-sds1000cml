// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"open-sds/app/internal/bus"
)

// ─── configuration ports ───────────────────────────────────────────────────

const (
	reqRead  = 0x80026700
	reqWrite = 0x40026701

	cs3PhysBase = 0x03000000 // CS3 window (fpga-specs 05 §2.3): CS1=0x01.., CS2=0x02.., CS3=0x03..
	cs3MapLen   = 4096
	cfgByteOff  = int(bus.CS3ConfigPort) << 1 // 0x0E: the port at physical 0x0300000E
)

// ioctlPort drives CS3 0x07 through the inherited /dev/Gpmc fd — the path
// every other register access uses; ~5.9 M writes per load, ≈16 s.
// TRLC-LINKS: REQ-SDS-005
type ioctlPort struct{ fd int }

// TRLC-LINKS: REQ-SDS-005
func (p *ioctlPort) xfer(req uintptr, val uint16) (uint16, error) {
	b := [6]byte{bus.PlaneCS3, 0, byte(bus.CS3ConfigPort), 0, byte(val), byte(val >> 8)}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(p.fd), req, uintptr(unsafe.Pointer(&b[0]))); errno != 0 {
		return 0, errno
	}
	return uint16(b[4]) | uint16(b[5])<<8, nil
}

// TRLC-LINKS: REQ-SDS-005
func (p *ioctlPort) WriteCfg(v uint16) error {
	if _, err := p.xfer(reqWrite, v); err != nil {
		return fmt.Errorf("gpmc write cs3 %#04x=%#04x: %w", bus.CS3ConfigPort, v, err)
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-005
func (p *ioctlPort) ReadCfg() (uint16, error) {
	v, err := p.xfer(reqRead, 0)
	if err != nil {
		return 0, fmt.Errorf("gpmc read cs3 %#04x: %w", bus.CS3ConfigPort, err)
	}
	return v, nil
}

// memPort drives the same port through a /dev/mem O_SYNC mapping of the CS3
// window: one 16-bit store per DCLK edge, ≈2.5 s per load (tools/fpga_reload
// measured 1.18 Mbit/s). Reads go through the mapping as well (the port is a
// plain register in the MAX V; the double-shift trap is checked at open by
// comparing a mmap read against an ioctl read).
// TRLC-LINKS: REQ-SDS-005
type memPort struct {
	m    []byte
	word *uint16
}

//go:noinline
// TRLC-LINKS: REQ-SDS-005
func store16(p *uint16, v uint16) { *p = v }

//go:noinline
// TRLC-LINKS: REQ-SDS-005
func load16(p *uint16) uint16 { return *p }

// TRLC-LINKS: REQ-SDS-005
func (p *memPort) WriteCfg(v uint16) error  { store16(p.word, v); return nil }
// TRLC-LINKS: REQ-SDS-005
func (p *memPort) ReadCfg() (uint16, error) { return load16(p.word), nil }
// TRLC-LINKS: REQ-SDS-005
func (p *memPort) Close()                   { syscall.Munmap(p.m) }

// openMemPort maps the CS3 window and cross-checks it against the ioctl port:
// both must read the same configuration-port word. On any failure the caller
// stays on the ioctl port.
// TRLC-LINKS: REQ-SDS-005
func openMemPort(ref Port) (*memPort, error) {
	f, err := os.OpenFile("/dev/mem", os.O_RDWR|syscall.O_SYNC, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := syscall.Mmap(int(f.Fd()), cs3PhysBase, cs3MapLen, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("cs3 mmap: %w", err)
	}
	p := &memPort{m: m, word: (*uint16)(unsafe.Pointer(&m[cfgByteOff]))}
	want, err := ref.ReadCfg()
	if err != nil {
		p.Close()
		return nil, err
	}
	if got := load16(p.word); got != want {
		p.Close()
		return nil, fmt.Errorf("cs3 mmap verify: port reads %#04x via mmap, %#04x via ioctl", got, want)
	}
	return p, nil
}

// ─── boot entrypoint ───────────────────────────────────────────────────────

// Bringup is the boot hook (workplan §4.1): verify the fabric identity over
// CS1; on mismatch reload the embedded default image over the CS3 port and
// verify again. read is the CS1 register read; gpmcFD the inherited /dev/Gpmc
// descriptor. It must run BEFORE the engine drives the bus. A returned error
// means the fabric is not the default image and cannot be made so.
// TRLC-LINKS: REQ-SDS-004, REQ-SDS-005
func Bringup(gpmcFD int, read Reader, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if err := Verify(read); err == nil {
		logf("fpgaload: fabric already carries the default image — no reload")
		return nil
	} else {
		logf("fpgaload: %v", err)
	}
	rbf := Default()
	if len(rbf) == 0 {
		return fmt.Errorf("fpgaload: fabric is not the default image and this binary embeds no bitstream (build with `make app-release`)")
	}
	ioc := &ioctlPort{fd: gpmcFD}
	var port Port = ioc
	if mp, err := openMemPort(ioc); err != nil {
		logf("fpgaload: /dev/mem CS3 port unavailable (%v) — ioctl bit-bang (~16 s)", err)
	} else {
		defer mp.Close()
		port = mp
		logf("fpgaload: /dev/mem CS3 port verified — mmap bit-bang (~3 s)")
	}
	return EnsureDefault(read, port, rbf, Options{Logf: logf})
}

// ConfigureVolatile loads an explicitly selected non-default fabric through
// CS3 selector 7. It never opens or closes /dev/Gpmc and never programs flash.
// Callers must verify their own fabric identity after this function returns.
// TRLC-LINKS: REQ-SDS-005
func ConfigureVolatile(gpmcFD int, rbf []byte, options Options) error {
	if gpmcFD < 0 {
		return fmt.Errorf("fpgaload: inherited GPMC fd required")
	}
	p := &ioctlPort{fd: gpmcFD}
	if m, err := openMemPort(p); err == nil {
		defer m.Close()
		return Reload(m, rbf, options)
	}
	return Reload(p, rbf, options)
}

// ConfigStatus reads the configuration port through the inherited fd (a read
// never disturbs the fabric). Used by the diagnostic status line.
// TRLC-LINKS: REQ-SDS-005
func ConfigStatus(gpmcFD int) (uint16, error) {
	return (&ioctlPort{fd: gpmcFD}).ReadCfg()
}
