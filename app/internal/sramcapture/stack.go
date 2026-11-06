// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE

package sramcapture

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/labeth/open-dso-core/superres"
)

// Edge-locked FPGA stacking port (fpga/acq_sram/stack_engine_port.v,
// ADR-STACKING-IMAGE-SPLIT). Only accumulated moments cross the bus.
const (
	StackCapabilityID = uint16(0x5342)

	selStackGrant     = 109
	selStackMailIndex = 110
	selStackMail      = 111
	selStackOp        = 112
	selStackLevel     = 113 // write: level/hysteresis; read: capability
	selStackFlags     = 114 // write: channel/falling/mask; read: tile bins
	selStackPre       = 115 // write: pre samples; read: accepted hits
	selStackSep       = 117 // write: separation; read: crossings
	selStackBins      = 119 // write: tile bins; read: peek pair
	selStackFirstBin  = 120
	selStackFactor    = 122
	selStackInitial   = 123
	selStackFirst     = 125
	selStackTile      = 127
	selStackFactorCap = 121 // read: bit0 power-of-two factors only
	// Template qualifier (ADR-STACKING-TEMPLATE-QUALIFIER).
	selStackTemplateIndex = 96
	selStackTemplateByte  = 97
	selStackQualify       = 98
	selStackQualifyCount  = 99
	selStackQualifyStride = 100
	selStackQualifyPre    = 101
	selStackQualifyThresh = 103
	selStackRejected      = 122 // read
	selStackTemplateCap   = 124 // read: template capacity, 0 without qualifier

	stackOpScan      = 1
	stackOpTileRead  = 3
	stackOpTileWrite = 4
	stackOpReset     = 5

	stackOutstanding = 1
	stackError       = 4
	stackInitialized = 16
	stackOwned       = 32
)

// StackConfig selects edge-locked hits on one channel and the fine grid.
// A hit is placed at the interpolated Level crossing; PreSamples puts the
// window origin before it. Bins fine bins span Bins/Factor input samples.
// TRLC-LINKS: REQ-SDS-141
type StackConfig struct {
	Channel       uint8  `json:"channel"`
	Falling       bool   `json:"falling"`
	Level         uint8  `json:"level"`
	Hysteresis    uint8  `json:"hysteresis"`
	PreSamples    uint32 `json:"pre_samples"`
	MinSeparation uint32 `json:"min_separation"`
	Bins          int    `json:"bins"`
	Factor        int    `json:"factor"`
	ChannelMask   uint8  `json:"channel_mask"`
	// Template, when set, qualifies each hit: the sum of |align channel -
	// Template[k]| at crossing-TemplatePre+k*TemplateStride must not exceed
	// TemplateThreshold (ADR-STACKING-TEMPLATE-QUALIFIER).
	Template          []int `json:"template,omitempty"`
	TemplateStride    int   `json:"template_stride,omitempty"`
	TemplatePre       int   `json:"template_pre,omitempty"`
	TemplateThreshold int   `json:"template_threshold,omitempty"`
}

// TRLC-LINKS: REQ-SDS-141
func (c *Capture) stackTileBins() (int, error) {
	id, err := c.read(selStackLevel)
	if err != nil || id != StackCapabilityID {
		return 0, err
	}
	bins, err := c.read(selStackFlags)
	return int(bins), err
}

// TRLC-LINKS: REQ-SDS-141
func (cfg StackConfig) validate(tile int, pow2 bool) error {
	switch {
	case tile == 0:
		return errors.New("sramcapture: loaded image has no stacking port")
	case cfg.Channel > 1:
		return fmt.Errorf("sramcapture: stack channel %d", cfg.Channel)
	case cfg.Bins < 1 || cfg.Bins > 1<<20:
		return fmt.Errorf("sramcapture: stack bins %d", cfg.Bins)
	case cfg.Factor < 1 || cfg.Factor > 0xffff:
		return fmt.Errorf("sramcapture: stack factor %d", cfg.Factor)
	case pow2 && (cfg.Factor&(cfg.Factor-1) != 0 || cfg.Factor > 1<<24):
		return fmt.Errorf("sramcapture: stack factor %d must be a power of two on this image", cfg.Factor)
	case cfg.ChannelMask == 0 || cfg.ChannelMask > 3:
		return fmt.Errorf("sramcapture: stack channel mask %d", cfg.ChannelMask)
	case len(cfg.Template) > 0 && (cfg.TemplateStride < 1 || cfg.TemplateStride > 255 || cfg.TemplatePre < 0 || cfg.TemplateThreshold < 0):
		return fmt.Errorf("sramcapture: template stride %d, pre %d, threshold %d", cfg.TemplateStride, cfg.TemplatePre, cfg.TemplateThreshold)
	}
	for _, v := range cfg.Template {
		if v < 0 || v > 255 {
			return fmt.Errorf("sramcapture: template value %d is not a code", v)
		}
	}
	return nil
}

