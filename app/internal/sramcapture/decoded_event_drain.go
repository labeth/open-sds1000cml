// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"fmt"
	"io"
	"time"
)

// DrainDecodedEvents writes complete ABI records until cancellation or error.
// It must be the sole reader for this epoch and start immediately after enable.
// FPGA loss records pass through unchanged; missing emitted sequence numbers are
// a transport error. Writer calls never hold the capture mutex. The caller owns
// writer deadlines: cancellation cannot interrupt an arbitrary blocked Write.
// This method neither rearms acquisition nor releases a retained SRAM record.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) DrainDecodedEvents(ctx context.Context, epoch uint32, dst io.Writer) (uint64, error) {
	var delivered uint64
	var sequence uint32
	for {
		if err := ctx.Err(); err != nil {
			return delivered, err
		}
		event, available, err := c.ReadDecodedEvent(epoch)
		if err != nil {
			return delivered, err
		}
		if !available {
			timer := time.NewTimer(time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return delivered, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if event.Sequence != sequence {
			return delivered, fmt.Errorf("sramcapture: decoded sequence gap: got %d expected %d", event.Sequence, sequence)
		}
		data, err := event.MarshalBinary()
		if err != nil {
			return delivered, err
		}
		n, err := dst.Write(data)
		if err != nil {
			return delivered, err
		}
		if n != len(data) {
			return delivered, io.ErrShortWrite
		}
		delivered++
		sequence++
	}
}
