// Package fpgaload configures the acquisition Cyclone with the default acq2
// image at boot: passive-serial reconfiguration of the volatile CRAM, bit-banged
// through the GPMC CS3 configuration port (workplan §4.1; fpga-specs 05 §3.1).
//
// The idea and the proven bit map come from owned-fpga app/internal/fpgaload
// (commits 680e441, 5337711, 3221601 "fpga_reload needs -bitrev"); the code is a
// re-implementation with the port injected, so the WHOLE sequence — nCONFIG
// pulse first, nSTATUS wait, every DCLK edge, byte and bit order, init clocks,
// CONF_DONE poll, retries, the identity verify — runs in the offline suite
// against a fake port.
//
// CS3 0x07 write bits (factory loader FUN_001b0dc4, bench-proven by
// tools/fpga_reload): b0 = DCLK, b1 = nCONFIG, b2 = DATA0, b3 = SPI route
// (dead on this unit). Read bits: b6 = nSTATUS, b7 = CONF_DONE. Any write with
// b1 low collapses the running fabric — which is exactly what starts a cycle.
//
// Configuration is volatile: a bad or partial load only black-screens
// acquisition, and a power-cycle restores the factory image from NAND. Nothing
// here writes any flash, by construction.
// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import (
	"fmt"
	"time"

	"open-sds/app/internal/iface"
)

// Port is the GPMC CS3 configuration port. Implementations: the ioctl port and
// the /dev/mem fast port (device.go) and the test fake.
// TRLC-LINKS: REQ-SDS-005
type Port interface {
	// WriteCfg writes the 16-bit configuration-port word.
	WriteCfg(v uint16) error
	// ReadCfg reads it back (nSTATUS = bit 6, CONF_DONE = bit 7).
	ReadCfg() (uint16, error)
}

const (
	BitDCLK     uint16 = 1 << 0
	BitNCONFIG  uint16 = 1 << 1
	BitDATA0    uint16 = 1 << 2
	BitNSTATUS  uint16 = 1 << 6
	BitCONFDONE uint16 = 1 << 7

	// wordReset asserts nCONFIG (everything low); wordIdle is the parked
	// port: nCONFIG high, DCLK and DATA0 low.
	wordReset uint16 = 0x0000
	wordIdle  uint16 = BitNCONFIG

	// Bitstream size for EP4CE10, uncompressed (fpga-specs 05 §3.3). Anything
	// else is a different device, a changed fit, or compression left on.
	RBFLen = 368011
)

// Options tunes the reload; zero values take the defaults below.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-092
type Options struct {
	// BitOrder is the caller's request; the default reads the order out of the
	// container header (container.go). An explicit value that contradicts the
	// container is refused unless Force is set.
	BitOrder BitOrder
	Force    bool
	// InitClocks is the number of DCLK cycles clocked after the last data bit
	// so the device leaves the initialization phase. The bench-proven loader
	// (owned-fpga 680e441) clocks 16 zero bytes = 128 DCLK; the workplan says
	// "16 init clocks". Extra clocks in user mode are harmless, too few can
	// leave the device short of user mode, so the default is the proven 128.
	InitClocks int
	// NConfigLow is the nCONFIG low hold (default 2 ms, tools/fpga_reload).
	NConfigLow time.Duration
	// StatusPoll / StatusPolls bound the nSTATUS wait after the release
	// (defaults 10 ms × 21, the factory loader's bound).
	StatusPoll  time.Duration
	StatusPolls int
	// Timeout / PollEvery bound the CONF_DONE poll (defaults 5 s / 1 ms).
	Timeout   time.Duration
	PollEvery time.Duration
	// Attempts is the number of full cycles tried before giving up (default 3,
	// the vendor loader's count).
	Attempts int
	// RequireLen refuses an image that is not exactly RBFLen bytes (default
	// true). Tests use short containers.
	AllowAnyLen bool
	// Sleep is injectable so tests do not wait (default time.Sleep).
	Sleep func(time.Duration)
	Logf  func(string, ...any)
	// Progress reports bytes actually shifted; completion still requires CONF_DONE.
	Progress func(sent, total int)
}