// stackOp issues one port operation and waits for its acknowledgement.
// TRLC-LINKS: REQ-SDS-141
func (c *Capture) stackOp(ctx context.Context, op uint16, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.stackOpIn(ctx, op)
}

// stackOpIn is stackOp within the caller's deadline (a deadline is two
// clock reads, about 10 us each on this kernel: a tile's moments share one).
// TRLC-LINKS: REQ-SDS-141
func (c *Capture) stackOpIn(ctx context.Context, op uint16) error {
	if err := c.write(selStackOp, op); err != nil {
		return err
	}
	// A tile read or write is done within microseconds, a scan takes
	// milliseconds: the status is read without sleeping for waitSpin (a
	// 200 us sleep woke late on this kernel, and a tile's 64 reads each
	// waited it out), then every 200 us.
	var spinUntil time.Time
	for i := 0; ; i++ {
		s, err := c.read(selStackOp)
		if err != nil {
			return err
		}
		if s&stackOutstanding == 0 {
			if s&stackError != 0 {
				return fmt.Errorf("sramcapture: stack operation %d failed (status %04x)", op, s)
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("sramcapture: stack operation %d: %w", op, err)
		}
		if i%waitClockEvery != waitClockEvery-1 {
			continue
		}
		if spinUntil.IsZero() {
			spinUntil = time.Now().Add(waitSpin)
		}
		if time.Now().Before(spinUntil) {
			continue
		}
		time.Sleep(200 * time.Microsecond)
	}
}

// TRLC-LINKS: REQ-SDS-141
func (c *Capture) stackGrant(ctx context.Context, on bool) error {
	v := uint16(0)
	if on {
		v = 1
	}
	if err := c.write(selStackGrant, v); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	for {
		s, err := c.read(selStackGrant)
		if err != nil {
			return err
		}
		if (s&2 != 0) == on {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("sramcapture: stack SRAM grant %v: %w", on, err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-141
func (c *Capture) stackDownload(ctx context.Context, bin, ch int) (superres.FPGAStackMoments, error) {
	if err := c.write(selStackTile, uint16((bin<<1|ch)&0xffff)); err != nil {
		return superres.FPGAStackMoments{}, err
	}
	if err := c.stackOp(ctx, stackOpTileRead, time.Second); err != nil {
		return superres.FPGAStackMoments{}, err
	}
	if err := c.write(selStackMailIndex, 0); err != nil {
		return superres.FPGAStackMoments{}, err
	}
	var words [superres.FPGAStackWords]uint16
	// The mailbox read port advances on every read: one batched pop when the
	// bus offers it (one worker round trip), single reads otherwise.
	if pop, ok := c.bus.(interface{ PopWordsChecked(uint16, []uint16) error }); ok {
		if err := pop.PopWordsChecked(selStackMail, words[:]); err != nil {
			return superres.FPGAStackMoments{}, err
		}
		c.beats.Add(uint64(len(words)))
	} else {
		for i := range words {
			w, err := c.read(selStackMail)
			if err != nil {
				return superres.FPGAStackMoments{}, err
			}
			words[i] = w
		}
	}
	return superres.DecodeFPGAMoments(words[:])
}

// isolatedStack is a bus whose worker downloads a tile's moments itself.
type isolatedStack interface {
	StackDownloadIsolated(n int, dst []byte) (owned bool, err error)
}

// StackDownloadWords reads the first n moments of the tile (bin-major, two
// channels a bin) into dst, superres.FPGAStackWords little-endian words
// each, unchecked: a bus worker's half of StackDownload.
// TRLC-LINKS: REQ-SDS-141
func (c *Capture) StackDownloadWords(ctx context.Context, n int, dst []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(dst) != n*superres.FPGAStackWords*2 {
		return errors.New("sramcapture: stack download buffer")
	}
	pop, ok := c.bus.(interface{ PopBytesChecked(uint16, []byte) error })
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for i := range n {
		if err := c.write(selStackTile, uint16(i)); err != nil {
			return err
		}
		if err := c.stackOpIn(ctx, stackOpTileRead); err != nil {
			return err
		}
		if err := c.write(selStackMailIndex, 0); err != nil {
			return err
		}
		at := dst[i*superres.FPGAStackWords*2 : (i+1)*superres.FPGAStackWords*2]
		if ok {
			if err := pop.PopBytesChecked(selStackMail, at); err != nil {
				return err
			}
			continue
		}
		for k := range superres.FPGAStackWords {
			w, err := c.read(selStackMail)
			if err != nil {
				return err
			}
			binary.LittleEndian.PutUint16(at[2*k:], w)
		}
	}
	c.beats.Add(uint64(n * superres.FPGAStackWords)) //nolint:gosec // G115: a count of words read, never negative
	return nil
}

// Session steps save and restore tile moments so every tile can scan the same
// frozen record before the next acquisition. Use the same initial hit count
// for every tile of a record to preserve odd/even membership.

// StackBegin validates cfg against the loaded image, writes the static stack
// configuration and returns the tile size in bins.
// TRLC-LINKS: REQ-SDS-141
func (c *Capture) StackBegin(cfg StackConfig) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tile, err := c.stackTileBins()
	if err != nil {
		return 0, err
	}
	caps, err := c.read(selStackFactorCap)
	if err != nil {
		return 0, err
	}
	if err := cfg.validate(tile, caps&1 != 0); err != nil {
		return 0, err
	}
	flags := uint16(cfg.Channel) | uint16(cfg.ChannelMask)<<2
	if cfg.Falling {
		flags |= 2
	}
	for _, w := range []struct {
		sel uint16
		v   uint32
		two bool
	}{
		{selStackLevel, uint32(cfg.Level) | uint32(cfg.Hysteresis)<<8, false},
		{selStackFlags, uint32(flags), false},
		{selStackPre, cfg.PreSamples, true},
		{selStackSep, cfg.MinSeparation, true},
		{selStackFactor, uint32(cfg.Factor), false}, //nolint:gosec // G115: bounded by StackConfig.validate
		{selStackFirst, 0, true},
	} {
		if w.two {
			err = c.write32(w.sel, w.v)
		} else {
			err = c.write(w.sel, uint16(w.v&0xffff))
		}
		if err != nil {
			return 0, err
		}
	}
	return tile, c.stackQualifier(cfg)
}

// stackQualifier uploads cfg's template, or disables qualification.
// TRLC-LINKS: REQ-SDS-141
func (c *Capture) stackQualifier(cfg StackConfig) error {
	if len(cfg.Template) == 0 {
		return c.write(selStackQualify, 0)
	}
	capacity, err := c.read(selStackTemplateCap)
	if err != nil {
		return err
	}
	if len(cfg.Template) > int(capacity) {
		return fmt.Errorf("sramcapture: template of %d points exceeds the image's %d", len(cfg.Template), capacity)
	}
	if err := c.write(selStackTemplateIndex, 0); err != nil {
		return err
	}
	for _, v := range cfg.Template {
		if err := c.write(selStackTemplateByte, uint16(v&0xffff)); err != nil {
			return err
		}
	}
	for _, w := range []struct {
		sel uint16
		v   uint32
		two bool
	}{
		{selStackQualifyCount, uint32(len(cfg.Template)), false},     //nolint:gosec // G115: bounded by StackConfig.validate
		{selStackQualifyStride, uint32(cfg.TemplateStride), false},   //nolint:gosec // G115: bounded by StackConfig.validate
		{selStackQualifyPre, uint32(cfg.TemplatePre), true},          //nolint:gosec // G115: bounded by StackConfig.validate
		{selStackQualifyThresh, uint32(cfg.TemplateThreshold), true}, //nolint:gosec // G115: bounded by StackConfig.validate
		{selStackQualify, 1, false},
	} {
		var err error
		if w.two {
			err = c.write32(w.sel, w.v)
		} else {
			err = c.write(w.sel, uint16(w.v&0xffff))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// StackResetTile clears the FPGA tile and poisoned state before a new tile.
// TRLC-LINKS: REQ-SDS-141
func (c *Capture) StackResetTile(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stackOp(ctx, stackOpReset, time.Second)
}

// StackScan accumulates the frozen record into the current tile: bins
// [first, first+n) of the global grid, odd/even numbering continuing from
// initial. It holds the SRAM grant only for the scan. Returns the new hit
// total and this record's crossings.
// TRLC-LINKS: REQ-SDS-141
func (c *Capture) StackScan(ctx context.Context, first, n int, initial uint32) (hits, crossings, rejected uint32, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, err := c.status()
	if err != nil {
		return 0, 0, 0, err
	}
	if !m.Ready || !m.Frozen || m.Running || m.SamplesPerWord != 2 || m.Length == 0 {
		return 0, 0, 0, errors.New("sramcapture: stacking needs a frozen raw record")
	}
	for _, w := range []struct {
		sel uint16
		v   uint32
		two bool
	}{{selStackBins, uint32(n), false}, {selStackFirstBin, uint32(first), true}, {selStackInitial, initial, true}} { //nolint:gosec // G115: bounded by StackConfig.validate
		if w.two {
			err = c.write32(w.sel, w.v)
		} else {
			err = c.write(w.sel, uint16(w.v&0xffff))
		}
		if err != nil {
			return 0, 0, 0, err
		}
	}
	if err = c.stackGrant(ctx, true); err != nil {
		return 0, 0, 0, err
	}
	defer func() {
		if e := c.stackGrant(context.Background(), false); err == nil {
			err = e
		}
	}()
	if err = c.stackOp(ctx, stackOpScan, 5*time.Second); err != nil {
		return 0, 0, 0, err
	}
	if hits, err = c.read32(selStackPre); err != nil {
		return 0, 0, 0, err
	}
	if crossings, err = c.read32(selStackSep); err != nil {
		return 0, 0, 0, err
	}
	rejected, err = c.read32(selStackRejected)
	return hits, crossings, rejected, err
}

// StackDownload reads the current tile's first len(dst)/2 bins, two channels
// per bin, into dst.
// TRLC-LINKS: REQ-SDS-141
func (c *Capture) StackDownload(ctx context.Context, dst []superres.FPGAStackMoments) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(dst)%2 != 0 {
		return errors.New("sramcapture: stack download needs whole bins")
	}
	// A bus worker reads the tile's moments in one round trip; a moment
	// that reads back inconsistent is read again here, as below.
	var raw []byte
	if r, ok := c.bus.(isolatedStack); ok && len(dst) != 0 {
		raw = make([]byte, len(dst)*superres.FPGAStackWords*2)
		if owned, err := r.StackDownloadIsolated(len(dst), raw); !owned {
			raw = nil
		} else if err != nil {
			return err
		}
	}
	for i := range dst {
		if raw != nil {
			var words [superres.FPGAStackWords]uint16
			for k := range words {
				words[k] = binary.LittleEndian.Uint16(raw[(i*superres.FPGAStackWords+k)*2:])
			}
			if m, err := superres.DecodeFPGAMoments(words[:]); err == nil && m.CountOdd <= m.Count {
				dst[i] = m
				continue
			}
		}
		// A moment whose odd subset exceeds its total cannot come from the
		// accumulator: re-read it once, then report the transfer fault.
		var m superres.FPGAStackMoments
		var err error
		for range 2 {
			if m, err = c.stackDownload(ctx, i/2, i%2); err == nil && m.CountOdd > m.Count {
				err = fmt.Errorf("sramcapture: stack bin %d channel %d read back inconsistent (odd %d > %d)", i/2, i%2, m.CountOdd, m.Count)
			}
			if err == nil {
				break
			}
		}
		if err != nil {
			return err
		}
		dst[i] = m
	}
	return nil
}
