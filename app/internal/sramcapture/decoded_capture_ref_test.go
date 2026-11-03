// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE

package sramcapture

import (
	"context"
	"errors"
	"time"
)

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
			return Metadata{}, errors.New("sramcapture: retained record identity changed")
		}
		if m.DataFault {
			return Metadata{}, errors.New("sramcapture: retained record has an acquisition data fault")
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
