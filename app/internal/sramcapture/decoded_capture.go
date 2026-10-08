// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"fmt"
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
// Streaming and WaitRetained can run concurrently. Neither automatically rearms.
// TRLC-LINKS: REQ-SDS-013
type DecodedCapture struct {
	capture  *Capture
	Identity RecordIdentity
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsDecodedCapture() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stream, err := c.read(63)
	if err != nil {
		return false, err
	}
	identity, err := c.read(73)
	return stream == 0x4501 && identity == 0x5201 && err == nil, err
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) StartDecodedCapture(ctx context.Context, cfg Config) (*DecodedCapture, error) {
	c.mu.Lock()
	capability, err := c.read(73)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if capability != 0x5201 {
		return nil, fmt.Errorf("sramcapture: fabric lacks retained record identity")
	}
	c.mu.Lock()
	oldID, err := c.read32(69)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	cfg.DecodedEvents = true
	if err := c.Arm(ctx, cfg); err != nil {
		return nil, err
	}
	c.mu.Lock()
	epoch, err := c.read32(61)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
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
			return nil, fmt.Errorf("sramcapture: capture has no record identity")
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

// Stream emits only decoded ABI records, including loss markers, until context
// cancellation or transport failure. The caller controls writer deadlines.
// TRLC-LINKS: REQ-SDS-013
func (s *DecodedCapture) Stream(ctx context.Context, dst io.Writer) (uint64, error) {
	return s.capture.DrainDecodedEvents(ctx, s.Identity.Epoch, dst)
}

// WaitRetained waits independently of the stream writer, then checks identity.
// It rejects a later acquisition rather than attributing its raw data to this one.
// TRLC-LINKS: REQ-SDS-013
func (s *DecodedCapture) WaitRetained(ctx context.Context) (Metadata, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Metadata{}, err
		}
		m, err := s.capture.Status()
		if err != nil {
			return m, err
		}
		if !m.HasRecordIdentity || m.EventEpoch != s.Identity.Epoch || m.RecordID != s.Identity.Record {
			return Metadata{}, fmt.Errorf("sramcapture: retained record identity changed")
		}
		if m.DataFault {
			return Metadata{}, fmt.Errorf("sramcapture: retained record has an acquisition data fault")
		}
		if m.Frozen && m.Ready && !m.Running {
			return m, nil
		}
		timer := time.NewTimer(100 * time.Microsecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Metadata{}, ctx.Err()
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
