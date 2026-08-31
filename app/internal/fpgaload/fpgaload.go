// Package fpgaload configures the acquisition FPGA with the owned standard
// bitstream at boot (method B: passive-serial reconfiguration of volatile CRAM).
//
// Cold boot leaves whatever the CPU's NAND loader put in the fabric; this app
// generated its own bitstream, so it reconfigures to the owned build before the
// engine drives the bus and verifies the result by the interface build-ID. The
// reload path is deliberately OUTSIDE the register interface (bus/iface): pulsing
// nCONFIG is a write to the read-only CONF_DONE selector, and the fabric's
// register front-end is down while it reconfigures, so both the nCONFIG pulse and
// the CONF_DONE poll go straight to the GPMC config port, and the bitstream
// streams over the passive-serial SPI node.
//
// Configuration lives in volatile SRAM (CRAM): a bad or partial load only
// black-screens acquisition, and a power-cycle restores the factory image from
// NAND. Nothing here writes any configuration flash (NAND or EPCS) by
// construction — there is no flash-programming path in this package. Keep it that
// way.
//
// The logic here (Reload, EnsureStandard) is hardware-independent: it drives the
// injected ConfigPort/SerialLoader so it is exercised in full by the offline
// suite. The syscall-backed implementations and the boot entrypoint (Bringup)
// live in device.go; the embedded bitstream is build-tag gated (bitstream_*.go).
package fpgaload

import (
	"fmt"
	"time"

	"open-sds/app/internal/iface"
)

// ConfigPort is the GPMC config port: the nCONFIG pulse that starts a fresh
// passive-serial cycle, and the CONF_DONE status read.
type ConfigPort interface {
	// PulseNCONFIG drives a low->high edge on the config port, dropping
	// CONF_DONE and arming the fabric to accept DCLK/DATA.
	PulseNCONFIG() error
	// ConfDone reports whether the fabric has finished configuring.
	ConfDone() (bool, error)
}

// SerialLoader is the passive-serial data path (DCLK/DATA0 over spidev).
type SerialLoader interface {
	// Configure programs the loader bus mode/width/clock. It is called before
	// any nCONFIG pulse, so a bad SPI node fails before the live fabric is
	// disturbed.
	Configure() error
	// SendChunk clocks one buffer out DATA0 as a single framed transfer.
	SendChunk(buf []byte) error
}

// Options tunes the reload; the zero value is filled with the defaults below.
type Options struct {
	// BitOrder selects the wire bit order. The zero value, BitOrderAuto, reads
	// it out of the container's device header (container.go): a native Quartus
	// image is bit-reversed, the pre-reversed factory image ships raw, and
	// anything unrecognised is REFUSED before the fabric is touched. Set it
	// explicitly only to assert an expectation — an explicit value that
	// contradicts the container is an error unless ForceBitOrder is also set.
	BitOrder BitOrder
	// ForceBitOrder loads an image whose container disagrees with BitOrder, or
	// whose container cannot be read at all. Last resort: the wrong order clocks
	// in cleanly and leaves CONF_DONE low, which looks like dead silicon.
	ForceBitOrder bool
	// ChunkSize caps each SPI transfer (the spidev bufsiz bounce buffer;
	// default 4096). A single transfer above bufsiz returns EMSGSIZE.
	ChunkSize int
	// InitClocks is the count of trailing zero bytes clocked after the last
	// config byte, for the Cyclone IV init phase (default 16).
	InitClocks int
	// Timeout / PollEvery bound the CONF_DONE poll (defaults 5s / 1ms).
	Timeout   time.Duration
	PollEvery time.Duration
	// Sleep is injectable so tests do not wait in real time (default time.Sleep).
	Sleep func(time.Duration)
	// Logf receives progress lines (default: discard).
	Logf func(string, ...any)
}

const (
	defaultChunkSize  = 4096
	defaultInitClocks = 16
	defaultTimeout    = 5 * time.Second
	defaultPollEvery  = 1 * time.Millisecond

	// minRBFLen guards against a grossly truncated embed. The real correctness
	// gates are CONF_DONE and the build-ID verify; this only catches an empty or
	// obviously partial blob before it touches the fabric. The standard build is
	// ~360 KB (see fpga/standard); anything under 64 KB is not a bitstream.
	minRBFLen = 64 * 1024
)