// TRLC-LINKS: REQ-SDS-005
func (o *Options) withDefaults() {
	if o.InitClocks <= 0 {
		o.InitClocks = 128
	}
	if o.NConfigLow <= 0 {
		o.NConfigLow = 2 * time.Millisecond
	}
	if o.StatusPoll <= 0 {
		o.StatusPoll = 10 * time.Millisecond
	}
	if o.StatusPolls <= 0 {
		o.StatusPolls = 21
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Second
	}
	if o.PollEvery <= 0 {
		o.PollEvery = time.Millisecond
	}
	if o.Attempts <= 0 {
		o.Attempts = 3
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
}

// bitrev reverses the bits of one byte (Cyclone IV PS shifts LSB-first; the
// loader presents MSB-first, so a native Quartus .rbf is reversed per byte).
// TRLC-LINKS: REQ-SDS-092
func bitrev(b byte) byte {
	b = b&0xF0>>4 | b&0x0F<<4
	b = b&0xCC>>2 | b&0x33<<2
	b = b&0xAA>>1 | b&0x55<<1
	return b
}

// Reload runs one or more full passive-serial cycles until CONF_DONE asserts.
// It does NOT verify the build-ID — the caller does that over CS1 once the
// fabric is up (EnsureDefault).
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-092
func Reload(p Port, rbf []byte, o Options) error {
	o.withDefaults()
	if !o.AllowAnyLen && len(rbf) != RBFLen {
		return fmt.Errorf("fpgaload: bitstream is %d bytes, want %d (uncompressed EP4CE10 .rbf) — refusing", len(rbf), RBFLen)
	}
	// Decide the wire order BEFORE the fabric is touched: an image we cannot
	// read is refused with the live fabric intact.
	rev, why, err := resolveBitOrder(rbf, o.BitOrder, o.Force)
	if err != nil {
		return fmt.Errorf("fpgaload: %w", err)
	}
	o.Logf("fpgaload: bit order: %s", why)
	var last error
	for attempt := 1; attempt <= o.Attempts; attempt++ {
		if err := configureOnce(p, rbf, rev, &o); err != nil {
			last = err
			o.Logf("fpgaload: attempt %d/%d failed: %v", attempt, o.Attempts, err)
			continue
		}
		return nil
	}
	return last
}

// configureOnce is one passive-serial cycle (fpga-specs 05 §3.1 steps 1–7).
// TRLC-LINKS: REQ-SDS-005
func configureOnce(p Port, rbf []byte, rev bool, o *Options) error {
	// 1. assert nCONFIG (all low) and hold.
	if err := p.WriteCfg(wordReset); err != nil {
		return fmt.Errorf("nCONFIG assert: %w", err)
	}
	o.Sleep(o.NConfigLow)
	// 2. release nCONFIG; the device drives nSTATUS low then releases it.
	if err := p.WriteCfg(wordIdle); err != nil {
		return fmt.Errorf("nCONFIG release: %w", err)
	}
	// 3. wait for nSTATUS high (ready to accept data).
	ready := false
	for i := 0; i < o.StatusPolls; i++ {
		v, err := p.ReadCfg()
		if err != nil {
			return fmt.Errorf("nSTATUS read: %w", err)
		}
		if v&BitNSTATUS != 0 {
			ready = true
			break
		}
		o.Sleep(o.StatusPoll)
	}
	if !ready {
		return fmt.Errorf("nSTATUS did not assert within %v after the nCONFIG release", time.Duration(o.StatusPolls)*o.StatusPoll)
	}
	// 4. shift every bit MSB-first of the (bit-reversed) byte: present DATA0
	//    with DCLK low, then raise DCLK to latch. nCONFIG stays high.
	if o.Progress != nil {
		o.Progress(0, len(rbf))
	}
	for i, by := range rbf {
		if rev {
			by = bitrev(by)
		}
		for bit := 7; bit >= 0; bit-- {
			w := wordIdle
			if (by>>uint(bit))&1 == 1 {
				w |= BitDATA0
			}
			if err := p.WriteCfg(w); err != nil {
				return fmt.Errorf("byte %d bit %d: %w", i, bit, err)
			}
			if err := p.WriteCfg(w | BitDCLK); err != nil {
				return fmt.Errorf("byte %d bit %d clock: %w", i, bit, err)
			}
		}
		if o.Progress != nil && ((i+1)%16384 == 0 || i+1 == len(rbf)) {
			o.Progress(i+1, len(rbf))
		}
	}
	// 5. init clocks (DATA0 low).
	for i := 0; i < o.InitClocks; i++ {
		if err := p.WriteCfg(wordIdle); err != nil {
			return fmt.Errorf("init clock %d: %w", i, err)
		}
		if err := p.WriteCfg(wordIdle | BitDCLK); err != nil {
			return fmt.Errorf("init clock %d: %w", i, err)
		}
	}
	// 6. park the port.
	if err := p.WriteCfg(wordIdle); err != nil {
		return fmt.Errorf("park: %w", err)
	}
	// 7. CONF_DONE.
	maxPolls := int(o.Timeout/o.PollEvery) + 1
	for i := 0; i < maxPolls; i++ {
		v, err := p.ReadCfg()
		if err != nil {
			return fmt.Errorf("CONF_DONE read: %w", err)
		}
		if v&BitCONFDONE != 0 {
			o.Logf("fpgaload: CONF_DONE after %v (port %#04x)", time.Duration(i)*o.PollEvery, v)
			return nil
		}
		o.Sleep(o.PollEvery)
	}
	v, _ := p.ReadCfg()
	return fmt.Errorf("CONF_DONE not asserted within %v (port reads %#04x; 0x0040 = clocked but never configured, i.e. wrong bit order or corrupt image)", o.Timeout, v)
}

// Reader reads a CS1 register (bus.Dev.Read with plane 1 bound).
// TRLC-LINKS: REQ-SDS-004
type Reader func(sel uint16) (uint16, error)

// Identity is the four identity words of a fabric.
// TRLC-LINKS: REQ-SDS-004
type Identity struct {
	BuildLo, BuildHi, Version, Fabric uint16
}

// TRLC-LINKS: REQ-SDS-004
func (id Identity) BuildID() uint32 { return uint32(id.BuildHi)<<16 | uint32(id.BuildLo) }

// ReadIdentity reads BUILDID_LO/HI, VERSION and FABRIC_ID.
// TRLC-LINKS: REQ-SDS-004
func ReadIdentity(read Reader) (Identity, error) {
	var id Identity
	var err error
	if id.BuildLo, err = read(iface.SelBuildidLo); err != nil {
		return id, err
	}
	if id.BuildHi, err = read(iface.SelBuildidHi); err != nil {
		return id, err
	}
	if id.Version, err = read(iface.SelVersion); err != nil {
		return id, err
	}
	if id.Fabric, err = read(iface.SelFabricId); err != nil {
		return id, err
	}
	return id, nil
}

// Verify reads the identity and checks it against the generated interface.
// TRLC-LINKS: REQ-SDS-004
func Verify(read Reader) error {
	id, err := ReadIdentity(read)
	if err != nil {
		return fmt.Errorf("identity read: %w", err)
	}
	return iface.CheckIdentity(id.BuildLo, id.BuildHi, id.Version, id.Fabric)
}

// EnsureDefault verifies the fabric is the default image (BUILDID + FABRIC_ID +
// VERSION) and, on any mismatch, reloads rbf and verifies again. It returns
// an error only when the fabric ends up NOT the default image; the caller must
// then refuse to drive. A nil/empty rbf (a build without the bitstream) can only
// succeed if the fabric already matches.
// TRLC-LINKS: REQ-SDS-004, REQ-SDS-005
func EnsureDefault(read Reader, p Port, rbf []byte, o Options) error {
	o.withDefaults()
	if err := Verify(read); err == nil {
		o.Logf("fpgaload: fabric is already the default image (build-ID %#08x) — no reload", iface.BuildID)
		return nil
	} else {
		o.Logf("fpgaload: fabric is not the default image: %v", err)
	}
	if len(rbf) == 0 {
		return fmt.Errorf("fpgaload: fabric mismatch and no embedded bitstream (build with `make app-release`) — cannot configure")
	}
	o.Logf("fpgaload: reloading the embedded default image (%d bytes)", len(rbf))
	if err := Reload(p, rbf, o); err != nil {
		return err
	}
	if err := Verify(read); err != nil {
		return fmt.Errorf("fpgaload: post-reload verify failed: %w", err)
	}
	o.Logf("fpgaload: loaded and verified build-ID %#08x (fabric %#04x)", iface.BuildID, iface.FabricID)
	return nil
}
