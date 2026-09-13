package sramcapture

import (
	"context"
	"encoding/binary"
	"fmt"
)

// VerifyCounter replaces volatile SRAM contents with a complete counter
// record and checks every recalled word. Call only before handing acquisition
// ownership to the engine; this is a startup transport check, not a live probe.
func (c *Capture) VerifyCounter(ctx context.Context) error {
	if err := c.Arm(ctx, Config{Source: Counter, PreWords: Words - 17, PostWords: 17}); err != nil {
		return err
	}
	if err := c.WaitFrozen(ctx); err != nil {
		return err
	}
	sink := &counterVerifier{}
	m, err := c.Status()
	if err != nil {
		return err
	}
	recall := c.Recall
	if m.Revision == 10 {
		recall = c.RecallForward
	}
	n, err := recall(ctx, 0, Words, sink)
	if err != nil {
		return err
	}
	if n != int64(Bytes) || sink.next != Words {
		return fmt.Errorf("sramcapture: short startup counter: %d bytes, %d words", n, sink.next)
	}
	return nil
}

type counterVerifier struct{ next uint32 }

func (s *counterVerifier) Write(b []byte) (int, error) {
	if len(b)%4 != 0 {
		return 0, fmt.Errorf("sramcapture: unaligned startup counter")
	}
	for i := 0; i < len(b); i += 4 {
		if got := binary.LittleEndian.Uint32(b[i:]); got != s.next {
			return i, fmt.Errorf("sramcapture: startup counter word %d is %d", s.next, got)
		}
		s.next++
	}
	return len(b), nil
}
