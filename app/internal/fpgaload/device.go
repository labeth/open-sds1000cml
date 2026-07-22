package fpgaload

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"open-sds/app/internal/iface"
)

// ─── GPMC config port (nCONFIG pulse / CONF_DONE) ────────────────────────────

const (
	// 6-byte ioctl request codes for /dev/Gpmc, identical to the register bus
	// (bus.reqRead/reqWrite). The config port is reached raw here, not through
	// bus.Write: pulsing nCONFIG is a write to the read-only CONF_DONE selector
	// (bus.Writable would reject it), and the fabric's register front-end is
	// down while it reconfigures.
	reqRead  = 0x80026700
	reqWrite = 0x40026701

	// nCONFIG pulse values written to the CS3 CONF_DONE selector. The register
	// map documents only the READ (CONF_DONE.DONE); which written bits drive
	// nCONFIG is a bench ASSUMPTION — drive a low->high edge to force a fresh
	// passive-serial cycle, with a low hold and a post-release settle
	// (Cyclone IV tCFG / nSTATUS recovery). Verify the mapping and timings on
	// real silicon.
	nconfigAssertLow = 0x0000
	nconfigReleaseHi = 0x0001
	nconfigLowHold   = 2 * time.Millisecond
	nconfigSettle    = 5 * time.Millisecond
)

// gpmcConfigPort drives the config port over a /dev/Gpmc fd. The fd is shared
// with the register bus but never used concurrently: Bringup runs before the
// engine starts.
type gpmcConfigPort struct {
	fd    int
	sleep func(time.Duration)
}

func (g *gpmcConfigPort) rawIoctl(req uintptr, b *[6]byte) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(g.fd), req, uintptr(unsafe.Pointer(&b[0])))
	if errno != 0 {
		return errno
	}
	return nil
}

// encode6 mirrors bus.encode: b[0]=plane (never 0 — index underflow stalls the
// bus), selector un-shifted in b[2..3] (the driver shifts <<1), value LE in
// b[4..5].
func encode6(plane uint8, sel, val uint16) [6]byte {
	return [6]byte{plane, 0, byte(sel), byte(sel >> 8), byte(val), byte(val >> 8)}
}

func (g *gpmcConfigPort) writeCS3(sel, val uint16) error {
	b := encode6(uint8(iface.CS3), sel, val)
	if err := g.rawIoctl(reqWrite, &b); err != nil {
		return fmt.Errorf("gpmc write cs3 %#04x=%#04x: %w", sel, val, err)
	}
	return nil
}

func (g *gpmcConfigPort) readCS3(sel uint16) (uint16, error) {
	b := encode6(uint8(iface.CS3), sel, 0)
	if err := g.rawIoctl(reqRead, &b); err != nil {
		return 0, fmt.Errorf("gpmc read cs3 %#04x: %w", sel, err)
	}
	return uint16(b[4]) | uint16(b[5])<<8, nil
}

// PulseNCONFIG drives a low->high edge on the config port.
func (g *gpmcConfigPort) PulseNCONFIG() error {
	if err := g.writeCS3(iface.SelCONF_DONE, nconfigAssertLow); err != nil {
		return err
	}
	g.sleep(nconfigLowHold)
	if err := g.writeCS3(iface.SelCONF_DONE, nconfigReleaseHi); err != nil {
		return err
	}
	g.sleep(nconfigSettle)
	return nil
}

// ConfDone reads CONF_DONE.DONE via the generated field accessor.
func (g *gpmcConfigPort) ConfDone() (bool, error) {
	v, err := g.readCS3(iface.SelCONF_DONE)
	if err != nil {
		return false, err
	}
	return iface.ConfDoneDone(v), nil
}

// ─── passive-serial data path (/dev/spidev1.1) ───────────────────────────────

const (
	// spidev write-direction (_IOW) config ioctls, matching analog/spi.go. The
	// argument is a POINTER to the value; the size nibble encodes its width.
	spiWrMode     = 0x40016b01 // SPI_IOC_WR_MODE          (u8)
	spiWrBits     = 0x40016b03 // SPI_IOC_WR_BITS_PER_WORD (u8)
	spiWrMaxSpeed = 0x40046b04 // SPI_IOC_WR_MAX_SPEED_HZ  (u32)
	spiMessage1   = 0x40206b00 // SPI_IOC_MESSAGE(1): one spi_ioc_transfer

	loaderMode = 0 // mode 0 (CPOL0/CPHA0) — passive-serial path
	loaderBits = 8 // 8-bit words

	// DefaultSpeedHz is the passive-serial DCLK. The SAME spidev node runs at
	// mode 3 / 300 kHz for the gain DAC, so Configure sets mode/bits/speed
	// EXPLICITLY before streaming and never relies on inherited defaults.
	DefaultSpeedHz = 24 * 1000 * 1000
)

