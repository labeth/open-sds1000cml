// Package analog drives the vertical analog front end (spec 06): the coarse
// relay word on /dev/spidev1.0 and the fine-gain DAC on /dev/spidev1.1. SPI
// is off the GPMC bus, so this package is driven directly by producers (HTTP
// handlers) under its own lock — it never touches the acquisition engine
// (spec 09 §1 control classes).
package analog

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// SPI ioctl requests (spec 06 §4.1). The write-direction _IOW codes are
// mandatory — the _IOR aliases do not program the bus.
const (
	spiWrMode     = 0x40016b01
	spiWrBits     = 0x40016b03
	spiWrMaxSpeed = 0x40046b04
	spiMessage1   = 0x40206b00 // SPI_IOC_MESSAGE(1): one spi_ioc_transfer

	spiSpeedHz = 300000
	spiMode    = 3
)

// spiTransfer mirrors struct spi_ioc_transfer (32 bytes).
type spiTransfer struct {
	txBuf       uint64
	rxBuf       uint64
	len         uint32
	speedHz     uint32
	delayUsecs  uint16
	bitsPerWord uint8
	csChange    uint8
	txNbits     uint8
	rxNbits     uint8
	wordDelay   uint8
	pad         uint8
}

// Transport is the SPI surface the front end drives; faked in tests.
type Transport interface {
	// WriteRelay emits one 24-bit relay word on spidev1.0 (MSB-first).
	WriteRelay(word uint32) error
	// WriteGain emits the two gain-DAC bytes on spidev1.1 as two separate
	// CS-framed single-byte transfers, CH2 FIRST then CH1 (spec 06 §4.1).
	WriteGain(ch2, ch1 uint8) error
}

// Dev is the real SPI transport. Both fds are opened once and never closed.
type Dev struct {
	relayFD int // /dev/spidev1.0: mode 3, 24 bits/word, 300 kHz
	gainFD  int // /dev/spidev1.1: mode 3, 8 bits/word, 300 kHz
}

func ioctlPtr(fd int, req uintptr, p unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(p))
	if errno != 0 {
		return errno
	}
	return nil
}

func openSPI(path string, bits uint8) (int, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return -1, err
	}
	fd := int(f.Fd())
	// Keep the fd alive for the process lifetime; drop the finalizer path.
	// (Losing the wrapper to GC would close the SPI node mid-session.)
	mode := uint8(spiMode)
	speed := uint32(spiSpeedHz)
	b := bits
	if err := ioctlPtr(fd, spiWrMode, unsafe.Pointer(&mode)); err != nil {
		return -1, fmt.Errorf("%s: set mode: %w", path, err)
	}
	if err := ioctlPtr(fd, spiWrBits, unsafe.Pointer(&b)); err != nil {
		return -1, fmt.Errorf("%s: set bits: %w", path, err)
	}
	if err := ioctlPtr(fd, spiWrMaxSpeed, unsafe.Pointer(&speed)); err != nil {
		return -1, fmt.Errorf("%s: set speed: %w", path, err)
	}
	// Prevent the *os.File from being finalized (and the fd closed).
	spiFiles = append(spiFiles, f)
	return fd, nil
}

// spiFiles pins the opened *os.File wrappers for the process lifetime.
var spiFiles []*os.File

// NewDev opens both SPI nodes with the spec 06 settings. The gain node
// (spidev1.1) is physically shared with the FPGA bitstream loader, but the
// mode-3 / 8-bit / 300 kHz single-byte path reaches only the DAC and cannot
// touch nCONFIG — never reconfigure this node at loader settings.
func NewDev() (*Dev, error) {
	relay, err := openSPI("/dev/spidev1.0", 24)
	if err != nil {
		return nil, err
	}
	gain, err := openSPI("/dev/spidev1.1", 8)
	if err != nil {
		return nil, err
	}
	return &Dev{relayFD: relay, gainFD: gain}, nil
}

func (d *Dev) message(fd int, buf []byte, bits uint8) error {
	tr := spiTransfer{
		txBuf:       uint64(uintptr(unsafe.Pointer(&buf[0]))),
		len:         uint32(len(buf)),
		speedHz:     spiSpeedHz,
		bitsPerWord: bits,
	}
	err := ioctlPtr(fd, spiMessage1, unsafe.Pointer(&tr))
	// txBuf holds buf's address as an integer the GC can't see; keep buf
	// alive until the syscall (which reads through that pointer) returns.
	runtime.KeepAlive(buf)
	return err
}

func (d *Dev) WriteRelay(word uint32) error {
	// One 24-bit word in a 32-bit container (len=4), MSB-first on the wire.
	var buf [4]byte
	buf[0] = byte(word)
	buf[1] = byte(word >> 8)
	buf[2] = byte(word >> 16)
	buf[3] = 0
	return d.message(d.relayFD, buf[:], 24)
}

func (d *Dev) WriteGain(ch2, ch1 uint8) error {
	// Two separate CS-framed transfers, no address byte, CH2 then CH1.
	b2 := []byte{ch2}
	if err := d.message(d.gainFD, b2, 8); err != nil {
		return err
	}
	b1 := []byte{ch1}
	return d.message(d.gainFD, b1, 8)
}
