// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/labeth/open-sds1000cml/ota/internal/gpmc"
)

// regBus reads and writes the board's registers: CS1 is the FPGA, CS3 the
// configuration port and the DACs. Only the takeover stage writes, once the
// factory app is gone; the gpmc package stays read-only for the agent.
type regBus interface {
	Read(plane uint8, sel uint16) (uint16, error)
	Write(plane uint8, sel, val uint16) error
}

// The planes and the CS3 registers the takeover stage uses (spec 02 §6,
// spec 12 §2).
const (
	planeCS1 = 1
	planeCS3 = 3

	cs3Config   = 0x07 // passive-serial port: DCLK b0, nCONFIG b1, DATA0 b2; reads nSTATUS b6, CONF_DONE b7
	cfgDCLK     = 1 << 0
	cfgNCONFIG  = 1 << 1
	cfgDATA0    = 1 << 2
	cfgNSTATUS  = 1 << 6
	cfgCONFDONE = 1 << 7
)

// offsetDAC are each channel's offset DAC registers on CS3, low byte then
// the high byte, which latches the code (spec 02 §6).
var offsetDAC = [2][2]uint16{{0x10, 0x30}, {0x11, 0x31}}

// setOffsetDAC writes code to channel ch's (0 or 1) offset DAC.
// TRLC-LINKS: REQ-SDS-288
func setOffsetDAC(b regBus, ch int, code uint16) error {
	if err := b.Write(planeCS3, offsetDAC[ch][0], code&0xff); err != nil {
		return err
	}
	return b.Write(planeCS3, offsetDAC[ch][1], code>>8)
}

// configure loads rbf into the FPGA through the CS3 port, the sequence of
// spec 12 §2: nCONFIG low 2 ms, nSTATUS up, every byte LSB first on DATA0
// with a DCLK pulse, 128 more clocks, CONF_DONE. Three tries. The load is
// volatile: a power-cycle brings the factory image back.
// TRLC-LINKS: REQ-SDS-288
func configure(b regBus, rbf []byte, sleep func(time.Duration)) error {
	if len(rbf) < 1024 {
		return fmt.Errorf("an FPGA image of %d bytes", len(rbf))
	}
	var last error
	for range 3 {
		if last = configureOnce(b, rbf, sleep); last == nil {
			return nil
		}
	}
	return last
}

// configureOnce is one passive-serial load.
// TRLC-LINKS: REQ-SDS-288
func configureOnce(b regBus, rbf []byte, sleep func(time.Duration)) error {
	w := func(v uint16) error { return b.Write(planeCS3, cs3Config, v) }
	if err := w(0); err != nil {
		return err
	}
	sleep(2 * time.Millisecond)
	if err := w(cfgNCONFIG); err != nil {
		return err
	}
	if !pollCfg(b, cfgNSTATUS, 21, 10*time.Millisecond, sleep) {
		return errors.New("nSTATUS did not rise after nCONFIG")
	}
	for _, by := range rbf {
		for bit := range 8 {
			v := uint16(cfgNCONFIG)
			if by>>bit&1 != 0 {
				v |= cfgDATA0
			}
			if err := w(v); err != nil {
				return err
			}
			if err := w(v | cfgDCLK); err != nil {
				return err
			}
		}
	}
	for range 128 {
		if err := w(cfgNCONFIG); err != nil {
			return err
		}
		if err := w(cfgNCONFIG | cfgDCLK); err != nil {
			return err
		}
	}
	if err := w(cfgNCONFIG); err != nil {
		return err
	}
	if !pollCfg(b, cfgCONFDONE, 5000, time.Millisecond, sleep) {
		return errors.New("CONF_DONE did not rise")
	}
	return nil
}

// pollCfg polls the configuration port until bit is set, n times every gap.
// TRLC-LINKS: REQ-SDS-288
func pollCfg(b regBus, bit uint16, n int, gap time.Duration, sleep func(time.Duration)) bool {
	for range n {
		if v, err := b.Read(planeCS3, cs3Config); err == nil && v&bit != 0 {
			return true
		}
		sleep(gap)
	}
	return false
}

// devBus is the inherited /dev/Gpmc descriptor as a regBus.
type devBus struct{ fd int }

// The register ioctls on /dev/Gpmc (spec 12 §6.3).
const (
	ioctlRead  = 0x80026700
	ioctlWrite = 0x40026701
)

// Read reads one register.
// TRLC-LINKS: REQ-SDS-288
func (d devBus) Read(plane uint8, sel uint16) (uint16, error) {
	return d.xfer(ioctlRead, plane, sel, 0)
}

// Write writes one register.
// TRLC-LINKS: REQ-SDS-288
func (d devBus) Write(plane uint8, sel, val uint16) error {
	_, err := d.xfer(ioctlWrite, plane, sel, val)
	return err
}

// xfer is one register ioctl on the 6-byte record; a raw system call, as a
// load is millions of them.
// TRLC-LINKS: REQ-SDS-288
func (d devBus) xfer(req uintptr, plane uint8, sel, val uint16) (uint16, error) {
	if plane != planeCS1 && plane != planeCS3 {
		return 0, fmt.Errorf("plane %d", plane)
	}
	b := gpmc.EncodeAccess(plane, sel, val)
	if _, _, errno := unix.RawSyscall(unix.SYS_IOCTL, uintptr(d.fd), req, uintptr(unsafe.Pointer(&b[0]))); errno != 0 { //nolint:gosec // G103: the driver's ioctl takes the record's address
		return 0, fmt.Errorf("gpmc cs%d 0x%02x: %w", plane, sel, errno)
	}
	return gpmc.DecodeValue(b), nil
}