// spiTransfer mirrors struct spi_ioc_transfer byte-for-byte (32 bytes), matching
// analog.spiTransfer.
type spiTransfer struct {
	txBuf       uint64
	rxBuf       uint64
	length      uint32
	speedHz     uint32
	delayUsecs  uint16
	bitsPerWord uint8
	csChange    uint8
	pad         uint32
}

// spidevLoader is the passive-serial DATA0/DCLK path. It opens its own fd on the
// shared spidev node; Bringup runs before the analog front end opens the same
// node, so there is no contention at boot.
type spidevLoader struct {
	fd    int
	speed uint32
}

// OpenSpidev opens the passive-serial loader node.
func OpenSpidev(path string, speed uint32) (*spidevLoader, error) {
	if speed == 0 {
		speed = DefaultSpeedHz
	}
	fd, err := syscall.Open(path, syscall.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s (root?): %w", path, err)
	}
	return &spidevLoader{fd: fd, speed: speed}, nil
}

func (s *spidevLoader) Close() error { return syscall.Close(s.fd) }

func (s *spidevLoader) ioctlPtr(req uintptr, p unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(s.fd), req, uintptr(p))
	if errno != 0 {
		return errno
	}
	return nil
}

// Configure sets mode 0 / 8-bit / loader clock, so the bus is programmed for the
// passive-serial load even if the fd was left at the gain-DAC settings.
func (s *spidevLoader) Configure() error {
	mode := uint8(loaderMode)
	if err := s.ioctlPtr(spiWrMode, unsafe.Pointer(&mode)); err != nil {
		return fmt.Errorf("SPI_IOC_WR_MODE: %w", err)
	}
	bits := uint8(loaderBits)
	if err := s.ioctlPtr(spiWrBits, unsafe.Pointer(&bits)); err != nil {
		return fmt.Errorf("SPI_IOC_WR_BITS_PER_WORD: %w", err)
	}
	speed := s.speed
	if err := s.ioctlPtr(spiWrMaxSpeed, unsafe.Pointer(&speed)); err != nil {
		return fmt.Errorf("SPI_IOC_WR_MAX_SPEED_HZ: %w", err)
	}
	return nil
}

// SendChunk clocks one buffer out DATA0 as a single CS-framed TX-only transfer.
func (s *spidevLoader) SendChunk(buf []byte) error {
	if len(buf) == 0 {
		return nil
	}
	tr := spiTransfer{
		txBuf:       uint64(uintptr(unsafe.Pointer(&buf[0]))),
		length:      uint32(len(buf)),
		speedHz:     s.speed,
		bitsPerWord: loaderBits,
	}
	err := s.ioctlPtr(spiMessage1, unsafe.Pointer(&tr))
	runtime.KeepAlive(buf)
	if err != nil {
		return fmt.Errorf("SPI_IOC_MESSAGE(1): %w", err)
	}
	return nil
}

// ─── boot entrypoint ─────────────────────────────────────────────────────────

// Bringup configures the fabric with the owned standard bitstream at boot and
// verifies it over the register interface. read is bus.Read; gpmcFD is the
// inherited /dev/Gpmc descriptor; spidevPath is the passive-serial loader node.
//
// It must run BEFORE the engine drives the bus and BEFORE the analog front end
// opens the shared spidev node. A returned error means the fabric is not the
// standard build and cannot be made so — the caller should refuse to drive.
func Bringup(gpmcFD int, spidevPath string, read func(iface.Plane, uint16) (uint16, error), logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	cfg := &gpmcConfigPort{fd: gpmcFD, sleep: time.Sleep}
	opts := Options{BitReverse: true, Logf: logf}

	ser, err := OpenSpidev(spidevPath, DefaultSpeedHz)
	if err != nil {
		// No loader bus: we can still run iff the fabric already happens to be
		// the standard build; otherwise we cannot configure it.
		logf("fpgaload: loader node %s unavailable (%v) — verify only", spidevPath, err)
		if verr := iface.Verify(read); verr != nil {
			return fmt.Errorf("no loader bus and fabric not standard: %w", verr)
		}
		logf("fpgaload: fabric already the standard build — continuing without loader")
		return nil
	}
	defer ser.Close()
	return EnsureStandard(read, cfg, ser, opts)
}
