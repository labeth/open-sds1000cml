// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE

package sramcapture

import (
	"context"
	"errors"
	"io"
	"time"
)

// RecordIdentity associates one retained SRAM acquisition with a decoder epoch.
// It does not imply exact sample-position alignment of decoder events yet.
// TRLC-LINKS: REQ-SDS-013
type RecordIdentity struct {
	Epoch  uint32 `json:"epoch"`
	Record uint32 `json:"record"`
}

// DecodedCapture separates continuous decoded output from later raw retrieval.
// The acquisition owner must serialize StartDecodedCapture with other arm calls.
// Nothing here rearms the acquisition.
// TRLC-LINKS: REQ-SDS-013
type DecodedCapture struct {
	capture  *Capture
	Identity RecordIdentity
}

// SupportsDecodedCapture reports whether the image captures decoded events.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsDecodedCapture() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var v [2]uint16 // both capabilities in one batch
	err := c.readMany([]uint16{63, 73}, v[:])
	return v[0] == 0x4501 && v[1] == 0x5201 && err == nil, err
}

// StartDecodedCapture starts capturing the decoded events of cfg's decoder.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) StartDecodedCapture(ctx context.Context, cfg Config) (*DecodedCapture, error) {
	// The identity capability and the record now shown in one batch.
	var v [3]uint16
	c.mu.Lock()
	err := c.readMany([]uint16{73, 69, 70}, v[:])
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if v[0] != 0x5201 {
		return nil, errors.New("sramcapture: fabric lacks retained record identity")
	}
	oldID := uint32(v[1]) | uint32(v[2])<<16
	cfg.DecodedEvents = true
	if err := c.Arm(ctx, cfg); err != nil {
		return nil, err
	}
	var e [2]uint16
	c.mu.Lock()
	err = c.readMany([]uint16{61, 62}, e[:])
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	epoch := uint32(e[0]) | uint32(e[1])<<16
	// Command acknowledgement precedes priming and the actual SRAM arm.
	// Do not return the previous frozen record's identity during that interval.
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m, err := c.Status()
		if err != nil {
			return nil, err
		}
		if !m.HasRecordIdentity {
			return nil, errors.New("sramcapture: capture has no record identity")
		}
		if m.RecordID != oldID && m.EventEpoch == epoch {
			return &DecodedCapture{capture: c, Identity: RecordIdentity{Epoch: epoch, Record: m.RecordID}}, nil
		}
		timer := time.NewTimer(100 * time.Microsecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Recall verifies identity atomically with raw retrieval. Failed client writes
// leave SRAM frozen so the same record can be requested again.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-033
func (s *DecodedCapture) Recall(ctx context.Context, offset, count uint32, dst io.Writer) (int64, error) {
	return s.capture.recallChecked(ctx, offset, count, dst, false, &s.Identity)
}
