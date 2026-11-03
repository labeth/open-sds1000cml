// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// frontEnd writes the analog front end on the ARM's SPI bus (spec 06 §2):
// the relay word (coarse range, coupling, bandwidth limit, trigger source and
// coupling) on /dev/spidev1.0, 24 bits a word, and the two fine-gain DAC
// bytes on /dev/spidev1.1, CH2 first. Both mode 3 at 300 kHz. The takeover
// stage opens them once the factory app is gone.
type frontEnd interface {
	WriteRelay(word uint32) error
	WriteGain(ch2, ch1 uint8) error
}

// The spidev ioctls (write direction: they program the bus) and the rate.
const (
	spiWrMode   = 0x40016b01
	spiWrBits   = 0x40016b03
	spiWrSpeed  = 0x40046b04
	spiMessage1 = 0x40206b00
	spiSpeedHz  = 300000
)

// spiTransfer is struct spi_ioc_transfer.
type spiTransfer struct {
	txBuf, rxBuf       uint64
	len, speedHz       uint32
	delayUsecs         uint16
	bitsPerWord        uint8
	csChange           uint8
	txNbits, rxNbits   uint8
	wordDelay, padding uint8
}

// spiDev is the two spidev nodes.
type spiDev struct{ relay, gain int }

// openSPI opens a spidev node in mode 3 at 300 kHz with bits a word.
// TRLC-LINKS: REQ-SDS-290
func openSPI(path string, bits uint8) (int, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	mode, b, speed := uint8(3), bits, uint32(spiSpeedHz)
	for _, s := range []struct {
		req uintptr
		p   unsafe.Pointer
	}{{spiWrMode, unsafe.Pointer(&mode)}, {spiWrBits, unsafe.Pointer(&b)}, {spiWrSpeed, unsafe.Pointer(&speed)}} { //nolint:gosec // G103: the ioctls take their argument by address
		if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), s.req, uintptr(s.p)); errno != 0 {
			_ = unix.Close(fd)
			return -1, fmt.Errorf("%s: %w", path, errno)
		}
	}
	return fd, nil
}

// openFrontEnd opens both nodes.
// TRLC-LINKS: REQ-SDS-290
func openFrontEnd() (*spiDev, error) {
	relay, err := openSPI("/dev/spidev1.0", 24)
	if err != nil {
		return nil, err
	}
	gain, err := openSPI("/dev/spidev1.1", 8)
	if err != nil {
		_ = unix.Close(relay)
		return nil, err
	}
	return &spiDev{relay: relay, gain: gain}, nil
}

// Close closes both nodes.
// TRLC-LINKS: REQ-SDS-290
func (d *spiDev) Close() {
	_ = unix.Close(d.relay)
	_ = unix.Close(d.gain)
}

// message is one CS-framed transfer of buf.
// TRLC-LINKS: REQ-SDS-290
func (d *spiDev) message(fd int, buf []byte, bits uint8) error {
	tr := spiTransfer{txBuf: uint64(uintptr(unsafe.Pointer(&buf[0]))), len: uint32(len(buf)), speedHz: spiSpeedHz, bitsPerWord: bits} //nolint:gosec // G103,G115: the transfer names its buffer by address; a few bytes
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), spiMessage1, uintptr(unsafe.Pointer(&tr)))                               //nolint:gosec // G103: the ioctl takes its argument by address
	runtime.KeepAlive(buf)
	if errno != 0 {
		return errno
	}
	return nil
}

// WriteRelay sends the whole 24-bit relay word, as four bytes little-endian.
// TRLC-LINKS: REQ-SDS-290
func (d *spiDev) WriteRelay(word uint32) error {
	return d.message(d.relay, []byte{byte(word & 0xff), byte(word >> 8 & 0xff), byte(word >> 16 & 0xff), 0}, 24)
}

// WriteGain sends the two gain bytes as two transfers, CH2 first.
// TRLC-LINKS: REQ-SDS-290
func (d *spiDev) WriteGain(ch2, ch1 uint8) error {
	if err := d.message(d.gain, []byte{ch2}, 8); err != nil {
		return err
	}
	return d.message(d.gain, []byte{ch1}, 8)
}