func (o *Options) withDefaults() {
	if o.ChunkSize <= 0 {
		o.ChunkSize = defaultChunkSize
	}
	if o.InitClocks < 0 {
		o.InitClocks = 0
	} else if o.InitClocks == 0 {
		o.InitClocks = defaultInitClocks
	}
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	if o.PollEvery <= 0 {
		o.PollEvery = defaultPollEvery
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
}

// bitrevTable maps each byte to its bit-reversed value (LSB-first passive serial
// vs. MSB-first SoC SPI master). WHETHER a given image needs it comes from the
// container, not from a default — see DetectOrder in container.go.
var bitrevTable [256]byte

func init() {
	for i := range bitrevTable {
		b := byte(i)
		b = b&0xF0>>4 | b&0x0F<<4
		b = b&0xCC>>2 | b&0x33<<2
		b = b&0xAA>>1 | b&0x55<<1
		bitrevTable[i] = b
	}
}

// Reload runs the method-B sequence: configure the loader bus, pulse nCONFIG,
// stream the (optionally bit-reversed) bitstream in bufsiz chunks plus init
// clocks, then poll CONF_DONE. It does NOT verify the build-ID — that is the
// caller's job over the register interface once the fabric is up (see
// EnsureStandard).
func Reload(cfg ConfigPort, ser SerialLoader, rbf []byte, o Options) error {
	o.withDefaults()
	if len(rbf) < minRBFLen {
		return fmt.Errorf("fpgaload: bitstream is %d bytes (< %d) — empty or truncated, refusing to configure", len(rbf), minRBFLen)
	}

	// Decide the wire bit order from the container BEFORE anything is opened or
	// driven. An image we cannot read is refused here, with the live fabric
	// untouched, instead of being clocked in the wrong order.
	bitrev, why, err := resolveBitOrder(rbf, o.BitOrder, o.ForceBitOrder)
	if err != nil {
		return fmt.Errorf("fpgaload: %w", err)
	}
	o.Logf("fpgaload: bit order %v (%s)", bitrev, why)

	// Configure the loader bus FIRST: a bad SPI node fails here, before nCONFIG
	// drops the live fabric.
	if err := ser.Configure(); err != nil {
		return fmt.Errorf("fpgaload: configure loader bus: %w", err)
	}

	o.Logf("fpgaload: pulsing nCONFIG")
	if err := cfg.PulseNCONFIG(); err != nil {
		return fmt.Errorf("fpgaload: nCONFIG pulse: %w", err)
	}

	// Build the wire image once (bit-reversed if required), then stream it.
	img := rbf
	if bitrev {
		img = make([]byte, len(rbf))
		for i, v := range rbf {
			img[i] = bitrevTable[v]
		}
	}
	o.Logf("fpgaload: streaming %d bytes in %d-byte chunks (bitrev=%v)", len(img), o.ChunkSize, bitrev)
	for off := 0; off < len(img); off += o.ChunkSize {
		end := off + o.ChunkSize
		if end > len(img) {
			end = len(img)
		}
		if err := ser.SendChunk(img[off:end]); err != nil {
			return fmt.Errorf("fpgaload: stream at byte %d: %w", off, err)
		}
	}
	// Trailing init clocks so the fabric enters user mode.
	if o.InitClocks > 0 {
		if err := ser.SendChunk(make([]byte, o.InitClocks)); err != nil {
			return fmt.Errorf("fpgaload: init clocks: %w", err)
		}
	}

	// Poll CONF_DONE. Count-based so the bound is deterministic and testable;
	// Sleep is injectable so tests do not wait in real time.
	o.Logf("fpgaload: polling CONF_DONE")
	maxPolls := int(o.Timeout/o.PollEvery) + 1
	for i := 0; i < maxPolls; i++ {
		done, err := cfg.ConfDone()
		if err != nil {
			return fmt.Errorf("fpgaload: read CONF_DONE: %w", err)
		}
		if done {
			o.Logf("fpgaload: CONF_DONE asserted after %v", time.Duration(i)*o.PollEvery)
			return nil
		}
		o.Sleep(o.PollEvery)
	}
	return fmt.Errorf("fpgaload: CONF_DONE not asserted within %v", o.Timeout)
}

// EnsureStandard verifies the fabric is the owned standard build and, on any
// mismatch, reconfigures it with the embedded bitstream and re-verifies. read is
// the register-interface read (bus.Read). It returns an error only when the
// fabric ends up NOT the standard build: the caller should then refuse to drive.
//
// A cold-boot fabric is the factory NAND image, so the first verify normally
// fails and the reload is the expected path. If there is no embedded bitstream
// (a build without -tags withbitstream) the reload cannot proceed, and this
// returns an error unless the fabric already happens to be the standard build.
func EnsureStandard(read func(iface.Plane, uint16) (uint16, error), cfg ConfigPort, ser SerialLoader, o Options) error {
	o.withDefaults()
	if err := iface.Verify(read); err == nil {
		o.Logf("fpgaload: fabric is already the standard build (%#08x) — no reconfig", iface.BuildID)
		return nil
	} else {
		o.Logf("fpgaload: fabric is not the standard build (%v)", err)
	}

	rbf := Standard()
	if len(rbf) == 0 {
		return fmt.Errorf("fpgaload: fabric mismatch and no embedded bitstream (build with -tags withbitstream) — cannot configure")
	}
	o.Logf("fpgaload: reconfiguring with embedded standard bitstream (%d bytes)", len(rbf))
	if err := Reload(cfg, ser, rbf, o); err != nil {
		return err
	}
	if err := iface.Verify(read); err != nil {
		return fmt.Errorf("fpgaload: post-reload verify failed: %w", err)
	}
	o.Logf("fpgaload: standard bitstream loaded and verified (%#08x)", iface.BuildID)
	return nil
}
